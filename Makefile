.PHONY: build build-logs install install-logs test clean fmt vet docker-logs

# Build the binary
build:
	go build -o bin/citadel ./cmd/citadel

# Build the logs daemon binary
build-logs:
	go build -o bin/citadel-logs ./cmd/citadel-logs

# Install globally
install:
	go install ./cmd/citadel

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
