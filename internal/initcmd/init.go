package initcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ClusterBox/citadel/internal/project"
	"github.com/ClusterBox/citadel/pkg/config"
)

// Flag names shared by the cobra command and Options.Provided.
const (
	FlagECS             = "ecs"
	FlagLambda          = "lambda"
	FlagName            = "name"
	FlagRegion          = "region"
	FlagAccount         = "account"
	FlagEnvs            = "envs"
	FlagPort            = "port"
	FlagCPU             = "cpu"
	FlagMemory          = "memory"
	FlagHealthCheckPath = "health-check-path"
	FlagFunctionName    = "function-name"
	FlagSecrets         = "secrets"
)

// ValueFlags lists every flag that sets a value (checked with Changed()).
var ValueFlags = []string{
	FlagECS, FlagLambda, FlagName, FlagRegion, FlagAccount, FlagEnvs,
	FlagPort, FlagCPU, FlagMemory, FlagHealthCheckPath, FlagFunctionName, FlagSecrets,
}

var (
	ecsOnlyFlags    = []string{FlagPort, FlagCPU, FlagMemory, FlagHealthCheckPath}
	lambdaOnlyFlags = []string{FlagFunctionName}
)

// Prompt titles (also used by tests to script answers).
const (
	titleRuntime      = "Runtime"
	titleName         = "Project name"
	titleRegion       = "AWS region"
	titleAccount      = "AWS account ID"
	titleEnvs         = "Environments (comma-separated)"
	titlePort         = "Container port"
	titleCPU          = "Task CPU units (256 = 0.25 vCPU)"
	titleMemory       = "Task memory (MiB)"
	titleHealth       = "Health check path"
	titleFunctionName = "Lambda function name (blank = <name>-<env>)"
	titleSecrets      = "Secret env var names (comma-separated)"
)

// Prompter asks the user for values. The real one uses huh; tests use fakes.
type Prompter interface {
	Select(title string, options []string, def string) (string, error)
	Input(title, def string, validate func(string) error) (string, error)
}

// Detector finds defaults from the environment.
type Detector interface {
	// Region returns the region from the environment, or "" when unset.
	Region() string
	// Account returns the AWS account of the current credentials.
	Account(ctx context.Context, region string) (string, error)
}

// Options configures one `citadel init`.
type Options struct {
	ConfigPath string          // citadel.yml to write; .citadel/ goes next to it
	Values     Values          // flag values, with DefaultValues for unset flags
	Provided   map[string]bool // flags the user passed explicitly
	Yes        bool            // never prompt (also set for non-TTY stdin)
	Force      bool            // overwrite an existing citadel.yml
	DryRun     bool            // print what would be written; write nothing
	Version    string          // citadel version, recorded in generated files
	Out        io.Writer
	Now        func() time.Time // nil means time.Now
}

// RuntimeFromFlags maps --ecs/--lambda to a runtime ("" when neither is set).
func RuntimeFromFlags(ecs, lambda bool) (config.Runtime, error) {
	switch {
	case ecs && lambda:
		return "", fmt.Errorf("--ecs and --lambda are mutually exclusive")
	case ecs:
		return config.RuntimeECS, nil
	case lambda:
		return config.RuntimeLambda, nil
	}
	return "", nil
}

// Run executes `citadel init`. Every check runs before anything is written, so
// a failure leaves the filesystem untouched.
func Run(ctx context.Context, opts Options, p Prompter, d Detector) error {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Provided == nil {
		opts.Provided = map[string]bool{}
	}

	_, statErr := os.Stat(opts.ConfigPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("check %s: %w", opts.ConfigPath, statErr)
	}
	if statErr == nil && !opts.Force {
		return adopt(opts)
	}

	v, err := collect(ctx, opts, p, d)
	if err != nil {
		return err
	}
	content, err := Render(v, opts.Version)
	if err != nil {
		return err
	}

	configDir := filepath.Dir(opts.ConfigPath)
	if opts.DryRun {
		fmt.Fprintf(opts.Out, "# %s (dry run: nothing written)\n%s\n", opts.ConfigPath, content)
		describeProjectDir(opts.Out, configDir)
		return nil
	}

	if err := project.WriteFileAtomic(opts.ConfigPath, content); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "✅ Wrote %s (%s runtime)\n", opts.ConfigPath, v.Runtime)
	if err := ensureProjectDir(opts, configDir, v.Name, string(v.Runtime)); err != nil {
		return err
	}
	printNext(opts, v.Envs[0])
	return nil
}

