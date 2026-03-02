package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
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
			skipSSM, _ := cmd.Flags().GetBool("skip-ssm")
			wait, _ := cmd.Flags().GetBool("wait")
			streamLogs, _ := cmd.Flags().GetBool("stream-logs")
			tailLines, _ := cmd.Flags().GetInt("tail")

			opts := &pipeline.DeployOptions{
				ConfigPath:  configPath,
				Environment: environment,
				EnvFile:     envFile,
				DeployInfra: deployInfra,
				SkipSSM:     skipSSM,
				DryRun:      dryRun,
				Wait:        wait,
				StreamLogs:  streamLogs,
				TailLines:   tailLines,
			}

			return pipeline.Deploy(ctx, opts)
		},
	}

	deployCmd.Flags().String("env-file", ".env", "Path to .env file")
	deployCmd.Flags().Bool("deploy-infra", false, "Deploy/update CDK infrastructure")
	deployCmd.Flags().Bool("skip-ssm", false, "Skip syncing secrets to SSM Parameter Store")
	deployCmd.Flags().Bool("wait", false, "Wait for deployment to stabilize")
	deployCmd.Flags().Bool("stream-logs", false, "Stream CloudWatch logs after deployment")
	deployCmd.Flags().Int("tail", 100, "Number of log lines to show initially")

	// sync-secrets command
	syncSecretsCmd := &cobra.Command{
		Use:   "sync-secrets",
		Short: "Sync secrets from .env to SSM Parameter Store",
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "" {
				return fmt.Errorf("--env is required")
			}

			ctx := context.Background()

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			envFile, _ := cmd.Flags().GetString("env-file")

			fmt.Printf("🔐 Syncing secrets to SSM Parameter Store...\n")

			awsClient, err := aws.NewClient(ctx, cfg.Region)
			if err != nil {
				return fmt.Errorf("failed to create AWS client: %w", err)
			}

			result, err := awsClient.SyncSecrets(ctx, cfg, envFile, dryRun)
			if err != nil {
				return fmt.Errorf("failed to sync secrets: %w", err)
			}

			fmt.Printf("   Updated: %d parameters\n", result.Updated)
			fmt.Printf("   Skipped: %d parameters (unchanged)\n", result.Skipped)
			if len(result.Missing) > 0 {
				fmt.Printf("   Missing: %v\n", result.Missing)
			}

			fmt.Printf("✅ Secret sync complete\n")
			return nil
		},
	}

	syncSecretsCmd.Flags().String("env-file", ".env", "Path to .env file")

	// build command
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build and push Docker image to ECR",
		RunE: func(cmd *cobra.Command, args []string) error {
			if environment == "" {
				return fmt.Errorf("--env is required")
			}

			ctx := context.Background()

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			fmt.Printf("🐳 Building and pushing Docker image...\n")

			opts := &pipeline.DeployOptions{
				ConfigPath:  configPath,
				Environment: environment,
				DryRun:      dryRun,
			}

			imageURI, err := pipeline.BuildAndPush(ctx, cfg, opts)
			if err != nil {
				return fmt.Errorf("build failed: %w", err)
			}

			fmt.Printf("✅ Image pushed: %s\n", imageURI)
			return nil
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

			ctx := context.Background()

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			fmt.Printf("📊 Deployment Status — %s (%s)\n\n", cfg.Name, environment)

			awsClient, err := aws.NewClient(ctx, cfg.Region)
			if err != nil {
				return fmt.Errorf("failed to create AWS client: %w", err)
			}

			// ECS service status
			fmt.Printf("🚀 ECS Service:\n")
			ecsClient := awsClient.NewECSClient()
			if err := ecsClient.GetServiceStatus(ctx, cfg); err != nil {
				fmt.Printf("   Error: %v\n", err)
			}

			// Recent logs
			fmt.Printf("\n📜 Recent Logs:\n")
			logsClient := awsClient.NewLogsClient()
			if err := logsClient.GetRecentLogs(ctx, cfg, 10); err != nil {
				fmt.Printf("   Error: %v\n", err)
			}

			return nil
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

			ctx := context.Background()

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			tailLines, _ := cmd.Flags().GetInt("tail")

			fmt.Printf("📜 Streaming logs for %s (%s)\n\n", cfg.Name, environment)

			awsClient, err := aws.NewClient(ctx, cfg.Region)
			if err != nil {
				return fmt.Errorf("failed to create AWS client: %w", err)
			}

			logsClient := awsClient.NewLogsClient()
			return logsClient.StreamLogs(ctx, cfg, tailLines)
		},
	}

	logsCmd.Flags().Int("tail", 100, "Number of log lines to show initially")

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
