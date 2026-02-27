# Citadel Installation Guide

## Install from Source

```bash
cd ~/Documents/github/employments/clusterbox/citadel
go mod tidy
go install ./cmd/citadel
```

This installs `citadel` to `~/go/bin/citadel`.

## Add to PATH

Add this to your `~/.zshrc` (or `~/.bashrc` if using bash):

```bash
export PATH="$HOME/go/bin:$PATH"
```

Then reload your shell:

```bash
source ~/.zshrc  # or source ~/.bashrc
```

## Verify Installation

```bash
citadel --version
# Output: citadel version dev

citadel --help
# Shows all available commands
```

## Quick Start

```bash
# In your project directory (with citadel.yml)
citadel deploy --env dev --dry-run

# Real deployment
citadel deploy --env dev --deploy-infra
```

## Uninstall

```bash
rm ~/go/bin/citadel
```

## For New Sessions

If `citadel` isn't found in a new terminal:

```bash
# One-time per session
export PATH="$HOME/go/bin:$PATH"

# Or make it permanent
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

## Alternative: Use Full Path

Instead of adding to PATH, you can always use:

```bash
~/go/bin/citadel deploy --env dev
```

## Building for Distribution (Future)

For production releases, build for multiple platforms:

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o citadel-linux-amd64 ./cmd/citadel

# macOS
GOOS=darwin GOARCH=amd64 go build -o citadel-darwin-amd64 ./cmd/citadel
GOOS=darwin GOARCH=arm64 go build -o citadel-darwin-arm64 ./cmd/citadel

# Windows
GOOS=windows GOARCH=amd64 go build -o citadel-windows-amd64.exe ./cmd/citadel
```

## Homebrew (Future)

When published:

```bash
brew install clusterbox/tap/citadel
```

## Dependencies

Citadel requires:
- **Go** (for installation from source)
- **Docker** (for building images)
- **AWS CLI** (for account ID lookup)
- **CDK CLI** (for infrastructure deployment)
- **Git** (for SHA tagging)

Install dependencies:

```bash
# Docker (if not installed)
# See: https://docs.docker.com/engine/install/

# AWS CLI
brew install awscli  # or apt-get install awscli

# CDK CLI
npm install -g aws-cdk

# Git (usually pre-installed)
```
