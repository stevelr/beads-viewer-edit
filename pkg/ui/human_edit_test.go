package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// --- Config Tests ---

func TestDefaultEditConfig(t *testing.T) {
	cfg := DefaultEditConfig()
	if cfg.BrPath != "br" {
		t.Errorf("BrPath = %q, want %q", cfg.BrPath, "br")
	}
	if cfg.EditorPath == "" {
		t.Error("EditorPath should not be empty")
	}
	if cfg.Hotkeys.EditPriority != "ctrl+p" {
		t.Errorf("EditPriority = %q, want %q", cfg.Hotkeys.EditPriority, "ctrl+p")
	}
	if cfg.Hotkeys.OpenEditor != "O" {
		t.Errorf("OpenEditor = %q, want %q", cfg.Hotkeys.OpenEditor, "O")
	}
	if cfg.Hotkeys.CreateIssue != "ctrl+n" {
		t.Errorf("CreateIssue = %q, want %q", cfg.Hotkeys.CreateIssue, "ctrl+n")
	}
	if cfg.Hotkeys.CreateSubIssue != "ctrl+g" {
		t.Errorf("CreateSubIssue = %q, want %q", cfg.Hotkeys.CreateSubIssue, "ctrl+g")
	}
}

func TestLoadEditConfig_Defaults(t *testing.T) {
	// When no config file exists, should return defaults
	// Change to a temp dir where no bv-edit.yaml exists
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	cfg := LoadEditConfig()
	if cfg.BrPath != "br" {
		t.Errorf("BrPath = %q, want %q", cfg.BrPath, "br")
	}
	if cfg.Hotkeys.EditStatus != "ctrl+o" {
		t.Errorf("EditStatus = %q, want %q", cfg.Hotkeys.EditStatus, "ctrl+o")
	}
}

