package config

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Built-in step names (used as `uses: citadel/<name>`) and the reserved name
// the executor uses for an automatic rollback step.
const (
	StepSSMSync    = "ssm-sync"
	StepBuild      = "build"
	StepCDK        = "cdk"
	StepDeploy     = "deploy"
	StepConfigSync = "config-sync"
	StepWait       = "wait"
	StepRollback   = "rollback"

	usesPrefix = "citadel/"
)

var builtinSteps = map[string]bool{
	StepSSMSync: true, StepBuild: true, StepCDK: true,
	StepDeploy: true, StepConfigSync: true, StepWait: true,
}

// StepKind is the kind of a pipeline step.
type StepKind string

const (
	KindBuiltin StepKind = "builtin"
	KindRun     StepKind = "run"
	KindTask    StepKind = "task"
	KindHTTP    StepKind = "http"
)

// PipelineVars are the ${…} variables pipeline steps may use.
var PipelineVars = []string{"env", "name", "image", "sha", "region", "account"}

var (
	stepNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// Only lowercase ${word} is a citadel variable; ${HOME}, $PATH etc. are
	// left for the shell.
	varRef = regexp.MustCompile(`\$\{([a-z][a-z0-9_]*)\}`)
)

// PipelineStep is one entry of citadel.yml's pipeline: list. Exactly one of
// Uses, Run, Task, HTTP is set (enforced by Validate).
type PipelineStep struct {
	Name string      `yaml:"name,omitempty"`
	Uses string      `yaml:"uses,omitempty"`
	Run  string      `yaml:"run,omitempty"`
	Task TaskCommand `yaml:"task,omitempty"`
	HTTP string      `yaml:"http,omitempty"`

	Shell            []string          `yaml:"shell,omitempty"`
	WorkingDirectory string            `yaml:"working_directory,omitempty"`
	Env              map[string]string `yaml:"env,omitempty"`

	Container string `yaml:"container,omitempty"`

	ExpectStatus      int    `yaml:"expect_status,omitempty"`
	Retries           int    `yaml:"retries,omitempty"`
	Interval          string `yaml:"interval,omitempty"`
	RequestTimeout    string `yaml:"request_timeout,omitempty"`
	RollbackOnFailure bool   `yaml:"rollback_on_failure,omitempty"`

	Envs            []string `yaml:"envs,omitempty"`
	ContinueOnError bool     `yaml:"continue_on_error,omitempty"`
	Timeout         string   `yaml:"timeout,omitempty"`
}

// PipelineSteps is citadel.yml's pipeline: list. Decoding errors name the
// step as pipeline[<i>] "<name>", like validation errors.
type PipelineSteps []PipelineStep

// UnmarshalYAML decodes each step, labelling its errors with its position.
func (p *PipelineSteps) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: pipeline: must be a list of steps", n.Line)
	}
	steps := make(PipelineSteps, len(n.Content))
	for i, item := range n.Content {
		if err := item.Decode(&steps[i]); err != nil {
			return fmt.Errorf("pipeline[%d] %q: %w", i, rawStepName(item), err)
		}
	}
	*p = steps
	return nil
}

// rawStepName is the step's name (or built-in name) read straight from its
// YAML mapping, for errors raised before the step is decoded.
func rawStepName(n *yaml.Node) string {
	if n.Kind != yaml.MappingNode {
		return ""
	}
	var uses string
	for i := 0; i+1 < len(n.Content); i += 2 {
		switch n.Content[i].Value {
		case "name":
			return n.Content[i+1].Value
		case "uses":
			uses = strings.TrimPrefix(n.Content[i+1].Value, usesPrefix)
		}
	}
	return uses
}

// pipelineStepKeys are the keys a pipeline step may use, in declaration
// order (read from PipelineStep's yaml tags so they cannot drift).
var pipelineStepKeys = func() []string {
	t := reflect.TypeOf(PipelineStep{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if k, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); k != "" && k != "-" {
			keys = append(keys, k)
		}
	}
	return keys
}()

