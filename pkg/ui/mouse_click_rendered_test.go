package ui

import (
	"fmt"
	"strings"
	"testing"
)

// rowYInRenderedView renders the model's current list layout (split or mobile)
// and returns the 0-based terminal y at which the row whose ID contains marker
// appears. It returns -1 if the marker isn't found. This walks the SAME bytes
// the user sees, so any drift in the rendered chrome (filter bar, header wrap,
// border) is reflected here — unlike asserting against hard-coded offsets.
func rowYInRenderedView(t *testing.T, m Model, marker string) int {
	t.Helper()
	var view string
	if m.isSplitView {
		view = m.renderSplitView()
	} else {
		view = m.renderListWithHeader()
	}
	for i, ln := range strings.Split(view, "\n") {
		if strings.Contains(ln, marker) {
			return i
		}
	}
	return -1
}

// TestLeftClickMatchesRenderedRow is the regression test for bv-164: a
// left-click must select the row actually drawn under the cursor, at both wide
// and narrow widths, in both the split and the single-column (mobile) layout.
//
// It is deliberately driven through the REAL rendered View() (via
// rowYInRenderedView) rather than synthetic y offsets: the previous tests baked
// in the same wrong "first row at y=2 / y=1" assumption as the buggy handler and
// so never caught the off-by-one / off-by-two. Here, if the chrome geometry ever
// drifts again (e.g. the header re-wraps on a narrow pane, or the always-present
// filter bar changes height), this test fails because the click lands on the
// wrong rendered row.
func TestLeftClickMatchesRenderedRow(t *testing.T) {
	cases := []struct {
		name        string
		width       int
		height      int
		wantSplit   bool
		targetRowID string // a row guaranteed to be on the first page
	}{
		// Split view (width > SplitViewThreshold=100). w=110/120 previously
		// wrapped the column header (off-by-two); w=140/200 did not (off-by-one).
		{"split_narrow_110", 110, 30, true, "bv-002"},
		{"split_narrow_120", 120, 30, true, "bv-002"},
		{"split_wide_140", 140, 30, true, "bv-002"},
		{"split_wide_200", 200, 30, true, "bv-002"},
		// Mobile / single-column (width <= threshold). w=60 previously wrapped
		// the header (off-by-two); w=80 did not (off-by-one).
		{"mobile_narrow_60", 60, 30, false, "bv-002"},
		{"mobile_wide_80", 80, 30, false, "bv-002"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedModel(t, mouseTestIssues(30), tc.width, tc.height)
			if m.isSplitView != tc.wantSplit {
				t.Fatalf("width %d: isSplitView=%v, want %v", tc.width, m.isSplitView, tc.wantSplit)
			}
			if !tc.wantSplit {
				// Mobile click path only fires when the list (not the detail
				// viewport) is showing.
				m.showDetails = false
			}
			// Start the selection somewhere other than the target so a no-op
			// (selection unchanged) can't masquerade as success.
			m.list.Select(10)

			y := rowYInRenderedView(t, m, tc.targetRowID)
			if y < 0 {
				t.Fatalf("%s: target row %q not found in rendered view", tc.name, tc.targetRowID)
			}

			// Click at x inside the list panel, at the row's rendered y.
			updated := m.handleLeftClick(3, y)

			if updated.focused != focusList {
				t.Errorf("%s: after list click focused=%v, want focusList", tc.name, updated.focused)
			}
			// The target row id is "bv-002" -> absolute index 2 on page 0.
			wantIdx := 2
			if got := updated.list.Index(); got != wantIdx {
				t.Errorf("%s: clicked rendered row %q at y=%d -> selected index %d, want %d (chrome=%d)",
					tc.name, tc.targetRowID, y, got, wantIdx, m.listChromeLines())
			}
		})
	}
}

// TestListChromeLinesMatchesRenderedGeometry pins listChromeLines() to the
// actual rendered position of the first list row across the same wide/narrow,
// split/mobile matrix. If a renderer change makes the chrome taller or shorter
// (e.g. the header starts wrapping again), the helper and the rendered output
// disagree and this fails — keeping the single-source-of-truth honest.
func TestListChromeLinesMatchesRenderedGeometry(t *testing.T) {
	widths := []struct {
		w, h      int
		wantSplit bool
	}{
		{110, 30, true}, {120, 30, true}, {140, 30, true}, {200, 30, true},
		{60, 30, false}, {80, 30, false},
	}
	for _, c := range widths {
		c := c
		t.Run(fmt.Sprintf("w%d", c.w), func(t *testing.T) {
			m := sizedModel(t, mouseTestIssues(30), c.w, c.h)
			if m.isSplitView != c.wantSplit {
				t.Fatalf("w=%d isSplitView=%v want %v", c.w, m.isSplitView, c.wantSplit)
			}
			if !c.wantSplit {
				m.showDetails = false
			}
			m.list.Select(0)
			rowY := rowYInRenderedView(t, m, "bv-000")
			if rowY < 0 {
				t.Fatalf("w=%d: row bv-000 not found in rendered view", c.w)
			}
			if got := m.listChromeLines(); got != rowY {
				t.Errorf("w=%d: listChromeLines()=%d but first row rendered at y=%d", c.w, got, rowY)
			}
		})
	}
}
