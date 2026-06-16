package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/internal/deploydb"
	"github.com/ClusterBox/citadel/internal/docker"
	"github.com/ClusterBox/citadel/pkg/config"
)

// DeployOptions configures a deployment pipeline run
type DeployOptions struct {
	ConfigPath  string
	Environment string
	EnvFile     string
	DeployInfra bool
	SkipSSM     bool
	DryRun      bool
	StreamLogs  bool
	Wait        bool
	TailLines   int
	Message     string
}

// Deploy executes the full deployment pipeline
func Deploy(ctx context.Context, opts *DeployOptions) error {
	// 1. Load config
	fmt.Printf("🏰 Citadel Deploy Pipeline\n\n")
	fmt.Printf("📋 Loading configuration from %s...\n", opts.ConfigPath)
	
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate environment exists
	envCfg, err := cfg.GetEnv(opts.Environment)
	if err != nil {
		return err
	}

	fmt.Printf("   Project: %s\n", cfg.Name)
	fmt.Printf("   Environment: %s\n", opts.Environment)
	fmt.Printf("   Region: %s\n", cfg.Region)
	fmt.Printf("   Account: %s\n", envCfg.Account)
	fmt.Printf("\n")

	// 2. Sync secrets to SSM (unless --skip-ssm)
	if !opts.SkipSSM && opts.EnvFile != "" {
		fmt.Printf("🔐 Syncing secrets to SSM Parameter Store...\n")

		awsClient, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client: %w", err)
		}

		result, err := awsClient.SyncSecrets(ctx, cfg, opts.EnvFile, opts.DryRun)
		if err != nil {
			return fmt.Errorf("failed to sync secrets: %w", err)
		}

		fmt.Printf("   Updated: %d parameters\n", result.Updated)
		fmt.Printf("   Skipped: %d parameters (unchanged)\n", result.Skipped)
		if len(result.Missing) > 0 {
			fmt.Printf("   ⚠️  Missing: %v\n", result.Missing)
			return fmt.Errorf("missing required secrets")
		}
		fmt.Printf("\n")
	} else if opts.SkipSSM {
		fmt.Printf("⏭️  Skipping SSM secret sync (--skip-ssm)\n\n")
	}

	// 3. Deploy CDK infrastructure (if requested)
	if opts.DeployInfra {
		fmt.Printf("🏗️  Deploying CDK infrastructure...\n")
		
		if err := deployCDK(ctx, cfg, opts); err != nil {
			return fmt.Errorf("failed to deploy CDK infrastructure: %w", err)
		}
		
		fmt.Printf("✅ Infrastructure deployed\n\n")
	}

	// 4. Build and push Docker image
	fmt.Printf("🐳 Building Docker image...\n")
	
	imageTag, err := buildAndPushImage(ctx, cfg, opts)
	if err != nil {
		return fmt.Errorf("failed to build/push image: %w", err)
	}
	
	fmt.Printf("✅ Image pushed: %s\n\n", imageTag)

	// 5. Update the running service (runtime-specific) and record the deploy.
	if !opts.DryRun {
		runtime := cfg.ResolvedRuntime()
		fmt.Printf("🚀 Deploying to %s...\n", runtime)

		awsClient, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client: %w", err)
		}

		target := resolveTarget(cfg, opts.Environment)
		finish := deployRecorder(ctx, cfg, opts, imageTag, target)

		deployer := selectDeployer(cfg, awsClient)
		if err := deployer.Update(ctx, cfg, opts.Environment, imageTag); err != nil {
			finish(err)
			return fmt.Errorf("failed to update %s: %w", runtime, err)
		}
		if opts.Wait {
			if err := deployer.WaitStable(ctx, cfg, opts.Environment); err != nil {
				finish(err)
				return fmt.Errorf("deployment did not stabilize: %w", err)
			}
		}
		finish(nil)
		fmt.Printf("\n")
	}

	fmt.Printf("✨ Deployment complete!\n")

	// 6. Stream logs if requested
	if opts.StreamLogs && !opts.DryRun {
		fmt.Printf("\n📜 Streaming CloudWatch logs (Ctrl+C to exit)...\n\n")

		awsClient, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client for logs: %w", err)
		}

		tailLines := opts.TailLines
		if tailLines <= 0 {
			tailLines = 100
		}

		logGroup, err := awsClient.NewECSClient().DiscoverLogGroup(ctx, cfg)
		if err != nil {
			return fmt.Errorf("failed to resolve log group: %w", err)
		}

		logsClient := awsClient.NewLogsClient()
		return logsClient.StreamLogs(ctx, logGroup, tailLines)
	}

	return nil
}

// deployCDK deploys the CDK infrastructure
func deployCDK(ctx context.Context, cfg *config.DeployConfig, opts *DeployOptions) error {
	// Find CDK directory (should be in cdk/ relative to config)
	configDir := filepath.Dir(opts.ConfigPath)
	cdkDir := filepath.Join(configDir, "cdk")

	// Check if cdk directory exists
	if _, err := os.Stat(cdkDir); os.IsNotExist(err) {
		return fmt.Errorf("CDK directory not found: %s", cdkDir)
	}

	// Run cdk deploy
	cmd := exec.CommandContext(ctx, "cdk", "deploy",
		"--context", fmt.Sprintf("env=%s", opts.Environment),
		"--require-approval", "never",
	)
	cmd.Dir = cdkDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if opts.DryRun {
		fmt.Printf("   [dry-run] Would run: cdk deploy --context env=%s\n", opts.Environment)
		return nil
	}

	return cmd.Run()
}

