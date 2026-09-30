package correlation

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	json "github.com/goccy/go-json"
)

// TestHistoryArtifactRoundTripPreservesBeadID guards a real bug: CorrelatedCommit.BeadID
// is tagged json:"-" (hidden from the public report), but the HEAD-artifact disk cache
// serializes the pre-assembly Commits slice. Without a custom codec, BeadID is dropped on
// round-trip and assembleReport (which groups commits onto beads by BeadID) returns
// commit-less histories on the middle-tier "edit a bead, re-triage" path.
func TestHistoryArtifactRoundTripPreservesBeadID(t *testing.T) {
	in := &historyArtifact{
		Events: []BeadEvent{{BeadID: "bv-9", EventType: EventCreated}},
		Commits: []CorrelatedCommit{
			{SHA: "aaa", BeadID: "bv-9", Method: "explicit", Confidence: 0.9},
			{SHA: "bbb", BeadID: "bv-7"},
			{SHA: "ccc", BeadID: ""}, // unlinked commit: empty must stay empty
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out historyArtifact
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Commits) != 3 {
		t.Fatalf("commit count: got %d want 3", len(out.Commits))
	}
	want := []string{"bv-9", "bv-7", ""}
	for i, w := range want {
		if out.Commits[i].BeadID != w {
			t.Errorf("Commits[%d].BeadID: got %q want %q", i, out.Commits[i].BeadID, w)
		}
	}
	// Other fields must still round-trip (regression guard for the wire struct).
	if out.Commits[0].SHA != "aaa" || out.Commits[0].Method != "explicit" || out.Commits[0].Confidence != 0.9 {
		t.Errorf("non-BeadID fields not preserved: %+v", out.Commits[0])
	}
	if len(out.Events) != 1 || out.Events[0].BeadID != "bv-9" {
		t.Errorf("events not preserved: %+v", out.Events)
	}
}

func TestPersistentCacheFreshnessRejectsFutureTimestamps(t *testing.T) {
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		createdAt time.Time
		wantFresh bool
	}{
		{name: "fresh", createdAt: now.Add(-time.Hour), wantFresh: true},
		{name: "age boundary", createdAt: now.Add(-correlationDiskCacheMaxAge), wantFresh: true},
		{name: "stale", createdAt: now.Add(-correlationDiskCacheMaxAge - time.Nanosecond)},
		{name: "future", createdAt: now.Add(time.Nanosecond)},
		{name: "zero", createdAt: time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cacheCreatedAtIsFresh(tt.createdAt, now, correlationDiskCacheMaxAge); got != tt.wantFresh {
				t.Fatalf("cacheCreatedAtIsFresh()=%v, want %v", got, tt.wantFresh)
			}
		})
	}

	reportEntries := map[string]correlationDiskCacheEntry{
		"fresh":  {CreatedAt: now.Add(-time.Hour)},
		"future": {CreatedAt: now.Add(time.Hour)},
		"stale":  {CreatedAt: now.Add(-correlationDiskCacheMaxAge - time.Second)},
		"zero":   {},
	}
	pruneCorrelationDiskCacheEntries(now, reportEntries)
	if len(reportEntries) != 1 {
		t.Fatalf("report prune retained %d entries, want only fresh", len(reportEntries))
	}
	if _, ok := reportEntries["fresh"]; !ok {
		t.Fatal("report prune removed fresh entry")
	}

	artifactEntries := map[string]headArtifactCacheEntry{
		"fresh":  {CreatedAt: now.Add(-time.Hour)},
		"future": {CreatedAt: now.Add(time.Hour)},
		"stale":  {CreatedAt: now.Add(-headArtifactCacheMaxAge - time.Second)},
		"zero":   {},
	}
	pruneHeadArtifactCacheEntries(now, artifactEntries)
	if len(artifactEntries) != 1 {
		t.Fatalf("artifact prune retained %d entries, want only fresh", len(artifactEntries))
	}
	if _, ok := artifactEntries["fresh"]; !ok {
		t.Fatal("artifact prune removed fresh entry")
	}
}

