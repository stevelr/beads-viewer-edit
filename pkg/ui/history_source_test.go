package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/internal/datasource"
)

// writeFileAt writes content to path and sets its mtime (and atime) to the given
// time so tests can deterministically reproduce sub-second mtime skew between the
// SQLite DB and the JSONL export.
func writeFileAt(t *testing.T, path string, content []byte, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// TestResolveHistoryCorrelationPath_PrefersJSONLOverDB is the bv #171 regression:
// when the smart data-source selector hands the History view the SQLite DB path
// (because beads.db is a few milliseconds newer than issues.jsonl after a normal
// `br sync`), the correlator must still follow the git-tracked JSONL — git history
// of the binary DB yields zero lifecycle events, so every correlation would be
// lost. resolveHistoryCorrelationPath redirects DB (or any non-JSONL) selections
// to the sibling JSONL while leaving JSONL selections untouched.
func TestResolveHistoryCorrelationPath_PrefersJSONLOverDB(t *testing.T) {
	repo := t.TempDir()
	beadsDir := filepath.Join(repo, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}

	jsonlPath := filepath.Join(beadsDir, "issues.jsonl")
	dbPath := filepath.Join(beadsDir, "beads.db")

	// Reproduce the exact trigger: DB mtime 41ms NEWER than the JSONL.
	base := time.Now().Add(-time.Hour)
	writeFileAt(t, jsonlPath, []byte(`{"id":"bv-1","title":"x","status":"open"}`+"\n"), base)
	writeFileAt(t, dbPath, []byte("SQLite format 3\x00"), base.Add(41*time.Millisecond))

	// Sanity check: confirm the freshest-mtime selector really does pick the DB
	// under this skew (the bug's trigger). If it didn't, the regression below
	// would pass vacuously.
	sources, err := datasource.DiscoverSources(datasource.DiscoveryOptions{
		BeadsDir: beadsDir,
		RepoPath: repo,
	})
	if err != nil {
		t.Fatalf("DiscoverSources: %v", err)
	}
	if len(sources) == 0 {
		t.Fatalf("expected at least one discovered source")
	}
	if sources[0].Path != dbPath {
		t.Fatalf("precondition not met: expected freshest-mtime selection to be the DB %q, got %q (sources=%+v)", dbPath, sources[0].Path, sources)
	}

	// The fix: even though the DB was selected, History correlation must follow
	// the JSONL.
	got := resolveHistoryCorrelationPath(dbPath, repo)
	if got != jsonlPath {
		t.Fatalf("expected correlation path %q (JSONL), got %q (DB-derived selection must redirect to JSONL)", jsonlPath, got)
	}
}

// TestResolveHistoryCorrelationPath_KeepsJSONLSelection verifies that when the
// selector already chose a JSONL (e.g. JSONL is the freshest source, or the
// `touch issues.jsonl` workaround was applied), the path is preserved unchanged.
func TestResolveHistoryCorrelationPath_KeepsJSONLSelection(t *testing.T) {
	repo := t.TempDir()
	beadsDir := filepath.Join(repo, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	jsonlPath := filepath.Join(beadsDir, "issues.jsonl")
	writeFileAt(t, jsonlPath, []byte(`{"id":"bv-1","title":"x","status":"open"}`+"\n"), time.Now())

	if got := resolveHistoryCorrelationPath(jsonlPath, repo); got != jsonlPath {
		t.Fatalf("JSONL selection must be preserved: want %q, got %q", jsonlPath, got)
	}

	// Case-insensitive extension match (e.g. .JSONL) is also preserved.
	upper := filepath.Join(beadsDir, "issues.JSONL")
	if got := resolveHistoryCorrelationPath(upper, repo); got != upper {
		t.Fatalf("uppercase .JSONL selection must be preserved: want %q, got %q", upper, got)
	}
}

// TestResolveHistoryCorrelationPath_FallsBackWhenNoJSONL verifies graceful
// degradation: when the selected source is a DB but no JSONL exists alongside it
// (or anywhere standard), the original path is returned so the correlator's own
// default-file resolution still runs rather than panicking or returning "".
func TestResolveHistoryCorrelationPath_FallsBackWhenNoJSONL(t *testing.T) {
	repo := t.TempDir()
	beadsDir := filepath.Join(repo, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	dbPath := filepath.Join(beadsDir, "beads.db")
	writeFileAt(t, dbPath, []byte("SQLite format 3\x00"), time.Now())

	if got := resolveHistoryCorrelationPath(dbPath, repo); got != dbPath {
		t.Fatalf("with no JSONL present, original path must be preserved: want %q, got %q", dbPath, got)
	}
}

// TestResolveHistoryCorrelationPath_EmptyPath verifies that an empty selection
// (workspace mode) is passed straight through so the correlator discovers the
// standard beads files itself.
func TestResolveHistoryCorrelationPath_EmptyPath(t *testing.T) {
	if got := resolveHistoryCorrelationPath("", t.TempDir()); got != "" {
		t.Fatalf("empty path must be preserved, got %q", got)
	}
}
