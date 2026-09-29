package deployable

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

// baseYAML is a minimal valid ECS citadel.yml; tests append blocks to it.
const baseYAML = `
name: demo
region: us-east-1
container: {port: 8080, cpu: 256, memory: 512, health_check_path: /health}
environments:
  dev: {account: "111111111111", min_capacity: 1, max_capacity: 2}
  prod: {account: "111111111111", min_capacity: 2, max_capacity: 6}
secrets: [DATABASE_URL]
`

// synth renders the stack for citadelYAML in env and returns its template.
func synth(t *testing.T, citadelYAML, env string) assertions.Template {
	t.Helper()
	path := filepath.Join(t.TempDir(), "citadel.yml")
	if err := os.WriteFile(path, []byte(citadelYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	app := awscdk.NewApp(nil)
	stack := NewDeployableService(app, "demo-"+env, &DeployableServiceProps{
		StackProps:  awscdk.StackProps{Env: &awscdk.Environment{Region: jsii.String("us-east-1")}},
		ConfigPath:  path,
		Environment: env,
	})
	return assertions.Template_FromStack(stack, nil)
}

// taskRolePolicy matches a task-role inline policy (its role's logical ID
// starts with "TaskRole", where runtime grants belong) holding an Allow
// statement with exactly these actions and this resource.
func taskRolePolicy(actions []any, resource string) map[string]any {
	return map[string]any{
		"PolicyDocument": map[string]any{
			"Statement": assertions.Match_ArrayWith(&[]any{
				assertions.Match_ObjectLike(&map[string]any{"Effect": "Allow", "Action": actions, "Resource": resource}),
			}),
		},
		"Roles": assertions.Match_ArrayWith(&[]any{
			map[string]any{"Ref": assertions.Match_StringLikeRegexp(jsii.String("^TaskRole"))},
		}),
	}
}

func TestSSMReadGrantsTaskRoleWithEnvExpanded(t *testing.T) {
	tpl := synth(t, baseYAML+`
iam:
  ssm_read:
    - /smaug/{env}/psp
`, "prod")
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), taskRolePolicy(
		[]any{"ssm:GetParameter", "ssm:GetParameters"},
		"arn:aws:ssm:*:*:parameter/smaug/prod/psp/*",
	))
}

func TestNoIAMBlockAddsNoSSMGrantToTaskRole(t *testing.T) {
	tpl := synth(t, baseYAML, "dev")
	// Without iam: or queues: the task role has no inline policy at all.
	tpl.ResourcePropertiesCountIs(jsii.String("AWS::IAM::Policy"), &map[string]any{
		"Roles": assertions.Match_ArrayWith(&[]any{
			map[string]any{"Ref": assertions.Match_StringLikeRegexp(jsii.String("^TaskRole"))},
		}),
	}, jsii.Number(0))
}

const cacheYAML = baseYAML + `
cache:
  engine: valkey
  serverless: true
`

func TestCacheCreatesServerlessValkeyInTaskSubnets(t *testing.T) {
	for env, subnet := range map[string]string{"dev": "^VpcPublicSubnet", "prod": "^VpcPrivateSubnet"} {
		tpl := synth(t, cacheYAML, env)
		tpl.ResourceCountIs(jsii.String("AWS::ElastiCache::ServerlessCache"), jsii.Number(1))
		tpl.HasResourceProperties(jsii.String("AWS::ElastiCache::ServerlessCache"), map[string]any{
			"Engine":              "valkey",
			"ServerlessCacheName": "demo-" + env,
			"SubnetIds": []any{
				map[string]any{"Ref": assertions.Match_StringLikeRegexp(jsii.String(subnet))},
				map[string]any{"Ref": assertions.Match_StringLikeRegexp(jsii.String(subnet))},
			},
			"SecurityGroupIds": []any{
				map[string]any{"Fn::GetAtt": []any{assertions.Match_StringLikeRegexp(jsii.String("^CacheSecurityGroup")), "GroupId"}},
			},
		})
	}
}

