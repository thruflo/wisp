package tui

import (
	"bufio"
	"io"
	"unicode/utf8"
)

// Key represents a keyboard input.
type Key int

const (
	KeyUnknown Key = iota
	KeyEscape
	KeyEnter
	KeyBackspace
	KeyTab
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyCtrlC
	KeyCtrlD
	KeyRune // Regular character
)

// KeyEvent represents a key press event.
type KeyEvent struct {
	Key  Key
	Rune rune // Only valid when Key == KeyRune
}

// KeyReader reads keyboard input from a raw terminal.
type KeyReader struct {
	reader *bufio.Reader
}

// NewKeyReader creates a KeyReader from the given io.Reader.
// The reader should be a raw terminal input (e.g., os.Stdin after term.MakeRaw).
func NewKeyReader(r io.Reader) *KeyReader {
	return &KeyReader{
		reader: bufio.NewReaderSize(r, 64),
	}
}

// ReadKey reads a single key event from the input.
// This method blocks until a key is pressed.
func (k *KeyReader) ReadKey() (KeyEvent, error) {
	b, err := k.reader.ReadByte()
	if err != nil {
		return KeyEvent{}, err
	}

	switch b {
	case 0x03: // Ctrl+C
		return KeyEvent{Key: KeyCtrlC}, nil
	case 0x04: // Ctrl+D
		return KeyEvent{Key: KeyCtrlD}, nil
	case 0x09: // Tab
		return KeyEvent{Key: KeyTab}, nil
	case 0x0D: // Enter (carriage return)
		return KeyEvent{Key: KeyEnter}, nil
	case 0x7F, 0x08: // Backspace (DEL or BS)
		return KeyEvent{Key: KeyBackspace}, nil
	case 0x1B: // Escape or escape sequence start
		return k.readEscapeSequence()
	default:
		// Check if it's a printable character or UTF-8 sequence
		if b >= 0x20 && b < 0x7F {
			return KeyEvent{Key: KeyRune, Rune: rune(b)}, nil
		}
		// Handle UTF-8 multi-byte characters
		if b >= 0xC0 {
			return k.readUTF8(b)
		}
		return KeyEvent{Key: KeyUnknown}, nil
	}
}

// readEscapeSequence handles escape sequences (arrow keys, etc).
func (k *KeyReader) readEscapeSequence() (KeyEvent, error) {
	// Check if there's more data immediately available
	// If not, it's just the escape key
	if k.reader.Buffered() == 0 {
		// We can't use a timeout in blocking mode, so we'll read the next byte
		// But for a simple implementation, we'll just return Escape
		// In practice, terminals send escape sequences quickly enough
		b, err := k.reader.ReadByte()
		if err != nil {
			return KeyEvent{Key: KeyEscape}, nil
		}
		if b != '[' && b != 'O' {
			// Not a CSI or SS3 sequence, put it back conceptually
			// For simplicity, we'll just return escape
			k.reader.UnreadByte()
			return KeyEvent{Key: KeyEscape}, nil
		}
		return k.parseCSI(b)
	}

	b, err := k.reader.ReadByte()
	if err != nil {
		return KeyEvent{Key: KeyEscape}, nil
	}

	if b != '[' && b != 'O' {
		k.reader.UnreadByte()
		return KeyEvent{Key: KeyEscape}, nil
	}

	return k.parseCSI(b)
}

// parseCSI parses a CSI (Control Sequence Introducer) or SS3 sequence.
func (k *KeyReader) parseCSI(prefix byte) (KeyEvent, error) {
	b, err := k.reader.ReadByte()
	if err != nil {
		return KeyEvent{Key: KeyEscape}, nil
	}

	switch b {
	case 'A':
		return KeyEvent{Key: KeyUp}, nil
	case 'B':
		return KeyEvent{Key: KeyDown}, nil
	case 'C':
		return KeyEvent{Key: KeyRight}, nil
	case 'D':
		return KeyEvent{Key: KeyLeft}, nil
	default:
		// Unknown sequence, consume remaining bytes if any
		for k.reader.Buffered() > 0 {
			next, _ := k.reader.ReadByte()
			// Stop at terminal characters
			if (next >= 'A' && next <= 'Z') || next == '~' {
				break
			}
		}
		return KeyEvent{Key: KeyUnknown}, nil
	}
}

