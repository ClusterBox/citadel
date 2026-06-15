package deployable

import (
	"fmt"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudfront"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudfrontorigins"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsec2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsecr"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsecs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsecspatterns"
	"github.com/aws/aws-cdk-go/awscdk/v2/awselasticloadbalancingv2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsssm"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// DeployableServiceProps defines the props for DeployableService
type DeployableServiceProps struct {
	awscdk.StackProps
	ConfigPath  string
	Environment string
}

// DeployableService creates a complete ECS Fargate service from citadel.yml
func NewDeployableService(scope constructs.Construct, id string, props *DeployableServiceProps) awscdk.Stack {
	var sprops awscdk.StackProps
	if props != nil {
		sprops = props.StackProps
	}
	stack := awscdk.NewStack(scope, &id, &sprops)

	// Load citadel.yml config
	cfg, err := config.Load(props.ConfigPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	// Get environment-specific config
	envCfg, err := cfg.GetEnv(props.Environment)
	if err != nil {
		panic(fmt.Sprintf("Failed to get environment config: %v", err))
	}

	// Build VPC
	vpc := buildVPC(stack, cfg, envCfg, props.Environment)

	// Build ECR repository
	repo := buildECRRepository(stack, cfg)

	// Build ECS cluster
	cluster := buildECSCluster(stack, cfg, vpc, props.Environment)

	// Build IAM roles
	executionRole, taskRole := buildIAMRoles(stack, cfg)

	// Build log group
	logGroup := buildLogGroup(stack, cfg)

	// Build task definition
	taskDef := buildTaskDefinition(stack, cfg, envCfg, executionRole, taskRole)

	// Add container with auto-generated secrets
	buildContainer(stack, cfg, envCfg, taskDef, repo, logGroup, props.Environment)

	// Build Fargate service
	service := buildFargateService(stack, cfg, envCfg, cluster, taskDef, vpc, props.Environment)

	// Configure health check
	configureHealthCheck(service, cfg)

	// Build CloudFront (if enabled)
	var distribution awscloudfront.Distribution
	if cfg.CloudFront != nil && cfg.CloudFront.Enabled {
		distribution = buildCloudFront(stack, cfg, service, props.Environment)
	}

	// Outputs
	buildOutputs(stack, cfg, service, repo, distribution)

	return stack
}

// buildVPC creates a VPC with environment-specific configuration
func buildVPC(stack awscdk.Stack, cfg *config.DeployConfig, envCfg *config.EnvConfig, env string) awsec2.Vpc {
	maxAZs := 2
	if cfg.VPC != nil && cfg.VPC.MaxAZs > 0 {
		maxAZs = cfg.VPC.MaxAZs
	}

	// Dev: Public subnets only (no NAT Gateway cost)
	if env == "dev" {
		return awsec2.NewVpc(stack, jsii.String("Vpc"), &awsec2.VpcProps{
			MaxAzs: jsii.Number(float64(maxAZs)),
			SubnetConfiguration: &[]*awsec2.SubnetConfiguration{
				{
					Name:       jsii.String("Public"),
					SubnetType: awsec2.SubnetType_PUBLIC,
					CidrMask:   jsii.Number(24),
				},
			},
		})
	}

	// Prod: Private subnets with NAT
	natGateways := 1
	if cfg.VPC != nil && cfg.VPC.NATGateways != nil {
		natGateways = *cfg.VPC.NATGateways
	}

	return awsec2.NewVpc(stack, jsii.String("Vpc"), &awsec2.VpcProps{
		MaxAzs: jsii.Number(float64(maxAZs)),
		SubnetConfiguration: &[]*awsec2.SubnetConfiguration{
			{
				Name:       jsii.String("Public"),
				SubnetType: awsec2.SubnetType_PUBLIC,
				CidrMask:   jsii.Number(24),
			},
			{
				Name:       jsii.String("Private"),
				SubnetType: awsec2.SubnetType_PRIVATE_WITH_EGRESS,
				CidrMask:   jsii.Number(24),
			},
		},
		NatGateways: jsii.Number(float64(natGateways)),
	})
}

// buildECRRepository imports or references the ECR repository
func buildECRRepository(stack awscdk.Stack, cfg *config.DeployConfig) awsecr.IRepository {
	repoName := fmt.Sprintf("%s-repo", cfg.Name)
	return awsecr.Repository_FromRepositoryName(stack, jsii.String("EcrRepo"), jsii.String(repoName))
}

// buildECSCluster creates an ECS cluster
func buildECSCluster(stack awscdk.Stack, cfg *config.DeployConfig, vpc awsec2.Vpc, env string) awsecs.Cluster {
	containerInsights := awsecs.ContainerInsights_DISABLED
	if env == "prod" {
		containerInsights = awsecs.ContainerInsights_ENHANCED
	}

	return awsecs.NewCluster(stack, jsii.String("Cluster"), &awsecs.ClusterProps{
		ClusterName:                    jsii.String(fmt.Sprintf("%s-cluster", cfg.Name)),
		Vpc:                            vpc,
		ContainerInsightsV2:            containerInsights,
		EnableFargateCapacityProviders: jsii.Bool(true),
	})
}

// buildIAMRoles creates execution and task roles
func buildIAMRoles(stack awscdk.Stack, cfg *config.DeployConfig) (awsiam.Role, awsiam.Role) {
	// Execution role (for pulling images and accessing secrets)
	executionRole := awsiam.NewRole(stack, jsii.String("TaskExecutionRole"), &awsiam.RoleProps{
		AssumedBy: awsiam.NewServicePrincipal(jsii.String("ecs-tasks.amazonaws.com"), nil),
		ManagedPolicies: &[]awsiam.IManagedPolicy{
			awsiam.ManagedPolicy_FromAwsManagedPolicyName(jsii.String("service-role/AmazonECSTaskExecutionRolePolicy")),
		},
	})

	// Allow reading SSM parameters
	executionRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("ssm:GetParameters", "ssm:GetParameter"),
		Resources: jsii.Strings(
			fmt.Sprintf("arn:aws:ssm:*:*:parameter/%s/*", cfg.Name),
		),
	}))

	// Task role (for application runtime permissions)
	taskRole := awsiam.NewRole(stack, jsii.String("TaskRole"), &awsiam.RoleProps{
		AssumedBy: awsiam.NewServicePrincipal(jsii.String("ecs-tasks.amazonaws.com"), nil),
	})

	// Grant scoped SQS access to declared queues.
	if cfg.Queues != nil {
		if len(cfg.Queues.Consume) > 0 {
			taskRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
				Actions: jsii.Strings(
					"sqs:ReceiveMessage",
					"sqs:DeleteMessage",
					"sqs:GetQueueAttributes",
					"sqs:ChangeMessageVisibility",
				),
				Resources: arnPointers(cfg.Queues.Consume),
			}))
		}
		if len(cfg.Queues.Produce) > 0 {
			taskRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
				Actions: jsii.Strings(
					"sqs:SendMessage",
					"sqs:GetQueueAttributes",
				),
				Resources: arnPointers(cfg.Queues.Produce),
			}))
		}
	}

	return executionRole, taskRole
}

