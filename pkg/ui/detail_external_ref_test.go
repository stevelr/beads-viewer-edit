package ui

import (
	"strings"
	"testing"

	"github.com/Dicklesworthstone/beads_viewer/pkg/analysis"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// TestBuildDetailMarkdownShowsExternalRef verifies the detail panel surfaces a
// populated external_ref (issue #172), mirroring `br show`'s "Ref:" line.
func TestBuildDetailMarkdownShowsExternalRef(t *testing.T) {
	ref := "docs/specs/foo.md"
	issues := []model.Issue{
		{ID: "bv-1", Title: "With ref", Status: model.StatusOpen, ExternalRef: &ref},
		{ID: "bv-2", Title: "No ref", Status: model.StatusOpen},
		{ID: "bv-3", Title: "Empty ref", Status: model.StatusOpen, ExternalRef: new(string)},
	}
	issueMap := make(map[string]*model.Issue, len(issues))
	for i := range issues {
		issueMap[issues[i].ID] = &issues[i]
	}

	m := NewInsightsModel(analysis.Insights{}, issueMap, Theme{})

	withRef := m.buildDetailMarkdown("bv-1")
	if !strings.Contains(withRef, "External Ref") || !strings.Contains(withRef, ref) {
		t.Fatalf("detail for bv-1 should show external ref %q; got:\n%s", ref, withRef)
	}

	// Absent and empty-string refs must not render the row.
	if got := m.buildDetailMarkdown("bv-2"); strings.Contains(got, "External Ref") {
		t.Fatalf("detail for bv-2 (nil ref) should not show External Ref; got:\n%s", got)
	}
	if got := m.buildDetailMarkdown("bv-3"); strings.Contains(got, "External Ref") {
		t.Fatalf("detail for bv-3 (empty ref) should not show External Ref; got:\n%s", got)
	}
}
