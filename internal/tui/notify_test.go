package tui

import (
	"bytes"
	"testing"
)

func TestNotifier_Bell(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf)

	n.Bell()

	if buf.String() != Bell {
		t.Errorf("Bell() wrote %q, want %q", buf.String(), Bell)
	}
}

func TestNotifier_BellMultiple(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf)

	n.Bell()
	n.Bell()

	want := Bell + Bell
	if buf.String() != want {
		t.Errorf("Bell() twice wrote %q, want %q", buf.String(), want)
	}
}

func TestNotifier_NotifyAttention_Foreground(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf)

	err := n.NotifyAttention("Test Title", "Test Message", true)
	if err != nil {
		t.Errorf("NotifyAttention() returned error: %v", err)
	}

	// Foreground mode should ring bell
	if buf.String() != Bell {
		t.Errorf("NotifyAttention(foreground) wrote %q, want %q", buf.String(), Bell)
	}
}

func TestNotifier_NotifyAttention_Background(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf)

	// Background mode attempts OS notification but doesn't write bell
	// On non-darwin platforms, NotifyOS is a no-op
	_ = n.NotifyAttention("Test Title", "Test Message", false)

	// Should NOT ring bell in background mode
	if buf.String() != "" {
		t.Errorf("NotifyAttention(background) wrote %q, want empty", buf.String())
	}
}

func TestNotificationReason_String(t *testing.T) {
	tests := []struct {
		reason NotificationReason
		want   string
	}{
		{NotifyReasonNeedsInput, "Input Required"},
		{NotifyReasonBlocked, "Blocked"},
		{NotifyReasonDone, "Completed"},
		{NotifyReasonStuck, "Stuck"},
		{NotifyReasonMaxIterations, "Iteration Limit"},
		{NotifyReasonMaxDuration, "Duration Limit"},
		{NotifyReasonMaxBudget, "Budget Limit"},
		{NotifyReasonCrash, "Crashed"},
		{NotificationReason(99), "Wisp"}, // Unknown reason
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.reason.String(); got != tt.want {
				t.Errorf("NotificationReason(%d).String() = %q, want %q", tt.reason, got, tt.want)
			}
		})
	}
}

func TestNotificationReason_DefaultMessage(t *testing.T) {
	branch := "wisp/test-feature"

	tests := []struct {
		reason  NotificationReason
		wantSub string // Substring to check
	}{
		{NotifyReasonNeedsInput, "needs your input"},
		{NotifyReasonBlocked, "is blocked"},
		{NotifyReasonDone, "completed successfully"},
		{NotifyReasonStuck, "is stuck"},
		{NotifyReasonMaxIterations, "iteration limit"},
		{NotifyReasonMaxDuration, "time limit"},
		{NotifyReasonMaxBudget, "budget limit"},
		{NotifyReasonCrash, "crashed"},
		{NotificationReason(99), "needs attention"},
	}

	for _, tt := range tests {
		t.Run(tt.wantSub, func(t *testing.T) {
			got := tt.reason.DefaultMessage(branch)
			if !bytes.Contains([]byte(got), []byte(tt.wantSub)) {
				t.Errorf("DefaultMessage(%q) = %q, want substring %q", branch, got, tt.wantSub)
			}
			// Should also contain branch name
			if !bytes.Contains([]byte(got), []byte(branch)) {
				t.Errorf("DefaultMessage(%q) = %q, want to contain branch name", branch, got)
			}
		})
	}
}

func TestNotifier_NotifyForReason(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf)

	// Test foreground notification
	err := n.NotifyForReason(NotifyReasonDone, "wisp/feature", true)
	if err != nil {
		t.Errorf("NotifyForReason() returned error: %v", err)
	}

	// Should ring bell in foreground mode
	if buf.String() != Bell {
		t.Errorf("NotifyForReason(foreground) wrote %q, want %q", buf.String(), Bell)
	}
}

func TestNotifier_NotifyForReason_Background(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(&buf)

	// Test background notification
	// On non-darwin platforms, this is a no-op
	_ = n.NotifyForReason(NotifyReasonBlocked, "wisp/feature", false)

	// Should NOT ring bell in background mode
	if buf.String() != "" {
		t.Errorf("NotifyForReason(background) wrote %q, want empty", buf.String())
	}
}
