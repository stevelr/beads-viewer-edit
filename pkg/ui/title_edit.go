package ui

import (
	"fmt"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// TitleEditState holds the state for inline title editing.
type TitleEditState struct {
	Active  bool
	Buffer  string
	Cursor  int
	IssueID string
}

// StartTitleEdit activates inline title editing for the selected issue.
func (m Model) startTitleEdit() (Model, tea.Cmd) {
	issue := m.getSelectedIssue()
	if issue == nil {
		m.statusMsg = "No issue selected"
		m.statusIsError = true
		return m, nil
	}
	m.titleEditState = TitleEditState{
		Active:  true,
		Buffer:  issue.Title,
		Cursor:  utf8.RuneCountInString(issue.Title),
		IssueID: issue.ID,
	}
	m.statusMsg = "Title: " + RenderTitleEdit(m.titleEditState, 0)
	m.statusIsError = false
	return m, nil
}

// handleTitleEditKey processes a key press during inline title editing.
// Returns (Model, tea.Cmd, handled). If handled is true, the caller should
// return immediately.
// fork: human-edit
func (m Model) handleTitleEditKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if !m.titleEditState.Active {
		return m, nil, false
	}

	runes := []rune(m.titleEditState.Buffer)
	cursor := m.titleEditState.Cursor

	switch msg.String() {
	case "esc":
		m.titleEditState = TitleEditState{}
		m.statusMsg = "Title edit cancelled"
		return m, nil, true

	case "enter":
		title := m.titleEditState.Buffer
		issueID := m.titleEditState.IssueID
		brPath := m.editConfig.BrPath
		m.titleEditState = TitleEditState{}
		m.statusMsg = fmt.Sprintf("Saving title for %s...", issueID)
		return m, func() tea.Msg {
			if err := SetTitle(brPath, issueID, title); err != nil {
				return editErrorMsg{err: err}
			}
			return editAppliedMsg{issueID: issueID, nFields: 1}
		}, true

	case "backspace":
		if cursor > 0 {
			runes = append(runes[:cursor-1], runes[cursor:]...)
			m.titleEditState.Buffer = string(runes)
			m.titleEditState.Cursor = cursor - 1
		}
		return m, nil, true

	case "delete":
		if cursor < len(runes) {
			runes = append(runes[:cursor], runes[cursor+1:]...)
			m.titleEditState.Buffer = string(runes)
		}
		return m, nil, true

	case "left":
		if cursor > 0 {
			m.titleEditState.Cursor = cursor - 1
		}
		return m, nil, true

	case "right":
		if cursor < len(runes) {
			m.titleEditState.Cursor = cursor + 1
		}
		return m, nil, true

	case "home", "ctrl+a":
		m.titleEditState.Cursor = 0
		return m, nil, true

	case "end", "ctrl+e":
		m.titleEditState.Cursor = len(runes)
		return m, nil, true

	case "ctrl+k":
		runes = runes[:cursor]
		m.titleEditState.Buffer = string(runes)
		return m, nil, true

	case " ":
		// Space is reported as tea.KeySpace, not tea.KeyRunes
		runes = insertRune(runes, cursor, ' ')
		cursor++
		m.titleEditState.Buffer = string(runes)
		m.titleEditState.Cursor = cursor
		return m, nil, true

	default:
		// Insert printable runes
		if msg.Type == tea.KeyRunes {
			for _, r := range msg.Runes {
				runes = insertRune(runes, cursor, r)
				cursor++
			}
			m.titleEditState.Buffer = string(runes)
			m.titleEditState.Cursor = cursor
			return m, nil, true
		}
	}

	return m, nil, true
}

// insertRune inserts a rune at position pos in the slice.
func insertRune(runes []rune, pos int, r rune) []rune {
	newRunes := make([]rune, len(runes)+1)
	copy(newRunes, runes[:pos])
	newRunes[pos] = r
	copy(newRunes[pos+1:], runes[pos:])
	return newRunes
}

// RenderTitleEdit renders the title edit field with a bar cursor.
func RenderTitleEdit(state TitleEditState, width int) string {
	if !state.Active {
		return ""
	}
	runes := []rune(state.Buffer)
	cursor := state.Cursor
	if cursor > len(runes) {
		cursor = len(runes)
	}

	before := string(runes[:cursor])
	after := string(runes[cursor:])
	return before + "│" + after
}
