package tui

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyReader_ReadKey_SingleChar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  KeyEvent
	}{
		{"letter a", []byte{'a'}, KeyEvent{Key: KeyRune, Rune: 'a'}},
		{"letter Z", []byte{'Z'}, KeyEvent{Key: KeyRune, Rune: 'Z'}},
		{"digit 5", []byte{'5'}, KeyEvent{Key: KeyRune, Rune: '5'}},
		{"space", []byte{' '}, KeyEvent{Key: KeyRune, Rune: ' '}},
		{"punctuation", []byte{'!'}, KeyEvent{Key: KeyRune, Rune: '!'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := NewKeyReader(bytes.NewReader(tt.input))
			got, err := reader.ReadKey()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestKeyReader_ReadKey_ControlChars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  KeyEvent
	}{
		{"ctrl+c", []byte{0x03}, KeyEvent{Key: KeyCtrlC}},
		{"ctrl+d", []byte{0x04}, KeyEvent{Key: KeyCtrlD}},
		{"tab", []byte{0x09}, KeyEvent{Key: KeyTab}},
		{"enter", []byte{0x0D}, KeyEvent{Key: KeyEnter}},
		{"backspace DEL", []byte{0x7F}, KeyEvent{Key: KeyBackspace}},
		{"backspace BS", []byte{0x08}, KeyEvent{Key: KeyBackspace}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := NewKeyReader(bytes.NewReader(tt.input))
			got, err := reader.ReadKey()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestKeyReader_ReadKey_ArrowKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  KeyEvent
	}{
		{"up arrow", []byte{0x1B, '[', 'A'}, KeyEvent{Key: KeyUp}},
		{"down arrow", []byte{0x1B, '[', 'B'}, KeyEvent{Key: KeyDown}},
		{"right arrow", []byte{0x1B, '[', 'C'}, KeyEvent{Key: KeyRight}},
		{"left arrow", []byte{0x1B, '[', 'D'}, KeyEvent{Key: KeyLeft}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := NewKeyReader(bytes.NewReader(tt.input))
			got, err := reader.ReadKey()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestKeyReader_ReadKey_UTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  KeyEvent
	}{
		{"euro sign", []byte{0xE2, 0x82, 0xAC}, KeyEvent{Key: KeyRune, Rune: '€'}},
		{"chinese char", []byte{0xE4, 0xB8, 0xAD}, KeyEvent{Key: KeyRune, Rune: '中'}},
		{"emoji", []byte{0xF0, 0x9F, 0x98, 0x80}, KeyEvent{Key: KeyRune, Rune: '😀'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := NewKeyReader(bytes.NewReader(tt.input))
			got, err := reader.ReadKey()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseShortcut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event KeyEvent
		want  Shortcut
	}{
		{"t key", KeyEvent{Key: KeyRune, Rune: 't'}, ShortcutTail},
		{"T key", KeyEvent{Key: KeyRune, Rune: 'T'}, ShortcutTail},
		{"a key", KeyEvent{Key: KeyRune, Rune: 'a'}, ShortcutAttach},
		{"A key", KeyEvent{Key: KeyRune, Rune: 'A'}, ShortcutAttach},
		{"d key", KeyEvent{Key: KeyRune, Rune: 'd'}, ShortcutDetach},
		{"D key", KeyEvent{Key: KeyRune, Rune: 'D'}, ShortcutDetach},
		{"k key", KeyEvent{Key: KeyRune, Rune: 'k'}, ShortcutKill},
		{"K key", KeyEvent{Key: KeyRune, Rune: 'K'}, ShortcutKill},
		{"escape", KeyEvent{Key: KeyEscape}, ShortcutEscape},
		{"ctrl+c", KeyEvent{Key: KeyCtrlC}, ShortcutQuit},
		{"other key", KeyEvent{Key: KeyRune, Rune: 'x'}, ShortcutNone},
		{"enter", KeyEvent{Key: KeyEnter}, ShortcutNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ParseShortcut(tt.event))
		})
	}
}

func TestLineEditor_HandleKey_Characters(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)
	editor := NewLineEditor(term)

	// Type "hello"
	for _, r := range "hello" {
		done := editor.HandleKey(KeyEvent{Key: KeyRune, Rune: r})
		assert.False(t, done)
	}

	assert.Equal(t, "hello", editor.Text())
	assert.Equal(t, 5, editor.Cursor())
	assert.Equal(t, 5, editor.Len())
}

func TestLineEditor_HandleKey_Backspace(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)
	editor := NewLineEditor(term)

	// Type "hello"
	for _, r := range "hello" {
		editor.HandleKey(KeyEvent{Key: KeyRune, Rune: r})
	}

	// Backspace twice
	editor.HandleKey(KeyEvent{Key: KeyBackspace})
	editor.HandleKey(KeyEvent{Key: KeyBackspace})

	assert.Equal(t, "hel", editor.Text())
	assert.Equal(t, 3, editor.Cursor())
}

func TestLineEditor_HandleKey_BackspaceAtStart(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)
	editor := NewLineEditor(term)

	// Backspace on empty buffer should do nothing
	editor.HandleKey(KeyEvent{Key: KeyBackspace})
	assert.Equal(t, "", editor.Text())
	assert.Equal(t, 0, editor.Cursor())
}