func TestLoadEditConfig_FromFile(t *testing.T) {
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	configYAML := `br_path: "/usr/local/bin/br"
editor_path: "/usr/local/bin/hx"
extra_assignees:
  - alice
  - bob
hotkeys:
  edit_priority: "ctrl+1"
  edit_status: "ctrl+2"
`
	if err := os.WriteFile(filepath.Join(tmp, "bv-edit.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadEditConfig()
	if cfg.BrPath != "/usr/local/bin/br" {
		t.Errorf("BrPath = %q, want %q", cfg.BrPath, "/usr/local/bin/br")
	}
	if cfg.EditorPath != "/usr/local/bin/hx" {
		t.Errorf("EditorPath = %q, want %q", cfg.EditorPath, "/usr/local/bin/hx")
	}
	if cfg.Hotkeys.EditPriority != "ctrl+1" {
		t.Errorf("EditPriority = %q, want %q", cfg.Hotkeys.EditPriority, "ctrl+1")
	}
	if cfg.Hotkeys.EditStatus != "ctrl+2" {
		t.Errorf("EditStatus = %q, want %q", cfg.Hotkeys.EditStatus, "ctrl+2")
	}
	// Unset hotkeys should fall back to defaults
	if cfg.Hotkeys.EditAssignee != "ctrl+a" {
		t.Errorf("EditAssignee = %q, want %q", cfg.Hotkeys.EditAssignee, "ctrl+a")
	}
	if cfg.Hotkeys.OpenEditor != "O" {
		t.Errorf("OpenEditor = %q, want %q", cfg.Hotkeys.OpenEditor, "O")
	}
	if len(cfg.ExtraAssignees) != 2 {
		t.Errorf("ExtraAssignees len = %d, want 2", len(cfg.ExtraAssignees))
	}
}

// --- Snapshot Tests ---

func TestSnapshotFromIssue(t *testing.T) {
	issue := &model.Issue{
		ID:                 "BD-123",
		Title:              "Test issue",
		Description:        "A description",
		Design:             "Design doc",
		AcceptanceCriteria: "Criteria",
		Notes:              "Some notes",
		Status:             model.StatusOpen,
		Priority:           2,
		Assignee:           "alice",
		Labels:             []string{"bug", "ux"},
		SourceRepo:         "my-repo",
	}
	snap := SnapshotFromIssue(issue)
	if snap.ID != "BD-123" {
		t.Errorf("ID = %q", snap.ID)
	}
	if snap.Title != "Test issue" {
		t.Errorf("Title = %q", snap.Title)
	}
	if snap.Status != "open" {
		t.Errorf("Status = %q", snap.Status)
	}
	if snap.Priority != 2 {
		t.Errorf("Priority = %d", snap.Priority)
	}
	if len(snap.Labels) != 2 || snap.Labels[0] != "bug" {
		t.Errorf("Labels = %v", snap.Labels)
	}
	// Ensure labels are a copy
	issue.Labels[0] = "modified"
	if snap.Labels[0] != "bug" {
		t.Error("Labels were not deep-copied")
	}
}

func TestSnapshotFromBrJSON_Object(t *testing.T) {
	json := `{"id":"BD-1","title":"Test","status":"open","priority":1,"assignee":"bob","labels":["a","b"]}`
	snap, err := SnapshotFromBrJSON(json)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID != "BD-1" {
		t.Errorf("ID = %q", snap.ID)
	}
	if snap.Priority != 1 {
		t.Errorf("Priority = %d", snap.Priority)
	}
	if len(snap.Labels) != 2 {
		t.Errorf("Labels = %v", snap.Labels)
	}
}

func TestSnapshotFromBrJSON_Array(t *testing.T) {
	json := `[{"id":"BD-2","title":"Array Test","status":"closed","priority":3}]`
	snap, err := SnapshotFromBrJSON(json)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID != "BD-2" {
		t.Errorf("ID = %q", snap.ID)
	}
	if snap.Status != "closed" {
		t.Errorf("Status = %q", snap.Status)
	}
}

func TestSnapshotFromBrJSON_Empty(t *testing.T) {
	_, err := SnapshotFromBrJSON("[]")
	if err == nil {
		t.Error("expected error for empty array")
	}
}

func TestSnapshotFromBrJSON_Invalid(t *testing.T) {
	_, err := SnapshotFromBrJSON("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- Markdown Roundtrip Tests ---

func TestSnapshotToMarkdown(t *testing.T) {
	snap := IssueSnapshot{
		ID:                 "BD-42",
		Title:              "Fix the widget",
		Status:             "open",
		Priority:           2,
		Assignee:           "alice",
		Labels:             []string{"bug", "ux"},
		Description:        "The widget is broken.",
		Design:             "Replace the cog.",
		AcceptanceCriteria: "Widget works.",
		Notes:              "See also BD-456.",
		SourceRepo:         "my-repo",
	}
	md := SnapshotToMarkdown(snap)

	// Check frontmatter
	if !strings.HasPrefix(md, "---\n") {
		t.Error("missing opening ---")
	}
	if !strings.Contains(md, "id: BD-42") {
		t.Error("missing id field")
	}
	if !strings.Contains(md, "priority: 2") {
		t.Error("missing priority field")
	}
	if !strings.Contains(md, "labels: bug, ux") {
		t.Error("missing labels field")
	}
	if !strings.Contains(md, "repo: my-repo") {
		t.Error("missing repo field")
	}

	// Check body sections use XML-style tags
	if !strings.Contains(md, "<description>") {
		t.Error("missing <description> tag")
	}
	if !strings.Contains(md, "</description>") {
		t.Error("missing </description> tag")
	}
	if !strings.Contains(md, "The widget is broken.") {
		t.Error("missing description content")
	}
	if !strings.Contains(md, "<notes>") {
		t.Error("missing <notes> tag")
	}
	if !strings.Contains(md, "</notes>") {
		t.Error("missing </notes> tag")
	}
}

func TestSnapshotToMarkdown_NoRepo(t *testing.T) {
	snap := IssueSnapshot{
		ID:       "BD-1",
		Title:    "Simple",
		Status:   "open",
		Priority: 0,
	}
	md := SnapshotToMarkdown(snap)
	if strings.Contains(md, "repo:") {
		t.Error("repo field should be omitted when empty")
	}
}

func TestSnapshotToMarkdown_SpecialCharsInTitle(t *testing.T) {
	snap := IssueSnapshot{
		ID:       "BD-1",
		Title:    `Fix the widget: a [tricky] one`,
		Status:   "open",
		Priority: 0,
	}
	md := SnapshotToMarkdown(snap)
	// Should be YAML-safe
	parsed, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if parsed.Title != snap.Title {
		t.Errorf("roundtrip title = %q, want %q", parsed.Title, snap.Title)
	}
}

func TestMarkdownToSnapshot(t *testing.T) {
	md := `---
id: BD-42
title: Fix the widget
status: open
priority: 2
assignee: alice
labels: bug, ux
repo: my-repo
---

<description>
The widget is broken.
</description>

<design>
Replace the cog.
</design>

<acceptance_criteria>
Widget works.
</acceptance_criteria>

<notes>
See also BD-456.
</notes>
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID != "BD-42" {
		t.Errorf("ID = %q", snap.ID)
	}
	if snap.Title != "Fix the widget" {
		t.Errorf("Title = %q", snap.Title)
	}
	if snap.Status != "open" {
		t.Errorf("Status = %q", snap.Status)
	}
	if snap.Priority != 2 {
		t.Errorf("Priority = %d", snap.Priority)
	}
	if snap.Assignee != "alice" {
		t.Errorf("Assignee = %q", snap.Assignee)
	}
	if len(snap.Labels) != 2 || snap.Labels[0] != "bug" || snap.Labels[1] != "ux" {
		t.Errorf("Labels = %v", snap.Labels)
	}
	if snap.SourceRepo != "my-repo" {
		t.Errorf("SourceRepo = %q", snap.SourceRepo)
	}
	if snap.Description != "The widget is broken." {
		t.Errorf("Description = %q", snap.Description)
	}
	if snap.Design != "Replace the cog." {
		t.Errorf("Design = %q", snap.Design)
	}
	if snap.AcceptanceCriteria != "Widget works." {
		t.Errorf("AcceptanceCriteria = %q", snap.AcceptanceCriteria)
	}
	if snap.Notes != "See also BD-456." {
		t.Errorf("Notes = %q", snap.Notes)
	}
}

func TestMarkdownToSnapshot_MarkdownHeadingsInContent(t *testing.T) {
	md := `---
id: BD-1
title: Test
---

<description>
Some intro text.

## Root Cause

The cog is broken.

# Architecture Note

This affects the core.
</description>

<acceptance_criteria>
Works fine.
</acceptance_criteria>
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	if snap.AcceptanceCriteria != "Works fine." {
		t.Errorf("AcceptanceCriteria = %q", snap.AcceptanceCriteria)
	}
	// Markdown headings inside <description> must be preserved
	if !strings.Contains(snap.Description, "## Root Cause") {
		t.Errorf("Description lost ## heading, got: %q", snap.Description)
	}
	if !strings.Contains(snap.Description, "# Architecture Note") {
		t.Errorf("Description lost # heading, got: %q", snap.Description)
	}
	if !strings.Contains(snap.Description, "The cog is broken.") {
		t.Errorf("Description lost content after heading, got: %q", snap.Description)
	}
}

func TestMarkdownToSnapshot_MissingFrontmatter(t *testing.T) {
	_, err := MarkdownToSnapshot("no frontmatter here")
	if err == nil {
		t.Error("expected error for missing frontmatter")
	}
}

func TestMarkdownToSnapshot_MissingClosing(t *testing.T) {
	_, err := MarkdownToSnapshot("---\nid: BD-1\ntitle: Test\n")
	if err == nil {
		t.Error("expected error for missing closing ---")
	}
}

func TestMarkdownRoundtrip(t *testing.T) {
	original := IssueSnapshot{
		ID:                 "BD-99",
		Title:              "Full roundtrip test",
		Status:             "in_progress",
		Priority:           3,
		Assignee:           "bob",
		Labels:             []string{"feature", "backend"},
		Description:        "Multi-line\ndescription\nhere.",
		Design:             "",
		AcceptanceCriteria: "Tests pass.",
		Notes:              "Note 1\nNote 2",
		SourceRepo:         "core",
	}

	md := SnapshotToMarkdown(original)
	parsed, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatalf("roundtrip parse: %v", err)
	}

	if parsed.ID != original.ID {
		t.Errorf("ID: %q != %q", parsed.ID, original.ID)
	}
	if parsed.Title != original.Title {
		t.Errorf("Title: %q != %q", parsed.Title, original.Title)
	}
	if parsed.Status != original.Status {
		t.Errorf("Status: %q != %q", parsed.Status, original.Status)
	}
	if parsed.Priority != original.Priority {
		t.Errorf("Priority: %d != %d", parsed.Priority, original.Priority)
	}
	if parsed.Assignee != original.Assignee {
		t.Errorf("Assignee: %q != %q", parsed.Assignee, original.Assignee)
	}
	if !stringSliceEqual(parsed.Labels, original.Labels) {
		t.Errorf("Labels: %v != %v", parsed.Labels, original.Labels)
	}
	if strings.TrimSpace(parsed.Description) != strings.TrimSpace(original.Description) {
		t.Errorf("Description: %q != %q", parsed.Description, original.Description)
	}
	if strings.TrimSpace(parsed.Design) != strings.TrimSpace(original.Design) {
		t.Errorf("Design: %q != %q", parsed.Design, original.Design)
	}
	if strings.TrimSpace(parsed.AcceptanceCriteria) != strings.TrimSpace(original.AcceptanceCriteria) {
		t.Errorf("AcceptanceCriteria: %q != %q", parsed.AcceptanceCriteria, original.AcceptanceCriteria)
	}
	if strings.TrimSpace(parsed.Notes) != strings.TrimSpace(original.Notes) {
		t.Errorf("Notes: %q != %q", parsed.Notes, original.Notes)
	}
	if parsed.SourceRepo != original.SourceRepo {
		t.Errorf("SourceRepo: %q != %q", parsed.SourceRepo, original.SourceRepo)
	}
}

func TestMarkdownRoundtrip_EmptySections(t *testing.T) {
	original := IssueSnapshot{
		ID:       "BD-1",
		Title:    "Empty sections",
		Status:   "open",
		Priority: 0,
	}
	md := SnapshotToMarkdown(original)
	parsed, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatalf("roundtrip parse: %v", err)
	}
	if parsed.Description != "" {
		t.Errorf("Description should be empty, got %q", parsed.Description)
	}
}

// --- Diff Tests ---

func TestDiffSnapshots_NoChanges(t *testing.T) {
	s := IssueSnapshot{
		ID: "BD-1", Title: "Same", Status: "open", Priority: 2,
		Assignee: "alice", Labels: []string{"a"},
	}
	d := DiffSnapshots(s, s)
	if !d.IsEmpty() {
		t.Error("diff should be empty for identical snapshots")
	}
	if d.FieldCount() != 0 {
		t.Errorf("FieldCount = %d, want 0", d.FieldCount())
	}
}

func TestDiffSnapshots_AllChanged(t *testing.T) {
	orig := IssueSnapshot{
		ID: "BD-1", Title: "Old", Status: "open", Priority: 2,
		Assignee: "alice", Labels: []string{"a"}, Description: "old desc",
		Design: "old design", AcceptanceCriteria: "old ac", Notes: "old notes",
		SourceRepo: "repo1",
	}
	edited := IssueSnapshot{
		ID: "BD-1", Title: "New", Status: "closed", Priority: 0,
		Assignee: "bob", Labels: []string{"b", "c"}, Description: "new desc",
		Design: "new design", AcceptanceCriteria: "new ac", Notes: "new notes",
		SourceRepo: "repo2",
	}
	d := DiffSnapshots(orig, edited)
	if d.IsEmpty() {
		t.Error("diff should not be empty")
	}
	if d.FieldCount() != 10 {
		t.Errorf("FieldCount = %d, want 10", d.FieldCount())
	}
	if d.Title == nil || *d.Title != "New" {
		t.Errorf("Title diff = %v", d.Title)
	}
	if d.Priority == nil || *d.Priority != 0 {
		t.Errorf("Priority diff = %v", d.Priority)
	}
	if d.Labels == nil || len(d.Labels) != 2 {
		t.Errorf("Labels diff = %v", d.Labels)
	}
}

func TestDiffSnapshots_WhitespaceTrimming(t *testing.T) {
	orig := IssueSnapshot{
		ID: "BD-1", Title: "  Same  ", Description: "  desc  ",
	}
	edited := IssueSnapshot{
		ID: "BD-1", Title: "Same", Description: "desc",
	}
	d := DiffSnapshots(orig, edited)
	if d.Title != nil {
		t.Error("Title should be nil (whitespace-only difference)")
	}
	if d.Description != nil {
		t.Error("Description should be nil (whitespace-only difference)")
	}
}

func TestDiffSnapshots_PartialChange(t *testing.T) {
	orig := IssueSnapshot{
		ID: "BD-1", Title: "Same", Status: "open", Priority: 2,
	}
	edited := IssueSnapshot{
		ID: "BD-1", Title: "Same", Status: "closed", Priority: 2,
	}
	d := DiffSnapshots(orig, edited)
	if d.FieldCount() != 1 {
		t.Errorf("FieldCount = %d, want 1", d.FieldCount())
	}
	if d.Status == nil {
		t.Error("Status should be changed")
	}
	if d.Title != nil {
		t.Error("Title should be nil")
	}
}

// --- BuildUpdateArgv Tests ---

func TestBuildUpdateArgv_AllFields(t *testing.T) {
	title := "New Title"
	desc := "New Desc"
	design := "New Design"
	ac := "New AC"
	notes := "New Notes"
	status := "closed"
	priority := 1
	assignee := "bob"
	d := &IssueDiff{
		Title:              &title,
		Description:        &desc,
		Design:             &design,
		AcceptanceCriteria: &ac,
		Notes:              &notes,
		Status:             &status,
		Priority:           &priority,
		Assignee:           &assignee,
		Labels:             []string{"bug", "ux"},
	}
	argv := BuildUpdateArgv("br", "BD-1", d)

	if argv[0] != "br" || argv[1] != "update" || argv[2] != "BD-1" || argv[3] != "--no-auto-import" {
		t.Errorf("prefix = %v", argv[:4])
	}

	// Check that all fields are present
	joined := strings.Join(argv, " ")
	for _, expected := range []string{
		"--title=New Title",
		"--description=New Desc",
		"--design=New Design",
		"--acceptance-criteria=New AC",
		"--notes=New Notes",
		"--status=closed",
		"--priority=1",
		"--assignee=bob",
		"--set-labels=bug",
		"--set-labels=ux",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing %q in argv: %s", expected, joined)
		}
	}
}

func TestBuildUpdateArgv_Empty(t *testing.T) {
	d := &IssueDiff{}
	argv := BuildUpdateArgv("br", "BD-1", d)
	// br update BD-1 --no-auto-import
	if len(argv) != 4 {
		t.Errorf("empty diff should produce 4 args, got %d: %v", len(argv), argv)
	}
}

func TestBuildUpdateArgv_PriorityOnly(t *testing.T) {
	p := 4
	d := &IssueDiff{Priority: &p}
	argv := BuildUpdateArgv("br", "BD-1", d)
	// br update BD-1 --no-auto-import --priority=4
	if len(argv) != 5 {
		t.Errorf("expected 5 args, got %d: %v", len(argv), argv)
	}
	if argv[4] != "--priority=4" {
		t.Errorf("priority arg = %q", argv[4])
	}
}

// --- Assignee Collection Tests ---

func TestCollectAssignees(t *testing.T) {
	issues := []model.Issue{
		{Assignee: "bob"},
		{Assignee: "alice"},
		{Assignee: "bob"}, // duplicate
		{Assignee: ""},    // empty
		{Assignee: "  "},  // whitespace
	}
	result := CollectAssignees(issues, []string{"charlie", "alice"})

	if len(result) != 3 {
		t.Errorf("len = %d, want 3, got %v", len(result), result)
	}
	// Should be sorted
	if result[0] != "alice" || result[1] != "bob" || result[2] != "charlie" {
		t.Errorf("result = %v", result)
	}
}

func TestCollectAssignees_Empty(t *testing.T) {
	result := CollectAssignees(nil, nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

// --- Parse Labels Tests ---

func TestParseLabels(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"bug, ux", []string{"bug", "ux"}},
		{"single", []string{"single"}},
		{"", nil},
		{" , , ", nil},
		{" a , b , c ", []string{"a", "b", "c"}},
	}
	for _, tc := range tests {
		result := parseLabels(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("parseLabels(%q) = %v, want %v", tc.input, result, tc.expected)
			continue
		}
		for i := range result {
			if result[i] != tc.expected[i] {
				t.Errorf("parseLabels(%q)[%d] = %q, want %q", tc.input, i, result[i], tc.expected[i])
			}
		}
	}
}

// --- Section Tag Parsing Edge Cases ---

func TestParseSections_AngleBracketsInContent(t *testing.T) {
	// < and > inside a known tag pair should be treated as content
	md := `---
id: BD-1
title: Test
---

<description>
Use x < 10 and y > 5 to filter.
The <b>bold</b> tag is just HTML.
Also check <unknown_tag> stuff </unknown_tag>.
</description>
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Description, "x < 10") {
		t.Errorf("lost < in content: %q", snap.Description)
	}
	if !strings.Contains(snap.Description, "y > 5") {
		t.Errorf("lost > in content: %q", snap.Description)
	}
	if !strings.Contains(snap.Description, "<b>bold</b>") {
		t.Errorf("lost HTML tags in content: %q", snap.Description)
	}
	if !strings.Contains(snap.Description, "<unknown_tag>") {
		t.Errorf("lost unknown tags in content: %q", snap.Description)
	}
}

func TestParseSections_AngleBracketsOutsideSection(t *testing.T) {
	// < and > between sections should be ignored, not start/end a section
	md := `---
id: BD-1
title: Test
---

<not_a_known_tag>
this should be ignored
</not_a_known_tag>

<description>
Real description.
</description>
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Description != "Real description." {
		t.Errorf("Description = %q", snap.Description)
	}
}

func TestParseSections_NestedKnownTagNamesInContent(t *testing.T) {
	// A line like "<notes>" inside <description> is just content (it's inside
	// a section, so only the matching </description> closes it).
	md := `---
id: BD-1
title: Test
---

<description>
See the <notes> section for more.
Also </design> is just text here.
</description>

<notes>
Actual notes.
</notes>
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Description, "<notes>") {
		t.Errorf("Description should contain literal <notes>: %q", snap.Description)
	}
	if !strings.Contains(snap.Description, "</design>") {
		t.Errorf("Description should contain literal </design>: %q", snap.Description)
	}
	if snap.Notes != "Actual notes." {
		t.Errorf("Notes = %q", snap.Notes)
	}
}

func TestParseSections_ClosingTagOnlyMatchesCurrent(t *testing.T) {
	// </notes> should NOT close <description>
	md := `---
id: BD-1
title: Test
---

<description>
Line 1
</notes>
Line 2
</description>
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Line 1\n</notes>\nLine 2"
	if snap.Description != expected {
		t.Errorf("Description = %q, want %q", snap.Description, expected)
	}
}

func TestParseSections_MissingClosingTag(t *testing.T) {
	// If the file ends without a closing tag, content is still captured
	md := `---
id: BD-1
title: Test
---

<description>
Some content without closing tag.
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Description != "Some content without closing tag." {
		t.Errorf("Description = %q", snap.Description)
	}
}

func TestRoundtrip_ContentWithMarkdownAndHTML(t *testing.T) {
	original := IssueSnapshot{
		ID:       "BD-1",
		Title:    "Complex content",
		Status:   "open",
		Priority: 2,
		Description: `# Big Heading

## Sub Heading

Some text with <b>HTML</b> and x < 10 && y > 5.

### Another heading

More content.`,
		Design: `Use the <Widget> component.

Check if count > 0.`,
		Notes: `See <notes> about the <description> tag.`,
	}

	md := SnapshotToMarkdown(original)
	parsed, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatalf("roundtrip parse: %v", err)
	}

	if strings.TrimSpace(parsed.Description) != strings.TrimSpace(original.Description) {
		t.Errorf("Description roundtrip failed.\nGot:  %q\nWant: %q", parsed.Description, original.Description)
	}
	if strings.TrimSpace(parsed.Design) != strings.TrimSpace(original.Design) {
		t.Errorf("Design roundtrip failed.\nGot:  %q\nWant: %q", parsed.Design, original.Design)
	}
	if strings.TrimSpace(parsed.Notes) != strings.TrimSpace(original.Notes) {
		t.Errorf("Notes roundtrip failed.\nGot:  %q\nWant: %q", parsed.Notes, original.Notes)
	}
}

func TestParseSections_IndentedClosingTags(t *testing.T) {
	// Markdown LSP formatters may indent closing tags when there is content.
	// The parser must handle this via TrimSpace.
	md := `---
id: BD-1
title: Test
---

<description>
Some content here.
    </description>

<notes>
A note.

	</notes>
`
	snap, err := MarkdownToSnapshot(md)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Description != "Some content here." {
		t.Errorf("Description = %q, want %q", snap.Description, "Some content here.")
	}
	if snap.Notes != "A note." {
		t.Errorf("Notes = %q, want %q", snap.Notes, "A note.")
	}
}

func TestKnownSectionTags(t *testing.T) {
	expected := []string{"description", "design", "acceptance_criteria", "notes"}
	for _, tag := range expected {
		if !knownSectionTags[tag] {
			t.Errorf("knownSectionTags missing %q", tag)
		}
	}
	if knownSectionTags["unknown"] {
		t.Error("knownSectionTags should not contain 'unknown'")
	}
}

// --- Editor Detection Tests ---

func TestIsTerminalEditor(t *testing.T) {
	tests := []struct {
		editor   string
		expected bool
	}{
		{"hx", true},
		{"vim", true},
		{"nvim", true},
		{"vi", true},
		{"nano", true},
		{"emacs", true},
		{"pico", true},
		{"joe", true},
		{"ne", true},
		{"code", false},
		{"gedit", false},
		{"", false},
		{"/usr/bin/hx", true},
		{"/usr/local/bin/nvim", true},
	}
	for _, tc := range tests {
		result := IsTerminalEditor(tc.editor)
		if result != tc.expected {
			t.Errorf("IsTerminalEditor(%q) = %v, want %v", tc.editor, result, tc.expected)
		}
	}
}

// --- YAML Escape Tests ---

func TestYamlEscapeTitle(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"Simple title"},
		{"Title with: colon"},
		{"Title with [brackets]"},
		{`Title with "quotes"`},
		{"Title with 'quotes'"},
		{"- starts with dash"},
		{" leading space"},
	}
	for _, tc := range tests {
		escaped := yamlEscapeTitle(tc.input)
		// Verify it roundtrips through YAML
		snap := IssueSnapshot{ID: "BD-1", Title: tc.input, Status: "open"}
		md := SnapshotToMarkdown(snap)
		parsed, err := MarkdownToSnapshot(md)
		if err != nil {
			t.Errorf("roundtrip failed for %q (escaped=%q): %v", tc.input, escaped, err)
			continue
		}
		if parsed.Title != tc.input {
			t.Errorf("roundtrip title %q != %q (escaped=%q)", parsed.Title, tc.input, escaped)
		}
	}
}

// --- Known Constants Tests ---

func TestKnownStatuses(t *testing.T) {
	if len(KnownStatuses) != 9 {
		t.Errorf("KnownStatuses len = %d, want 9", len(KnownStatuses))
	}
	if KnownStatuses[0] != "open" {
		t.Errorf("first status = %q", KnownStatuses[0])
	}
	if KnownStatuses[8] != "tombstone" {
		t.Errorf("last status = %q", KnownStatuses[8])
	}
}

func TestPriorityLabels(t *testing.T) {
	if len(PriorityLabels) != 5 {
		t.Errorf("PriorityLabels len = %d, want 5", len(PriorityLabels))
	}
	if !strings.HasPrefix(PriorityLabels[0], "P0") {
		t.Errorf("first label = %q", PriorityLabels[0])
	}
}

// --- SaveFailedBuffer Tests ---

func TestSaveFailedBuffer(t *testing.T) {
	path := SaveFailedBuffer("TEST-123", "some content")
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "some content" {
		t.Errorf("content = %q", string(data))
	}
	if path != "/tmp/bv-failed-TEST-123.md" {
		t.Errorf("path = %q", path)
	}
}

// --- StringSliceEqual Tests ---

// --- extractBrErrorMessage Tests ---

func TestExtractBrErrorMessage_ValidJSON(t *testing.T) {
	raw := `{
    "error": {
        "code": "DATABASE_ERROR",
        "message": "Database error: UNIQUE constraint failed: export_hashes.issue_id",
        "hint": null,
        "retryable": false,
        "context": null
    }
}`
	result := extractBrErrorMessage(raw)
	expected := "Database error: UNIQUE constraint failed: export_hashes.issue_id"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractBrErrorMessage_PlainText(t *testing.T) {
	raw := "something went wrong"
	result := extractBrErrorMessage(raw)
	if result != raw {
		t.Errorf("got %q, want %q", result, raw)
	}
}

func TestExtractBrErrorMessage_EmptyMessage(t *testing.T) {
	raw := `{"error": {"code": "UNKNOWN", "message": ""}}`
	result := extractBrErrorMessage(raw)
	// Empty message field → fall back to raw
	if result != raw {
		t.Errorf("got %q, want %q", result, raw)
	}
}

func TestExtractBrErrorMessage_MalformedJSON(t *testing.T) {
	raw := `{"error": {broken`
	result := extractBrErrorMessage(raw)
	if result != raw {
		t.Errorf("got %q, want %q", result, raw)
	}
}

// --- AddComment Config Tests ---

func TestDefaultEditConfig_AddComment(t *testing.T) {
	cfg := DefaultEditConfig()
	if cfg.Hotkeys.AddComment != "ctrl+x" {
		t.Errorf("AddComment = %q, want %q", cfg.Hotkeys.AddComment, "ctrl+x")
	}
}

func TestStringSliceEqual(t *testing.T) {
	if !stringSliceEqual(nil, nil) {
		t.Error("nil == nil should be true")
	}
	if !stringSliceEqual([]string{}, []string{}) {
		t.Error("empty == empty should be true")
	}
	if !stringSliceEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("same should be true")
	}
	if stringSliceEqual([]string{"a"}, []string{"b"}) {
		t.Error("different should be false")
	}
	if stringSliceEqual([]string{"a"}, []string{"a", "b"}) {
		t.Error("different length should be false")
	}
}
