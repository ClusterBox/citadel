package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/ClusterBox/citadel/pkg/config"
)

func newBuiltin(name string) Step {
	switch name {
	case config.StepSSMSync:
		return ssmSyncStep{}
	case config.StepBuild:
		return buildStep{}
	case config.StepCDK:
		return cdkStep{}
	case config.StepDeploy:
		return deployStep{}
	case config.StepConfigSync:
		return &configSyncStep{}
	case config.StepWait:
		return waitStep{}
	}
	panic("unknown built-in step " + name) // config validation rejects these
}

// ssm-sync: sync declared secrets from the env file to SSM.
type ssmSyncStep struct{}

func (ssmSyncStep) Name() string { return config.StepSSMSync }
func (ssmSyncStep) Plan(_ context.Context, sc *StepContext, out io.Writer) (bool, error) {
	if sc.Opts.SkipSSM {
		fmt.Fprintf(out, "⏭️  Skipping SSM secret sync (--skip-ssm)\n\n")
		return true, nil
	}
	return sc.Opts.EnvFile == "", nil
}
func (ssmSyncStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	n, err := sc.ops.syncSecrets(ctx, w, sc.Cfg, sc.Opts)
	sc.SecretsUpdated = n
	return err
}

// build: build and push the image.
type buildStep struct{}

func (buildStep) Name() string { return config.StepBuild }
func (buildStep) Plan(context.Context, *StepContext, io.Writer) (bool, error) {
	return false, nil
}
func (buildStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	fmt.Fprintf(w, "🐳 Building Docker image...\n")
	uri, err := sc.ops.build(ctx, w, sc.Cfg, sc.Opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "✅ Image pushed: %s\n\n", uri)
	sc.setImage(uri)
	return nil
}
func (buildStep) wrapErr(err error) error { return fmt.Errorf("failed to build/push image: %w", err) }

// cdk: cdk deploy with imageTag=<sha> (only with --deploy-infra).
type cdkStep struct{}

func (cdkStep) Name() string         { return config.StepCDK }
func (cdkStep) changesService() bool { return true }
func (cdkStep) Plan(_ context.Context, sc *StepContext, _ io.Writer) (bool, error) {
	return !sc.Opts.DeployInfra, nil
}
func (cdkStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	fmt.Fprintf(w, "🏗️  Deploying CDK infrastructure...\n")
	if err := sc.ops.cdk(ctx, w, sc.Cfg, sc.Opts); err != nil {
		return err
	}
	fmt.Fprintf(w, "✅ Infrastructure deployed\n\n")
	sc.cdkRan = true
	return nil
}
func (cdkStep) wrapErr(err error) error {
	return fmt.Errorf("failed to deploy CDK infrastructure: %w", err)
}

// deploy: roll the service onto the image (ECS revision or Lambda update),
// unless an ECS cdk deploy already did. Opens the deployments.db record.
type deployStep struct{}

func (deployStep) Name() string         { return config.StepDeploy }
func (deployStep) changesService() bool { return true }
func (deployStep) Plan(ctx context.Context, sc *StepContext, out io.Writer) (bool, error) {
	if sc.Opts.DryRun {
		return true, nil
	}
	rolledByCDK := sc.cdkRan && sc.Cfg.ResolvedRuntime() == config.RuntimeECS &&
		cdkRolledService(ctx, out, sc)
	d, err := sc.ops.deployer(ctx, sc.Cfg)
	if err != nil {
		return false, fmt.Errorf("failed to create AWS client: %w", err)
	}
	sc.deployer = d
	sc.Record = sc.ops.recorder(ctx, out, sc.Cfg, sc.Opts, sc.ImageURI, sc.Target)
	if rolledByCDK {
		fmt.Fprintf(out, "⏭️  Skipping ECS update (rolled out by CDK)\n")
		return true, nil
	}
	return false, nil
}
func (deployStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	runtime := sc.Cfg.ResolvedRuntime()
	if sc.ImageURI == "" {
		return fmt.Errorf("no image to deploy: citadel/build did not run for %s", sc.Env)
	}
	fmt.Fprintf(w, "🚀 Deploying to %s...\n", runtime)
	if err := sc.deployer.Update(ctx, w, sc.Cfg, sc.Env, sc.ImageURI); err != nil {
		return fmt.Errorf("failed to update %s: %w", runtime, err)
	}
	return nil
}

// cdkRolledService decides, after an ECS `cdk deploy`, whether CDK really
// rolled the service onto the image. CloudFormation no-ops when the template
// is unchanged, so citadel rolls the service itself whenever secrets changed,
// the service is on another image, or that cannot be verified.
func cdkRolledService(ctx context.Context, out io.Writer, sc *StepContext) bool {
	if sc.SecretsUpdated > 0 {
		fmt.Fprintf(out, "🔁 Secrets changed; rolling the service so tasks pick them up\n")
		return false
	}
	runs, err := sc.ops.serviceRunsImage(ctx, sc.Cfg, sc.Env, sc.ImageURI)
	if err != nil {
		fmt.Fprintf(out, "⚠️  could not verify the CDK rollout (%v); rolling the service with citadel\n", err)
		return false
	}
	if !runs {
		fmt.Fprintf(out, "🔁 CDK left the service on another image; rolling it to %s\n", sc.ImageURI)
		return false
	}
	return true
}

// config-sync: apply env:/secret manifest to the Lambda function (dry run:
// print the diff). dryRun is captured in Plan so wrapErr can pick the same
// message the pre-engine code used for each mode.
type configSyncStep struct{ dryRun bool }

func (*configSyncStep) Name() string { return config.StepConfigSync }
func (s *configSyncStep) Plan(_ context.Context, sc *StepContext, out io.Writer) (bool, error) {
	s.dryRun = sc.Opts.DryRun
	if !sc.Opts.SkipConfig {
		return false, nil
	}
	if !sc.Opts.DryRun {
		fmt.Fprintf(out, "⏭️  Skipping function config sync (--skip-config)\n")
	}
	return true, nil
}
func (s *configSyncStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	if err := sc.ops.configSync(ctx, w, sc.Cfg, sc.Env, s.dryRun); err != nil {
		return err
	}
	if s.dryRun {
		fmt.Fprintf(w, "\n")
	}
	return nil
}
func (s *configSyncStep) wrapErr(err error) error {
	if s.dryRun {
		return fmt.Errorf("failed to diff function config: %w", err)
	}
	return fmt.Errorf("failed to sync function config: %w", err)
}

// wait: wait for the rollout to stabilize (only with --wait).
type waitStep struct{}

func (waitStep) Name() string { return config.StepWait }
func (waitStep) Plan(ctx context.Context, sc *StepContext, _ io.Writer) (bool, error) {
	if sc.Opts.DryRun || !sc.Opts.Wait {
		return true, nil
	}
	if sc.deployer == nil {
		d, err := sc.ops.deployer(ctx, sc.Cfg)
		if err != nil {
			return false, fmt.Errorf("failed to create AWS client: %w", err)
		}
		sc.deployer = d
	}
	return false, nil
}
func (waitStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	return sc.deployer.WaitStable(ctx, w, sc.Cfg, sc.Env)
}
func (waitStep) wrapErr(err error) error { return fmt.Errorf("deployment did not stabilize: %w", err) }
func (waitStep) after(sc *StepContext, out io.Writer) {
	if !sc.Opts.DryRun {
		fmt.Fprintf(out, "\n")
	}
}
