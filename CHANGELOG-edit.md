# Changelog — Human Edit Features

This file is a log of changes since the last update of bv-human-edit.md which is the current effective PRD for human-edit changes. After syncing with bv-human-edit.md, the changes listed here can be removed.

## Unreleased

- Merged upstream v0.25.1 (4db2f8dc). No human-edit code changes were
  needed. Upstream's shortcuts sidebar (`;`) now lays views out beside it.
  The edit picker is in the overlay chain, so like help and the other
  pickers it is drawn full-width without the sidebar.
- Merged upstream v0.25.0 (87cee25). Upstream now runs the editor-exit
  `br update` asynchronously. It now uses the configured `br_path`, and the
  immediate reload happens when the update result arrives. The edit-key guard
  no longer checks the removed `showAttentionView` flag: the attention view is
  now a focus mode, which the list/board/detail focus check already excludes.
  README's key table now lists `ctrl+y` and `ctrl+x`.
- Merged upstream v0.22.0 (95a706c). Upstream made `*Model` the `tea.Model`
  implementation, so the human-edit / title-edit `Model` methods now use
  pointer receivers and return `*Model`; no behavioral change.