// UnmarshalYAML rejects unknown keys (a misspelled `environment:` or
// `rollback-on-failure:` would otherwise be silently ignored).
func (s *PipelineStep) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if !slices.Contains(pipelineStepKeys, k.Value) {
				return fmt.Errorf("unknown key %q (line %d; valid keys: %s)", k.Value, k.Line, strings.Join(pipelineStepKeys, ", "))
			}
		}
	}
	type plain PipelineStep // no UnmarshalYAML: avoids recursion
	return n.Decode((*plain)(s))
}

// TaskCommand is a task: value — a string (run as `sh -c`) or a list (the
// container command as-is, for images without a shell).
type TaskCommand struct {
	Script string
	Args   []string
}

// UnmarshalYAML accepts a scalar or a sequence of strings.
func (t *TaskCommand) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		t.Script = n.Value
		return nil
	case yaml.SequenceNode:
		return n.Decode(&t.Args)
	default:
		return fmt.Errorf("task: must be a string or a list of strings")
	}
}

// IsZero reports whether no task command is set (used by yaml omitempty).
func (t TaskCommand) IsZero() bool { return t.Script == "" && len(t.Args) == 0 }

// Command returns the container command for the task.
func (t TaskCommand) Command() []string {
	if t.Script != "" {
		return []string{"sh", "-c", t.Script}
	}
	return t.Args
}

// String returns the command as written, for logs and dry runs.
func (t TaskCommand) String() string {
	if t.Script != "" {
		return t.Script
	}
	return strings.Join(t.Args, " ")
}

func (s PipelineStep) kinds() []StepKind {
	var k []StepKind
	if s.Uses != "" {
		k = append(k, KindBuiltin)
	}
	if s.Run != "" {
		k = append(k, KindRun)
	}
	if !s.Task.IsZero() {
		k = append(k, KindTask)
	}
	if s.HTTP != "" {
		k = append(k, KindHTTP)
	}
	return k
}

// Kind returns the step's kind. Valid only for a validated step.
func (s PipelineStep) Kind() StepKind {
	if k := s.kinds(); len(k) == 1 {
		return k[0]
	}
	return ""
}

// Builtin returns "build" for `uses: citadel/build` ("" for custom steps).
func (s PipelineStep) Builtin() string {
	if s.Uses == "" {
		return ""
	}
	return strings.TrimPrefix(s.Uses, usesPrefix)
}

// StepName is the name used for logs and run.json: Name, or the built-in name.
func (s PipelineStep) StepName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Builtin()
}

func durationOr(v string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(v); err == nil && v != "" {
		return d
	}
	return def
}

// TimeoutOrDefault is the run/task timeout (default 30m).
func (s PipelineStep) TimeoutOrDefault() time.Duration { return durationOr(s.Timeout, 30*time.Minute) }

// IntervalOrDefault is the http retry interval (default 6s).
func (s PipelineStep) IntervalOrDefault() time.Duration { return durationOr(s.Interval, 6*time.Second) }

// RequestTimeoutOrDefault is the per-attempt http timeout (default 5s).
func (s PipelineStep) RequestTimeoutOrDefault() time.Duration {
	return durationOr(s.RequestTimeout, 5*time.Second)
}

// RetriesOrDefault is the number of http attempts (default 10).
func (s PipelineStep) RetriesOrDefault() int {
	if s.Retries > 0 {
		return s.Retries
	}
	return 10
}

// ExpectStatusOrDefault is the http status that means healthy (default 200).
func (s PipelineStep) ExpectStatusOrDefault() int {
	if s.ExpectStatus != 0 {
		return s.ExpectStatus
	}
	return 200
}

// ShellOrDefault is the interpreter for run steps; the command is appended.
func (s PipelineStep) ShellOrDefault() []string {
	if len(s.Shell) > 0 {
		return s.Shell
	}
	return []string{"bash", "-euo", "pipefail", "-c"}
}

// ExpandVars replaces citadel ${var} references using vals. A lowercase
// ${word} that is not a known variable is an error; ${UPPER} and $VAR are
// left untouched for the shell.
func ExpandVars(s string, vals map[string]string) (string, error) {
	var unknown string
	out := varRef.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if v, ok := vals[name]; ok {
			return v
		}
		if unknown == "" {
			unknown = name
		}
		return m
	})
	if unknown != "" {
		return "", fmt.Errorf("unknown variable ${%s} (known: %s)", unknown, strings.Join(PipelineVars, ", "))
	}
	return out, nil
}

