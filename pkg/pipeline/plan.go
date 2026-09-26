package pipeline

import "github.com/ClusterBox/citadel/pkg/config"

// planSteps turns citadel.yml's (resolved) pipeline into executable steps.
func planSteps(cfg *config.DeployConfig) []plannedStep {
	var steps []plannedStep
	for _, s := range cfg.ResolvedPipeline() {
		var st Step
		switch s.Kind() {
		case config.KindBuiltin:
			st = newBuiltin(s.Builtin())
		case config.KindRun:
			st = newRunStep(s)
		case config.KindTask:
			st = newTaskStep(s)
		case config.KindHTTP:
			st = newHTTPStep(s)
		}
		steps = append(steps, plannedStep{Step: st, opts: stepOptions{
			envs: s.Envs, continueOnError: s.ContinueOnError, rollback: s.RollbackOnFailure,
		}})
	}
	return steps
}

func needsSnapshot(steps []plannedStep) bool {
	for _, s := range steps {
		if s.opts.rollback {
			return true
		}
	}
	return false
}
