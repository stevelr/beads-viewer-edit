# bv Human Edit Feature — Design & Implementation

## Overview

The human-edit feature adds in-TUI issue editing to beads_viewer (bv). Users can modify individual fields via quick-edit pickers, edit titles inline, or open a full per-issue markdown file in a terminal editor. All mutations go through the `br` CLI as a subprocess — bv never writes to JSONL or SQLite directly.

---

## 1. Hotkeys

All edit hotkeys are **configurable** via `bv-edit.yaml`. Defaults shown. Active in **List**, **Board**, and **Detail** (split-view) focus modes only. Suppressed when any modal, picker, or overlay is active.

| Default Key | Config Key       | Action                                               |
| ----------- | ---------------- | ---------------------------------------------------- |
| `ctrl+p`    | edit_priority    | Open priority picker                                 |
| `ctrl+o`    | edit_status      | Open status picker                                   |
| `ctrl+a`    | edit_assignee    | Open assignee picker                                 |
| `ctrl+t`    | edit_title       | Inline title edit                                    |
| `O`         | open_editor      | Open issue in terminal editor (smart dispatch)       |
| `ctrl+n`    | create_issue     | Create new issue, then open in editor                |
| `ctrl+g`    | create_sub_issue | Create sub-issue under selected, then open in editor |
| `ctrl+x`    | add_comment      | Add comment to selected issue via editor             |

The `ctrl+` prefix keeps edit keys in a separate namespace from view/navigation keys, avoiding conflicts with current and future upstream bindings.

**`O` key smart dispatch:** If the configured editor (from `editor_path`, `$EDITOR`, or `$VISUAL`) is a terminal editor (hx, vim, vi, nvim, nano, etc.), the `O` key uses the new terminal-based markdown edit workflow. Otherwise it falls through to the existing `openInEditor()` GUI editor path (VS Code, gedit, etc.), leaving that behavior completely untouched.

---

## 2. Configuration

### 2.1 Config File: `bv-edit.yaml`

Loaded from `./bv-edit.yaml` or `~/.config/bv-edit.yaml` (first found wins). Falls back to compiled defaults if neither exists.

```yaml
br_path: "br"
editor_path: "hx"  # defaults to $EDITOR, then $VISUAL, then "vi"
wezterm_command: "wezterm cli split-pane --bottom --"
extra_assignees:
  - "alice"
  - "bob"
hotkeys:
  edit_priority: "ctrl+p"
  edit_status: "ctrl+o"
  edit_assignee: "ctrl+a"
  open_editor: "O"
  edit_title: "ctrl+t"
  create_issue: "ctrl+n"
  create_sub_issue: "ctrl+g"
  add_comment: "ctrl+x"
```

### 2.2 Go Structs

```go
type EditConfig struct {
    BrPath         string       `yaml:"br_path"`
    EditorPath     string       `yaml:"editor_path"`
    WeztermCommand string       `yaml:"wezterm_command"`
    Hotkeys        HotkeyConfig `yaml:"hotkeys"`
    ExtraAssignees []string     `yaml:"extra_assignees"`
}

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
```

Hotkey fields use `string` (not `rune`) because `yaml.v3` cannot unmarshal a YAML string like `"ctrl+p"` into a Go `rune`. The strings are matched against `tea.KeyMsg.String()` at runtime.

`LoadEditConfig()` reads the file, unmarshals onto defaults, and fills in any blank hotkey/path fields from `DefaultEditConfig()`.

---

## 3. Data Persistence Model

bv has **no write path** of its own. All mutations go through the `br` CLI tool:

- `br update ID --field=value` for all field mutations
- `br show ID --json` for fetching fresh data
- `br create "title" -p N -s status --silent` for new issues
- `br comments add ID -f FILE` for adding comments

All `br` subprocess calls include `--no-auto-import` to avoid prefix-mismatch errors in mixed-prefix workspaces (where the JSONL contains issues from multiple prefix namespaces like `bd-*` and `bv-*`).

After `br update` succeeds, bv triggers a `FileChangedMsg` which reloads all issues from the data source (JSONL/SQLite). bv also has a file watcher that reloads when JSONL changes.

