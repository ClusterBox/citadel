package config

import (
	"strings"
	"testing"
)

func TestVPCIDValidation(t *testing.T) {
	base := `
name: demo
region: us-east-1
container: {port: 8080, cpu: 256, memory: 512}
secrets: [DATABASE_URL]
environments:
  prod: %s
`
	cases := map[string]string{
		`{account: "111111111111", min_capacity: 1, max_capacity: 2, vpc_id: vpc-01f19f79f65a87711}`: "",
		`{account: "111111111111", min_capacity: 1, max_capacity: 2}`:                                "",
		`{account: "111111111111", min_capacity: 1, max_capacity: 2, vpc_id: legolas-prod}`:          "is not a VPC ID",
		`{min_capacity: 1, max_capacity: 2, vpc_id: vpc-01f19f79f65a87711}`:                          "needs environments.prod.account",
	}
	for env, want := range cases {
		_, err := Parse([]byte(strings.Replace(base, "%s", env, 1)))
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", env, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: error %v, want it to contain %q", env, err, want)
		}
	}
}
