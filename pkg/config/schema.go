package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DeployConfig represents the deploy.yml schema
type DeployConfig struct {
	Name         string                 `yaml:"name"`
	Region       string                 `yaml:"region"`
	Container    ContainerConfig        `yaml:"container"`
	Environments map[string]EnvConfig   `yaml:"environments"`
	Secrets      []string               `yaml:"secrets"`
	VPC          *VPCConfig             `yaml:"vpc,omitempty"`
	CloudFront   *CloudFrontConfig      `yaml:"cloudfront,omitempty"`
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

// Load reads and parses a deploy.yml file
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

// Validate checks if the config is valid
func (c *DeployConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Region == "" {
		return fmt.Errorf("region is required")
	}
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
	return nil
}

// GetEnv returns the config for a specific environment
func (c *DeployConfig) GetEnv(name string) (*EnvConfig, error) {
	env, ok := c.Environments[name]
	if !ok {
		return nil, fmt.Errorf("environment %q not found", name)
	}
	return &env, nil
}
