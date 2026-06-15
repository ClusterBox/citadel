// citadel-logs is the always-on daemon that watches CloudWatch log groups for
// every registered clusterbox service and surfaces 500-class events at
// http://localhost:5500/logs.
//
// It is a separate binary from `citadel` to keep the deploy CLI lean. See
// docs/superpowers/specs/2026-06-09-citadel-logs-daemon-design.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	cwaws "github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/internal/ingest"
	"github.com/ClusterBox/citadel/internal/logsdb"
	"github.com/ClusterBox/citadel/internal/registry"
	"github.com/ClusterBox/citadel/internal/webui"
	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/fsnotify/fsnotify"
)

const (
	defaultRegistryPath = "/etc/citadel/registry.yml"
	defaultDBPath       = "/data/citadel-logs.db"
	defaultListenAddr   = ":5500"
	// retentionWindow is how long error_events are kept before sweeping.
	retentionWindow = 7 * 24 * time.Hour
	sweepInterval   = 1 * time.Hour
)

func main() {
	registryPath := flag.String("registry", envOr("CITADEL_LOGS_REGISTRY", defaultRegistryPath), "path to registry.yml")
	dbPath := flag.String("db", envOr("CITADEL_LOGS_DB", defaultDBPath), "path to SQLite db file")
	addr := flag.String("addr", envOr("CITADEL_LOGS_ADDR", defaultListenAddr), "HTTP listen address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger, *registryPath, *dbPath, *addr); err != nil {
		logger.Error("daemon exited", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(logger *slog.Logger, registryPath, dbPath, addr string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	db, err := logsdb.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	factory := awsLogsClientFactory()
	runner := ingest.NewRunner(db, factory, logger)
	defer runner.Stop()

	if err := reloadRegistry(ctx, logger, db, runner, registryPath); err != nil {
		logger.Warn("initial registry load had errors", "err", err)
	}

	go watchRegistry(ctx, logger, db, runner, registryPath)
	go retentionSweeper(ctx, logger, db)

	srv, err := webui.New(db, factory)
	if err != nil {
		return fmt.Errorf("build webui: %w", err)
	}
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logger.Info("citadel-logs listening", "addr", addr, "registry", registryPath, "db", dbPath)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// awsLogsClientFactory returns a LogsClientFactory that builds one
// internal/aws.Client per region and reuses it.
func awsLogsClientFactory() ingest.LogsClientFactory {
	var mu sync.Mutex
	cache := map[string]*cwaws.LogsClient{}
	return func(ctx context.Context, region string) (ingest.AWSLogsClient, error) {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := cache[region]; ok {
			return c, nil
		}
		client, err := cwaws.NewClient(ctx, region)
		if err != nil {
			return nil, err
		}
		lc := client.NewLogsClient()
		cache[region] = lc
		return lc, nil
	}
}

func reloadRegistry(ctx context.Context, logger *slog.Logger, db *logsdb.DB, runner *ingest.Runner, path string) error {
	services, errs := registry.Resolve(path)
	if len(errs) > 0 {
		for _, e := range errs {
			logger.Warn("registry entry skipped", "err", e)
		}
	}
	if len(services) == 0 {
		logger.Info("no services registered", "registry", path)
		_ = db.PruneServices(ctx, nil)
		runner.Sync(ctx, nil)
		return nil
	}

	var targets []ingest.Target
	var keepIDs []string
	for _, svc := range services {
		logGroup, err := resolveLogGroup(ctx, svc)
		if err != nil {
			logger.Warn("could not resolve log group", "service", svc.ID, "err", err)
			continue
		}
		if err := db.UpsertService(ctx, logsdb.Service{
			ID: svc.ID, Name: svc.Name, Env: svc.Env, Region: svc.Region,
			Runtime: string(svc.Runtime), LogGroup: logGroup, RepoPath: svc.RepoPath,
		}); err != nil {
			logger.Warn("upsert service", "service", svc.ID, "err", err)
			continue
		}
		keepIDs = append(keepIDs, svc.ID)
		targets = append(targets, ingest.Target{
			ID: svc.ID, Region: svc.Region, Runtime: svc.Runtime, LogGroup: logGroup,
		})
	}
	if err := db.PruneServices(ctx, keepIDs); err != nil {
		logger.Warn("prune services", "err", err)
	}
	runner.Sync(ctx, targets)
	logger.Info("registry reloaded", "services", len(targets))
	return nil
}

// resolveLogGroup turns a registry.Service into an actual CloudWatch log group
// name, using the right AWS API per runtime. ECS resolution leans on the same
// task-definition introspection used by `citadel logs`.
func resolveLogGroup(ctx context.Context, svc registry.Service) (string, error) {
	awsClient, err := cwaws.NewClient(ctx, svc.Region)
	if err != nil {
		return "", err
	}
	switch svc.Runtime {
	case config.RuntimeECS:
		// DiscoverLogGroup needs Name to derive cluster/service convention.
		// Other fields are unused by that code path.
		cfg := &config.DeployConfig{Name: svc.Name, Region: svc.Region}
		return awsClient.NewECSClient().DiscoverLogGroup(ctx, cfg)
	case config.RuntimeLambda:
		return awsClient.NewLambdaClient().ResolveLogGroup(ctx, svc.LambdaFunction)
	default:
		return "", fmt.Errorf("unknown runtime %q", svc.Runtime)
	}
}

func watchRegistry(ctx context.Context, logger *slog.Logger, db *logsdb.DB, runner *ingest.Runner, path string) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("fsnotify unavailable, hot reload disabled", "err", err)
		return
	}
	defer w.Close()
	if err := w.Add(path); err != nil {
		logger.Warn("watch registry file failed", "path", path, "err", err)
		return
	}
	// Debounce bursts of events from editors that write atomically.
	var debounce *time.Timer
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				// Editors that save atomically (write tmp + rename) replace
				// the inode, so the watch silently goes dead. Re-add the
				// path on every reload to keep observing the new file.
				_ = w.Remove(path)
				if err := w.Add(path); err != nil {
					logger.Warn("re-add registry watch failed", "path", path, "err", err)
				}
				if err := reloadRegistry(ctx, logger, db, runner, path); err != nil {
					logger.Warn("hot reload failed", "err", err)
				}
			})
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			logger.Warn("fsnotify error", "err", err)
		}
	}
}

func retentionSweeper(ctx context.Context, logger *slog.Logger, db *logsdb.DB) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	sweep := func() {
		cutoff := time.Now().Add(-retentionWindow).UnixMilli()
		n, err := db.Sweep(ctx, cutoff)
		if err != nil {
			logger.Warn("retention sweep failed", "err", err)
			return
		}
		if n > 0 {
			logger.Info("retention sweep", "deleted", n)
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}
