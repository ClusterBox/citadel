package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	cwaws "github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/internal/ingest/parsers"
	"github.com/ClusterBox/citadel/internal/logsdb"
	"github.com/ClusterBox/citadel/pkg/config"
)

const (
	pollInterval = 10 * time.Second
	// startBacklog is how far back to start polling when there's no cursor.
	startBacklog = 10 * time.Minute
	pageLimit    = int32(1000)
	// ingestLag accounts for CloudWatch Logs' indexing delay. We never query
	// closer to "now" than this, so the cursor doesn't advance past events
	// that exist in the log group but aren't yet returned by FilterLogEvents.
	// Picked conservatively: most CW Logs writes are visible within ~10s, but
	// agent/SDK retry paths can push tail-end delivery to 30–60s.
	ingestLag = 1 * time.Minute
)

// Target identifies a service for the ingest loop.
type Target struct {
	ID       string
	Region   string
	Runtime  config.Runtime
	LogGroup string
}

// AWSLogsClient is the subset of internal/aws.LogsClient that ingest uses.
// Defined here so tests can inject a fake without spinning up a real AWS SDK.
type AWSLogsClient interface {
	FilterEvents(ctx context.Context, logGroup string, startMs, endMs int64, limit int32, nextToken *string) (*cwaws.FilterEventsPage, error)
}

// LogsClientFactory builds an AWSLogsClient for a given region. The runner
// caches one per region.
type LogsClientFactory func(ctx context.Context, region string) (AWSLogsClient, error)

// Runner owns the per-service goroutines. Add/Remove are safe to call after
// Start to support registry hot-reload.
type Runner struct {
	db          *logsdb.DB
	factory     LogsClientFactory
	log         *slog.Logger
	clientCache sync.Map // region -> AWSLogsClient

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewRunner wires the runner.
func NewRunner(db *logsdb.DB, factory LogsClientFactory, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		db:      db,
		factory: factory,
		log:     logger,
		cancels: map[string]context.CancelFunc{},
	}
}

// Sync brings the running set in line with targets, starting/stopping
// goroutines as needed. Safe to call repeatedly on registry reload.
func (r *Runner) Sync(ctx context.Context, targets []Target) {
	want := map[string]Target{}
	for _, t := range targets {
		want[t.ID] = t
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, cancel := range r.cancels {
		if _, ok := want[id]; !ok {
			cancel()
			delete(r.cancels, id)
		}
	}
	for id, t := range want {
		if _, ok := r.cancels[id]; ok {
			continue
		}
		childCtx, cancel := context.WithCancel(ctx)
		r.cancels[id] = cancel
		go r.runService(childCtx, t)
	}
}

// Stop cancels every running goroutine.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cancel := range r.cancels {
		cancel()
	}
	r.cancels = map[string]context.CancelFunc{}
}

func (r *Runner) clientFor(ctx context.Context, region string) (AWSLogsClient, error) {
	if v, ok := r.clientCache.Load(region); ok {
		return v.(AWSLogsClient), nil
	}
	c, err := r.factory(ctx, region)
	if err != nil {
		return nil, err
	}
	r.clientCache.Store(region, c)
	return c, nil
}

func (r *Runner) runService(ctx context.Context, t Target) {
	logger := r.log.With("service", t.ID, "log_group", t.LogGroup)
	parser, err := parsers.ForRuntime(t.Runtime)
	if err != nil {
		logger.Error("no parser for runtime, service ignored", "runtime", t.Runtime, "err", err)
		return
	}
	client, err := r.clientFor(ctx, t.Region)
	if err != nil {
		logger.Error("failed to build aws client", "err", err)
		return
	}

	// Stagger first tick so all N services don't hit AWS simultaneously.
	stagger := time.Duration(hash32(t.ID)%uint32(pollInterval/time.Second)) * time.Second
	select {
	case <-time.After(stagger):
	case <-ctx.Done():
		return
	}

	backoff := pollInterval
	const maxBackoff = 5 * time.Minute

	for {
		if err := r.poll(ctx, t, client, parser, logger); err != nil {
			logger.Warn("poll failed, backing off", "err", err, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = pollInterval
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runner) poll(ctx context.Context, t Target, client AWSLogsClient, parser parsers.Parser, logger *slog.Logger) error {
	cursor, ok, err := r.db.LoadCursor(ctx, t.ID)
	if err != nil {
		return fmt.Errorf("load cursor: %w", err)
	}
	if !ok {
		cursor = time.Now().Add(-startBacklog).UnixMilli()
	}
	end := time.Now().Add(-ingestLag).UnixMilli()
	if end <= cursor {
		return nil
	}

	var nextToken *string
	maxTS := cursor
	pages := 0
	const pageCap = 20 // safety: at most 20 pages per tick

	for pages < pageCap {
		page, err := client.FilterEvents(ctx, t.LogGroup, cursor, end, pageLimit, nextToken)
		if err != nil {
			return err
		}
		for _, e := range page.Events {
			ev, matched := parser.Parse(e)
			if !matched {
				if e.Timestamp != nil && *e.Timestamp > maxTS {
					maxTS = *e.Timestamp
				}
				continue
			}
			row := logsdb.ErrorEvent{
				ServiceID: t.ID,
				TS:        ev.TS,
				Message:   ev.Message,
				Raw:       ev.Raw,
				Status:    nullInt(ev.Status),
				Level:     nullString(ev.Level),
				RequestID: nullString(ev.RequestID),
				Stack:     nullString(ev.Stack),
				LogStream: nullString(ev.LogStream),
				CWEventID: nullString(ev.CWEventID),
			}
			if _, err := r.db.InsertErrorEvent(ctx, row); err != nil {
				logger.Warn("insert error event failed", "err", err)
				continue
			}
			if ev.TS > maxTS {
				maxTS = ev.TS
			}
		}
		pages++
		if page.NextToken == nil {
			break
		}
		nextToken = page.NextToken
	}

	// Advance cursor to the latest fully-scanned point. If events were seen,
	// max(event.ts)+1 avoids re-fetching them; otherwise `end` ensures we don't
	// keep re-scanning an empty window forever.
	next := end
	if maxTS > cursor && maxTS+1 < end {
		next = maxTS + 1
	}
	if err := r.db.SaveCursor(ctx, t.ID, next); err != nil {
		return fmt.Errorf("save cursor: %w", err)
	}
	return nil
}

func nullInt(n int) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(n), Valid: true}
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func hash32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// ErrNoTargets is reserved for future use when the daemon has no registered
// services.
var ErrNoTargets = errors.New("no ingest targets registered")
