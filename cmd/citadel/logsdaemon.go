package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ClusterBox/citadel/internal/registry"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// defaultRegistryPath is where `citadel logs-daemon register` will read/write
// when the user does not pass --registry. The daemon expects this same file
// mounted into the container at /etc/citadel/registry.yml.
func defaultRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".citadel/registry.yml"
	}
	return filepath.Join(home, ".citadel", "registry.yml")
}

func newLogsDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs-daemon",
		Short: "Manage the citadel-logs daemon registry",
		Long: `Manage which services the citadel-logs daemon observes.

The daemon reads ~/.citadel/registry.yml on startup (and hot-reloads on
change). This subcommand group is a thin convenience over editing that file.`,
	}
	cmd.AddCommand(newLogsDaemonRegisterCmd())
	cmd.AddCommand(newLogsDaemonListCmd())
	cmd.AddCommand(newLogsDaemonUnregisterCmd())
	return cmd
}

func newLogsDaemonRegisterCmd() *cobra.Command {
	var env, repo, regPath string
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Add the current repo + env to the daemon registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			if env == "" {
				return fmt.Errorf("--env is required")
			}
			if repo == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				repo = wd
			}
			path := regPath
			if path == "" {
				path = defaultRegistryPath()
			}
			f, err := loadOrEmpty(path)
			if err != nil {
				return err
			}
			for _, e := range f.Services {
				if e.Repo == repo && e.Env == env {
					fmt.Printf("Already registered: %s (%s)\n", repo, env)
					return nil
				}
			}
			f.Services = append(f.Services, registry.Entry{Repo: repo, Env: env})
			if err := writeRegistry(path, f); err != nil {
				return err
			}
			fmt.Printf("✅ Registered %s (%s) in %s\n", repo, env, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "env", "", "Environment (dev|prod)")
	cmd.Flags().StringVar(&repo, "repo", "", "Path to repo (defaults to current directory)")
	cmd.Flags().StringVar(&regPath, "registry", "", "Path to registry.yml (defaults to ~/.citadel/registry.yml)")
	return cmd
}

func newLogsDaemonListCmd() *cobra.Command {
	var regPath string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List services in the daemon registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := regPath
			if path == "" {
				path = defaultRegistryPath()
			}
			f, err := loadOrEmpty(path)
			if err != nil {
				return err
			}
			if len(f.Services) == 0 {
				fmt.Printf("(empty: %s)\n", path)
				return nil
			}
			fmt.Printf("Registry: %s\n\n", path)
			for _, e := range f.Services {
				fmt.Printf("  - %s  (env=%s)\n", e.Repo, e.Env)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&regPath, "registry", "", "Path to registry.yml")
	return cmd
}

func newLogsDaemonUnregisterCmd() *cobra.Command {
	var env, repo, regPath string
	cmd := &cobra.Command{
		Use:   "unregister",
		Short: "Remove a repo+env from the daemon registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			if env == "" {
				return fmt.Errorf("--env is required")
			}
			if repo == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				repo = wd
			}
			path := regPath
			if path == "" {
				path = defaultRegistryPath()
			}
			f, err := loadOrEmpty(path)
			if err != nil {
				return err
			}
			kept := f.Services[:0]
			removed := false
			for _, e := range f.Services {
				if e.Repo == repo && e.Env == env {
					removed = true
					continue
				}
				kept = append(kept, e)
			}
			f.Services = kept
			if !removed {
				fmt.Printf("Not registered: %s (%s)\n", repo, env)
				return nil
			}
			if err := writeRegistry(path, f); err != nil {
				return err
			}
			fmt.Printf("✅ Unregistered %s (%s)\n", repo, env)
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "env", "", "Environment (dev|prod)")
	cmd.Flags().StringVar(&repo, "repo", "", "Path to repo (defaults to current directory)")
	cmd.Flags().StringVar(&regPath, "registry", "", "Path to registry.yml")
	return cmd
}

func loadOrEmpty(path string) (*registry.File, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &registry.File{}, nil
	}
	return registry.LoadFile(path)
}

func writeRegistry(path string, f *registry.File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
