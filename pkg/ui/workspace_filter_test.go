package ui

import (
	"strings"
	"testing"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"

	tea "github.com/charmbracelet/bubbletea"
)

func requireWorkspaceFilterString(t *testing.T, name, got, want string) {
	t.Helper()
	if strings.Compare(got, want) != 0 {
		t.Fatalf("expected %s %q, got %q", name, want, got)
	}
}

func TestApplyFilterRespectsWorkspaceRepoFilter(t *testing.T) {
	issues := []model.Issue{
		{ID: "api-AUTH-1", Title: "API", Status: model.StatusOpen},
		{ID: "web-UI-1", Title: "Web", Status: model.StatusOpen},
	}

	m := NewModel(issues, nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = updated.(Model)

	m.EnableWorkspaceMode(WorkspaceInfo{
		Enabled:      true,
		RepoCount:    2,
		RepoPrefixes: []string{"api-", "web-"},
	})

	// Filter to api only
	m.activeRepos = map[string]bool{"api": true}
	m.applyFilter()

	if got := len(m.list.Items()); got != 1 {
		t.Fatalf("expected 1 visible item after repo filter, got %d", got)
	}
	item, ok := m.list.Items()[0].(IssueItem)
	if !ok {
		t.Fatalf("expected IssueItem")
	}
	requireWorkspaceFilterString(t, "api issue", item.Issue.ID, "api-AUTH-1")

	// Clear repo filter (nil = all repos)
	m.activeRepos = nil
	m.applyFilter()
	if got := len(m.list.Items()); got != 2 {
		t.Fatalf("expected 2 visible items with no repo filter, got %d", got)
	}
}

func TestApplyFilterRespectsHyphenatedWorkspaceRepoKey(t *testing.T) {
	issues := []model.Issue{
		{ID: "backend-service-AUTH-1", Title: "Backend", Status: model.StatusOpen, SourceRepo: "backend-service"},
		{ID: "web-app-UI-1", Title: "Web", Status: model.StatusOpen, SourceRepo: "web-app"},
	}

	m := NewModel(issues, nil, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = updated.(Model)

	m.EnableWorkspaceMode(WorkspaceInfo{
		Enabled:      true,
		RepoCount:    2,
		RepoPrefixes: []string{"backend-service-", "web-app-"},
	})

	m.activeRepos = map[string]bool{"backend-service": true}
	m.applyFilter()

	if got := len(m.list.Items()); got != 1 {
		t.Fatalf("expected 1 visible item after hyphenated repo filter, got %d", got)
	}
	item, ok := m.list.Items()[0].(IssueItem)
	if !ok {
		t.Fatalf("expected IssueItem")
	}
	requireWorkspaceFilterString(t, "backend-service issue", item.Issue.ID, "backend-service-AUTH-1")
	requireWorkspaceFilterString(t, "item repo prefix", item.RepoPrefix, "backend-service")
}
