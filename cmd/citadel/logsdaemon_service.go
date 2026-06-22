package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
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
