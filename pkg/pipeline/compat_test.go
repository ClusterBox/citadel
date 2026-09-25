package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

const testImage = "111111111111.dkr.ecr.us-east-1.amazonaws.com/demo-dev-repo:abc1234"

type fakeDeployer struct{ l *fakeOps }

func (d fakeDeployer) Update(_ context.Context, w io.Writer, _ *config.DeployConfig, _, image string) error {
	d.l.calls = append(d.l.calls, "update:"+image)
	fmt.Fprint(w, "[deploy]\n")
	return d.l.updateErr
}

func (d fakeDeployer) WaitStable(_ context.Context, w io.Writer, _ *config.DeployConfig, _ string) error {
	d.l.calls = append(d.l.calls, "wait")
	fmt.Fprint(w, "[wait]\n")
	return nil
}

type fakeOps struct {
	calls          []string
	secretsUpdated int
	runsImage      bool
	runsImageErr   error
	buildErr       error
	updateErr      error
	recorded       []error
}

func (l *fakeOps) ops() *ops {
	return &ops{
		syncSecrets: func(_ context.Context, w io.Writer, _ *config.DeployConfig, _ *DeployOptions) (int, error) {
			l.calls = append(l.calls, "ssm-sync")
			fmt.Fprint(w, "[ssm-sync]\n")
			return l.secretsUpdated, nil
		},
		build: func(_ context.Context, w io.Writer, _ *config.DeployConfig, _ *DeployOptions) (string, error) {
			l.calls = append(l.calls, "build")
			fmt.Fprint(w, "[build]\n")
			return testImage, l.buildErr
		},
		cdk: func(_ context.Context, w io.Writer, _ *config.DeployConfig, _ *DeployOptions) error {
			l.calls = append(l.calls, "cdk")
			fmt.Fprint(w, "[cdk]\n")
			return nil
		},
		serviceRunsImage: func(context.Context, *config.DeployConfig, string, string) (bool, error) {
			l.calls = append(l.calls, "check")
			return l.runsImage, l.runsImageErr
		},
		deployer: func(context.Context, *config.DeployConfig) (Deployer, error) { return fakeDeployer{l}, nil },
		configSync: func(_ context.Context, w io.Writer, _ *config.DeployConfig, _ string, dryRun bool) error {
			l.calls = append(l.calls, fmt.Sprintf("config-sync:%v", dryRun))
			fmt.Fprint(w, "[config-sync]\n")
			return nil
		},
		recorder: func(context.Context, io.Writer, *config.DeployConfig, *DeployOptions, string, string) func(error) {
			l.calls = append(l.calls, "record")
			return func(err error) { l.recorded = append(l.recorded, err) }
		},
		snapshot: func(context.Context, *config.DeployConfig, string) (string, error) { return "td:7", nil },
		rollback: func(context.Context, io.Writer, *config.DeployConfig, string, string) error {
			l.calls = append(l.calls, "rollback")
			return nil
		},
		runTask: func(_ context.Context, w io.Writer, _ *config.DeployConfig, _, _ string, spec aws.TaskSpec) error {
			l.calls = append(l.calls, "task")
			return nil
		},
	}
}

