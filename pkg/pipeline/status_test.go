package pipeline

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ClusterBox/citadel/internal/project"
)

func TestFormatLastDeploy(t *testing.T) {
	st := &project.State{
		RunID: "20260925T100000Z-a1b2", Status: "success", GitSHA: "abc1234", DeployedBy: "alice",
		FinishedAt: time.Date(2026, 9, 25, 10, 3, 0, 0, time.UTC),
	}
	want := "🗂  Last local deploy: success · abc1234 · 2026-09-25 10:03 UTC · by alice\n   Run: .citadel/runs/20260925T100000Z-a1b2"
	if got := FormatLastDeploy(st); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestLastLocalDeploy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "citadel.yml")
	if got := LastLocalDeploy(cfgPath, "dev"); got != "" {
		t.Fatalf("no .citadel/: got %q, want empty", got)
	}
	d, _ := project.Open(dir)
	if got := LastLocalDeploy(cfgPath, "dev"); got != "" {
		t.Fatalf("never deployed: got %q, want empty", got)
	}
	d.WriteState("dev", project.State{RunID: "r", Status: "failed", GitSHA: "s", DeployedBy: "bob"})
	if got := LastLocalDeploy(cfgPath, "dev"); got == "" {
		t.Fatal("expected a summary after a deploy")
	}
}
