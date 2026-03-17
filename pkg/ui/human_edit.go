package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

// --- Configuration ---

// EditConfig holds the configuration for the human-edit system.
type EditConfig struct {
	BrPath         string       `yaml:"br_path"`
	EditorPath     string       `yaml:"editor_path"`
	WeztermCommand string       `yaml:"wezterm_command"`
	Hotkeys        HotkeyConfig `yaml:"hotkeys"`
	ExtraAssignees []string     `yaml:"extra_assignees"`
}

// HotkeyConfig holds the configurable hotkey bindings for edit operations.
type HotkeyConfig struct {
	EditPriority   string `yaml:"edit_priority"`
	EditStatus     string `yaml:"edit_status"`
	EditAssignee   string `yaml:"edit_assignee"`
	OpenEditor     string `yaml:"open_editor"`
	EditTitle      string `yaml:"edit_title"`
	CreateIssue    string `yaml:"create_issue"`
	CreateSubIssue string `yaml:"create_sub_issue"`
	AddComment     string `yaml:"add_comment"`
}

// DefaultEditConfig returns a config with sensible defaults.
// EditorPath defaults to $EDITOR, then $VISUAL, then "vi".
func DefaultEditConfig() EditConfig {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}
	return EditConfig{
		BrPath:         "br",
		EditorPath:     editor,
		WeztermCommand: "wezterm cli split-pane --bottom --",
		Hotkeys: HotkeyConfig{
			EditPriority:   "ctrl+p",
			EditStatus:     "ctrl+o",
			EditAssignee:   "ctrl+a",
			OpenEditor:     "O",
			EditTitle:      "ctrl+t",
			CreateIssue:    "ctrl+n",
			CreateSubIssue: "ctrl+g",
			AddComment:     "ctrl+x",
		},
	}
}

// LoadEditConfig loads from ./bv-edit.yaml or ~/.config/bv-edit.yaml. Falls
// back to defaults if neither exists.
func LoadEditConfig() EditConfig {
	cfg := DefaultEditConfig()
	paths := []string{"bv-edit.yaml"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "bv-edit.yaml"))
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			continue
		}
		// Fill in any blank hotkeys with defaults
		def := DefaultEditConfig().Hotkeys
		if cfg.Hotkeys.EditPriority == "" {
			cfg.Hotkeys.EditPriority = def.EditPriority
		}
		if cfg.Hotkeys.EditStatus == "" {
			cfg.Hotkeys.EditStatus = def.EditStatus
		}
		if cfg.Hotkeys.EditAssignee == "" {
			cfg.Hotkeys.EditAssignee = def.EditAssignee
		}
		if cfg.Hotkeys.OpenEditor == "" {
			cfg.Hotkeys.OpenEditor = def.OpenEditor
		}
		if cfg.Hotkeys.EditTitle == "" {
			cfg.Hotkeys.EditTitle = def.EditTitle
		}
		if cfg.Hotkeys.CreateIssue == "" {
			cfg.Hotkeys.CreateIssue = def.CreateIssue
		}
		if cfg.Hotkeys.CreateSubIssue == "" {
			cfg.Hotkeys.CreateSubIssue = def.CreateSubIssue
		}
		if cfg.Hotkeys.AddComment == "" {
			cfg.Hotkeys.AddComment = def.AddComment
		}
		if cfg.BrPath == "" {
			cfg.BrPath = "br"
		}
		if cfg.EditorPath == "" {
			cfg.EditorPath = DefaultEditConfig().EditorPath
		}
		return cfg
	}
	return cfg
}

// --- Issue Snapshot ---

// IssueSnapshot is a lightweight struct holding all editable fields for an issue.
type IssueSnapshot struct {
	ID                 string
	Title              string
	Description        string
	Design             string
	AcceptanceCriteria string
	Notes              string
	Status             string
	Priority           int
	Assignee           string
	Labels             []string
	SourceRepo         string
}

// SnapshotFromIssue builds a snapshot from an in-memory issue.
func SnapshotFromIssue(issue *model.Issue) IssueSnapshot {
	labels := make([]string, len(issue.Labels))
	copy(labels, issue.Labels)
	return IssueSnapshot{
		ID:                 issue.ID,
		Title:              issue.Title,
		Description:        issue.Description,
		Design:             issue.Design,
		AcceptanceCriteria: issue.AcceptanceCriteria,
		Notes:              issue.Notes,
		Status:             string(issue.Status),
		Priority:           issue.Priority,
		Assignee:           issue.Assignee,
		Labels:             labels,
		SourceRepo:         issue.SourceRepo,
	}
}

