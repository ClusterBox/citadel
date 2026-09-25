.PHONY: build build-logs install install-logs uninstall update test clean fmt vet docker-logs check dev release-snapshot actionlint

# Resolve where `go install` places binaries (GOBIN, else GOPATH/bin)
GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

# Build the binary
build:
	go build -o bin/citadel ./cmd/citadel

# Build the logs daemon binary
build-logs:
	go build -o bin/citadel-logs ./cmd/citadel-logs

# Install globally
install: install-logs
	go install ./cmd/citadel
	@echo "Installed citadel to $(GOBIN)/citadel"
	@case ":$$PATH:" in *":$(GOBIN):"*) ;; *) echo "⚠️  $(GOBIN) is not on your PATH";; esac

# Install the logs daemon globally
install-logs:
	go install ./cmd/citadel-logs

# Build the citadel-logs Docker image
docker-logs:
	docker build -f Dockerfile.logs -t clusterbox/citadel-logs:latest .

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -rf bin/ dist/

# Format code
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

# Run all checks
check: fmt vet test

# Development build with race detector
dev:
	go build -race -o bin/citadel ./cmd/citadel

# Remove the installed binary
uninstall:
	rm -f $(GOBIN)/citadel $(GOBIN)/citadel-logs
	@echo "Removed $(GOBIN)/citadel and $(GOBIN)/citadel-logs"

# Pull latest and reinstall
update:
	git pull --ff-only
	go install ./cmd/citadel ./cmd/citadel-logs
	@echo "Updated citadel in $(GOBIN)"

# Build release archives locally without publishing (needs Docker).
# Note: "goreleaser/goreleaser:v2" is not a published tag on Docker Hub
# (only full semver tags like v2.18.2 and "latest" exist); pinned to the
# latest v2.x stable release for determinism.
# Runs as the host UID/GID so dist/ isn't left root-owned; HOME=/tmp so
# `git config --global` has somewhere to write for a UID with no passwd entry.
release-snapshot:
	docker run --rm -v "$(CURDIR):/src" -w /src -u "$$(id -u):$$(id -g)" -e HOME=/tmp --entrypoint sh goreleaser/goreleaser:v2.18.2 \
		-c 'git config --global --add safe.directory /src && goreleaser release --snapshot --clean'

# Lint GitHub workflow files (needs Docker).
actionlint:
	docker run --rm -v "$(CURDIR):/repo" -w /repo rhysd/actionlint:latest -color
