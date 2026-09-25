package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDefaultPath(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"neither exists", nil, "citadel.yml"},
		{"yml only", []string{"citadel.yml"}, "citadel.yml"},
		{"yaml only", []string{"citadel.yaml"}, "citadel.yaml"},
		{"both exist, yml wins", []string{"citadel.yml", "citadel.yaml"}, "citadel.yml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				touch(t, filepath.Join(dir, f))
			}
			got := ResolveDefaultPath(dir)
			if want := filepath.Join(dir, tc.want); got != want {
				t.Fatalf("ResolveDefaultPath = %s, want %s", got, want)
			}
		})
	}
}

func TestResolveDefaultPath_DotStaysRelative(t *testing.T) {
	// main passes "." — the result must stay a plain relative name so error
	// messages and --config defaults read "citadel.yml", not "./citadel.yml".
	t.Chdir(t.TempDir())
	if got := ResolveDefaultPath("."); got != "citadel.yml" {
		t.Fatalf("got %q, want citadel.yml", got)
	}
}

func TestParse(t *testing.T) {
	valid := "name: demo\nregion: us-east-1\nruntime: lambda\nenvironments:\n  dev:\n    account: \"111111111111\"\n"
	cfg, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("Parse(valid) error: %v", err)
	}
	if cfg.Name != "demo" || cfg.ResolvedRuntime() != RuntimeLambda {
		t.Fatalf("parsed %+v", cfg)
	}

	if _, err := Parse([]byte("name: [")); err == nil || !strings.Contains(err.Error(), "failed to parse config") {
		t.Fatalf("Parse(broken yaml) error = %v, want parse error", err)
	}
	if _, err := Parse([]byte("region: us-east-1\n")); err == nil || !strings.Contains(err.Error(), "invalid config") {
		t.Fatalf("Parse(no name) error = %v, want invalid config", err)
	}
}
