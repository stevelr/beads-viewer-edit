package correlation

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	json "github.com/goccy/go-json"
)

// diskCacheForced lets a non-robot caller (notably the `--export-pages` /
// `--watch-export` path) opt the correlation caches in for its own process
// without setting BV_ROBOT=1 (which also flips unrelated robot-mode output).
// The history export re-runs the same HEAD-keyed extraction on every re-export;
// enabling the content-addressed per-commit / HEAD-artifact / report caches for
// it means a long-lived watcher pays the full git-blob materialization once and
// then serves incrementally (#182). It is set once at startup and only ever
// read, so a plain atomic bool is sufficient.
var diskCacheForced atomic.Bool

// SetDiskCacheEnabled force-enables (or disables) the persistent correlation
// caches for the current process regardless of BV_ROBOT. BV_NO_CACHE=1 still
// wins as a hard off switch. Intended for the export path, which needs the
// caches' incrementality but does not run in robot mode.
func SetDiskCacheEnabled(on bool) { diskCacheForced.Store(on) }

// Persistent on-disk cache for the correlation HistoryReport consumed by the
// robot triage/next/history paths. The expensive part of GenerateReport is the
// git blob I/O (git log --raw --follow, git cat-file --batch streaming the
// historical .beads/issues.jsonl blobs, plus the batched co-commit git logs).
// Agents call `bv --robot-triage` repeatedly in a loop and between calls
// usually NOTHING relevant has changed, so the entire report is recomputable-
// identical and can be served from disk without spawning any extraction git
// subprocesses.
//
// The cache key captures every input the report depends on:
//   - HEAD commit SHA: invalidates when commits land (history changes).
//   - hashBeads(beads): the ID/Title/Status of the beads embedded in the report
//     (changes when beads are added/closed/retitled, including uncommitted
//     working-tree edits, since the bead slice is loaded from the working tree).
//   - hashOptions(opts): Limit / BeadID / Since / Until.
//   - schema version: bumps invalidate every stale entry on format changes.
//
// Working-tree note: the git-extracted portion of the report only reflects
// committed history (git log / cat-file never see uncommitted edits), so an
// uncommitted change to .beads/issues.jsonl cannot alter the extracted events.
// The only working-tree-visible inputs are the bead ID/Title/Status, which are
// captured by hashBeads. The key is therefore complete; a dirty tree still
// produces a correct hit/miss.

const (
	correlationDiskCacheVersion      = 1
	correlationDiskCacheFileName     = "correlation_report_cache.json"
	correlationDiskCacheDirName      = "bv"
	correlationDiskCacheMaxEntries   = 6
	correlationDiskCacheMaxAge       = 24 * time.Hour
	correlationDiskCacheMaxEntrySize = 64 << 20 // 64MB serialized report ceiling
)

type correlationDiskCacheFile struct {
	Version int                                  `json:"version"`
	Entries map[string]correlationDiskCacheEntry `json:"entries"`
}

type correlationDiskCacheEntry struct {
	CreatedAt  time.Time      `json:"created_at"`
	AccessedAt time.Time      `json:"accessed_at"`
	HeadSHA    string         `json:"head_sha"`
	BeadsHash  string         `json:"beads_hash"`
	OptsHash   string         `json:"opts_hash"`
	Report     *HistoryReport `json:"report"`
}

// correlationDiskCacheEnabled reports whether the persistent report cache is
// active. It mirrors the analysis disk cache: on in robot mode, off when the
// caller asked to bypass caches.
func correlationDiskCacheEnabled() bool {
	if os.Getenv("BV_NO_CACHE") == "1" {
		return false
	}
	return os.Getenv("BV_ROBOT") == "1" || diskCacheForced.Load()
}

// correlationDiskCachePath resolves the cache file location, honoring the same
// conventions as the analysis disk cache: BV_CACHE_DIR override, otherwise the
// user cache dir (which respects XDG_CACHE_HOME), under a shared "bv" subdir.
func correlationDiskCachePath(create bool) (string, error) {
	base := os.Getenv("BV_CACHE_DIR")
	if base == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(dir, correlationDiskCacheDirName)
	}
	if create {
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, correlationDiskCacheFileName), nil
}

