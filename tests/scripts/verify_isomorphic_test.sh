#!/usr/bin/env bash
# verify_isomorphic_test.sh: proves scripts/verify_isomorphic.sh never touches
# the caller's worktree (G5). It clones this repository into a temp directory,
# dirties the clone, runs the script there, and checks that:
#   1. an invalid or option-looking ref fails before any git mutation;
#   2. after a real run the dirty file, the index, and the stash are unchanged
#      and no baseline worktree is left registered.
# Usage: tests/scripts/verify_isomorphic_test.sh   (needs go and git)
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/bv-iso-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

clone="$tmp/clone"
git clone -q "$root" "$clone"
# The script builds ./cmd/bv in both trees; vendor/ must come along.
if [ -d "$root/vendor" ] && [ ! -d "$clone/vendor" ]; then
  cp -r "$root/vendor" "$clone/vendor"
fi
# Test the script as it is in the working tree, not the committed copy.
cp "$root/scripts/verify_isomorphic.sh" "$clone/scripts/verify_isomorphic.sh"
cd "$clone"
git config user.email iso@example.com
git config user.name iso
baseline=$(git rev-parse HEAD~1)

# Pre-existing stash entry first (git stash push without a pathspec would
# otherwise swallow the dirty marker below), then dirty the tree with one
# tracked modification and one untracked file.
echo "stashed" > stashed_marker.txt
git add stashed_marker.txt && git stash push -q -m "pre-existing stash" -- stashed_marker.txt
echo "// dirty marker" >> pkg/model/types.go
echo "untracked" > untracked_marker.txt
snapshot() { { git status --porcelain=v1 --untracked-files=all; git diff; git stash list; git worktree list; } | sha256sum | cut -d' ' -f1; }
before=$(snapshot)

fail=0
# 1. Invalid ref: must fail (exit 2) and change nothing.
if scripts/verify_isomorphic.sh "no-such-ref-$$" >/dev/null 2>&1; then
  echo "FAIL: invalid ref was accepted"; fail=1
fi
if scripts/verify_isomorphic.sh "--output=/tmp/x" >/dev/null 2>&1; then
  echo "FAIL: option-looking ref was accepted"; fail=1
fi
if [ "$(snapshot)" != "$before" ]; then
  echo "FAIL: rejected refs still mutated the worktree"; fail=1
fi

# 2. Real run against HEAD~1 (outputs may legitimately differ; only the tree matters).
set +e
scripts/verify_isomorphic.sh "$baseline" >"$tmp/run.log" 2>&1
rc=$?
set -e
echo "verify_isomorphic.sh exit=$rc (0 = isomorphic, 1 = outputs differ; both acceptable here)"
if [ "$rc" -ne 0 ] && [ "$rc" -ne 1 ]; then
  echo "FAIL: unexpected exit $rc"; tail -20 "$tmp/run.log"; fail=1
fi
if [ "$(snapshot)" != "$before" ]; then
  echo "FAIL: the run changed the caller's tree/index/stash/worktrees:"
  git status --porcelain=v1 --untracked-files=all; git stash list; git worktree list
  fail=1
fi
if ! grep -q 'dirty marker' pkg/model/types.go; then
  echo "FAIL: tracked modification lost"; fail=1
fi
if [ "$(git stash list | wc -l)" -ne 1 ]; then
  echo "FAIL: stash list changed"; fail=1
fi
if [ "$(git worktree list | wc -l)" -ne 1 ]; then
  echo "FAIL: baseline worktree left behind"; git worktree list; fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "verify_isomorphic_test: PASS (caller's tree untouched, invalid refs rejected before mutation)"
fi
exit "$fail"
