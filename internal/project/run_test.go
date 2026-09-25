package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// tickClock advances one second per call so step durations are non-zero.
func tickClock(start time.Time) func() time.Time {
	t := start
	return func() time.Time { t = t.Add(time.Second); return t }
}

func readRunFile(t *testing.T, r *Run) runFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Dir(), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rf runFile
	if err := json.Unmarshal(data, &rf); err != nil {
		t.Fatal(err)
	}
	return rf
}

func newTestRun(t *testing.T, now func() time.Time) (*Dir, *Run, *bytes.Buffer) {
	t.Helper()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var term bytes.Buffer
	r, err := d.newRun(RunInfo{Env: "dev", Message: "ship it", GitSHA: "abc1234"}, &term, now)
	if err != nil {
		t.Fatal(err)
	}
	return d, r, &term
}

func TestNewRun_IDAndInitialRunJSON(t *testing.T) {
	d, r, _ := newTestRun(t, fixedClock(testNow))
	if !strings.HasPrefix(r.ID(), "20260925T100000Z-") || len(r.ID()) != len("20260925T100000Z-a1b2") {
		t.Fatalf("ID = %q", r.ID())
	}
	if r.Dir() != filepath.Join(d.Path(), "runs", r.ID()) {
		t.Fatalf("Dir = %q", r.Dir())
	}
	rf := readRunFile(t, r)
	if rf.Status != StatusInProgress || rf.Env != "dev" || rf.Message != "ship it" || rf.GitSHA != "abc1234" {
		t.Fatalf("run.json = %+v", rf)
	}
	if rf.FinishedAt != nil {
		t.Fatal("finished_at set on a new run")
	}
}

func TestRunIDs_SortChronologically(t *testing.T) {
	d, _ := Open(t.TempDir())
	early, _ := d.newRun(RunInfo{Env: "dev"}, io.Discard, fixedClock(testNow))
	late, _ := d.newRun(RunInfo{Env: "dev"}, io.Discard, fixedClock(testNow.Add(time.Hour)))
	ids := []string{late.ID(), early.ID()}
	sort.Strings(ids)
	if ids[0] != early.ID() {
		t.Fatalf("sorted = %v, want early first", ids)
	}
}

func TestNewRun_SameSecondGetsDistinctFolders(t *testing.T) {
	d, _ := Open(t.TempDir())
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		r, err := d.newRun(RunInfo{Env: "dev"}, io.Discard, fixedClock(testNow))
		if err != nil {
			t.Fatal(err)
		}
		if seen[r.Dir()] {
			t.Fatalf("run folder %s reused", r.Dir())
		}
		seen[r.Dir()] = true
	}
}

func TestStep_TeesToTerminalAndLogFile(t *testing.T) {
	_, r, term := newTestRun(t, tickClock(testNow))
	s := r.Step("build")
	fmt.Fprintf(s.Out(), "hello\n")
	s.End(nil)

	if term.String() != "hello\n" {
		t.Fatalf("terminal = %q", term.String())
	}
	log, err := os.ReadFile(filepath.Join(r.Dir(), "01-build.log"))
	if err != nil || string(log) != "hello\n" {
		t.Fatalf("log = %q, %v", log, err)
	}
	rf := readRunFile(t, r)
	if len(rf.Steps) != 1 || rf.Steps[0].Name != "build" || rf.Steps[0].Status != StatusSuccess || rf.Steps[0].DurationMS <= 0 {
		t.Fatalf("steps = %+v", rf.Steps)
	}
}

