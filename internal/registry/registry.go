// Package registry loads the list of services the citadel-logs daemon watches.
//
// Sources of truth:
//   - ~/.citadel/registry.yml (host) — list of {repo, env} entries.
//   - <repo>/citadel.yml — name, region, runtime, and lambda details per service.
//
// The registry intentionally does not call DeployConfig.Validate because the
// daemon only needs identity + ingestion metadata (name, region, runtime, log
// group target). It is OK for a Lambda repo to omit container fields.
package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ClusterBox/citadel/pkg/config"
	"gopkg.in/yaml.v3"
)

// Entry is a single line in registry.yml.
type Entry struct {
	Repo string `yaml:"repo"`
	Env  string `yaml:"env"`
}

// File is the on-disk shape of registry.yml.
type File struct {
	Services []Entry `yaml:"services"`
}

// Service is a fully-resolved view of a registered service after reading both
// registry.yml and the repo's citadel.yml. ID is "<name>-<env>".
type Service struct {
	ID       string
	Name     string
	Env      string
	Region   string
	Runtime  config.Runtime
	RepoPath string
	// LambdaFunction is populated only when Runtime == RuntimeLambda.
	LambdaFunction string
}

// LoadFile parses a registry.yml. Empty file is valid (zero services).
func LoadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry %s: %w", path, err)
	}
	var f File
	if len(data) == 0 {
		return &f, nil
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse registry %s: %w", path, err)
	}
	return &f, nil
}

// Resolve loads the registry file and each referenced citadel.yml, returning
// the list of services the daemon should watch. Errors on one entry do not
// abort the whole load — they are returned alongside the successful entries so
// the daemon can surface "degraded" services in the UI rather than failing to
// start.
func Resolve(registryPath string) ([]Service, []error) {
	f, err := LoadFile(registryPath)
	if err != nil {
		return nil, []error{err}
	}
	var services []Service
	var errs []error
	for _, e := range f.Services {
		svc, err := resolveEntry(e)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		services = append(services, svc)
	}
	return services, errs
}

func resolveEntry(e Entry) (Service, error) {
	if e.Repo == "" {
		return Service{}, fmt.Errorf("registry entry missing repo")
	}
	if e.Env == "" {
		return Service{}, fmt.Errorf("registry entry %s missing env", e.Repo)
	}
	cfgPath := filepath.Join(e.Repo, "citadel.yml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return Service{}, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	// Decode into a minimal view rather than the full DeployConfig. The daemon
	// only needs four fields, and being lenient here means an unrelated schema
	// drift elsewhere in citadel.yml (e.g. legacy queues shape) doesn't block
	// log ingestion for an otherwise-valid service.
	var cfg minimalCitadelYML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Service{}, fmt.Errorf("parse %s: %w", cfgPath, err)
	}
	if cfg.Name == "" {
		return Service{}, fmt.Errorf("%s: name is required", cfgPath)
	}
	if cfg.Region == "" {
		return Service{}, fmt.Errorf("%s: region is required", cfgPath)
	}
	rt := config.Runtime(cfg.Runtime)
	if rt == "" {
		rt = config.RuntimeECS
	}
	svc := Service{
		ID:       fmt.Sprintf("%s-%s", cfg.Name, e.Env),
		Name:     cfg.Name,
		Env:      e.Env,
		Region:   cfg.Region,
		Runtime:  rt,
		RepoPath: e.Repo,
	}
	if rt == config.RuntimeLambda {
		// Mirror config.ResolveFunctionName: an explicit functionName (with
		// "{env}" substitution) wins, otherwise the "<name>-<env>" convention
		// applies. Convention-based Lambda services (e.g. smaug, which sets no
		// functionName) must not be dropped from the registry.
		fnCfg := &config.DeployConfig{Name: cfg.Name}
		if cfg.Lambda != nil {
			fnCfg.Lambda = &config.LambdaConfig{FunctionName: cfg.Lambda.FunctionName}
		}
		svc.LambdaFunction = fnCfg.ResolveFunctionName(e.Env)
	}
	return svc, nil
}

// minimalCitadelYML is the slice of citadel.yml the daemon reads. Defined
// separately from config.DeployConfig so schema churn elsewhere (queues
// shape, container fields, etc.) does not break log ingestion.
type minimalCitadelYML struct {
	Name    string `yaml:"name"`
	Region  string `yaml:"region"`
	Runtime string `yaml:"runtime,omitempty"`
	Lambda  *struct {
		FunctionName string `yaml:"functionName"`
	} `yaml:"lambda,omitempty"`
}
