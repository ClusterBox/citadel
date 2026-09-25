package initcmd

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"github.com/ClusterBox/citadel/pkg/config"
)

//go:embed templates/*.yml.tmpl
var templateFS embed.FS

// templates holds one "<runtime>.yml.tmpl" per supported runtime. Adding a
// runtime (e.g. ec2) means adding a template file plus its init flag.
var templates = template.Must(template.ParseFS(templateFS, "templates/*.yml.tmpl"))

type envEntry struct {
	Name    string
	Account string
}

type templateData struct {
	Values
	Version string
	EnvList []envEntry
}

// Render produces citadel.yml content for v and checks it with config.Parse,
// so `citadel init` can never write a file that citadel itself would reject.
// Values are validated first, which also makes it safe to emit them unquoted.
func Render(v Values, version string) ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	data := templateData{Values: v, Version: version}
	for _, e := range v.Envs {
		data.EnvList = append(data.EnvList, envEntry{Name: e, Account: v.Account})
	}
	name := string(v.Runtime) + ".yml.tmpl"
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	if _, err := config.Parse(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("rendered citadel.yml is invalid (this is a citadel bug): %w", err)
	}
	return buf.Bytes(), nil
}
