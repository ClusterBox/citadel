package pipeline

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

func taskCtx(t *testing.T) (*StepContext, *aws.TaskSpec) {
	sc := runCtx(t)
	sc.Cfg = &config.DeployConfig{Name: "demo"}
	sc.ImageURI = "r:abc"
	var got aws.TaskSpec
	sc.ops = &ops{runTask: func(_ context.Context, _ io.Writer, _ *config.DeployConfig, env, image string, spec aws.TaskSpec) error {
		if env != "dev" || image != "r:abc" {
			t.Fatalf("env=%s image=%s", env, image)
		}
		got = spec
		return nil
	}}
	return sc, &got
}

func TestTaskStep_ScriptExpandsVars(t *testing.T) {
	sc, got := taskCtx(t)
	s := newTaskStep(config.PipelineStep{Name: "migrate", Task: config.TaskCommand{Script: "migrate --env ${env}"}, Timeout: "15m"})
	if err := s.Run(context.Background(), sc, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Command, []string{"sh", "-c", "migrate --env dev"}) || got.Timeout != 15*time.Minute {
		t.Fatalf("spec = %+v", *got)
	}
}

func TestTaskStep_ListFormAndContainer(t *testing.T) {
	sc, got := taskCtx(t)
	s := newTaskStep(config.PipelineStep{Name: "m", Task: config.TaskCommand{Args: []string{"node", "migrate.js", "${env}"}}, Container: "app"})
	if err := s.Run(context.Background(), sc, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Command, []string{"node", "migrate.js", "dev"}) || got.Container != "app" {
		t.Fatalf("spec = %+v", *got)
	}
}

func TestTaskStep_NoImageFails(t *testing.T) {
	sc, _ := taskCtx(t)
	sc.ImageURI = ""
	err := newTaskStep(config.PipelineStep{Name: "m", Task: config.TaskCommand{Script: "x"}}).Run(context.Background(), sc, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "citadel/build did not run for dev") {
		t.Fatalf("err = %v", err)
	}
}

func TestTaskStep_DryRun(t *testing.T) {
	sc, got := taskCtx(t)
	sc.Opts.DryRun = true
	var out bytes.Buffer
	if err := newTaskStep(config.PipelineStep{Name: "m", Task: config.TaskCommand{Script: "migrate"}}).Run(context.Background(), sc, &out); err != nil {
		t.Fatal(err)
	}
	if got.Command != nil || !strings.Contains(out.String(), "[dry-run] Would run task: migrate") {
		t.Fatalf("spec=%+v out=%q", *got, out.String())
	}
}

func TestTaskStep_ContainerExpandsVars(t *testing.T) {
	sc, got := taskCtx(t)
	s := newTaskStep(config.PipelineStep{Name: "m", Task: config.TaskCommand{Script: "migrate"}, Container: "${name}-${env}"})
	if err := s.Run(context.Background(), sc, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got.Container != "demo-dev" {
		t.Fatalf("container = %q, want demo-dev", got.Container)
	}
}
