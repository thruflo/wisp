.PHONY: build test test-integration test-real-sprites test-e2e cleanup-test-sprites clean

# Default Go build flags
GOFLAGS ?= -v

# Build all binaries
build:
	go build $(GOFLAGS) -o wisp ./cmd/wisp
	go build $(GOFLAGS) -o cleanup-test-sprites ./cmd/cleanup-test-sprites

# Run unit tests
test:
	go test ./...

# Run integration tests (with mocks)
test-integration:
	go test -tags=integration ./internal/integration/...

# Run real Sprite tests (requires SPRITE_TOKEN)
test-real-sprites:
	go test -v -tags=integration,real_sprites -timeout 5m ./internal/integration/...

# Run E2E tests (requires SPRITE_TOKEN, builds actual CLI)
test-e2e:
	go test -v -tags=integration,real_sprites,e2e -timeout 10m ./internal/integration/...

# Clean up orphan test sprites (dry run)
cleanup-test-sprites:
	go run ./cmd/cleanup-test-sprites

# Clean up orphan test sprites (actually delete)
cleanup-test-sprites-force:
	go run ./cmd/cleanup-test-sprites --force

# Clean build artifacts
clean:
	rm -f wisp cleanup-test-sprites
	go clean ./...
