package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

var defaultOps = ops{
	syncSecrets:      syncSecrets,
	build:            buildAndPushImage,
	cdk:              deployCDK,
	serviceRunsImage: serviceRunsImage,
	deployer: func(ctx context.Context, cfg *config.DeployConfig) (Deployer, error) {
		c, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return nil, err
		}
		return selectDeployer(cfg, c), nil
	},
	configSync: func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env string, dryRun bool) error {
		c, err := aws.NewClient(ctx, cfg.Region)
		if err != nil {
			return fmt.Errorf("failed to create AWS client: %w", err)
		}
		return syncLambdaConfig(ctx, w, c.NewLambdaClient(), cfg, env, dryRun)
	},
	recorder: deployRecorder,
	snapshot: snapshotDeployment,
	rollback: rollbackDeployment,
	runTask:  runTask,
}

// snapshotDeployment returns what runs now: the ECS task-definition ARN or
// the Lambda function's image URI.
func snapshotDeployment(ctx context.Context, cfg *config.DeployConfig, env string) (string, error) {
	c, err := aws.NewClient(ctx, cfg.Region)
	if err != nil {
		return "", err
	}
	if cfg.ResolvedRuntime() == config.RuntimeLambda {
		return c.NewLambdaClient().FunctionImage(ctx, cfg.ResolveFunctionName(env))
	}
	return c.NewECSClient().CurrentTaskDefinitionARN(ctx, cfg, env)
}

// rollbackDeployment puts the service back on the snapshot and waits for it.
func rollbackDeployment(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, before string) error {
	c, err := aws.NewClient(ctx, cfg.Region)
	if err != nil {
		return err
	}
	if cfg.ResolvedRuntime() == config.RuntimeLambda {
		lc := c.NewLambdaClient()
		fn := cfg.ResolveFunctionName(env)
		if err := lc.UpdateFunctionCode(ctx, fn, before); err != nil {
			return err
		}
		fmt.Fprintf(w, "✅ Function %s set back to %s\n", fn, before)
		return lc.WaitForFunctionUpdated(ctx, fn)
	}
	ec := c.NewECSClient()
	if err := ec.RollbackService(ctx, w, cfg, env, before); err != nil {
		return err
	}
	return ec.WaitForStableService(ctx, w, cfg, env)
}

// runTask runs a pipeline task: step as a one-off ECS task.
func runTask(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, imageURI string, spec aws.TaskSpec) error {
	c, err := aws.NewClient(ctx, cfg.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS client: %w", err)
	}
	return c.NewECSClient().RunOneOffTask(ctx, w, c.NewLogsClient(), cfg, env, imageURI, spec)
}
