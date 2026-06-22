# Logs Daemon systemd Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `start`/`stop`/`restart`/`status`/`logs` subcommands to `citadel logs-daemon` that manage the `citadel-logs` binary as a native systemd `--user` service.

**Architecture:** A new file `cmd/citadel/logsdaemon_service.go` holds a pure `renderUnit` function, a binary-resolution helper, an `installUnit` filesystem step, and lifecycle helpers that shell out to `systemctl`/`loginctl`/`journalctl` through an injectable `execRunner` interface. Cobra subcommands wire these together and are registered on the existing `logs-daemon` command group.

**Tech Stack:** Go 1.25, cobra, systemd (Linux `--user` services). No new dependencies.

## Global Constraints

- Linux-only feature; non-Linux platforms must error with a pointer to `docker compose -f docker-compose.logs.yml up -d`.
- Service name: `citadel-logs.service`. Unit file: `~/.config/systemd/user/citadel-logs.service`.
- Daemon binds loopback only: default addr `127.0.0.1:5500`.
- Unit `ExecStart` uses systemd `%h` specifier for home-relative paths: registry `%h/.citadel/registry.yml`, db `%h/.local/share/citadel/citadel-logs.db`.
- AWS env captured at install time: `--profile`/`--region` flags override, else inherit `AWS_PROFILE`/`AWS_REGION`; empty values omit their `Environment=` line.
- All new code lives in `package main` in `cmd/citadel/`. Match existing cobra/`fmt.Printf` style (emoji status lines like `✅`).
- Run `go test ./cmd/citadel/...` and `go vet ./cmd/citadel/...` clean.

---

### Task 1: Unit-file renderer

**Files:**
- Create: `cmd/citadel/logsdaemon_service.go`
- Test: `cmd/citadel/logsdaemon_service_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const serviceName = "citadel-logs.service"`
  - `const defaultDaemonAddr = "127.0.0.1:5500"`
  - `const unitRegistryPath = "%h/.citadel/registry.yml"`
  - `const unitDBPath = "%h/.local/share/citadel/citadel-logs.db"`
  - `type unitOptions struct { BinaryPath, RegistryPath, DBPath, Addr, Profile, Region string }`
  - `func renderUnit(o unitOptions) string`

- [ ] **Step 1: Write the failing test**

In `cmd/citadel/logsdaemon_service_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/citadel/ -run TestRenderUnit -v`
Expected: FAIL — `undefined: renderUnit` (and the constants/type).

- [ ] **Step 3: Write minimal implementation**

In `cmd/citadel/logsdaemon_service.go`:

```go
package main

import (
	"fmt"
	"strings"
)

const (
	serviceName       = "citadel-logs.service"
	defaultDaemonAddr = "127.0.0.1:5500"
	unitRegistryPath  = "%h/.citadel/registry.yml"
	unitDBPath        = "%h/.local/share/citadel/citadel-logs.db"
)

type unitOptions struct {
	BinaryPath   string
	RegistryPath string
	DBPath       string
	Addr         string
	Profile      string
	Region       string
}

// renderUnit builds the systemd --user unit file contents. Empty Profile or
// Region omit their Environment= line entirely.
func renderUnit(o unitOptions) string {
	var env strings.Builder
	if o.Profile != "" {
		fmt.Fprintf(&env, "Environment=AWS_PROFILE=%s\n", o.Profile)
	}
	if o.Region != "" {
		fmt.Fprintf(&env, "Environment=AWS_REGION=%s\n", o.Region)
	}
	return fmt.Sprintf(`[Unit]
Description=citadel-logs daemon
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s --registry %s --db %s --addr %s
%sRestart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, o.BinaryPath, o.RegistryPath, o.DBPath, o.Addr, env.String())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/citadel/ -run TestRenderUnit -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add cmd/citadel/logsdaemon_service.go cmd/citadel/logsdaemon_service_test.go
git commit -m "feat(logs-daemon): render systemd user unit file"
```

---

### Task 2: Binary resolution

**Files:**
- Modify: `cmd/citadel/logsdaemon_service.go`
- Test: `cmd/citadel/logsdaemon_service_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces:
  - `func resolveLogsBinaryIn(exeDir string, lookPath func(string) (string, error)) (string, error)`
  - `func resolveLogsBinary() (string, error)` — wraps the above with the real `os.Executable` dir and `exec.LookPath`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/citadel/logsdaemon_service_test.go`:

```go
import (
	"errors"
	"os"
	"path/filepath"
	// (keep existing imports: strings, testing)
)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/citadel/ -run TestResolveLogsBinaryIn -v`
Expected: FAIL — `undefined: resolveLogsBinaryIn`.

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/citadel/logsdaemon_service.go`. Update the import block to add `os`, `os/exec`, `path/filepath`:

