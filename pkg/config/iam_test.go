package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidate_IAMSSMRead(t *testing.T) {
	cases := map[string]struct {
		prefixes []string
		wantErr  string // substring; "" means valid
	}{
		"absent list":           {nil, ""},
		"env placeholder":       {[]string{"/smaug/{env}/psp"}, ""},
		"trailing slash":        {[]string{"/smaug/{env}/psp/"}, ""},
		"several":               {[]string{"/a/b", "/c"}, ""},
		"no leading slash":      {[]string{"smaug/psp"}, "must start with '/'"},
		"root only":             {[]string{"/"}, "every parameter in the account"},
		"wildcard":              {[]string{"/smaug/*"}, "must not contain wildcards"},
		"question mark":         {[]string{"/smaug/?"}, "must not contain wildcards"},
		"empty segment":         {[]string{"/smaug//psp"}, "empty path segment"},
		"second entry is named": {[]string{"/ok", "bad"}, "iam.ssm_read[1]"},
		"typo placeholder":      {[]string{"/smaug/{stage}/psp"}, "placeholder other than {env}"},
		"stray brace":           {[]string{"/smaug/{env/psp"}, "placeholder other than {env}"},
	}
	for name, c := range cases {
		cfg := baseValidConfig()
		cfg.IAM = &IAMConfig{SSMRead: c.prefixes}
		err := cfg.Validate()
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.wantErr)
		}
	}
}

func TestValidate_IAMSSMReadRejectedForLambdaRuntime(t *testing.T) {
	cfg := &DeployConfig{
		Name: "smaug", Region: "us-east-1", Runtime: RuntimeLambda,
		Environments: map[string]EnvConfig{"dev": {Account: "111111111111"}},
		IAM:          &IAMConfig{SSMRead: []string{"/smaug/{env}/psp"}},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "only supported for the \"ecs\" runtime") {
		t.Fatalf("err = %v, want an ecs-only error", err)
	}
}

func TestSSMReadResources(t *testing.T) {
	cfg := baseValidConfig()
	if got := cfg.SSMReadResources("dev"); got != nil {
		t.Errorf("no iam block: got %v, want nil", got)
	}
	cfg.IAM = &IAMConfig{SSMRead: []string{"/smaug/{env}/psp/", "/shared/config"}}
	want := []string{
		"arn:aws:ssm:*:*:parameter/smaug/prod/psp/*",
		"arn:aws:ssm:*:*:parameter/shared/config/*",
	}
	if got := cfg.SSMReadResources("prod"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParse_IAMBlock(t *testing.T) {
	cfg, err := Parse([]byte(`
name: demo
region: us-east-1
container: {port: 8080, cpu: 256, memory: 512}
environments: {dev: {account: "111111111111"}}
secrets: [DATABASE_URL]
iam:
  ssm_read:
    - /smaug/{env}/psp
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IAM == nil || !reflect.DeepEqual(cfg.IAM.SSMRead, []string{"/smaug/{env}/psp"}) {
		t.Fatalf("iam = %+v", cfg.IAM)
	}
}

// {env} makes a YAML flow sequence ambiguous; quoted it parses, which is what
// the README tells people to write.
func TestParse_IAMFlowStyleNeedsQuotes(t *testing.T) {
	const head = `
name: demo
region: us-east-1
container: {port: 8080, cpu: 256, memory: 512}
environments: {dev: {account: "111111111111"}}
secrets: [DATABASE_URL]
`
	cfg, err := Parse([]byte(head + `iam: {ssm_read: ["/smaug/{env}/psp"]}` + "\n"))
	if err != nil || cfg.IAM.SSMRead[0] != "/smaug/{env}/psp" {
		t.Fatalf("quoted flow style: cfg = %+v, err = %v", cfg, err)
	}
	if _, err := Parse([]byte(head + `iam: {ssm_read: [/smaug/{env}/psp]}` + "\n")); err == nil {
		t.Fatal("unquoted {env} in a flow sequence parsed; the README's quoting advice is then wrong")
	}
}
