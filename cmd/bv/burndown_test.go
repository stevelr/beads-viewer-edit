package main

import (
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/analysis"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

func TestCalculateBurndownAt_OnTrackWithProgress(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 4) // inclusive = 5 days
	now := start.AddDate(0, 0, 1) // day 2

	closedAt := start.Add(12 * time.Hour)
	issues := []model.Issue{
		{ID: "A", Title: "Done", Status: model.StatusClosed, Priority: 1, IssueType: model.TypeTask, ClosedAt: &closedAt},
		{ID: "B", Title: "Remaining", Status: model.StatusOpen, Priority: 1, IssueType: model.TypeTask},
	}

	sprint := &model.Sprint{
		ID:        "sprint-1",
		Name:      "Sprint 1",
		StartDate: start,
		EndDate:   end,
		BeadIDs:   []string{"A", "B"},
	}

	out := calculateBurndownAt(sprint, issues, now)

	if out.TotalDays != 5 {
		t.Fatalf("TotalDays=%d; want 5", out.TotalDays)
	}
	if out.ElapsedDays != 2 {
		t.Fatalf("ElapsedDays=%d; want 2", out.ElapsedDays)
	}
	if out.RemainingDays != 3 {
		t.Fatalf("RemainingDays=%d; want 3", out.RemainingDays)
	}

	if out.TotalIssues != 2 || out.CompletedIssues != 1 || out.RemainingIssues != 1 {
		t.Fatalf("issues totals mismatch: total=%d completed=%d remaining=%d", out.TotalIssues, out.CompletedIssues, out.RemainingIssues)
	}

	if out.ProjectedComplete == nil {
		t.Fatalf("ProjectedComplete is nil; want non-nil")
	}
	wantProjected := now.AddDate(0, 0, 3) // see calculateBurndownAt: int(daysToComplete)+1
	if !out.ProjectedComplete.Equal(wantProjected) {
		t.Fatalf("ProjectedComplete=%s; want %s", out.ProjectedComplete.UTC().Format(time.RFC3339), wantProjected.Format(time.RFC3339))
	}
	if !out.OnTrack {
		t.Fatalf("OnTrack=false; want true")
	}

	if got, want := len(out.DailyPoints), out.ElapsedDays; got != want {
		t.Fatalf("DailyPoints=%d; want %d", got, want)
	}
	if got, want := len(out.IdealLine), out.TotalDays+1; got != want {
		t.Fatalf("IdealLine=%d; want %d", got, want)
	}
}

func TestCalculateBurndownAt_NoProgressSetsOnTrackFalse(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 4)
	now := start.AddDate(0, 0, 2) // day 3

	issues := []model.Issue{
		{ID: "A", Title: "Open 1", Status: model.StatusOpen, Priority: 1, IssueType: model.TypeTask},
		{ID: "B", Title: "Open 2", Status: model.StatusOpen, Priority: 1, IssueType: model.TypeTask},
	}

	sprint := &model.Sprint{
		ID:        "sprint-1",
		Name:      "Sprint 1",
		StartDate: start,
		EndDate:   end,
		BeadIDs:   []string{"A", "B"},
	}

	out := calculateBurndownAt(sprint, issues, now)

	if out.ElapsedDays <= 0 {
		t.Fatalf("ElapsedDays=%d; want >0", out.ElapsedDays)
	}
	if out.CompletedIssues != 0 {
		t.Fatalf("CompletedIssues=%d; want 0", out.CompletedIssues)
	}
	if out.ProjectedComplete != nil {
		t.Fatalf("ProjectedComplete=%v; want nil", out.ProjectedComplete)
	}
	if out.OnTrack {
		t.Fatalf("OnTrack=true; want false")
	}
}

