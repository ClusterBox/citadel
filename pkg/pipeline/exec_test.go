package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

type fakeStep struct {
	name    string
	skip    bool
	planErr error
	runErr  error
	print   string
	changes bool
	calls   *[]string
}

func (f *fakeStep) Name() string { return f.name }
func (f *fakeStep) Plan(_ context.Context, _ *StepContext, _ io.Writer) (bool, error) {
	*f.calls = append(*f.calls, "plan:"+f.name)
	return f.skip, f.planErr
}
func (f *fakeStep) Run(_ context.Context, _ *StepContext, w io.Writer) error {
	*f.calls = append(*f.calls, "run:"+f.name)
	fmt.Fprint(w, f.print)
	return f.runErr
}
func (f *fakeStep) changesService() bool { return f.changes }

func newExecHarness(t *testing.T) (*StepContext, *project.Run, *bytes.Buffer) {
	t.Helper()
	d, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	run, err := d.NewRun(project.RunInfo{Env: "dev"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	sc := &StepContext{Cfg: &config.DeployConfig{Name: "demo"}, Opts: &DeployOptions{}, Env: "dev", state: &project.State{}, ops: &ops{}}
	return sc, run, &out
}

func steps(ps ...plannedStep) []plannedStep { return ps }

func TestExecute_RunsInOrderAndRecordsSteps(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "a", print: "A\n", calls: &calls}},
		plannedStep{Step: &fakeStep{name: "b", skip: true, calls: &calls}},
		plannedStep{Step: &fakeStep{name: "c", print: "C\n", calls: &calls}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"plan:a", "run:a", "plan:b", "plan:c", "run:c"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
	run.Finish(nil, project.State{})
	if want := []string{"a:success", "b:skipped", "c:success"}; !reflect.DeepEqual(stepSummary(readRun(t, run.Dir())), want) {
		t.Fatalf("steps = %v", stepSummary(readRun(t, run.Dir())))
	}
	if out.String() != "A\nC\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestExecute_EnvsFilterSkipsWithMessage(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "seed", calls: &calls}, opts: stepOptions{envs: []string{"prod"}}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("a filtered step must not be planned or run: %v", calls)
	}
	if out.String() != "⏭️  Skipping seed (not for dev)\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestExecute_StopsOnFailureAndRecordsFinalError(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var recorded error
	recordCalls := 0
	sc.Record = func(err error) { recordCalls++; recorded = err }
	var calls []string
	boom := errors.New("boom")
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "a", runErr: boom, calls: &calls}},
		plannedStep{Step: &fakeStep{name: "b", calls: &calls}},
	))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if reflect.DeepEqual(calls, []string{"plan:a", "run:a", "plan:b", "run:b"}) {
		t.Fatal("pipeline continued after a failure")
	}
	if recordCalls != 1 || !errors.Is(recorded, boom) {
		t.Fatalf("Record called %d times with %v", recordCalls, recorded)
	}
}

func TestExecute_ContinueOnError(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	var recorded error = errors.New("unset")
	sc.Record = func(err error) { recorded = err }
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "flaky", runErr: errors.New("nope"), calls: &calls}, opts: stepOptions{continueOnError: true}},
		plannedStep{Step: &fakeStep{name: "b", calls: &calls}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "⚠️  flaky failed; continuing: nope") {
		t.Fatalf("out = %q", out.String())
	}
	if recorded != nil {
		t.Fatalf("Record got %v, want nil", recorded)
	}
	run.Finish(nil, project.State{})
	if want := []string{"flaky:failed", "b:success"}; !reflect.DeepEqual(stepSummary(readRun(t, run.Dir())), want) {
		t.Fatalf("steps = %v", stepSummary(readRun(t, run.Dir())))
	}
}

func TestExecute_PlanErrorStopsWithoutRecordingStep(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "a", planErr: errors.New("no aws"), calls: &calls}},
	))
	if err == nil || err.Error() != "no aws" {
		t.Fatalf("err = %v", err)
	}
	run.Finish(err, project.State{})
	if len(readRun(t, run.Dir()).Steps) != 0 {
		t.Fatal("a plan error must not create a step record")
	}
}

type wrappingStep struct{ fakeStep }

func (w *wrappingStep) wrapErr(err error) error {
	return fmt.Errorf("failed to build/push image: %w", err)
}

func TestExecute_WrapErrKeepsRawStepError(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &wrappingStep{fakeStep{name: "build", runErr: errors.New("docker down"), calls: &calls}}},
	))
	if err == nil || err.Error() != "failed to build/push image: docker down" {
		t.Fatalf("err = %v", err)
	}
	run.Finish(err, project.State{})
	if s := readRun(t, run.Dir()).Steps[0]; s.Status != "failed" {
		t.Fatalf("step = %+v", s)
	}
}

func TestExecute_RollbackOnFailure(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	sc.needSnapshot = true
	sc.ops.snapshot = func(context.Context, *config.DeployConfig, string) (string, error) { return "td:7", nil }
	var rolledTo string
	sc.ops.rollback = func(_ context.Context, w io.Writer, _ *config.DeployConfig, _ string, before string) error {
		rolledTo = before
		fmt.Fprint(w, "[rolled back]\n")
		return nil
	}
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "deploy", changes: true, calls: &calls}},
		plannedStep{Step: &fakeStep{name: "smoke", runErr: errors.New("503"), calls: &calls}, opts: stepOptions{rollback: true}},
	))
	if err == nil || !strings.Contains(err.Error(), "smoke: 503") || !strings.Contains(err.Error(), "rolled back to td:7") {
		t.Fatalf("err = %v", err)
	}
	if rolledTo != "td:7" {
		t.Fatalf("rolled back to %q", rolledTo)
	}
	run.Finish(err, project.State{})
	if want := []string{"deploy:success", "smoke:failed", "rollback:success"}; !reflect.DeepEqual(stepSummary(readRun(t, run.Dir())), want) {
		t.Fatalf("steps = %v", stepSummary(readRun(t, run.Dir())))
	}
}