func TestLineEditor_HandleKey_ArrowKeys(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)
	editor := NewLineEditor(term)

	// Type "abc"
	for _, r := range "abc" {
		editor.HandleKey(KeyEvent{Key: KeyRune, Rune: r})
	}

	// Move left
	editor.HandleKey(KeyEvent{Key: KeyLeft})
	assert.Equal(t, 2, editor.Cursor())

	editor.HandleKey(KeyEvent{Key: KeyLeft})
	assert.Equal(t, 1, editor.Cursor())

	// Move right
	editor.HandleKey(KeyEvent{Key: KeyRight})
	assert.Equal(t, 2, editor.Cursor())

	// Can't go past end
	editor.HandleKey(KeyEvent{Key: KeyRight})
	editor.HandleKey(KeyEvent{Key: KeyRight})
	assert.Equal(t, 3, editor.Cursor())

	// Can't go before start
	editor.HandleKey(KeyEvent{Key: KeyLeft})
	editor.HandleKey(KeyEvent{Key: KeyLeft})
	editor.HandleKey(KeyEvent{Key: KeyLeft})
	editor.HandleKey(KeyEvent{Key: KeyLeft})
	assert.Equal(t, 0, editor.Cursor())
}

func TestLineEditor_HandleKey_Enter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)
	editor := NewLineEditor(term)

	// Type "hello"
	for _, r := range "hello" {
		editor.HandleKey(KeyEvent{Key: KeyRune, Rune: r})
	}

	// Enter returns true
	done := editor.HandleKey(KeyEvent{Key: KeyEnter})
	assert.True(t, done)
	assert.Equal(t, "hello", editor.Text())
}

func TestLineEditor_HandleKey_InsertInMiddle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)
	editor := NewLineEditor(term)

	// Type "hlo"
	for _, r := range "hlo" {
		editor.HandleKey(KeyEvent{Key: KeyRune, Rune: r})
	}

	// Move to position 1 and insert 'e'
	editor.HandleKey(KeyEvent{Key: KeyLeft})
	editor.HandleKey(KeyEvent{Key: KeyLeft})
	editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'e'})

	assert.Equal(t, "helo", editor.Text())
	assert.Equal(t, 2, editor.Cursor())

	// Insert 'l'
	editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'l'})
	assert.Equal(t, "hello", editor.Text())
}

func TestLineEditor_Clear(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)
	editor := NewLineEditor(term)

	// Type something
	for _, r := range "hello" {
		editor.HandleKey(KeyEvent{Key: KeyRune, Rune: r})
	}

	editor.Clear()

	assert.Equal(t, "", editor.Text())
	assert.Equal(t, 0, editor.Cursor())
	assert.Equal(t, 0, editor.Len())
}
