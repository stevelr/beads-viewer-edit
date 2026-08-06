package analysis

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	json "github.com/goccy/go-json"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

func TestCacheSetTTLAndHash(t *testing.T) {
	issues := []model.Issue{{ID: "C1", Title: "Cache"}}
	c := NewCache(10 * time.Second)
	stats := &GraphStats{NodeCount: 1}
	c.Set(issues, stats)
	if c.Hash() == "" {
		t.Fatalf("expected hash after Set")
	}

	// Override TTL and ensure GetByHash respects expiry
	c.SetTTL(-1 * time.Second)
	if got, ok := c.Get(issues); got != nil || ok {
		t.Fatalf("expected cache miss after expired TTL")
	}
}

// TestGraphStatsCacheBlob_SoARoundTrip is a regression guard for the SoA
// (struct-of-arrays / dictionary-encoded) on-disk format: GraphStats →
// graphStatsCacheBlob → JSON (compact columnar) → graphStatsCacheBlob must
// reproduce value-identical metric maps, including the sparse and nil cases
// that distinguish absent vs. present-zero and nil vs. empty maps.
func TestGraphStatsCacheBlob_SoARoundTrip(t *testing.T) {
	cases := map[string]graphStatsCacheBlob{
		"dense": {
			OutDegree:        map[string]int{"A": 1, "B": 0, "C": 3},
			InDegree:         map[string]int{"A": 0, "B": 2, "C": 1},
			TopologicalOrder: []string{"A", "B", "C"},
			Density:          0.5,
			NodeCount:        3,
			EdgeCount:        4,
			PageRank:         map[string]float64{"A": 0.1, "B": 0.0, "C": 0.4},
			Betweenness:      map[string]float64{"A": 0, "B": 0, "C": 0},
			Eigenvector:      map[string]float64{"A": 0.7, "B": 0.2, "C": 0.99},
			Hubs:             map[string]float64{"A": 1, "B": 2, "C": 3},
			Authorities:      map[string]float64{"A": 3, "B": 2, "C": 1},
			CriticalPathScore: map[string]float64{
				"A": 5.5, "B": 0, "C": 2.25,
			},
			CoreNumber:   map[string]int{"A": 2, "B": 1, "C": 2},
			Slack:        map[string]float64{"A": 0, "B": 1.5, "C": 0},
			Articulation: []string{"B"},
			Cycles:       [][]string{{"A", "C"}},
		},
		// Sparse: CoreNumber/Slack cover a subset of the node union; this must
		// stay distinct from present-zero after the round trip.
		"sparse_and_nil": {
			OutDegree:   map[string]int{"X": 1, "Y": 2, "Z": 0},
			InDegree:    map[string]int{"X": 0, "Y": 0, "Z": 2},
			PageRank:    map[string]float64{"X": 0.3, "Y": 0.3, "Z": 0.4},
			CoreNumber:  map[string]int{"X": 1}, // sparse: only X
			Slack:       map[string]float64{"Z": 9.0},
			Betweenness: nil, // nil must round-trip back to nil
			Hubs:        map[string]float64{},
		},
		"empty": {},
	}

	for name, blob := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(blob)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got graphStatsCacheBlob
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(blob, got) {
				t.Fatalf("round-trip mismatch:\n want %#v\n  got %#v", blob, got)
			}
		})
	}
}

// TestGraphStatsCacheBlob_SoAStoresNodesOnce verifies the columnar layout: the
// serialized payload stores each node ID exactly once (in "nodes") rather than
// repeating it as a key in every per-node metric map.
func TestGraphStatsCacheBlob_SoAStoresNodesOnce(t *testing.T) {
	blob := graphStatsCacheBlob{
		OutDegree:   map[string]int{"NODE-001": 1, "NODE-002": 2},
		InDegree:    map[string]int{"NODE-001": 0, "NODE-002": 1},
		PageRank:    map[string]float64{"NODE-001": 0.5, "NODE-002": 0.5},
		Betweenness: map[string]float64{"NODE-001": 0.1, "NODE-002": 0.2},
	}
	data, err := json.Marshal(blob)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	for _, id := range []string{"NODE-001", "NODE-002"} {
		n := 0
		for i := 0; i+len(id) <= len(s); i++ {
			if s[i:i+len(id)] == id {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("node %q appears %d times in SoA payload, want exactly 1 (columnar)", id, n)
		}
	}
}

// TestRobotDiskCache_VersionGate confirms a v1 (old-format) cache file is
// treated as a miss, not mis-parsed against the v2 reader.
func TestRobotDiskCache_VersionGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "analysis_cache.json")
	// Old v1 file with the legacy map-keyed result shape.
	old := `{"version":1,"entries":{"k|c":{"created_at":"2026-01-01T00:00:00Z","result":{"page_rank":{"A":0.5}}}}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cf := readRobotDiskCacheLocked(f)
	if cf.Version != robotAnalysisDiskCacheVersion {
		t.Fatalf("version: got %d, want %d", cf.Version, robotAnalysisDiskCacheVersion)
	}
	if len(cf.Entries) != 0 {
		t.Fatalf("expected v1 cache to be ignored (0 entries), got %d", len(cf.Entries))
	}
}

// TestExpandFloatIntNegativeIndexNoPanic guards a corrupt/hand-edited cache file
// with a NEGATIVE sparse index: it must degrade (drop the bad entry) rather than
// panic on nodes[-1] and crash the whole bv command.
func TestExpandFloatIntNegativeIndexNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on negative index (must degrade to a miss): %v", r)
		}
	}()
	nodes := []string{"a", "b"}
	fm := expandFloat(true, []int32{-1, 1}, []float64{9.0, 2.0}, nodes)
	if len(fm) != 1 || fm["b"] != 2.0 {
		t.Errorf("expandFloat: expected only the valid index kept, got %v", fm)
	}
	im := expandInt(true, []int32{-3}, []int{7}, nodes)
	if len(im) != 0 {
		t.Errorf("expandInt: expected empty (only negative index), got %v", im)
	}
}
