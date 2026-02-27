package env

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Load reads a .env file and returns a map of key-value pairs
func Load(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open env file: %w", err)
	}
	defer file.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split on first '='
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid line %d: missing '=' separator", lineNum)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		value = strings.Trim(value, `"'`)

		if key == "" {
			return nil, fmt.Errorf("invalid line %d: empty key", lineNum)
		}

		env[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading env file: %w", err)
	}

	return env, nil
}

// Validate checks if all required secrets are present in the env map
func Validate(required []string, env map[string]string) []string {
	var missing []string

	for _, key := range required {
		if _, ok := env[key]; !ok {
			missing = append(missing, key)
		}
	}

	return missing
}
