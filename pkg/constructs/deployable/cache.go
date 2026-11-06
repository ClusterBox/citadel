package deployable

import (
	"fmt"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsec2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsecs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awselasticache"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/jsii-runtime-go"
)

// ElastiCache Serverless serves the primary endpoint on cachePort and the
// reader endpoint on cacheReaderPort. AWS requires both open to clients: some
// dial both on every new connection even without read-from-replica.
const (
	cachePort       = 6379
	cacheReaderPort = 6380
)

// valkeySidecarImage is the cache_mode: sidecar container's image, pinned to
// the Valkey major the serverless caches run (9.0) so dev and prod behave
// alike.
const valkeySidecarImage = "valkey/valkey:9.0.7-alpine"

// serverlessCache is what the cache: block adds to the stack.
type serverlessCache struct {
	securityGroup awsec2.SecurityGroup // nil for a sidecar
	endpoint      *string              // "host:port", resolved at deploy time
	sidecar       bool                 // a Valkey container in each task, no TLS
}

// buildCache creates the ElastiCache Serverless cache declared by cache:, in
// the same subnets as the service's tasks, behind a security group that
// admits nothing until allowFrom opens it to the service. Nil when absent.
func buildCache(stack awscdk.Stack, cfg *config.DeployConfig, envCfg *config.EnvConfig, vpc awsec2.IVpc, env string) *serverlessCache {
	if cfg.Cache == nil {
		return nil
	}
	if envCfg.ResolvedCacheMode() == config.CacheModeSidecar {
		// addCacheSidecar adds the container; nothing else is created.
		return &serverlessCache{endpoint: jsii.String(fmt.Sprintf("localhost:%d", cachePort)), sidecar: true}
	}
	name := cfg.CacheName(env)
	sg := awsec2.NewSecurityGroup(stack, jsii.String("CacheSecurityGroup"), &awsec2.SecurityGroupProps{
		Vpc:              vpc,
		Description:      jsii.String(fmt.Sprintf("%s cache: reachable only from the service tasks", name)),
		AllowAllOutbound: jsii.Bool(false),
	})
	subnets := vpc.SelectSubnets(&awsec2.SubnetSelection{SubnetType: taskSubnetType(env)}).SubnetIds
	subnetIDs := make([]interface{}, 0, len(*subnets))
	for _, id := range *subnets {
		subnetIDs = append(subnetIDs, id)
	}
	cache := awselasticache.NewCfnServerlessCache(stack, jsii.String("Cache"), &awselasticache.CfnServerlessCacheProps{
		Engine:              jsii.String(cfg.Cache.Engine),
		ServerlessCacheName: jsii.String(name),
		Description:         jsii.String(fmt.Sprintf("%s - created by citadel", name)),
		SubnetIds:           &subnetIDs,
		SecurityGroupIds:    &[]interface{}{sg.SecurityGroupId()},
	})
	return &serverlessCache{
		securityGroup: sg,
		endpoint:      awscdk.Fn_Join(jsii.String(":"), &[]*string{cache.AttrEndpointAddress(), cache.AttrEndpointPort()}),
	}
}

// allowFrom opens the cache's ports to the service's tasks, and only to them.
func (c *serverlessCache) allowFrom(service awsecs.FargateService) {
	if c.securityGroup == nil {
		return // a sidecar is reached over localhost
	}
	c.securityGroup.Connections().AllowFrom(service,
		awsec2.Port_TcpRange(jsii.Number(cachePort), jsii.Number(cacheReaderPort)),
		jsii.String("Valkey (primary and reader endpoints) from the service tasks"))
}

// addCacheSidecar adds the cache_mode: sidecar Valkey container. It shares the
// task's network namespace and listens only on localhost, keeps nothing on
// disk, and is essential (a task without its cache is replaced). The app
// container waits for it to answer PING before starting.
func addCacheSidecar(taskDef awsecs.FargateTaskDefinition, logGroup awslogs.LogGroup, app awsecs.ContainerDefinition) {
	valkey := taskDef.AddContainer(jsii.String("valkey"), &awsecs.ContainerDefinitionOptions{
		Image:     awsecs.ContainerImage_FromRegistry(jsii.String(valkeySidecarImage), nil),
		Essential: jsii.Bool(true),
		Command: jsii.Strings("valkey-server",
			"--bind", "127.0.0.1",
			"--port", fmt.Sprintf("%d", cachePort),
			"--save", "",
			"--appendonly", "no",
			"--maxmemory", "64mb",
			"--maxmemory-policy", "volatile-lru"),
		HealthCheck: &awsecs.HealthCheck{
			Command:  jsii.Strings("CMD", "valkey-cli", "-p", fmt.Sprintf("%d", cachePort), "ping"),
			Interval: awscdk.Duration_Seconds(jsii.Number(10)),
			Timeout:  awscdk.Duration_Seconds(jsii.Number(5)),
			Retries:  jsii.Number(3),
		},
		Logging: awsecs.LogDriver_AwsLogs(&awsecs.AwsLogDriverProps{
			LogGroup:     logGroup,
			StreamPrefix: jsii.String("valkey"),
		}),
	})
	app.AddContainerDependencies(&awsecs.ContainerDependency{
		Container: valkey,
		Condition: awsecs.ContainerDependencyCondition_HEALTHY,
	})
}
