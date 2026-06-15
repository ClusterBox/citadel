package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	// Create a temporary .env file
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")

	content := `# Comment
DATABASE_HOST=localhost
DATABASE_PORT=5432
DATABASE_USER="postgres"
DATABASE_PASSWORD='secret123'

# Another comment
API_KEY=abc123xyz
`
	if err := os.WriteFile(envFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test env file: %v", err)
	}

	// Test loading
	env, err := Load(envFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify values
	tests := []struct {
		key   string
		want  string
	}{
		{"DATABASE_HOST", "localhost"},
		{"DATABASE_PORT", "5432"},
		{"DATABASE_USER", "postgres"},
		{"DATABASE_PASSWORD", "secret123"},
		{"API_KEY", "abc123xyz"},
	}

	for _, tt := range tests {
		got, ok := env[tt.key]
		if !ok {
			t.Errorf("Missing key: %s", tt.key)
			continue
		}
		if got != tt.want {
			t.Errorf("Key %s: got %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestValidate(t *testing.T) {
	env := map[string]string{
		"KEY1": "value1",
		"KEY2": "value2",
	}

	required := []string{"KEY1", "KEY2", "KEY3", "KEY4"}

	missing := Validate(required, env)

	if len(missing) != 2 {
		t.Errorf("Expected 2 missing keys, got %d", len(missing))
	}

	expectedMissing := map[string]bool{"KEY3": true, "KEY4": true}
	for _, key := range missing {
		if !expectedMissing[key] {
			t.Errorf("Unexpected missing key: %s", key)
		}
	}
}
