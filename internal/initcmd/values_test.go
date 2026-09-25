package initcmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ClusterBox/citadel/pkg/config"
)

func validECS() Values {
	v := DefaultValues()
	v.Runtime = config.RuntimeECS
	v.Name = "my-api"
	v.Region = "us-east-1"
	v.Account = "111111111111"
	v.Secrets = []string{"DATABASE_URL"}
	return v
}

func validLambda() Values {
	v := DefaultValues()
	v.Runtime = config.RuntimeLambda
	v.Name = "smaug"
	v.Region = "eu-west-1"
	v.Account = "111111111111"
	return v
}

func TestDefaultValues(t *testing.T) {
	v := DefaultValues()
	if !reflect.DeepEqual(v.Envs, []string{"dev", "prod"}) || v.Port != 3000 || v.CPU != 256 || v.Memory != 512 || v.HealthCheckPath != "/health" {
		t.Fatalf("DefaultValues = %+v", v)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"legolas":      "legolas",
		"My App.v2":    "my-app-v2",
		"--Weird__Dir": "weird-dir",
		"__tmp":        "tmp",
		"___":          "app",
		"":             "app",
		"api2":         "api2",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
		if err := ValidateName(SanitizeName(in)); err != nil {
			t.Errorf("SanitizeName(%q) produced an invalid name: %v", in, err)
		}
	}
}

func TestSplitList(t *testing.T) {
	got := SplitList(" dev, prod,,dev ,staging ")
	if !reflect.DeepEqual(got, []string{"dev", "prod", "staging"}) {
		t.Fatalf("SplitList = %v", got)
	}
	if got := SplitList(""); len(got) != 0 {
		t.Fatalf("SplitList(\"\") = %v, want empty", got)
	}
}

func TestFieldValidators(t *testing.T) {
	type check struct {
		fn   func(string) error
		ok   []string
		bad  []string
		name string
	}
	checks := []check{
		{ValidateName, []string{"a", "my-api", "api2"}, []string{"", "My App", "-x", "x-", "a_b"}, "name"},
		{ValidateRegion, []string{"us-east-1", "eu-central-2", "ap-southeast-1"}, []string{"", "useast1", "US-EAST-1"}, "region"},
		{ValidateAccount, []string{"111111111111"}, []string{"", "11111111111", "1111111111112", "abcdefghijkl"}, "account"},
		{ValidateEnvName, []string{"dev", "prod", "staging-2"}, []string{"", "Dev", "a/b", "-x"}, "env"},
		{ValidateSecretName, []string{"DATABASE_URL", "_X", "a1"}, []string{"", "1ABC", "MY-KEY", "A B"}, "secret"},
		{ValidateHealthCheckPath, []string{"/", "/health"}, []string{"", "health"}, "health"},
		{ValidateFunctionName, []string{"smaug-{env}", "fn_1"}, []string{"", "a b", "x:y"}, "function"},
	}
	for _, c := range checks {
		for _, s := range c.ok {
			if err := c.fn(s); err != nil {
				t.Errorf("%s: %q rejected: %v", c.name, s, err)
			}
		}
		for _, s := range c.bad {
			if err := c.fn(s); err == nil {
				t.Errorf("%s: %q accepted", c.name, s)
			}
		}
	}
}

func TestValidateName_ErrorShowsExample(t *testing.T) {
	err := ValidateName("My App")
	if err == nil || !strings.Contains(err.Error(), "my-api") {
		t.Fatalf("error = %v, want one that shows an example", err)
	}
}

func TestValuesValidate(t *testing.T) {
	if err := validECS().Validate(); err != nil {
		t.Fatalf("valid ECS: %v", err)
	}
	if err := validLambda().Validate(); err != nil {
		t.Fatalf("valid Lambda without secrets: %v", err)
	}

	bad := map[string]func(*Values){
		"runtime ec2":      func(v *Values) { v.Runtime = "ec2" },
		"no envs":          func(v *Values) { v.Envs = nil },
		"duplicate env":    func(v *Values) { v.Envs = []string{"dev", "dev"} },
		"duplicate secret": func(v *Values) { v.Secrets = []string{"A", "A"} },
		"port zero":        func(v *Values) { v.Port = 0 },
		"port too high":    func(v *Values) { v.Port = 70000 },
		"cpu zero":         func(v *Values) { v.CPU = 0 },
		"ecs no secrets":   func(v *Values) { v.Secrets = nil },
		"bad health path":  func(v *Values) { v.HealthCheckPath = "health" },
	}
	for name, mutate := range bad {
		v := validECS()
		mutate(&v)
		if err := v.Validate(); err == nil {
			t.Errorf("%s: Validate succeeded", name)
		}
	}

	v := validECS()
	v.Secrets = nil
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "--secrets") {
		t.Fatalf("ECS without secrets error = %v, want a hint naming --secrets", err)
	}

	l := validLambda()
	l.FunctionName = "a b"
	if err := l.Validate(); err == nil {
		t.Fatal("invalid function name accepted")
	}
}