// TestAssembleReportGroupsCommitsAfterRoundTrip verifies the end-to-end consequence:
// after a cache round-trip, assembleReport still attaches commits to their beads.
func TestAssembleReportGroupsCommitsAfterRoundTrip(t *testing.T) {
	art := &historyArtifact{
		Commits: []CorrelatedCommit{
			{SHA: "aaa", BeadID: "bv-9", Method: "explicit", Confidence: 0.9},
			{SHA: "bbb", BeadID: "bv-9", Method: "temporal", Confidence: 0.5},
		},
	}
	b, _ := json.Marshal(art)
	var rt historyArtifact
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatal(err)
	}
	c := &Correlator{}
	beads := []BeadInfo{{ID: "bv-9", Title: "t", Status: "open"}}
	report := c.assembleReport(beads, CorrelatorOptions{}, &rt)
	h, ok := report.Histories["bv-9"]
	if !ok {
		t.Fatalf("no history for bv-9")
	}
	if len(h.Commits) != 2 {
		t.Errorf("bv-9 commits after round-trip: got %d want 2 (BeadID grouping broken)", len(h.Commits))
	}
	if len(report.CommitIndex) == 0 {
		t.Errorf("CommitIndex empty after round-trip (reverse lookup broken)")
	}
}

func TestCorrelationCacheNamespaceCanonicalAndIsolated(t *testing.T) {
	repo := t.TempDir()
	relPrimary := filepath.Join(".beads", "issues.jsonl")
	absPrimary := filepath.Join(repo, relPrimary)

	relNamespace := correlationCacheNamespace(repo, relPrimary)
	absNamespace := correlationCacheNamespace(filepath.Join(repo, "."), absPrimary)
	if relNamespace != absNamespace {
		t.Fatalf("equivalent primary paths produced different namespaces: relative=%q absolute=%q", relNamespace, absNamespace)
	}

	legacyNamespace := correlationCacheNamespace(repo, filepath.Join(".beads", "beads.jsonl"))
	if relNamespace == legacyNamespace {
		t.Fatal("different selected Beads histories shared a cache namespace")
	}

	otherRepoNamespace := correlationCacheNamespace(t.TempDir(), relPrimary)
	if relNamespace == otherRepoNamespace {
		t.Fatal("different repositories shared a cache namespace")
	}
}

func TestPersistentCorrelationCachesIsolateNamespaces(t *testing.T) {
	t.Setenv("BV_ROBOT", "1")
	t.Setenv("BV_NO_CACHE", "")
	t.Setenv("BV_CACHE_DIR", t.TempDir())

	const (
		namespaceA = "repo-a:issues"
		namespaceB = "repo-b:issues"
		headSHA    = "same-head"
		beadsHash  = "same-beads"
		optsHash   = "same-options"
	)

	// The report cache validates entry.Report.DataHash against the beads hash
	// key, so distinguish the two namespaces via GitRange instead.
	putCorrelationDiskCachedReport(namespaceA, headSHA, beadsHash, optsHash, &HistoryReport{DataHash: beadsHash, GitRange: "report-a"})
	putCorrelationDiskCachedReport(namespaceB, headSHA, beadsHash, optsHash, &HistoryReport{DataHash: beadsHash, GitRange: "report-b"})
	for namespace, want := range map[string]string{namespaceA: "report-a", namespaceB: "report-b"} {
		got, ok := getCorrelationDiskCachedReport(namespace, headSHA, beadsHash, optsHash)
		if !ok || got.GitRange != want {
			t.Fatalf("report cache namespace %q = (%+v, %v), want git range %q", namespace, got, ok, want)
		}
	}

	putHeadArtifactCached(namespaceA, headSHA, optsHash, &historyArtifact{Events: []BeadEvent{{BeadID: "artifact-a"}}})
	putHeadArtifactCached(namespaceB, headSHA, optsHash, &historyArtifact{Events: []BeadEvent{{BeadID: "artifact-b"}}})
	for namespace, want := range map[string]string{namespaceA: "artifact-a", namespaceB: "artifact-b"} {
		got, ok := getHeadArtifactCached(namespace, headSHA, optsHash)
		if !ok || len(got.Events) != 1 || got.Events[0].BeadID != want {
			t.Fatalf("artifact cache namespace %q = (%+v, %v), want bead %q", namespace, got, ok, want)
		}
	}
}

