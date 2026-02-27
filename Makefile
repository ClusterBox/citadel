.PHONY: build install test clean fmt vet

# Build the binary
build:
	go build -o bin/citadel ./cmd/citadel

# Install globally
install:
	go install ./cmd/citadel

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
