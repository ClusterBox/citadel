package main

import (
	"fmt"
	"os"

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
			fmt.Printf("🏰 Citadel Deploy\n")
			fmt.Printf("Config: %s\n", configPath)
			fmt.Printf("Environment: %s\n", environment)
			fmt.Printf("Dry run: %v\n", dryRun)
			
			// TODO: Implement deployment pipeline
			return fmt.Errorf("not implemented yet")
		},
	}

	var deployInfra bool
	deployCmd.Flags().BoolVar(&deployInfra, "deploy-infra", false, "Deploy/update CDK infrastructure")

	// sync-secrets command
	syncSecretsCmd := &cobra.Command{
		Use:   "sync-secrets",
		Short: "Sync secrets from .env to SSM Parameter Store",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("🔐 Syncing secrets to SSM\n")
			// TODO: Implement secret sync
			return fmt.Errorf("not implemented yet")
		},
	}

	// build command
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build and push Docker image to ECR",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("🐳 Building Docker image\n")
			// TODO: Implement Docker build/push
			return fmt.Errorf("not implemented yet")
		},
	}

	// status command
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show deployment status",
		RunE: func(cmd *cobra.Command, args []string) error {
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
