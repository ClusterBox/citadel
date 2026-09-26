package pipeline

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/ClusterBox/citadel/pkg/config"
)

// TestCDKStep_NoImageFails covers ruling R8: cdk deploys imageTag=<sha>, so
// it must not run when citadel/build was filtered out for this environment.
func TestCDKStep_NoImageFails(t *testing.T) {
	cdkCalls := 0
	sc := &StepContext{Cfg: &config.DeployConfig{Name: "demo"}, Opts: &DeployOptions{DeployInfra: true}, Env: "prod",
		ops: &ops{cdk: func(context.Context, io.Writer, *config.DeployConfig, *DeployOptions) error { cdkCalls++; return nil }}}
	err := cdkStep{}.Run(context.Background(), sc, &bytes.Buffer{})
	if err == nil || err.Error() != "no image for cdk: citadel/build did not run for prod" {
		t.Fatalf("err = %v", err)
	}
	if cdkCalls != 0 {
		t.Fatal("cdk ran without an image")
	}
}

func TestDeployStep_NoImageFails(t *testing.T) {
	l := fakeOps{}
	sc := &StepContext{Cfg: &config.DeployConfig{Name: "demo"}, Opts: &DeployOptions{}, Env: "prod", deployer: fakeDeployer{&l}}
	err := deployStep{}.Run(context.Background(), sc, &bytes.Buffer{})
	if err == nil || err.Error() != "no image to deploy: citadel/build did not run for prod" {
		t.Fatalf("err = %v", err)
	}
	if len(l.calls) != 0 {
		t.Fatalf("deployed without an image: %v", l.calls)
	}
}
