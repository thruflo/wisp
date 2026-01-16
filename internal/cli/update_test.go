package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateDiff(t *testing.T) {
	tests := []struct {
		name         string
		old          string
		new          string
		wantAdded    []string // lines we expect to see with "+"
		wantRemoved  []string // lines we expect to see with "-"
		wantNoChange bool     // expect "No changes detected"
	}{
		{
			name:         "no changes",
			old:          "line 1\nline 2\nline 3",
			new:          "line 1\nline 2\nline 3",
			wantNoChange: true,
		},
		{
			name:        "simple addition",
			old:         "line 1\nline 3",
			new:         "line 1\nline 2\nline 3",
			wantAdded:   []string{"line 2"},
			wantRemoved: []string{},
		},
		{
			name:        "simple removal",
			old:         "line 1\nline 2\nline 3",
			new:         "line 1\nline 3",
			wantAdded:   []string{},
			wantRemoved: []string{"line 2"},
		},
		{
			name:        "modification",
			old:         "line 1\nold content\nline 3",
			new:         "line 1\nnew content\nline 3",
			wantAdded:   []string{"new content"},
			wantRemoved: []string{"old content"},
		},
		{
			name:        "multiple changes",
			old:         "header\nold section 1\nmiddle\nold section 2\nfooter",
			new:         "header\nnew section 1\nmiddle\nnew section 2\nfooter",
			wantAdded:   []string{"new section 1", "new section 2"},
			wantRemoved: []string{"old section 1", "old section 2"},
		},
		{
			name:        "addition at end",
			old:         "line 1\nline 2",
			new:         "line 1\nline 2\nline 3\nline 4",
			wantAdded:   []string{"line 3", "line 4"},
			wantRemoved: []string{},
		},
		{
			name:        "removal at end",
			old:         "line 1\nline 2\nline 3\nline 4",
			new:         "line 1\nline 2",
			wantAdded:   []string{},
			wantRemoved: []string{"line 3", "line 4"},
		},
		{
			name:        "empty to content",
			old:         "",
			new:         "new line 1\nnew line 2",
			wantAdded:   []string{"new line 1", "new line 2"},
			wantRemoved: []string{},
		},
		{
			name:        "content to empty",
			old:         "old line 1\nold line 2",
			new:         "",
			wantAdded:   []string{},
			wantRemoved: []string{"old line 1", "old line 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateDiff(tt.old, tt.new)

			if tt.wantNoChange {
				assert.Contains(t, result, "No changes detected")
				return
			}

			// Check that expected additions appear
			for _, added := range tt.wantAdded {
				assert.Contains(t, result, "+"+added,
					"expected addition %q not found in diff", added)
			}

			// Check that expected removals appear
			for _, removed := range tt.wantRemoved {
				assert.Contains(t, result, "-"+removed,
					"expected removal %q not found in diff", removed)
			}

			// Verify diff format
			assert.Contains(t, result, "# RFC Changes")
			assert.Contains(t, result, "```diff")
		})
	}
}

func TestGenerateDiff_Format(t *testing.T) {
	old := "# Title\n\nOld paragraph.\n\n## Section\n\nOld section content."
	new := "# Title\n\nNew paragraph.\n\n## Section\n\nNew section content."

	result := GenerateDiff(old, new)

	// Should have markdown header
	assert.True(t, strings.HasPrefix(result, "# RFC Changes"))

	// Should contain diff code block
	assert.Contains(t, result, "```diff")
	assert.Contains(t, result, "```")

	// Should show changes
	assert.Contains(t, result, "+New paragraph.")
	assert.Contains(t, result, "-Old paragraph.")
}

func TestGenerateDiff_LargeDocument(t *testing.T) {
	// Simulate a realistic RFC change
	old := `# Feature RFC

## Overview

This document describes the original feature implementation.

## Requirements

1. Requirement A
2. Requirement B
3. Requirement C

## Implementation

The implementation will follow the standard approach.

### Phase 1

- Setup infrastructure
- Configure dependencies

### Phase 2

- Implement core logic
- Add error handling

## Testing

- Unit tests
- Integration tests
`

	new := `# Feature RFC

## Overview

This document describes the updated feature implementation.

## Requirements

1. Requirement A
2. Requirement B (modified)
3. Requirement C
4. Requirement D (new)

## Implementation

The implementation will follow an improved approach.

### Phase 1

- Setup infrastructure
- Configure dependencies
- Add monitoring

### Phase 2

- Implement core logic
- Add error handling
- Add logging

## Testing

- Unit tests
- Integration tests
- Performance tests
`

	result := GenerateDiff(old, new)

	// Should detect key changes
	assert.Contains(t, result, "+")
	assert.Contains(t, result, "-")

	// Should be readable
	lines := strings.Split(result, "\n")
	assert.True(t, len(lines) > 5, "diff should have meaningful content")
}

func TestComputeLineDiff(t *testing.T) {
	tests := []struct {
		name     string
		old      []string
		new      []string
		wantDiff bool
	}{
		{
			name:     "identical",
			old:      []string{"a", "b", "c"},
			new:      []string{"a", "b", "c"},
			wantDiff: false,
		},
		{
			name:     "addition",
			old:      []string{"a", "c"},
			new:      []string{"a", "b", "c"},
			wantDiff: true,
		},
		{
			name:     "removal",
			old:      []string{"a", "b", "c"},
			new:      []string{"a", "c"},
			wantDiff: true,
		},
		{
			name:     "modification",
			old:      []string{"a", "old", "c"},
			new:      []string{"a", "new", "c"},
			wantDiff: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeLineDiff(tt.old, tt.new)

			if tt.wantDiff {
				assert.NotEmpty(t, result, "expected diff output")
			} else {
				assert.Empty(t, result, "expected no diff output")
			}
		})
	}
}