// arnPointers converts a slice of ARN strings into the *[]*string form the
// CDK Resources field expects.
func arnPointers(arns []string) *[]*string {
	ptrs := make([]*string, 0, len(arns))
	for _, arn := range arns {
		ptrs = append(ptrs, jsii.String(arn))
	}
	return &ptrs
}

// buildLogGroup creates a CloudWatch log group
func buildLogGroup(stack awscdk.Stack, cfg *config.DeployConfig) awslogs.LogGroup {
	return awslogs.NewLogGroup(stack, jsii.String("LogGroup"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String(fmt.Sprintf("/ecs/%s", cfg.Name)),
		Retention:     awslogs.RetentionDays_ONE_WEEK,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})
}

// buildTaskDefinition creates a Fargate task definition
func buildTaskDefinition(stack awscdk.Stack, cfg *config.DeployConfig, envCfg *config.EnvConfig, executionRole, taskRole awsiam.Role) awsecs.FargateTaskDefinition {
	return awsecs.NewFargateTaskDefinition(stack, jsii.String("TaskDef"), &awsecs.FargateTaskDefinitionProps{
		Cpu:            jsii.Number(float64(cfg.Container.CPU)),
		MemoryLimitMiB: jsii.Number(float64(cfg.Container.Memory)),
		ExecutionRole:  executionRole,
		TaskRole:       taskRole,
		RuntimePlatform: &awsecs.RuntimePlatform{
			CpuArchitecture:       awsecs.CpuArchitecture_X86_64(),
			OperatingSystemFamily: awsecs.OperatingSystemFamily_LINUX(),
		},
	})
}

