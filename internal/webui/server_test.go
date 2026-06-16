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
