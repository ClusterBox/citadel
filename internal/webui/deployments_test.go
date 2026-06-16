package webui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/internal/deploydb"
)

func seededDeployDB(t *testing.T) *deploydb.DB {
	t.Helper()
	db, err := deploydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	id, _ := db.Insert(context.Background(), deploydb.Deployment{
		Project: "smaug", Env: "dev", Runtime: "lambda", Region: "us-east-1",
		GitSHA: "a1b2c3d", ImageURI: "uri", Message: "fix reminder scan",
		DeployedBy: "alice", Target: "smaug-dev",
	})
	_ = db.MarkSuccess(context.Background(), id)
	return db
}

func TestDeployServerRendersRows(t *testing.T) {
	srv, err := NewDeployServer(seededDeployDB(t))
	if err != nil {
		t.Fatalf("NewDeployServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/deployments")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "smaug") || !strings.Contains(body, "fix reminder scan") {
		t.Errorf("expected seeded deploy in page, got:\n%s", body)
	}
	if !strings.Contains(body, "status-success") {
		t.Errorf("expected status class in page")
	}
}
