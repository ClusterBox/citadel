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