func TestExecute_RollbackWithoutSnapshot(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	sc.needSnapshot = true
	sc.ops.snapshot = func(context.Context, *config.DeployConfig, string) (string, error) { return "", errors.New("denied") }
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "deploy", changes: true, calls: &calls}},
		plannedStep{Step: &fakeStep{name: "smoke", runErr: errors.New("503"), calls: &calls}, opts: stepOptions{rollback: true}},
	))
	if err == nil || !strings.Contains(err.Error(), "no snapshot of the previous deployment") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "⚠️  could not record the current deployment for rollback: denied") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestExecute_RollbackFailureReportsBoth(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	sc.needSnapshot = true
	sc.ops.snapshot = func(context.Context, *config.DeployConfig, string) (string, error) { return "td:7", nil }
	sc.ops.rollback = func(context.Context, io.Writer, *config.DeployConfig, string, string) error {
		return errors.New("throttled")
	}
	err := execute(context.Background(), sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "deploy", changes: true, calls: &calls}},
		plannedStep{Step: &fakeStep{name: "smoke", runErr: errors.New("503"), calls: &calls}, opts: stepOptions{rollback: true}},
	))
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "rollback failed: throttled") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecute_SnapshotOnlyWhenNeededAndBeforeFirstServiceChange(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	snaps := 0
	sc.ops.snapshot = func(context.Context, *config.DeployConfig, string) (string, error) { snaps++; return "td:7", nil }
	_ = execute(context.Background(), sc, run, out, steps(plannedStep{Step: &fakeStep{name: "deploy", changes: true, calls: &calls}}))
	if snaps != 0 {
		t.Fatal("snapshot taken although no step needs rollback")
	}
	sc2, run2, out2 := newExecHarness(t)
	sc2.needSnapshot = true
	sc2.ops.snapshot = sc.ops.snapshot
	_ = execute(context.Background(), sc2, run2, out2, steps(
		plannedStep{Step: &fakeStep{name: "cdk", changes: true, calls: &calls}},
		plannedStep{Step: &fakeStep{name: "deploy", changes: true, calls: &calls}},
	))
	if snaps != 1 || sc2.Before != "td:7" {
		t.Fatalf("snapshots = %d, Before = %q", snaps, sc2.Before)
	}
}

func TestExecute_PanicRecordsFailure(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var recorded error
	sc.Record = func(err error) { recorded = err }
	defer func() {
		if recover() == nil {
			t.Fatal("panic was swallowed")
		}
		if recorded == nil || !strings.Contains(recorded.Error(), "panic: kaboom") {
			t.Fatalf("Record got %v", recorded)
		}
	}()
	_ = execute(context.Background(), sc, run, out, steps(plannedStep{Step: panicStep{}}))
}

type panicStep struct{}

func (panicStep) Name() string                                                { return "p" }
func (panicStep) Plan(context.Context, *StepContext, io.Writer) (bool, error) { return false, nil }
func (panicStep) Run(context.Context, *StepContext, io.Writer) error          { panic("kaboom") }

type runRecord struct {
	Status string `json:"status"`
	Steps  []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"steps"`
}

func readRun(t *testing.T, runDir string) runRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r runRecord
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func stepSummary(r runRecord) []string {
	var out []string
	for _, s := range r.Steps {
		out = append(out, s.Name+":"+s.Status)
	}
	return out
}

// cancelStep cancels the deploy while it runs and returns ctx.Err(), like a
// run: or task: step interrupted by Ctrl-C.
type cancelStep struct{ cancel context.CancelFunc }

func (cancelStep) Name() string                                                { return "smoke" }
func (cancelStep) Plan(context.Context, *StepContext, io.Writer) (bool, error) { return false, nil }
func (s cancelStep) Run(ctx context.Context, _ *StepContext, _ io.Writer) error {
	s.cancel()
	<-ctx.Done()
	return ctx.Err()
}

func TestExecute_CancelledDeployDoesNotRollBack(t *testing.T) {
	sc, run, out := newExecHarness(t)
	var calls []string
	sc.needSnapshot = true
	sc.ops.snapshot = func(context.Context, *config.DeployConfig, string) (string, error) { return "td:7", nil }
	rollbacks := 0
	sc.ops.rollback = func(context.Context, io.Writer, *config.DeployConfig, string, string) error {
		rollbacks++
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := execute(ctx, sc, run, out, steps(
		plannedStep{Step: &fakeStep{name: "deploy", changes: true, calls: &calls}},
		plannedStep{Step: cancelStep{cancel}, opts: stepOptions{rollback: true}},
	))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rollbacks != 0 {
		t.Fatalf("rolled back %d times after a cancellation", rollbacks)
	}
	if !strings.Contains(out.String(), "↩️  Not rolling back: deploy was cancelled\n") {
		t.Fatalf("out = %q", out.String())
	}
	run.Finish(err, project.State{})
	if want := []string{"deploy:success", "smoke:failed"}; !reflect.DeepEqual(stepSummary(readRun(t, run.Dir())), want) {
		t.Fatalf("steps = %v", stepSummary(readRun(t, run.Dir())))
	}
}