### 3.1 Subprocess Helpers

```go
func FetchBrJSON(brPath, issueID string) (string, error)
// exec: br show ID --json --no-auto-import

func RunBrUpdate(argv []string) error
// exec: argv[0] argv[1:]...
// Captures stderr; on error, parses br's JSON error envelope for human-readable message

func SetPriority(brPath, issueID string, modelPriority int) error
func SetStatus(brPath, issueID, status string) error
func SetAssignee(brPath, issueID, assignee string) error
func SetTitle(brPath, issueID, title string) error
// Each calls RunBrUpdate with appropriate args + --no-auto-import

func CreateIssue(brPath string, parentID *string) (string, error)
// exec: br create "New Issue" -p 2 -s open --silent --no-auto-import [--parent ID]
// Returns trimmed stdout as new issue ID

func AddBrComment(brPath, issueID, filePath string) error
// exec: br comments add ISSUE -f FILE --no-auto-import
```

### 3.2 Error Handling

`RunBrUpdate` captures stderr (never pipes to `os.Stderr`, which would corrupt the TUI). If `br` returns a JSON error envelope like `{"error":{"message":"..."}}`, the `message` field is extracted for display in the status bar. On failure, the markdown buffer is saved to `/tmp/bv-failed-ISSUEID.md` so the user can recover their edits.

---

## 4. Issue Snapshot

`IssueSnapshot` is a lightweight struct holding all editable fields:

```go
type IssueSnapshot struct {
    ID, Title, Description, Design, AcceptanceCriteria, Notes string
    Status string
    Priority int
    Assignee string
    Labels []string
    SourceRepo string
}
```

Two constructors:

- `SnapshotFromIssue(issue *model.Issue)` — from bv's in-memory data
- `SnapshotFromBrJSON(jsonStr string)` — parses `br show --json` output (accepts JSON object or single-element array)

---

## 5. Markdown Editing Format

### 5.1 Serialization (`SnapshotToMarkdown`)

```markdown
---
id: BD-123
title: "Fix the widget: a [tricky] one"
status: open
priority: 2
assignee: alice
labels: bug, ux
repo: my-repo
---

<description>
The widget is broken.

## Root Cause

The cog is misaligned.

</description>

<design>

</design>

<acceptance_criteria>
Widget works.

</acceptance_criteria>

<notes>
See also BD-456.

</notes>
```

**Frontmatter fields:**

- `id` — read-only identifier (changes ignored on parse)
- `title` — YAML-escaped via `yaml.Marshal` when it contains special chars (colons, brackets, quotes, etc.)
- `status` — one of: open, in_progress, blocked, deferred, pinned, hooked, review, closed, tombstone
- `priority` — integer 0-4 (P0=0 is highest, same as `br` CLI)
- `assignee` — free text
- `labels` — comma-separated string
- `repo` — only included if non-empty (maps to `SourceRepo`)

**Body sections use XML-style tags**, not markdown headings. This is critical: field content (especially `description` and `notes`) routinely contains markdown headings (`## heading`, `# heading`). Using `## heading` as section delimiters would cause the parser to split content at those headings, silently losing data. XML-style tags are unambiguous.

**Known section tags:** `description`, `design`, `acceptance_criteria`, `notes`. Only these exact tag names are recognized as section delimiters. All other `<` and `>` occurrences (HTML tags, comparisons like `x < 10`, unknown tags) are treated as literal content.

**Blank line before closing tag:** A blank line is emitted before each `</tag>` to prevent markdown LSP formatters (e.g. format-on-save in helix or other editors) from indenting the closing tag. The parser uses `strings.TrimSpace()` on each line before comparing, so indented closing tags parse correctly regardless.

**Empty sections** are written as `<tag>\n\n</tag>`.

### 5.2 Parsing (`MarkdownToSnapshot`)

