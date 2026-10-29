package config

import (
	"fmt"
	"regexp"
	"sort"
)

var vpcIDPattern = regexp.MustCompile(`^vpc-[0-9a-f]{8,17}$`)

// validateVPC checks every environment's vpc_id. A lookup needs a concrete
// account, so an environment reusing a VPC must declare one.
func (c *DeployConfig) validateVPC() error {
	envs := make([]string, 0, len(c.Environments))
	for env := range c.Environments {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	for _, env := range envs {
		e := c.Environments[env]
		if e.VPCID == "" {
			continue
		}
		if !vpcIDPattern.MatchString(e.VPCID) {
			return fmt.Errorf("environments.%s.vpc_id: %q is not a VPC ID (vpc-...)", env, e.VPCID)
		}
		if e.Account == "" {
			return fmt.Errorf("environments.%s.vpc_id: needs environments.%s.account to look the VPC up", env, env)
		}
	}
	return nil
}
