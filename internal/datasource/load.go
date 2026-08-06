package datasource

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Dicklesworthstone/beads_viewer/pkg/loader"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// LoadIssues performs smart multi-source detection and loading.
// It discovers all available sources (SQLite, JSONL), validates them, selects
// the freshest valid source, and loads issues from it. SQLite is preferred over
// JSONL when both exist at comparable freshness, since SQLite reflects the most
// recent state (including status changes from br operations).
//
// Falls back to legacy JSONL-only loading via loader.LoadIssues if smart
// detection finds no valid sources.
func LoadIssues(repoPath string) ([]model.Issue, error) {
	if source, ok, err := ExplicitBeadsDBSource(); err != nil {
		return nil, err
	} else if ok {
		return LoadFromSource(source)
	}

	beadsDir, err := loader.GetBeadsDir(repoPath)
	if err != nil {
		return nil, err
	}

	issues, smartErr := loadSmart(beadsDir, repoPath)
	if smartErr == nil {
		return issues, nil
	}

	// Fall back to legacy JSONL-only loading
	return loader.LoadIssues(repoPath)
}

// LoadIssuesFromDir performs smart source detection within a known beads directory.
// This is useful when the caller already knows the .beads path.
func LoadIssuesFromDir(beadsDir string) ([]model.Issue, error) {
	issues, smartErr := loadSmart(beadsDir, "")
	if smartErr == nil {
		return issues, nil
	}

	// Fall back to JSONL
	jsonlPath, err := loader.FindJSONLPath(beadsDir)
	if err != nil {
		return nil, err
	}
	return loader.LoadIssuesFromFile(jsonlPath)
}

// ExplicitBeadsDBSource returns the direct source named by BEADS_DB when it
// points at a concrete source file. Directory values return ok=false so callers
// can use normal source discovery within that directory.
func ExplicitBeadsDBSource() (DataSource, bool, error) {
	return SourceFromFile(os.Getenv(loader.BeadsDBEnvVar))
}

// SourceFromFile returns a DataSource for a concrete source file path. Directory
// paths and empty values return ok=false so callers can fall back to discovery.
func SourceFromFile(dbPath string) (DataSource, bool, error) {
	if strings.TrimSpace(dbPath) == "" {
		return DataSource{}, false, nil
	}

	info, err := os.Stat(dbPath)
	if err == nil && info.IsDir() {
		return DataSource{}, false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return DataSource{}, true, fmt.Errorf("stat %s: %w", dbPath, err)
	}

	sourceType, priority, ok := explicitBeadsDBFileType(dbPath)
	if !ok {
		if err == nil {
			return DataSource{}, true, fmt.Errorf("unsupported BEADS_DB file type: %s", dbPath)
		}
		return DataSource{}, false, nil
	}

	source := DataSource{
		Type:     sourceType,
		Path:     dbPath,
		Priority: priority,
	}
	if err == nil {
		source.ModTime = info.ModTime()
		source.Size = info.Size()
	}
	return source, true, nil
}

func explicitBeadsDBFileType(dbPath string) (SourceType, int, bool) {
	switch strings.ToLower(filepath.Ext(dbPath)) {
	case ".db", ".sqlite", ".sqlite3":
		return SourceTypeSQLite, PrioritySQLite, true
	case ".jsonl":
		return SourceTypeJSONLLocal, PriorityJSONLLocal, true
	default:
		return "", 0, false
	}
}

// loadSmart discovers sources, selects the best, and loads from it in a single
// fused pass.
//
// Historically discovery ran a full content-scan validation (validateJSONL) on
// every source to set Valid/IssueCount and apply the malformed-error-rate gate,
// and the selected source was then read AGAIN by the loader — so the 1.9MB
// issues.jsonl was parsed twice per robot invocation. Here we skip the standalone
// validation pass and let the loader's own tolerant parse (it already strips BOM,
// caps long lines, and skips/counts malformed lines) serve as the validation
// pass: the same 10% malformed-error-rate gate is applied to the loader's parse
// stats post-load. A genuinely-corrupt JSONL is still rejected (and we fall
// through to the next candidate), but the happy path reads the file exactly once.
func loadSmart(beadsDir, repoPath string) ([]model.Issue, error) {
	sources, err := DiscoverSources(DiscoveryOptions{
		BeadsDir:               beadsDir,
		RepoPath:               repoPath,
		ValidateAfterDiscovery: false,
	})
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no valid sources discovered")
	}

	// Order candidates exactly as SelectBestSource would (freshest, then
	// priority) so we try the authoritative source first and fall back through
	// the rest only if it fails to validate-and-load.
	ordered := make([]DataSource, len(sources))
	copy(ordered, sources)
	sortByFreshnessThenPriority(ordered)

	var lastErr error
	for i := range ordered {
		issues, err := loadAndValidate(ordered[i])
		if err != nil {
			lastErr = err
			continue
		}
		return issues, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("no valid sources discovered: %w", lastErr)
	}
	return nil, fmt.Errorf("no valid sources discovered")
}

