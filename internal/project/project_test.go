package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
}

func TestOpen_CreatesDirAndGitignoreIdempotently(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if d.Path() != filepath.Join(root, ".citadel") {
		t.Fatalf("Path() = %s", d.Path())
	}
	gi := filepath.Join(d.Path(), ".gitignore")
	got, err := os.ReadFile(gi)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "state/\nruns/\n" {
		t.Fatalf(".gitignore = %q", got)
	}

	// A user's additions survive a second Open.
	if err := os.WriteFile(gi, []byte("state/\nruns/\nscratch/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(gi)
	if !strings.Contains(string(got), "scratch/") {
		t.Fatal("Open rewrote an existing .gitignore")
	}
}

func TestOpen_ReadOnlyParentFails(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	if _, err := Open(root); err == nil {
		t.Fatal("Open succeeded in a read-only directory")
	}
}

func TestExisting_DoesNotCreate(t *testing.T) {
	root := t.TempDir()
	if _, err := Existing(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Existing error = %v, want ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".citadel")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Existing created .citadel/")
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Existing(root); err != nil {
		t.Fatalf("Existing after Open: %v", err)
	}
}

func TestEnsureProject_WritesOnceAndKeepsExisting(t *testing.T) {
	d, _ := Open(t.TempDir())
	if _, err := d.Project(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Project() on fresh dir error = %v, want ErrNotExist", err)
	}

	first := NewMeta("legolas", "ecs", "v1.2.3", testNow)
	if len(first.ID) != 16 {
		t.Fatalf("ID %q, want 16 hex chars", first.ID)
	}
	got, created, err := d.EnsureProject(first)
	if err != nil || !created || got.ID != first.ID {
		t.Fatalf("first EnsureProject = %+v, %v, %v", got, created, err)
	}

	got, created, err = d.EnsureProject(NewMeta("renamed", "lambda", "v9", testNow))
	if err != nil || created {
		t.Fatalf("second EnsureProject created=%v err=%v", created, err)
	}
	if got.ID != first.ID || got.Name != "legolas" {
		t.Fatalf("existing project.yml was replaced: %+v", got)
	}

	read, err := d.Project()
	if err != nil {
		t.Fatal(err)
	}
	if read.CreatedWith != "citadel v1.2.3" || !read.CreatedAt.Equal(testNow) || read.Runtime != "ecs" {
		t.Fatalf("round-trip = %+v", read)
	}
}

func TestProject_IgnoresUnknownFields(t *testing.T) {
	d, _ := Open(t.TempDir())
	body := "id: abc\nname: x\nruntime: ecs\nfuture_field: 1\n"
	if err := os.WriteFile(filepath.Join(d.Path(), "project.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := d.Project()
	if err != nil || m.ID != "abc" {
		t.Fatalf("Project() = %+v, %v", m, err)
	}
}

func TestState_RoundTrip(t *testing.T) {
	d, _ := Open(t.TempDir())
	want := State{
		RunID: "20260925T100000Z-a1b2", Status: "success", GitSHA: "abc1234",
		ImageURI: "1.dkr.ecr.us-east-1.amazonaws.com/x:abc1234", Target: "x-dev-service",
		DeployedBy: "alice", StartedAt: testNow, FinishedAt: testNow.Add(time.Minute),
	}
	if err := d.WriteState("dev", want); err != nil {
		t.Fatal(err)
	}
	got, err := d.ReadState("dev")
	if err != nil {
		t.Fatal(err)
	}
	// Compare times with Equal: JSON round-trips can change a Time's internal
	// location pointer, which would break a plain struct ==.
	if got.RunID != want.RunID || got.Status != want.Status || got.GitSHA != want.GitSHA ||
		got.ImageURI != want.ImageURI || got.Target != want.Target || got.DeployedBy != want.DeployedBy ||
		!got.StartedAt.Equal(want.StartedAt) || !got.FinishedAt.Equal(want.FinishedAt) {
		t.Fatalf("ReadState = %+v, want %+v", *got, want)
	}
}

func TestReadState_NeverDeployed(t *testing.T) {
	d, _ := Open(t.TempDir())
	if _, err := d.ReadState("prod"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want ErrNotExist", err)
	}
}

func TestState_RejectsPathLikeEnv(t *testing.T) {
	d, _ := Open(t.TempDir())
	for _, env := range []string{"", "../x", "a/b", "."} {
		if err := d.WriteState(env, State{}); err == nil {
			t.Errorf("WriteState(%q) succeeded", env)
		}
		if _, err := d.ReadState(env); err == nil {
			t.Errorf("ReadState(%q) succeeded", env)
		}
	}
}

func TestWriteFileAtomic_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.yml")
	for _, body := range []string{"one", "two"} {
		if err := WriteFileAtomic(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(path)
	if string(got) != "two" {
		t.Fatalf("content = %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1 (temp file leaked?)", len(entries))
	}
}
