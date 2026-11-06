package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CacheEndpointEnvVar is the plain environment variable the cache: block
// injects into the container, holding the cache's "host:port" endpoint.
const CacheEndpointEnvVar = "VALKEY_ENDPOINT"

// CacheTLSEnvVar is set to "false" in cache_mode: sidecar environments, whose
// local Valkey has no TLS. Serverless caches always require TLS, so it is not
// set for them (clients default to TLS).
const CacheTLSEnvVar = "VALKEY_TLS"

// Cache modes, per environment (cache_mode:).
const (
	// CacheModeServerless is an ElastiCache Serverless cache (the default).
	CacheModeServerless = "serverless"
	// CacheModeSidecar is a Valkey container in every task, on localhost
	// without TLS or persistence. It costs nothing beyond the task, but each
	// task has its own cache (nothing is shared between replicas) and the data
	// is lost whenever a task stops. Meant for dev, where a serverless cache
	// still bills its 100 MB storage minimum (~$6/month) while nearly empty.
	CacheModeSidecar = "sidecar"
)

// ResolvedCacheMode returns the environment's cache mode, defaulting to
// CacheModeServerless.
func (e *EnvConfig) ResolvedCacheMode() string {
	if e.CacheMode == "" {
		return CacheModeServerless
	}
	return e.CacheMode
}

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
	envs := make([]string, 0, len(c.Environments))
	for env := range c.Environments {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	sidecar := false
	for _, env := range envs {
		switch mode := c.Environments[env].CacheMode; mode {
		case "", CacheModeServerless:
		case CacheModeSidecar:
			sidecar = true
		default:
			return fmt.Errorf("environments.%s.cache_mode: %q is not supported; use %q or %q", env, mode, CacheModeServerless, CacheModeSidecar)
		}
		if c.Environments[env].CacheMode != "" && c.Cache == nil {
			return fmt.Errorf("environments.%s.cache_mode is set but there is no cache: block", env)
		}
	}
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
		if sidecar && s == CacheTLSEnvVar {
			return fmt.Errorf("%s is set by cache_mode: sidecar; remove it from secrets", CacheTLSEnvVar)
		}
	}
	if _, ok := c.Env[CacheTLSEnvVar]; sidecar && ok {
		return fmt.Errorf("%s is set by cache_mode: sidecar; remove it from env", CacheTLSEnvVar)
	}
	for _, env := range envs {
		if name := c.CacheName(env); len(name) > 40 || !serverlessCacheName.MatchString(name) {
			return fmt.Errorf("cache: name %q (from <name>-<env>) must be at most 40 lowercase letters, digits and single hyphens, starting with a letter", name)
		}
	}
	return nil
}

// CacheName is the ElastiCache Serverless cache name for env: "<name>-<env>",
// like every other per-environment resource. In a vpc_id environment it ends
// with the VPC ID's first 8 hex digits ("<name>-<env>-01f19f79"): a cache
// moving VPCs is replaced, and CloudFormation creates the replacement before
// deleting the old one, so the two names must differ.
func (c *DeployConfig) CacheName(env string) string {
	name := c.ResolvedName(env)
	if e, ok := c.Environments[env]; ok && e.VPCID != "" {
		hex := strings.TrimPrefix(e.VPCID, "vpc-")
		name += "-" + hex[:min(8, len(hex))]
	}
	return name
}
