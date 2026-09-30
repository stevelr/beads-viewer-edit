# Analysis engine benchmarks (measured)

Measured 2026-09-02 on this repository's tracker (611 issues, 746 edges) with:

```
go test ./pkg/analysis -run '^$' -bench 'RealData_(FullTriage|FullAnalysis|GraphBuild)$' -benchtime=5x -benchmem
```

- Go: `go1.25.5 linux/amd64`
- CPU: AMD EPYC-Milan (shared VM, 8 hardware threads visible to Go)
- `BenchmarkRealData_FullAnalysis` uses `FullAnalysisConfig` (exact betweenness, all Phase 2 metrics)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| `BenchmarkRealData_FullTriage` | 1,213,159 (1.21 ms) | 612,406 | 4,461 |
| `BenchmarkRealData_FullAnalysis` | 42,929,145 (42.9 ms) | 2,258,521 | 16,441 |
| `BenchmarkRealData_GraphBuild` | 743,300 (0.74 ms) | 428,472 | 3,244 |

Five iterations each; this is a point measurement on a shared machine, not a
regression baseline. The regression baseline with `benchstat` comparison is
tracker item H6 (`benchmarks/baseline.txt`, `scripts/benchmark.sh`).
