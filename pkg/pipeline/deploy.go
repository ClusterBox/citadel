package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"time"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/internal/deploydb"
	"github.com/ClusterBox/citadel/internal/docker"
	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

// DeployOptions configures a deployment pipeline run
type DeployOptions struct {
	ConfigPath  string
	Environment string
	EnvFile     string
	DeployInfra bool
	SkipSSM     bool
	SkipConfig  bool
	DryRun      bool
	StreamLogs  bool
	Wait        bool
	TailLines   int
	Message     string
	Out         io.Writer // where progress is written; nil means os.Stdout
	Version     string    // citadel version, recorded in .citadel/project.yml
}

// out returns the writer progress goes to (os.Stdout unless Out is set).
func (o *DeployOptions) out() io.Writer {
	if o.Out != nil {
		return o.Out
	}
	return os.Stdout
}

// Deploy executes the full deployment pipeline. Every stage runs as a named
// step of a project.Run (see openRun): non-dry-run deploys leave per-step logs
// under .citadel/runs/ next to citadel.yml and update .citadel/state/<env>.json.
func Deploy(ctx context.Context, opts *DeployOptions) (retErr error) {
	out := opts.out()

	// 1. Load config
	fmt.Fprintf(out, "🏰 Citadel Deploy Pipeline\n\n")
	fmt.Fprintf(out, "📋 Loading configuration from %s...\n", opts.ConfigPath)

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate environment exists
	envCfg, err := cfg.GetEnv(opts.Environment)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "   Project: %s\n", cfg.Name)
	fmt.Fprintf(out, "   Environment: %s\n", opts.Environment)
	fmt.Fprintf(out, "   Region: %s\n", cfg.Region)
	fmt.Fprintf(out, "   Account: %s\n", envCfg.Account)
	fmt.Fprintf(out, "\n")

	gitSHA, _ := getGitSHA()
	run := openRun(opts, cfg, gitSHA)
	state := project.State{
		Target:     resolveTarget(cfg, opts.Environment),
		DeployedBy: currentUser(),
	}
	// Finish is idempotent: this records the outcome on every early return.
	defer func() { run.Finish(retErr, state) }()

	// 2. Sync secrets to SSM (unless --skip-ssm)
	if !opts.SkipSSM && opts.EnvFile != "" {
		step := run.Step("ssm-sync")
		err := syncSecrets(ctx, step.Out(), cfg, opts)
		step.End(err)
		if err != nil {
			return err
		}
	} else {
		if opts.SkipSSM {
			fmt.Fprintf(out, "⏭️  Skipping SSM secret sync (--skip-ssm)\n\n")
		}
		run.Skip("ssm-sync")
	}

	// 3. Deploy CDK infrastructure (if requested)
	if opts.DeployInfra {
		step := run.Step("cdk")
		fmt.Fprintf(step.Out(), "🏗️  Deploying CDK infrastructure...\n")
		err := deployCDK(ctx, step.Out(), cfg, opts)
		if err == nil {
			fmt.Fprintf(step.Out(), "✅ Infrastructure deployed\n\n")
		}
		step.End(err)
		if err != nil {
			return fmt.Errorf("failed to deploy CDK infrastructure: %w", err)
		}
	} else {
		run.Skip("cdk")
	}

	// 4. Build and push Docker image
	step := run.Step("build")
	fmt.Fprintf(step.Out(), "🐳 Building Docker image...\n")
	imageTag, err := buildAndPushImage(ctx, step.Out(), cfg, opts)
	if err == nil {
		fmt.Fprintf(step.Out(), "✅ Image pushed: %s\n\n", imageTag)
	}
	step.End(err)
	if err != nil {
		return fmt.Errorf("failed to build/push image: %w", err)
	}
	state.ImageURI = imageTag

	// 5. Update the running service (runtime-specific) and record the deploy.
	if !opts.DryRun {
		if err := rollout(ctx, out, run, cfg, opts, imageTag, state.Target); err != nil {
			return err
		}
	}

	if opts.DryRun && cfg.ResolvedRuntime() == config.RuntimeLambda && !opts.SkipConfig {
		awsClient, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client: %w", err)
		}
		if err := syncLambdaConfig(ctx, out, awsClient.NewLambdaClient(), cfg, opts.Environment, true); err != nil {
			return fmt.Errorf("failed to diff function config: %w", err)
		}
		fmt.Fprintf(out, "\n")
	}

	fmt.Fprintf(out, "✨ Deployment complete!\n")
	// Record success now: log streaming below blocks until Ctrl-C.
	run.Finish(nil, state)

	// 6. Stream logs if requested
	if opts.StreamLogs && !opts.DryRun {
		fmt.Fprintf(out, "\n📜 Streaming CloudWatch logs (Ctrl+C to exit)...\n\n")

		awsClient, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client for logs: %w", err)
		}

		tailLines := opts.TailLines
		if tailLines <= 0 {
			tailLines = 100
		}

		logGroup, err := awsClient.NewECSClient().DiscoverLogGroup(ctx, cfg, opts.Environment)
		if err != nil {
			return fmt.Errorf("failed to resolve log group: %w", err)
		}

		logsClient := awsClient.NewLogsClient()
		return logsClient.StreamLogs(ctx, logGroup, tailLines)
	}

	return nil
}