// brShowJSON is the struct used to parse `br show --json` output.
type brShowJSON struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Design             string   `json:"design"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Notes              string   `json:"notes"`
	Status             string   `json:"status"`
	Priority           int      `json:"priority"`
	Assignee           string   `json:"assignee"`
	Labels             []string `json:"labels"`
	SourceRepo         string   `json:"source_repo"`
}

// SnapshotFromBrJSON parses the JSON output of `br show ID --json`.
// Accepts either a JSON object or a single-element array.
func SnapshotFromBrJSON(jsonStr string) (IssueSnapshot, error) {
	trimmedJSON := strings.TrimSpace(jsonStr)
	var items []brShowJSON

	if strings.HasPrefix(trimmedJSON, "[") {
		if err := jsonUnmarshal([]byte(trimmedJSON), &items); err != nil {
			return IssueSnapshot{}, fmt.Errorf("parse br JSON array: %w", err)
		}
	} else {
		var item brShowJSON
		if err := jsonUnmarshal([]byte(trimmedJSON), &item); err != nil {
			return IssueSnapshot{}, fmt.Errorf("parse br JSON object: %w", err)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return IssueSnapshot{}, fmt.Errorf("empty br JSON output")
	}
	it := items[0]
	return IssueSnapshot{
		ID:                 it.ID,
		Title:              it.Title,
		Description:        it.Description,
		Design:             it.Design,
		AcceptanceCriteria: it.AcceptanceCriteria,
		Notes:              it.Notes,
		Status:             it.Status,
		Priority:           it.Priority,
		Assignee:           it.Assignee,
		Labels:             it.Labels,
		SourceRepo:         it.SourceRepo,
	}, nil
}

// --- Markdown Serialization ---

// SnapshotToMarkdown serializes an IssueSnapshot to the markdown editing format.
func SnapshotToMarkdown(s IssueSnapshot) string {
	var b strings.Builder

	// YAML frontmatter
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("id: %s\n", s.ID))
	b.WriteString(fmt.Sprintf("title: %s\n", yamlEscapeTitle(s.Title)))
	b.WriteString(fmt.Sprintf("status: %s\n", s.Status))
	b.WriteString(fmt.Sprintf("priority: %d\n", s.Priority))
	b.WriteString(fmt.Sprintf("assignee: %s\n", s.Assignee))
	b.WriteString(fmt.Sprintf("labels: %s\n", strings.Join(s.Labels, ", ")))
	if s.SourceRepo != "" {
		b.WriteString(fmt.Sprintf("repo: %s\n", s.SourceRepo))
	}
	b.WriteString("---\n")

	// Body sections — XML-style tags so that markdown headings inside
	// field content are not confused with section delimiters.
	writeSection(&b, "description", s.Description)
	writeSection(&b, "design", s.Design)
	writeSection(&b, "acceptance_criteria", s.AcceptanceCriteria)
	writeSection(&b, "notes", s.Notes)

	return b.String()
}

func writeSection(b *strings.Builder, tag, content string) {
	b.WriteString(fmt.Sprintf("\n<%s>\n", tag))
	trimmed := strings.TrimSpace(content)
	if trimmed != "" {
		b.WriteString(trimmed)
		b.WriteString("\n")
	}
	// Blank line before closing tag prevents markdown LSP formatters from
	// indenting the closing tag when format-on-save is enabled.
	b.WriteString(fmt.Sprintf("\n</%s>\n", tag))
}

// yamlEscapeTitle wraps the title in quotes if it contains YAML-special characters.
func yamlEscapeTitle(title string) string {
	if strings.ContainsAny(title, ":{}[]|>&*!#%@`,?\"'\\") ||
		strings.HasPrefix(title, "-") ||
		strings.HasPrefix(title, " ") ||
		strings.HasSuffix(title, " ") {
		// Use yaml.Marshal for safe escaping, then trim the trailing newline
		out, err := yaml.Marshal(title)
		if err != nil {
			return fmt.Sprintf("%q", title)
		}
		return strings.TrimSpace(string(out))
	}
	return title
}

// frontmatterData is used for parsing the YAML frontmatter.
type frontmatterData struct {
	ID       string  `yaml:"id"`
	Title    string  `yaml:"title"`
	Status   *string `yaml:"status"`
	Priority *int    `yaml:"priority"`
	Assignee *string `yaml:"assignee"`
	Labels   *string `yaml:"labels"`
	Repo     *string `yaml:"repo"`
}

// MarkdownToSnapshot parses a markdown editing file back to an IssueSnapshot.
func MarkdownToSnapshot(md string) (IssueSnapshot, error) {
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return IssueSnapshot{}, fmt.Errorf("missing opening --- in frontmatter")
	}

	// Find closing ---
	closingIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closingIdx = i
			break
		}
	}
	if closingIdx < 0 {
		return IssueSnapshot{}, fmt.Errorf("missing closing --- in frontmatter")
	}

	// Parse YAML frontmatter
	yamlStr := strings.Join(lines[1:closingIdx], "\n")
	var fm frontmatterData
	if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
		return IssueSnapshot{}, fmt.Errorf("parse frontmatter YAML: %w", err)
	}

	snap := IssueSnapshot{
		ID:    fm.ID,
		Title: fm.Title,
	}
	if fm.Status != nil {
		snap.Status = *fm.Status
	}
	if fm.Priority != nil {
		snap.Priority = *fm.Priority
	}
	if fm.Assignee != nil {
		snap.Assignee = *fm.Assignee
	}
	if fm.Labels != nil {
		snap.Labels = parseLabels(*fm.Labels)
	}
	if fm.Repo != nil {
		snap.SourceRepo = *fm.Repo
	}

	// Parse body sections
	body := lines[closingIdx+1:]
	sections := parseSections(body)
	if v, ok := sections["description"]; ok {
		snap.Description = v
	}
	if v, ok := sections["design"]; ok {
		snap.Design = v
	}
	if v, ok := sections["acceptance_criteria"]; ok {
		snap.AcceptanceCriteria = v
	}
	if v, ok := sections["notes"]; ok {
		snap.Notes = v
	}

	return snap, nil
}

