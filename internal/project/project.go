// Package project owns the per-project .citadel/ directory that lives next to
// citadel.yml: committed metadata (project.yml), the last deploy per env
// (state/), and per-deploy step logs (runs/). Callers never build paths under
// .citadel/ themselves.
//
// It is distinct from the global ~/.citadel/ (deployments.db, registry.yml),
// which holds cross-project data.
package project

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DirName is the project directory created next to citadel.yml.
const DirName = ".citadel"

const (
	projectFile      = "project.yml"
	stateDirName     = "state"
	runsDirName      = "runs"
	gitignoreFile    = ".gitignore"
	gitignoreContent = "state/\nruns/\n"
)

// Dir is an opened .citadel/ directory.
type Dir struct {
	path string
}

// Path returns the absolute or relative path of the .citadel/ directory.
func (d *Dir) Path() string { return d.path }

// Open returns the .citadel/ directory inside configDir, creating it and its
// .gitignore when missing. It is idempotent and never rewrites an existing
// .gitignore, so users may add their own lines.
func Open(configDir string) (*Dir, error) {
	p := filepath.Join(configDir, DirName)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", p, err)
	}
	gi := filepath.Join(p, gitignoreFile)
	if _, err := os.Stat(gi); errors.Is(err, os.ErrNotExist) {
		if err := WriteFileAtomic(gi, []byte(gitignoreContent)); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat %s: %w", gi, err)
	}
	return &Dir{path: p}, nil
}

// Existing returns the .citadel/ directory inside configDir without creating
// anything. The error wraps os.ErrNotExist when there is none.
func Existing(configDir string) (*Dir, error) {
	p := filepath.Join(configDir, DirName)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", p)
	}
	return &Dir{path: p}, nil
}

// Meta is the committed project.yml. Fields may be added over time but never
// removed; unknown fields are ignored on read.
type Meta struct {
	ID          string    `yaml:"id"`
	Name        string    `yaml:"name"`
	Runtime     string    `yaml:"runtime"`
	CreatedAt   time.Time `yaml:"created_at"`
	CreatedWith string    `yaml:"created_with"`
}

// NewMeta returns metadata for a new project with a fresh random id. The id is
// the project's stable identity, independent of name changes.
func NewMeta(name, runtime, version string, now time.Time) Meta {
	return Meta{
		ID:          randomHex(8),
		Name:        name,
		Runtime:     runtime,
		CreatedAt:   now.UTC().Truncate(time.Second),
		CreatedWith: "citadel " + version,
	}
}

// Project reads project.yml. The error wraps os.ErrNotExist when it is missing.
func (d *Dir) Project() (*Meta, error) {
	data, err := os.ReadFile(filepath.Join(d.path, projectFile))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", projectFile, err)
	}
	return &m, nil
}

// WriteProject atomically writes project.yml.
func (d *Dir) WriteProject(m Meta) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode %s: %w", projectFile, err)
	}
	return WriteFileAtomic(filepath.Join(d.path, projectFile), data)
}

// EnsureProject returns the existing project.yml, or writes m when there is
// none. created reports whether m was written.
func (d *Dir) EnsureProject(m Meta) (*Meta, bool, error) {
	existing, err := d.Project()
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if err := d.WriteProject(m); err != nil {
		return nil, false, err
	}
	return &m, true, nil
}

// randomHex returns 2n lowercase hex chars. crypto/rand.Read never returns an
// error on supported platforms (Go 1.24+), so the error is ignored.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