```go
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)
```

```go
// resolveLogsBinaryIn finds the citadel-logs binary, preferring a sibling of
// the running citadel executable, then falling back to PATH.
func resolveLogsBinaryIn(exeDir string, lookPath func(string) (string, error)) (string, error) {
	if exeDir != "" {
		cand := filepath.Join(exeDir, "citadel-logs")
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	if p, err := lookPath("citadel-logs"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("citadel-logs binary not found next to citadel or on PATH; run `make install`")
}

func resolveLogsBinary() (string, error) {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	return resolveLogsBinaryIn(exeDir, exec.LookPath)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/citadel/ -run TestResolveLogsBinaryIn -v`
Expected: PASS (all three subtests).

- [ ] **Step 5: Commit**

```bash
git add cmd/citadel/logsdaemon_service.go cmd/citadel/logsdaemon_service_test.go
git commit -m "feat(logs-daemon): resolve citadel-logs binary path"
```

---

### Task 3: Install the unit file (filesystem)

**Files:**
- Modify: `cmd/citadel/logsdaemon_service.go`
- Test: `cmd/citadel/logsdaemon_service_test.go`

**Interfaces:**
- Consumes: `renderUnit`, `unitOptions`, `unitRegistryPath`, `unitDBPath` (Task 1); `resolveLogsBinary` (Task 2).
- Produces:
  - `func dataDir() (string, error)` → `~/.local/share/citadel`
  - `func unitFilePath() (string, error)` → `~/.config/systemd/user/citadel-logs.service`
  - `func installUnit(addr, profile, region string) error` — resolves the binary, creates the data + unit directories, writes the rendered unit file (mode 0644).

- [ ] **Step 1: Write the failing test**

Append to `cmd/citadel/logsdaemon_service_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/citadel/ -run TestInstallUnit -v`
Expected: FAIL — `undefined: installUnit`.

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/citadel/logsdaemon_service.go`:

```go
func dataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "citadel"), nil
}

func unitFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", serviceName), nil
}

// installUnit resolves the daemon binary, ensures the data and unit
// directories exist, and writes the rendered unit file.
func installUnit(addr, profile, region string) error {
	bin, err := resolveLogsBinary()
	if err != nil {
		return err
	}
	dd, err := dataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dd, 0o755); err != nil {
		return err
	}
	unitPath, err := unitFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	unit := renderUnit(unitOptions{
		BinaryPath:   bin,
		RegistryPath: unitRegistryPath,
		DBPath:       unitDBPath,
		Addr:         addr,
		Profile:      profile,
		Region:       region,
	})
	return os.WriteFile(unitPath, []byte(unit), 0o644)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/citadel/ -run TestInstallUnit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/citadel/logsdaemon_service.go cmd/citadel/logsdaemon_service_test.go
git commit -m "feat(logs-daemon): install systemd unit file"
```

---

### Task 4: Lifecycle helpers (start/stop/restart) via injectable runner

**Files:**
- Modify: `cmd/citadel/logsdaemon_service.go`
- Test: `cmd/citadel/logsdaemon_service_test.go`

**Interfaces:**
- Consumes: `serviceName` (Task 1).
- Produces:
  - `type execRunner interface { Run(name string, args ...string) error; Output(name string, args ...string) (string, error) }`
  - `type osRunner struct{}` implementing `execRunner` (Run wires stdio; Output captures stdout).
  - `func startService(r execRunner) error` — `daemon-reload`, `enable --now`, then best-effort `loginctl enable-linger` (warns, never fails).
  - `func stopService(r execRunner, disable bool) error` — `stop`, optional `disable`.
  - `func restartService(r execRunner) error` — `daemon-reload`, `restart`.
  - `func enableLinger(r execRunner) error` — runs `loginctl enable-linger <current user>`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/citadel/logsdaemon_service_test.go`. Add `"errors"` if not already imported (Task 2 added it):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/citadel/ -run 'TestStartService|TestStopService|TestRestartService' -v`
Expected: FAIL — `undefined: startService` (and friends).

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/citadel/logsdaemon_service.go`. Update imports to add `os/user`:

