package config

import (
	"strings"
	"testing"
)

func TestValidate_Cache(t *testing.T) {
	cases := map[string]struct {
		mutate  func(*DeployConfig)
		wantErr string // substring; "" means valid
	}{
		"absent":                 {func(c *DeployConfig) { c.Cache = nil }, ""},
		"valkey serverless":      {func(c *DeployConfig) {}, ""},
		"other engine":           {func(c *DeployConfig) { c.Cache.Engine = "redis" }, `"redis" is not supported`},
		"missing engine":         {func(c *DeployConfig) { c.Cache.Engine = "" }, `"" is not supported`},
		"not serverless":         {func(c *DeployConfig) { c.Cache.Serverless = false }, "serverless must be true"},
		"one AZ":                 {func(c *DeployConfig) { c.VPC = &VPCConfig{MaxAZs: 1} }, "at least 2 availability zones"},
		"two AZs":                {func(c *DeployConfig) { c.VPC = &VPCConfig{MaxAZs: 2} }, ""},
		"endpoint also a secret": {func(c *DeployConfig) { c.Secrets = append(c.Secrets, "VALKEY_ENDPOINT") }, "remove it from secrets"},
		"endpoint also in env":   {func(c *DeployConfig) { c.Env = map[string]string{"VALKEY_ENDPOINT": "x"} }, "remove it from env"},
		"uppercase name":         {func(c *DeployConfig) { c.Name = "Demo" }, `"Demo-dev"`},
		"name too long":          {func(c *DeployConfig) { c.Name = strings.Repeat("a", 37) }, "at most 40"},
		"name at the limit":      {func(c *DeployConfig) { c.Name = strings.Repeat("a", 36) }, ""},
		"double hyphen":          {func(c *DeployConfig) { c.Name = "demo-" }, `"demo--dev"`},
	}
	for name, tc := range cases {
		cfg := baseValidConfig()
		cfg.Cache = &CacheConfig{Engine: "valkey", Serverless: true}
		tc.mutate(cfg)
		err := cfg.Validate()
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.wantErr)
		}
	}
}

func TestValidate_CacheRejectedForLambdaRuntime(t *testing.T) {
	cfg := &DeployConfig{
		Name: "smaug", Region: "us-east-1", Runtime: RuntimeLambda,
		Environments: map[string]EnvConfig{"dev": {Account: "111111111111"}},
		Cache:        &CacheConfig{Engine: "valkey", Serverless: true},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "only supported for the \"ecs\" runtime") {
		t.Fatalf("err = %v, want an ecs-only error", err)
	}
}

func TestCacheName(t *testing.T) {
	cfg := baseValidConfig()
	if got := cfg.CacheName("prod"); got != "demo-prod" {
		t.Errorf("CacheName = %q, want demo-prod", got)
	}
}
