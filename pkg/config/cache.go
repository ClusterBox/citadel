package config

import (
	"fmt"
	"regexp"
	"sort"
)

// CacheEndpointEnvVar is the plain environment variable the cache: block
// injects into the container, holding the cache's "host:port" endpoint.
const CacheEndpointEnvVar = "VALKEY_ENDPOINT"

// CacheEngineValkey is the only cache engine citadel provisions.
const CacheEngineValkey = "valkey"

// CacheConfig asks citadel to create an ElastiCache Serverless cache in the
// service's VPC (optional). Only the task's security group may reach it, and
// its endpoint is injected as VALKEY_ENDPOINT. Serverless caches always
// require TLS.
type CacheConfig struct {
	Engine     string `yaml:"engine"`
	Serverless bool   `yaml:"serverless"`
}

// serverlessCacheName is ElastiCache's naming rule (with the 40-character
// limit checked separately): a lowercase letter, then lowercase letters,
// digits and single hyphens, not ending in a hyphen.
var serverlessCacheName = regexp.MustCompile(`^[a-z](?:[a-z0-9]|-[a-z0-9])*$`)

// validateCache checks the cache: block. An absent block is valid.
func (c *DeployConfig) validateCache() error {
	if c.Cache == nil {
		return nil
	}
	if c.ResolvedRuntime() != RuntimeECS {
		return fmt.Errorf("cache: is only supported for the %q runtime", RuntimeECS)
	}
	if c.Cache.Engine != CacheEngineValkey {
		return fmt.Errorf("cache.engine: %q is not supported; use %q", c.Cache.Engine, CacheEngineValkey)
	}
	if !c.Cache.Serverless {
		return fmt.Errorf("cache.serverless must be true; only ElastiCache Serverless is supported")
	}
	if c.VPC != nil && c.VPC.MaxAZs == 1 {
		return fmt.Errorf("cache: needs subnets in at least 2 availability zones; raise vpc.max_azs")
	}
	if _, ok := c.Env[CacheEndpointEnvVar]; ok {
		return fmt.Errorf("%s is set by cache:; remove it from env", CacheEndpointEnvVar)
	}
	for _, s := range c.Secrets {
		if s == CacheEndpointEnvVar {
			return fmt.Errorf("%s is set by cache:; remove it from secrets", CacheEndpointEnvVar)
		}
	}
	envs := make([]string, 0, len(c.Environments))
	for env := range c.Environments {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	for _, env := range envs {
		if name := c.CacheName(env); len(name) > 40 || !serverlessCacheName.MatchString(name) {
			return fmt.Errorf("cache: name %q (from <name>-<env>) must be at most 40 lowercase letters, digits and single hyphens, starting with a letter", name)
		}
	}
	return nil
}

// CacheName is the ElastiCache Serverless cache name for env: "<name>-<env>",
// like every other per-environment resource.
func (c *DeployConfig) CacheName(env string) string {
	return c.ResolvedName(env)
}
