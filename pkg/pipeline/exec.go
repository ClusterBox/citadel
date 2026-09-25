package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

// execute runs steps in order, recording each in run, and returns the error
// that stopped the pipeline (nil on success). sc.Record, once a step has set
// it, is always called with the final outcome — also on panic.
func execute(ctx context.Context, sc *StepContext, run *project.Run, out io.Writer, steps []plannedStep) (retErr error) {
	defer func() {
		if p := recover(); p != nil {
			if sc.Record != nil {
				sc.Record(fmt.Errorf("panic: %v", p))
			}
			panic(p)
		}
		if sc.Record != nil {
			sc.Record(retErr)
		}
	}()

	for _, ps := range steps {
		name := ps.Name()
		if !ps.opts.appliesTo(sc.Env) {
			fmt.Fprintf(out, "⏭️  Skipping %s (not for %s)\n", name, sc.Env)
			run.Skip(name)
			continue
		}
		skip, err := ps.Plan(ctx, sc, out)
		if err != nil {
			return err
		}
		if skip {
			run.Skip(name)
			runAfter(ps, sc, out)
			continue
		}
		if c, ok := ps.Step.(serviceChanger); ok && c.changesService() {
			takeSnapshot(ctx, sc, out)
		}

		step := run.Step(name)
		stepErr := ps.Run(ctx, sc, step.Out())
		step.End(stepErr)
		if stepErr != nil {
			wrapped := stepErr
			if w, ok := ps.Step.(errWrapper); ok {
				wrapped = w.wrapErr(stepErr)
			}
			switch {
			case ps.opts.continueOnError:
				fmt.Fprintf(out, "⚠️  %s failed; continuing: %v\n", name, wrapped)
				continue
			case ps.opts.rollback:
				return rollbackAfter(ctx, sc, run, fmt.Errorf("%s: %w", name, wrapped))
			default:
				return wrapped
			}
		}
		runAfter(ps, sc, out)
	}
	return nil
}

func runAfter(ps plannedStep, sc *StepContext, out io.Writer) {
	if a, ok := ps.Step.(afterer); ok {
		a.after(sc, out)
	}
}

// takeSnapshot records what runs before the first service-changing step, but
// only when some step may need to roll back (so default pipelines make no
// extra AWS call).
func takeSnapshot(ctx context.Context, sc *StepContext, out io.Writer) {
	if !sc.needSnapshot || sc.snapshotTaken || sc.Opts.DryRun {
		return
	}
	sc.snapshotTaken = true
	before, err := sc.ops.snapshot(ctx, sc.Cfg, sc.Env)
	if err != nil {
		fmt.Fprintf(out, "⚠️  could not record the current deployment for rollback: %v\n", err)
		return
	}
	sc.Before = before
}

// rollbackAfter runs the automatic rollback as its own recorded step and
// returns the failure that triggered it (plus the rollback error, if any).
func rollbackAfter(ctx context.Context, sc *StepContext, run *project.Run, cause error) error {
	step := run.Step(config.StepRollback)
	w := step.Out()
	var rbErr error
	if sc.Before == "" {
		rbErr = errors.New("cannot roll back: no snapshot of the previous deployment")
	} else {
		fmt.Fprintf(w, "↩️  Rolling back to %s\n", sc.Before)
		rbErr = sc.ops.rollback(ctx, w, sc.Cfg, sc.Env, sc.Before)
	}
	step.End(rbErr)
	if rbErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback failed: %w", rbErr))
	}
	return fmt.Errorf("%w (rolled back to %s)", cause, sc.Before)
}
