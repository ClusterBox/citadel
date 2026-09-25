package pipeline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

func demoCfg() *config.DeployConfig {
	return &config.DeployConfig{Name: "demo", Region: "us-east-1", Runtime: config.RuntimeLambda}
}

func TestOpenRun_DryRunTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	run := openRun(&DeployOptions{ConfigPath: filepath.Join(dir, "citadel.yml"), Environment: "dev", DryRun: true, Out: &bytes.Buffer{}}, demoCfg(), "abc1234")
	if run.Dir() != "" {
		t.Fatalf("dry run recorded to %s", run.Dir())
	}
	if _, err := os.Stat(filepath.Join(dir, ".citadel")); !os.IsNotExist(err) {
		t.Fatal("dry run created .citadel/")
	}
}

func TestOpenRun_CreatesCitadelNextToConfigNotCwd(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "svc")
	if err := os.Mkdir(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(root, "elsewhere")
	os.Mkdir(elsewhere, 0o755)
	t.Chdir(elsewhere)

	opts := &DeployOptions{ConfigPath: "../svc/citadel.yml", Environment: "dev", Message: "m", Version: "v0.test", Out: &bytes.Buffer{}}
	run := openRun(opts, demoCfg(), "abc1234")

	if !strings.HasPrefix(run.Dir(), filepath.Join("..", "svc", ".citadel", "runs")) {
		t.Fatalf("run dir = %q, want under ../svc/.citadel/runs", run.Dir())
	}
	if _, err := os.Stat(filepath.Join(elsewhere, ".citadel")); !os.IsNotExist(err) {
		t.Fatal(".citadel/ created in the working directory")
	}
	d, err := project.Existing(svc)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := d.Project()
	if err != nil || meta.Name != "demo" || meta.Runtime != "lambda" || meta.CreatedWith != "citadel v0.test" {
		t.Fatalf("project.yml = %+v, %v", meta, err)
	}
}

func TestOpenRun_UnwritableDirDegradesWithOneWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	var out bytes.Buffer
	run := openRun(&DeployOptions{ConfigPath: filepath.Join(dir, "citadel.yml"), Environment: "dev", Out: &out}, demoCfg(), "abc1234")
	if run.Dir() != "" {
		t.Fatalf("expected terminal-only run, got %s", run.Dir())
	}
	if strings.Count(out.String(), "run logging disabled") != 1 {
		t.Fatalf("output = %q", out.String())
	}
}

// TestFinishRun_PanicRecordsFailedAndRePanics guards against a panicking
// deploy being recorded as success: during panic unwinding retErr is nil, so
// a plain `defer run.Finish(retErr, state)` would write "success" to
// run.json and state/<env>.json even though the deploy crashed.
func TestFinishRun_PanicRecordsFailedAndRePanics(t *testing.T) {
	dir := t.TempDir()
	d, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var term bytes.Buffer
	run, err := d.NewRun(project.RunInfo{Env: "dev"}, &term)
	if err != nil {
		t.Fatal(err)
	}

	var retErr error
	state := project.State{}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected the panic to propagate past finishRun")
			}
		}()
		defer finishRun(run, &retErr, &state)
		panic("boom")
	}()

	data, err := os.ReadFile(filepath.Join(run.Dir(), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rf struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(data, &rf); err != nil {
		t.Fatal(err)
	}
	if rf.Status != project.StatusFailed {
		t.Fatalf("status = %q, want %q", rf.Status, project.StatusFailed)
	}
	if !strings.Contains(rf.Error, "boom") {
		t.Fatalf("error = %q, want it to mention the panic value", rf.Error)
	}
}
