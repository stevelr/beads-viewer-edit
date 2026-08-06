package analysis_test

// Regression tests for beads_viewer#158: bv's triage code path treated every
// dependency edge as a blocker, ignoring the `type` discriminator. In
// particular, an open parent-child rollup edge from a child to its parent
// epic was reported as a blocker of the child, producing inverted advice
// ("Work on <epic> first to unblock <epic>.1"). The canonical beads_rust
// (`br`) semantics — implemented in
// `beads_rust/src/storage/sqlite.rs::compute_blocked_issues_map_impl` —
// treat only `blocks`, `conditional-blocks`, and `waits-for` as direct
// predecessor edges; `parent-child` is a rollup, never a direct blocker.

import (
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/analysis"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// --- helpers --------------------------------------------------------------

func repro158Contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func repro158FindRec(t *testing.T, recs []analysis.Recommendation, id string) *analysis.Recommendation {
	t.Helper()
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}

// --- TriageContext.OpenBlockers ------------------------------------------

// Reproduces the original report: a child with ONLY a parent-child edge to
// an open parent must have an empty blocked_by set, matching `br ready`.
func TestRepro158_ParentChildNotBlocker(t *testing.T) {
	issues := []model.Issue{
		{ID: "epic-1", Status: model.StatusOpen, IssueType: model.TypeEpic},
		{
			ID:        "epic-1.1",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "epic-1.1", DependsOnID: "epic-1", Type: model.DepParentChild},
			},
		},
	}
	analyzer := analysis.NewAnalyzer(issues)
	ctx := analysis.NewTriageContext(analyzer)

	blockers := ctx.OpenBlockers("epic-1.1")
	if len(blockers) != 0 {
		t.Fatalf("expected 0 blockers for child with only parent-child edge, got %v", blockers)
	}
}

// Mixed edge set: a child has one parent-child edge and two real `blocks`
// edges. Only the real blockers must surface — exactly what `br ready`
// reports. This corresponds to the `pp-art-vision-2026-03f9.5.7` case in
// the bug report.
func TestRepro158_MixedEdges_OnlyBlocksSurface(t *testing.T) {
	issues := []model.Issue{
		{ID: "epic-1", Status: model.StatusOpen, IssueType: model.TypeEpic},
		{ID: "task-a", Status: model.StatusOpen, IssueType: model.TypeTask},
		{ID: "task-b", Status: model.StatusOpen, IssueType: model.TypeTask},
		{
			ID:        "epic-1.7",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				// Rollup edge — must NOT be a blocker.
				{IssueID: "epic-1.7", DependsOnID: "epic-1", Type: model.DepParentChild},
				// Real predecessor edges — MUST be blockers.
				{IssueID: "epic-1.7", DependsOnID: "task-a", Type: model.DepBlocks},
				{IssueID: "epic-1.7", DependsOnID: "task-b", Type: model.DepBlocks},
			},
		},
	}
	analyzer := analysis.NewAnalyzer(issues)
	ctx := analysis.NewTriageContext(analyzer)

	blockers := ctx.OpenBlockers("epic-1.7")
	if len(blockers) != 2 {
		t.Fatalf("expected exactly 2 blockers (the two `blocks` edges), got %d: %v", len(blockers), blockers)
	}
	if repro158Contains(blockers, "epic-1") {
		t.Errorf("parent-child parent must NOT appear in blocked_by, got %v", blockers)
	}
	if !repro158Contains(blockers, "task-a") || !repro158Contains(blockers, "task-b") {
		t.Errorf("real `blocks` predecessors missing from blocked_by, got %v", blockers)
	}
}

// `related` and `discovered-from` edges must also be excluded — they are
// associative, not gating.
func TestRepro158_NonBlockingEdgeTypesExcluded(t *testing.T) {
	cases := []struct {
		name    string
		depType model.DependencyType
	}{
		{"related", model.DepRelated},
		{"discovered-from", model.DepDiscoveredFrom},
		{"parent-child", model.DepParentChild},
	}
	for _, tc := range cases {
		t.Run(string(tc.depType), func(t *testing.T) {
			issues := []model.Issue{
				{ID: "X", Status: model.StatusOpen, IssueType: model.TypeTask},
				{
					ID:        "Y",
					Status:    model.StatusOpen,
					IssueType: model.TypeTask,
					Dependencies: []*model.Dependency{
						{IssueID: "Y", DependsOnID: "X", Type: tc.depType},
					},
				},
			}
			analyzer := analysis.NewAnalyzer(issues)
			ctx := analysis.NewTriageContext(analyzer)

			if got := ctx.OpenBlockers("Y"); len(got) != 0 {
				t.Errorf("non-blocking edge type %q must yield no blockers, got %v", tc.depType, got)
			}
		})
	}
}

