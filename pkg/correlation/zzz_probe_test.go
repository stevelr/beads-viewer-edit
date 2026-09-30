package correlation

import (
	"testing"

	json "github.com/goccy/go-json"
)

// Verify marshaling a *historyArtifact (pointer, as stored in cache entry) routes
// through the value-receiver MarshalJSON, and full cache file round-trips.
func TestProbeCacheEntryRoundTrip(t *testing.T) {
	art := &historyArtifact{
		Events:  []BeadEvent{{BeadID: "bv-9", EventType: EventCreated}},
		Commits: []CorrelatedCommit{{SHA: "aaa", BeadID: "bv-9"}, {SHA: "bbb", BeadID: ""}},
	}
	// As the size-cap preflight does: json.Marshal(art) where art is *historyArtifact
	pre, err := json.Marshal(art)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("preflight bytes: %s", pre)
	if !contains(string(pre), "commit_bead_ids") {
		t.Errorf("preflight marshal did NOT route through custom codec (no commit_bead_ids): %s", pre)
	}

	// Now the full cache file with pointer entry
	cf := headArtifactCacheFile{Version: 1, Entries: map[string]headArtifactCacheEntry{
		"k": {HeadSHA: "h", OptsHash: "o", Artifact: art},
	}}
	data, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "commit_bead_ids") {
		t.Errorf("cache-file marshal did NOT route through custom codec: %s", data)
	}
	var back headArtifactCacheFile
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	e := back.Entries["k"]
	if e.Artifact == nil {
		t.Fatal("artifact nil after roundtrip")
	}
	if e.Artifact.Commits[0].BeadID != "bv-9" || e.Artifact.Commits[1].BeadID != "" {
		t.Errorf("BeadID lost: %+v", e.Artifact.Commits)
	}
}

// Nil Commits / nil Events.
func TestProbeNilSlices(t *testing.T) {
	art := &historyArtifact{}
	data, err := json.Marshal(art)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("nil-slice marshal: %s", data)
	var back historyArtifact
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Commits != nil {
		t.Logf("Commits non-nil after roundtrip: %#v", back.Commits)
	}
}

// Length mismatch / corrupt cache: more Commits than CommitBeadIDs (shorter).
// The decoder now rejects mismatched parallel arrays outright (an error, never
// a panic); cache readers treat the decode error as a miss and recompute.
func TestProbeShorterBeadIDs(t *testing.T) {
	raw := `{"events":null,"commits":[{"sha":"a"},{"sha":"b"},{"sha":"c"}],"commit_bead_ids":["x"]}`
	var back historyArtifact
	err := json.Unmarshal([]byte(raw), &back)
	if err == nil {
		t.Fatal("expected corrupt artifact (shorter commit_bead_ids) to be rejected")
	}
	t.Logf("shorter CommitBeadIDs rejected without panic: %v", err)
}

// LONGER CommitBeadIDs than commits (corrupt): must be rejected, not panic.
func TestProbeLongerBeadIDs(t *testing.T) {
	raw := `{"commits":[{"sha":"a"}],"commit_bead_ids":["x","y","z"]}`
	var back historyArtifact
	err := json.Unmarshal([]byte(raw), &back)
	if err == nil {
		t.Fatal("expected corrupt artifact (longer commit_bead_ids) to be rejected")
	}
	t.Logf("longer CommitBeadIDs rejected without panic: %v", err)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