// BuildAndPush is the exported entry point for the standalone build command
func BuildAndPush(ctx context.Context, cfg *config.DeployConfig, opts *DeployOptions) (string, error) {
	return buildAndPushImage(ctx, cfg, opts)
}

// buildAndPushImage builds and pushes the Docker image to ECR
func buildAndPushImage(ctx context.Context, cfg *config.DeployConfig, opts *DeployOptions) (string, error) {
	// Create Docker client
	dockerClient, err := docker.NewClient(ctx)
	if err != nil {
		return "", err
	}
	defer dockerClient.Close()

	// Get git SHA for tagging
	gitSHA, err := getGitSHA()
	if err != nil {
		return "", fmt.Errorf("failed to get git SHA: %w", err)
	}

	// Determine context path (directory containing citadel.yml)
	contextPath := filepath.Dir(opts.ConfigPath)
	
	// Get AWS account ID
	awsClient, err := aws.NewClient(ctx, cfg.Region)
	if err != nil {
		return "", err
	}

	accountID, err := getAWSAccountID(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get AWS account ID: %w", err)
	}

	// Build image tags
	repoName := fmt.Sprintf("%s-repo", cfg.Name)
	ecrURI := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", accountID, cfg.Region, repoName)
	imageTag := fmt.Sprintf("%s:%s", repoName, gitSHA)
	imageURI := fmt.Sprintf("%s:%s", ecrURI, gitSHA)
	latestURI := fmt.Sprintf("%s:latest", ecrURI)

	if opts.DryRun {
		fmt.Printf("   [dry-run] Would build: %s\n", imageTag)
		fmt.Printf("   [dry-run] Would push: %s\n", imageURI)
		fmt.Printf("   [dry-run] Would push: %s\n", latestURI)
		return imageURI, nil
	}

	// Build image
	fmt.Printf("   Building image: %s\n", imageTag)
	if _, err := dockerClient.Build(ctx, cfg, contextPath, imageTag); err != nil {
		return "", err
	}

	// Tag for ECR
	fmt.Printf("   Tagging: %s → %s\n", imageTag, imageURI)
	if err := dockerClient.Tag(ctx, imageTag, imageURI); err != nil {
		return "", err
	}

	fmt.Printf("   Tagging: %s → %s\n", imageTag, latestURI)
	if err := dockerClient.Tag(ctx, imageTag, latestURI); err != nil {
		return "", err
	}

	// Push to ECR
	fmt.Printf("   Pushing: %s\n", imageURI)
	if err := dockerClient.Push(ctx, awsClient.ECR, imageURI); err != nil {
		return "", err
	}

	fmt.Printf("   Pushing: %s\n", latestURI)
	if err := dockerClient.Push(ctx, awsClient.ECR, latestURI); err != nil {
		return "", err
	}

	return imageURI, nil
}

// getGitSHA returns the current git commit SHA (short)
func getGitSHA() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output[:len(output)-1]), nil // Remove trailing newline
}

// getAWSAccountID returns the current AWS account ID
func getAWSAccountID(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output[:len(output)-1]), nil // Remove trailing newline
}

// resolveTarget returns the human-facing deploy target: the Lambda function
// name for lambda runtime, otherwise the ECS service name.
func resolveTarget(cfg *config.DeployConfig, env string) string {
	if cfg.ResolvedRuntime() == config.RuntimeLambda {
		return cfg.ResolveFunctionName(env)
	}
	if cfg.ECS != nil && cfg.ECS.Service != "" {
		return cfg.ECS.Service
	}
	return fmt.Sprintf("%s-service", cfg.Name)
}

// deployRecorder opens the local deployment-history DB and returns a finish
// func that marks the deploy success or failed. All failures degrade
// gracefully (warn, no-op) so history never blocks a deploy.
func deployRecorder(ctx context.Context, cfg *config.DeployConfig, opts *DeployOptions, imageURI, target string) func(err error) {
	noop := func(error) {}
	dbPath, err := deploydb.DefaultPath()
	if err != nil {
		fmt.Printf("   ⚠️  deployment history disabled: %v\n", err)
		return noop
	}
	db, err := deploydb.Open(dbPath)
	if err != nil {
		fmt.Printf("   ⚠️  deployment history disabled: %v\n", err)
		return noop
	}

	who := "unknown"
	if u, uerr := user.Current(); uerr == nil {
		who = u.Username
	}
	gitSHA, _ := getGitSHA()

	id, err := db.Insert(ctx, deploydb.Deployment{
		Project: cfg.Name, Env: opts.Environment, Runtime: string(cfg.ResolvedRuntime()),
		Region: cfg.Region, GitSHA: gitSHA, ImageURI: imageURI, Message: opts.Message,
		DeployedBy: who, Target: target,
	})
	if err != nil {
		fmt.Printf("   ⚠️  could not record deployment: %v\n", err)
		db.Close()
		return noop
	}

	return func(deployErr error) {
		defer db.Close()
		// Detach from ctx: on a failed/timed-out deploy the original context is
		// often already cancelled, which would make the mark statements no-op
		// and leave the row stuck at in_progress forever.
		markCtx := context.WithoutCancel(ctx)
		if deployErr != nil {
			_ = db.MarkFailed(markCtx, id, deployErr.Error())
			return
		}
		_ = db.MarkSuccess(markCtx, id)
	}
}