func TestCacheInjectsEndpointAsPlainEnvVar(t *testing.T) {
	tpl := synth(t, cacheYAML, "dev")
	tpl.HasResourceProperties(jsii.String("AWS::ECS::TaskDefinition"), map[string]any{
		"ContainerDefinitions": assertions.Match_ArrayWith(&[]any{
			assertions.Match_ObjectLike(&map[string]any{
				"Environment": assertions.Match_ArrayWith(&[]any{
					map[string]any{
						"Name": "VALKEY_ENDPOINT",
						"Value": map[string]any{"Fn::Join": []any{":", []any{
							map[string]any{"Fn::GetAtt": []any{"Cache", "Endpoint.Address"}},
							map[string]any{"Fn::GetAtt": []any{"Cache", "Endpoint.Port"}},
						}}},
					},
				}),
			}),
		}),
	})
}

func TestCacheAdmitsOnlyTheServiceOn6379(t *testing.T) {
	tpl := synth(t, cacheYAML, "dev")
	tpl.ResourcePropertiesCountIs(jsii.String("AWS::EC2::SecurityGroupIngress"), &map[string]any{
		"GroupId": map[string]any{"Fn::GetAtt": []any{assertions.Match_StringLikeRegexp(jsii.String("^CacheSecurityGroup")), "GroupId"}},
	}, jsii.Number(1))
	tpl.HasResourceProperties(jsii.String("AWS::EC2::SecurityGroupIngress"), map[string]any{
		"IpProtocol":            "tcp",
		"FromPort":              6379,
		"ToPort":                6379,
		"GroupId":               map[string]any{"Fn::GetAtt": []any{assertions.Match_StringLikeRegexp(jsii.String("^CacheSecurityGroup")), "GroupId"}},
		"SourceSecurityGroupId": map[string]any{"Fn::GetAtt": []any{assertions.Match_StringLikeRegexp(jsii.String("^ServiceSecurityGroup")), "GroupId"}},
	})
	tpl.HasOutput(jsii.String("CacheEndpoint"), map[string]any{})
}

func TestNoCacheBlockCreatesNoCache(t *testing.T) {
	tpl := synth(t, baseYAML, "dev")
	tpl.ResourceCountIs(jsii.String("AWS::ElastiCache::ServerlessCache"), jsii.Number(0))
	tpl.HasResourceProperties(jsii.String("AWS::ECS::TaskDefinition"), map[string]any{
		"ContainerDefinitions": assertions.Match_ArrayWith(&[]any{
			assertions.Match_ObjectLike(&map[string]any{
				"Environment": assertions.Match_Not(assertions.Match_ArrayWith(&[]any{
					assertions.Match_ObjectLike(&map[string]any{"Name": "VALKEY_ENDPOINT"}),
				})),
			}),
		}),
	})
}

// The subnet selection moved into taskSubnetType; the service itself must not
// change: dev tasks keep a public IP, prod tasks stay private.
func TestServiceNetworkingUnchanged(t *testing.T) {
	for env, ip := range map[string]string{"dev": "ENABLED", "prod": "DISABLED"} {
		tpl := synth(t, baseYAML, env)
		tpl.HasResourceProperties(jsii.String("AWS::ECS::Service"), map[string]any{
			"NetworkConfiguration": map[string]any{
				"AwsvpcConfiguration": assertions.Match_ObjectLike(&map[string]any{"AssignPublicIp": ip}),
			},
		})
	}
}

func TestQueueARNsAreScopedToTheEnvironment(t *testing.T) {
	tpl := synth(t, baseYAML+`
queues:
  produce:
    - arn:aws:sqs:us-east-1:111111111111:settle-{env}
`, "dev")
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), taskRolePolicy(
		[]any{"sqs:SendMessage", "sqs:GetQueueAttributes"},
		"arn:aws:sqs:us-east-1:111111111111:settle-dev",
	))
}
