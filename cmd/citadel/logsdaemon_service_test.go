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

func TestInstallUnit_WritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Put a fake citadel-logs on PATH so resolveLogsBinary succeeds.
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "citadel-logs")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	if err := installUnit(defaultDaemonAddr, "clusterbox", "us-east-1"); err != nil {
		t.Fatal(err)
	}

	unitPath := filepath.Join(home, ".config", "systemd", "user", serviceName)
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("unit file not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "ExecStart="+fakeBin+" --registry") {
		t.Fatalf("ExecStart does not reference resolved binary:\n%s", content)
	}
	if !strings.Contains(content, "Environment=AWS_PROFILE=clusterbox") {
		t.Fatalf("missing profile env:\n%s", content)
	}

	// Data directory must have been created.
	if fi, err := os.Stat(filepath.Join(home, ".local", "share", "citadel")); err != nil || !fi.IsDir() {
		t.Fatalf("data dir not created: %v", err)
	}
}

type fakeRunner struct {
	calls   []string
	runErr  error
	outputs map[string]string
}

func key(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

func (f *fakeRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, key(name, args...))
	return f.runErr
}

func (f *fakeRunner) Output(name string, args ...string) (string, error) {
	k := key(name, args...)
	f.calls = append(f.calls, k)
	return f.outputs[k], nil
}

func TestStartService_CommandSequence(t *testing.T) {
	r := &fakeRunner{}
	if err := startService(r); err != nil {
		t.Fatal(err)
	}
	// First two calls are deterministic; linger uses the current username.
	if len(r.calls) < 3 {
		t.Fatalf("expected at least 3 calls, got %v", r.calls)
	}
	if r.calls[0] != "systemctl --user daemon-reload" {
		t.Fatalf("call[0] = %q", r.calls[0])
	}
	if r.calls[1] != "systemctl --user enable --now citadel-logs.service" {
		t.Fatalf("call[1] = %q", r.calls[1])
	}
	if !strings.HasPrefix(r.calls[2], "loginctl enable-linger ") {
		t.Fatalf("call[2] = %q", r.calls[2])
	}
}

func TestStartService_LingerFailureIsNonFatal(t *testing.T) {
	// loginctl enable-linger can be denied; that must not fail start.
	if err := startService(&lingerFailRunner{}); err != nil {
		t.Fatalf("linger failure should not be fatal, got %v", err)
	}
}

// lingerFailRunner succeeds for systemctl but fails loginctl.
type lingerFailRunner struct{ calls []string }

func (f *lingerFailRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, key(name, args...))
	if name == "loginctl" {
		return errors.New("enable-linger denied")
	}
	return nil
}
func (f *lingerFailRunner) Output(name string, args ...string) (string, error) {
	return "", nil
}

func TestStopService_WithDisable(t *testing.T) {
	r := &fakeRunner{}
	if err := stopService(r, true); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user stop citadel-logs.service",
		"systemctl --user disable citadel-logs.service",
	}
	if strings.Join(r.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestStopService_WithoutDisable(t *testing.T) {
	r := &fakeRunner{}
	if err := stopService(r, false); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || r.calls[0] != "systemctl --user stop citadel-logs.service" {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestRestartService_CommandSequence(t *testing.T) {
	r := &fakeRunner{}
	if err := restartService(r); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user restart citadel-logs.service",
	}
	if strings.Join(r.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestLogsArgs(t *testing.T) {
	cases := []struct {
		lines  int
		follow bool
		want   string
	}{
		{0, false, "--user -u citadel-logs.service"},
		{50, false, "--user -u citadel-logs.service -n 50"},
		{0, true, "--user -u citadel-logs.service -f"},
		{20, true, "--user -u citadel-logs.service -n 20 -f"},
	}
	for _, c := range cases {
		got := strings.Join(logsArgs(c.lines, c.follow), " ")
		if got != c.want {
			t.Fatalf("logsArgs(%d,%v) = %q, want %q", c.lines, c.follow, got, c.want)
		}
	}
}

func TestFormatStatus(t *testing.T) {
	got := formatStatus("active", "/home/me/.citadel/registry.yml", 3)
	if !strings.Contains(got, "active") {
		t.Fatalf("missing state:\n%s", got)
	}
	if !strings.Contains(got, "http://localhost:5500/logs") {
		t.Fatalf("missing dashboard URL:\n%s", got)
	}
	if !strings.Contains(got, "3 service") {
		t.Fatalf("missing service count:\n%s", got)
	}

	unknown := formatStatus("", "/x/registry.yml", 0)
	if !strings.Contains(unknown, "unknown") {
		t.Fatalf("empty state should render unknown:\n%s", unknown)
	}

	wsOnly := formatStatus("   ", "/x/registry.yml", 0)
	if !strings.Contains(wsOnly, "unknown") {
		t.Fatalf("whitespace-only state should render unknown:\n%s", wsOnly)
	}
}

func TestResolveAWSEnv_FlagOverridesEnv(t *testing.T) {
	t.Setenv("AWS_PROFILE", "envprofile")
	t.Setenv("AWS_REGION", "us-west-2")

	// Flags win when set.
	p, r := resolveAWSEnv("flagprofile", "eu-west-1")
	if p != "flagprofile" || r != "eu-west-1" {
		t.Fatalf("flags should win: got (%q,%q)", p, r)
	}

	// Empty flags fall back to env.
	p, r = resolveAWSEnv("", "")
	if p != "envprofile" || r != "us-west-2" {
		t.Fatalf("should fall back to env: got (%q,%q)", p, r)
	}
}

func TestInstallUnit_RejectsNewlineInjection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "citadel-logs"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	err := installUnit(defaultDaemonAddr, "evil\nExecStartPre=/bin/rm -rf x", "us-east-1")
	if err == nil {
		t.Fatal("expected installUnit to reject a profile containing a newline")
	}
	if _, statErr := os.Stat(filepath.Join(home, ".config", "systemd", "user", serviceName)); statErr == nil {
		t.Fatal("unit file must not be written when validation fails")
	}
}
