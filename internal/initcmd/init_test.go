package initcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

type fakePrompter struct {
	answers map[string]string // prompt title → answer; unlisted titles accept the default
	asked   []string          // "title=default" in call order
}

func (f *fakePrompter) answer(title, def string) string {
	f.asked = append(f.asked, title+"="+def)
	if a, ok := f.answers[title]; ok {
		return a
	}
	return def
}

func (f *fakePrompter) Select(title string, options []string, def string) (string, error) {
	return f.answer(title, def), nil
}

func (f *fakePrompter) Input(title, def string, validate func(string) error) (string, error) {
	a := f.answer(title, def)
	if validate != nil {
		if err := validate(a); err != nil {
			return "", fmt.Errorf("fake answer %q for %q rejected: %w", a, title, err)
		}
	}
	return a, nil
}

func (f *fakePrompter) askedTitle(title string) (def string, ok bool) {
	for _, a := range f.asked {
		if t, d, _ := strings.Cut(a, "="); t == title {
			return d, true
		}
	}
	return "", false
}

type fakeDetector struct {
	region  string
	account string
	err     error
}

func (f fakeDetector) Region() string { return f.region }
func (f fakeDetector) Account(ctx context.Context, region string) (string, error) {
	return f.account, f.err
}

var okDetector = fakeDetector{region: "eu-west-1", account: "111111111111"}

