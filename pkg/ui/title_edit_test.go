package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTitleEditState_Defaults(t *testing.T) {
	s := TitleEditState{}
	if s.Active {
		t.Error("should not be active by default")
	}
	if s.Buffer != "" {
		t.Error("buffer should be empty")
	}
	if s.Cursor != 0 {
		t.Error("cursor should be 0")
	}
}

func TestRenderTitleEdit_Inactive(t *testing.T) {
	s := TitleEditState{}
	result := RenderTitleEdit(s, 80)
	if result != "" {
		t.Errorf("inactive should return empty, got %q", result)
	}
}

func TestRenderTitleEdit_CursorAtEnd(t *testing.T) {
	s := TitleEditState{Active: true, Buffer: "abc", Cursor: 3}
	result := RenderTitleEdit(s, 80)
	if result != "abc│" {
		t.Errorf("got %q, want %q", result, "abc│")
	}
}

func TestRenderTitleEdit_CursorInMiddle(t *testing.T) {
	s := TitleEditState{Active: true, Buffer: "abc", Cursor: 1}
	result := RenderTitleEdit(s, 80)
	if result != "a│bc" {
		t.Errorf("got %q, want %q", result, "a│bc")
	}
}

func TestRenderTitleEdit_CursorAtStart(t *testing.T) {
	s := TitleEditState{Active: true, Buffer: "abc", Cursor: 0}
	result := RenderTitleEdit(s, 80)
	if result != "│abc" {
		t.Errorf("got %q, want %q", result, "│abc")
	}
}

// Test key handling through the handleTitleEditKey helper.
// We test this indirectly by constructing a minimal Model with titleEditState set.

func makeTitleEditModel(buffer string, cursor int) Model {
	return Model{
		titleEditState: TitleEditState{
			Active:  true,
			Buffer:  buffer,
			Cursor:  cursor,
			IssueID: "BD-1",
		},
		editConfig: DefaultEditConfig(),
	}
}

func TestTitleEdit_Escape(t *testing.T) {
	m := makeTitleEditModel("hello", 3)
	m2, _, handled := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyEscape})
	if !handled {
		t.Error("should be handled")
	}
	if m2.titleEditState.Active {
		t.Error("should be inactive after escape")
	}
}

func TestTitleEdit_Backspace(t *testing.T) {
	m := makeTitleEditModel("hello", 3)
	m2, _, handled := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if !handled {
		t.Error("should be handled")
	}
	if m2.titleEditState.Buffer != "helo" {
		t.Errorf("buffer = %q, want %q", m2.titleEditState.Buffer, "helo")
	}
	if m2.titleEditState.Cursor != 2 {
		t.Errorf("cursor = %d, want 2", m2.titleEditState.Cursor)
	}
}

func TestTitleEdit_BackspaceAtStart(t *testing.T) {
	m := makeTitleEditModel("hello", 0)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if m2.titleEditState.Buffer != "hello" {
		t.Errorf("buffer should not change at start")
	}
	if m2.titleEditState.Cursor != 0 {
		t.Errorf("cursor should stay at 0")
	}
}

func TestTitleEdit_Delete(t *testing.T) {
	m := makeTitleEditModel("hello", 2)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyDelete})
	if m2.titleEditState.Buffer != "helo" {
		t.Errorf("buffer = %q, want %q", m2.titleEditState.Buffer, "helo")
	}
	if m2.titleEditState.Cursor != 2 {
		t.Errorf("cursor = %d, want 2", m2.titleEditState.Cursor)
	}
}

func TestTitleEdit_DeleteAtEnd(t *testing.T) {
	m := makeTitleEditModel("hello", 5)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyDelete})
	if m2.titleEditState.Buffer != "hello" {
		t.Errorf("buffer should not change at end")
	}
}