1. Opening `---` must be first line
2. Closing `---` delimits end of frontmatter
3. YAML parsed via `yaml.Unmarshal` into a struct with `*string`/`*int` fields for optionality
4. Body parsed by tag state machine:
   - `<tagname>` on its own line (trimmed) starts a section if `tagname` is in `knownSectionTags`
   - Lines accumulate until `</tagname>` (matching the current tag only)
   - A closing tag for a _different_ known tag is treated as content (e.g. `</notes>` inside `<description>` is literal text)
   - Content is trimmed (leading/trailing whitespace removed via `strings.TrimSpace`)
   - If file ends without closing tag, content is captured as-is
5. Labels string split on commas, each trimmed, empty strings filtered out

### 5.3 Parsing Rules for `<` and `>` in Content

When **inside** a section (between `<tag>` and `</tag>`):

- Only `</currenttag>` (trimmed, on its own line) closes the section
- Everything else is content, including `<b>`, `</othertag>`, `<description>`, `x < 10`, etc.

When **outside** a section:

- Only `<knowntagname>` (trimmed, on its own line, in `knownSectionTags`) opens a section
- Everything else is ignored (inter-section whitespace, HTML, unknown tags)

---

## 6. Field Diffing and Update

### 6.1 Diff (`DiffSnapshots`)

Compares each field between original and edited snapshots:

- String fields compared after `strings.TrimSpace()` (whitespace-insensitive)
- Priority compared as integers
- Labels compared as `[]string` (order-sensitive)
- Returns `IssueDiff` with `*T` for each field (`nil` = unchanged)
- `IsEmpty()` and `FieldCount()` methods

### 6.2 Build Update Command (`BuildUpdateArgv`)

Produces: `br update ISSUE_ID --no-auto-import [--field=value ...]`

Flag format: `--title=VALUE`, `--description=VALUE`, `--design=VALUE`, `--acceptance-criteria=VALUE`, `--notes=VALUE`, `--status=VALUE`, `--priority=N`, `--assignee=VALUE`, `--set-labels=LABEL` (one per label).

Priority: bv model uses 0-4, same as `br` — no conversion needed.

---

## 7. Quick-Edit Modals

### 7.1 EditPickerModal

A generic list-selection modal reused for priority, status, and assignee pickers. The caller interprets the selected `Cursor` index based on context.

```go
type EditPickerModal struct {
    Title   string
    Items   []string
    Cursor  int
    Result  EditPickerResult  // PickerPending, PickerAccepted, PickerCancelled
    IssueID string
}
```

**Key handling:** `j`/`k`/`↓`/`↑` navigate, `Enter` accepts, `Esc` cancels, `0`-`4` digit shortcuts jump cursor to that index (priority picker only; digits outside item range are ignored).

**Rendering:** Centered popup, bordered (lipgloss rounded border), with title, `▸` cursor marker, and footer hint `"j/k nav | Enter apply | Esc cancel"`. Fixed width ~30 chars.

### 7.2 Priority Picker (`ctrl+p`)

Items: `["P0  Critical", "P1  High", "P2  Medium", "P3  Low", "P4  Minimal"]`

Cursor index maps directly to priority value (0-4). Pre-positioned to current issue priority. On accept: `br update ID --priority=N`.

### 7.3 Status Picker (`ctrl+o`)

Items: `["open", "in_progress", "blocked", "deferred", "pinned", "hooked", "review", "closed", "tombstone"]`

Pre-positioned to current issue status. On accept: `br update ID --status=VALUE`.

### 7.4 Assignee Picker (`ctrl+a`)

Items: sorted unique assignees from all loaded issues + `extra_assignees` from config. If empty, shows "No assignees found" in status bar. Pre-positioned to current assignee. On accept: `br update ID --assignee=VALUE`.

### 7.5 Integration

Model fields: `editPicker EditPickerModal`, `editPickerKind editPickerKind`, `showEditPicker bool`.

In `Update()`: when `showEditPicker` is true, keys are forwarded to the picker. On `PickerAccepted`, a `tea.Cmd` dispatches the appropriate `br update` call asynchronously. On success, `editAppliedMsg` triggers a data reload via `FileChangedMsg`.

In `View()`: when `showEditPicker` is true, the picker modal is rendered as an overlay (takes priority over most other views, rendered before the update modal).

---

