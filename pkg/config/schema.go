package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Runtime identifies which AWS compute primitive a service runs on. The
// citadel deploy CLI only handles RuntimeECS; RuntimeLambda exists so the
// citadel-logs daemon can ingest logs from Lambda-backed services like smaug
// using the same citadel.yml registry mechanism.
type Runtime string

const (
	RuntimeECS    Runtime = "ecs"
	RuntimeLambda Runtime = "lambda"
)

// DeployConfig represents the citadel.yml schema
type DeployConfig struct {
	Name         string               `yaml:"name"`
	Region       string               `yaml:"region"`
	Runtime      Runtime              `yaml:"runtime,omitempty"`
	Lambda       *LambdaConfig        `yaml:"lambda,omitempty"`
	Container    ContainerConfig      `yaml:"container"`
	Environments map[string]EnvConfig `yaml:"environments"`
	Secrets      []string             `yaml:"secrets"`
	Queues       *QueuesConfig        `yaml:"queues,omitempty"`
	ECS          *ECSConfig           `yaml:"ecs,omitempty"`
	VPC          *VPCConfig           `yaml:"vpc,omitempty"`
	CloudFront   *CloudFrontConfig    `yaml:"cloudfront,omitempty"`
}

// LambdaConfig declares Lambda-specific metadata used by the logs daemon.
// Required when runtime: lambda.
type LambdaConfig struct {
	FunctionName string `yaml:"functionName"`
}

// ResolvedRuntime returns the runtime with the implicit default (ecs) applied.
func (c *DeployConfig) ResolvedRuntime() Runtime {
	if c.Runtime == "" {
		return RuntimeECS
	}
	return c.Runtime
}

// ECSConfig overrides how Citadel locates an existing ECS service. When unset,
// Citadel falls back to the "<name>-cluster" / "<name>-service" convention.
// Useful for projects whose ECS resources were not created by Citadel.
type ECSConfig struct {
	Cluster string `yaml:"cluster,omitempty"`
	Service string `yaml:"service,omitempty"`
}

// QueuesConfig declares the SQS queues a service may access, split by intent.
// Consume queues receive read/delete permissions; produce queues receive send
// permissions. A queue ARN may appear in both lists.
type QueuesConfig struct {
	Consume []string `yaml:"consume,omitempty"`
	Produce []string `yaml:"produce,omitempty"`
}

// ContainerConfig defines container runtime settings
type ContainerConfig struct {
	Port                   int    `yaml:"port"`
	CPU                    int    `yaml:"cpu"`
	Memory                 int    `yaml:"memory"`
	HealthCheckPath        string `yaml:"health_check_path"`
	HealthCheckGracePeriod int    `yaml:"health_check_grace_period"`
}

// EnvConfig defines per-environment settings
type EnvConfig struct {
	Account     string `yaml:"account"`
	MinCapacity int    `yaml:"min_capacity"`
	MaxCapacity int    `yaml:"max_capacity"`
	FargateSpot bool   `yaml:"fargate_spot,omitempty"`
}

// VPCConfig defines custom VPC settings (optional)
type VPCConfig struct {
	MaxAZs      int  `yaml:"max_azs"`
	NATGateways *int `yaml:"nat_gateways,omitempty"`
}

// CloudFrontConfig defines CloudFront distribution settings (optional)
type CloudFrontConfig struct {
	Enabled bool   `yaml:"enabled"`
	Comment string `yaml:"comment,omitempty"`
}

// Load reads and parses a citadel.yml file
func Load(path string) (*DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg DeployConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// Validate checks if the config is valid for the citadel deploy pipeline.
// Lambda-runtime configs skip ECS-only field requirements but still must
// declare a function name.
func (c *DeployConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Region == "" {
		return fmt.Errorf("region is required")
	}
	switch c.ResolvedRuntime() {
	case RuntimeECS:
		if c.Container.Port == 0 {
			return fmt.Errorf("container.port is required")
		}
		if c.Container.CPU == 0 {
			return fmt.Errorf("container.cpu is required")
		}
		if c.Container.Memory == 0 {
			return fmt.Errorf("container.memory is required")
		}
		if len(c.Environments) == 0 {
			return fmt.Errorf("at least one environment is required")
		}
		if len(c.Secrets) == 0 {
			return fmt.Errorf("at least one secret is required")
		}
	case RuntimeLambda:
		if c.Lambda == nil || c.Lambda.FunctionName == "" {
			return fmt.Errorf("lambda.functionName is required when runtime: lambda")
		}
	default:
		return fmt.Errorf("runtime %q: must be %q or %q", c.Runtime, RuntimeECS, RuntimeLambda)
	}
	if err := c.validateQueues(); err != nil {
		return err
	}
	return nil
}

// validateQueues checks that every queue ARN under queues: is a well-formed
// SQS ARN. An absent queues: block is valid.
func (c *DeployConfig) validateQueues() error {
	if c.Queues == nil {
		return nil
	}
	for i, arn := range c.Queues.Consume {
		if !isValidSQSARN(arn) {
			return fmt.Errorf("queues.consume[%d]: %q is not a valid SQS ARN", i, arn)
		}
	}
	for i, arn := range c.Queues.Produce {
		if !isValidSQSARN(arn) {
			return fmt.Errorf("queues.produce[%d]: %q is not a valid SQS ARN", i, arn)
		}
	}
	return nil
}

// isValidSQSARN reports whether s has the shape
// arn:aws:sqs:<region>:<account-id>:<queue-name> with all six segments present.
func isValidSQSARN(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return false
	}
	if parts[0] != "arn" || parts[1] != "aws" || parts[2] != "sqs" {
		return false
	}
	return parts[3] != "" && parts[4] != "" && parts[5] != ""
}

// GetEnv returns the config for a specific environment
func (c *DeployConfig) GetEnv(name string) (*EnvConfig, error) {
	env, ok := c.Environments[name]
	if !ok {
		return nil, fmt.Errorf("environment %q not found", name)
	}
	return &env, nil
}