func TestTitleEdit_LeftRight(t *testing.T) {
	m := makeTitleEditModel("hello", 3)

	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyLeft})
	if m2.titleEditState.Cursor != 2 {
		t.Errorf("after left: cursor = %d, want 2", m2.titleEditState.Cursor)
	}

	m3, _, _ := m2.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyRight})
	if m3.titleEditState.Cursor != 3 {
		t.Errorf("after right: cursor = %d, want 3", m3.titleEditState.Cursor)
	}
}

func TestTitleEdit_LeftAtStart(t *testing.T) {
	m := makeTitleEditModel("hello", 0)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyLeft})
	if m2.titleEditState.Cursor != 0 {
		t.Errorf("cursor should stay at 0")
	}
}

func TestTitleEdit_RightAtEnd(t *testing.T) {
	m := makeTitleEditModel("hello", 5)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyRight})
	if m2.titleEditState.Cursor != 5 {
		t.Errorf("cursor should stay at end")
	}
}

func TestTitleEdit_Home(t *testing.T) {
	m := makeTitleEditModel("hello", 3)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyHome})
	if m2.titleEditState.Cursor != 0 {
		t.Errorf("cursor = %d, want 0", m2.titleEditState.Cursor)
	}
}

func TestTitleEdit_End(t *testing.T) {
	m := makeTitleEditModel("hello", 1)
	// KeyEnd is not a standard tea.KeyType, so test via ctrl+e
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyCtrlE})
	if m2.titleEditState.Cursor != 5 {
		t.Errorf("cursor = %d, want 5", m2.titleEditState.Cursor)
	}
}

func TestTitleEdit_CtrlK(t *testing.T) {
	m := makeTitleEditModel("hello world", 5)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyCtrlK})
	if m2.titleEditState.Buffer != "hello" {
		t.Errorf("buffer = %q, want %q", m2.titleEditState.Buffer, "hello")
	}
}

func TestTitleEdit_InsertRune(t *testing.T) {
	m := makeTitleEditModel("hllo", 1)
	m2, _, handled := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if !handled {
		t.Error("should be handled")
	}
	if m2.titleEditState.Buffer != "hello" {
		t.Errorf("buffer = %q, want %q", m2.titleEditState.Buffer, "hello")
	}
	if m2.titleEditState.Cursor != 2 {
		t.Errorf("cursor = %d, want 2", m2.titleEditState.Cursor)
	}
}

func TestTitleEdit_InsertAtEnd(t *testing.T) {
	m := makeTitleEditModel("hell", 4)
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if m2.titleEditState.Buffer != "hello" {
		t.Errorf("buffer = %q, want %q", m2.titleEditState.Buffer, "hello")
	}
	if m2.titleEditState.Cursor != 5 {
		t.Errorf("cursor = %d, want 5", m2.titleEditState.Cursor)
	}
}

func TestTitleEdit_Space(t *testing.T) {
	m := makeTitleEditModel("ab", 1)
	m2, _, handled := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if !handled {
		t.Error("should be handled")
	}
	if m2.titleEditState.Buffer != "a b" {
		t.Errorf("buffer = %q, want %q", m2.titleEditState.Buffer, "a b")
	}
	if m2.titleEditState.Cursor != 2 {
		t.Errorf("cursor = %d, want 2", m2.titleEditState.Cursor)
	}
}

func TestTitleEdit_Inactive(t *testing.T) {
	m := Model{
		titleEditState: TitleEditState{Active: false},
	}
	_, _, handled := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyEscape})
	if handled {
		t.Error("inactive state should not handle keys")
	}
}

func TestTitleEdit_UTF8(t *testing.T) {
	// Test with multi-byte characters
	m := makeTitleEditModel("héllo", 2) // cursor after é
	m2, _, _ := m.handleTitleEditKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if m2.titleEditState.Buffer != "hllo" {
		t.Errorf("buffer = %q, want %q", m2.titleEditState.Buffer, "hllo")
	}
	if m2.titleEditState.Cursor != 1 {
		t.Errorf("cursor = %d, want 1", m2.titleEditState.Cursor)
	}
}
