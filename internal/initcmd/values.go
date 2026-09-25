// Package initcmd implements `citadel init`: it gathers values from flags,
// the environment and prompts, renders a commented citadel.yml from an
// embedded per-runtime template, and creates the project's .citadel/ dir.
package initcmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ClusterBox/citadel/pkg/config"
)

// FallbackRegion is used when neither --region nor AWS_REGION /
// AWS_DEFAULT_REGION is set.
const FallbackRegion = "us-east-1"

// Values are the answers that fill a citadel.yml template.
type Values struct {
	Runtime         config.Runtime
	Name            string
	Region          string
	Account         string
	Envs            []string
	Port            int
	CPU             int
	Memory          int
	HealthCheckPath string
	FunctionName    string
	Secrets         []string
}

// DefaultValues returns the values used when a flag is not passed and nothing
// is detected. Name, region and account are filled in by Run.
func DefaultValues() Values {
	return Values{
		Envs:            []string{"dev", "prod"},
		Port:            3000,
		CPU:             256,
		Memory:          512,
		HealthCheckPath: "/health",
	}
}

var (
	nameRe         = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	regionRe       = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	accountRe      = regexp.MustCompile(`^[0-9]{12}$`)
	secretRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	functionNameRe = regexp.MustCompile(`^[A-Za-z0-9_{}-]+$`)
)

// ValidateName checks a project name. It becomes part of every AWS resource
// name ("<name>-<env>-cluster", ...), so it is restricted to lowercase DNS-ish.
func ValidateName(s string) error {
	if !nameRe.MatchString(s) {
		return fmt.Errorf("name %q: use lowercase letters, digits and dashes, e.g. my-api", s)
	}
	return nil
}

// ValidateRegion checks the shape of an AWS region name.
func ValidateRegion(s string) error {
	if !regionRe.MatchString(s) {
		return fmt.Errorf("region %q: expected an AWS region such as us-east-1", s)
	}
	return nil
}

// ValidateAccount checks a 12-digit AWS account ID.
func ValidateAccount(s string) error {
	if !accountRe.MatchString(s) {
		return fmt.Errorf("account %q: must be the 12-digit AWS account ID", s)
	}
	return nil
}

// ValidateEnvName checks an environment name (used in resource names too).
func ValidateEnvName(s string) error {
	if !nameRe.MatchString(s) {
		return fmt.Errorf("environment %q: use lowercase letters, digits and dashes, e.g. dev", s)
	}
	return nil
}

// ValidateSecretName checks an env-var name declared under secrets:.
func ValidateSecretName(s string) error {
	if !secretRe.MatchString(s) {
		return fmt.Errorf("secret %q: must be an env var name such as DATABASE_URL", s)
	}
	return nil
}

// ValidateHealthCheckPath checks the container health check path.
func ValidateHealthCheckPath(s string) error {
	if !strings.HasPrefix(s, "/") {
		return fmt.Errorf("health check path %q: must start with /", s)
	}
	return nil
}

// ValidateFunctionName checks lambda.functionName ("{env}" is allowed).
func ValidateFunctionName(s string) error {
	if !functionNameRe.MatchString(s) {
		return fmt.Errorf("function name %q: use letters, digits, - and _ (\"{env}\" is substituted)", s)
	}
	return nil
}

// Validate checks every value for v.Runtime. Render calls it before rendering.
func (v Values) Validate() error {
	if v.Runtime != config.RuntimeECS && v.Runtime != config.RuntimeLambda {
		return fmt.Errorf("runtime %q: citadel init supports %q and %q", v.Runtime, config.RuntimeECS, config.RuntimeLambda)
	}
	for _, check := range []struct {
		fn  func(string) error
		val string
	}{{ValidateName, v.Name}, {ValidateRegion, v.Region}, {ValidateAccount, v.Account}} {
		if err := check.fn(check.val); err != nil {
			return err
		}
	}
	if len(v.Envs) == 0 {
		return fmt.Errorf("at least one environment is required (--envs dev,prod)")
	}
	if err := validateList(v.Envs, ValidateEnvName, "environment"); err != nil {
		return err
	}
	if err := validateList(v.Secrets, ValidateSecretName, "secret"); err != nil {
		return err
	}
	switch v.Runtime {
	case config.RuntimeECS:
		if v.Port < 1 || v.Port > 65535 {
			return fmt.Errorf("port %d: must be between 1 and 65535", v.Port)
		}
		if v.CPU <= 0 {
			return fmt.Errorf("cpu %d: must be positive (256 = 0.25 vCPU)", v.CPU)
		}
		if v.Memory <= 0 {
			return fmt.Errorf("memory %d: must be positive (MiB)", v.Memory)
		}
		if err := ValidateHealthCheckPath(v.HealthCheckPath); err != nil {
			return err
		}
		if len(v.Secrets) == 0 {
			return fmt.Errorf("the ecs runtime needs at least one secret name: pass --secrets DATABASE_URL (names only; values come from your .env file)")
		}
	case config.RuntimeLambda:
		if v.FunctionName != "" {
			if err := ValidateFunctionName(v.FunctionName); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateList(items []string, check func(string) error, kind string) error {
	seen := map[string]bool{}
	for _, it := range items {
		if err := check(it); err != nil {
			return err
		}
		if seen[it] {
			return fmt.Errorf("%s %q is listed twice", kind, it)
		}
		seen[it] = true
	}
	return nil
}

// SanitizeName turns a directory name into a default project name: lowercase
// ASCII letters and digits, with each run of other characters collapsed to one
// dash and leading/trailing dashes dropped. It falls back to "app".
func SanitizeName(s string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
			continue
		}
		pendingDash = true
	}
	if b.Len() == 0 {
		return "app"
	}
	return b.String()
}

// SplitList parses a comma-separated flag or prompt answer: items are trimmed,
// empty items dropped, duplicates removed (first occurrence wins).
func SplitList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
