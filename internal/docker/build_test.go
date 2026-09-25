package docker

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isOrUnder reports whether name is prefix itself or a path under it, using
// the current OS path separator (tar headers use "/" but filepath.Walk on
// this OS produces relative paths with filepath.Separator).
func isOrUnder(name, prefix string) bool {
	if name == prefix {
		return true
	}
	return strings.HasPrefix(name, prefix+string(filepath.Separator)) || strings.HasPrefix(name, prefix+"/")
}

// TestCreateTarFromDirectory_SkipsCitadelAndGitDirs guards against .citadel/
// (per-deploy run logs, local state) entering every Docker build context: it
// busts the COPY . . layer cache and can leak deploy logs into images.
func TestCreateTarFromDirectory_SkipsCitadelAndGitDirs(t *testing.T) {
	dir := t.TempDir()

	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite(filepath.Join(".citadel", "runs", "x", "03-build.log"), "log output")
	mustWrite(filepath.Join(".git", "HEAD"), "ref: refs/heads/main")
	mustWrite("app.go", "package main")

	rc, err := createTarFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	var names []string
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}

	for _, n := range names {
		if isOrUnder(n, ".citadel") {
			t.Fatalf("tar contains .citadel entry: %s (all names: %v)", n, names)
		}
		if isOrUnder(n, ".git") {
			t.Fatalf("tar contains .git entry: %s (all names: %v)", n, names)
		}
	}

	found := false
	for _, n := range names {
		if n == "app.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tar missing app.go, got %v", names)
	}
}
