.PHONY: build build-logs install install-logs uninstall update test clean fmt vet docker-logs check dev

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
	rm -f $(GOBIN)/citadel
	@echo "Removed $(GOBIN)/citadel"

# Pull latest and reinstall
update:
	git pull --ff-only
	go install ./cmd/citadel ./cmd/citadel-logs
	@echo "Updated citadel in $(GOBIN)"
