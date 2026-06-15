package logsdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func mustOpen(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "logs.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAppliesSchema(t *testing.T) {
	db := mustOpen(t)
	if _, err := db.ExecContext(context.Background(), `INSERT INTO services (id,name,env,region,runtime,log_group,repo_path) VALUES (?,?,?,?,?,?,?)`,
		"s", "s", "dev", "us-east-1", "ecs", "/ecs/s", "/r"); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertAndListServices(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	s := Service{ID: "aragorn-dev", Name: "aragorn", Env: "dev", Region: "us-east-1", Runtime: "ecs", LogGroup: "/ecs/aragorn", RepoPath: "/repos/aragorn"}
	if err := db.UpsertService(ctx, s); err != nil {
		t.Fatal(err)
	}
	// Upserting again with a different log_group should overwrite.
	s.LogGroup = "/aws/ecs/aragorn"
	if err := db.UpsertService(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].LogGroup != "/aws/ecs/aragorn" {
		t.Fatalf("expected upserted log_group, got %+v", got)
	}
}

func TestErrorEventDedupeViaCWEventID(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, Service{ID: "s", Name: "s", Env: "dev", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})
	e := ErrorEvent{
		ServiceID: "s",
		TS:        1000,
		Message:   "boom",
		Raw:       "raw line",
		CWEventID: sql.NullString{String: "cw-1", Valid: true},
	}
	inserted, err := db.InsertErrorEvent(ctx, e)
	if err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}
	inserted, err = db.InsertErrorEvent(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("expected dedupe to suppress second insert")
	}
	got, err := db.RecentErrors(ctx, "s", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row after dedupe, got %d", len(got))
	}
}

func TestCursorLoadSave(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, Service{ID: "s", Name: "s", Env: "dev", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})
	_, ok, err := db.LoadCursor(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no cursor initially")
	}
	if err := db.SaveCursor(ctx, "s", 12345); err != nil {
		t.Fatal(err)
	}
	ts, ok, err := db.LoadCursor(ctx, "s")
	if err != nil || !ok || ts != 12345 {
		t.Fatalf("expected 12345/true, got %d/%v err=%v", ts, ok, err)
	}
}

func TestSweepRemovesOldEvents(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, Service{ID: "s", Name: "s", Env: "dev", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})
	for i, ts := range []int64{100, 200, 300, 400} {
		_, _ = db.InsertErrorEvent(ctx, ErrorEvent{
			ServiceID: "s", TS: ts, Message: "m", Raw: "r",
			CWEventID: sql.NullString{String: cwid(i), Valid: true},
		})
	}
	n, err := db.Sweep(ctx, 250)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected sweep to delete 2, got %d", n)
	}
	got, _ := db.RecentErrors(ctx, "s", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 events left, got %d", len(got))
	}
}

func TestPruneServicesCascadesErrors(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	for _, id := range []string{"keep", "drop"} {
		_ = db.UpsertService(ctx, Service{ID: id, Name: id, Env: "dev", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})
		_, _ = db.InsertErrorEvent(ctx, ErrorEvent{
			ServiceID: id, TS: 1, Message: "m", Raw: "r",
			CWEventID: sql.NullString{String: id + "-e", Valid: true},
		})
	}
	if err := db.PruneServices(ctx, []string{"keep"}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.ListServices(ctx)
	if len(got) != 1 || got[0].ID != "keep" {
		t.Fatalf("expected only 'keep' left, got %+v", got)
	}
	dropEvts, _ := db.RecentErrors(ctx, "drop", 10)
	if len(dropEvts) != 0 {
		t.Fatalf("expected dropped events cascaded, got %d", len(dropEvts))
	}
}

func TestCountByService(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		_ = db.UpsertService(ctx, Service{ID: id, Name: id, Env: "dev", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})
	}
	_, _ = db.InsertErrorEvent(ctx, ErrorEvent{ServiceID: "a", TS: 1000, Message: "m", Raw: "r", CWEventID: sql.NullString{String: "a1", Valid: true}})
	_, _ = db.InsertErrorEvent(ctx, ErrorEvent{ServiceID: "a", TS: 2000, Message: "m", Raw: "r", CWEventID: sql.NullString{String: "a2", Valid: true}})
	_, _ = db.InsertErrorEvent(ctx, ErrorEvent{ServiceID: "b", TS: 50, Message: "m", Raw: "r", CWEventID: sql.NullString{String: "b1", Valid: true}})

	counts, err := db.CountByService(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if counts["a"] != 2 || counts["b"] != 0 {
		t.Fatalf("counts wrong: %+v", counts)
	}
}

func cwid(i int) string { return string(rune('a' + i)) }
