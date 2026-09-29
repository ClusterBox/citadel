package deployable

import (
	"fmt"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsec2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsecs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awselasticache"
	"github.com/aws/jsii-runtime-go"
)

// ElastiCache Serverless serves the primary endpoint on cachePort and the
// reader endpoint on cacheReaderPort. AWS requires both open to clients: some
// dial both on every new connection even without read-from-replica.
const (
	cachePort       = 6379
	cacheReaderPort = 6380
)

// serverlessCache is what the cache: block adds to the stack.
type serverlessCache struct {
	securityGroup awsec2.SecurityGroup
	endpoint      *string // "host:port", resolved at deploy time
}

// buildCache creates the ElastiCache Serverless cache declared by cache:, in
// the same subnets as the service's tasks, behind a security group that
// admits nothing until allowFrom opens it to the service. Nil when absent.
func buildCache(stack awscdk.Stack, cfg *config.DeployConfig, vpc awsec2.Vpc, env string) *serverlessCache {
	if cfg.Cache == nil {
		return nil
	}
	name := cfg.CacheName(env)
	sg := awsec2.NewSecurityGroup(stack, jsii.String("CacheSecurityGroup"), &awsec2.SecurityGroupProps{
		Vpc:              vpc,
		Description:      jsii.String(fmt.Sprintf("%s cache: reachable only from the service's tasks", name)),
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
	c.securityGroup.Connections().AllowFrom(service,
		awsec2.Port_TcpRange(jsii.Number(cachePort), jsii.Number(cacheReaderPort)),
		jsii.String("Valkey (primary and reader endpoints) from the service's tasks"))
}
