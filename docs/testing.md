# Testing Guide

This document describes wisp's test tiers, how to run them, required setup, and debugging strategies.

## Test Tiers

Wisp uses a tiered testing approach, from fast unit tests to slow end-to-end tests:

| Tier | Build Tags | Credentials | What it Tests |
|------|------------|-------------|---------------|
| Unit | none | none | Individual functions and packages |
| Integration | `integration` | none | Component workflows with mocks |
| Real Sprites | `integration,real_sprites` | `SPRITE_TOKEN` | Real Sprite infrastructure |
| Real Claude | `integration,real_sprites,real_claude` | `SPRITE_TOKEN` + Claude on Sprite | Full Claude execution |
| E2E | `e2e` | varies | Complete CLI workflows |

## Running Tests

### Unit Tests

Unit tests run without any special setup:

```bash
go test ./...
```

For verbose output:

```bash
go test -v ./...
```

### Integration Tests (with mocks)

These tests exercise component workflows using mock Sprite clients:

```bash
go test -tags=integration ./internal/integration/...
```

Or via Make:

```bash
make test-integration
```

### Real Sprite Tests

These tests create actual Sprites and require valid credentials:

```bash
# Ensure SPRITE_TOKEN is set (see Credentials section below)
go test -v -tags=integration,real_sprites -timeout 5m ./internal/integration/...
```

Or via Make:

```bash
make test-real-sprites
```

### Real Claude Tests

These tests run Claude on real Sprites and incur API costs:

```bash
go test -v -tags=integration,real_sprites,real_claude -timeout 10m ./internal/integration/...
```

### E2E Tests

End-to-end tests build the actual wisp binary and run CLI commands:

```bash
go test -v -tags=e2e -timeout 10m ./internal/integration/...
```

For E2E tests with real Sprites:

```bash
go test -v -tags=integration,real_sprites,e2e -timeout 10m ./internal/integration/...
```

Or via Make:

```bash
make test-e2e
```

### Running Specific Tests

Run a single test by name:

```bash
go test -v -run TestRealSprite_CreateAndDelete -tags=integration,real_sprites ./internal/integration/...
```

### Short Mode

Skip slow tests during development:

```bash
go test -short ./...
```

Real Sprite tests automatically skip in short mode.

## Credentials Setup

### Local Development

Create `.wisp/.sprite.env` with your credentials:

```bash
SPRITE_TOKEN="your-sprite-token"
GITHUB_TOKEN="your-github-token"
```

The test utilities automatically load credentials from:
1. `.wisp/.sprite.env` in the project root
2. Environment variables (fallback)

### CI/CD

Set credentials as environment variables:

```bash
export SPRITE_TOKEN="..."
export GITHUB_TOKEN="..."
```

### Verifying Credentials

Test that credentials are working:

```bash
go test -v -run TestRealSprite_CreateAndDelete -tags=integration,real_sprites ./internal/integration/...
```

If credentials are missing, tests skip with a message:

```
SPRITE_TOKEN not available, skipping real Sprite test
```

## Test Directory Structure

```
internal/
  integration/
    main_test.go                    # TestMain with global cleanup
    components_integration_test.go  # Mock-based workflow tests
    real_sprites_integration_test.go # Real Sprite SDK tests
    real_claude_integration_test.go # Real Claude execution tests
    production_paths_test.go        # Production code path verification
    cli_harness_test.go             # CLI test harness
    cli_e2e_test.go                 # CLI E2E test cases
    headless_test.go                # Headless mode tests
  testutil/
    env.go                          # Test environment helpers
    fixtures.go                     # Sample test data
    assertions.go                   # Custom test assertions
    cleanup.go                      # Sprite cleanup registry
    timeout.go                      # Test timeout helpers
```

## Test Utilities

### Setting Up a Test Environment

```go
func TestExample(t *testing.T) {
    tmpDir, store := testutil.SetupTestDir(t)
    // tmpDir has full .wisp structure with config, templates
    // store is ready to use for state operations
}
```

### Real Sprite Testing

```go
func TestRealSpriteExample(t *testing.T) {
    env := testutil.SetupRealSpriteEnv(t) // Skips if no credentials

    spriteName := testutil.GenerateTestSpriteName(t)
    t.Cleanup(func() {
        testutil.CleanupSprite(t, env.Client, spriteName)
    })

    // Create and use the Sprite
    ctx, cancel := testutil.SpriteOperationContext(t)
    defer cancel()

    err := env.Client.Create(ctx, spriteName, "")
    require.NoError(t, err)
}
```

### Sprite Cleanup Registry

For global cleanup in case individual test cleanup fails:

```go
testutil.RegisterSprite(spriteName)
// ... test code ...
// Sprite will be cleaned up by TestMain even if t.Cleanup fails
```