// openRun returns the Run that records this deploy under .citadel/ next to
// the config file. Dry runs, and any failure to use .citadel/ (read-only
// checkout, permissions), get a terminal-only Run: run logging is best-effort
// and never blocks a deploy.
func openRun(opts *DeployOptions, cfg *config.DeployConfig, gitSHA string) *project.Run {
	out := opts.out()
	if opts.DryRun {
		return project.TerminalRun(out)
	}
	dir, err := project.Open(filepath.Dir(opts.ConfigPath))
	if err == nil {
		_, _, err = dir.EnsureProject(project.NewMeta(cfg.Name, string(cfg.ResolvedRuntime()), opts.Version, time.Now()))
	}
	var run *project.Run
	if err == nil {
		run, err = dir.NewRun(project.RunInfo{Env: opts.Environment, Message: opts.Message, GitSHA: gitSHA}, out)
	}
	if err != nil {
		fmt.Fprintf(out, "⚠️  run logging disabled: %v\n\n", err)
		return project.TerminalRun(out)
	}
	return run
}

// syncSecrets syncs the declared secrets from the env file to SSM.
func syncSecrets(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions) error {
	fmt.Fprintf(w, "🔐 Syncing secrets to SSM Parameter Store...\n")

	awsClient, err := aws.NewClient(ctx, cfg.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS client: %w", err)
	}

	result, err := awsClient.SyncSecrets(ctx, cfg, opts.Environment, opts.EnvFile, opts.DryRun)
	if err != nil {
		return fmt.Errorf("failed to sync secrets: %w", err)
	}

	fmt.Fprintf(w, "   Updated: %d parameters\n", result.Updated)
	fmt.Fprintf(w, "   Skipped: %d parameters (unchanged)\n", result.Skipped)
	if len(result.Missing) > 0 {
		fmt.Fprintf(w, "   ⚠️  Missing: %v\n", result.Missing)
		return fmt.Errorf("missing required secrets")
	}
	fmt.Fprintf(w, "\n")
	return nil
}

// rollout updates the running service, syncs Lambda config and optionally
// waits for stability, recording the deploy in ~/.citadel/deployments.db.
// Steps: deploy, config-sync (lambda only), wait.
func rollout(ctx context.Context, out io.Writer, run *project.Run, cfg *config.DeployConfig, opts *DeployOptions, imageURI, target string) error {
	runtime := cfg.ResolvedRuntime()
	step := run.Step("deploy")
	w := step.Out()
	fmt.Fprintf(w, "🚀 Deploying to %s...\n", runtime)

	awsClient, err := aws.NewClient(ctx, cfg.Region)
	if err != nil {
		err = fmt.Errorf("failed to create AWS client: %w", err)
		step.End(err)
		return err
	}

	finish := deployRecorder(ctx, w, cfg, opts, imageURI, target)

	deployer := selectDeployer(cfg, awsClient)
	if err := deployer.Update(ctx, w, cfg, opts.Environment, imageURI); err != nil {
		finish(err)
		err = fmt.Errorf("failed to update %s: %w", runtime, err)
		step.End(err)
		return err
	}
	step.End(nil)

	if runtime == config.RuntimeLambda {
		if opts.SkipConfig {
			fmt.Fprintf(out, "⏭️  Skipping function config sync (--skip-config)\n")
			run.Skip("config-sync")
		} else {
			step := run.Step("config-sync")
			err := syncLambdaConfig(ctx, step.Out(), awsClient.NewLambdaClient(), cfg, opts.Environment, false)
			step.End(err)
			if err != nil {
				finish(err)
				return fmt.Errorf("failed to sync function config: %w", err)
			}
		}
	}

	if opts.Wait {
		step := run.Step("wait")
		err := deployer.WaitStable(ctx, step.Out(), cfg, opts.Environment)
		step.End(err)
		if err != nil {
			finish(err)
			return fmt.Errorf("deployment did not stabilize: %w", err)
		}
	} else {
		run.Skip("wait")
	}

	finish(nil)
	fmt.Fprintf(out, "\n")
	return nil
}

// currentUser returns the local username recorded as the deployer.
func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}