## 8. Inline Title Edit

### 8.1 State

```go
type TitleEditState struct {
    Active  bool
    Buffer  string
    Cursor  int      // rune index
    IssueID string
}
```

### 8.2 Activation (`ctrl+t`)

Sets buffer to current title, cursor to end of string. Status bar shows the title buffer with a bar cursor: `Title: Fix the widget│`.

### 8.3 Key Handling

Intercepted in `Update()` before all other key processing when `titleEditState.Active` is true:

| Key             | Action                                 |
| --------------- | -------------------------------------- |
| `Esc`           | Cancel, clear state                    |
| `Enter`         | Save: `br update ID --title=VALUE`     |
| `Backspace`     | Delete char before cursor (rune-aware) |
| `Delete`        | Delete char after cursor               |
| `Left`/`Right`  | Move cursor one rune                   |
| `Home`/`Ctrl+a` | Cursor to start                        |
| `End`/`Ctrl+e`  | Cursor to end                          |
| `Ctrl+k`        | Kill to end of line                    |
| `Space`         | Insert space at cursor                 |
| Printable runes | Insert at cursor                       |

**Space handling:** Bubble Tea reports space as `tea.KeySpace` (not `tea.KeyRunes`), so it requires an explicit `case " "` — the generic rune insertion path does not handle it.

### 8.4 Rendering

`RenderTitleEdit()` produces a string with a Unicode bar cursor (`│`) at the insert position: `Fix the w│idget`. At end-of-string: `Fix the widget│`. The status bar is updated on every keystroke with `Title: ` prefix followed by the rendered buffer.

---

## 9. Full Editor Workflow (`O` key)

### 9.1 Smart Dispatch

The `O` key checks `editConfig.EditorPath` (which defaults from `$EDITOR`, `$VISUAL`, or `"vi"`):

- If the editor is a terminal editor: use the new markdown edit workflow
- Otherwise: return `handled=false`, fall through to existing `openInEditor()` GUI path

Detection uses `IsTerminalEditor()` in `model.go`, backed by the `terminalEditorExecutables` map. Recognized executables: `vim`, `vi`, `nvim`, `nano`, `emacs`, `pico`, `joe`, `ne`, `hx`. The function extracts the base name from the configured editor path before lookup.

### 9.2 Snapshot and Markdown Generation

1. Get selected issue from list (or board)
2. Build `IssueSnapshot` from in-memory issue data
3. Best-effort refresh from `br show ID --json` (fall back to in-memory if `br` fails)
4. Take backup snapshot: save `br show ID --json` to `.beads/snapshots/{UNIX_SECS}_{NANOS}_{ISSUE_ID}.json`
5. Serialize snapshot to markdown with YAML frontmatter + XML-style body tags
6. Write to `/tmp/bv-edit-{ISSUE_ID}.md`

### 9.3 Editor Launch

**WezTerm async mode** (`$WEZTERM_PANE` set):

1. Parse `wezterm_command` into program + args (split on whitespace)
2. Append editor path and markdown file path
3. Spawn process (stdout/stderr discarded)
4. Set up `PendingEdit` with file-watch polling
5. Status: "Editing {id} in {editor} (watching for saves)"

**Synchronous mode** (non-WezTerm):

1. Use `tea.ExecProcess` to suspend Bubble Tea and yield terminal to editor
2. On editor exit, the callback reads the markdown, parses, diffs, and applies
3. Bubble Tea resumes automatically

### 9.4 File-Watch Polling (async WezTerm mode)

`PendingEdit` state machine, polled every 250ms via `tea.Tick`:

```go
type PendingEdit struct {
    IssueID     string
    Original    IssueSnapshot
    LastApplied IssueSnapshot
    MdPath      string
    LastMtime   time.Time
    ChangedAt   *time.Time  // nil = not debouncing
}
```

**Poll cycle:**

1. If file no longer exists → clear `pendingEdit`, show "Editor session ended"
2. If mtime changed → record `ChangedAt`, wait for debounce
3. If `ChangedAt` set and 250ms elapsed → read file, parse, diff against `LastApplied`
4. If non-empty diff → `br update`, advance `LastApplied`, trigger data reload
5. Schedule next 250ms tick

