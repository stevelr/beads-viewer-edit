package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewEditPickerModal(t *testing.T) {
	items := []string{"A", "B", "C"}
	m := NewEditPickerModal("Test", items, 1, "BD-1")
	if m.Title != "Test" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Cursor != 1 {
		t.Errorf("Cursor = %d", m.Cursor)
	}
	if m.IssueID != "BD-1" {
		t.Errorf("IssueID = %q", m.IssueID)
	}
	if m.Result != PickerPending {
		t.Errorf("Result = %d", m.Result)
	}
}

func TestNewEditPickerModal_ClampsCursor(t *testing.T) {
	items := []string{"A", "B"}
	m := NewEditPickerModal("Test", items, 5, "BD-1")
	if m.Cursor != 1 {
		t.Errorf("Cursor = %d, want 1 (clamped)", m.Cursor)
	}

	m2 := NewEditPickerModal("Test", items, -1, "BD-1")
	if m2.Cursor != 0 {
		t.Errorf("Cursor = %d, want 0 (clamped)", m2.Cursor)
	}
}

func TestEditPickerModal_Navigation(t *testing.T) {
	items := []string{"A", "B", "C"}
	m := NewEditPickerModal("Test", items, 0, "BD-1")

	// Move down
	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.Cursor != 1 {
		t.Errorf("after j: Cursor = %d, want 1", m.Cursor)
	}

	// Move down again
	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.Cursor != 2 {
		t.Errorf("after j: Cursor = %d, want 2", m.Cursor)
	}

	// Move down at bottom (should stay)
	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.Cursor != 2 {
		t.Errorf("at bottom: Cursor = %d, want 2", m.Cursor)
	}

	// Move up
	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.Cursor != 1 {
		t.Errorf("after k: Cursor = %d, want 1", m.Cursor)
	}
}

func TestEditPickerModal_ArrowKeys(t *testing.T) {
	items := []string{"A", "B", "C"}
	m := NewEditPickerModal("Test", items, 0, "BD-1")

	m = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Cursor != 1 {
		t.Errorf("after down: Cursor = %d, want 1", m.Cursor)
	}

	m = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Cursor != 0 {
		t.Errorf("after up: Cursor = %d, want 0", m.Cursor)
	}

	// Up at top should stay
	m = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Cursor != 0 {
		t.Errorf("at top: Cursor = %d, want 0", m.Cursor)
	}
}

func TestEditPickerModal_Accept(t *testing.T) {
	items := []string{"A", "B"}
	m := NewEditPickerModal("Test", items, 1, "BD-1")

	m = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Result != PickerAccepted {
		t.Errorf("Result = %d, want PickerAccepted", m.Result)
	}
	if m.Cursor != 1 {
		t.Errorf("Cursor = %d, want 1", m.Cursor)
	}
}

func TestEditPickerModal_Cancel(t *testing.T) {
	items := []string{"A", "B"}
	m := NewEditPickerModal("Test", items, 0, "BD-1")

	m = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.Result != PickerCancelled {
		t.Errorf("Result = %d, want PickerCancelled", m.Result)
	}
}

func TestEditPickerModal_DigitShortcuts(t *testing.T) {
	items := []string{"P0  Critical", "P1  High", "P2  Medium", "P3  Low", "P4  Minimal"}
	m := NewEditPickerModal("Set Priority", items, 2, "BD-1")

	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0")})
	if m.Cursor != 0 {
		t.Errorf("after '0': Cursor = %d, want 0", m.Cursor)
	}

	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if m.Cursor != 4 {
		t.Errorf("after '4': Cursor = %d, want 4", m.Cursor)
	}

	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if m.Cursor != 3 {
		t.Errorf("after '3': Cursor = %d, want 3", m.Cursor)
	}

	// Result should still be pending (digits don't auto-confirm)
	if m.Result != PickerPending {
		t.Errorf("Result = %d, want PickerPending", m.Result)
	}
}

func TestEditPickerModal_DigitOutOfRange(t *testing.T) {
	items := []string{"A", "B"} // only 2 items
	m := NewEditPickerModal("Test", items, 0, "BD-1")

	m = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if m.Cursor != 0 {
		t.Errorf("out-of-range digit should not move cursor, got %d", m.Cursor)
	}
}

func TestEditPickerModal_IgnoresNonKeyMsg(t *testing.T) {
	items := []string{"A"}
	m := NewEditPickerModal("Test", items, 0, "BD-1")

	// Passing a non-key message should not change anything
	m = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.Cursor != 0 {
		t.Errorf("Cursor changed on non-key msg")
	}
	if m.Result != PickerPending {
		t.Errorf("Result changed on non-key msg")
	}
}

func TestEditPickerKindConstants(t *testing.T) {
	// Ensure the constants are distinct
	if editPickerPriority == editPickerStatus {
		t.Error("priority == status")
	}
	if editPickerStatus == editPickerAssignee {
		t.Error("status == assignee")
	}
}

func TestPickerResultConstants(t *testing.T) {
	if PickerPending == PickerAccepted {
		t.Error("pending == accepted")
	}
	if PickerAccepted == PickerCancelled {
		t.Error("accepted == cancelled")
	}
}