func TestGenerateIdealLineScoped_MidSprintAdditionChangesSlope(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	sprint := &model.Sprint{ID: "s", Name: "S", StartDate: start, EndDate: start.AddDate(0, 0, 4)} // 5 sprint days
	events := []ScopeChangeEvent{{Date: start.AddDate(0, 0, 2), IssueID: "X", IssueTitle: "Late add", Action: "added"}}

	got := generateIdealLineScoped(sprint, 6, events)

	// Day 0 starts from the original scope (5, not 6); the addition lands on
	// day 2 and the line re-linearizes from 4 to zero over the remaining days.
	want := []int{5, 4, 4, 3, 2, 0}
	if len(got) != len(want) {
		t.Fatalf("got %d points, want %d: %+v", len(got), len(want), got)
	}
	for i, p := range got {
		if p.Remaining != want[i] {
			t.Fatalf("point %d remaining=%d, want %d (all: %+v)", i, p.Remaining, want[i], got)
		}
		if wantDate := start.AddDate(0, 0, i); !p.Date.Equal(wantDate) {
			t.Fatalf("point %d date=%s, want %s", i, p.Date, wantDate)
		}
	}
	if got[2].Remaining <= got[1].Remaining-1 {
		t.Fatalf("expected the day-2 addition to interrupt the descent: %+v", got)
	}
}

func TestGenerateIdealLineScoped_NoOrOutOfWindowEventsMatchUnscoped(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	sprint := &model.Sprint{ID: "s", Name: "S", StartDate: start, EndDate: start.AddDate(0, 0, 6)}
	plain := generateIdealLine(sprint, 9)
	if len(plain) == 0 {
		t.Fatalf("generateIdealLine returned no points")
	}

	cases := map[string][]ScopeChangeEvent{
		"nil events":      nil,
		"before start":    {{Date: start.AddDate(0, 0, -3), IssueID: "A", Action: "added"}},
		"after end":       {{Date: start.AddDate(0, 0, 30), IssueID: "B", Action: "removed"}},
		"unknown action":  {{Date: start.AddDate(0, 0, 2), IssueID: "C", Action: "renamed"}},
		"net zero change": {{Date: start.AddDate(0, 0, 2), IssueID: "D", Action: "added"}, {Date: start.AddDate(0, 0, 2), IssueID: "E", Action: "removed"}},
	}
	for name, events := range cases {
		got := generateIdealLineScoped(sprint, 9, events)
		if len(got) != len(plain) {
			t.Fatalf("%s: got %d points, want %d", name, len(got), len(plain))
		}
		for i := range plain {
			if got[i].Remaining != plain[i].Remaining || !got[i].Date.Equal(plain[i].Date) {
				t.Fatalf("%s: point %d = %+v, want %+v", name, i, got[i], plain[i])
			}
		}
	}

	if got := generateIdealLineScoped(&model.Sprint{ID: "nodates"}, 3, cases["before start"]); got != nil {
		t.Fatalf("sprint without dates should yield nil, got %+v", got)
	}
}

func TestCalculateBurndownAt_IncludesAtRiskItems(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.AddDate(0, 0, 5)
	issues := []model.Issue{
		{ID: "A", Title: "Stuck", Status: model.StatusBlocked, Priority: 2, IssueType: model.TypeTask, UpdatedAt: start},
		{ID: "B", Title: "Moving", Status: model.StatusInProgress, Priority: 2, IssueType: model.TypeTask, UpdatedAt: now.Add(-time.Hour)},
		{ID: "C", Title: "Stuck but not in sprint", Status: model.StatusBlocked, Priority: 0, IssueType: model.TypeTask, UpdatedAt: start},
	}
	sprint := &model.Sprint{ID: "sprint-1", Name: "Sprint 1", StartDate: start, EndDate: start.AddDate(0, 0, 9), BeadIDs: []string{"A", "B"}}

	out := calculateBurndownAt(sprint, issues, now)

	if len(out.AtRisk) != 1 || out.AtRisk[0].ID != "A" {
		t.Fatalf("AtRisk=%+v, want only A", out.AtRisk)
	}
	hasSignal := func(s analysis.AtRiskSignal) bool {
		for _, got := range out.AtRisk[0].Signals {
			if got == s {
				return true
			}
		}
		return false
	}
	if !hasSignal(analysis.AtRiskBlockedTooLong) || !hasSignal(analysis.AtRiskNoActivity) {
		t.Fatalf("A signals=%v, want blocked_too_long and no_activity", out.AtRisk[0].Signals)
	}
	if hasSignal(analysis.AtRiskCriticalBlocked) {
		t.Fatalf("P2 bead must not be critical_blocked: %v", out.AtRisk[0].Signals)
	}
}
