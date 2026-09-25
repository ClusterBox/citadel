package project

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path via a temp file in the same directory
// and a rename, so readers never observe a partially written file and an
// interrupted write leaves the previous content intact.
func WriteFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
