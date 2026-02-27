package main

import (
	"os"

	"github.com/ClusterBox/citadel/pkg/constructs/deployable"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	// Get environment from context
	env := "dev"
	if contextEnv := app.Node().TryGetContext(jsii.String("env")); contextEnv != nil {
		env = contextEnv.(string)
	}

	// Create stack using DeployableService construct
	// This reads ../citadel.yml and auto-generates everything
	deployable.NewDeployableService(app, "legolas-"+env, &deployable.DeployableServiceProps{
		StackProps: awscdk.StackProps{
			StackName: jsii.String("legolas-" + env),
			Env: &awscdk.Environment{
				Region: jsii.String("us-east-1"),
			},
			Description: jsii.String("Legolas " + env + " - Deployed by Citadel"),
		},
		ConfigPath:  "../citadel.yml",
		Environment: env,
	})

	app.Synth(nil)
}