func TestSkip_RecordedWithoutNumberOrLog(t *testing.T) {
	_, r, _ := newTestRun(t, tickClock(testNow))
	r.Skip("ssm-sync")
	s := r.Step("build")
	s.End(nil)

	if _, err := os.Stat(filepath.Join(r.Dir(), "01-build.log")); err != nil {
		t.Fatalf("first executed step must be 01: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(r.Dir(), "*ssm-sync*"))
	if len(matches) != 0 {
		t.Fatalf("skipped step has files: %v", matches)
	}
	rf := readRunFile(t, r)
	if len(rf.Steps) != 2 || rf.Steps[0].Status != StatusSkipped || rf.Steps[1].Name != "build" {
		t.Fatalf("steps = %+v", rf.Steps)
	}
}

func TestFinish_FailureRecordsRunStepAndState(t *testing.T) {
	d, r, _ := newTestRun(t, tickClock(testNow))
	s := r.Step("build")
	buildErr := errors.New("docker daemon unreachable")
	s.End(buildErr)
	r.Finish(fmt.Errorf("failed to build/push image: %w", buildErr), State{Target: "demo-dev-service", DeployedBy: "alice"})

	rf := readRunFile(t, r)
	if rf.Status != StatusFailed || !strings.Contains(rf.Error, "docker daemon unreachable") || rf.FinishedAt == nil {
		t.Fatalf("run.json = %+v", rf)
	}
	if rf.Steps[0].Status != StatusFailed || rf.Steps[0].Error != "docker daemon unreachable" {
		t.Fatalf("step = %+v", rf.Steps[0])
	}
	st, err := d.ReadState("dev")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusFailed || st.RunID != r.ID() || st.GitSHA != "abc1234" || st.Target != "demo-dev-service" || st.DeployedBy != "alice" {
		t.Fatalf("state = %+v", st)
	}
}

func TestFinish_IsIdempotent(t *testing.T) {
	d, r, _ := newTestRun(t, tickClock(testNow))
	r.Finish(nil, State{})
	r.Finish(errors.New("late"), State{})
	if rf := readRunFile(t, r); rf.Status != StatusSuccess {
		t.Fatalf("status = %s, want success (second Finish must be a no-op)", rf.Status)
	}
	if st, _ := d.ReadState("dev"); st.Status != StatusSuccess {
		t.Fatalf("state status = %s", st.Status)
	}
}

func TestNewRun_PrunesToNewest50AndIgnoresForeignEntries(t *testing.T) {
	d, _ := Open(t.TempDir())
	runs := filepath.Join(d.Path(), "runs")
	for i := 0; i < 55; i++ {
		name := fmt.Sprintf("20200101T0000%02dZ-abcd", i)
		if err := os.MkdirAll(filepath.Join(runs, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(runs, "notes"), 0o755)
	os.WriteFile(filepath.Join(runs, ".DS_Store"), nil, 0o644)

	r, err := d.newRun(RunInfo{Env: "dev"}, io.Discard, fixedClock(testNow))
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(runs)
	var runDirs []string
	for _, e := range entries {
		if runIDRe.MatchString(e.Name()) {
			runDirs = append(runDirs, e.Name())
		}
	}
	if len(runDirs) != MaxRuns {
		t.Fatalf("%d run dirs remain, want %d", len(runDirs), MaxRuns)
	}
	if _, err := os.Stat(r.Dir()); err != nil {
		t.Fatal("the new run was pruned")
	}
	if _, err := os.Stat(filepath.Join(runs, "20200101T000000Z-abcd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oldest run survived pruning")
	}
	for _, keep := range []string{"notes", ".DS_Store"} {
		if _, err := os.Stat(filepath.Join(runs, keep)); err != nil {
			t.Fatalf("pruning removed foreign entry %s", keep)
		}
	}
}

func TestTerminalRun_WritesOnlyToTerminal(t *testing.T) {
	var term bytes.Buffer
	r := TerminalRun(&term)
	s := r.Step("build")
	fmt.Fprintf(s.Out(), "x\n")
	s.End(errors.New("boom"))
	r.Skip("cdk")
	r.Finish(nil, State{})
	if term.String() != "x\n" || r.Dir() != "" || r.ID() != "" {
		t.Fatalf("term=%q dir=%q id=%q", term.String(), r.Dir(), r.ID())
	}
}

func TestRun_UnwritableRunDirDegradesToTerminal(t *testing.T) {
	skipIfRoot(t)
	_, r, term := newTestRun(t, tickClock(testNow))
	dir := r.Dir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	s := r.Step("build")
	fmt.Fprintf(s.Out(), "still here\n")
	s.End(nil)
	r.Finish(nil, State{})

	out := term.String()
	if !strings.Contains(out, "still here\n") {
		t.Fatalf("terminal lost output: %q", out)
	}
	if strings.Count(out, "run logging disabled") != 1 {
		t.Fatalf("want exactly one warning, got %q", out)
	}
}

type failWriter struct{ calls int }

func (f *failWriter) Write(p []byte) (int, error) {
	f.calls++
	return 0, errors.New("disk full")
}

func TestTeeWriter_LogFailureNeverBreaksTerminal(t *testing.T) {
	var term bytes.Buffer
	fw := &failWriter{}
	tw := &teeWriter{term: &term, file: fw}
	for i := 0; i < 3; i++ {
		n, err := tw.Write([]byte("line\n"))
		if err != nil || n != 5 {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if term.String() != "line\nline\nline\n" {
		t.Fatalf("terminal = %q", term.String())
	}
	if fw.calls != 1 {
		t.Fatalf("log file written %d times, want 1 (dropped after first failure)", fw.calls)
	}
}
