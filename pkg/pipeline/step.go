package pipeline

import (
	"context"
	"io"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

// Step is one entry of a deploy pipeline.
type Step interface {
	Name() string
	// Plan runs right before the step and decides whether it runs. It may
	// print to out and perform read-only checks (e.g. deploy's CDK-rollout
	// check). true means skip: the step is recorded as skipped.
	Plan(ctx context.Context, sc *StepContext, out io.Writer) (bool, error)
	// Run performs the step, writing to w (terminal + the step's log file).
	Run(ctx context.Context, sc *StepContext, w io.Writer) error
}

// errWrapper wraps a step's error for the pipeline-level error while the
// step's run.json record keeps the raw error (matching pre-engine output).
type errWrapper interface{ wrapErr(error) error }

// afterer runs after a step succeeds or is skipped by Plan.
type afterer interface {
	after(sc *StepContext, out io.Writer)
}

// serviceChanger marks steps that change the running service (cdk, deploy);
// the rollback snapshot is taken before the first one that runs.
type serviceChanger interface{ changesService() bool }

// stepOptions are the citadel.yml settings shared by all step kinds.
type stepOptions struct {
	envs            []string
	continueOnError bool
	rollback        bool
}

func (o stepOptions) appliesTo(env string) bool {
	if len(o.envs) == 0 {
		return true
	}
	for _, e := range o.envs {
		if e == env {
			return true
		}
	}
	return false
}

type plannedStep struct {
	Step
	opts stepOptions
}

// StepContext is the state shared by the steps of one deploy.
type StepContext struct {
	Cfg            *config.DeployConfig
	Opts           *DeployOptions
	Env            string
	Vars           map[string]string
	ImageURI       string
	SecretsUpdated int
	Target         string
	// Before is the rollback snapshot: the ECS task-definition ARN or the
	// Lambda image URI running before this deploy ("" when none was taken).
	Before string
	// Record closes the ~/.citadel/deployments.db row; set by the deploy step.
	Record func(error)

	cdkRan        bool
	deployer      Deployer
	needSnapshot  bool
	snapshotTaken bool
	state         *project.State
	ops           *ops
}

// setImage records the pushed image for later steps, ${image} and state.
func (sc *StepContext) setImage(uri string) {
	sc.ImageURI = uri
	if sc.Vars != nil {
		sc.Vars["image"] = uri
	}
	if sc.state != nil {
		sc.state.ImageURI = uri
	}
}

// ops are the side effects steps perform. Production values are defaultOps;
// tests substitute fakes so no AWS, Docker or CDK is touched.
type ops struct {
	syncSecrets      func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions) (int, error)
	build            func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions) (string, error)
	cdk              func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions) error
	serviceRunsImage func(ctx context.Context, cfg *config.DeployConfig, env, imageURI string) (bool, error)
	deployer         func(ctx context.Context, cfg *config.DeployConfig) (Deployer, error)
	configSync       func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env string, dryRun bool) error
	recorder         func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, opts *DeployOptions, imageURI, target string) func(error)
	snapshot         func(ctx context.Context, cfg *config.DeployConfig, env string) (string, error)
	rollback         func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, before string) error
	runTask          func(ctx context.Context, w io.Writer, cfg *config.DeployConfig, env, imageURI string, spec aws.TaskSpec) error
}