// DefaultPipeline is today's fixed sequence, used when citadel.yml has no
// pipeline: block.
func DefaultPipeline(rt Runtime) []PipelineStep {
	names := []string{StepSSMSync, StepBuild, StepCDK, StepDeploy}
	if rt == RuntimeLambda {
		names = append(names, StepConfigSync)
	}
	names = append(names, StepWait)
	steps := make([]PipelineStep, len(names))
	for i, n := range names {
		steps[i] = PipelineStep{Uses: usesPrefix + n}
	}
	return steps
}

// ResolvedPipeline returns the declared pipeline, or the default one.
func (c *DeployConfig) ResolvedPipeline() []PipelineStep {
	if len(c.Pipeline) > 0 {
		return c.Pipeline
	}
	return DefaultPipeline(c.ResolvedRuntime())
}

func (s PipelineStep) misplacedField(kind StepKind) string {
	switch {
	case kind != KindRun && len(s.Shell) > 0:
		return "shell only applies to run steps"
	case kind != KindRun && s.WorkingDirectory != "":
		return "working_directory only applies to run steps"
	case kind != KindRun && len(s.Env) > 0:
		return "env only applies to run steps"
	case kind != KindTask && s.Container != "":
		return "container only applies to task steps"
	case kind != KindHTTP && s.ExpectStatus != 0:
		return "expect_status only applies to http steps"
	case kind != KindHTTP && s.Retries != 0:
		return "retries only applies to http steps"
	case kind != KindHTTP && s.Interval != "":
		return "interval only applies to http steps"
	case kind != KindHTTP && s.RequestTimeout != "":
		return "request_timeout only applies to http steps"
	case kind != KindHTTP && s.RollbackOnFailure:
		return "rollback_on_failure only applies to http steps"
	case kind != KindRun && kind != KindTask && s.Timeout != "":
		return "timeout only applies to run and task steps"
	}
	return ""
}

// subsetOf reports whether envs is a non-empty subset of of (an empty envs
// means "every environment").
func subsetOf(envs, of []string) bool {
	if len(envs) == 0 {
		return false
	}
	for _, e := range envs {
		if !slices.Contains(of, e) {
			return false
		}
	}
	return true
}