### 9.5 Post-Editor (synchronous mode)

In the `tea.ExecProcess` callback (runs after editor exits):

1. Read markdown file
2. Parse to snapshot
3. Diff against original
4. If empty diff → `editNoChangesMsg`
5. Build `br update` argv, execute
6. On success → `editAppliedMsg` with field count
7. On failure → save buffer to `/tmp/bv-failed-ISSUEID.md`, return `editErrorMsg`

### 9.6 Error Recovery

On any `br update` failure during the full-editor flow:

- The markdown buffer is saved to `/tmp/bv-failed-{ISSUE_ID}.md`
- Status bar shows the path and error message
- User can manually re-apply by copying the file or re-editing

---

## 10. Create New Issue (`ctrl+n`, `ctrl+g`)

### 10.1 `ctrl+n` — New top-level issue

1. Run `br create "New Issue" -p 2 -s open --silent --no-auto-import`
2. Capture new issue ID from stdout (`createAndEditMsg`)
3. Fetch fresh data via `br show NEW_ID --json`
4. Build snapshot, serialize to markdown
5. Open in editor (same WezTerm/sync logic as `O` key)

### 10.2 `ctrl+g` — New sub-issue

Same as `ctrl+n` but passes `--parent SELECTED_ISSUE_ID` to `br create`.

### 10.3 Empty Issue Cleanup on Cancel

When creating a new issue (`ctrl+n` / `ctrl+g`), if the user closes the editor without making any changes, the stub issue is automatically deleted via `br delete`. This applies to both synchronous and WezTerm async editor modes. Without this cleanup, cancelled creates would leave empty placeholder issues in the database.

---

## 11. Add Comment (`ctrl+x`)

Opens `$EDITOR` with an empty temp file (`/tmp/bv-comment-{ISSUE_ID}.md`) for the user to compose a markdown comment. Requires a terminal editor.

**Flow:**

1. Check that `$EDITOR` is a terminal editor (error if not)
2. Write empty temp file
3. Launch editor via `tea.ExecProcess` (synchronous suspend/resume)
4. On editor exit, read file and trim whitespace
5. If empty → "Comment empty — not added", remove temp file
6. If non-empty → `br comments add ISSUE_ID -f FILE --no-auto-import`
7. On success → remove temp file, trigger data reload
8. On failure → show error in status bar (temp file preserved for recovery)

**Why `ctrl+x` and not `ctrl+m`?** `ctrl+m` sends the same byte as Enter (carriage return, ASCII 0x0D) in terminals. Bubble Tea reports it as `"enter"`, making it unusable as a distinct hotkey.

**Message type:** `commentEditorFinishedMsg` — carries `err`, `mdPath`, `issueID`, `brPath`. Handler: `handleCommentEditorFinished`.

---

## 12. Post-Edit Refresh (all edit operations)

After any successful edit (`editAppliedMsg`):

1. Status message: "Updated {issue_id}: {n_fields} field(s) saved"
2. Dispatches `FileChangedMsg` to trigger full data reload
3. bv's existing reload preserves list selection by issue ID and re-runs analysis

---

## 13. Snapshot/Backup System

Snapshots are taken **only before full-editor sessions** (`O` key), not before quick-edits. Quick-edits change one field and the previous value is visible in the picker — recovery is trivial.

For full-editor snapshots:

1. Fetch `br show ISSUE_ID --json`
2. Create dir `.beads/snapshots/` (if not exists)
3. Save to `.beads/snapshots/{UNIX_SECS}_{NANOS}_{ISSUE_ID}.json`

---

## 14. Help Integration

### 14.1 Help Overlay (`?` key)

An "Editing" panel is added to the `?` help overlay (`renderHelpOverlay` in model.go), showing all 8 edit hotkeys with descriptions. Rendered as a bordered panel matching the existing style.

### 14.2 Help Overlay Column Balancing