```go
import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)
```

```go
type execRunner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) (string, error)
}

type osRunner struct{}

func (osRunner) Run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

func (osRunner) Output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func enableLinger(r execRunner) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	return r.Run("loginctl", "enable-linger", u.Username)
}

func startService(r execRunner) error {
	if err := r.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := r.Run("systemctl", "--user", "enable", "--now", serviceName); err != nil {
		return err
	}
	if err := enableLinger(r); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  could not enable linger (service won't start until login): %v\n", err)
	}
	return nil
}

func stopService(r execRunner, disable bool) error {
	if err := r.Run("systemctl", "--user", "stop", serviceName); err != nil {
		return err
	}
	if disable {
		return r.Run("systemctl", "--user", "disable", serviceName)
	}
	return nil
}

func restartService(r execRunner) error {
	if err := r.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return r.Run("systemctl", "--user", "restart", serviceName)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/citadel/ -run 'TestStartService|TestStopService|TestRestartService' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add cmd/citadel/logsdaemon_service.go cmd/citadel/logsdaemon_service_test.go
git commit -m "feat(logs-daemon): start/stop/restart lifecycle helpers"
```

---

### Task 5: Status and logs helpers

**Files:**
- Modify: `cmd/citadel/logsdaemon_service.go`
- Test: `cmd/citadel/logsdaemon_service_test.go`

**Interfaces:**
- Consumes: `serviceName` (Task 1).
- Produces:
  - `func logsArgs(lines int, follow bool) []string` — builds `journalctl` args. Always `--user -u citadel-logs.service`; appends `-n <lines>` when `lines > 0`; appends `-f` when `follow`.
  - `func formatStatus(state, registryPath string, count int) string` — multi-line status report. Empty `state` renders as `unknown`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/citadel/logsdaemon_service_test.go`:

```go
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
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/citadel/ -run 'TestLogsArgs|TestFormatStatus' -v`
Expected: FAIL — `undefined: logsArgs` / `undefined: formatStatus`.

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/citadel/logsdaemon_service.go` (add `"strconv"` to imports):

```go
func logsArgs(lines int, follow bool) []string {
	args := []string{"--user", "-u", serviceName}
	if lines > 0 {
		args = append(args, "-n", strconv.Itoa(lines))
	}
	if follow {
		args = append(args, "-f")
	}
	return args
}

func formatStatus(state, registryPath string, count int) string {
	if strings.TrimSpace(state) == "" {
		state = "unknown"
	}
	return fmt.Sprintf("citadel-logs: %s\ndashboard:    http://localhost:5500/logs\nregistered:   %d service(s) in %s\n",
		state, count, registryPath)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/citadel/ -run 'TestLogsArgs|TestFormatStatus' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/citadel/logsdaemon_service.go cmd/citadel/logsdaemon_service_test.go
git commit -m "feat(logs-daemon): status and logs helpers"
```

---

### Task 6: Wire cobra subcommands

**Files:**
- Modify: `cmd/citadel/logsdaemon_service.go`
- Modify: `cmd/citadel/logsdaemon.go:33-36` (register new subcommands in `newLogsDaemonCmd`)

**Interfaces:**
- Consumes: `installUnit` (Task 3); `startService`, `stopService`, `restartService`, `osRunner` (Task 4); `logsArgs`, `formatStatus` (Task 5); `defaultRegistryPath`, `loadOrEmpty` (existing in `logsdaemon.go`); `defaultDaemonAddr`, `serviceName` (Task 1).
- Produces:
  - `func ensureLinux() error`
  - `func newLogsDaemonStartCmd() *cobra.Command`
  - `func newLogsDaemonStopCmd() *cobra.Command`
  - `func newLogsDaemonRestartCmd() *cobra.Command`
  - `func newLogsDaemonStatusCmd() *cobra.Command`
  - `func newLogsDaemonLogsCmd() *cobra.Command`

- [ ] **Step 1: Add the subcommand constructors and Linux guard**

Add to `cmd/citadel/logsdaemon_service.go` (add `"runtime"` and `"github.com/spf13/cobra"` to imports):