// parseLabels splits a comma-separated labels string.
func parseLabels(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// knownSectionTags is the set of tag names that parseSections recognises as
// section delimiters.  Any `<foo>` or `</foo>` where foo is NOT in this set is
// treated as literal content, so `<` and `>` in user markdown pass through
// safely.
var knownSectionTags = map[string]bool{
	"description":         true,
	"design":              true,
	"acceptance_criteria": true,
	"notes":               true,
}

// parseSections extracts <tag>...</tag> sections from the markdown body.
// Only tags listed in knownSectionTags are treated as delimiters; all other
// lines (including lines containing `<` or `>`) are treated as content.
func parseSections(lines []string) map[string]string {
	sections := make(map[string]string)
	var currentKey string
	var currentLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for closing tag first (while inside a section).
		if currentKey != "" {
			if trimmed == "</"+currentKey+">" {
				sections[currentKey] = strings.TrimSpace(strings.Join(currentLines, "\n"))
				currentKey = ""
				currentLines = nil
				continue
			}
			currentLines = append(currentLines, line)
			continue
		}

		// Outside a section: look for an opening tag.
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") &&
			!strings.HasPrefix(trimmed, "</") {
			tag := trimmed[1 : len(trimmed)-1]
			if knownSectionTags[tag] {
				currentKey = tag
				currentLines = nil
				continue
			}
		}
		// Anything else outside a section is ignored (inter-section whitespace, etc.)
	}

	// If the file ends without a closing tag, capture what we have.
	if currentKey != "" {
		sections[currentKey] = strings.TrimSpace(strings.Join(currentLines, "\n"))
	}

	return sections
}

// --- Diffing ---

// IssueDiff holds the changed fields between two snapshots.
// nil means unchanged.
type IssueDiff struct {
	Title              *string
	Description        *string
	Design             *string
	AcceptanceCriteria *string
	Notes              *string
	Status             *string
	Priority           *int
	Assignee           *string
	Labels             []string // nil = unchanged, non-nil = full replacement
	SourceRepo         *string
}

// IsEmpty returns true if no fields changed.
func (d *IssueDiff) IsEmpty() bool {
	return d.Title == nil && d.Description == nil && d.Design == nil &&
		d.AcceptanceCriteria == nil && d.Notes == nil && d.Status == nil &&
		d.Priority == nil && d.Assignee == nil && d.Labels == nil &&
		d.SourceRepo == nil
}

// FieldCount returns the number of changed fields.
func (d *IssueDiff) FieldCount() int {
	n := 0
	if d.Title != nil {
		n++
	}
	if d.Description != nil {
		n++
	}
	if d.Design != nil {
		n++
	}
	if d.AcceptanceCriteria != nil {
		n++
	}
	if d.Notes != nil {
		n++
	}
	if d.Status != nil {
		n++
	}
	if d.Priority != nil {
		n++
	}
	if d.Assignee != nil {
		n++
	}
	if d.Labels != nil {
		n++
	}
	if d.SourceRepo != nil {
		n++
	}
	return n
}