// validatePipeline enforces the rules from the actions-engine spec. An absent
// pipeline: (the default) is always valid.
func (c *DeployConfig) validatePipeline() error {
	if len(c.Pipeline) == 0 {
		return nil
	}
	runtime := c.ResolvedRuntime()
	sample := map[string]string{}
	for _, v := range PipelineVars {
		sample[v] = "x"
	}
	seen := map[string]bool{}
	pos := map[string]int{} // built-in name → index
	var taskIdx, rollbackIdx []int

	for i, s := range c.Pipeline {
		label := s.StepName()
		errf := func(format string, a ...any) error {
			return fmt.Errorf("pipeline[%d] %q: %s", i, label, fmt.Sprintf(format, a...))
		}
		kinds := s.kinds()
		if len(kinds) != 1 {
			return errf("set exactly one of uses, run, task, http (found %d)", len(kinds))
		}
		kind := kinds[0]

		if kind == KindBuiltin {
			b := s.Builtin()
			if !strings.HasPrefix(s.Uses, usesPrefix) || !builtinSteps[b] {
				return errf("unknown step %q (built-ins: citadel/ssm-sync, citadel/build, citadel/cdk, citadel/deploy, citadel/config-sync, citadel/wait)", s.Uses)
			}
			if _, dup := pos[b]; dup {
				return errf("citadel/%s may appear only once", b)
			}
			pos[b] = i
			if s.Name != "" && s.Name != b {
				return errf("built-in steps are named after the action; drop name:")
			}
		} else {
			if s.Name == "" {
				return errf("name is required for %s steps", kind)
			}
			if builtinSteps[s.Name] || s.Name == StepRollback {
				return errf("%q is reserved; pick another name", s.Name)
			}
		}
		if !stepNameRe.MatchString(label) {
			return errf("name must match %s", stepNameRe)
		}
		if seen[label] {
			return errf("duplicate step name")
		}
		seen[label] = true

		for _, e := range s.Envs {
			if _, ok := c.Environments[e]; !ok {
				return errf("envs: %q is not in environments", e)
			}
		}
		if msg := s.misplacedField(kind); msg != "" {
			return errf("%s", msg)
		}
		for field, v := range map[string]string{"timeout": s.Timeout, "interval": s.Interval, "request_timeout": s.RequestTimeout} {
			if v == "" {
				continue
			}
			if d, err := time.ParseDuration(v); err != nil || d <= 0 {
				return errf("%s: invalid duration %q", field, v)
			}
		}
		if s.Retries < 0 {
			return errf("retries must be at least 1")
		}
		if s.ExpectStatus != 0 && (s.ExpectStatus < 100 || s.ExpectStatus > 599) {
			return errf("expect_status must be between 100 and 599")
		}

		texts := []string{s.Run, s.HTTP, s.Task.Script, s.WorkingDirectory}
		texts = append(texts, s.Task.Args...)
		for _, v := range s.Env {
			texts = append(texts, v)
		}
		for _, text := range texts {
			if _, err := ExpandVars(text, sample); err != nil {
				return errf("%v", err)
			}
		}

		switch {
		case kind == KindTask:
			if runtime != RuntimeECS {
				return errf("task: is only supported for the ecs runtime")
			}
			taskIdx = append(taskIdx, i)
		case kind == KindHTTP && s.RollbackOnFailure:
			rollbackIdx = append(rollbackIdx, i)
		case kind == KindBuiltin && s.Builtin() == StepConfigSync && runtime != RuntimeLambda:
			return errf("citadel/config-sync is only for the lambda runtime")
		}
	}

	requireBefore := func(earlier, later string) error {
		li, ok := pos[later]
		if !ok {
			return nil
		}
		ei, ok := pos[earlier]
		if !ok {
			return fmt.Errorf("pipeline[%d] %q: requires citadel/%s earlier in the pipeline", li, later, earlier)
		}
		if ei > li {
			return fmt.Errorf("pipeline[%d] %q: must come after citadel/%s", li, later, earlier)
		}
		return nil
	}
	for _, rule := range [][2]string{
		{StepBuild, StepCDK}, {StepBuild, StepDeploy}, {StepCDK, StepDeploy},
		{StepDeploy, StepConfigSync}, {StepDeploy, StepWait},
	} {
		if rule[0] == StepCDK {
			if _, ok := pos[StepCDK]; !ok {
				continue // cdk is optional before deploy
			}
		}
		if err := requireBefore(rule[0], rule[1]); err != nil {
			return err
		}
	}
	if bi, ok := pos[StepBuild]; ok && len(c.Pipeline[bi].Envs) > 0 {
		// cdk, deploy and task: need the image citadel/build pushes, so
		// they may only run where build runs (R8).
		buildEnvs := c.Pipeline[bi].Envs
		needImage := append([]int{}, taskIdx...)
		for _, b := range []string{StepCDK, StepDeploy} {
			if i, ok := pos[b]; ok {
				needImage = append(needImage, i)
			}
		}
		sort.Ints(needImage)
		for _, i := range needImage {
			if !subsetOf(c.Pipeline[i].Envs, buildEnvs) {
				return fmt.Errorf("pipeline[%d] %q: runs in environments where citadel/build does not (build envs: %s)",
					i, c.Pipeline[i].StepName(), strings.Join(buildEnvs, ", "))
			}
		}
	}
	for _, i := range taskIdx {
		if bi, ok := pos[StepBuild]; !ok || bi > i {
			return fmt.Errorf("pipeline[%d] %q: requires citadel/build earlier in the pipeline", i, c.Pipeline[i].StepName())
		}
	}
	for _, i := range rollbackIdx {
		if di, ok := pos[StepDeploy]; !ok || di > i {
			return fmt.Errorf("pipeline[%d] %q: rollback_on_failure needs citadel/deploy earlier in the pipeline", i, c.Pipeline[i].StepName())
		}
	}
	return nil
}