// `blocks` and the empty-string legacy default MUST still produce blockers.
func TestRepro158_BlocksEdgesStillBlock(t *testing.T) {
	issues := []model.Issue{
		{ID: "X", Status: model.StatusOpen, IssueType: model.TypeTask},
		{ID: "Z", Status: model.StatusOpen, IssueType: model.TypeTask},
		{
			ID:        "Y",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "Y", DependsOnID: "X", Type: model.DepBlocks},
				// Empty Type is treated as blocking for legacy compatibility
				// (see DependencyType.IsBlocking()).
				{IssueID: "Y", DependsOnID: "Z", Type: ""},
			},
		},
	}
	analyzer := analysis.NewAnalyzer(issues)
	ctx := analysis.NewTriageContext(analyzer)

	blockers := ctx.OpenBlockers("Y")
	if len(blockers) != 2 {
		t.Fatalf("expected 2 blockers (blocks + legacy empty), got %d: %v", len(blockers), blockers)
	}
	if !repro158Contains(blockers, "X") || !repro158Contains(blockers, "Z") {
		t.Errorf("missing expected blockers, got %v", blockers)
	}
}

// Closed parent-child parents were silently filtered before; the same must
// remain true with the fix — closing the parent never changes the child's
// blocked_by (it was always empty for parent-child anyway).
func TestRepro158_ClosedParentNoEffect(t *testing.T) {
	issues := []model.Issue{
		{ID: "epic-1", Status: model.StatusClosed, IssueType: model.TypeEpic},
		{
			ID:        "epic-1.1",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "epic-1.1", DependsOnID: "epic-1", Type: model.DepParentChild},
			},
		},
	}
	analyzer := analysis.NewAnalyzer(issues)
	ctx := analysis.NewTriageContext(analyzer)

	if got := ctx.OpenBlockers("epic-1.1"); len(got) != 0 {
		t.Errorf("closed parent must yield no blockers, got %v", got)
	}
}

// --- UnblocksMap regression ----------------------------------------------

// The UnblocksMap inflated parent epics with every child in `unblocks_ids`
// before the fix, because each child reported the epic as its sole blocker.
// After the fix, an open parent with N children and no real predecessors
// must not show those children under `unblocks_ids`.
func TestRepro158_UnblocksMap_ParentDoesNotUnblockChildren(t *testing.T) {
	issues := []model.Issue{
		{ID: "epic-1", Status: model.StatusOpen, IssueType: model.TypeEpic},
		{
			ID:        "epic-1.1",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "epic-1.1", DependsOnID: "epic-1", Type: model.DepParentChild},
			},
		},
		{
			ID:        "epic-1.2",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "epic-1.2", DependsOnID: "epic-1", Type: model.DepParentChild},
			},
		},
	}
	analyzer := analysis.NewAnalyzer(issues)
	tctx := analysis.NewTriageContext(analyzer)

	unblocks := tctx.UnblocksMap()
	if got := unblocks["epic-1"]; len(got) != 0 {
		t.Errorf("epic-1 must not appear to unblock its children via parent-child, got %v", got)
	}
}

// A real blocker chain still produces the expected unblocks entry.
func TestRepro158_UnblocksMap_RealBlockerStillUnblocks(t *testing.T) {
	issues := []model.Issue{
		{ID: "X", Status: model.StatusOpen, IssueType: model.TypeTask},
		{
			ID:        "Y",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "Y", DependsOnID: "X", Type: model.DepBlocks},
			},
		},
	}
	analyzer := analysis.NewAnalyzer(issues)
	tctx := analysis.NewTriageContext(analyzer)

	unblocks := tctx.UnblocksMap()
	got := unblocks["X"]
	if len(got) != 1 || got[0] != "Y" {
		t.Errorf("expected X to unblock [Y], got %v", got)
	}
}

// --- End-to-end triage output (mirrors `--robot-triage`) -----------------