// DiffSnapshots compares two snapshots and returns the differences.
func DiffSnapshots(original, edited IssueSnapshot) IssueDiff {
	var d IssueDiff
	if strings.TrimSpace(original.Title) != strings.TrimSpace(edited.Title) {
		v := edited.Title
		d.Title = &v
	}
	if strings.TrimSpace(original.Description) != strings.TrimSpace(edited.Description) {
		v := edited.Description
		d.Description = &v
	}
	if strings.TrimSpace(original.Design) != strings.TrimSpace(edited.Design) {
		v := edited.Design
		d.Design = &v
	}
	if strings.TrimSpace(original.AcceptanceCriteria) != strings.TrimSpace(edited.AcceptanceCriteria) {
		v := edited.AcceptanceCriteria
		d.AcceptanceCriteria = &v
	}
	if strings.TrimSpace(original.Notes) != strings.TrimSpace(edited.Notes) {
		v := edited.Notes
		d.Notes = &v
	}
	if strings.TrimSpace(original.Status) != strings.TrimSpace(edited.Status) {
		v := edited.Status
		d.Status = &v
	}
	if original.Priority != edited.Priority {
		v := edited.Priority
		d.Priority = &v
	}
	if strings.TrimSpace(original.Assignee) != strings.TrimSpace(edited.Assignee) {
		v := edited.Assignee
		d.Assignee = &v
	}
	if !stringSliceEqual(original.Labels, edited.Labels) {
		d.Labels = edited.Labels
	}
	if strings.TrimSpace(original.SourceRepo) != strings.TrimSpace(edited.SourceRepo) {
		v := edited.SourceRepo
		d.SourceRepo = &v
	}
	return d
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- Build Update Argv ---

// BuildUpdateArgv constructs the br update command arguments from a diff.
// Includes --no-auto-import to avoid prefix-mismatch errors in mixed-prefix workspaces.
func BuildUpdateArgv(brPath, issueID string, d *IssueDiff) []string {
	argv := []string{brPath, "update", issueID, "--no-auto-import"}
	if d.Title != nil {
		argv = append(argv, fmt.Sprintf("--title=%s", *d.Title))
	}
	if d.Description != nil {
		argv = append(argv, fmt.Sprintf("--description=%s", *d.Description))
	}
	if d.Design != nil {
		argv = append(argv, fmt.Sprintf("--design=%s", *d.Design))
	}
	if d.AcceptanceCriteria != nil {
		argv = append(argv, fmt.Sprintf("--acceptance-criteria=%s", *d.AcceptanceCriteria))
	}
	if d.Notes != nil {
		argv = append(argv, fmt.Sprintf("--notes=%s", *d.Notes))
	}
	if d.Status != nil {
		argv = append(argv, fmt.Sprintf("--status=%s", *d.Status))
	}
	if d.Priority != nil {
		argv = append(argv, fmt.Sprintf("--priority=%d", *d.Priority))
	}
	if d.Assignee != nil {
		argv = append(argv, fmt.Sprintf("--assignee=%s", *d.Assignee))
	}
	if d.Labels != nil {
		for _, l := range d.Labels {
			argv = append(argv, fmt.Sprintf("--set-labels=%s", l))
		}
	}
	return argv
}

// --- Subprocess Helpers ---

// FetchBrJSON runs `br show ID --json` and returns stdout.
// Uses --no-auto-import to avoid prefix-mismatch errors in mixed-prefix workspaces.
func FetchBrJSON(brPath, issueID string) (string, error) {
	out, err := exec.Command(brPath, "show", issueID, "--json", "--no-auto-import").Output()
	if err != nil {
		return "", fmt.Errorf("br show %s --json: %w", issueID, err)
	}
	return string(out), nil
}

// RunBrUpdate runs the given br update argv.
// Stderr is captured and included in the error message (not piped to the
// terminal, which would corrupt the TUI).
func RunBrUpdate(argv []string) error {
	if len(argv) < 1 {
		return fmt.Errorf("empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		se := strings.TrimSpace(stderr.String())
		if se != "" {
			return fmt.Errorf("%w: %s", err, extractBrErrorMessage(se))
		}
		return err
	}
	return nil
}

// extractBrErrorMessage tries to pull a human-readable message out of br's
// JSON error envelope.  Falls back to the raw string if parsing fails.
func extractBrErrorMessage(raw string) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err == nil && envelope.Error.Message != "" {
		return envelope.Error.Message
	}
	return raw
}

// SetPriority sets the priority of an issue via br update.
func SetPriority(brPath, issueID string, priority int) error {
	return RunBrUpdate([]string{brPath, "update", issueID, "--no-auto-import", fmt.Sprintf("--priority=%d", priority)})
}

// SetStatus sets the status of an issue via br update.
func SetStatus(brPath, issueID, status string) error {
	return RunBrUpdate([]string{brPath, "update", issueID, "--no-auto-import", fmt.Sprintf("--status=%s", status)})
}

// SetAssignee sets the assignee of an issue via br update.
func SetAssignee(brPath, issueID, assignee string) error {
	return RunBrUpdate([]string{brPath, "update", issueID, "--no-auto-import", fmt.Sprintf("--assignee=%s", assignee)})
}

// SetTitle sets the title of an issue via br update.
func SetTitle(brPath, issueID, title string) error {
	return RunBrUpdate([]string{brPath, "update", issueID, "--no-auto-import", fmt.Sprintf("--title=%s", title)})
}

// CreateIssue creates a new issue via br create. Returns the new issue ID.
func CreateIssue(brPath string, parentID *string) (string, error) {
	args := []string{brPath, "create", "New Issue", "-p", "2", "-s", "open", "--silent", "--no-auto-import"}
	if parentID != nil {
		args = append(args, "--parent", *parentID)
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return "", fmt.Errorf("br create: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// DeleteIssue deletes an issue via br delete.
func DeleteIssue(brPath, issueID string) error {
	return RunBrUpdate([]string{brPath, "delete", issueID, "--no-auto-import"})
}

// --- Snapshot Backup ---

// TakeSnapshot saves a br show JSON snapshot to .beads/snapshots/.
func TakeSnapshot(brPath, issueID string) error {
	jsonStr, err := FetchBrJSON(brPath, issueID)
	if err != nil {
		return err
	}
	dir := filepath.Join(".beads", "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	now := time.Now()
	filename := fmt.Sprintf("%d_%d_%s.json", now.Unix(), now.Nanosecond(), issueID)
	return os.WriteFile(filepath.Join(dir, filename), []byte(jsonStr), 0o644)
}

// SaveFailedBuffer saves the markdown buffer to /tmp for recovery.
func SaveFailedBuffer(issueID, content string) string {
	path := fmt.Sprintf("/tmp/bv-failed-%s.md", issueID)
	_ = os.WriteFile(path, []byte(content), 0o644)
	return path
}

// --- Assignee Collection ---

// CollectAssignees returns sorted unique assignees from issues and config extras.
func CollectAssignees(issues []model.Issue, extras []string) []string {
	seen := make(map[string]bool)
	for i := range issues {
		a := strings.TrimSpace(issues[i].Assignee)
		if a != "" {
			seen[a] = true
		}
	}
	for _, a := range extras {
		a = strings.TrimSpace(a)
		if a != "" {
			seen[a] = true
		}
	}
	result := make([]string, 0, len(seen))
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// --- Known Statuses ---

// KnownStatuses is the list of valid status values for the status picker.
var KnownStatuses = []string{
	"open", "in_progress", "blocked", "deferred", "pinned",
	"hooked", "review", "closed", "tombstone",
}

// PriorityLabels for the priority picker.
var PriorityLabels = []string{
	"P0  Critical",
	"P1  High",
	"P2  Medium",
	"P3  Low",
	"P4  Minimal",
}

// --- Editor detection ---

// --- Pending Edit (async file-watch) ---

// PendingEdit tracks an in-progress async editor session.
type PendingEdit struct {
	IssueID      string
	Original     IssueSnapshot
	LastApplied  IssueSnapshot
	MdPath       string
	LastMtime    time.Time
	ChangedAt    *time.Time
	NewlyCreated bool
}

// --- Tea Messages ---

type editAppliedMsg struct {
	issueID string
	nFields int
}

type editErrorMsg struct {
	err error
}

type editNoChangesMsg struct {
	issueID string
}

type editorFinishedMsg struct {
	err          error
	mdPath       string
	snapshot     IssueSnapshot
	issueID      string
	brPath       string
	newlyCreated bool
}

type pollEditMsg struct {
	time time.Time
}

type createAndEditMsg struct {
	issueID string
	err     error
}

type commentEditorFinishedMsg struct {
	err     error
	mdPath  string
	issueID string
	brPath  string
}

// --- Key Handler (called from Update) ---

// tryEditKeyHandler checks if the key matches an edit hotkey and dispatches.
// Returns handled=false if no match (caller should continue normal handling).
// fork: human-edit
func (m Model) tryEditKeyHandler(key string) (Model, tea.Cmd, bool) {
	hk := m.editConfig.Hotkeys

	// Guard: no edit keys while any modal/picker/overlay is active
	if m.showLabelPicker || m.showRecipePicker || m.showRepoPicker ||
		m.showEditPicker || m.titleEditState.Active ||
		m.showAgentPrompt || m.showCassModal || m.showUpdateModal ||
		m.showHelp || m.showTutorial || m.showQuitConfirm ||
		m.showTimeTravelPrompt || m.showAlertsPanel ||
		m.showLabelHealthDetail || m.showLabelDrilldown ||
		m.showLabelGraphAnalysis || m.showAttentionView {
		return m, nil, false
	}

	// Active in list, board, and detail (split-view) focus
	if m.focused != focusList && m.focused != focusBoard && m.focused != focusDetail {
		return m, nil, false
	}

	switch key {
	case hk.EditPriority:
		m2, cmd := m.openPriorityPicker()
		return m2, cmd, true
	case hk.EditStatus:
		m2, cmd := m.openStatusPicker()
		return m2, cmd, true
	case hk.EditAssignee:
		m2, cmd := m.openAssigneePicker()
		return m2, cmd, true
	case hk.EditTitle:
		m2, cmd := m.startTitleEdit()
		return m2, cmd, true
	case hk.OpenEditor:
		m2, cmd := m.openIssueInEditor()
		if cmd != nil {
			return m2, cmd, true
		}
		// nil cmd means fall through to existing openInEditor() for GUI editors
		return m, nil, false
	case hk.CreateIssue:
		m2, cmd := m.createNewIssue(nil)
		return m2, cmd, true
	case hk.CreateSubIssue:
		m2, cmd := m.createSubIssue()
		return m2, cmd, true
	case hk.AddComment:
		m2, cmd := m.addComment()
		return m2, cmd, true
	}
	return m, nil, false
}

// --- Picker Openers ---

func (m Model) getSelectedIssue() *model.Issue {
	if m.focused == focusBoard {
		return m.board.SelectedIssue()
	}
	sel := m.list.SelectedItem()
	if sel == nil {
		return nil
	}
	if item, ok := sel.(IssueItem); ok {
		return &item.Issue
	}
	return nil
}

func (m Model) openPriorityPicker() (Model, tea.Cmd) {
	issue := m.getSelectedIssue()
	if issue == nil {
		m.statusMsg = "No issue selected"
		m.statusIsError = true
		return m, nil
	}
	cursor := issue.Priority
	if cursor < 0 {
		cursor = 0
	}
	if cursor > 4 {
		cursor = 4
	}
	m.editPicker = NewEditPickerModal("Set Priority", PriorityLabels, cursor, issue.ID)
	m.editPickerKind = editPickerPriority
	m.showEditPicker = true
	return m, nil
}

func (m Model) openStatusPicker() (Model, tea.Cmd) {
	issue := m.getSelectedIssue()
	if issue == nil {
		m.statusMsg = "No issue selected"
		m.statusIsError = true
		return m, nil
	}
	cursor := 0
	for i, s := range KnownStatuses {
		if s == string(issue.Status) {
			cursor = i
			break
		}
	}
	m.editPicker = NewEditPickerModal("Set Status", KnownStatuses, cursor, issue.ID)
	m.editPickerKind = editPickerStatus
	m.showEditPicker = true
	return m, nil
}

func (m Model) openAssigneePicker() (Model, tea.Cmd) {
	issue := m.getSelectedIssue()
	if issue == nil {
		m.statusMsg = "No issue selected"
		m.statusIsError = true
		return m, nil
	}
	assignees := CollectAssignees(m.issues, m.editConfig.ExtraAssignees)
	if len(assignees) == 0 {
		m.statusMsg = "No assignees found"
		m.statusIsError = false
		return m, nil
	}
	cursor := 0
	for i, a := range assignees {
		if a == issue.Assignee {
			cursor = i
			break
		}
	}
	m.editPicker = NewEditPickerModal("Set Assignee", assignees, cursor, issue.ID)
	m.editPickerKind = editPickerAssignee
	m.showEditPicker = true
	return m, nil
}

// --- Full Editor ---

func (m Model) openIssueInEditor() (Model, tea.Cmd) {
	issue := m.getSelectedIssue()
	if issue == nil {
		m.statusMsg = "No issue selected"
		m.statusIsError = true
		return m, nil
	}

	// Smart dispatch: only proceed if the configured editor is terminal-based
	if !IsTerminalEditor(m.editConfig.EditorPath) {
		// Fall through to existing openInEditor (GUI editor path)
		return m, nil
	}

	snap := SnapshotFromIssue(issue)

	// Best-effort refresh from br
	if jsonStr, err := FetchBrJSON(m.editConfig.BrPath, snap.ID); err == nil {
		if fresh, err := SnapshotFromBrJSON(jsonStr); err == nil {
			snap = fresh
		}
	}

	// Take backup snapshot (best-effort)
	_ = TakeSnapshot(m.editConfig.BrPath, snap.ID)

	// Write markdown to temp file
	md := SnapshotToMarkdown(snap)
	mdPath := fmt.Sprintf("/tmp/bv-edit-%s.md", snap.ID)
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		m.statusMsg = fmt.Sprintf("Failed to write edit file: %v", err)
		m.statusIsError = true
		return m, nil
	}

	brPath := m.editConfig.BrPath
	issueID := snap.ID
	original := snap

	// WezTerm async mode
	if os.Getenv("WEZTERM_PANE") != "" {
		if err := launchInWezterm(&m.editConfig, mdPath); err != nil {
			m.statusMsg = fmt.Sprintf("WezTerm launch failed: %v", err)
			m.statusIsError = true
			return m, nil
		}
		info, err := os.Stat(mdPath)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Stat edit file: %v", err)
			m.statusIsError = true
			return m, nil
		}
		m.pendingEdit = &PendingEdit{
			IssueID:     issueID,
			Original:    original,
			LastApplied: original,
			MdPath:      mdPath,
			LastMtime:   info.ModTime(),
		}
		m.statusMsg = fmt.Sprintf("Editing %s in %s (watching for saves)", issueID, filepath.Base(m.editConfig.EditorPath))
		return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
			return pollEditMsg{time: t}
		})
	}

	// Synchronous mode via tea.ExecProcess
	editorCmd := exec.Command(m.editConfig.EditorPath, mdPath)
	m.statusMsg = fmt.Sprintf("Opening %s in editor...", issueID)
	return m, tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return editorFinishedMsg{
			err:      err,
			mdPath:   mdPath,
			snapshot: original,
			issueID:  issueID,
			brPath:   brPath,
		}
	})
}

