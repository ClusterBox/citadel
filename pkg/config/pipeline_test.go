package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const ecsBase = `name: demo
region: us-east-1
container: {port: 3000, cpu: 256, memory: 512}
secrets: [DATABASE_URL]
environments:
  dev: {account: "111111111111"}
  prod: {account: "111111111111"}
`

const lambdaBase = `name: demo
region: us-east-1
runtime: lambda
environments:
  dev: {account: "111111111111"}
`

func parseWith(t *testing.T, base, pipeline string) (*DeployConfig, error) {
	t.Helper()
	return Parse([]byte(base + pipeline))
}

func TestPipeline_ValidFullExample(t *testing.T) {
	cfg, err := parseWith(t, ecsBase, `pipeline:
  - uses: citadel/ssm-sync
  - name: test
    run: npm test
    env: {NODE_ENV: test, WHERE: "${env}"}
  - uses: citadel/build
  - name: migrate
    task: npx prisma migrate deploy
    timeout: 15m
  - name: migrate-exec
    task: [node, dist/migrate.js, "${env}"]
    container: app
  - uses: citadel/cdk
  - uses: citadel/deploy
  - uses: citadel/wait
  - name: smoke
    http: https://api-${env}.example.com/health
    rollback_on_failure: true
    envs: [dev, prod]
`)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.ResolvedPipeline()
	if len(p) != 9 {
		t.Fatalf("len = %d", len(p))
	}
	if p[3].Kind() != KindTask || !reflect.DeepEqual(p[3].Task.Command(), []string{"sh", "-c", "npx prisma migrate deploy"}) {
		t.Fatalf("string task = %+v", p[3].Task)
	}
	if !reflect.DeepEqual(p[4].Task.Command(), []string{"node", "dist/migrate.js", "${env}"}) {
		t.Fatalf("list task = %+v", p[4].Task)
	}
	if p[3].TimeoutOrDefault() != 15*time.Minute || p[1].TimeoutOrDefault() != 30*time.Minute {
		t.Fatal("timeouts")
	}
	if p[0].StepName() != "ssm-sync" || p[0].Builtin() != "ssm-sync" || p[8].StepName() != "smoke" {
		t.Fatal("names")
	}
	if p[8].RetriesOrDefault() != 10 || p[8].ExpectStatusOrDefault() != 200 ||
		p[8].IntervalOrDefault() != 6*time.Second || p[8].RequestTimeoutOrDefault() != 5*time.Second {
		t.Fatal("http defaults")
	}
	if !reflect.DeepEqual(p[1].ShellOrDefault(), []string{"bash", "-euo", "pipefail", "-c"}) {
		t.Fatal("shell default")
	}
}

func TestPipeline_DefaultWhenAbsent(t *testing.T) {
	ecs, err := Parse([]byte(ecsBase))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range ecs.ResolvedPipeline() {
		names = append(names, s.StepName())
	}
	if want := []string{"ssm-sync", "build", "cdk", "deploy", "wait"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("ecs default = %v", names)
	}
	lam, err := Parse([]byte(lambdaBase))
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, s := range lam.ResolvedPipeline() {
		names = append(names, s.StepName())
	}
	if want := []string{"ssm-sync", "build", "cdk", "deploy", "config-sync", "wait"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("lambda default = %v", names)
	}
}

