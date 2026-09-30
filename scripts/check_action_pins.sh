#!/usr/bin/env bash
# check_action_pins.sh: every GitHub Actions `uses:` reference must be pinned to
# a 40-hex commit SHA (mutable tags can be moved under a supply-chain attack).
# Scans .github/workflows/*.yml and the workflow template bv generates for
# GitHub Pages deployments (pkg/export/github.go). Exit 1 on any violation.
#
# Usage: scripts/check_action_pins.sh [file ...]   (default: the two sources above)
set -euo pipefail

cd "$(dirname "$0")/.."

files=("$@")
if [ ${#files[@]} -eq 0 ]; then
  while IFS= read -r f; do files+=("$f"); done < <(ls .github/workflows/*.yml .github/workflows/*.yaml 2>/dev/null || true)
  files+=(pkg/export/github.go)
fi

violations=0
checked=0
for f in "${files[@]}"; do
  [ -f "$f" ] || { echo "check_action_pins: missing file $f" >&2; violations=$((violations+1)); continue; }
  # Match `uses: owner/repo@ref` (also inside Go string literals). Local
  # actions (`uses: ./path`) and docker references carry no ref to pin.
  while IFS= read -r line; do
    checked=$((checked+1))
    ref=$(printf '%s' "$line" | sed -E 's/.*uses:[[:space:]]*["'"'"']?[^@[:space:]"'"'"']+@([^[:space:]"'"'"'#]+).*/\1/')
    if ! printf '%s' "$ref" | grep -Eq '^[0-9a-f]{40}$'; then
      echo "UNPINNED: $f: $(printf '%s' "$line" | sed -E 's/^[[:space:]]+//')"
      violations=$((violations+1))
    fi
  done < <(grep -nE 'uses:[[:space:]]*["'"'"']?[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@' "$f" || true)
done

echo "check_action_pins: $checked action reference(s) checked in ${#files[@]} file(s), $violations unpinned"
[ "$violations" -eq 0 ]