func launchInWezterm(config *EditConfig, mdPath string) error {
	parts := strings.Fields(config.WeztermCommand)
	if len(parts) == 0 {
		return fmt.Errorf("empty wezterm_command")
	}
	args := append(parts[1:], config.EditorPath, mdPath)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// --- Create Issue ---

func (m Model) createNewIssue(parentID *string) (Model, tea.Cmd) {
	brPath := m.editConfig.BrPath
	return m, func() tea.Msg {
		id, err := CreateIssue(brPath, parentID)
		return createAndEditMsg{issueID: id, err: err}
	}
}

func (m Model) createSubIssue() (Model, tea.Cmd) {
	issue := m.getSelectedIssue()
	if issue == nil {
		m.statusMsg = "No issue selected"
		m.statusIsError = true
		return m, nil
	}
	parentID := issue.ID
	return m.createNewIssue(&parentID)
}

// --- Add Comment ---

// AddBrComment adds a comment to an issue via br comments add, reading from a file.
func AddBrComment(brPath, issueID, filePath string) error {
	return RunBrUpdate([]string{brPath, "comments", "add", issueID, "-f", filePath, "--no-auto-import"})
}

func (m Model) addComment() (Model, tea.Cmd) {
	issue := m.getSelectedIssue()
	if issue == nil {
		m.statusMsg = "No issue selected"
		m.statusIsError = true
		return m, nil
	}

	// Determine editor
	editor := m.editConfig.EditorPath
	if !IsTerminalEditor(editor) {
		m.statusMsg = "Add comment requires a terminal editor (set $EDITOR or editor_path)"
		m.statusIsError = true
		return m, nil
	}

	// Write empty temp file for the comment
	mdPath := fmt.Sprintf("/tmp/bv-comment-%s.md", issue.ID)
	if err := os.WriteFile(mdPath, []byte(""), 0o644); err != nil {
		m.statusMsg = fmt.Sprintf("Failed to write comment file: %v", err)
		m.statusIsError = true
		return m, nil
	}

	issueID := issue.ID
	brPath := m.editConfig.BrPath

	editorCmd := exec.Command(editor, mdPath)
	m.statusMsg = fmt.Sprintf("Adding comment to %s...", issueID)
	return m, tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return commentEditorFinishedMsg{
			err:     err,
			mdPath:  mdPath,
			issueID: issueID,
			brPath:  brPath,
		}
	})
}

