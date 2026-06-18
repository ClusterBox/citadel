package webui

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/internal/logsdb"
)

func newTestServer(t *testing.T) (*Server, *logsdb.DB) {
	t.Helper()
	db, err := logsdb.Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv, err := New(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, db
}

func TestDashboardRendersWhenEmpty(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "no services registered") {
		t.Fatalf("expected empty-state message in body")
	}
}

func TestErrorsFragmentReflectsDB(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "s", Name: "s", Env: "dev", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})
	_, _ = db.InsertErrorEvent(ctx, logsdb.ErrorEvent{
		ServiceID: "s", TS: 1700000000000, Message: "boom",
		Status: sql.NullInt64{Int64: 500, Valid: true},
		Raw:    "raw",
		CWEventID: sql.NullString{String: "ev-1", Valid: true},
	})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/logs/errors?service=s")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "boom") || !strings.Contains(string(body), "500") {
		t.Fatalf("fragment missing data: %s", string(body))
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestBuildDashboardGroupsByName(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg-p", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-dev", Name: "smaug", Env: "dev", Region: "r", Runtime: "lambda", LogGroup: "lg-d", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "legolas-prod", Name: "legolas", Env: "prod", Region: "r", Runtime: "ecs", LogGroup: "lg-l", RepoPath: "/r"})

	view, err := srv.buildDashboard(ctx, "smaug")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Apps) != 2 {
		t.Fatalf("expected 2 app rail entries, got %d", len(view.Apps))
	}
	if view.Selected != "smaug" {
		t.Fatalf("expected Selected=smaug, got %q", view.Selected)
	}
	if len(view.Columns) != 2 {
		t.Fatalf("expected 2 env columns for smaug, got %d", len(view.Columns))
	}
	// alphabetical by env: dev before prod
	if view.Columns[0].Env != "dev" || view.Columns[1].Env != "prod" {
		t.Fatalf("columns not alphabetical by env: %q, %q", view.Columns[0].Env, view.Columns[1].Env)
	}
	if view.Columns[0].ID != "smaug-dev" || view.Columns[1].ID != "smaug-prod" {
		t.Fatalf("unexpected column IDs: %q, %q", view.Columns[0].ID, view.Columns[1].ID)
	}
}

func TestBuildDashboardDefaultsToFirstApp(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "aragorn-prod", Name: "aragorn", Env: "prod", Region: "r", Runtime: "ecs", LogGroup: "lg", RepoPath: "/r"})

	view, err := srv.buildDashboard(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// apps are ordered by name; aragorn sorts first
	if view.Selected != "aragorn" {
		t.Fatalf("expected default Selected=aragorn, got %q", view.Selected)
	}
	if len(view.Columns) != 1 || view.Columns[0].ID != "aragorn-prod" {
		t.Fatalf("expected single aragorn-prod column, got %#v", view.Columns)
	}
}

func TestBuildDashboardSingleEnvOneColumn(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})

	view, err := srv.buildDashboard(ctx, "smaug")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Columns) != 1 {
		t.Fatalf("expected 1 column for single-env app, got %d", len(view.Columns))
	}
	if len(view.Apps) != 1 || view.Apps[0].Envs != "prod" {
		t.Fatalf("unexpected app rail entry: %#v", view.Apps)
	}
}

func TestRailGroupsAppsByName(t *testing.T) {
	srv, db := newTestServer(t)
	ctx := context.Background()
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-prod", Name: "smaug", Env: "prod", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})
	_ = db.UpsertService(ctx, logsdb.Service{ID: "smaug-dev", Name: "smaug", Env: "dev", Region: "r", Runtime: "lambda", LogGroup: "lg", RepoPath: "/r"})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/logs/services?app=smaug")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	// one app link, not one per env
	if strings.Count(s, "/logs?app=smaug") != 1 {
		t.Fatalf("expected exactly one app link, body: %s", s)
	}
	if strings.Contains(s, "?service=") {
		t.Fatalf("rail should not link by service id, body: %s", s)
	}
	if !strings.Contains(s, "dev · prod") {
		t.Fatalf("expected env indicator 'dev · prod', body: %s", s)
	}
}
