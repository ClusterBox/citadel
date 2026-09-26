package pipeline

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/ClusterBox/citadel/pkg/config"
)

// httpStep is a post-rollout health check: GET until the expected status.
type httpStep struct {
	name       string
	url        string
	expect     int
	retries    int
	interval   time.Duration
	reqTimeout time.Duration
	client     *http.Client
	sleep      func(ctx context.Context, d time.Duration) error
}

func newHTTPStep(s config.PipelineStep) *httpStep {
	return &httpStep{
		name: s.StepName(), url: s.HTTP, expect: s.ExpectStatusOrDefault(), retries: s.RetriesOrDefault(),
		interval: s.IntervalOrDefault(), reqTimeout: s.RequestTimeoutOrDefault(),
		client: &http.Client{}, sleep: sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (s *httpStep) Name() string { return s.name }

func (s *httpStep) Plan(context.Context, *StepContext, io.Writer) (bool, error) { return false, nil }

func (s *httpStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	url, err := config.ExpandVars(s.url, sc.Vars)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "▶️  %s: GET %s (expect %d)\n", s.name, url, s.expect)
	if sc.Opts.DryRun {
		fmt.Fprintf(w, "   [dry-run] Would check: %s\n", url)
		return nil
	}
	last := ""
	for i := 1; i <= s.retries; i++ {
		status, err := s.attempt(ctx, url)
		switch {
		case err != nil:
			last = err.Error()
		case status == s.expect:
			fmt.Fprintf(w, "   attempt %d/%d: %d\n", i, s.retries, status)
			return nil
		default:
			last = strconv.Itoa(status)
		}
		fmt.Fprintf(w, "   attempt %d/%d: %s\n", i, s.retries, last)
		if i < s.retries {
			if err := s.sleep(ctx, s.interval); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("health check failed after %d attempts (last: %s)", s.retries, last)
}

func (s *httpStep) attempt(ctx context.Context, url string) (int, error) {
	rctx, cancel := context.WithTimeout(ctx, s.reqTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	return resp.StatusCode, nil
}