// handleCommentEditorFinished processes the result of a comment editor session.
// fork: human-edit
func (m Model) handleCommentEditorFinished(msg commentEditorFinishedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.statusMsg = fmt.Sprintf("Editor error: %v", msg.err)
		m.statusIsError = true
		return m, nil
	}

	mdBytes, err := os.ReadFile(msg.mdPath)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Read comment file: %v", err)
		m.statusIsError = true
		return m, nil
	}

	text := strings.TrimSpace(string(mdBytes))
	if text == "" {
		m.statusMsg = "Comment empty — not added"
		m.statusIsError = false
		_ = os.Remove(msg.mdPath)
		return m, nil
	}

	if err := AddBrComment(msg.brPath, msg.issueID, msg.mdPath); err != nil {
		m.statusMsg = fmt.Sprintf("Add comment failed: %v", err)
		m.statusIsError = true
		return m, nil
	}

	_ = os.Remove(msg.mdPath)
	m.statusMsg = fmt.Sprintf("Comment added to %s", msg.issueID)
	m.statusIsError = false
	return m, func() tea.Msg { return FileChangedMsg{} }
}

// --- Message Handlers (called from Update) ---

// handleEditApplied processes a successful edit result. fork: human-edit
func (m Model) handleEditApplied(msg editAppliedMsg) Model {
	m.statusMsg = fmt.Sprintf("Updated %s: %d field(s) saved", msg.issueID, msg.nFields)
	m.statusIsError = false
	return m
}

