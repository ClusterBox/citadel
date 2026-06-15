package ingest

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	cwaws "github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/internal/ingest/parsers"
	"github.com/ClusterBox/citadel/internal/logsdb"
	"github.com/ClusterBox/citadel/pkg/config"
)

type fakeLogsClient struct {
	pages []*cwaws.FilterEventsPage
	calls int
}

func (f *fakeLogsClient) FilterEvents(ctx context.Context, lg string, start, end int64, limit int32, token *string) (*cwaws.FilterEventsPage, error) {
	if f.calls >= len(f.pages) {
		return &cwaws.FilterEventsPage{}, nil
	}
	p := f.pages[f.calls]
	f.calls++
	return p, nil
}

func mkEvent(msg, id string, ts int64) cwtypes.FilteredLogEvent {
	return cwtypes.FilteredLogEvent{
		Message:       aws.String(msg),
		EventId:       aws.String(id),
		Timestamp:     aws.Int64(ts),
		LogStreamName: aws.String("stream-1"),
	}
}

func TestPollPersistsErrorsAndAdvancesCursor(t *testing.T) {
	db, err := logsdb.Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{
		ID: "aragorn-dev", Name: "aragorn", Env: "dev", Region: "us-east-1",
		Runtime: string(config.RuntimeECS), LogGroup: "/ecs/aragorn", RepoPath: "/repos/aragorn",
	})

	client := &fakeLogsClient{
		pages: []*cwaws.FilterEventsPage{
			{Events: []cwtypes.FilteredLogEvent{
				mkEvent(`{"level":"error","statusCode":500,"message":"boom","requestId":"r1"}`, "ev-1", 1000),
				mkEvent(`{"level":"info","message":"ok"}`, "ev-2", 1500),
			}},
		},
	}

	r := NewRunner(db, func(ctx context.Context, region string) (AWSLogsClient, error) {
		return client, nil
	}, slog.Default())

	target := Target{ID: "aragorn-dev", Region: "us-east-1", Runtime: config.RuntimeECS, LogGroup: "/ecs/aragorn"}
	parser, _ := parsers.ForRuntime(target.Runtime)
	if err := r.poll(ctx, target, client, parser, slog.Default()); err != nil {
		t.Fatal(err)
	}

	events, err := db.RecentErrors(ctx, "aragorn-dev", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stored error event, got %d", len(events))
	}
	if events[0].Status.Int64 != 500 {
		t.Fatalf("expected status 500, got %d", events[0].Status.Int64)
	}

	cursor, ok, err := db.LoadCursor(ctx, "aragorn-dev")
	if err != nil || !ok {
		t.Fatalf("cursor not saved: ok=%v err=%v", ok, err)
	}
	// Cursor must advance past at least the event timestamps we observed.
	if cursor <= 1500 {
		t.Fatalf("expected cursor > 1500, got %d", cursor)
	}
}

func TestPollDedupesOnRerun(t *testing.T) {
	db, err := logsdb.Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{
		ID: "s", Name: "s", Env: "dev", Region: "us-east-1",
		Runtime: string(config.RuntimeECS), LogGroup: "lg", RepoPath: "/r",
	})

	page := &cwaws.FilterEventsPage{Events: []cwtypes.FilteredLogEvent{
		mkEvent(`{"level":"error","statusCode":500,"message":"boom"}`, "ev-1", 1000),
	}}
	client := &fakeLogsClient{pages: []*cwaws.FilterEventsPage{page, page}}
	parser, _ := parsers.ForRuntime(config.RuntimeECS)
	r := NewRunner(db, nil, slog.Default())
	target := Target{ID: "s", Region: "us-east-1", Runtime: config.RuntimeECS, LogGroup: "lg"}

	if err := r.poll(ctx, target, client, parser, slog.Default()); err != nil {
		t.Fatal(err)
	}
	// Reset cursor to force re-polling the same window.
	_ = db.SaveCursor(ctx, "s", 0)
	if err := r.poll(ctx, target, client, parser, slog.Default()); err != nil {
		t.Fatal(err)
	}

	events, _ := db.RecentErrors(ctx, "s", 10)
	if len(events) != 1 {
		t.Fatalf("expected dedupe to keep 1 event, got %d", len(events))
	}
}