The `?` overlay arranges panels into 2 or 3 columns depending on terminal width. Panels are distributed using a greedy height-balanced algorithm: each panel is placed into the shortest column (by `lipgloss.Height`). This produces roughly equal vertical heights across columns regardless of panel sizes, preventing clipping when the screen is tight.

### 14.3 Context Help (Quick Reference)

Edit keys are documented in the **List** and **Board** context help strings (`context_help.go`), appended to the Actions section in compact form:

```
Ctrl+p/o/a Set priority/status/assignee
Ctrl+t    Edit title │ O  Edit issue
Ctrl+n/g  New issue/sub │ Ctrl+x  Comment
```

The **Detail** context help updates the `O` key description from "Open in editor" to "Edit issue (terminal editor)".

---

## 15. File Structure

| File                        | Action     | Merge Risk       |
| --------------------------- | ---------- | ---------------- |
| `pkg/ui/human_edit.go`      | **NEW**    | None             |
| `pkg/ui/human_edit_test.go` | **NEW**    | None             |
| `pkg/ui/edit_modal.go`      | **NEW**    | None             |
| `pkg/ui/edit_modal_test.go` | **NEW**    | None             |
| `pkg/ui/title_edit.go`      | **NEW**    | None             |
| `pkg/ui/title_edit_test.go` | **NEW**    | None             |
| `pkg/ui/model.go`           | **MODIFY** | High (minimized) |
| `pkg/ui/context_help.go`    | **MODIFY** | Low              |

### 15.1 Changes to model.go (~30 lines)

All additions marked with `// fork: human-edit` comments.

**1. Model struct fields** (appended at end):

```go
editConfig     EditConfig
pendingEdit    *PendingEdit
editPicker     EditPickerModal
editPickerKind editPickerKind
showEditPicker bool
titleEditState TitleEditState
```

**2. NewModel()** — one line:

```go
editConfig: LoadEditConfig(),
```

**3. Update() — message handlers** (appended to msg type switch):

```go
case editAppliedMsg:            → handleEditApplied(msg) + FileChangedMsg reload
case editErrorMsg:              → handleEditError(msg)
case editNoChangesMsg:          → handleEditNoChanges(msg)
case editorFinishedMsg:         → handleEditorFinished(msg)
case pollEditMsg:               → handleEditPoll()
case createAndEditMsg:          → handleCreateAndEdit(msg)
case commentEditorFinishedMsg:  → handleCommentEditorFinished(msg)
```

**4. Update() — key dispatch** (two insertion points):

**Before the global key switch** (right after the time-travel input handler, before `if m.list.FilterState() != list.Filtering`):
```go
// Title edit intercept — must run before global switch steals Esc/q/j/k
if m.titleEditState.Active { handleTitleEditKey(msg); update status bar }
// Edit picker intercept — must run before global switch steals Esc/Enter
if m.showEditPicker { forward to picker; check result }
```

This placement is critical: the global key switch has `case "esc"` (quit confirm) and `case "q"` (quit) which would steal keys from the title editor and picker if they ran after the switch. The title edit and picker handlers allow only `ctrl+c` to pass through for force-quit.

**Inside the non-filtering block, before focus-specific dispatch:**
```go
// Edit hotkey dispatch (ctrl+p, ctrl+o, ctrl+a, ctrl+t, O, ctrl+n, ctrl+g, ctrl+x)
if m2, cmd, handled := m.tryEditKeyHandler(msg.String()); handled { ... }
```

`tryEditKeyHandler` returns a 3-tuple `(Model, tea.Cmd, bool)`. The `bool` indicates whether the key was handled. This is necessary because some edit operations (picker openers, title edit) return `nil` cmd but still modify the model — and since `Update()` uses a value receiver, the modified model must be returned even without a cmd.

**5. View()** — one overlay:

```go
} else if m.showEditPicker {
    body = m.editPicker.View(m.theme, m.width, m.height-1)
```

**6. renderHelpOverlay()** — one panel added:

```go
editSection := []struct{ key, desc string }{ ... }
renderPanel("Editing", "✏", 3, editSection)
```

### 15.2 Key Dispatch Guard

`tryEditKeyHandler` is called from `Update()` inside the `if m.list.FilterState() != list.Filtering` block, after all modal/overlay handlers but before the focus-specific `switch m.focused`.