func compatConfig(t *testing.T, runtime, extra string) string {
	t.Helper()
	body := "name: demo\nregion: us-east-1\nruntime: " + runtime + "\nenvironments:\n  dev:\n    account: \"111111111111\"\n"
	if runtime == "ecs" {
		body += "container:\n  port: 3000\n  cpu: 256\n  memory: 512\nsecrets:\n  - DATABASE_URL\n"
	}
	path := filepath.Join(t.TempDir(), "citadel.yml")
	if err := os.WriteFile(path, []byte(body+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func header(cfgPath string) string {
	return "🏰 Citadel Deploy Pipeline\n\n📋 Loading configuration from " + cfgPath + "...\n" +
		"   Project: demo\n   Environment: dev\n   Region: us-east-1\n   Account: 111111111111\n\n"
}

const buildBlock = "🐳 Building Docker image...\n[build]\n✅ Image pushed: " + testImage + "\n\n"
const cdkBlock = "🏗️  Deploying CDK infrastructure...\n[cdk]\n✅ Infrastructure deployed\n\n"

type compatCase struct {
	name, runtime string
	opts          DeployOptions
	l             fakeOps
	wantOut       string // after the header
	wantSteps     []string
}

func TestDeploy_DefaultPipelineMatchesPreEngineOutput(t *testing.T) {
	cases := []compatCase{
		{
			name: "ecs env-file no-infra wait", runtime: "ecs",
			opts:      DeployOptions{EnvFile: ".env", Wait: true},
			wantOut:   "[ssm-sync]\n" + buildBlock + "🚀 Deploying to ecs...\n[deploy]\n[wait]\n\n✨ Deployment complete!\n",
			wantSteps: []string{"ssm-sync:success", "build:success", "cdk:skipped", "deploy:success", "wait:success"},
		},
		{
			name: "ecs skip-ssm infra rolled-by-cdk", runtime: "ecs",
			opts:      DeployOptions{SkipSSM: true, DeployInfra: true},
			l:         fakeOps{runsImage: true},
			wantOut:   "⏭️  Skipping SSM secret sync (--skip-ssm)\n\n" + buildBlock + cdkBlock + "⏭️  Skipping ECS update (rolled out by CDK)\n\n✨ Deployment complete!\n",
			wantSteps: []string{"ssm-sync:skipped", "build:success", "cdk:success", "deploy:skipped", "wait:skipped"},
		},
		{
			name: "ecs infra secrets changed", runtime: "ecs",
			opts:      DeployOptions{EnvFile: ".env", DeployInfra: true},
			l:         fakeOps{secretsUpdated: 2},
			wantOut:   "[ssm-sync]\n" + buildBlock + cdkBlock + "🔁 Secrets changed; rolling the service so tasks pick them up\n🚀 Deploying to ecs...\n[deploy]\n\n✨ Deployment complete!\n",
			wantSteps: []string{"ssm-sync:success", "build:success", "cdk:success", "deploy:success", "wait:skipped"},
		},
		{
			name: "ecs infra cdk no-op", runtime: "ecs",
			opts:      DeployOptions{DeployInfra: true},
			l:         fakeOps{runsImage: false},
			wantOut:   buildBlock + cdkBlock + "🔁 CDK left the service on another image; rolling it to " + testImage + "\n🚀 Deploying to ecs...\n[deploy]\n\n✨ Deployment complete!\n",
			wantSteps: []string{"ssm-sync:skipped", "build:success", "cdk:success", "deploy:success", "wait:skipped"},
		},
		{
			name: "ecs infra check error", runtime: "ecs",
			opts:      DeployOptions{DeployInfra: true},
			l:         fakeOps{runsImageErr: errors.New("throttled")},
			wantOut:   buildBlock + cdkBlock + "⚠️  could not verify the CDK rollout (throttled); rolling the service with citadel\n🚀 Deploying to ecs...\n[deploy]\n\n✨ Deployment complete!\n",
			wantSteps: []string{"ssm-sync:skipped", "build:success", "cdk:success", "deploy:success", "wait:skipped"},
		},
		{
			name: "lambda env-file infra wait", runtime: "lambda",
			opts:      DeployOptions{EnvFile: ".env", DeployInfra: true, Wait: true},
			wantOut:   "[ssm-sync]\n" + buildBlock + cdkBlock + "🚀 Deploying to lambda...\n[deploy]\n[config-sync]\n[wait]\n\n✨ Deployment complete!\n",
			wantSteps: []string{"ssm-sync:success", "build:success", "cdk:success", "deploy:success", "config-sync:success", "wait:success"},
		},
		{
			name: "lambda skip-config", runtime: "lambda",
			opts:      DeployOptions{SkipConfig: true, Wait: true},
			wantOut:   buildBlock + "🚀 Deploying to lambda...\n[deploy]\n⏭️  Skipping function config sync (--skip-config)\n[wait]\n\n✨ Deployment complete!\n",
			wantSteps: []string{"ssm-sync:skipped", "build:success", "cdk:skipped", "deploy:success", "config-sync:skipped", "wait:success"},
		},
		{
			name: "lambda dry-run", runtime: "lambda",
			opts:    DeployOptions{EnvFile: ".env", DryRun: true},
			wantOut: "[ssm-sync]\n" + buildBlock + "[config-sync]\n\n✨ Deployment complete!\n",
		},
		{
			name: "ecs dry-run infra", runtime: "ecs",
			opts:    DeployOptions{DryRun: true, DeployInfra: true},
			wantOut: buildBlock + cdkBlock + "✨ Deployment complete!\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cfgPath := compatConfig(t, c.runtime, "")
			var out bytes.Buffer
			opts := c.opts
			opts.ConfigPath, opts.Environment, opts.Message, opts.Out = cfgPath, "dev", "m", &out
			l := c.l
			if err := deployWith(context.Background(), &opts, l.ops()); err != nil {
				t.Fatal(err)
			}
			if want := header(cfgPath) + c.wantOut; out.String() != want {
				t.Fatalf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", out.String(), want)
			}
			if c.opts.DryRun {
				if _, err := os.Stat(filepath.Join(filepath.Dir(cfgPath), ".citadel")); !os.IsNotExist(err) {
					t.Fatal("dry run created .citadel/")
				}
				return
			}
			if got := stepSummary(onlyRun(t, cfgPath)); !reflect.DeepEqual(got, c.wantSteps) {
				t.Fatalf("steps = %v, want %v", got, c.wantSteps)
			}
			if len(l.recorded) != 1 || l.recorded[0] != nil {
				t.Fatalf("deploy history recorded %v, want one success", l.recorded)
			}
		})
	}
}

func TestDeploy_BuildFailureStopsBeforeCDK(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := compatConfig(t, "ecs", "")
	l := fakeOps{buildErr: errors.New("docker daemon unreachable")}
	opts := DeployOptions{ConfigPath: cfgPath, Environment: "dev", DeployInfra: true, Out: io.Discard}
	err := deployWith(context.Background(), &opts, l.ops())
	if err == nil || err.Error() != "failed to build/push image: docker daemon unreachable" {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(l.calls, []string{"build"}) {
		t.Fatalf("calls = %v", l.calls)
	}
}

func TestDeploy_CustomPipelineWithHealthCheckRollback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := compatConfig(t, "ecs", `pipeline:
  - uses: citadel/build
  - name: migrate
    task: migrate
  - uses: citadel/deploy
  - name: smoke
    http: http://127.0.0.1:1/health
    retries: 1
    request_timeout: 100ms
    rollback_on_failure: true
`)
	l := fakeOps{}
	opts := DeployOptions{ConfigPath: cfgPath, Environment: "dev", Out: io.Discard}
	err := deployWith(context.Background(), &opts, l.ops())
	if err == nil || !strings.Contains(err.Error(), "smoke: health check failed") || !strings.Contains(err.Error(), "rolled back to td:7") {
		t.Fatalf("err = %v", err)
	}
	if want := []string{"build", "task", "record", "update:" + testImage, "rollback"}; !reflect.DeepEqual(l.calls, want) {
		t.Fatalf("calls = %v, want %v", l.calls, want)
	}
	if len(l.recorded) != 1 || l.recorded[0] == nil {
		t.Fatalf("deploy history must record the failure, got %v", l.recorded)
	}
	if r := onlyRun(t, cfgPath); r.Status != "failed" {
		t.Fatalf("run status = %s", r.Status)
	}
}

func TestDeploy_NoImageFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := compatConfig(t, "ecs", `pipeline:
  - uses: citadel/build
    envs: [dev]
  - uses: citadel/deploy
`)
	// Deploy to an env where build is filtered out.
	body, _ := os.ReadFile(cfgPath)
	os.WriteFile(cfgPath, []byte(strings.Replace(string(body), "environments:\n", "environments:\n  prod:\n    account: \"111111111111\"\n", 1)), 0o644)
	l := fakeOps{}
	opts := DeployOptions{ConfigPath: cfgPath, Environment: "prod", Out: io.Discard}
	err := deployWith(context.Background(), &opts, l.ops())
	if err == nil || !strings.Contains(err.Error(), "citadel/build did not run for prod") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range l.calls {
		if strings.HasPrefix(c, "update:") {
			t.Fatalf("deployed without an image: %v", l.calls)
		}
	}
}

func onlyRun(t *testing.T, configPath string) runRecord {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(filepath.Dir(configPath), ".citadel", "runs", "*"))
	if len(dirs) != 1 {
		t.Fatalf("want exactly one run, found %v", dirs)
	}
	return readRun(t, dirs[0])
}