// buildContainer adds the container to the task definition with auto-generated secrets
func buildContainer(stack awscdk.Stack, cfg *config.DeployConfig, envCfg *config.EnvConfig, taskDef awsecs.FargateTaskDefinition, repo awsecr.IRepository, logGroup awslogs.LogGroup, env string) awsecs.ContainerDefinition {
	// AUTO-GENERATE SECRETS MAP from citadel.yml - THIS IS THE KEY FEATURE!
	secrets := make(map[string]awsecs.Secret)
	for _, secretName := range cfg.Secrets {
		param := awsssm.StringParameter_FromSecureStringParameterAttributes(
			stack,
			jsii.String(fmt.Sprintf("Param-%s", secretName)),
			&awsssm.SecureStringParameterAttributes{
				ParameterName: jsii.String(fmt.Sprintf("/%s/%s", cfg.Name, secretName)),
			},
		)
		secrets[secretName] = awsecs.Secret_FromSsmParameter(param)
	}

	// Build environment variables
	environment := make(map[string]*string)
	environment["PORT"] = jsii.String(fmt.Sprintf("%d", cfg.Container.Port))
	environment["SERVICE_NAME"] = jsii.String(cfg.Name)
	environment["ENVIRONMENT"] = jsii.String(env)

	// Add container
	container := taskDef.AddContainer(jsii.String("AppContainer"), &awsecs.ContainerDefinitionOptions{
		Image:       awsecs.ContainerImage_FromEcrRepository(repo, jsii.String("latest")),
		Essential:   jsii.Bool(true),
		Secrets:     &secrets,
		Environment: &environment,
		Logging: awsecs.LogDriver_AwsLogs(&awsecs.AwsLogDriverProps{
			LogGroup:     logGroup,
			StreamPrefix: jsii.String("ecs"),
		}),
		HealthCheck: &awsecs.HealthCheck{
			Command: jsii.Strings(
				"CMD-SHELL",
				fmt.Sprintf("wget -q --spider http://localhost:%d%s || exit 1",
					cfg.Container.Port,
					cfg.Container.HealthCheckPath,
				),
			),
			Interval:    awscdk.Duration_Seconds(jsii.Number(30)),
			Timeout:     awscdk.Duration_Seconds(jsii.Number(5)),
			Retries:     jsii.Number(3),
			StartPeriod: awscdk.Duration_Seconds(jsii.Number(float64(cfg.Container.HealthCheckGracePeriod))),
		},
	})

	container.AddPortMappings(&awsecs.PortMapping{
		ContainerPort: jsii.Number(float64(cfg.Container.Port)),
		Protocol:      awsecs.Protocol_TCP,
	})

	return container
}

// buildFargateService creates the Fargate service with ALB
func buildFargateService(stack awscdk.Stack, cfg *config.DeployConfig, envCfg *config.EnvConfig, cluster awsecs.Cluster, taskDef awsecs.FargateTaskDefinition, vpc awsec2.Vpc, env string) awsecspatterns.ApplicationLoadBalancedFargateService {
	// Capacity provider strategies
	var capacityProviderStrategies *[]*awsecs.CapacityProviderStrategy
	if envCfg.FargateSpot {
		capacityProviderStrategies = &[]*awsecs.CapacityProviderStrategy{
			{
				CapacityProvider: jsii.String("FARGATE_SPOT"),
				Weight:           jsii.Number(1),
			},
			{
				CapacityProvider: jsii.String("FARGATE"),
				Weight:           jsii.Number(0),
				Base:             jsii.Number(0),
			},
		}
	}

	// Subnet selection
	var subnetType awsec2.SubnetType
	var assignPublicIP bool
	if env == "dev" {
		subnetType = awsec2.SubnetType_PUBLIC
		assignPublicIP = true
	} else {
		subnetType = awsec2.SubnetType_PRIVATE_WITH_EGRESS
		assignPublicIP = false
	}

	return awsecspatterns.NewApplicationLoadBalancedFargateService(stack, jsii.String("Service"), &awsecspatterns.ApplicationLoadBalancedFargateServiceProps{
		Cluster:                    cluster,
		TaskDefinition:             taskDef,
		DesiredCount:               jsii.Number(float64(envCfg.MinCapacity)),
		ServiceName:                jsii.String(fmt.Sprintf("%s-service", cfg.Name)),
		AssignPublicIp:             jsii.Bool(assignPublicIP),
		PublicLoadBalancer:         jsii.Bool(true),
		CapacityProviderStrategies: capacityProviderStrategies,
		TaskSubnets: &awsec2.SubnetSelection{
			SubnetType: subnetType,
		},
		CircuitBreaker: &awsecs.DeploymentCircuitBreaker{
			Enable:   jsii.Bool(true),
			Rollback: jsii.Bool(true),
		},
		MinHealthyPercent: jsii.Number(100),
		MaxHealthyPercent: jsii.Number(200),
	})
}

