package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestShortcutsSidebarComposedWidthFitsTerminal is a stronger companion to
// TestShortcutsSidebarReservesLayoutWidth. That test measures m.View(), whose
// final lipgloss clamp to m.width hides any over-wide composition inside a
// string buffer — exactly the failure mode (#168) where a real terminal wraps
// the overflow back into the panes. This test instead reconstructs the
// body+sidebar JoinHorizontal BEFORE the final clamp and asserts it fits within
// m.width, so it catches reservation-math drift (e.g. forgetting the sidebar's
// rendered border columns, or a body path that ignores mainContentWidth()).
func TestShortcutsSidebarComposedWidthFitsTerminal(t *testing.T) {
	maxLineWidth := func(s string) int {
		mx := 0
		for _, ln := range strings.Split(s, "\n") {
			if w := lipgloss.Width(ln); w > mx {
				mx = w
			}
		}
		return mx
	}

	// composeWidth rebuilds the same body+sidebar join View() performs, before
	// the final full-screen clamp, for the currently-focused list/detail body.
	composeWidth := func(m Model, showDetails bool) int {
		var body string
		if m.isSplitView {
			body = m.renderSplitView()
		} else if showDetails {
			body = m.viewport.View()
		} else {
			body = m.renderListWithHeader()
		}
		m.shortcutsSidebar.SetFocus(m.focused)
		m.shortcutsSidebar.SetSize(m.shortcutsSidebar.Width(), m.height-2)
		sidebar := m.shortcutsSidebar.View()
		return maxLineWidth(lipgloss.JoinHorizontal(lipgloss.Top, body, sidebar))
	}

	cases := []struct {
		name      string
		w, h      int
		wantSplit bool
	}{
		{"split_narrow_110", 110, 30, true},
		{"split_120", 120, 30, true},
		{"split_wide_200", 200, 40, true},
		{"mobile_80", 80, 30, false},
		{"mobile_60", 60, 24, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedModel(t, mouseTestIssues(40), tc.w, tc.h)
			if m.isSplitView != tc.wantSplit {
				t.Fatalf("w=%d isSplitView=%v want %v", tc.w, m.isSplitView, tc.wantSplit)
			}

			// Open the sidebar via the real `;` key path.
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(";")})
			m = updated.(Model)
			if !m.showShortcutsSidebar {
				t.Fatalf("`;` did not enable the shortcuts sidebar")
			}

			// List/board body and (mobile) detail body must both fit once the
			// sidebar column is appended.
			if cw := composeWidth(m, false); cw > m.width {
				t.Errorf("list body + sidebar composed width %d exceeds terminal width %d (#168 overflow)", cw, m.width)
			}
			if !m.isSplitView {
				if cw := composeWidth(m, true); cw > m.width {
					t.Errorf("detail body + sidebar composed width %d exceeds terminal width %d (#168 overflow)", cw, m.width)
				}
			}
		})
	}
}
