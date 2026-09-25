# citadel deploy (GitHub Action)

A composite GitHub Action that runs [citadel](../README.md) deploys from CI:
resolve environment, install citadel, sync secrets, build/push, optional CDK,
roll out, wait — in one step.

## Usage

```yaml
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: ${{ github.ref_name == 'main' && 'prod' || 'dev' }}
    permissions:
      contents: read
    concurrency: { group: citadel-${{ github.ref_name }}, cancel-in-progress: false }
    steps:
      - uses: actions/checkout@v4
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
          aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
          aws-region: us-east-1
      - uses: ClusterBox/citadel/action@v0.3.0
        with:
          deploy-infra: true
          env-file: ${{ secrets.CITADEL_ENV_FILE }}
```

`main` deploys `prod` and `development` deploys `dev` by default (override
with `environment` or `branch-map`).

## Inputs

| Input | Default | Meaning |
|---|---|---|
| `environment` | `''` (resolved from the branch via `branch-map`) | citadel environment to deploy. Empty resolves it from the branch via branch-map. |
| `branch-map` | `main=prod,development=dev` | Comma-separated branch=env pairs used when environment is empty. |
| `env-file` | `''` | Contents of the .env file (pass a secret). Required unless skip-secrets is true. |
| `skip-secrets` | `false` | Skip syncing secrets to SSM (passes --skip-ssm). |
| `deploy-infra` | `false` | Also run cdk deploy (passes --deploy-infra); installs Node 20, the aws-cdk CLI and Go for cdk/go.mod. |
| `wait` | `true` | Wait for the deployment to stabilize (passes --wait). |
| `message` | `''` (head commit subject and short SHA) | Deploy message (-m). Empty uses the head commit subject and short SHA. |
| `working-directory` | `.` | Directory to run citadel in. |
| `config` | `citadel.yml` | Path to citadel.yml, relative to working-directory. |
| `version` | `''` (matches this action's tag) | citadel version (X.Y.Z). Empty uses the version matching this action's tag. |
| `upload-logs` | `true` | Upload .citadel/runs/<id> as a workflow artifact. |
| `install-only` | `false` | Test-only. Stop after installing citadel. |

## Outputs

| Output | Meaning |
|---|---|
| `environment` | The citadel environment deployed. |
| `run-id` | The .citadel run id. |
| `image-uri` | The image URI deployed. |

## Secrets

Create a `CITADEL_ENV_FILE` secret per GitHub environment (`dev`, `prod`, …)
holding the contents of that environment's `.env` file:

```bash
gh secret set CITADEL_ENV_FILE --env dev --repo <owner>/<repo> < dev.env
```

> [!WARNING]
> This moves the source of truth for secret values from laptops to GitHub
> environment secrets. A laptop deploy with a stale `.env` will overwrite
> values CI set, and vice versa; teams should update the GitHub secret first.

**Masking:** before citadel runs, the action parses the `.env` contents the
same way citadel's own env loader does (skipping blank lines and `#`
comments, splitting on the first `=`, stripping one layer of matching
quotes) and emits a `::add-mask::` workflow command for every non-empty
value, so none of it can appear in logs. A trimmed, non-blank,
non-comment line with no `=` — the continuation of a pasted multi-line
secret such as a PEM key — is masked too. The `.env` contents are written to
a private (`mktemp`, mode 0600) temp file under `RUNNER_TEMP` that is always
removed via an `EXIT` trap, since composite actions have no post-step to run
cleanup in.

## Versions

Pin `@vX.Y.Z` (e.g. `ClusterBox/citadel/action@v0.3.0`) to get exactly
citadel `vX.Y.Z`, downloaded as a checksum-verified release binary.

Branches, commit SHAs and moving tags (anything that isn't a `vX.Y.Z` ref)
build citadel from source at that ref instead, and the action prints an
`::notice::` that an unreleased citadel is in use. Pin to a `vX.Y.Z` tag for
reproducible deploys.

## What it does

1. Resolve the citadel environment from the `environment` input or the
   current branch via `branch-map`.
2. Decide whether to install a release or build from source, then install
   citadel (and set up Go first when building from source).
3. When `deploy-infra: true`, install Node 20 and the `aws-cdk` CLI, and set
   up Go for `cdk/go.mod` if the CDK app has one.
4. Run `citadel deploy` — sync secrets, build/push, optional CDK, roll out,
   wait — writing the env-file to a private temp file and masking every
   value first.
5. Locate the run citadel just recorded.
6. Upload `.citadel/runs/<id>/` as a workflow artifact named
   `citadel-run-<environment>-<run-id>` (when `upload-logs: true`).
7. Write a job summary from the run.

## Limitations

- Linux and macOS runners only (`RUNNER_OS` must be `Linux` or `macOS`).
- No Docker layer cache: each run's `citadel deploy` builds the image from
  scratch on the runner.