// handleEditError processes an edit error. fork: human-edit
func (m Model) handleEditError(msg editErrorMsg) Model {
	m.statusMsg = fmt.Sprintf("Edit failed: %v", msg.err)
	m.statusIsError = true
	return m
}

// handleEditNoChanges processes a no-changes result. fork: human-edit
func (m Model) handleEditNoChanges(msg editNoChangesMsg) Model {
	m.statusMsg = fmt.Sprintf("No changes detected for %s", msg.issueID)
	m.statusIsError = false
	return m
}

// handleEditorFinished processes the result of a synchronous editor session.
// fork: human-edit
func (m Model) handleEditorFinished(msg editorFinishedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.statusMsg = fmt.Sprintf("Editor error: %v", msg.err)
		m.statusIsError = true
		return m, nil
	}

	mdBytes, err := os.ReadFile(msg.mdPath)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Read edit file: %v", err)
		m.statusIsError = true
		return m, nil
	}

	edited, err := MarkdownToSnapshot(string(mdBytes))
	if err != nil {
		path := SaveFailedBuffer(msg.issueID, string(mdBytes))
		m.statusMsg = fmt.Sprintf("Parse failed (saved to %s): %v", path, err)
		m.statusIsError = true
		return m, nil
	}

	diff := DiffSnapshots(msg.snapshot, edited)
	if diff.IsEmpty() {
		if msg.newlyCreated {
			_ = DeleteIssue(msg.brPath, msg.issueID)
			m.statusMsg = fmt.Sprintf("No changes — deleted empty issue %s", msg.issueID)
			return m, func() tea.Msg { return FileChangedMsg{} }
		}
		m.statusMsg = fmt.Sprintf("No changes detected for %s", msg.issueID)
		return m, nil
	}

	argv := BuildUpdateArgv(msg.brPath, msg.issueID, &diff)
	if err := RunBrUpdate(argv); err != nil {
		path := SaveFailedBuffer(msg.issueID, string(mdBytes))
		m.statusMsg = fmt.Sprintf("br update failed (saved to %s): %v", path, err)
		m.statusIsError = true
		return m, nil
	}

	m.statusMsg = fmt.Sprintf("Updated %s: %d field(s) saved", msg.issueID, diff.FieldCount())
	m.statusIsError = false

	// Trigger reload
	return m, func() tea.Msg { return FileChangedMsg{} }
}

// handleEditPoll handles periodic file-watch polling for async editor sessions.
// fork: human-edit
func (m Model) handleEditPoll() (Model, tea.Cmd) {
	if m.pendingEdit == nil {
		return m, nil
	}

	pe := m.pendingEdit

	// Check if file still exists
	info, err := os.Stat(pe.MdPath)
	if os.IsNotExist(err) {
		diff := DiffSnapshots(pe.Original, pe.LastApplied)
		if pe.NewlyCreated && diff.IsEmpty() {
			_ = DeleteIssue(m.editConfig.BrPath, pe.IssueID)
			m.pendingEdit = nil
			m.statusMsg = fmt.Sprintf("No changes — deleted empty issue %s", pe.IssueID)
			return m, func() tea.Msg { return FileChangedMsg{} }
		}
		m.pendingEdit = nil
		m.statusMsg = "Editor session ended"
		return m, nil
	}
	if err != nil {
		return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
			return pollEditMsg{time: t}
		})
	}

	mtime := info.ModTime()
	if mtime != pe.LastMtime {
		pe.LastMtime = mtime
		now := time.Now()
		pe.ChangedAt = &now
		return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
			return pollEditMsg{time: t}
		})
	}

	if pe.ChangedAt != nil && time.Since(*pe.ChangedAt) >= 250*time.Millisecond {
		pe.ChangedAt = nil

		mdBytes, err := os.ReadFile(pe.MdPath)
		if err != nil {
			return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
				return pollEditMsg{time: t}
			})
		}

		edited, err := MarkdownToSnapshot(string(mdBytes))
		if err != nil {
			m.statusMsg = fmt.Sprintf("Parse error: %v", err)
			m.statusIsError = true
			return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
				return pollEditMsg{time: t}
			})
		}

		diff := DiffSnapshots(pe.LastApplied, edited)
		if !diff.IsEmpty() {
			argv := BuildUpdateArgv(m.editConfig.BrPath, pe.IssueID, &diff)
			if err := RunBrUpdate(argv); err != nil {
				m.statusMsg = fmt.Sprintf("br update failed: %v", err)
				m.statusIsError = true
			} else {
				pe.LastApplied = edited
				m.statusMsg = fmt.Sprintf("Updated %s: %d field(s) saved", pe.IssueID, diff.FieldCount())
				m.statusIsError = false
				// Trigger reload
				return m, tea.Batch(
					func() tea.Msg { return FileChangedMsg{} },
					tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
						return pollEditMsg{time: t}
					}),
				)
			}
		}
	}

	return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
		return pollEditMsg{time: t}
	})
}