func correlationDiskCacheKey(headSHA, beadsHash, optsHash string) string {
	return headSHA + ":" + beadsHash + ":" + optsHash
}

func readCorrelationDiskCacheLocked(f *os.File) correlationDiskCacheFile {
	empty := correlationDiskCacheFile{Version: correlationDiskCacheVersion, Entries: map[string]correlationDiskCacheEntry{}}
	if _, err := f.Seek(0, 0); err != nil {
		return empty
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return empty
	}
	var cf correlationDiskCacheFile
	if err := json.Unmarshal(data, &cf); err != nil || cf.Version != correlationDiskCacheVersion {
		return empty
	}
	if cf.Entries == nil {
		cf.Entries = map[string]correlationDiskCacheEntry{}
	}
	return cf
}

func writeCorrelationDiskCacheLocked(f *os.File, cf correlationDiskCacheFile) error {
	if cf.Entries == nil {
		cf.Entries = map[string]correlationDiskCacheEntry{}
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	data, err := json.Marshal(cf)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func pruneCorrelationDiskCacheEntries(now time.Time, entries map[string]correlationDiskCacheEntry) {
	for k, e := range entries {
		if e.CreatedAt.IsZero() || now.Sub(e.CreatedAt) > correlationDiskCacheMaxAge {
			delete(entries, k)
		}
	}
}

func evictCorrelationDiskCacheLRU(entries map[string]correlationDiskCacheEntry) {
	if len(entries) <= correlationDiskCacheMaxEntries {
		return
	}
	type item struct {
		key string
		t   time.Time
	}
	items := make([]item, 0, len(entries))
	for k, e := range entries {
		t := e.AccessedAt
		if t.IsZero() {
			t = e.CreatedAt
		}
		items = append(items, item{key: k, t: t})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].t.Equal(items[j].t) {
			return items[i].key < items[j].key
		}
		return items[i].t.Before(items[j].t)
	})
	for len(entries) > correlationDiskCacheMaxEntries && len(items) > 0 {
		delete(entries, items[0].key)
		items = items[1:]
	}
}

// getCorrelationDiskCachedReport returns a cached report for the given key, if
// present and fresh. It performs no git extraction subprocesses; the only git
// call the caller makes to reach this is the rev-parse HEAD used to build the
// key. Following the pass-1 lesson, a pure read hit does NOT rewrite the cache
// file just to bump the LRU AccessedAt timestamp: rewriting a multi-MB report
// file on every robot invocation would dominate the cost of a hit, and the
// AccessedAt bookkeeping is not load-bearing for correctness (eviction falls
// back to CreatedAt for never-rewritten entries, an acceptable LRU
// approximation). Prunes are likewise only persisted on the write path.
func getCorrelationDiskCachedReport(headSHA, beadsHash, optsHash string) (*HistoryReport, bool) {
	if !correlationDiskCacheEnabled() {
		return nil, false
	}
	path, err := correlationDiskCachePath(false)
	if err != nil {
		return nil, false
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return nil, false
	}
	defer func() { _ = unlockFile(f) }()

	cf := readCorrelationDiskCacheLocked(f)
	key := correlationDiskCacheKey(headSHA, beadsHash, optsHash)
	entry, ok := cf.Entries[key]
	if !ok || entry.Report == nil {
		return nil, false
	}
	if entry.CreatedAt.IsZero() || time.Since(entry.CreatedAt) > correlationDiskCacheMaxAge {
		return nil, false
	}
	return entry.Report, true
}