### Test Timeouts

Use deadline-aware contexts to respect test timeouts:

```go
ctx, cancel := testutil.ContextWithTestDeadline(t, 10*time.Second)
defer cancel()
```

Or for standard Sprite operations (30-second timeout):

```go
ctx, cancel := testutil.SpriteOperationContext(t)
defer cancel()
```

## Debugging Failed Tests

### Verbose Output

Add `-v` for detailed test output:

```bash
go test -v -run TestName -tags=integration ./internal/integration/...
```

### Preserving Test Sprites

To keep Sprites for debugging, comment out the cleanup in your test:

```go
// t.Cleanup(func() {
//     testutil.CleanupSprite(t, env.Client, spriteName)
// })
```

Then inspect the Sprite directly:

```bash
sprite ssh <sprite-name>
```

### Race Detection

Run tests with race detection:

```bash
go test -race ./...
```

### Timeout Issues

If tests hang, add explicit timeouts:

```bash
go test -timeout 2m ./internal/integration/...
```

For integration tests, always use `-timeout` to prevent indefinite hangs:

```bash
go test -tags=integration,real_sprites -timeout 5m ./internal/integration/...
```

### Test Logs

Tests log diagnostic information with `t.Logf`. View with `-v`:

```bash
go test -v -run TestRealSprite_SyncManagerIntegration -tags=integration,real_sprites ./internal/integration/...
```

Example output:

```
=== RUN   TestRealSprite_SyncManagerIntegration
    real_sprites_integration_test.go:229: testing with Sprite: wisp-test-a1b2c3d4-12345678
    testutil_test.go:267: cleanup: deleted Sprite wisp-test-a1b2c3d4-12345678
--- PASS: TestRealSprite_SyncManagerIntegration (15.23s)
```

## Cleaning Up Orphan Sprites

Test failures or interrupts can leave orphan Sprites. Use the cleanup utility:

### List Orphan Sprites (Dry Run)

```bash
make cleanup-test-sprites
```

Or directly:

```bash
go run ./cmd/cleanup-test-sprites
```

Output:

```
Found 3 orphan sprite(s) matching "wisp-test-*" older than 1h0m0s:

  wisp-test-a1b2c3d4-12345678 (created 2h30m ago)
  wisp-test-b2c3d4e5-23456789 (created 1h45m ago)
  wisp-test-c3d4e5f6-34567890 (created 1h15m ago)

Dry run - no sprites deleted. Use --force to delete.
```

### Delete Orphan Sprites

```bash
make cleanup-test-sprites-force
```

Or:

```bash
go run ./cmd/cleanup-test-sprites --force
```

### Custom Age Threshold

Delete Sprites older than 30 minutes:

```bash
go run ./cmd/cleanup-test-sprites --force --max-age 30m
```

### How It Works

The cleanup utility:
1. Loads `SPRITE_TOKEN` from environment or `.wisp/.sprite.env`
2. Lists all Sprites matching pattern `wisp-test-*`
3. Filters to Sprites older than the threshold (default 1 hour)
4. Deletes matching Sprites (with `--force`)

## Writing New Tests

### Unit Tests

Place alongside source code:

```
internal/cli/
  start.go
  start_test.go
```

Use table-driven tests:

```go
func TestParseStatus(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    Status
        wantErr bool
    }{
        {"continue", "CONTINUE", StatusContinue, false},
        {"invalid", "INVALID", "", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := ParseStatus(tt.input)
            if tt.wantErr {
                assert.Error(t, err)
                return
            }
            assert.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

### Integration Tests

Add to `internal/integration/` with appropriate build tags:

```go
//go:build integration

package integration

func TestWorkflow(t *testing.T) {
    // Uses mocks
}
```

For real Sprites:

```go
//go:build integration && real_sprites

package integration

func TestRealSpriteWorkflow(t *testing.T) {
    env := testutil.SetupRealSpriteEnv(t)
    // Uses real Sprite
}
```

### E2E Tests

Use the CLI harness:

```go
//go:build e2e

package integration

func TestCLI_MyCommand(t *testing.T) {
    h := NewCLIHarness(t)
    result := h.Run("my-command", "--flag")
    h.RequireSuccess(result, "command should succeed")
    assert.Contains(t, result.Stdout, "expected output")
}
```

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make test` | Run unit tests |
| `make test-integration` | Run integration tests with mocks |
| `make test-real-sprites` | Run real Sprite tests |
| `make test-e2e` | Run E2E tests |
| `make cleanup-test-sprites` | List orphan Sprites (dry run) |
| `make cleanup-test-sprites-force` | Delete orphan Sprites |
| `make clean` | Remove build artifacts |
