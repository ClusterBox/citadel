package config

import (
	"strings"
	"testing"
)

func TestValidate_Ingress(t *testing.T) {
	cases := map[string]struct {
		mutate  func(*DeployConfig)
		wantErr string // substring; "" means valid
	}{
		"absent":            {func(c *DeployConfig) {}, ""},
		"alb":               {func(c *DeployConfig) { setIngress(c, "dev", IngressALB) }, ""},
		"cloudflare tunnel": {func(c *DeployConfig) { setIngress(c, "dev", IngressCloudflareTunnel) }, ""},
		"unknown":           {func(c *DeployConfig) { setIngress(c, "dev", "nlb") }, `environments.dev.ingress: "nlb"`},
		"token also a secret": {func(c *DeployConfig) {
			setIngress(c, "dev", IngressCloudflareTunnel)
			c.Secrets = append(c.Secrets, TunnelTokenSecret)
		}, "remove it from secrets"},
		"token also in env": {func(c *DeployConfig) {
			setIngress(c, "dev", IngressCloudflareTunnel)
			c.Env = map[string]string{TunnelTokenSecret: "x"}
		}, "remove it from env"},
		"token name free without a tunnel": {func(c *DeployConfig) {
			c.Secrets = append(c.Secrets, TunnelTokenSecret)
		}, ""},
	}
	for name, tc := range cases {
		cfg := baseValidConfig()
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

func TestResolvedIngress_DefaultsToALB(t *testing.T) {
	if got := (&EnvConfig{}).ResolvedIngress(); got != IngressALB {
		t.Fatalf("ResolvedIngress() = %q, want %q", got, IngressALB)
	}
}

func setIngress(c *DeployConfig, env string, ingress Ingress) {
	e := c.Environments[env]
	e.Ingress = ingress
	c.Environments[env] = e
}