// putCorrelationDiskCachedReport persists a freshly computed report. This runs
// only after a real recompute (a cache miss), so the full rewrite cost is
// amortized against the expensive git extraction it just avoided next time.
func putCorrelationDiskCachedReport(headSHA, beadsHash, optsHash string, report *HistoryReport) {
	if !correlationDiskCacheEnabled() || report == nil {
		return
	}
	// Bound the serialized size: do not persist pathologically large reports.
	data, err := json.Marshal(report)
	if err != nil || len(data) > correlationDiskCacheMaxEntrySize {
		return
	}

	path, err := correlationDiskCachePath(true)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return
	}
	defer func() { _ = unlockFile(f) }()

	now := time.Now().UTC()
	cf := readCorrelationDiskCacheLocked(f)
	pruneCorrelationDiskCacheEntries(now, cf.Entries)
	if cf.Entries == nil {
		cf.Entries = map[string]correlationDiskCacheEntry{}
	}
	cf.Entries[correlationDiskCacheKey(headSHA, beadsHash, optsHash)] = correlationDiskCacheEntry{
		CreatedAt:  now,
		AccessedAt: now,
		HeadSHA:    headSHA,
		BeadsHash:  beadsHash,
		OptsHash:   optsHash,
		Report:     report,
	}
	evictCorrelationDiskCacheLRU(cf.Entries)
	_ = writeCorrelationDiskCacheLocked(f, cf)
}

// GenerateReportCached wraps Correlator.GenerateReport with a two-layer
// persistent disk cache, both keyed off the repository HEAD:
//
//  1. OUTER report cache (this file), keyed on HEAD + hashBeads + options.
//     A hit means NOTHING relevant changed; the fully assembled report is
//     returned with no git extraction and no report re-assembly.
//
//  2. INNER HEAD-artifact cache (head_artifact_cache.go), keyed on HEAD +
//     options ONLY (no hashBeads). When the outer cache misses *because beads
//     changed* but HEAD is unchanged (the `br update X; bv --robot-triage`
//     loop), the cached history artifact — the expensive, purely-history-
//     derived []BeadEvent + co-commit data — is loaded cheaply and the report
//     is re-assembled against the *current* beads via assembleReport, skipping
//     the 232MB git-blob extraction entirely. The freshly assembled report is
//     then written back to the outer cache for the new bead-hash.
//
// On a full miss (HEAD changed, or cold) it extracts once, persists BOTH the
// artifact and the report, and returns. The assembled report is byte-identical
// (modulo the always-fresh GeneratedAt timestamp, as with the pre-existing
// outer cache) whether served fresh, from the outer cache, or rebuilt from the
// artifact, because assembleReport is a pure function of (beads, opts, artifact)
// and the artifact is reproduced exactly from the same HEAD+options.
//
// When the cache is disabled (non-robot mode, BV_NO_CACHE=1) or the HEAD cannot
// be resolved, it falls straight through to GenerateReport unchanged.
func (c *Correlator) GenerateReportCached(beads []BeadInfo, opts CorrelatorOptions) (*HistoryReport, error) {
	if !correlationDiskCacheEnabled() {
		return c.GenerateReport(beads, opts)
	}

	headSHA, err := getGitHeadContext(c.ctx, c.repoPath)
	if err != nil {
		// Can't key the cache without a stable HEAD; compute uncached.
		return c.GenerateReport(beads, opts)
	}
	beadsHash := hashBeads(beads)
	optsHash := hashOptions(opts)

	// Layer 1: fully assembled report for this exact (HEAD, beads, opts).
	if report, ok := getCorrelationDiskCachedReport(headSHA, beadsHash, optsHash); ok {
		return report, nil
	}

	// Layer 2: HEAD-only artifact. A hit here means the expensive extraction is
	// reusable; only the cheap bead-dependent assembly must run.
	if art, ok := getHeadArtifactCached(headSHA, optsHash); ok {
		report := c.assembleReport(beads, opts, art)
		putCorrelationDiskCachedReport(headSHA, beadsHash, optsHash, report)
		return report, nil
	}

	// Full miss: extract once, then assemble. Persist both layers.
	art, err := c.extractHistoryArtifact(opts)
	if err != nil {
		return nil, err
	}
	putHeadArtifactCached(headSHA, optsHash, art)

	report := c.assembleReport(beads, opts, art)
	putCorrelationDiskCachedReport(headSHA, beadsHash, optsHash, report)
	return report, nil
}
