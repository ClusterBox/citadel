package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ClusterBox/citadel/pkg/pipeline"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "citadel",
		Short: "Declarative CDK deployment framework for AWS ECS/Fargate",
		Long: `Citadel makes container deployments as simple as the Serverless Framework
experience — but targeting ECS/Fargate instead of Lambda.

Single source of truth: citadel.yml defines everything about your deployment.`,
		Version: version,
	}

	// Global flags
	var configPath string
	var environment string
	var dryRun bool

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "citadel.yml", "Path to citadel.yml")
	rootCmd.PersistentFlags().StringVarP(&environment, "env", "e", "", "Target environment (dev/prod)")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without executing")

	// deploy command
	deployCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Full deployment pipeline (sync + infra? + build + deploy)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "" {
				return fmt.Errorf("--env is required")
			}

			ctx := context.Background()
			
			envFile, _ := cmd.Flags().GetString("env-file")
			deployInfra, _ := cmd.Flags().GetBool("deploy-infra")
			wait, _ := cmd.Flags().GetBool("wait")

			opts := &pipeline.DeployOptions{
				ConfigPath:  configPath,
				Environment: environment,
				EnvFile:     envFile,
				DeployInfra: deployInfra,
				DryRun:      dryRun,
				Wait:        wait,
			}

			return pipeline.Deploy(ctx, opts)
		},
	}

	deployCmd.Flags().String("env-file", ".env", "Path to .env file")
	deployCmd.Flags().Bool("deploy-infra", false, "Deploy/update CDK infrastructure")
	deployCmd.Flags().Bool("wait", false, "Wait for deployment to stabilize")

	// sync-secrets command
	syncSecretsCmd := &cobra.Command{
		Use:   "sync-secrets",
		Short: "Sync secrets from .env to SSM Parameter Store",
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "" {
				return fmt.Errorf("--env is required")
			}

			fmt.Printf("🔐 Syncing secrets to SSM\n")
			// TODO: Call SSM sync directly
			return fmt.Errorf("not implemented yet - use 'deploy' command")
		},
	}

	// build command
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build and push Docker image to ECR",
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "" {
				return fmt.Errorf("--env is required")
			}

			fmt.Printf("🐳 Building Docker image\n")
			// TODO: Call Docker build directly
			return fmt.Errorf("not implemented yet - use 'deploy' command")
		},
	}

	// status command
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show deployment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "" {
				return fmt.Errorf("--env is required")
			}

			fmt.Printf("📊 Deployment Status\n")
			// TODO: Implement status check
			return fmt.Errorf("not implemented yet")
		},
	}

	// logs command
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "Stream CloudWatch logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "" {
				return fmt.Errorf("--env is required")
			}

			fmt.Printf("📜 Streaming logs\n")
			// TODO: Implement log streaming
			return fmt.Errorf("not implemented yet")
		},
	}

	var tailLines int
	logsCmd.Flags().IntVar(&tailLines, "tail", 100, "Number of lines to show initially")

	// Add commands
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(syncSecretsCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(logsCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
