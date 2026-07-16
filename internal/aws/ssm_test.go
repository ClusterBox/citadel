package aws

import (
	"testing"

	"github.com/ClusterBox/citadel/pkg/config"
)

func TestSecretParamName_EnvNamespaced(t *testing.T) {
	cfg := &config.DeployConfig{Name: "legolas"}

	if got := secretParamName(cfg, "dev", "DATABASE_URL"); got != "/legolas-dev/DATABASE_URL" {
		t.Errorf("dev: got %q, want /legolas-dev/DATABASE_URL", got)
	}
	if got := secretParamName(cfg, "prod", "PAYSTACK_SECRET_KEY"); got != "/legolas-prod/PAYSTACK_SECRET_KEY" {
		t.Errorf("prod: got %q, want /legolas-prod/PAYSTACK_SECRET_KEY", got)
	}
}

func TestSecretPrefix_MatchesSecretParamName(t *testing.T) {
	cfg := &config.DeployConfig{Name: "smaug"}

	if got := SecretPrefix(cfg, "dev"); got != "/smaug-dev" {
		t.Errorf("prefix: got %q, want /smaug-dev", got)
	}
	// The manifest consumer joins prefix + "/" + name; that must equal what
	// SyncSecrets writes via secretParamName.
	joined := SecretPrefix(cfg, "dev") + "/" + "DATABASE_URL"
	if want := secretParamName(cfg, "dev", "DATABASE_URL"); joined != want {
		t.Errorf("joined path %q does not match secretParamName %q", joined, want)
	}
}
