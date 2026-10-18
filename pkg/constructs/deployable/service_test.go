package deployable

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// ElastiCache Serverless serves the primary endpoint on 6379 and the reader
// endpoint on 6380; AWS requires both open because some clients dial both.
func TestCacheAdmitsOnlyTheServiceOnItsPorts(t *testing.T) {
	tpl := synth(t, cacheYAML, "dev")
	tpl.ResourcePropertiesCountIs(jsii.String("AWS::EC2::SecurityGroupIngress"), &map[string]any{
		"GroupId": map[string]any{"Fn::GetAtt": []any{assertions.Match_StringLikeRegexp(jsii.String("^CacheSecurityGroup")), "GroupId"}},
	}, jsii.Number(1))
	tpl.HasResourceProperties(jsii.String("AWS::EC2::SecurityGroupIngress"), map[string]any{
		"IpProtocol":            "tcp",
		"FromPort":              6379,
		"ToPort":                6380,
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

// EC2 rejects a security group or rule description outside this set, and
// CloudFormation only finds out at deploy time, failing (and rolling back)
// the whole stack. An apostrophe ("the service's tasks") did exactly that.
var ec2Description = regexp.MustCompile(`^[a-zA-Z0-9. _\-:/()#,@\[\]+=&;{}!$*]{0,255}$`)

func TestSecurityGroupDescriptionsAreValidForEC2(t *testing.T) {
	for _, env := range []string{"dev", "prod"} {
		tpl := synth(t, cacheYAML+"\ncloudfront: {enabled: true}\n", env)
		resources := (*tpl.ToJSON())["Resources"].(map[string]any)
		checked := 0
		check := func(where string, v any) {
			s, ok := v.(string)
			if !ok {
				return // an intrinsic (Fn::Join etc.); resolved by CloudFormation
			}
			checked++
			if !ec2Description.MatchString(s) {
				t.Errorf("%s %s: description %q has characters EC2 rejects", env, where, s)
			}
		}
		for id, r := range resources {
			res := r.(map[string]any)
			props, _ := res["Properties"].(map[string]any)
			switch res["Type"] {
			case "AWS::EC2::SecurityGroup":
				check(id+".GroupDescription", props["GroupDescription"])
				for _, key := range []string{"SecurityGroupIngress", "SecurityGroupEgress"} {
					rules, _ := props[key].([]any)
					for i, rule := range rules {
						check(fmt.Sprintf("%s.%s[%d]", id, key, i), rule.(map[string]any)["Description"])
					}
				}
			case "AWS::EC2::SecurityGroupIngress", "AWS::EC2::SecurityGroupEgress":
				check(id+".Description", props["Description"])
			}
		}
		if checked == 0 {
			t.Fatalf("%s: no descriptions found; the walk is broken", env)
		}
	}
}

// tunnelYAML runs dev behind a Cloudflare tunnel and prod behind the ALB,
// with CloudFront on, so each test can see both ingress paths.
const tunnelYAML = `
name: demo
region: us-east-1
container: {port: 8080, cpu: 256, memory: 512, health_check_path: /health}
environments:
  dev: {account: "111111111111", min_capacity: 1, max_capacity: 2, ingress: cloudflare_tunnel}
  prod: {account: "111111111111", min_capacity: 2, max_capacity: 6}
secrets: [DATABASE_URL]
cloudfront: {enabled: true}
`

func TestTunnelIngressDropsLoadBalancerAndCloudFront(t *testing.T) {
	for env, want := range map[string]float64{"dev": 0, "prod": 1} {
		tpl := synth(t, tunnelYAML, env)
		tpl.ResourceCountIs(jsii.String("AWS::ElasticLoadBalancingV2::LoadBalancer"), jsii.Number(want))
		tpl.ResourceCountIs(jsii.String("AWS::ElasticLoadBalancingV2::TargetGroup"), jsii.Number(want))
		tpl.ResourceCountIs(jsii.String("AWS::CloudFront::Distribution"), jsii.Number(want))
	}
}

func TestTunnelIngressAddsCloudflaredSidecar(t *testing.T) {
	tpl := synth(t, tunnelYAML, "dev")
	tpl.HasResourceProperties(jsii.String("AWS::ECS::TaskDefinition"), map[string]any{
		"ContainerDefinitions": assertions.Match_ArrayWith(&[]any{
			assertions.Match_ObjectLike(&map[string]any{
				"Name":      "cloudflared",
				"Image":     "cloudflare/cloudflared:2026.9.3",
				"Essential": true,
				"Command":   []any{"tunnel", "--no-autoupdate", "run"},
				"Secrets": []any{
					assertions.Match_ObjectLike(&map[string]any{
						"Name": "TUNNEL_TOKEN",
						"ValueFrom": map[string]any{"Fn::Join": []any{"", assertions.Match_ArrayWith(&[]any{
							":parameter/demo-dev/CLOUDFLARE_TUNNEL_TOKEN",
						})}},
					}),
				},
			}),
		}),
	})
	// The app container never sees the token.
	tpl.HasResourceProperties(jsii.String("AWS::ECS::TaskDefinition"), map[string]any{
		"ContainerDefinitions": assertions.Match_ArrayWith(&[]any{
			assertions.Match_ObjectLike(&map[string]any{
				"Name": "AppContainer",
				"Secrets": assertions.Match_Not(assertions.Match_ArrayWith(&[]any{
					assertions.Match_ObjectLike(&map[string]any{"Name": "TUNNEL_TOKEN"}),
				})),
			}),
		}),
	})
}

// Switching an environment to the tunnel must update its ECS service and
// security group in place: both carry fixed names, so a new logical ID would
// make CloudFormation create the replacement first and fail on the name.
func TestTunnelIngressKeepsServiceLogicalIDs(t *testing.T) {
	ids := func(yaml string) map[string][]string {
		resources := (*synth(t, yaml, "dev").ToJSON())["Resources"].(map[string]any)
		out := map[string][]string{}
		for id, r := range resources {
			typ := r.(map[string]any)["Type"].(string)
			if typ == "AWS::ECS::Service" || (typ == "AWS::EC2::SecurityGroup" && regexp.MustCompile(`^ServiceSecurityGroup`).MatchString(id)) {
				out[typ] = append(out[typ], id)
			}
		}
		return out
	}
	alb, tunnel := ids(baseYAML), ids(tunnelYAML)
	for _, typ := range []string{"AWS::ECS::Service", "AWS::EC2::SecurityGroup"} {
		if len(alb[typ]) != 1 || fmt.Sprint(alb[typ]) != fmt.Sprint(tunnel[typ]) {
			t.Errorf("%s logical IDs: alb %v, tunnel %v; want the same single ID", typ, alb[typ], tunnel[typ])
		}
	}
}

func TestTunnelServiceKeepsPublicIPForEgress(t *testing.T) {
	tpl := synth(t, tunnelYAML, "dev")
	tpl.HasResourceProperties(jsii.String("AWS::ECS::Service"), map[string]any{
		"ServiceName":   "demo-dev-service",
		"LoadBalancers": assertions.Match_Absent(),
		"NetworkConfiguration": map[string]any{
			"AwsvpcConfiguration": assertions.Match_ObjectLike(&map[string]any{"AssignPublicIp": "ENABLED"}),
		},
	})
}

// Enhanced observability bills per container metric; prod uses standard.
func TestProdContainerInsightsIsStandard(t *testing.T) {
	tpl := synth(t, baseYAML, "prod")
	tpl.HasResourceProperties(jsii.String("AWS::ECS::Cluster"), map[string]any{
		"ClusterSettings": []any{map[string]any{"Name": "containerInsights", "Value": "enabled"}},
	})
}