// configureHealthCheck configures the ALB target group health check
func configureHealthCheck(service awsecspatterns.ApplicationLoadBalancedFargateService, cfg *config.DeployConfig) {
	service.TargetGroup().ConfigureHealthCheck(&awselasticloadbalancingv2.HealthCheck{
		Path:                    jsii.String(cfg.Container.HealthCheckPath),
		HealthyHttpCodes:        jsii.String("200"),
		Interval:                awscdk.Duration_Seconds(jsii.Number(30)),
		Timeout:                 awscdk.Duration_Seconds(jsii.Number(5)),
		HealthyThresholdCount:   jsii.Number(2),
		UnhealthyThresholdCount: jsii.Number(3),
	})
}

// buildCloudFront creates a CloudFront distribution
func buildCloudFront(stack awscdk.Stack, cfg *config.DeployConfig, service awsecspatterns.ApplicationLoadBalancedFargateService, env string) awscloudfront.Distribution {
	albOrigin := awscloudfrontorigins.NewHttpOrigin(service.LoadBalancer().LoadBalancerDnsName(), &awscloudfrontorigins.HttpOriginProps{
		ProtocolPolicy: awscloudfront.OriginProtocolPolicy_HTTP_ONLY,
	})

	comment := fmt.Sprintf("%s %s - HTTPS termination", cfg.Name, env)
	if cfg.CloudFront.Comment != "" {
		comment = cfg.CloudFront.Comment
	}

	return awscloudfront.NewDistribution(stack, jsii.String("CDN"), &awscloudfront.DistributionProps{
		DefaultBehavior: &awscloudfront.BehaviorOptions{
			Origin:               albOrigin,
			ViewerProtocolPolicy: awscloudfront.ViewerProtocolPolicy_REDIRECT_TO_HTTPS,
			AllowedMethods:       awscloudfront.AllowedMethods_ALLOW_ALL(),
			CachePolicy:          awscloudfront.CachePolicy_CACHING_DISABLED(),
			OriginRequestPolicy:  awscloudfront.OriginRequestPolicy_ALL_VIEWER(),
		},
		Comment: jsii.String(comment),
	})
}

// buildOutputs creates CloudFormation outputs
func buildOutputs(stack awscdk.Stack, cfg *config.DeployConfig, service awsecspatterns.ApplicationLoadBalancedFargateService, repo awsecr.IRepository, distribution awscloudfront.Distribution) {
	awscdk.NewCfnOutput(stack, jsii.String("LoadBalancerDNS"), &awscdk.CfnOutputProps{
		Value:       service.LoadBalancer().LoadBalancerDnsName(),
		Description: jsii.String("Application Load Balancer DNS"),
	})

	awscdk.NewCfnOutput(stack, jsii.String("EcrRepositoryUri"), &awscdk.CfnOutputProps{
		Value:       repo.RepositoryUri(),
		Description: jsii.String("ECR Repository URI"),
	})

	awscdk.NewCfnOutput(stack, jsii.String("ServiceName"), &awscdk.CfnOutputProps{
		Value:       service.Service().ServiceName(),
		Description: jsii.String("ECS Service Name"),
	})

	if distribution != nil {
		awscdk.NewCfnOutput(stack, jsii.String("CloudFrontURL"), &awscdk.CfnOutputProps{
			Value:       jsii.String(fmt.Sprintf("https://%s", *distribution.DistributionDomainName())),
			Description: jsii.String("CloudFront HTTPS URL"),
		})
	}
}
