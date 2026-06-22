package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUnit_WithProfileAndRegion(t *testing.T) {
	got := renderUnit(unitOptions{
		BinaryPath:   "/home/me/go/bin/citadel-logs",
		RegistryPath: unitRegistryPath,
		DBPath:       unitDBPath,
		Addr:         defaultDaemonAddr,
		Profile:      "clusterbox",
		Region:       "us-east-1",
	})

	want := `[Unit]
Description=citadel-logs daemon
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/home/me/go/bin/citadel-logs --registry %h/.citadel/registry.yml --db %h/.local/share/citadel/citadel-logs.db --addr 127.0.0.1:5500
Environment=AWS_PROFILE=clusterbox
Environment=AWS_REGION=us-east-1
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
	if got != want {
		t.Fatalf("unit mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderUnit_OmitsEmptyEnv(t *testing.T) {
	got := renderUnit(unitOptions{
		BinaryPath:   "/usr/bin/citadel-logs",
		RegistryPath: unitRegistryPath,
		DBPath:       unitDBPath,
		Addr:         defaultDaemonAddr,
	})
	if strings.Contains(got, "Environment=") {
		t.Fatalf("expected no Environment= lines, got:\n%s", got)
	}
	if !strings.Contains(got, "ExecStart=/usr/bin/citadel-logs --registry") {
		t.Fatalf("ExecStart missing, got:\n%s", got)
	}
	// The line after ExecStart must be Restart= when no env is set.
	if !strings.Contains(got, "--addr 127.0.0.1:5500\nRestart=on-failure") {
		t.Fatalf("expected Restart to immediately follow ExecStart, got:\n%s", got)
	}
}

func TestResolveLogsBinaryIn_PrefersSibling(t *testing.T) {
	dir := t.TempDir()
	sibling := filepath.Join(dir, "citadel-logs")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failLookPath := func(string) (string, error) { return "", errors.New("should not be called") }

	got, err := resolveLogsBinaryIn(dir, failLookPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != sibling {
		t.Fatalf("got %q, want %q", got, sibling)
	}
}

func TestResolveLogsBinaryIn_FallsBackToPath(t *testing.T) {
	emptyDir := t.TempDir()
	lookPath := func(name string) (string, error) {
		if name != "citadel-logs" {
			t.Fatalf("unexpected lookup %q", name)
		}
		return "/usr/local/bin/citadel-logs", nil
	}
	got, err := resolveLogsBinaryIn(emptyDir, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/local/bin/citadel-logs" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveLogsBinaryIn_NotFound(t *testing.T) {
	emptyDir := t.TempDir()
	lookPath := func(string) (string, error) { return "", errors.New("not found") }
	if _, err := resolveLogsBinaryIn(emptyDir, lookPath); err == nil {
		t.Fatal("expected error when binary missing")
	}
}