// TestHeadArtifactCache_VersionMismatchIsMiss (D2): an on-disk artifact
// written by an older format is a miss, never migrated, and the next put
// overwrites it with the current format.
func TestHeadArtifactCache_VersionMismatchIsMiss(t *testing.T) {
	t.Setenv("BV_ROBOT", "1")
	t.Setenv("BV_NO_CACHE", "")
	t.Setenv("BV_CACHE_DIR", t.TempDir())
	const namespace, headSHA, optsHash = "repo:issues", "head-1", "opts-1"

	putHeadArtifactCached(namespace, headSHA, optsHash, &historyArtifact{WalkedCommits: 7, Events: []BeadEvent{{BeadID: "bv-1"}}})
	if got, ok := getHeadArtifactCached(namespace, headSHA, optsHash); !ok || got.WalkedCommits != 7 || got.FormatVersion != historyArtifactFormatVersion {
		t.Fatalf("precondition: fresh put should hit with the current format, got (%+v, %v)", got, ok)
	}

	// Age the stored entry to the previous format the way an old binary would have left it.
	path, err := headArtifactCachePath(false)
	if err != nil {
		t.Fatalf("cache path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cf headArtifactCacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("decode cache: %v", err)
	}
	if len(cf.Entries) != 1 {
		t.Fatalf("expected one cache entry, got %d", len(cf.Entries))
	}
	for key, entry := range cf.Entries {
		entry.Artifact.FormatVersion = historyArtifactFormatVersion - 1
		cf.Entries[key] = entry
	}
	stale, _ := json.Marshal(cf)
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatalf("write stale cache: %v", err)
	}

	if got, ok := getHeadArtifactCached(namespace, headSHA, optsHash); ok {
		t.Fatalf("format v%d entry must be a miss, got hit %+v", historyArtifactFormatVersion-1, got)
	}

	// A foreign-version artifact is never persisted; a current one replaces the stale entry.
	putHeadArtifactCached(namespace, headSHA, optsHash, &historyArtifact{FormatVersion: historyArtifactFormatVersion + 1, WalkedCommits: 99})
	if _, ok := getHeadArtifactCached(namespace, headSHA, optsHash); ok {
		t.Fatalf("a foreign-version artifact must not be written over the stale entry")
	}
	putHeadArtifactCached(namespace, headSHA, optsHash, &historyArtifact{WalkedCommits: 8})
	if got, ok := getHeadArtifactCached(namespace, headSHA, optsHash); !ok || got.WalkedCommits != 8 {
		t.Fatalf("current-format put should replace the stale entry, got (%+v, %v)", got, ok)
	}
}

// TestHeadArtifactCache_RoundTripKeepsStrategyFields (D2): the multi-strategy
// artifact (explicit and temporal candidate sets, per-commit method lists,
// walk size, strategy runs) survives the disk cache intact.
func TestHeadArtifactCache_RoundTripKeepsStrategyFields(t *testing.T) {
	t.Setenv("BV_ROBOT", "1")
	t.Setenv("BV_NO_CACHE", "")
	t.Setenv("BV_CACHE_DIR", t.TempDir())
	const namespace, headSHA, optsHash = "repo:issues", "head-2", "opts-2"

	var runs []StrategyRun
	if err := json.Unmarshal([]byte(`[{"name":"explicit_id","matches":1},{"name":"temporal_author","matches":2}]`), &runs); err != nil {
		t.Fatalf("strategy run fixture: %v", err)
	}
	var temporal []temporalCandidate
	if err := json.Unmarshal([]byte(`[{"sha":"ccc","bead_id":"bv-2"}]`), &temporal); err != nil {
		t.Fatalf("temporal fixture: %v", err)
	}
	in := &historyArtifact{
		Events:        []BeadEvent{{BeadID: "bv-1", EventType: EventCreated}},
		Commits:       []CorrelatedCommit{{SHA: "aaa", BeadID: "bv-1", Method: MethodCoCommitted, Methods: []string{"co_committed", "explicit_id"}, Confidence: 0.9, Message: "feat(bv-1): x"}},
		Explicit:      []CorrelatedCommit{{SHA: "bbb", BeadID: "bv-1", Method: MethodExplicitID, Methods: []string{"explicit_id"}, Confidence: 0.95, Message: "fix bv-1"}},
		Temporal:      temporal,
		WalkedCommits: 42,
		Strategies:    runs,
	}
	putHeadArtifactCached(namespace, headSHA, optsHash, in)
	out, ok := getHeadArtifactCached(namespace, headSHA, optsHash)
	if !ok {
		t.Fatalf("expected a cache hit")
	}
	if out.WalkedCommits != 42 || len(out.Strategies) != 2 || len(out.Temporal) != 1 || len(out.Explicit) != 1 || len(out.Commits) != 1 {
		t.Fatalf("round trip lost strategy fields: %+v", out)
	}
	if out.Explicit[0].BeadID != "bv-1" || out.Explicit[0].Message != "fix bv-1" || out.Explicit[0].Confidence != 0.95 {
		t.Fatalf("explicit set not preserved: %+v", out.Explicit[0])
	}
	if len(out.Commits[0].Methods) != 2 || out.Commits[0].Methods[1] != "explicit_id" || out.Commits[0].BeadID != "bv-1" {
		t.Fatalf("per-commit methods/bead id not preserved: %+v", out.Commits[0])
	}
	if out.FormatVersion != historyArtifactFormatVersion {
		t.Fatalf("stored format version %d, want %d", out.FormatVersion, historyArtifactFormatVersion)
	}
}
