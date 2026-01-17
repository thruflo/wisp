package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Box drawing characters (Unicode)
const (
	BoxTopLeft     = "┌"
	BoxTopRight    = "┐"
	BoxBottomLeft  = "└"
	BoxBottomRight = "┘"
	BoxHorizontal  = "─"
	BoxVertical    = "│"
)

// VisualWidth returns the visual width of a string, excluding ANSI escape codes.
func VisualWidth(s string) int {
	return utf8.RuneCountInString(StripAnsi(s))
}

// StripAnsi removes ANSI escape codes from a string.
func StripAnsi(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '\033' && s[i+1] == '[' {
			// Skip the escape sequence until we hit a terminator letter
			j := i + 2
			for j < len(s) && !isAnsiTerminator(s[j]) {
				j++
			}
			if j < len(s) {
				j++ // Skip the terminator letter
			}
			i = j
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

// isAnsiTerminator returns true if the byte is an ANSI escape sequence terminator.
func isAnsiTerminator(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// Box draws a box with the given dimensions.
// Returns a slice of strings, one per line.
func Box(width, height int) []string {
	if width < 2 || height < 2 {
		return nil
	}

	lines := make([]string, height)

	// Top border
	lines[0] = BoxTopLeft + strings.Repeat(BoxHorizontal, width-2) + BoxTopRight

	// Middle rows
	middle := BoxVertical + strings.Repeat(" ", width-2) + BoxVertical
	for i := 1; i < height-1; i++ {
		lines[i] = middle
	}

	// Bottom border
	lines[height-1] = BoxBottomLeft + strings.Repeat(BoxHorizontal, width-2) + BoxBottomRight

	return lines
}

// BoxWithContent draws a box containing the given content lines.
// Each line is padded/truncated to fit within the box.
func BoxWithContent(width int, content []string) []string {
	if width < 4 {
		return nil
	}

	innerWidth := width - 4 // Account for borders and padding
	height := len(content) + 2

	lines := make([]string, height)

	// Top border
	lines[0] = BoxTopLeft + strings.Repeat(BoxHorizontal, width-2) + BoxTopRight

	// Content rows
	for i, line := range content {
		lines[i+1] = BoxVertical + " " + PadOrTruncate(line, innerWidth) + " " + BoxVertical
	}

	// Bottom border
	lines[height-1] = BoxBottomLeft + strings.Repeat(BoxHorizontal, width-2) + BoxBottomRight

	return lines
}

// PadOrTruncate pads or truncates a string to exactly width visual characters.
// Uses VisualWidth to properly handle ANSI escape codes.
func PadOrTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}

	visualLen := VisualWidth(s)

	if visualLen == width {
		return s
	}

	if visualLen < width {
		return s + strings.Repeat(" ", width-visualLen)
	}

	// Truncate based on visual width, preserving ANSI codes
	return truncateVisual(s, width)
}

// truncateVisual truncates a string to a visual width, handling ANSI codes.
func truncateVisual(s string, width int) string {
	if width <= 0 {
		return ""
	}

	var result strings.Builder
	visualPos := 0
	i := 0
	hasAnsi := false

	// Reserve space for ellipsis if needed
	targetWidth := width
	if width >= 3 {
		targetWidth = width - 3
	}

	for i < len(s) && visualPos < targetWidth {
		if i+1 < len(s) && s[i] == '\033' && s[i+1] == '[' {
			// ANSI escape sequence - copy it but don't count visual width
			hasAnsi = true
			start := i
			j := i + 2
			for j < len(s) && !isAnsiTerminator(s[j]) {
				j++
			}
			if j < len(s) {
				j++ // Include the terminator
			}
			result.WriteString(s[start:j])
			i = j
		} else {
			// Regular character - count it and copy
			_, size := utf8.DecodeRuneInString(s[i:])
			result.WriteString(s[i : i+size])
			visualPos++
			i += size
		}
	}

	// Add ellipsis; include reset code only if string had ANSI codes
	if width >= 3 && VisualWidth(s) > width {
		if hasAnsi {
			result.WriteString(Reset)
		}
		result.WriteString("...")
	}

	return result.String()
}

// Truncate truncates a string to max width, adding ellipsis if needed.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= width {
		return s
	}

	if width >= 3 {
		return string(runes[:width-3]) + "..."
	}
	return string(runes[:width])
}

// WrapText wraps text to fit within the given width.
// Returns a slice of lines.
func WrapText(text string, width int) []string {
	if width <= 0 {
		return nil
	}

	var lines []string
	words := strings.Fields(text)

	if len(words) == 0 {
		return lines
	}

	currentLine := words[0]

	for _, word := range words[1:] {
		if utf8.RuneCountInString(currentLine)+1+utf8.RuneCountInString(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

// CenterText centers text within the given width.
func CenterText(s string, width int) string {
	runeLen := utf8.RuneCountInString(s)
	if runeLen >= width {
		return PadOrTruncate(s, width)
	}

	leftPad := (width - runeLen) / 2
	rightPad := width - runeLen - leftPad

	return strings.Repeat(" ", leftPad) + s + strings.Repeat(" ", rightPad)
}

// RightAlign right-aligns text within the given width.
func RightAlign(s string, width int) string {
	runeLen := utf8.RuneCountInString(s)
	if runeLen >= width {
		return PadOrTruncate(s, width)
	}

	return strings.Repeat(" ", width-runeLen) + s
}

// ProgressBar renders a simple progress bar.
// Returns a string like "[████████░░░░░░░░] 50%"
func ProgressBar(current, total, width int) string {
	if total == 0 || width < 10 {
		return ""
	}

	// Calculate percentage
	pct := float64(current) / float64(total)
	if pct > 1 {
		pct = 1
	}

	// Calculate filled/empty sections
	barWidth := width - 7 // Space for "[] XXX%"
	filled := int(pct * float64(barWidth))
	empty := barWidth - filled

	// Build the bar
	bar := "[" +
		strings.Repeat("█", filled) +
		strings.Repeat("░", empty) +
		"]"

	// Add percentage
	pctNum := int(pct * 100)
	pctStr := fmt.Sprintf("%3d", pctNum)

	return bar + " " + pctStr + "%"
}

// Style applies ANSI style codes to text.
func Style(s string, codes ...string) string {
	if len(codes) == 0 {
		return s
	}
	return strings.Join(codes, "") + s + Reset
}

// StatusColor returns an appropriate color code for the given status.
func StatusColor(status string) string {
	switch strings.ToUpper(status) {
	case "RUNNING", "CONTINUE":
		return FgGreen
	case "DONE", "COMPLETE", "COMPLETED":
		return FgBrightGreen
	case "BLOCKED", "ERROR", "FAILED":
		return FgRed
	case "NEEDS_INPUT", "WAITING":
		return FgYellow
	case "STOPPED":
		return FgBrightBlack
	default:
		return ""
	}
}

// FormatStatus formats a status string with appropriate color.
func FormatStatus(status string) string {
	color := StatusColor(status)
	if color == "" {
		return status
	}
	return Style(status, color, Bold)
}
