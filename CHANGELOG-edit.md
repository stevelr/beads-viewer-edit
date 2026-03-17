# Changelog — Human Edit Features

This file is a log of changes since the last update of br-human-edit.md which is the current effective PRD for human-edit changes. After syncing with br-human-edit.md, the changes listed here can be removed.

## Unreleased

### Changed

- **Generic editor support:** Replaced helix-specific `helix_path` config field
  with `editor_path`. The default is now resolved from `$EDITOR`, then `$VISUAL`,
  then `"vi"`. Existing `bv-edit.yaml` files should rename `helix_path` to
  `editor_path`.

- **Unified terminal editor detection:** Merged the separate editor lists from
  `human_edit.go` and `model.go` into a single `IsTerminalEditor()` function
  backed by the `terminalEditorExecutables` map in `model.go`. Added `hx` to
  the shared list. Recognized terminal editors: vim, vi, nvim, nano, emacs,
  pico, joe, ne, hx.

- **Dynamic status messages:** Status bar messages now show the actual editor
  name (e.g. "Editing BD-1 in vim") instead of hardcoded "helix".

### Fixed

- **Empty new issues cleaned up on cancel:** When creating a new issue
  (ctrl+n / ctrl+g), if the user closes the editor without making changes, the
  empty issue is automatically deleted via `br delete`. Previously the stub
  issue would remain in the database. Applies to both synchronous and WezTerm
  async editor modes.