```go
func ensureLinux() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("the systemd service is Linux-only; on this platform run: docker compose -f docker-compose.logs.yml up -d")
	}
	return nil
}

// resolveAWSEnv applies flag overrides, falling back to the current shell's
// AWS_PROFILE / AWS_REGION.
func resolveAWSEnv(profileFlag, regionFlag string) (profile, region string) {
	profile = profileFlag
	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	region = regionFlag
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	return profile, region
}

func newLogsDaemonStartCmd() *cobra.Command {
	var profile, region, addr string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Install and start the citadel-logs systemd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureLinux(); err != nil {
				return err
			}
			p, rg := resolveAWSEnv(profile, region)
			if err := installUnit(addr, p, rg); err != nil {
				return err
			}
			if err := startService(osRunner{}); err != nil {
				return err
			}
			fmt.Printf("✅ citadel-logs started — http://localhost:5500/logs\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile (defaults to $AWS_PROFILE)")
	cmd.Flags().StringVar(&region, "region", "", "AWS region (defaults to $AWS_REGION)")
	cmd.Flags().StringVar(&addr, "addr", defaultDaemonAddr, "HTTP listen address")
	return cmd
}

func newLogsDaemonStopCmd() *cobra.Command {
	var disable bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the citadel-logs service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureLinux(); err != nil {
				return err
			}
			if err := stopService(osRunner{}, disable); err != nil {
				return err
			}
			fmt.Printf("✅ citadel-logs stopped\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&disable, "disable", false, "Also disable autostart on boot")
	return cmd
}

func newLogsDaemonRestartCmd() *cobra.Command {
	var profile, region, addr string
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Re-install the unit and restart the service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureLinux(); err != nil {
				return err
			}
			p, rg := resolveAWSEnv(profile, region)
			if err := installUnit(addr, p, rg); err != nil {
				return err
			}
			if err := restartService(osRunner{}); err != nil {
				return err
			}
			fmt.Printf("✅ citadel-logs restarted — http://localhost:5500/logs\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile (defaults to $AWS_PROFILE)")
	cmd.Flags().StringVar(&region, "region", "", "AWS region (defaults to $AWS_REGION)")
	cmd.Flags().StringVar(&addr, "addr", defaultDaemonAddr, "HTTP listen address")
	return cmd
}

func newLogsDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the citadel-logs service is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureLinux(); err != nil {
				return err
			}
			// is-active exits non-zero when inactive; the stdout text is what we want.
			out, _ := osRunner{}.Output("systemctl", "--user", "is-active", serviceName)
			regPath := defaultRegistryPath()
			f, _ := loadOrEmpty(regPath)
			fmt.Print(formatStatus(out, regPath, len(f.Services)))
			return nil
		},
	}
}

func newLogsDaemonLogsCmd() *cobra.Command {
	var lines int
	var noFollow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Tail the citadel-logs service journal",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureLinux(); err != nil {
				return err
			}
			return osRunner{}.Run("journalctl", logsArgs(lines, !noFollow)...)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 0, "Number of past lines to show")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "Print and exit instead of following")
	return cmd
}
```

- [ ] **Step 2: Register the subcommands**

In `cmd/citadel/logsdaemon.go`, the existing `newLogsDaemonCmd` adds three subcommands. Add the five new ones right after them:

```go
	cmd.AddCommand(newLogsDaemonRegisterCmd())
	cmd.AddCommand(newLogsDaemonListCmd())
	cmd.AddCommand(newLogsDaemonUnregisterCmd())
	cmd.AddCommand(newLogsDaemonStartCmd())
	cmd.AddCommand(newLogsDaemonStopCmd())
	cmd.AddCommand(newLogsDaemonRestartCmd())
	cmd.AddCommand(newLogsDaemonStatusCmd())
	cmd.AddCommand(newLogsDaemonLogsCmd())
	return cmd
```

- [ ] **Step 3: Build and vet**

Run: `go build ./cmd/citadel && go vet ./cmd/citadel/`
Expected: no output (success). Fix any unused-import errors by reconciling the import block to exactly: `fmt`, `os`, `os/exec`, `os/user`, `path/filepath`, `runtime`, `strconv`, `strings`, `github.com/spf13/cobra`.

- [ ] **Step 4: Smoke-test the command surface**

Run: `go run ./cmd/citadel logs-daemon --help`
Expected: help output lists `start`, `stop`, `restart`, `status`, `logs` alongside `register`, `list`, `unregister`.

