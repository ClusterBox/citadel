package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/ClusterBox/citadel/internal/env"
	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// SyncResult holds the results of a secret sync operation
type SyncResult struct {
	Updated int
	Skipped int
	Missing []string
}

// SyncSecrets synchronizes secrets from .env file to the env-namespaced SSM
// prefix "/<name>-<envName>/<KEY>" (see DeployConfig.ResolvedName), so dev and
// prod secrets stay isolated even when they share one AWS account. The envName
// parameter is named to avoid shadowing the imported "env" package below.
func (c *Client) SyncSecrets(ctx context.Context, cfg *config.DeployConfig, envName, envFile string, dryRun bool) (*SyncResult, error) {
	// Load environment variables from file
	envVars, err := env.Load(envFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load env file: %w", err)
	}

	// Validate all required secrets are present
	missing := env.Validate(cfg.Secrets, envVars)
	if len(missing) > 0 {
		return &SyncResult{Missing: missing}, fmt.Errorf("missing required secrets in %s: %v", envFile, missing)
	}

	result := &SyncResult{}

	// Sync each secret to SSM
	for _, secretName := range cfg.Secrets {
		value := envVars[secretName]
		paramName := secretParamName(cfg, envName, secretName)

		// Get existing parameter value if it exists
		existing, err := c.getParameter(ctx, paramName)
		if err != nil && err.Error() != "parameter not found" {
			return result, fmt.Errorf("failed to get parameter %s: %w", paramName, err)
		}

		// Skip if value hasn't changed
		if existing == value {
			result.Skipped++
			continue
		}

		// Update parameter
		if !dryRun {
			if err := c.putParameter(ctx, paramName, value); err != nil {
				return result, fmt.Errorf("failed to put parameter %s: %w", paramName, err)
			}
		}

		result.Updated++
	}

	return result, nil
}

// secretParamName builds the env-namespaced SSM parameter path for a secret:
// "/<name>-<envName>/<KEY>" (e.g. /legolas-dev/DATABASE_URL). Keeping this in
// one place means the convention matches the CDK construct's SSM prefix.
func secretParamName(cfg *config.DeployConfig, envName, secretName string) string {
	return fmt.Sprintf("/%s/%s", cfg.ResolvedName(envName), secretName)
}

// getParameter retrieves a parameter value from SSM
func (c *Client) getParameter(ctx context.Context, name string) (string, error) {
	input := &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	}

	output, err := c.SSM.GetParameter(ctx, input)
	if err != nil {
		var pnf *types.ParameterNotFound
		if errors.As(err, &pnf) {
			return "", fmt.Errorf("parameter not found")
		}
		return "", err
	}

	if output.Parameter == nil || output.Parameter.Value == nil {
		return "", fmt.Errorf("parameter value is nil")
	}

	return *output.Parameter.Value, nil
}

// putParameter stores or updates a parameter in SSM
func (c *Client) putParameter(ctx context.Context, name, value string) error {
	input := &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(value),
		Type:      types.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	}

	_, err := c.SSM.PutParameter(ctx, input)
	return err
}