// adopt handles an existing citadel.yml without --force: the config is left
// alone and only .citadel/ is created (or reported as already there).
func adopt(opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("%s already exists but is invalid (%v); fix it, or re-run with --force to overwrite it", opts.ConfigPath, err)
	}
	for _, f := range ValueFlags {
		if opts.Provided[f] {
			fmt.Fprintf(opts.Out, "ℹ️  %s already exists; init flags are ignored (use --force to regenerate it)\n", opts.ConfigPath)
			break
		}
	}

	configDir := filepath.Dir(opts.ConfigPath)
	if d, err := project.Existing(configDir); err == nil {
		if m, err := d.Project(); err == nil {
			fmt.Fprintf(opts.Out, "✅ %s is already initialised (project %s, id %s)\n", configDir, m.Name, m.ID)
			return nil
		}
	}
	if opts.DryRun {
		describeProjectDir(opts.Out, configDir)
		return nil
	}
	if err := ensureProjectDir(opts, configDir, cfg.Name, string(cfg.ResolvedRuntime())); err != nil {
		return err
	}
	printNext(opts, firstEnv(cfg))
	return nil
}

// collect resolves every value: explicit flags win, then detection, then
// prompts (unless opts.Yes) seeded with those defaults.
func collect(ctx context.Context, opts Options, p Prompter, d Detector) (Values, error) {
	v := opts.Values
	interactive := !opts.Yes
	ask := func(flag string) bool { return interactive && !opts.Provided[flag] }

	if v.Runtime == "" {
		v.Runtime = config.RuntimeECS
		if interactive {
			s, err := p.Select(titleRuntime, []string{string(config.RuntimeECS), string(config.RuntimeLambda)}, string(config.RuntimeECS))
			if err != nil {
				return v, err
			}
			v.Runtime = config.Runtime(s)
		}
	}
	if err := checkRuntimeFlags(v.Runtime, opts.Provided); err != nil {
		return v, err
	}

	input := func(flag, title string, cur *string, validate func(string) error) error {
		if !ask(flag) {
			return nil
		}
		s, err := p.Input(title, *cur, validate)
		if err != nil {
			return err
		}
		*cur = strings.TrimSpace(s)
		return nil
	}
	inputInt := func(flag, title string, cur *int) error {
		s := strconv.Itoa(*cur)
		if err := input(flag, title, &s, validatePositiveInt); err != nil {
			return err
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("%s: %q is not a number", title, s)
		}
		*cur = n
		return nil
	}
	inputList := func(flag, title string, cur *[]string, check func(string) error, required bool) error {
		s := strings.Join(*cur, ",")
		if err := input(flag, title, &s, listValidator(check, required)); err != nil {
			return err
		}
		*cur = SplitList(s)
		return nil
	}

	if !opts.Provided[FlagName] {
		abs, err := filepath.Abs(filepath.Dir(opts.ConfigPath))
		if err != nil {
			return v, err
		}
		v.Name = SanitizeName(filepath.Base(abs))
	}
	if err := input(FlagName, titleName, &v.Name, ValidateName); err != nil {
		return v, err
	}

	if !opts.Provided[FlagRegion] {
		v.Region = d.Region()
		if v.Region == "" {
			v.Region = FallbackRegion
		}
	}
	if err := input(FlagRegion, titleRegion, &v.Region, ValidateRegion); err != nil {
		return v, err
	}

	if !opts.Provided[FlagAccount] {
		acct, err := d.Account(ctx, v.Region)
		switch {
		case err == nil:
			v.Account = acct
		case !interactive:
			return v, fmt.Errorf("could not detect the AWS account (%v): pass --account or configure AWS credentials", err)
		}
	}
	if err := input(FlagAccount, titleAccount, &v.Account, ValidateAccount); err != nil {
		return v, err
	}

	if err := inputList(FlagEnvs, titleEnvs, &v.Envs, ValidateEnvName, true); err != nil {
		return v, err
	}

	switch v.Runtime {
	case config.RuntimeECS:
		if err := inputInt(FlagPort, titlePort, &v.Port); err != nil {
			return v, err
		}
		if err := inputInt(FlagCPU, titleCPU, &v.CPU); err != nil {
			return v, err
		}
		if err := inputInt(FlagMemory, titleMemory, &v.Memory); err != nil {
			return v, err
		}
		if err := input(FlagHealthCheckPath, titleHealth, &v.HealthCheckPath, ValidateHealthCheckPath); err != nil {
			return v, err
		}
		if err := inputList(FlagSecrets, titleSecrets, &v.Secrets, ValidateSecretName, true); err != nil {
			return v, err
		}
	case config.RuntimeLambda:
		if err := input(FlagFunctionName, titleFunctionName, &v.FunctionName, optional(ValidateFunctionName)); err != nil {
			return v, err
		}
		if err := inputList(FlagSecrets, titleSecrets, &v.Secrets, ValidateSecretName, false); err != nil {
			return v, err
		}
	}
	return v, nil
}

