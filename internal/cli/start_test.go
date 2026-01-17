package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateBranchName(t *testing.T) {
	tests := []struct {
		name     string
		specPath string
		want     string
	}{
		{
			name:     "simple markdown file",
			specPath: "docs/rfc.md",
			want:     "wisp/rfc",
		},
		{
			name:     "file with spaces",
			specPath: "docs/my feature spec.md",
			want:     "wisp/my-feature-spec",
		},
		{
			name:     "file with underscores",
			specPath: "docs/my_feature_spec.md",
			want:     "wisp/my-feature-spec",
		},
		{
			name:     "file with uppercase",
			specPath: "docs/MyFeatureSpec.md",
			want:     "wisp/myfeaturespec",
		},
		{
			name:     "nested path",
			specPath: "docs/features/auth/spec.md",
			want:     "wisp/spec",
		},
		{
			name:     "txt extension",
			specPath: "spec.txt",
			want:     "wisp/spec",
		},
		{
			name:     "no extension",
			specPath: "RFC",
			want:     "wisp/rfc",
		},
		{
			name:     "special characters removed",
			specPath: "docs/feat!@#$%ure.md",
			want:     "wisp/feature",
		},
		{
			name:     "multiple hyphens collapsed",
			specPath: "docs/my---feature.md",
			want:     "wisp/my-feature",
		},
		{
			name:     "leading/trailing hyphens trimmed",
			specPath: "docs/-feature-.md",
			want:     "wisp/feature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateBranchName(tt.specPath)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple lowercase",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "uppercase converted",
			input: "HelloWorld",
			want:  "helloworld",
		},
		{
			name:  "spaces to hyphens",
			input: "hello world",
			want:  "hello-world",
		},
		{
			name:  "underscores to hyphens",
			input: "hello_world",
			want:  "hello-world",
		},
		{
			name:  "special chars removed",
			input: "hello@world!",
			want:  "helloworld",
		},
		{
			name:  "numbers preserved",
			input: "feature123",
			want:  "feature123",
		},
		{
			name:  "multiple hyphens collapsed",
			input: "hello---world",
			want:  "hello-world",
		},
		{
			name:  "leading hyphen trimmed",
			input: "-hello",
			want:  "hello",
		},
		{
			name:  "trailing hyphen trimmed",
			input: "hello-",
			want:  "hello",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only special chars",
			input: "!@#$%",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHeadlessFlagRegistered(t *testing.T) {
	// Verify the headless flag is registered on the start command
	flag := startCmd.Flags().Lookup("headless")
	require.NotNil(t, flag, "--headless flag should be registered")
	assert.Equal(t, "bool", flag.Value.Type())
	assert.Equal(t, "false", flag.DefValue)
	assert.Contains(t, flag.Usage, "TUI")
}

func TestHeadlessResultJSONFormat(t *testing.T) {
	// Verify HeadlessResult serializes correctly to JSON
	result := HeadlessResult{
		Reason:     "completed",
		Iterations: 5,
		Branch:     "wisp/test-feature",
		SpriteName: "wisp-org-repo-abc123",
		Status:     "DONE",
		Summary:    "All tasks completed successfully",
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	// Verify all fields are present
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "completed", parsed["reason"])
	assert.Equal(t, float64(5), parsed["iterations"])
	assert.Equal(t, "wisp/test-feature", parsed["branch"])
	assert.Equal(t, "wisp-org-repo-abc123", parsed["sprite_name"])
	assert.Equal(t, "DONE", parsed["status"])
	assert.Equal(t, "All tasks completed successfully", parsed["summary"])
	assert.NotContains(t, parsed, "error") // Should be omitted when empty
}

func TestHeadlessResultWithError(t *testing.T) {
	// Verify HeadlessResult includes error when present
	result := HeadlessResult{
		Reason:     "crash",
		Iterations: 3,
		Branch:     "wisp/test",
		SpriteName: "test-sprite",
		Error:      "connection failed: timeout",
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "crash", parsed["reason"])
	assert.Equal(t, "connection failed: timeout", parsed["error"])
}
