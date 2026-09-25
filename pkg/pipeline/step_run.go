package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ClusterBox/citadel/pkg/config"
)

// runStep runs a shell command where citadel runs (laptop or CI runner).
type runStep struct {
	name      string
	cmd       string
	shell     []string
	dir       string
	env       map[string]string
	timeout   time.Duration
	killGrace time.Duration
}

func newRunStep(s config.PipelineStep) *runStep {
	return &runStep{
		name: s.StepName(), cmd: s.Run, shell: s.ShellOrDefault(), dir: s.WorkingDirectory,
		env: s.Env, timeout: s.TimeoutOrDefault(), killGrace: 10 * time.Second,
	}
}

func (s *runStep) Name() string { return s.name }

func (s *runStep) Plan(context.Context, *StepContext, io.Writer) (bool, error) { return false, nil }

func (s *runStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	cmdText, err := config.ExpandVars(s.cmd, sc.Vars)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "▶️  %s: %s\n", s.name, cmdText)
	if sc.Opts.DryRun {
		fmt.Fprintf(w, "   [dry-run] Would run: %s\n", cmdText)
		return nil
	}

	// R3: working_directory may reference ${var}s too (config validation
	// checks it), so expand it before joining with the config dir.
	dir, err := config.ExpandVars(s.dir, sc.Vars)
	if err != nil {
		return err
	}

	// CITADEL_* vars must win on conflict (spec §3 order), so the step's
	// env: is appended first and citadelEnv last: exec.Cmd uses the last
	// value for a duplicate key.
	env := os.Environ()
	for k, v := range s.env {
		expanded, err := config.ExpandVars(v, sc.Vars)
		if err != nil {
			return err
		}
		env = append(env, k+"="+expanded)
	}
	for k, v := range citadelEnv(sc.Vars) {
		env = append(env, k+"="+v)
	}

	args := append(append([]string{}, s.shell[1:]...), cmdText)
	cmd := exec.Command(s.shell[0], args...)
	cmd.Dir = filepath.Join(filepath.Dir(sc.Opts.ConfigPath), dir)
	cmd.Env = env
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		s.terminate(cmd, done)
		return fmt.Errorf("timed out after %s", s.timeout)
	case <-ctx.Done():
		s.terminate(cmd, done)
		return ctx.Err()
	}
}

// terminate stops the whole process group: SIGTERM, then SIGKILL after the
// grace period, so background children never outlive the step.
func (s *runStep) terminate(cmd *exec.Cmd, done <-chan error) {
	pgid := -cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(s.killGrace):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-done
	}
	_ = syscall.Kill(pgid, syscall.SIGKILL) // reap stragglers that ignored SIGTERM
}

// citadelEnv exposes the pipeline variables to run steps as CITADEL_* vars.
func citadelEnv(vars map[string]string) map[string]string {
	return map[string]string{
		"CITADEL_ENV": vars["env"], "CITADEL_NAME": vars["name"], "CITADEL_IMAGE": vars["image"],
		"CITADEL_SHA": vars["sha"], "CITADEL_REGION": vars["region"], "CITADEL_ACCOUNT": vars["account"],
	}
}
