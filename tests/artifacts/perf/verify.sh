#!/usr/bin/env bash
# Reusable per-round verification for the bv robot-path optimization loop.
# Usage: bash tests/artifacts/perf/verify.sh [bench]
#   - builds bv to /tmp/bv_cand
#   - checks golden-output equivalence (normalized) for triage/plan/insights/next
#   - if "bench" arg given, runs hyperfine vs the recorded baselines
# Exit 0 = goldens match. Non-zero = a regression.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"
G=tests/artifacts/perf/golden
# Normalize volatile fields: timestamps, all *ms timing (int or float), and
# round every float to 4 decimals to absorb ~1e-10 summation-order jitter.
norm() {
  sed -E \
    -e 's/"generated_at":"[^"]*"/"generated_at":"X"/g' \
    -e 's/"computed_at":"[^"]*"/"computed_at":"X"/g' \
    -e 's/,"compute_time_ms":[0-9.]+//g' \
    -e 's/,"ms":[0-9.]+//g' \
    -e 's/,"[a-zA-Z_]*_ms":[0-9.]+//g' \
    -e 's/"compute_time_ms":[0-9.]+,//g' \
    -e 's/"ms":[0-9.]+,//g' \
    -e 's/"[a-zA-Z_]*_ms":[0-9.]+,//g' \
    -e 's/([0-9]+\.[0-9]{4})[0-9]+/\1/g' \
    -e 's/[0-9]+ (days?)/N \1/g' \
    -e 's/"([a-z_]*_days)":[0-9]+/"\1":0/g'
}
# day-count fields (staleness "N days", *_days) are time-relative and drift daily
# regardless of code; normalized so goldens stay stable across calendar days.

echo "== build =="
go build -o /tmp/bv_cand ./cmd/bv || { echo "BUILD FAILED"; exit 2; }

rc=0
echo "== golden equivalence =="
for cmd in robot-triage robot-plan robot-insights robot-next; do
  /tmp/bv_cand --$cmd 2>/dev/null | norm > /tmp/cand-$cmd.json
  if diff -q "$G/$cmd.json" /tmp/cand-$cmd.json >/dev/null 2>&1; then
    echo "  OK   $cmd"
  else
    echo "  DIFF $cmd  (review: diff $G/$cmd.json /tmp/cand-$cmd.json)"
    rc=1
  fi
done

if [ "${1:-}" = "bench" ]; then
  echo "== hyperfine (warmup 2, 12 runs) =="
  hyperfine --warmup 2 --runs 12 \
    '/tmp/bv_cand --robot-triage' \
    '/tmp/bv_cand --robot-next' \
    '/tmp/bv_cand --robot-plan' \
    '/tmp/bv_cand --robot-insights' 2>&1 | grep -E "Benchmark|Time \(mean"
fi
exit $rc