// readUTF8 reads a multi-byte UTF-8 character.
func (k *KeyReader) readUTF8(first byte) (KeyEvent, error) {
	var buf [4]byte
	buf[0] = first

	// Determine how many bytes we need
	var n int
	switch {
	case first&0xE0 == 0xC0:
		n = 2
	case first&0xF0 == 0xE0:
		n = 3
	case first&0xF8 == 0xF0:
		n = 4
	default:
		return KeyEvent{Key: KeyUnknown}, nil
	}

	// Read remaining bytes
	for i := 1; i < n; i++ {
		b, err := k.reader.ReadByte()
		if err != nil {
			return KeyEvent{Key: KeyUnknown}, err
		}
		buf[i] = b
	}

	r, _ := utf8.DecodeRune(buf[:n])
	if r == utf8.RuneError {
		return KeyEvent{Key: KeyUnknown}, nil
	}

	return KeyEvent{Key: KeyRune, Rune: r}, nil
}

// Shortcut represents a TUI keyboard shortcut.
type Shortcut int

const (
	ShortcutNone   Shortcut = iota
	ShortcutTail            // 't' - toggle tail view
	ShortcutAttach          // 'a' - attach to sprite console
	ShortcutDetach          // 'd' - detach from current view
	ShortcutKill            // 'k' - kill session
	ShortcutEscape          // esc - background/exit
	ShortcutQuit            // ctrl+c - quit
)

// ParseShortcut converts a KeyEvent to a Shortcut.
func ParseShortcut(ev KeyEvent) Shortcut {
	switch ev.Key {
	case KeyEscape:
		return ShortcutEscape
	case KeyCtrlC:
		return ShortcutQuit
	case KeyRune:
		switch ev.Rune {
		case 't', 'T':
			return ShortcutTail
		case 'a', 'A':
			return ShortcutAttach
		case 'd', 'D':
			return ShortcutDetach
		case 'k', 'K':
			return ShortcutKill
		}
	}
	return ShortcutNone
}

// LineEditor handles line-based text input for NEEDS_INPUT responses.
type LineEditor struct {
	buffer   []rune
	cursor   int
	terminal *Terminal
}

// NewLineEditor creates a LineEditor that displays to the given terminal.
func NewLineEditor(t *Terminal) *LineEditor {
	return &LineEditor{
		buffer:   make([]rune, 0, 256),
		cursor:   0,
		terminal: t,
	}
}

// HandleKey processes a key event and updates the line buffer.
// Returns true if Enter was pressed (line complete), false otherwise.
func (e *LineEditor) HandleKey(ev KeyEvent) bool {
	switch ev.Key {
	case KeyEnter:
		return true
	case KeyBackspace:
		if e.cursor > 0 {
			// Remove character before cursor
			copy(e.buffer[e.cursor-1:], e.buffer[e.cursor:])
			e.buffer = e.buffer[:len(e.buffer)-1]
			e.cursor--
		}
	case KeyLeft:
		if e.cursor > 0 {
			e.cursor--
		}
	case KeyRight:
		if e.cursor < len(e.buffer) {
			e.cursor++
		}
	case KeyRune:
		// Insert character at cursor
		e.buffer = append(e.buffer, 0)
		copy(e.buffer[e.cursor+1:], e.buffer[e.cursor:])
		e.buffer[e.cursor] = ev.Rune
		e.cursor++
	}
	return false
}

// Text returns the current line content.
func (e *LineEditor) Text() string {
	return string(e.buffer)
}

// Clear resets the line editor.
func (e *LineEditor) Clear() {
	e.buffer = e.buffer[:0]
	e.cursor = 0
}

// Cursor returns the current cursor position.
func (e *LineEditor) Cursor() int {
	return e.cursor
}

// Len returns the length of the current buffer.
func (e *LineEditor) Len() int {
	return len(e.buffer)
}
