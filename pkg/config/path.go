package config

import (
	"os"
	"path/filepath"
)

// DefaultFileName is the config file citadel reads by default and the file
// `citadel init` writes.
const DefaultFileName = "citadel.yml"

// altFileName is accepted in place of DefaultFileName when only it exists.
const altFileName = "citadel.yaml"

// ResolveDefaultPath returns the config path to use in dir when --config was
// not passed: citadel.yml if it exists, else citadel.yaml if it exists, else
// citadel.yml (so "not found" errors and `citadel init` name the canonical file).
func ResolveDefaultPath(dir string) string {
	primary := filepath.Join(dir, DefaultFileName)
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	alt := filepath.Join(dir, altFileName)
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	return primary
}