func TestPipeline_ValidationErrors(t *testing.T) {
	cases := []struct {
		name, base, pipeline, want string
	}{
		{"two kinds", ecsBase, "pipeline:\n  - name: x\n    run: a\n    http: https://x\n", `pipeline[0] "x": set exactly one of uses, run, task, http`},
		{"no kind", ecsBase, "pipeline:\n  - name: x\n", `pipeline[0] "x": set exactly one of uses, run, task, http`},
		{"unknown uses", ecsBase, "pipeline:\n  - uses: citadel/deploy-all\n", `unknown step "citadel/deploy-all"`},
		{"custom without name", ecsBase, "pipeline:\n  - run: npm test\n", `name is required for run steps`},
		{"bad name", ecsBase, "pipeline:\n  - name: Run Tests\n    run: x\n", `name must match`},
		{"reserved name", ecsBase, "pipeline:\n  - name: build\n    run: x\n", `"build" is reserved`},
		{"rollback reserved", ecsBase, "pipeline:\n  - name: rollback\n    run: x\n", `"rollback" is reserved`},
		{"duplicate", ecsBase, "pipeline:\n  - name: t\n    run: a\n  - name: t\n    run: b\n", `pipeline[1] "t": duplicate step name`},
		{"builtin twice", ecsBase, "pipeline:\n  - uses: citadel/build\n  - uses: citadel/build\n", `citadel/build may appear only once`},
		{"unknown env", ecsBase, "pipeline:\n  - name: t\n    run: a\n    envs: [staging]\n", `envs: "staging" is not in environments`},
		{"misplaced field", ecsBase, "pipeline:\n  - name: t\n    run: a\n    retries: 3\n", `retries only applies to http steps`},
		{"bad duration", ecsBase, "pipeline:\n  - name: t\n    run: a\n    timeout: soon\n", `timeout: invalid duration`},
		{"bad status", ecsBase, "pipeline:\n  - name: h\n    http: https://x\n    expect_status: 42\n", `expect_status must be between 100 and 599`},
		{"negative retries", ecsBase, "pipeline:\n  - name: h\n    http: https://x\n    retries: -1\n", `retries must be at least 1`},
		{"unknown var", ecsBase, "pipeline:\n  - name: t\n    run: echo ${stage}\n", `unknown variable ${stage}`},
		{"cdk before build", ecsBase, "pipeline:\n  - uses: citadel/cdk\n  - uses: citadel/build\n", `pipeline[0] "cdk": must come after citadel/build`},
		{"deploy without build", ecsBase, "pipeline:\n  - uses: citadel/deploy\n", `pipeline[0] "deploy": requires citadel/build earlier in the pipeline`},
		{"deploy before cdk", ecsBase, "pipeline:\n  - uses: citadel/build\n  - uses: citadel/deploy\n  - uses: citadel/cdk\n", `pipeline[1] "deploy": must come after citadel/cdk`},
		{"wait without deploy", ecsBase, "pipeline:\n  - uses: citadel/build\n  - uses: citadel/wait\n", `pipeline[1] "wait": requires citadel/deploy earlier in the pipeline`},
		{"task before build", ecsBase, "pipeline:\n  - name: m\n    task: migrate\n  - uses: citadel/build\n", `pipeline[0] "m": requires citadel/build earlier in the pipeline`},
		{"task on lambda", lambdaBase, "pipeline:\n  - uses: citadel/build\n  - name: m\n    task: migrate\n", `task: is only supported for the ecs runtime`},
		{"config-sync on ecs", ecsBase, "pipeline:\n  - uses: citadel/build\n  - uses: citadel/deploy\n  - uses: citadel/config-sync\n", `citadel/config-sync is only for the lambda runtime`},
		{"rollback before deploy", ecsBase, "pipeline:\n  - uses: citadel/build\n  - name: h\n    http: https://x\n    rollback_on_failure: true\n  - uses: citadel/deploy\n", `rollback_on_failure needs citadel/deploy earlier in the pipeline`},
		{"empty task list", ecsBase, "pipeline:\n  - uses: citadel/build\n  - name: m\n    task: []\n", `set exactly one of uses, run, task, http`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseWith(t, c.base, c.pipeline)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestPipeline_MigrationsOnlyIsValid(t *testing.T) {
	if _, err := parseWith(t, ecsBase, "pipeline:\n  - name: schema\n    run: npx ts-node scripts/sync-schema.ts\n"); err != nil {
		t.Fatal(err)
	}
}

func TestExpandVars(t *testing.T) {
	vals := map[string]string{"env": "dev", "name": "demo", "image": "r:abc", "sha": "abc", "region": "us-east-1", "account": "1"}
	got, err := ExpandVars("deploy ${name}-${env} ${image} $HOME ${HOME} ${PATH}x", vals)
	if err != nil {
		t.Fatal(err)
	}
	if got != "deploy demo-dev r:abc $HOME ${HOME} ${PATH}x" {
		t.Fatalf("got %q", got)
	}
	if _, err := ExpandVars("${stage}", vals); err == nil || !strings.Contains(err.Error(), "unknown variable ${stage}") {
		t.Fatalf("err = %v", err)
	}
}
