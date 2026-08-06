package correlation

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestSnapshotMatchesLegacyPatch is a differential test: extractViaSnapshots
// must produce the same BeadEvents as the legacy extractViaGitLogPatch on a real
// repo. Point it at a beads repo via BV_DIFFCHECK_REPO; skipped otherwise.
func TestSnapshotMatchesLegacyPatch(t *testing.T) {
	repo := os.Getenv("BV_DIFFCHECK_REPO")
	if repo == "" {
		t.Skip("set BV_DIFFCHECK_REPO to a beads git repo to run the differential check")
	}
	e := NewExtractor(repo)
	opts := ExtractOptions{Limit: 200}

	legacy, err := e.extractViaGitLogPatch(opts)
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	snap, err := e.extractViaSnapshots(opts)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	key := func(ev BeadEvent) string {
		return fmt.Sprintf("%s|%s|%s|%s", ev.CommitSHA, ev.BeadID, ev.EventType, ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"))
	}
	ls := make([]string, 0, len(legacy))
	for _, ev := range legacy {
		ls = append(ls, key(ev))
	}
	ss := make([]string, 0, len(snap))
	for _, ev := range snap {
		ss = append(ss, key(ev))
	}
	sort.Strings(ls)
	sort.Strings(ss)

	t.Logf("legacy events=%d snapshot events=%d", len(ls), len(ss))
	if len(ls) != len(ss) {
		t.Fatalf("event count mismatch: legacy=%d snapshot=%d", len(ls), len(ss))
	}
	for i := range ls {
		if ls[i] != ss[i] {
			t.Fatalf("event mismatch at %d:\n legacy=%s\n snap  =%s", i, ls[i], ss[i])
		}
	}
}
