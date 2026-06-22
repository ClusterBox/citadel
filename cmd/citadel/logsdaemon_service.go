package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
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
