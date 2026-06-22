package main

import (
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
