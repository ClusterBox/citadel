package pipeline

import (
	"fmt"
	"path/filepath"

	"github.com/ClusterBox/citadel/internal/project"
)

// LastLocalDeploy summarises .citadel/state/<env>.json next to configPath for
// `citadel status`. It returns "" when this checkout has no .citadel/ or has
// never deployed env. It never creates anything.
func LastLocalDeploy(configPath, env string) string {
	dir, err := project.Existing(filepath.Dir(configPath))
	if err != nil {
		return ""
	}
	st, err := dir.ReadState(env)
	if err != nil {
		return ""
	}
	return FormatLastDeploy(st)
}

// FormatLastDeploy renders a State as the two-line `citadel status` summary.
func FormatLastDeploy(st *project.State) string {
	return fmt.Sprintf("🗂  Last local deploy: %s · %s · %s · by %s\n   Run: %s",
		st.Status, st.GitSHA, st.FinishedAt.UTC().Format("2006-01-02 15:04 UTC"), st.DeployedBy,
		filepath.Join(project.DirName, "runs", st.RunID))
}
