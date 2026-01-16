package tui

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
)

// Notifier handles notifications for TUI and backgrounded modes.
// When the TUI is active, it uses terminal bell.
// When backgrounded, it uses OS-native notifications.
type Notifier struct {
	out io.Writer
}

// NewNotifier creates a Notifier that writes bell to the given output.
func NewNotifier(out io.Writer) *Notifier {
	return &Notifier{out: out}
}

// Bell writes the terminal bell character to output.
// This produces an audible alert when the terminal is in the foreground.
func (n *Notifier) Bell() {
	fmt.Fprint(n.out, Bell)
}

// NotifyOS sends an OS-native notification.
// On macOS, this uses osascript to display a notification.
// On other platforms, this is a no-op.
func (n *Notifier) NotifyOS(title, message string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return notifyMacOS(title, message)
}

// NotifyAttention sends a notification requiring user attention.
// If isForeground is true, it rings the terminal bell.
// If isForeground is false, it sends an OS notification.
func (n *Notifier) NotifyAttention(title, message string, isForeground bool) error {
	if isForeground {
		n.Bell()
		return nil
	}
	return n.NotifyOS(title, message)
}

// notifyMacOS sends a notification using osascript on macOS.
func notifyMacOS(title, message string) error {
	script := fmt.Sprintf(`display notification %q with title %q`, message, title)
	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}

// NotificationReason represents why a notification is being sent.
type NotificationReason int

const (
	NotifyReasonNeedsInput NotificationReason = iota
	NotifyReasonBlocked
	NotifyReasonDone
	NotifyReasonStuck
	NotifyReasonMaxIterations
	NotifyReasonMaxDuration
	NotifyReasonMaxBudget
	NotifyReasonCrash
)

// String returns a human-readable title for the notification reason.
func (r NotificationReason) String() string {
	switch r {
	case NotifyReasonNeedsInput:
		return "Input Required"
	case NotifyReasonBlocked:
		return "Blocked"
	case NotifyReasonDone:
		return "Completed"
	case NotifyReasonStuck:
		return "Stuck"
	case NotifyReasonMaxIterations:
		return "Iteration Limit"
	case NotifyReasonMaxDuration:
		return "Duration Limit"
	case NotifyReasonMaxBudget:
		return "Budget Limit"
	case NotifyReasonCrash:
		return "Crashed"
	default:
		return "Wisp"
	}
}

// DefaultMessage returns a default notification message for the reason.
func (r NotificationReason) DefaultMessage(branch string) string {
	switch r {
	case NotifyReasonNeedsInput:
		return fmt.Sprintf("Session %s needs your input", branch)
	case NotifyReasonBlocked:
		return fmt.Sprintf("Session %s is blocked", branch)
	case NotifyReasonDone:
		return fmt.Sprintf("Session %s completed successfully", branch)
	case NotifyReasonStuck:
		return fmt.Sprintf("Session %s is stuck with no progress", branch)
	case NotifyReasonMaxIterations:
		return fmt.Sprintf("Session %s reached iteration limit", branch)
	case NotifyReasonMaxDuration:
		return fmt.Sprintf("Session %s exceeded time limit", branch)
	case NotifyReasonMaxBudget:
		return fmt.Sprintf("Session %s exceeded budget limit", branch)
	case NotifyReasonCrash:
		return fmt.Sprintf("Session %s crashed unexpectedly", branch)
	default:
		return fmt.Sprintf("Session %s needs attention", branch)
	}
}

// NotifyForReason sends a notification for the given reason.
// It uses the reason to determine the title and message.
func (n *Notifier) NotifyForReason(reason NotificationReason, branch string, isForeground bool) error {
	title := "wisp: " + reason.String()
	message := reason.DefaultMessage(branch)
	return n.NotifyAttention(title, message, isForeground)
}
