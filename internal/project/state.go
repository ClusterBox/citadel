package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// State is the last deploy of one environment from this checkout, stored in
// .citadel/state/<env>.json (git-ignored).
type State struct {
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	GitSHA     string    `json:"git_sha"`
	ImageURI   string    `json:"image_uri"`
	Target     string    `json:"target"`
	DeployedBy string    `json:"deployed_by"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// envNameRe guards the env name used as a file name against path traversal.
var envNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (d *Dir) statePath(env string) (string, error) {
	if !envNameRe.MatchString(env) {
		return "", fmt.Errorf("invalid environment name %q", env)
	}
	return filepath.Join(d.path, stateDirName, env+".json"), nil
}

// WriteState atomically writes state/<env>.json.
func (d *Dir) WriteState(env string, s State) error {
	path, err := d.statePath(env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	return WriteFileAtomic(path, append(data, '\n'))
}

// ReadState reads state/<env>.json. The error wraps os.ErrNotExist when env
// has never been deployed from this checkout.
func (d *Dir) ReadState(env string) (*State, error) {
	path, err := d.statePath(env)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}
