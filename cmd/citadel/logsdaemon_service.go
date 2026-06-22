package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
