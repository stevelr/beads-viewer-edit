package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// editPickerKind identifies which field the picker is editing.
type editPickerKind int

const (
	editPickerPriority editPickerKind = iota
	editPickerStatus
	editPickerAssignee
)

// EditPickerResult tracks the modal state.
type EditPickerResult int

const (
	PickerPending   EditPickerResult = iota
	PickerAccepted                   // User pressed Enter
	PickerCancelled                  // User pressed Esc
)

// EditPickerModal is a generic list-selection modal for quick-edit fields.
type EditPickerModal struct {
	Title   string
	Items   []string
	Cursor  int
	Result  EditPickerResult
	IssueID string
}

// NewEditPickerModal creates a new picker modal.
func NewEditPickerModal(title string, items []string, cursor int, issueID string) EditPickerModal {
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(items) && len(items) > 0 {
		cursor = len(items) - 1
	}
	return EditPickerModal{
		Title:   title,
		Items:   items,
		Cursor:  cursor,
		Result:  PickerPending,
		IssueID: issueID,
	}
}

// Update handles key input for the picker modal.
func (p EditPickerModal) Update(msg tea.Msg) EditPickerModal {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p
	}
	switch keyMsg.String() {
	case "j", "down":
		if p.Cursor < len(p.Items)-1 {
			p.Cursor++
		}
	case "k", "up":
		if p.Cursor > 0 {
			p.Cursor--
		}
	case "enter":
		p.Result = PickerAccepted
	case "esc":
		p.Result = PickerCancelled
	case "0", "1", "2", "3", "4":
		// Digit shortcuts jump cursor to that index (if valid)
		idx := int(keyMsg.Runes[0] - '0')
		if idx >= 0 && idx < len(p.Items) {
			p.Cursor = idx
		}
	}
	return p
}

// View renders the picker modal as a centered overlay string.
func (p EditPickerModal) View(theme Theme, width, height int) string {
	r := theme.Renderer

	modalWidth := 30
	if modalWidth > width-4 {
		modalWidth = width - 4
	}
	if modalWidth < 10 {
		modalWidth = 10
	}

	// Build content
	var b strings.Builder
	titleStyle := r.NewStyle().Bold(true).Foreground(theme.Primary)
	b.WriteString(titleStyle.Render(p.Title))
	b.WriteString("\n")
	b.WriteString(r.NewStyle().Foreground(theme.Border).Render(strings.Repeat("─", modalWidth-4)))
	b.WriteString("\n")

	for i, item := range p.Items {
		marker := "  "
		if i == p.Cursor {
			marker = "▸ "
		}
		line := fmt.Sprintf("%s%s", marker, item)
		if i == p.Cursor {
			b.WriteString(r.NewStyle().Bold(true).Foreground(theme.Secondary).Render(line))
		} else {
			b.WriteString(r.NewStyle().Foreground(theme.Subtext).Render(line))
		}
		b.WriteString("\n")
	}

	footerStyle := r.NewStyle().Foreground(ColorFooterHint).Italic(true)
	b.WriteString(footerStyle.Render("j/k nav | Enter apply | Esc cancel"))

	// Wrap in modal box
	modalStyle := r.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Secondary).
		Padding(1, 2).
		Width(modalWidth)

	modal := modalStyle.Render(b.String())

	// Center within the available area
	modalH := lipgloss.Height(modal)
	padTop := (height - modalH) / 2
	if padTop < 0 {
		padTop = 0
	}
	padLeft := (width - lipgloss.Width(modal)) / 2
	if padLeft < 0 {
		padLeft = 0
	}

	return strings.Repeat("\n", padTop) + strings.Repeat(" ", padLeft) + modal
}
