# Hypothesis Ledger — `bv --robot-triage` slowness

Triangulated via three orthogonal angles per claim (strace / time / source / perf / bench).

```
graph analysis is the bottleneck      : REJECTS — warm FullTriage = 1.26 ms; cold plan = 904 ms with only 2 git calls and a cache hit. Analysis is ~0.04% of triage wall.

correlation git fan-out dominates     : SUPPORTS — strace shows 336 `git show` (168 commits × 2). Removing correlation (plan/insights) drops git execs 340→2 and wall 2.87s→0.9s. /usr/bin/time sys=1.31s matches fork/exec cost.

two git calls per commit are needed   : REJECTS (opportunity) — name-status + numstat over the same SHA can be one `git show --name-status --numstat`, or all commits in ONE `git log --name-status --numstat <range>` (336→1). franken_networkx "batch, don't fan-out" pattern.

disk cache makes repeat calls fast    : REJECTS — the cache READ+REWRITE of a 6.6 MB file (stdlib encoding/json) is itself ~31% CPU / ~0.9s on every call, even on a hit (rewrites whole file to bump LRU AccessedAt under a flock). Self-inflicted floor.

stdlib encoding/json vs goccy         : SUPPORTS — perf shows encoding/json.Unmarshal 13.9% + Encode 13.8% in the cache path; the project already vendors goccy/go-json (used in loader) but the cache uses stdlib.

cache is big because of string maps    : SUPPORTS (contributing) — GraphStats serializes ~12 per-node map[string]float64; 757 nodes → 6.6 MB JSON. int-indexed arrays / compact encoding would shrink this an order of magnitude and speed (de)serialize.

network update check blocks triage    : REJECTS — only 1 bv exec + 340 git; no network execs in strace; update check is detached with timeout.

insights 8.4s = same floor            : REJECTS — insights has only 2 git calls; its 7.5s beyond the floor is exact betweenness O(V·E) + cycles + HITS + eigenvector on 757 nodes (rank 3), a separate target.

it's I/O-wait bound                    : REJECTS — plan user CPU 1046 ms > wall 904 ms ⇒ CPU-bound across cores; nanosleep/futex in `strace -w` are summed-across-threads artifacts, not real waits.
```

## Optimization ordering for extreme-software-optimization (Impact×Confidence/Effort)
1. **Rank 2 first** (disk cache): highest confidence, affects ALL robot commands, low blast radius. Options: don't rewrite on read-hit (separate small LRU sidecar / atomic touch), switch to goccy, compact int-indexed encoding, gzip. Biggest single lever for the whole tool.
2. **Rank 1** (correlation git fan-out): batch 336 git calls → 1 `git log --name-status --numstat`. Triage-specific, large win, medium effort (parse a combined stream — `extractor_snapshot.go` already parses name-status streams).
3. **Rank 3** (insights exact Phase-2): size-gate to approximate betweenness sooner / parallelize Brandes / iterative-convergence HITS+eigenvector (mine franken_networkx techniques).
4. **Rank 4** (string-keyed maps → int-indexed): compounds with #2 (smaller cache) and #3 (faster metric write-back).
5. **Rank 5** (loader allocs + ComputeDataHash): trim 22.8k allocs, incremental/partial hashing.

Guardrails for every round: keep a golden `--robot-triage`/`--robot-plan`/`--robot-insights` JSON, run `go test ./...`, `go vet`, `gofmt -l`, `ubs`, and re-benchmark with hyperfine before claiming a win. One lever per round.
