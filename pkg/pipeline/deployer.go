package pipeline

import (
	"context"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

// Deployer performs the runtime-specific "update to new image" step and an
// optional wait for the deployment to stabilize.
type Deployer interface {
	Update(ctx context.Context, cfg *config.DeployConfig, env, imageURI string) error
	WaitStable(ctx context.Context, cfg *config.DeployConfig, env string) error
}

// ecsDeployer forces a new ECS deployment (the task definition already
// references the :latest image just pushed).
type ecsDeployer struct{ c *aws.ECSClient }

func (d ecsDeployer) Update(ctx context.Context, cfg *config.DeployConfig, _, _ string) error {
	return d.c.UpdateService(ctx, cfg)
}

func (d ecsDeployer) WaitStable(ctx context.Context, cfg *config.DeployConfig, _ string) error {
	return d.c.WaitForStableService(ctx, cfg)
}

// lambdaDeployer points the function at the freshly pushed image.
type lambdaDeployer struct{ c *aws.LambdaClient }

func (d lambdaDeployer) Update(ctx context.Context, cfg *config.DeployConfig, env, imageURI string) error {
	return d.c.UpdateFunctionCode(ctx, cfg.ResolveFunctionName(env), imageURI)
}

func (d lambdaDeployer) WaitStable(ctx context.Context, cfg *config.DeployConfig, env string) error {
	return d.c.WaitForFunctionUpdated(ctx, cfg.ResolveFunctionName(env))
}

// selectDeployer returns the Deployer matching cfg's resolved runtime.
func selectDeployer(cfg *config.DeployConfig, awsClient *aws.Client) Deployer {
	if cfg.ResolvedRuntime() == config.RuntimeLambda {
		return lambdaDeployer{c: awsClient.NewLambdaClient()}
	}
	return ecsDeployer{c: awsClient.NewECSClient()}
}
