package pipeline

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClusterBox/citadel/pkg/config"
)

func noSleep(context.Context, time.Duration) error { return nil }

func TestHTTPStep_SucceedsAfterRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("secret body"))
	}))
	defer srv.Close()
	s := newHTTPStep(config.PipelineStep{Name: "smoke", HTTP: srv.URL + "/${env}", Retries: intp(5)})
	s.sleep = noSleep
	var out bytes.Buffer
	if err := s.Run(context.Background(), runCtx(t), &out); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 || !strings.Contains(out.String(), "attempt 1/5: 503") || !strings.Contains(out.String(), "attempt 3/5: 200") {
		t.Fatalf("hits=%d out=%q", hits.Load(), out.String())
	}
	if strings.Contains(out.String(), "secret body") {
		t.Fatal("response bodies must never be printed")
	}
}

func TestHTTPStep_FailsAfterAllAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	s := newHTTPStep(config.PipelineStep{Name: "smoke", HTTP: srv.URL, Retries: intp(2)})
	s.sleep = noSleep
	err := s.Run(context.Background(), runCtx(t), &bytes.Buffer{})
	if err == nil || err.Error() != "health check failed after 2 attempts (last: 500)" {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPStep_ExpectStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	s := newHTTPStep(config.PipelineStep{Name: "smoke", HTTP: srv.URL, ExpectStatus: intp(204), Retries: intp(1)})
	if err := s.Run(context.Background(), runCtx(t), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPStep_ConnectionErrorIsReported(t *testing.T) {
	s := newHTTPStep(config.PipelineStep{Name: "smoke", HTTP: "http://127.0.0.1:1/health", Retries: intp(1), RequestTimeout: "200ms"})
	err := s.Run(context.Background(), runCtx(t), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "health check failed after 1 attempts (last:") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPStep_DryRun(t *testing.T) {
	sc := runCtx(t)
	sc.Opts.DryRun = true
	var out bytes.Buffer
	if err := newHTTPStep(config.PipelineStep{Name: "smoke", HTTP: "https://api-${env}.example.com"}).Run(context.Background(), sc, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[dry-run] Would check: https://api-dev.example.com") {
		t.Fatalf("out = %q", out.String())
	}
}

func intp(n int) *int { return &n }