func checkRuntimeFlags(rt config.Runtime, provided map[string]bool) error {
	switch rt {
	case config.RuntimeLambda:
		for _, f := range ecsOnlyFlags {
			if provided[f] {
				return fmt.Errorf("--%s is only valid with --ecs", f)
			}
		}
	case config.RuntimeECS:
		for _, f := range lambdaOnlyFlags {
			if provided[f] {
				return fmt.Errorf("--%s is only valid with --lambda", f)
			}
		}
	}
	return nil
}

func validatePositiveInt(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return fmt.Errorf("%q: enter a positive whole number", s)
	}
	return nil
}

func listValidator(check func(string) error, required bool) func(string) error {
	return func(s string) error {
		items := SplitList(s)
		if required && len(items) == 0 {
			return fmt.Errorf("enter at least one value")
		}
		for _, it := range items {
			if err := check(it); err != nil {
				return err
			}
		}
		return nil
	}
}

func optional(check func(string) error) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return check(strings.TrimSpace(s))
	}
}

// ensureProjectDir creates .citadel/ and project.yml, keeping an existing
// project.yml (and its id) untouched.
func ensureProjectDir(opts Options, configDir, name, runtime string) error {
	d, err := project.Open(configDir)
	if err != nil {
		return err
	}
	m, created, err := d.EnsureProject(project.NewMeta(name, runtime, opts.Version, opts.Now()))
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(opts.Out, "✅ Created %s/ (project.yml, .gitignore)\n", d.Path())
	} else {
		fmt.Fprintf(opts.Out, "   Kept existing %s (id %s)\n", filepath.Join(d.Path(), "project.yml"), m.ID)
	}
	return nil
}

// describeProjectDir prints what a dry run would do with .citadel/.
func describeProjectDir(out io.Writer, configDir string) {
	if d, err := project.Existing(configDir); err == nil {
		if _, err := d.Project(); err == nil {
			fmt.Fprintf(out, "Would keep existing %s\n", filepath.Join(d.Path(), "project.yml"))
			return
		}
	}
	fmt.Fprintf(out, "Would create %s/ with .gitignore and project.yml\n", filepath.Join(configDir, project.DirName))
}

func printNext(opts Options, env string) {
	cmd := fmt.Sprintf("citadel deploy --env %s -m \"first deploy\"", env)
	if opts.ConfigPath != config.DefaultFileName {
		cmd += " --config " + opts.ConfigPath
	}
	fmt.Fprintf(opts.Out, "\nNext: commit %s and %s, then run:\n   %s\n",
		opts.ConfigPath, filepath.Join(filepath.Dir(opts.ConfigPath), project.DirName, "project.yml"), cmd)
}

// firstEnv returns the alphabetically first environment for the hint.
func firstEnv(cfg *config.DeployConfig) string {
	envs := make([]string, 0, len(cfg.Environments))
	for e := range cfg.Environments {
		envs = append(envs, e)
	}
	sort.Strings(envs)
	if len(envs) == 0 {
		return "dev"
	}
	return envs[0]
}