Run: `go run ./cmd/citadel logs-daemon status`
Expected (on Linux, service not yet installed): prints `citadel-logs: inactive` (or `unknown`), the dashboard URL, and a registered-service count — no crash.

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./cmd/citadel/ -v`
Expected: PASS (all tasks' tests).

- [ ] **Step 6: Commit**

```bash
git add cmd/citadel/logsdaemon_service.go cmd/citadel/logsdaemon.go
git commit -m "feat(logs-daemon): wire start/stop/restart/status/logs subcommands"
```

---

### Task 7: Install both binaries + document the service

**Files:**
- Modify: `Makefile` (the `install` and `update` targets)
- Modify: `README.md:59-78` (the "citadel-logs daemon → Run it" section)

**Interfaces:**
- Consumes: nothing.
- Produces: `make install` installs both `citadel` and `citadel-logs`; README documents the service commands.

- [ ] **Step 1: Make `install` install both binaries**

In `Makefile`, change the `install` target so it depends on `install-logs`:

```makefile
# Install globally
install: install-logs
	go install ./cmd/citadel
	@echo "Installed citadel to $(GOBIN)/citadel"
	@case ":$$PATH:" in *":$(GOBIN):"*) ;; *) echo "⚠️  $(GOBIN) is not on your PATH";; esac
```

And update the `update` target to reinstall both:

```makefile
# Pull latest and reinstall
update:
	git pull --ff-only
	go install ./cmd/citadel ./cmd/citadel-logs
	@echo "Updated citadel in $(GOBIN)"
```

- [ ] **Step 2: Verify the install target**

Run: `make install`
Expected: installs `citadel-logs` then `citadel`; `citadel-logs` is present at `$(go env GOPATH)/bin/citadel-logs`.

Run: `ls "$(go env GOPATH)/bin/citadel-logs"`
Expected: the path prints (file exists).

- [ ] **Step 3: Document the service in the README**

In `README.md`, replace the `### Run it` Docker block (lines ~59-78) with a native-service section. New content:

````markdown
### Run it as a service (Linux)

```bash
# 1. Install both binaries (citadel + citadel-logs)
make install

# 2. Register a repo
cd ~/Documents/github/my-backend
citadel logs-daemon register --env dev

# 3. Start the daemon as a systemd user service
citadel logs-daemon start

# 4. Open the dashboard
open http://localhost:5500/logs
```

`citadel logs-daemon start` installs a systemd `--user` unit
(`~/.config/systemd/user/citadel-logs.service`), enables it, and turns on user
lingering so it survives reboots and starts before you log in. It captures
`AWS_PROFILE` / `AWS_REGION` from your shell (override with `--profile` /
`--region`). The daemon stores its SQLite db under
`~/.local/share/citadel/citadel-logs.db` and serves the dashboard on
`127.0.0.1:5500`.

Other commands:

```bash
citadel logs-daemon status            # is it running?
citadel logs-daemon logs              # tail the journal (-n N, --no-follow)
citadel logs-daemon restart           # re-install unit + restart
citadel logs-daemon stop [--disable]  # stop (and optionally disable autostart)
```

On non-Linux platforms, use the Docker path instead:
`docker compose -f docker-compose.logs.yml up -d`.
````

- [ ] **Step 4: Final full check**

Run: `go test ./... && go vet ./...`
Expected: PASS, no vet warnings.

- [ ] **Step 5: Commit**

```bash
git add Makefile README.md
git commit -m "build,docs: install citadel-logs and document the systemd service"
```

---

## Notes for the implementer

- The five lifecycle subcommands' `RunE` bodies are intentionally thin and not
  unit-tested; every piece of logic they call (`installUnit`, `startService`,
  `stopService`, `restartService`, `logsArgs`, `formatStatus`) is tested in
  isolation. Verify the wiring with the Task 6 smoke tests instead.
- `systemctl --user is-active` exits non-zero when the service is inactive —
  that's why Task 6 ignores the error and uses stdout. Do not change it to
  treat the exit code as failure.
- Keep the import block in `logsdaemon_service.go` reconciled as you add tasks;
  the final set is: `fmt`, `os`, `os/exec`, `os/user`, `path/filepath`,
  `runtime`, `strconv`, `strings`, `github.com/spf13/cobra`.
