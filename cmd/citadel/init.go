package main

import (
	"os"
	"strings"

	"github.com/ClusterBox/citadel/internal/initcmd"
	"github.com/spf13/cobra"
)

func newInitCmd(configPath *string, dryRun *bool) *cobra.Command {
	var ecs, lambda, yes, force bool
	v := initcmd.DefaultValues()
	envs := strings.Join(v.Envs, ",")
	var secrets string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create citadel.yml and the project's .citadel/ directory",
		Long: `Create a commented citadel.yml for the chosen runtime and a .citadel/
directory next to it (project.yml is committed; state/ and runs/ are ignored).

Values come from flags, then detection (directory name, AWS_REGION, STS),
then prompts. --yes (or a non-interactive stdin) accepts the defaults.
If citadel.yml already exists it is left alone and only .citadel/ is created;
pass --force to regenerate it.`,
		Example: `  citadel init --ecs
  citadel init --lambda --yes --account 123456789012
  citadel init --ecs --yes --secrets DATABASE_URL,JWT_SECRET --port 8080`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := initcmd.RuntimeFromFlags(ecs, lambda)
			if err != nil {
				return err
			}
			v.Runtime = rt
			v.Envs = initcmd.SplitList(envs)
			v.Secrets = initcmd.SplitList(secrets)

			provided := map[string]bool{}
			for _, name := range initcmd.ValueFlags {
				provided[name] = cmd.Flags().Changed(name)
			}

			return initcmd.Run(cmd.Context(), initcmd.Options{
				ConfigPath: *configPath,
				Values:     v,
				Provided:   provided,
				Yes:        yes || !stdinIsTerminal(),
				Force:      force,
				DryRun:     *dryRun,
				Version:    version,
				Out:        cmd.OutOrStdout(),
			}, initcmd.NewHuhPrompter(), initcmd.NewEnvDetector())
		},
	}

	f := cmd.Flags()
	f.BoolVar(&ecs, initcmd.FlagECS, false, "Generate an ECS Fargate config")
	f.BoolVar(&lambda, initcmd.FlagLambda, false, "Generate a Lambda (container image) config")
	f.StringVar(&v.Name, initcmd.FlagName, "", "Project name (default: directory name)")
	f.StringVar(&v.Region, initcmd.FlagRegion, "", "AWS region (default: $AWS_REGION, $AWS_DEFAULT_REGION, us-east-1)")
	f.StringVar(&v.Account, initcmd.FlagAccount, "", "AWS account ID (default: detected via STS)")
	f.StringVar(&envs, initcmd.FlagEnvs, envs, "Comma-separated environments")
	f.IntVar(&v.Port, initcmd.FlagPort, v.Port, "Container port (ECS)")
	f.IntVar(&v.CPU, initcmd.FlagCPU, v.CPU, "Task CPU units, 256 = 0.25 vCPU (ECS)")
	f.IntVar(&v.Memory, initcmd.FlagMemory, v.Memory, "Task memory in MiB (ECS)")
	f.StringVar(&v.HealthCheckPath, initcmd.FlagHealthCheckPath, v.HealthCheckPath, "Health check path (ECS)")
	f.StringVar(&v.FunctionName, initcmd.FlagFunctionName, "", "Lambda function name, {env} substituted (default: <name>-<env>)")
	f.StringVar(&secrets, initcmd.FlagSecrets, "", "Comma-separated secret env var names (required for ECS)")
	f.BoolVarP(&yes, "yes", "y", false, "Accept defaults and never prompt")
	f.BoolVar(&force, "force", false, "Overwrite an existing citadel.yml")
	cmd.MarkFlagsMutuallyExclusive(initcmd.FlagECS, initcmd.FlagLambda)
	return cmd
}

// stdinIsTerminal reports whether prompts can be shown.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
