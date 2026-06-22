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
