package pipeline

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

// taskStep runs a one-off ECS task in the freshly built image.
type taskStep struct {
	name      string
	cmd       config.TaskCommand
	container string
	timeout   time.Duration
}

func newTaskStep(s config.PipelineStep) *taskStep {
	return &taskStep{name: s.StepName(), cmd: s.Task, container: s.Container, timeout: s.TimeoutOrDefault()}
}

func (s *taskStep) Name() string { return s.name }

func (s *taskStep) Plan(context.Context, *StepContext, io.Writer) (bool, error) { return false, nil }

func (s *taskStep) Run(ctx context.Context, sc *StepContext, w io.Writer) error {
	var command []string
	for _, part := range s.cmd.Command() {
		expanded, err := config.ExpandVars(part, sc.Vars)
		if err != nil {
			return err
		}
		command = append(command, expanded)
	}
	container, err := config.ExpandVars(s.container, sc.Vars)
	if err != nil {
		return err
	}
	shown := strings.Join(command, " ")
	if s.cmd.Script != "" {
		shown = command[2]
	}
	fmt.Fprintf(w, "▶️  %s: %s\n", s.name, shown)
	if sc.Opts.DryRun {
		fmt.Fprintf(w, "   [dry-run] Would run task: %s\n", shown)
		return nil
	}
	if sc.ImageURI == "" {
		return fmt.Errorf("no image to run: citadel/build did not run for %s", sc.Env)
	}
	return sc.ops.runTask(ctx, w, sc.Cfg, sc.Env, sc.ImageURI, aws.TaskSpec{
		Command: command, Container: container, Timeout: s.timeout,
	})
}
