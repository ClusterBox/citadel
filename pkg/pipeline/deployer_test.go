package pipeline

import (
	"testing"

	"github.com/ClusterBox/citadel/internal/aws"
	"github.com/ClusterBox/citadel/pkg/config"
)

func TestSelectDeployerByRuntime(t *testing.T) {
	awsClient := &aws.Client{}

	ecsCfg := &config.DeployConfig{Name: "legolas", Runtime: config.RuntimeECS}
	if _, ok := selectDeployer(ecsCfg, awsClient).(ecsDeployer); !ok {
		t.Errorf("ecs runtime: expected ecsDeployer")
	}

	lambdaCfg := &config.DeployConfig{Name: "smaug", Runtime: config.RuntimeLambda}
	if _, ok := selectDeployer(lambdaCfg, awsClient).(lambdaDeployer); !ok {
		t.Errorf("lambda runtime: expected lambdaDeployer")
	}

	// default (unset runtime) resolves to ecs
	defCfg := &config.DeployConfig{Name: "legolas"}
	if _, ok := selectDeployer(defCfg, awsClient).(ecsDeployer); !ok {
		t.Errorf("default runtime: expected ecsDeployer")
	}
}
