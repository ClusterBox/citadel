package config

import (
	"fmt"
	"strings"
)

// envPlaceholder is replaced with the deployment environment (dev, prod, ...)
// wherever a citadel.yml value may differ per environment. It matches the
// placeholder lambda.functionName already accepts.
const envPlaceholder = "{env}"

// ExpandEnv substitutes the environment name for every {env} in s.
func ExpandEnv(s, env string) string {
	return strings.ReplaceAll(s, envPlaceholder, env)
}

// hasUnknownPlaceholder reports whether s has a brace other than {env}: a
// typo such as {stage} would otherwise be granted literally and fail only at
// runtime with AccessDenied.
func hasUnknownPlaceholder(s string) bool {
	return strings.ContainsAny(strings.ReplaceAll(s, envPlaceholder, ""), "{}")
}

// IAMConfig declares extra runtime permissions for the ECS task role
// (optional). Boot-time secrets never need it: the execution role already
// reads /<name>-<env>/*.
type IAMConfig struct {
	// SSMRead lists SSM parameter path prefixes the application reads at
	// runtime, e.g. "/smaug/{env}/psp". Each grants ssm:GetParameter and
	// ssm:GetParameters on every parameter under the prefix.
	SSMRead []string `yaml:"ssm_read,omitempty"`
}

// validateIAM checks the iam: block. An absent block is valid.
func (c *DeployConfig) validateIAM() error {
	if c.IAM == nil {
		return nil
	}
	if c.ResolvedRuntime() != RuntimeECS && len(c.IAM.SSMRead) > 0 {
		return fmt.Errorf("iam.ssm_read is only supported for the %q runtime", RuntimeECS)
	}
	for i, p := range c.IAM.SSMRead {
		trimmed := strings.TrimSuffix(p, "/")
		switch {
		case !strings.HasPrefix(p, "/"):
			return fmt.Errorf("iam.ssm_read[%d]: %q must start with '/'", i, p)
		case trimmed == "":
			return fmt.Errorf("iam.ssm_read[%d]: %q would grant every parameter in the account; name a prefix", i, p)
		case strings.ContainsAny(p, "*?"):
			return fmt.Errorf("iam.ssm_read[%d]: %q must not contain wildcards; the prefix already covers everything under it", i, p)
		case strings.Contains(p, "//"):
			return fmt.Errorf("iam.ssm_read[%d]: %q contains an empty path segment", i, p)
		case hasUnknownPlaceholder(p):
			return fmt.Errorf("iam.ssm_read[%d]: %q has a placeholder other than {env}", i, p)
		}
	}
	return nil
}

// SSMReadResources returns the SSM parameter ARNs the task role may read in
// env: one "<prefix>/*" ARN per iam.ssm_read entry, with {env} expanded.
// Nil when the block is absent.
func (c *DeployConfig) SSMReadResources(env string) []string {
	if c.IAM == nil {
		return nil
	}
	out := make([]string, 0, len(c.IAM.SSMRead))
	for _, p := range c.IAM.SSMRead {
		prefix := strings.TrimSuffix(ExpandEnv(p, env), "/")
		out = append(out, fmt.Sprintf("arn:aws:ssm:*:*:parameter%s/*", prefix))
	}
	return out
}