// newOpts returns --yes options targeting <tmp>/<dirName>/citadel.yml.
func newOpts(t *testing.T, dirName string) (Options, *bytes.Buffer) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), dirName)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	return Options{
		ConfigPath: filepath.Join(dir, "citadel.yml"),
		Values:     DefaultValues(),
		Provided:   map[string]bool{},
		Yes:        true,
		Version:    "vTEST",
		Out:        &out,
		Now:        func() time.Time { return time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC) },
	}, &out
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func projectMeta(t *testing.T, configPath string) *project.Meta {
	t.Helper()
	d, err := project.Existing(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	m, err := d.Project()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRuntimeFromFlags(t *testing.T) {
	cases := []struct {
		ecs, lambda bool
		want        config.Runtime
		wantErr     bool
	}{
		{false, false, "", false},
		{true, false, config.RuntimeECS, false},
		{false, true, config.RuntimeLambda, false},
		{true, true, "", true},
	}
	for _, c := range cases {
		got, err := RuntimeFromFlags(c.ecs, c.lambda)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("RuntimeFromFlags(%v,%v) = %q, %v", c.ecs, c.lambda, got, err)
		}
	}
}

func TestRun_YesWritesConfigAndProjectDir(t *testing.T) {
	opts, out := newOpts(t, "My API")
	opts.Values.Secrets = []string{"DATABASE_URL"}
	p := &fakePrompter{}

	if err := Run(context.Background(), opts, p, okDetector); err != nil {
		t.Fatal(err)
	}
	if len(p.asked) != 0 {
		t.Fatalf("--yes prompted: %v", p.asked)
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "my-api" || cfg.Region != "eu-west-1" || cfg.ResolvedRuntime() != config.RuntimeECS {
		t.Fatalf("config = %s/%s/%s", cfg.Name, cfg.Region, cfg.ResolvedRuntime())
	}
	if cfg.Environments["prod"].Account != "111111111111" {
		t.Fatalf("prod account = %q", cfg.Environments["prod"].Account)
	}
	m := projectMeta(t, opts.ConfigPath)
	if m.Name != "my-api" || m.Runtime != "ecs" || m.CreatedWith != "citadel vTEST" {
		t.Fatalf("project.yml = %+v", m)
	}
	if !strings.Contains(out.String(), `citadel deploy --env dev -m "first deploy"`) {
		t.Fatalf("missing next step in output:\n%s", out.String())
	}
}

func TestRun_YesRegionFallsBackToUSEast1(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Values.Secrets = []string{"DATABASE_URL"}
	if err := Run(context.Background(), opts, &fakePrompter{}, fakeDetector{account: "111111111111"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(opts.ConfigPath)
	if cfg.Region != FallbackRegion {
		t.Fatalf("region = %q", cfg.Region)
	}
}

func TestRun_YesWithoutDetectableAccountFailsAndWritesNothing(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Values.Secrets = []string{"DATABASE_URL"}
	err := Run(context.Background(), opts, &fakePrompter{}, fakeDetector{err: errors.New("no credentials")})
	if err == nil || !strings.Contains(err.Error(), "--account") {
		t.Fatalf("error = %v, want a hint naming --account", err)
	}
	if names := dirEntries(t, filepath.Dir(opts.ConfigPath)); len(names) != 0 {
		t.Fatalf("files written on failure: %v", names)
	}
}

func TestRun_YesECSWithoutSecretsFailsAndWritesNothing(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	err := Run(context.Background(), opts, &fakePrompter{}, okDetector)
	if err == nil || !strings.Contains(err.Error(), "--secrets") {
		t.Fatalf("error = %v, want a hint naming --secrets", err)
	}
	if names := dirEntries(t, filepath.Dir(opts.ConfigPath)); len(names) != 0 {
		t.Fatalf("files written on failure: %v", names)
	}
}

func TestRun_ExplicitInvalidNameRejected(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Values.Secrets = []string{"DATABASE_URL"}
	opts.Values.Name = "My App"
	opts.Provided[FlagName] = true
	err := Run(context.Background(), opts, &fakePrompter{}, okDetector)
	if err == nil || !strings.Contains(err.Error(), "my-api") {
		t.Fatalf("error = %v", err)
	}
}

func TestRun_ECSFlagWithLambdaRejected(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Values.Runtime = config.RuntimeLambda
	opts.Provided[FlagPort] = true
	err := Run(context.Background(), opts, &fakePrompter{}, okDetector)
	if err == nil || !strings.Contains(err.Error(), "--port is only valid with --ecs") {
		t.Fatalf("error = %v", err)
	}
}

const smaugYML = `name: smaug
region: us-east-1
runtime: lambda
environments:
  dev:
    account: "454066810976"
`

func TestRun_ExistingConfigIsAdoptedNotTouched(t *testing.T) {
	opts, out := newOpts(t, "smaug")
	if err := os.WriteFile(opts.ConfigPath, []byte(smaugYML), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.Values.Runtime = config.RuntimeECS
	opts.Provided[FlagECS] = true

	if err := Run(context.Background(), opts, &fakePrompter{}, okDetector); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(opts.ConfigPath)
	if string(got) != smaugYML {
		t.Fatal("existing citadel.yml was modified")
	}
	m := projectMeta(t, opts.ConfigPath)
	if m.Name != "smaug" || m.Runtime != "lambda" {
		t.Fatalf("project.yml = %+v (must come from the existing config)", m)
	}
	if !strings.Contains(out.String(), "--force") {
		t.Fatalf("expected a note that flags were ignored without --force:\n%s", out.String())
	}
}

func TestRun_AlreadyInitialisedIsANoOp(t *testing.T) {
	opts, _ := newOpts(t, "smaug")
	os.WriteFile(opts.ConfigPath, []byte(smaugYML), 0o644)
	if err := Run(context.Background(), opts, &fakePrompter{}, okDetector); err != nil {
		t.Fatal(err)
	}
	id := projectMeta(t, opts.ConfigPath).ID

	var out bytes.Buffer
	opts.Out = &out
	if err := Run(context.Background(), opts, &fakePrompter{}, okDetector); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(out.String(), "already initialised") {
		t.Fatalf("output = %q", out.String())
	}
	if projectMeta(t, opts.ConfigPath).ID != id {
		t.Fatal("project id changed")
	}
}

func TestRun_BrokenExistingConfigPointsAtForce(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	os.WriteFile(opts.ConfigPath, []byte("name: [\n"), 0o644)
	err := Run(context.Background(), opts, &fakePrompter{}, okDetector)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v, want a hint naming --force", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(opts.ConfigPath), project.DirName)); !os.IsNotExist(err) {
		t.Fatal(".citadel/ created for a broken config")
	}
}

func TestRun_ForceOverwritesConfigKeepsProjectID(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Values.Secrets = []string{"DATABASE_URL"}
	if err := Run(context.Background(), opts, &fakePrompter{}, okDetector); err != nil {
		t.Fatal(err)
	}
	id := projectMeta(t, opts.ConfigPath).ID

	opts.Force = true
	opts.Values.Port = 9090
	opts.Provided[FlagPort] = true
	if err := Run(context.Background(), opts, &fakePrompter{}, okDetector); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(opts.ConfigPath)
	if cfg.Container.Port != 9090 {
		t.Fatalf("port = %d, want 9090 after --force", cfg.Container.Port)
	}
	if projectMeta(t, opts.ConfigPath).ID != id {
		t.Fatal("--force replaced project.yml")
	}
}

func TestRun_CitadelDirIsARegularFileFailsAndWritesNothing(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Values.Secrets = []string{"DATABASE_URL"}
	configDir := filepath.Dir(opts.ConfigPath)
	if err := os.WriteFile(filepath.Join(configDir, project.DirName), []byte("oops"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Run(context.Background(), opts, &fakePrompter{}, okDetector)
	if err == nil {
		t.Fatal("expected an error when .citadel exists as a regular file")
	}
	if _, statErr := os.Stat(opts.ConfigPath); !os.IsNotExist(statErr) {
		t.Fatal("citadel.yml was written even though .citadel/ could not be created")
	}
}

func TestRun_DryRunWritesNothing(t *testing.T) {
	opts, out := newOpts(t, "svc")
	opts.Values.Secrets = []string{"DATABASE_URL"}
	opts.DryRun = true
	if err := Run(context.Background(), opts, &fakePrompter{}, okDetector); err != nil {
		t.Fatal(err)
	}
	if names := dirEntries(t, filepath.Dir(opts.ConfigPath)); len(names) != 0 {
		t.Fatalf("dry run wrote: %v", names)
	}
	s := out.String()
	if !strings.Contains(s, "name: svc") || !strings.Contains(s, project.DirName) {
		t.Fatalf("dry-run output missing config or .citadel plan:\n%s", s)
	}
}

func TestRun_InteractiveUsesDetectedDefaults(t *testing.T) {
	opts, _ := newOpts(t, "Legolas")
	opts.Yes = false
	p := &fakePrompter{answers: map[string]string{titleRuntime: "lambda"}}

	if err := Run(context.Background(), opts, p, okDetector); err != nil {
		t.Fatal(err)
	}
	for title, wantDef := range map[string]string{
		titleName:    "legolas",
		titleRegion:  "eu-west-1",
		titleAccount: "111111111111",
		titleEnvs:    "dev,prod",
	} {
		def, ok := p.askedTitle(title)
		if !ok || def != wantDef {
			t.Errorf("prompt %q default = %q (asked=%v), want %q", title, def, ok, wantDef)
		}
	}
	if _, ok := p.askedTitle(titlePort); ok {
		t.Error("asked an ECS-only question for a lambda project")
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil || cfg.ResolvedRuntime() != config.RuntimeLambda {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
}

func TestRun_InteractiveAccountPromptWhenDetectionFails(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Yes = false
	opts.Values.Runtime = config.RuntimeLambda
	p := &fakePrompter{answers: map[string]string{titleAccount: "222222222222"}}

	if err := Run(context.Background(), opts, p, fakeDetector{region: "us-east-1", err: errors.New("no creds")}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(opts.ConfigPath)
	if cfg.Environments["dev"].Account != "222222222222" {
		t.Fatalf("account = %q", cfg.Environments["dev"].Account)
	}
}

func TestRun_ProvidedFlagsAreNotPrompted(t *testing.T) {
	opts, _ := newOpts(t, "svc")
	opts.Yes = false
	opts.Values.Runtime = config.RuntimeLambda
	opts.Values.Name = "custom"
	opts.Provided[FlagName] = true
	opts.Provided[FlagLambda] = true
	p := &fakePrompter{}

	if err := Run(context.Background(), opts, p, okDetector); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.askedTitle(titleName); ok {
		t.Fatal("prompted for a name given by flag")
	}
	if _, ok := p.askedTitle(titleRuntime); ok {
		t.Fatal("prompted for a runtime given by flag")
	}
}
