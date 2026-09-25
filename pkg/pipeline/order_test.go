package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

func writeTestConfig(t *testing.T, runtime string) string {
	t.Helper()
	body := "name: demo\nregion: us-east-1\nruntime: " + runtime + "\nenvironments:\n  dev:\n    account: \"111111111111\"\n"
	if runtime == "ecs" {
		body += "container:\n  port: 3000\n  cpu: 256\n  memory: 512\nsecrets:\n  - DATABASE_URL\n"
	}
	path := filepath.Join(t.TempDir(), "citadel.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type stageLog struct {
	calls       []string
	rolledByCDK bool
	buildErr    error
}

func (l *stageLog) stages() stages {
	return stages{
		syncSecrets: func(context.Context, io.Writer, *config.DeployConfig, *DeployOptions) error {
			l.calls = append(l.calls, "ssm-sync")
			return nil
		},
		build: func(context.Context, io.Writer, *config.DeployConfig, *DeployOptions) (string, error) {
			l.calls = append(l.calls, "build")
			return "111111111111.dkr.ecr.us-east-1.amazonaws.com/demo-dev-repo:abc1234", l.buildErr
		},
		cdk: func(context.Context, io.Writer, *config.DeployConfig, *DeployOptions) error {
			l.calls = append(l.calls, "cdk")
			return nil
		},
		rollout: func(_ context.Context, _ io.Writer, _ *project.Run, _ *config.DeployConfig, _ *DeployOptions, _, _ string, rolledByCDK bool) error {
			l.calls = append(l.calls, "rollout")
			l.rolledByCDK = rolledByCDK
			return nil
		},
	}
}

type runRecord struct {
	Status string `json:"status"`
	Steps  []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"steps"`
}

func readRun(t *testing.T, runDir string) runRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r runRecord
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func onlyRun(t *testing.T, configPath string) runRecord {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(filepath.Dir(configPath), ".citadel", "runs", "*"))
	if len(dirs) != 1 {
		t.Fatalf("want exactly one run, found %v", dirs)
	}
	return readRun(t, dirs[0])
}

func stepSummary(r runRecord) []string {
	var out []string
	for _, s := range r.Steps {
		out = append(out, s.Name+":"+s.Status)
	}
	return out
}

func deployOpts(configPath string, infra bool) *DeployOptions {
	return &DeployOptions{ConfigPath: configPath, Environment: "dev", EnvFile: ".env", DeployInfra: infra, Message: "m", Out: io.Discard}
}

func TestDeployOrder_ECSWithInfra_BuildsBeforeCDKAndCDKRollsOut(t *testing.T) {
	cfgPath := writeTestConfig(t, "ecs")
	var l stageLog
	if err := deployWith(context.Background(), deployOpts(cfgPath, true), l.stages()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ssm-sync", "build", "cdk", "rollout"}; !reflect.DeepEqual(l.calls, want) {
		t.Fatalf("calls = %v, want %v", l.calls, want)
	}
	if !l.rolledByCDK {
		t.Fatal("ECS with --deploy-infra must tell rollout that CDK already rolled the service")
	}
	r := onlyRun(t, cfgPath)
	if want := []string{"ssm-sync:success", "build:success", "cdk:success"}; !reflect.DeepEqual(stepSummary(r), want) {
		t.Fatalf("run steps = %v, want %v", stepSummary(r), want)
	}
}

func TestDeployOrder_ECSWithoutInfra_RolloutUpdatesService(t *testing.T) {
	cfgPath := writeTestConfig(t, "ecs")
	var l stageLog
	if err := deployWith(context.Background(), deployOpts(cfgPath, false), l.stages()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ssm-sync", "build", "rollout"}; !reflect.DeepEqual(l.calls, want) {
		t.Fatalf("calls = %v, want %v", l.calls, want)
	}
	if l.rolledByCDK {
		t.Fatal("without --deploy-infra citadel must roll the service itself")
	}
	if want := []string{"ssm-sync:success", "build:success", "cdk:skipped"}; !reflect.DeepEqual(stepSummary(onlyRun(t, cfgPath)), want) {
		t.Fatalf("run steps = %v", stepSummary(onlyRun(t, cfgPath)))
	}
}

func TestDeployOrder_LambdaWithInfra_StillUpdatesFunction(t *testing.T) {
	cfgPath := writeTestConfig(t, "lambda")
	var l stageLog
	if err := deployWith(context.Background(), deployOpts(cfgPath, true), l.stages()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ssm-sync", "build", "cdk", "rollout"}; !reflect.DeepEqual(l.calls, want) {
		t.Fatalf("calls = %v, want %v", l.calls, want)
	}
	if l.rolledByCDK {
		t.Fatal("CDK never rolls a Lambda's image; citadel must still UpdateFunctionCode")
	}
}

func TestDeployOrder_BuildFailureNeverRunsCDK(t *testing.T) {
	cfgPath := writeTestConfig(t, "ecs")
	l := stageLog{buildErr: errors.New("docker daemon unreachable")}
	err := deployWith(context.Background(), deployOpts(cfgPath, true), l.stages())
	if err == nil || !strings.Contains(err.Error(), "docker daemon unreachable") {
		t.Fatalf("err = %v", err)
	}
	if want := []string{"ssm-sync", "build"}; !reflect.DeepEqual(l.calls, want) {
		t.Fatalf("calls = %v: CDK must not run against an image that was never pushed", l.calls)
	}
	if r := onlyRun(t, cfgPath); r.Status != "failed" {
		t.Fatalf("run status = %s", r.Status)
	}
}

func TestRollout_ECSRolledByCDK_SkipsDeployStepButRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	d, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	run, err := d.NewRun(project.RunInfo{Env: "dev"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.DeployConfig{Name: "demo", Region: "us-east-1"}
	opts := &DeployOptions{Environment: "dev", Message: "m"}

	if err := rollout(context.Background(), &out, run, cfg, opts, "repo:abc1234", "demo-dev-service", true); err != nil {
		t.Fatal(err)
	}
	run.Finish(nil, project.State{})

	if !strings.Contains(out.String(), "⏭️  Skipping ECS update (rolled out by CDK)") {
		t.Fatalf("output = %q", out.String())
	}
	if want := []string{"deploy:skipped", "wait:skipped"}; !reflect.DeepEqual(stepSummary(readRun(t, run.Dir())), want) {
		t.Fatalf("run steps = %v", stepSummary(readRun(t, run.Dir())))
	}
	if _, err := os.Stat(filepath.Join(home, ".citadel", "deployments.db")); err != nil {
		t.Fatalf("deploy history not recorded: %v", err)
	}
}
