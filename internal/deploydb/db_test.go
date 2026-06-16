package deploydb

import (
	"context"
	"testing"
)

func mustOpen(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sample() Deployment {
	return Deployment{
		Project: "smaug", Env: "dev", Runtime: "lambda", Region: "us-east-1",
		GitSHA: "a1b2c3d", ImageURI: "123.dkr.ecr/smaug-repo:a1b2c3d",
		Message: "fix reminder scan", DeployedBy: "alice", Target: "smaug-dev",
	}
}

func TestInsertAndMarkSuccess(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()

	id, err := db.Insert(ctx, sample())
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	if err := db.MarkSuccess(ctx, id); err != nil {
		t.Fatalf("mark success: %v", err)
	}

	rows, err := db.List(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Status != "success" {
		t.Errorf("status = %q, want success", rows[0].Status)
	}
	if rows[0].FinishedAt == nil || rows[0].DurationMS == nil {
		t.Error("expected finished_at and duration_ms to be set")
	}
}

func TestMarkFailed(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	id, _ := db.Insert(ctx, sample())
	if err := db.MarkFailed(ctx, id, "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	rows, _ := db.List(ctx, Filter{Limit: 10})
	if rows[0].Status != "failed" || rows[0].Error != "boom" {
		t.Errorf("got status=%q error=%q, want failed/boom", rows[0].Status, rows[0].Error)
	}
}

func TestListFilters(t *testing.T) {
	db := mustOpen(t)
	ctx := context.Background()
	a := sample() // smaug/dev
	b := sample()
	b.Project = "legolas"
	b.Env = "prod"
	db.Insert(ctx, a)
	db.Insert(ctx, b)

	rows, _ := db.List(ctx, Filter{Project: "legolas", Limit: 10})
	if len(rows) != 1 || rows[0].Project != "legolas" {
		t.Fatalf("project filter failed: %+v", rows)
	}
	rows, _ = db.List(ctx, Filter{Env: "dev", Limit: 10})
	if len(rows) != 1 || rows[0].Env != "dev" {
		t.Fatalf("env filter failed: %+v", rows)
	}
}
