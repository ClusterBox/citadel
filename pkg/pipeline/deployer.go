package pipeline

import (
	"context"
	"io"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

// Deployer performs the runtime-specific "update to new image" step and an
// optional wait for the deployment to stabilize, writing progress to w.
type Deployer interface {
	Update(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, imageURI string) error
	WaitStable(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env string) error
}

// ecsDeployer rolls the ECS service onto the pushed image by registering a new
// task-definition revision (see aws.ECSClient.DeployImage).
type ecsDeployer struct{ c *aws.ECSClient }

func (d ecsDeployer) Update(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, imageURI string) error {
	return d.c.DeployImage(ctx, w, cfg, env, imageURI)
}

func (d ecsDeployer) WaitStable(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env string) error {
	return d.c.WaitForStableService(ctx, w, cfg, env)
}

// lambdaDeployer points the function at the freshly pushed image.
type lambdaDeployer struct{ c *aws.LambdaClient }

func (d lambdaDeployer) Update(ctx context.Context, _ io.Writer, cfg *config.DeployConfig, env, imageURI string) error {
	return d.c.UpdateFunctionCode(ctx, cfg.ResolveFunctionName(env), imageURI)
}

func (d lambdaDeployer) WaitStable(ctx context.Context, _ io.Writer, cfg *config.DeployConfig, env string) error {
	return d.c.WaitForFunctionUpdated(ctx, cfg.ResolveFunctionName(env))
}

// selectDeployer returns the Deployer matching cfg's resolved runtime.
func selectDeployer(cfg *config.DeployConfig, awsClient *aws.Client) Deployer {
	if cfg.ResolvedRuntime() == config.RuntimeLambda {
		return lambdaDeployer{c: awsClient.NewLambdaClient()}
	}
	return ecsDeployer{c: awsClient.NewECSClient()}
}