It returns `(m, nil, false)` (no-op) when:

- Any modal, picker, or overlay is active
- Focus is not `focusList`, `focusBoard`, or `focusDetail`

For the `O` key specifically: if `openIssueInEditor()` returns `nil` cmd (meaning the editor is not a terminal editor), `tryEditKeyHandler` returns `handled=false` so the existing `case "O": m.openInEditor()` in `handleListKeys` executes normally.

### 15.3 Existing `openInEditor()` — Left Untouched

The existing `openInEditor()` function and its `case "O"` in `handleListKeys` are completely unchanged. The smart dispatch intercepts `O` only when a terminal editor is detected.

---

## 16. Known Issues and Workarounds

### 16.1 Mixed-Prefix Workspaces

When the JSONL file contains issues with different ID prefixes (e.g. `bd-*` and `bv-*`), `br`'s auto-import fails with "Prefix mismatch". All `br` subprocess calls from bv include `--no-auto-import` to bypass this. This is safe because bv only needs to write via the SQLite DB, not re-import from JSONL.

### 16.2 br stderr Corruption

`br` outputs JSON error envelopes to stderr on failure. `RunBrUpdate` captures stderr into a `strings.Builder` (never `os.Stderr`) and extracts the human-readable `message` field from the JSON envelope for display in the status bar.

### 16.3 Markdown LSP Formatter Indentation

Markdown LSP formatters (e.g. in helix with format-on-save) may indent closing tags like `    </description>` when there is content inside. The serializer emits a blank line before each closing tag to prevent this. The parser uses `strings.TrimSpace()` on every line before tag comparison, so indented closing tags are handled correctly regardless.

---

### 16.4 Terminal Ctrl Key Aliases

Some `ctrl+` combinations send the same byte as common keys and cannot be used as distinct hotkeys: `ctrl+m` = Enter (0x0D), `ctrl+i` = Tab (0x09), `ctrl+h` = Backspace (0x08), `ctrl+[` = Escape (0x1B). The hotkey system avoids all of these.

---

## 17. Test Coverage

### 17.1 human_edit_test.go

- Config: default values (including `AddComment`), loading from file, fallback for unset fields
- Snapshot: construction from `model.Issue`, parsing from JSON (object and array), empty/invalid JSON
- Markdown: serialization (frontmatter fields, XML tags, repo omission, special chars in title), parsing (all fields, indented closing tags, markdown headings inside content, angle brackets inside/outside sections, nested known tag names as content, closing tag only matching current section, missing closing tag)
- Roundtrip: full roundtrip with all fields, empty sections, content with markdown headings + HTML + comparisons
- Diff: no changes, all changed, whitespace trimming, partial change
- BuildUpdateArgv: all fields, empty diff, single field, `--no-auto-import` always present
- Assignee collection: dedup, sort, extras, empty
- Label parsing: commas, single, empty, whitespace
- `IsTerminalEditor()`: hx, vim, nvim, vi, nano, emacs, pico, joe, ne, code, gedit, paths
- YAML escaping: roundtrip for titles with colons, brackets, quotes, dashes, spaces
- Error extraction: valid JSON envelope, plain text, empty message, malformed JSON
- Known constants: statuses count, priority labels count

### 17.2 edit_modal_test.go

- Construction, cursor clamping
- Navigation: j/k, arrow keys, bounds checking
- Accept/cancel: Enter, Esc
- Digit shortcuts: 0-4 jump cursor, out-of-range ignored, no auto-confirm
- Non-key message ignored
- Constant distinctness

### 17.3 title_edit_test.go

- Default state, inactive rendering
- Bar cursor rendering: at end (`widget│`), middle (`w│idget`), start (`│widget`)
- All key handlers: Esc, Backspace (including at start), Delete (including at end), Left/Right (including bounds), Home, End (via ctrl+e), Ctrl+k
- Space insertion (explicit `case " "` for `tea.KeySpace`)
- Rune insert: middle, end
- Inactive state returns unhandled
- UTF-8 multi-byte character handling
