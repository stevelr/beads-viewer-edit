# TOON versus JSON payload size

Measured 2026-09-02 on this repository (611 beads, 500-commit correlation
window) with `bv` at the reality-check commit, `toon` CLI 0.x from
`~/.local/bin/toon`, bytes on stdout. Token estimates from `--stats` track the
byte ratio closely (`--robot-triage`: JSON≈4669 tok, TOON≈5278 tok).

| Command | JSON bytes | TOON bytes | TOON / JSON | Verdict |
|---------|-----------:|-----------:|------------:|---------|
| `--robot-graph` | 148,053 | 138,115 | 0.93 | TOON smaller (wide adjacency tables) |
| `--robot-next` | 1,361 | 1,332 | 0.98 | about equal |
| `--robot-alerts` | 2,936 | 2,934 | 1.00 | about equal |
| `--robot-label-health` | 195,615 | 213,881 | 1.09 | JSON smaller |
| `--robot-insights` | 73,219 | 82,111 | 1.12 | JSON smaller |
| `--robot-triage` | 18,668 | 21,101 | 1.13 | JSON smaller |
| `--robot-plan` | 5,796 | 6,684 | 1.15 | JSON smaller |

Reading: TOON wins only on payloads dominated by uniform rows (the graph's
node and edge tables). Nested, heterogeneous payloads (triage
recommendations with reasons and unblock lists, insight metric maps) encode
larger. The README therefore recommends `--format toon` for `--robot-graph`
and tells agents to check `--stats` for anything else.

`tests/e2e/toon_size_test.go` re-measures the commands above when the `toon`
CLI is installed and fails if TOON is more than 10% larger for the commands
documented as TOON wins (`--robot-graph`).