// deployCDK deploys the CDK infrastructure
func deployCDK(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions) error {
	// Find CDK directory (should be in cdk/ relative to config)
	configDir := filepath.Dir(opts.ConfigPath)
	cdkDir := filepath.Join(configDir, "cdk")

	// Check if cdk directory exists
	if _, err := os.Stat(cdkDir); os.IsNotExist(err) {
		return fmt.Errorf("CDK directory not found: %s", cdkDir)
	}

	// Pin the rolled task definition to the immutable <sha> image instead of
	// :latest, matching the construct's imageTag context. The same SHA tags the
	// image pushed in buildAndPushImage (same commit, same value).
	gitSHA, err := getGitSHA()
	if err != nil {
		return fmt.Errorf("failed to get git SHA: %w", err)
	}

	// Run cdk deploy
	cmd := exec.CommandContext(ctx, "cdk", "deploy",
		"--context", fmt.Sprintf("env=%s", opts.Environment),
		"--context", fmt.Sprintf("imageTag=%s", gitSHA),
		"--require-approval", "never",
	)
	cmd.Dir = cdkDir
	cmd.Stdout = w
	cmd.Stderr = w

	if opts.DryRun {
		fmt.Fprintf(w, "   [dry-run] Would run: cdk deploy --context env=%s --context imageTag=%s\n", opts.Environment, gitSHA)
		return nil
	}

	return cmd.Run()
}

// BuildAndPush is the exported entry point for the standalone build command
func BuildAndPush(ctx context.Context, cfg *config.DeployConfig, opts *DeployOptions) (string, error) {
	return buildAndPushImage(ctx, opts.out(), cfg, opts)
}

// buildAndPushImage builds and pushes the Docker image to ECR
func buildAndPushImage(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions) (string, error) {
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

	// Build image tags. The ECR repo is env-namespaced ("<name>-<env>-repo") so
	// dev and prod images stay isolated, matching the CDK construct.
	repoName := fmt.Sprintf("%s-repo", cfg.ResolvedName(opts.Environment))
	ecrURI := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", accountID, cfg.Region, repoName)
	imageTag := fmt.Sprintf("%s:%s", repoName, gitSHA)
	imageURI := fmt.Sprintf("%s:%s", ecrURI, gitSHA)
	latestURI := fmt.Sprintf("%s:latest", ecrURI)

	if opts.DryRun {
		fmt.Fprintf(w, "   [dry-run] Would build: %s\n", imageTag)
		fmt.Fprintf(w, "   [dry-run] Would push: %s\n", imageURI)
		fmt.Fprintf(w, "   [dry-run] Would push: %s\n", latestURI)
		return imageURI, nil
	}

	// Build image
	fmt.Fprintf(w, "   Building image: %s\n", imageTag)
	if _, err := dockerClient.Build(ctx, w, cfg, contextPath, imageTag); err != nil {
		return "", err
	}

	// Tag for ECR
	fmt.Fprintf(w, "   Tagging: %s → %s\n", imageTag, imageURI)
	if err := dockerClient.Tag(ctx, imageTag, imageURI); err != nil {
		return "", err
	}

	fmt.Fprintf(w, "   Tagging: %s → %s\n", imageTag, latestURI)
	if err := dockerClient.Tag(ctx, imageTag, latestURI); err != nil {
		return "", err
	}

	// Push to ECR
	fmt.Fprintf(w, "   Pushing: %s\n", imageURI)
	if err := dockerClient.Push(ctx, w, awsClient.ECR, imageURI); err != nil {
		return "", err
	}

	fmt.Fprintf(w, "   Pushing: %s\n", latestURI)
	if err := dockerClient.Push(ctx, w, awsClient.ECR, latestURI); err != nil {
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
	return fmt.Sprintf("%s-service", cfg.ResolvedName(env))
}

// deployRecorder opens the local deployment-history DB and returns a finish
// func that marks the deploy success or failed. All failures degrade
// gracefully (warn, no-op) so history never blocks a deploy.
func deployRecorder(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions, imageURI, target string) func(err error) {
	noop := func(error) {}
	dbPath, err := deploydb.DefaultPath()
	if err != nil {
		fmt.Fprintf(w, "   ⚠️  deployment history disabled: %v\n", err)
		return noop
	}
	db, err := deploydb.Open(dbPath)
	if err != nil {
		fmt.Fprintf(w, "   ⚠️  deployment history disabled: %v\n", err)
		return noop
	}

	who := currentUser()
	gitSHA, _ := getGitSHA()

	id, err := db.Insert(ctx, deploydb.Deployment{
		Project: cfg.Name, Env: opts.Environment, Runtime: string(cfg.ResolvedRuntime()),
		Region: cfg.Region, GitSHA: gitSHA, ImageURI: imageURI, Message: opts.Message,
		DeployedBy: who, Target: target,
	})
	if err != nil {
		fmt.Fprintf(w, "   ⚠️  could not record deployment: %v\n", err)
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