// handleCreateAndEdit handles the result of creating a new issue and opens it in the editor.
// fork: human-edit
func (m Model) handleCreateAndEdit(msg createAndEditMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.statusMsg = fmt.Sprintf("Create failed: %v", msg.err)
		m.statusIsError = true
		return m, nil
	}
	if msg.issueID == "" {
		m.statusMsg = "Create returned empty ID"
		m.statusIsError = true
		return m, nil
	}

	// Fetch the new issue via br show
	jsonStr, err := FetchBrJSON(m.editConfig.BrPath, msg.issueID)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Fetch new issue: %v", err)
		m.statusIsError = true
		return m, nil
	}
	snap, err := SnapshotFromBrJSON(jsonStr)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Parse new issue: %v", err)
		m.statusIsError = true
		return m, nil
	}

	md := SnapshotToMarkdown(snap)
	mdPath := fmt.Sprintf("/tmp/bv-edit-%s.md", snap.ID)
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		m.statusMsg = fmt.Sprintf("Write edit file: %v", err)
		m.statusIsError = true
		return m, nil
	}

	brPath := m.editConfig.BrPath
	issueID := snap.ID
	original := snap

	// WezTerm async mode
	if os.Getenv("WEZTERM_PANE") != "" {
		if err := launchInWezterm(&m.editConfig, mdPath); err != nil {
			m.statusMsg = fmt.Sprintf("WezTerm launch failed: %v", err)
			m.statusIsError = true
			return m, nil
		}
		info, statErr := os.Stat(mdPath)
		if statErr != nil {
			m.statusMsg = fmt.Sprintf("Stat edit file: %v", statErr)
			m.statusIsError = true
			return m, nil
		}
		m.pendingEdit = &PendingEdit{
			IssueID:      issueID,
			Original:     original,
			LastApplied:  original,
			MdPath:       mdPath,
			LastMtime:    info.ModTime(),
			NewlyCreated: true,
		}
		m.statusMsg = fmt.Sprintf("Created %s — editing in %s", issueID, filepath.Base(m.editConfig.EditorPath))
		return m, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
			return pollEditMsg{time: t}
		})
	}

	// Synchronous mode
	editorCmd := exec.Command(m.editConfig.EditorPath, mdPath)
	m.statusMsg = fmt.Sprintf("Created %s — opening in editor...", issueID)
	return m, tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return editorFinishedMsg{
			err:          err,
			mdPath:       mdPath,
			snapshot:     original,
			issueID:      issueID,
			brPath:       brPath,
			newlyCreated: true,
		}
	})
}

// handleEditPickerResult processes the result of an edit picker modal.
// fork: human-edit
func (m Model) handleEditPickerResult() (Model, tea.Cmd) {
	if m.editPicker.Result != PickerAccepted {
		m.showEditPicker = false
		return m, nil
	}

	m.showEditPicker = false
	brPath := m.editConfig.BrPath
	issueID := m.editPicker.IssueID
	cursor := m.editPicker.Cursor

	switch m.editPickerKind {
	case editPickerPriority:
		m.statusMsg = fmt.Sprintf("Setting priority P%d on %s...", cursor, issueID)
		return m, func() tea.Msg {
			if err := SetPriority(brPath, issueID, cursor); err != nil {
				return editErrorMsg{err: err}
			}
			return editAppliedMsg{issueID: issueID, nFields: 1}
		}
	case editPickerStatus:
		if cursor >= 0 && cursor < len(KnownStatuses) {
			status := KnownStatuses[cursor]
			m.statusMsg = fmt.Sprintf("Setting status %s on %s...", status, issueID)
			return m, func() tea.Msg {
				if err := SetStatus(brPath, issueID, status); err != nil {
					return editErrorMsg{err: err}
				}
				return editAppliedMsg{issueID: issueID, nFields: 1}
			}
		}
	case editPickerAssignee:
		if cursor >= 0 && cursor < len(m.editPicker.Items) {
			assignee := m.editPicker.Items[cursor]
			m.statusMsg = fmt.Sprintf("Setting assignee %s on %s...", assignee, issueID)
			return m, func() tea.Msg {
				if err := SetAssignee(brPath, issueID, assignee); err != nil {
					return editErrorMsg{err: err}
				}
				return editAppliedMsg{issueID: issueID, nFields: 1}
			}
		}
	}
	return m, nil
}

// jsonUnmarshal wraps encoding/json for JSON parsing.
func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