// Full triage pipeline assertion mirroring the bug report's expected output:
// a child whose only edge is a parent-child rollup gets no "complete X first"
// reason, no blocked_by entries, and no "Work on parent first to unblock"
// action hint.
func TestRepro158_TriageOutput_NoInvertedAdvice(t *testing.T) {
	issues := []model.Issue{
		{ID: "epic-1", Status: model.StatusOpen, IssueType: model.TypeEpic, Title: "parent epic"},
		{
			ID:        "epic-1.1",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Title:     "child task",
			Dependencies: []*model.Dependency{
				{IssueID: "epic-1.1", DependsOnID: "epic-1", Type: model.DepParentChild},
			},
		},
	}
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	result := analysis.ComputeTriageWithOptionsAndTime(issues, analysis.TriageOptions{}, now)

	rec := repro158FindRec(t, result.Recommendations, "epic-1.1")
	if rec == nil {
		t.Fatalf("epic-1.1 missing from triage recommendations: %+v", result.Recommendations)
	}
	if len(rec.BlockedBy) != 0 {
		t.Errorf("epic-1.1.BlockedBy = %v, want empty (parent-child is not a blocker)", rec.BlockedBy)
	}
	for _, reason := range rec.Reasons {
		if strings.Contains(reason, "Blocked by") {
			t.Errorf("unexpected blocked-by reason for child with only parent-child edge: %q", reason)
		}
	}
	if strings.Contains(rec.Action, "first to unblock") {
		t.Errorf("inverted action hint surfaced: %q", rec.Action)
	}
}

// Golden-ish assertion for the mixed-edge case: only real blockers should
// surface in `BlockedBy` and the action hint should point at one of them,
// never at the parent epic.
func TestRepro158_TriageOutput_MixedEdges(t *testing.T) {
	issues := []model.Issue{
		{ID: "epic-1", Status: model.StatusOpen, IssueType: model.TypeEpic},
		{ID: "task-a", Status: model.StatusOpen, IssueType: model.TypeTask},
		{ID: "task-b", Status: model.StatusOpen, IssueType: model.TypeTask},
		{
			ID:        "epic-1.7",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "epic-1.7", DependsOnID: "epic-1", Type: model.DepParentChild},
				{IssueID: "epic-1.7", DependsOnID: "task-a", Type: model.DepBlocks},
				{IssueID: "epic-1.7", DependsOnID: "task-b", Type: model.DepBlocks},
			},
		},
	}
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	result := analysis.ComputeTriageWithOptionsAndTime(issues, analysis.TriageOptions{}, now)

	rec := repro158FindRec(t, result.Recommendations, "epic-1.7")
	if rec == nil {
		t.Fatalf("epic-1.7 missing from triage recommendations")
	}
	if len(rec.BlockedBy) != 2 {
		t.Errorf("epic-1.7.BlockedBy = %v, want 2 entries", rec.BlockedBy)
	}
	if repro158Contains(rec.BlockedBy, "epic-1") {
		t.Errorf("epic-1 parent must not appear in BlockedBy: %v", rec.BlockedBy)
	}
	if !repro158Contains(rec.BlockedBy, "task-a") || !repro158Contains(rec.BlockedBy, "task-b") {
		t.Errorf("real blockers missing from BlockedBy: %v", rec.BlockedBy)
	}
	if strings.Contains(rec.Action, "epic-1 first") {
		t.Errorf("action hint points at parent epic, not real blocker: %q", rec.Action)
	}
}

// Sanity check that the standalone parent epic is not itself reported as
// blocked when it has no predecessors of any kind.
func TestRepro158_OpenParentEpicNotBlocked(t *testing.T) {
	issues := []model.Issue{
		{ID: "epic-1", Status: model.StatusOpen, IssueType: model.TypeEpic},
		{
			ID:        "epic-1.1",
			Status:    model.StatusOpen,
			IssueType: model.TypeTask,
			Dependencies: []*model.Dependency{
				{IssueID: "epic-1.1", DependsOnID: "epic-1", Type: model.DepParentChild},
			},
		},
	}
	analyzer := analysis.NewAnalyzer(issues)
	tctx := analysis.NewTriageContext(analyzer)

	if got := tctx.OpenBlockers("epic-1"); len(got) != 0 {
		t.Errorf("standalone parent epic must not be reported blocked, got %v", got)
	}

	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	result := analysis.ComputeTriageWithOptionsAndTime(issues, analysis.TriageOptions{}, now)
	rec := repro158FindRec(t, result.Recommendations, "epic-1")
	if rec == nil {
		t.Fatalf("epic-1 missing from recommendations")
	}
	if len(rec.BlockedBy) != 0 {
		t.Errorf("epic-1.BlockedBy = %v, want empty", rec.BlockedBy)
	}
}
