# Changelog — Human Edit Features

This file is a log of changes since the last update of bv-human-edit.md which is the current effective PRD for human-edit changes. After syncing with bv-human-edit.md, the changes listed here can be removed.

## Unreleased

- Merged upstream v0.22.0 (95a706c). Upstream made `*Model` the `tea.Model`
  implementation, so the human-edit / title-edit `Model` methods now use
  pointer receivers and return `*Model`; no behavioral change.