// loadAndValidate loads a single source while applying the validation gate in the
// same pass. For SQLite the validation (integrity + schema check) is cheap and
// independent of the row read, so it runs first. For JSONL the loader's tolerant
// parse IS the validation pass: a single read materializes issues and yields the
// parse stats used to apply the malformed-error-rate gate.
func loadAndValidate(source DataSource) ([]model.Issue, error) {
	switch source.Type {
	case SourceTypeSQLite:
		if err := ValidateSource(&source); err != nil {
			return nil, err
		}
		return LoadFromSource(source)
	case SourceTypeJSONLLocal, SourceTypeJSONLWorktree:
		return loadAndValidateJSONL(source)
	default:
		return nil, fmt.Errorf("unsupported source type: %s", source.Type)
	}
}

// loadAndValidateJSONL performs the fused validate-and-materialize pass for a
// JSONL source: it parses the file once, applies the same default 10%
// malformed-error-rate gate that validateJSONL uses, and filters tombstones to
// honor the IssueReader contract. Reading the file a single time replaces the
// previous validate-then-load double parse.
func loadAndValidateJSONL(source DataSource) ([]model.Issue, error) {
	var stats loader.ParseStats
	all, err := loader.LoadIssuesFromFileWithOptions(source.Path, loader.ParseOptions{Stats: &stats})
	if err != nil {
		return nil, err
	}

	// Apply the error-rate gate against the same default threshold
	// validateJSONL enforces. Malformed JSON or failed model validation count as
	// Errors, so a genuinely-corrupt file is rejected here and we fall through to
	// the next candidate.
	maxRate := DefaultValidationOptions().MaxJSONLErrorRate
	if rate := stats.ErrorRate(); rate > maxRate {
		return nil, fmt.Errorf("%s: too many errors: %.1f%% (max %.1f%%)", source.Path, rate*100, maxRate*100)
	}

	// Reject a source that contained records but yielded ZERO valid issues — e.g.
	// a fresher stray sprints.jsonl/memories.jsonl, or a file made entirely of
	// non-issue `_type` records. The error-rate gate above misses these because
	// non-issue records are Skipped (not Errors), so the rate is 0. The old
	// validateJSONL counted every such non-issue line as an error and rejected the
	// file; this reproduces that so loadSmart falls through to the next candidate
	// instead of returning an empty list as "success". An empty file (no records
	// at all: Valid==Errors==Skipped==0) stays valid — a legitimately empty
	// project — matching validateJSONL's empty-file behavior. A file with Valid>0
	// whose issues are all tombstones is a real issues file and is accepted.
	if stats.Valid == 0 && stats.Errors+stats.Skipped > 0 {
		return nil, fmt.Errorf("%s: no issue records (%d non-issue/error lines, 0 valid issues)", source.Path, stats.Errors+stats.Skipped)
	}

	// Filter out tombstone issues to match the IssueReader contract (the same
	// filtering JSONLReader.LoadIssues applies).
	out := make([]model.Issue, 0, len(all))
	for i := range all {
		if !all[i].Status.IsTombstone() {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// LoadFromSource loads issues from a specific DataSource via the IssueReader
// interface, dispatching to the appropriate backend based on source type.
func LoadFromSource(source DataSource) ([]model.Issue, error) {
	reader, err := NewReader(source)
	if err != nil {
		return nil, fmt.Errorf("failed to open source %s: %w", source.Path, err)
	}
	defer reader.Close()
	return reader.LoadIssues()
}
