package pipeline

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ClusterBox/citadel/pkg/config"
)

func runCtx(t *testing.T) *StepContext {
	t.Helper()
	dir := t.TempDir()
	return &StepContext{
		Opts: &DeployOptions{ConfigPath: filepath.Join(dir, "citadel.yml")},
		Env:  "dev",
		Vars: map[string]string{"env": "dev", "name": "demo", "image": "r:abc", "sha": "abc", "region": "us-east-1", "account": "111"},
	}
}

func TestRunStep_EnvVarsAndWorkingDirectory(t *testing.T) {
	sc := runCtx(t)
	sub := filepath.Join(filepath.Dir(sc.Opts.ConfigPath), "svc")
	os.Mkdir(sub, 0o755)
	s := newRunStep(config.PipelineStep{Name: "t", Run: `echo "$PWD|${env}|$CITADEL_IMAGE|$WHO|${HOME:+home}"`,
		WorkingDirectory: "svc", Env: map[string]string{"WHO": "${name}"}})
	var out bytes.Buffer
	if err := s.Run(context.Background(), sc, &out); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasSuffix(got, "svc|dev|r:abc|demo|home") {
		t.Fatalf("out = %q", got)
	}
}

// TestRunStep_WorkingDirectoryExpandsVars covers controller ruling R3: config
// validation checks ${var} in working_directory, so the run step must expand
// it too (not just join it raw with the config dir).
func TestRunStep_WorkingDirectoryExpandsVars(t *testing.T) {
	sc := runCtx(t)
	sub := filepath.Join(filepath.Dir(sc.Opts.ConfigPath), "svc-dev")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newRunStep(config.PipelineStep{Name: "t", Run: `echo "$PWD"`, WorkingDirectory: "svc-${env}"})
	var out bytes.Buffer
	if err := s.Run(context.Background(), sc, &out); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasSuffix(got, "svc-dev") {
		t.Fatalf("out = %q, want working directory to resolve to svc-dev", got)
	}
}

func TestRunStep_NonZeroExit(t *testing.T) {
	err := newRunStep(config.PipelineStep{Name: "t", Run: "exit 3"}).Run(context.Background(), runCtx(t), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunStep_TimeoutKillsProcessGroup(t *testing.T) {
	sc := runCtx(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := newRunStep(config.PipelineStep{Name: "t", Timeout: "200ms",
		Run: `sleep 600 & echo $! > ` + pidFile + `; wait`})
	s.killGrace = 100 * time.Millisecond
	err := s.Run(context.Background(), sc, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("err = %v", err)
	}
	data, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("background child %d survived the timeout", pid)
	}
}

func TestRunStep_CancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newRunStep(config.PipelineStep{Name: "t", Run: "sleep 600"})
	s.killGrace = 100 * time.Millisecond
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if err := s.Run(ctx, runCtx(t), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunStep_DryRunDoesNotExecute(t *testing.T) {
	sc := runCtx(t)
	sc.Opts.DryRun = true
	marker := filepath.Join(t.TempDir(), "ran")
	var out bytes.Buffer
	if err := newRunStep(config.PipelineStep{Name: "t", Run: "touch " + marker + " ${env}"}).Run(context.Background(), sc, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("dry run executed the command")
	}
	if !strings.Contains(out.String(), "[dry-run] Would run: touch "+marker+" dev") {
		t.Fatalf("out = %q", out.String())
	}
}
