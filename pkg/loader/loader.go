package loader

import (
	"bufio"
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	json "github.com/goccy/go-json"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// beadsMetadata is the subset of .beads/metadata.json we care about.
type beadsMetadata struct {
	Backend     string `json:"backend"`
	JSONLExport string `json:"jsonl_export"`
}

// BeadsDirEnvVar is the name of the environment variable for custom beads directory
const BeadsDirEnvVar = "BEADS_DIR"

// BeadsDBEnvVar is the name of the environment variable for a specific database file
// or .beads directory path. Takes priority over BEADS_DIR.
// Can point to a specific file (e.g., /path/to/.beads/issues.jsonl) or a .beads directory.
const BeadsDBEnvVar = "BEADS_DB"

// PreferredJSONLNames defines the priority order for looking up beads data files.
// Priority order matches current br's canonical JSONL export first, with
// legacy bd/beads.jsonl workspaces still supported as a fallback.
var PreferredJSONLNames = []string{"issues.jsonl", "beads.jsonl", "beads.base.jsonl"}

// GetBeadsDir returns the beads directory path, with the following priority:
//  1. BEADS_DB env var (can point to a file or directory; if file, returns parent dir)
//  2. BEADS_DIR env var (used directly as the .beads directory)
//  3. .beads in the given repoPath (or cwd if empty)
//  4. .beads in the main git repository root (for worktrees)
func GetBeadsDir(repoPath string) (string, error) {
	// Check BEADS_DB environment variable first (highest priority after --db flag)
	if envDB := os.Getenv(BeadsDBEnvVar); envDB != "" {
		return resolveBeadsDB(envDB)
	}

	// Check BEADS_DIR environment variable
	if envDir := os.Getenv(BeadsDirEnvVar); envDir != "" {
		return envDir, nil
	}

	// Fall back to .beads in repo path
	if repoPath == "" {
		var err error
		repoPath, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	// Check for .beads in the given path first
	beadsDir := filepath.Join(repoPath, ".beads")
	if _, err := os.Stat(beadsDir); err == nil {
		return followBeadsRedirect(beadsDir)
	}

	// If not found, check if we're in a git worktree and look in the main repo
	mainRepoRoot, err := getMainRepoRoot(repoPath)
	if err == nil && mainRepoRoot != "" && mainRepoRoot != repoPath {
		mainBeadsDir := filepath.Join(mainRepoRoot, ".beads")
		if _, err := os.Stat(mainBeadsDir); err == nil {
			return followBeadsRedirect(mainBeadsDir)
		}
	}

	// Return the original path even if .beads doesn't exist
	// (caller will handle the error)
	return beadsDir, nil
}

// maxRedirectBytes and maxRedirectDepth bound .beads/redirect resolution,
// matching br's routing limits so bv and br agree on the target store.
const (
	maxRedirectBytes = 4096
	maxRedirectDepth = 10
)

// followBeadsRedirect resolves a .beads/redirect chain to its terminal beads
// directory, mirroring `br where` so bv reads from the same store br writes to.
// When beadsDir has no redirect file, it is returned unchanged. A malformed
// redirect (oversized, non-UTF-8, loop, missing target, or a target that is not
// a .beads/_beads directory) is surfaced as an error rather than silently
// falling back to the local .beads, which would reintroduce the stale-read bug.
func followBeadsRedirect(beadsDir string) (string, error) {
	current := beadsDir
	if abs, err := filepath.Abs(current); err == nil {
		current = abs
	}
	start := current
	visited := map[string]bool{current: true}

	for depth := 0; ; depth++ {
		target, ok, err := readBeadsRedirect(current)
		if err != nil {
			return "", err
		}
		if !ok {
			break
		}
		if depth >= maxRedirectDepth {
			return "", fmt.Errorf("redirect chain exceeds max depth (%d): %s", maxRedirectDepth, beadsDir)
		}
		if abs, err := filepath.Abs(target); err == nil {
			target = abs
		}
		if target == current {
			break
		}
		if visited[target] {
			return "", fmt.Errorf("redirect loop detected: %s -> %s", current, target)
		}
		visited[target] = true
		current = target
	}

	// No redirect was followed: return the original directory untouched.
	if current == start {
		return beadsDir, nil
	}

	info, err := os.Stat(current)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("redirect target not found: %s", current)
	}
	if base := filepath.Base(current); base != ".beads" && base != "_beads" {
		return "", fmt.Errorf("redirect target must be a .beads or _beads directory: %s", current)
	}
	return current, nil
}

// readBeadsRedirect reads the redirect file inside beadsDir. It returns the
// resolved target directory and true when a non-empty redirect exists. Relative
// targets resolve against beadsDir itself (so "." stays in place), matching br.
func readBeadsRedirect(beadsDir string) (string, bool, error) {
	redirectPath := filepath.Join(beadsDir, "redirect")
	info, err := os.Stat(redirectPath)
	if err != nil || info.IsDir() {
		return "", false, nil
	}
	if info.Size() > maxRedirectBytes {
		return "", false, fmt.Errorf("redirect file exceeds maximum size of %d bytes: %s", maxRedirectBytes, redirectPath)
	}

	data, err := os.ReadFile(redirectPath)
	if err != nil {
		return "", false, fmt.Errorf("failed to read redirect file %s: %w", redirectPath, err)
	}
	if !utf8.Valid(data) {
		return "", false, fmt.Errorf("redirect file must be valid UTF-8: %s", redirectPath)
	}

	target := strings.TrimSpace(string(data))
	if target == "" {
		return "", false, nil
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(beadsDir, target)
	}
	return target, true, nil
}

// resolveBeadsDB interprets a BEADS_DB value which can be either:
//   - An absolute path to a specific file (e.g., /path/to/.beads/beads.{jsonl,db,sqlite3})
//   - An absolute path to a .beads directory
//
// If it points to a file, returns the parent directory.
// If it points to a directory, returns the directory itself.
func resolveBeadsDB(dbPath string) (string, error) {
	info, err := os.Stat(dbPath)
	if err != nil {
		// Path doesn't exist yet -- guess based on whether it looks like a file path
		if looksLikeBeadsDBFile(dbPath) {
			return filepath.Dir(dbPath), nil
		}
		// Assume it's a directory
		return dbPath, nil
	}

	if info.IsDir() {
		return dbPath, nil
	}

	// It's a file -- return the parent directory
	return filepath.Dir(dbPath), nil
}

func looksLikeBeadsDBFile(dbPath string) bool {
	switch strings.ToLower(filepath.Ext(dbPath)) {
	case ".jsonl", ".db", ".sqlite", ".sqlite3":
		return true
	default:
		return false
	}
}

// IsBDWorkspace returns true when the given .beads directory belongs to a
// modern Dolt-native bd workspace. Detection is based on the presence of a
// .beads/dolt/ subdirectory or a metadata.json declaring backend=dolt.
func IsBDWorkspace(beadsDir string) bool {
	if beadsDir == "" {
		return false
	}

	// Fast path: modern beads stores Dolt data under .beads/dolt/.
	if info, err := os.Stat(filepath.Join(beadsDir, "dolt")); err == nil && info.IsDir() {
		return true
	}

	// Fallback: metadata.json may explicitly record the backend.
	metaPath := filepath.Join(beadsDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}

	var meta beadsMetadata
	if err := stdjson.Unmarshal(data, &meta); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(meta.Backend), "dolt")
}

// PrepareWorkspaceForRead resolves the active JSONL file for the workspace.
// For bd workspaces it can refresh .beads/issues.jsonl by running
// `bd export -o .beads/issues.jsonl` before reading. For regular br workspaces
// it falls through to FindJSONLPath.
func PrepareWorkspaceForRead(repoPath string, refreshBDExport bool, warnFunc func(string)) (string, string, error) {
	beadsDir, err := GetBeadsDir(repoPath)
	if err != nil {
		return "", "", err
	}
	jsonlPath, err := PrepareBeadsDirForRead(beadsDir, refreshBDExport, warnFunc)
	if err != nil {
		return "", "", err
	}
	return beadsDir, jsonlPath, nil
}

// PrepareBeadsDirForRead resolves the active JSONL file for an explicit .beads
// directory. In bd workspaces the compatibility export at .beads/issues.jsonl
// is used (optionally refreshed). In regular br workspaces FindJSONLPath is
// used as before.
func PrepareBeadsDirForRead(beadsDir string, refreshBDExport bool, warnFunc func(string)) (string, error) {
	if IsBDWorkspace(beadsDir) {
		issuesPath := filepath.Join(beadsDir, "issues.jsonl")
		if refreshBDExport {
			if err := exportBDIssuesJSONL(beadsDir, issuesPath); err != nil {
				if _, statErr := os.Stat(issuesPath); statErr == nil {
					if warnFunc != nil {
						warnFunc(fmt.Sprintf("bd export failed, using existing issues.jsonl: %v", err))
					}
				} else {
					return "", fmt.Errorf("failed to refresh bd compatibility JSONL: %w", err)
				}
			}
		}

		if _, err := os.Stat(issuesPath); err != nil {
			return "", fmt.Errorf("no compatibility JSONL found at %s; run 'bd export -o .beads/issues.jsonl'", issuesPath)
		}

		return issuesPath, nil
	}

	return FindJSONLPath(beadsDir)
}

// exportBDIssuesJSONL runs `bd export -o <issuesPath>` to produce a fresh
// JSONL compatibility file from the bd workspace's Dolt database.
func exportBDIssuesJSONL(beadsDir, issuesPath string) error {
	if _, err := exec.LookPath("bd"); err != nil {
		return fmt.Errorf("bd binary not found in PATH")
	}

	repoRoot := filepath.Dir(beadsDir)
	cmd := exec.Command("bd", "export", "-o", issuesPath)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", BeadsDirEnvVar, beadsDir))
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

// getMainRepoRoot returns the root directory of the main git repository.
// For regular repos, this returns the repo root.
// For worktrees, this returns the main repository root (not the worktree root).
func getMainRepoRoot(repoPath string) (string, error) {
	// First, check if we're in a git repository at all
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = repoPath
	topLevelOut, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	worktreeRoot := strings.TrimSpace(string(topLevelOut))

	// Check if this is a worktree by looking at the git-common-dir
	// For regular repos: git-common-dir == git-dir
	// For worktrees: git-common-dir points to main repo's .git
	cmd = exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = repoPath
	commonDirOut, err := cmd.Output()
	if err != nil {
		// Fallback: not a worktree or old git version
		return worktreeRoot, nil
	}
	commonDir := strings.TrimSpace(string(commonDirOut))

	cmd = exec.Command("git", "rev-parse", "--path-format=absolute", "--git-dir")
	cmd.Dir = repoPath
	gitDirOut, err := cmd.Output()
	if err != nil {
		return worktreeRoot, nil
	}
	gitDir := strings.TrimSpace(string(gitDirOut))

	// If git-common-dir == git-dir, we're in a regular repo
	if commonDir == gitDir {
		return worktreeRoot, nil
	}

	// We're in a worktree. The main repo root is the parent of git-common-dir.
	// git-common-dir typically points to /path/to/main-repo/.git
	mainRepoRoot := filepath.Dir(commonDir)

	return mainRepoRoot, nil
}

// FindJSONLPath locates the beads JSONL file in the given directory.
// Prefers issues.jsonl (current br) over beads.jsonl (legacy bd) and
// beads.base.jsonl (daemon/base export). Skips backup files and merge artifacts.
// Skips backup files and merge artifacts.
func FindJSONLPath(beadsDir string) (string, error) {
	return FindJSONLPathWithWarnings(beadsDir, nil)
}

// FindJSONLPathWithWarnings is like FindJSONLPath but optionally reports warnings
// about detected merge artifacts via the provided callback.
func FindJSONLPathWithWarnings(beadsDir string, warnFunc func(msg string)) (string, error) {
	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		return "", fmt.Errorf("failed to read beads directory: %w", err)
	}

	var candidates []string
	var mergeArtifacts []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()

		// Must be a .jsonl file
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		// Skip backups, merge artifacts, and deletion manifests
		if strings.Contains(name, ".backup") ||
			strings.Contains(name, ".orig") ||
			strings.Contains(name, ".merge") ||
			name == "deletions.jsonl" {
			continue
		}

		// Skip git merge conflict artifacts (beads.left.jsonl, beads.right.jsonl)
		// These are OURS/THEIRS sides during a merge conflict
		if strings.HasPrefix(name, "beads.left") || strings.HasPrefix(name, "beads.right") {
			mergeArtifacts = append(mergeArtifacts, name)
			continue
		}

		candidates = append(candidates, name)
	}

	// Warn about detected merge artifacts
	if len(mergeArtifacts) > 0 && warnFunc != nil {
		warnFunc(fmt.Sprintf("Merge artifact files detected: %s. Clean them up before relying on the JSONL view.",
			strings.Join(mergeArtifacts, ", ")))
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no beads JSONL file found in %s", beadsDir)
	}

	// Priority order for beads files:
	// Current br stack: metadata.json jsonl_export, then
	// issues.jsonl -> beads.jsonl -> beads.base.jsonl. Legacy bd workspaces
	// remain readable through the beads.jsonl fallback.
	preferredNames := PreferredJSONLNames
	if IsBDWorkspace(beadsDir) {
		preferredNames = []string{"issues.jsonl", "beads.jsonl", "beads.base.jsonl"}
	} else if metadataPreferred := metadataJSONLExportName(beadsDir); metadataPreferred != "" {
		preferredNames = prependPreferredName(metadataPreferred, PreferredJSONLNames)
	}

	for _, preferred := range preferredNames {
		for _, name := range candidates {
			if name == preferred {
				path := filepath.Join(beadsDir, name)
				// Check if file has content (skip empty files)
				if info, err := os.Stat(path); err == nil && info.Size() > 0 {
					return path, nil
				}
			}
		}
	}

	// Fall back to first non-empty candidate
	for _, name := range candidates {
		path := filepath.Join(beadsDir, name)
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return path, nil
		}
	}

	// Last resort: return first candidate even if empty
	return filepath.Join(beadsDir, candidates[0]), nil
}

func metadataJSONLExportName(beadsDir string) string {
	data, err := os.ReadFile(filepath.Join(beadsDir, "metadata.json"))
	if err != nil {
		return ""
	}
	var meta beadsMetadata
	if err := stdjson.Unmarshal(data, &meta); err != nil {
		return ""
	}
	name := filepath.Clean(strings.TrimSpace(meta.JSONLExport))
	if name == "." || name == "" || filepath.IsAbs(name) {
		return ""
	}
	if strings.HasPrefix(name, "..") || strings.ContainsAny(name, `/\`) {
		return ""
	}
	if !strings.HasSuffix(name, ".jsonl") {
		return ""
	}
	return name
}

func prependPreferredName(name string, defaults []string) []string {
	names := make([]string, 0, len(defaults)+1)
	names = append(names, name)
	for _, candidate := range defaults {
		if candidate != name {
			names = append(names, candidate)
		}
	}
	return names
}

// LoadIssues reads issues from the beads directory.
// Respects BEADS_DIR environment variable, otherwise uses .beads in repoPath.
// Automatically finds the correct JSONL file (issues.jsonl preferred, beads.jsonl fallback).
func LoadIssues(repoPath string) ([]model.Issue, error) {
	beadsDir, err := GetBeadsDir(repoPath)
	if err != nil {
		return nil, err
	}

	jsonlPath, err := FindJSONLPath(beadsDir)
	if err != nil {
		return nil, err
	}

	return LoadIssuesFromFile(jsonlPath)
}

// DefaultMaxBufferSize is the default buffer size for the scanner (10MB).
const DefaultMaxBufferSize = 1024 * 1024 * 10

// ParseOptions configures the behavior of ParseIssues.
type ParseOptions struct {
	// WarningHandler is called with warning messages (e.g., malformed JSON).
	// If nil, warnings are printed to os.Stderr.
	WarningHandler func(string)

	// BufferSize sets the maximum line size (in bytes) to read at once.
	// Lines longer than this are skipped with a warning.
	// If 0, uses DefaultMaxBufferSize (10MB).
	BufferSize int

	// IssueFilter optionally filters parsed issues. Return true to include.
	// When nil, all valid issues are included.
	IssueFilter func(*model.Issue) bool

	// Stats, when non-nil, receives per-line accounting as the stream is
	// parsed. This lets a single fused loader pass also serve as the
	// validation pass (issue count + malformed-error-rate gate) so the
	// 1.9MB issues.jsonl is read once instead of validate-then-load.
	// Only issue-shaped records are accounted; non-issue `_type` records,
	// empty lines, and long-skipped lines are not counted toward the gate,
	// matching datasource.validateJSONL semantics.
	Stats *ParseStats
}

// ParseStats accumulates per-line accounting for a single parse pass so a load
// can also produce the corruption verdict (malformed-error-rate gate) without a
// second read of the file. The categories mirror datasource.validateJSONL: a line
// counts toward Valid when its JSON decodes AND the resulting issue passes model
// validation (which subsumes the required id/title/status check), and toward
// Errors when the JSON is malformed OR the issue fails validation. Empty lines
// and over-long skipped lines are not accounted. Recognized non-issue `_type`
// records (and unknown `_type`) count toward Skipped — they are not errors, but
// a file made ENTIRELY of them yielded zero issues, which callers use to reject a
// wrong/non-issue source (e.g. a stray sprints.jsonl) rather than treat it as a
// valid empty project.
type ParseStats struct {
	// Valid is the number of issue-shaped lines that parsed and validated.
	Valid int
	// Errors is the number of issue-shaped lines that were malformed JSON or
	// failed model validation (e.g. missing required fields).
	Errors int
	// Skipped is the number of recognized non-issue `_type` records (memory,
	// sprint, forecast, burndown, ignore) plus unknown `_type` records — content
	// that was present but is not an issue.
	Skipped int
}

// ErrorRate returns the fraction of accounted issue lines that were errors.
// Returns 0 when no issue lines were seen (an empty file is valid).
func (s ParseStats) ErrorRate() float64 {
	total := s.Valid + s.Errors
	if total == 0 {
		return 0
	}
	return float64(s.Errors) / float64(total)
}

// LoadIssuesFromFileWithOptions reads issues from a file with custom options.
func LoadIssuesFromFileWithOptions(path string, opts ParseOptions) ([]model.Issue, error) {
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("no beads issues found at %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open issues file: %w", err)
	}
	defer file.Close()

	return ParseIssuesWithOptions(file, opts)
}

// LoadIssuesFromFileWithOptionsPooled reads issues from a file with pooling enabled.
// The caller must return pooled issues via ReturnIssuePtrsToPool when no longer needed.
func LoadIssuesFromFileWithOptionsPooled(path string, opts ParseOptions) (PooledIssues, error) {
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return PooledIssues{}, fmt.Errorf("no beads issues found at %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return PooledIssues{}, fmt.Errorf("failed to open issues file: %w", err)
	}
	defer file.Close()

	return ParseIssuesWithOptionsPooled(file, opts)
}

// LoadIssuesFromFile reads issues directly from a specific JSONL file path.
func LoadIssuesFromFile(path string) ([]model.Issue, error) {
	return LoadIssuesFromFileWithOptions(path, ParseOptions{})
}

// LoadIssuesFromFilePooled reads issues directly from a JSONL file path with pooling enabled.
func LoadIssuesFromFilePooled(path string) (PooledIssues, error) {
	return LoadIssuesFromFileWithOptionsPooled(path, ParseOptions{})
}

// ParseIssues parses JSONL content from a reader into issues.
// Handles UTF-8 BOM stripping, large lines, and validation.
func ParseIssues(r io.Reader) ([]model.Issue, error) {
	return ParseIssuesWithOptions(r, ParseOptions{})
}

// ParseIssuesWithOptions parses JSONL content with custom options.
func ParseIssuesWithOptions(r io.Reader, opts ParseOptions) ([]model.Issue, error) {
	issues, _, err := parseIssuesWithOptions(r, opts, false)
	return issues, err
}

// ParseIssuesWithOptionsPooled parses JSONL content with pooling enabled.
// The caller must return pooled issues via ReturnIssuePtrsToPool when no longer needed.
func ParseIssuesWithOptionsPooled(r io.Reader, opts ParseOptions) (PooledIssues, error) {
	issues, poolRefs, err := parseIssuesWithOptions(r, opts, true)
	if err != nil {
		return PooledIssues{}, err
	}
	return PooledIssues{Issues: issues, PoolRefs: poolRefs}, nil
}

func parseIssuesWithOptions(r io.Reader, opts ParseOptions, usePool bool) ([]model.Issue, []*model.Issue, error) {
	// Determine buffer size (the 10MB-default per-line cap).
	maxCapacity := opts.BufferSize
	if maxCapacity <= 0 {
		maxCapacity = DefaultMaxBufferSize
	}

	// Parallel fast path: for large on-disk files, JSONL is line-independent
	// (one JSON object per line), so the decode is embarrassingly parallel.
	// We read the file once, split it into line-aligned chunks, decode the
	// chunks across a bounded worker pool, and reassemble in ORIGINAL ORDER.
	// This is the alien-graveyard §8.2 "morsel-driven parallelism" pattern:
	// fixed-size morsels pulled by a bounded set of workers, with results
	// stitched back deterministically. The path is byte-equivalent to the
	// serial loop below (same BOM strip, same _type dispatch, same warnings in
	// original line order, same ParseStats, same pooled deep-copy semantics);
	// see parseIssuesParallel and the differential test in loader_test.go.
	if f, ok := r.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Size() >= parallelParseMinBytes {
			data, rerr := io.ReadAll(f)
			if rerr != nil {
				return nil, nil, fmt.Errorf("error reading issues stream: %w", rerr)
			}
			if countLines(data) >= parallelParseMinLines {
				return parseIssuesParallel(data, opts, usePool, maxCapacity)
			}
			// Small line count after all: fall back to the serial reader over
			// the bytes we already slurped (avoids a second read).
			r = bytes.NewReader(data)
		}
	}

	var issues []model.Issue
	var poolRefs []*model.Issue
	if f, ok := r.(*os.File); ok {
		if info, err := f.Stat(); err == nil {
			est := estimateIssueCap(info.Size())
			if est > 0 {
				issues = make([]model.Issue, 0, est)
				if usePool {
					poolRefs = make([]*model.Issue, 0, est)
				}
			}
		}
	}

	reader := bufio.NewReaderSize(r, maxCapacity)

	warn := resolveWarnHandler(opts.WarningHandler)

	lineNum := 0
	for {
		lineNum++
		// ReadLine returns a single line, not including the end-of-line bytes.
		// If the line was too long for the buffer then isPrefix is set and the
		// beginning of the line is returned.
		line, isPrefix, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			if usePool {
				ReturnIssuePtrsToPool(poolRefs)
			}
			return nil, nil, fmt.Errorf("error reading issues stream at line %d: %w", lineNum, err)
		}

		if isPrefix {
			// Line too long. Discard the rest of the line.
			warn(fmt.Sprintf("skipping line %d: line too long (exceeds %d bytes)", lineNum, maxCapacity))
			for isPrefix {
				_, isPrefix, err = reader.ReadLine()
				if err != nil && err != io.EOF {
					if usePool {
						ReturnIssuePtrsToPool(poolRefs)
					}
					return nil, nil, fmt.Errorf("error skipping long line at line %d: %w", lineNum, err)
				}
				if err == io.EOF {
					break
				}
			}
			continue
		}

		if len(line) == 0 {
			continue
		}

		// Strip UTF-8 BOM if present on the first line
		if lineNum == 1 {
			line = stripBOM(line)
		}

		issues, poolRefs = processIssueLine(line, lineNum, opts, usePool, issues, poolRefs, opts.Stats, warn)
	}

	return issues, poolRefs, nil
}

// processIssueLine applies the full per-line loader semantics to a single
// (BOM-stripped, non-empty, end-of-line-trimmed) JSONL line and appends any
// resulting issue. It is the single source of truth shared by the serial reader
// loop and the parallel chunk workers, guaranteeing the two paths are
// byte-equivalent: same `_type` dispatch, same malformed/invalid handling, same
// warning text keyed by lineNum, same ParseStats accounting, and the same
// pooled deep-copy semantics (bv-fn4b). It returns the (possibly grown) issues
// and poolRefs slices. stats may be nil; warn must be non-nil.
func processIssueLine(
	line []byte,
	lineNum int,
	opts ParseOptions,
	usePool bool,
	issues []model.Issue,
	poolRefs []*model.Issue,
	stats *ParseStats,
	warn func(string),
) ([]model.Issue, []*model.Issue) {
	// Dispatch by `_type` so non-issue records in beads JSONL
	// (e.g. memories, sprints, future record kinds) don't get parsed
	// as issues and warn-skipped with "issue ID cannot be empty"
	// on every load (issue #145). Empty / missing `_type` is the
	// historical "issue" shape and stays the default.
	switch recordTypeOf(line) {
	case recordTypeIssue:
		// fall through to the issue parser below
	case recordTypeMemory, recordTypeSprint, recordTypeForecast, recordTypeBurndown, recordTypeIgnore:
		// Recognized non-issue record. The viewer doesn't surface
		// these yet, so silently skip — we just need to not warn.
		if stats != nil {
			stats.Skipped++
		}
		return issues, poolRefs
	default:
		// Unknown _type: don't fail, but don't pretend it was an
		// issue either. A debug-level breadcrumb is enough; the
		// noisy "issue ID cannot be empty" warning was the actual
		// bug being reported.
		if stats != nil {
			stats.Skipped++
		}
		return issues, poolRefs
	}

	if usePool {
		issue := GetIssue()
		if err := json.Unmarshal(line, issue); err != nil {
			PutIssue(issue)
			if stats != nil {
				stats.Errors++
			}
			// Skip malformed lines but warn
			warn(fmt.Sprintf("skipping malformed JSON on line %d: %v", lineNum, err))
			return issues, poolRefs
		}

		normalizeLoadedIssue(issue)

		// Validate issue
		if err := issue.Validate(); err != nil {
			PutIssue(issue)
			if stats != nil {
				stats.Errors++
			}
			// Skip invalid issues
			warn(fmt.Sprintf("skipping invalid issue on line %d: %v", lineNum, err))
			return issues, poolRefs
		}
		if stats != nil {
			stats.Valid++
		}

		if opts.IssueFilter != nil && !opts.IssueFilter(issue) {
			PutIssue(issue)
			return issues, poolRefs
		}

		// Append the struct value first, then deep-copy slice fields on the VALUE
		// copy to break sharing with pooled backing arrays. This ensures that when
		// the pooled issue is returned to the pool and its backing arrays are reused,
		// the copied issue in the snapshot is not affected (bv-fn4b).
		issues = append(issues, *issue)
		DeepCopyIssueSlices(&issues[len(issues)-1])
		poolRefs = append(poolRefs, issue)
		return issues, poolRefs
	}

	var issue model.Issue
	if err := json.Unmarshal(line, &issue); err != nil {
		if stats != nil {
			stats.Errors++
		}
		// Skip malformed lines but warn
		warn(fmt.Sprintf("skipping malformed JSON on line %d: %v", lineNum, err))
		return issues, poolRefs
	}

	normalizeLoadedIssue(&issue)

	// Validate issue
	if err := issue.Validate(); err != nil {
		if stats != nil {
			stats.Errors++
		}
		// Skip invalid issues
		warn(fmt.Sprintf("skipping invalid issue on line %d: %v", lineNum, err))
		return issues, poolRefs
	}
	if stats != nil {
		stats.Valid++
	}

	if opts.IssueFilter != nil && !opts.IssueFilter(&issue) {
		return issues, poolRefs
	}

	issues = append(issues, issue)
	return issues, poolRefs
}

// Parallel-parse tuning. JSONL is line-independent, so for large files the
// JSON decode is embarrassingly parallel. Below these thresholds the goroutine
// + reassembly overhead outweighs the win, so we keep the serial path. The
// byte threshold also bounds the io.ReadAll buffer: we only slurp the whole
// file when it is big enough to benefit.
const (
	// parallelParseMinBytes is the file-size floor for attempting the parallel
	// path, set from the MEASURED crossover. The JSONL parse is dominated by
	// allocation/GC (per-issue decode + Validate), not raw CPU, so concurrency
	// only pays once the per-issue work outweighs the parallel path's extra
	// allocation (per-chunk slices + the order-preserving reassembly copy).
	// Measured on the project host (64c, Go 1.25.5), warm in-process:
	//   1.9MB  serial 13.4ms  vs parallel 15.3ms  (serial wins)
	//   4MB    serial 37.5ms  vs parallel 37.1ms  (crossover)
	//   8MB    serial 62.9ms  vs parallel 56.4ms  (parallel +10%)
	//   40MB   serial 246ms   vs parallel 203ms   (parallel +21%)
	// The repo's own ~1.9MB issues.jsonl therefore stays on the (faster) serial
	// path — no warm-path regression — while genuinely large stores (multi-MB
	// monorepo exports) get the parallel speedup. The threshold sits just below
	// the crossover so we never knowingly pick the slower path.
	parallelParseMinBytes = 4 * 1024 * 1024
	// parallelParseMinLines is the line-count floor; a few huge lines should
	// not trigger a parallel split that cannot actually distribute work.
	parallelParseMinLines = 512
	// parallelParseMinChunkBytes is the smallest chunk we will create. Chunks
	// smaller than this are dominated by per-chunk fixed costs (goroutine
	// hand-off, slice pre-sizing, result reassembly), so we never subdivide
	// below it even when there are many idle cores.
	parallelParseMinChunkBytes = 64 * 1024
	// parallelParseChunksPerWorker controls oversubscription: aiming for a few
	// chunks per worker keeps the morsel pool balanced (a worker that draws an
	// expensive chunk does not stall the whole pass) while bounding scheduling
	// overhead. The actual chunk size is derived from the file size so the
	// available cores are actually used instead of being starved by a fixed,
	// too-large chunk target.
	parallelParseChunksPerWorker = 3
)

// estimateIssueCap mirrors the serial pre-sizing heuristic: average issue line
// ~2KB, conservatively under-estimated, clamped to [64, 200k].
func estimateIssueCap(size int64) int {
	const avgIssueBytes = 2 * 1024
	const minCap = 64
	const maxCap = 200_000

	est := int(size / avgIssueBytes)
	if est < minCap && size > 0 {
		est = minCap
	}
	if est > maxCap {
		est = maxCap
	}
	return est
}

// resolveWarnHandler returns the effective warning sink: the caller's handler,
// or the default stderr printer (suppressed under BV_ROBOT=1).
func resolveWarnHandler(h func(string)) func(string) {
	if h != nil {
		return h
	}
	if os.Getenv("BV_ROBOT") == "1" {
		return func(string) {}
	}
	return func(msg string) {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", msg)
	}
}

// countLines counts newline-delimited records in data, matching the number of
// lineNum values the serial reader would assign (a trailing partial line with
// no newline still counts as one line).
func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++ // trailing line without a terminating newline
	}
	return n
}

// pendingWarn is a warning captured by a chunk worker, tagged with its global
// line number so the orchestrator can replay warnings in original line order.
type pendingWarn struct {
	lineNum int
	msg     string
}

// chunkResult holds one chunk's decoded output in original intra-chunk order,
// plus its accumulated stats and ordered warnings. Each worker owns its result
// exclusively (no shared mutable state), so there are no data races.
type chunkResult struct {
	issues   []model.Issue
	poolRefs []*model.Issue
	stats    ParseStats
	warns    []pendingWarn
}

// parseIssuesParallel decodes a whole JSONL buffer concurrently while remaining
// byte-equivalent to the serial parser. It splits data into line-aligned chunks
// at newline boundaries, decodes each chunk on a bounded worker pool via the
// shared processIssueLine, then reassembles issues/poolRefs in original order
// (chunk index, then intra-chunk index) and replays warnings in global line
// order. BOM is stripped from the first line of the first chunk only; the 10MB
// per-line cap, _type filtering, tombstone/normalize/validate semantics, and
// ParseStats accounting all match the serial path exactly.
func parseIssuesParallel(data []byte, opts ParseOptions, usePool bool, maxCapacity int) ([]model.Issue, []*model.Issue, error) {
	warn := resolveWarnHandler(opts.WarningHandler)

	// Build line-aligned chunk boundaries. Each chunk is [start,end) over data,
	// ending exactly after a '\n' (except possibly the last). We also record the
	// 1-based starting line number for each chunk so per-line warnings keep the
	// serial lineNum semantics.
	type chunkSpan struct {
		start, end int
		startLine  int // 1-based line number of the first line in this chunk
	}
	// Pick the worker count first, then derive a chunk size that actually
	// spreads the file across the available cores (a fixed, large chunk target
	// starves cores on mid-sized files). We aim for a few chunks per worker for
	// load balance, but never go below parallelParseMinChunkBytes.
	maxWorkers := runtime.GOMAXPROCS(0)
	if n := runtime.NumCPU(); n < maxWorkers {
		maxWorkers = n
	}
	if maxWorkers < 1 {
		maxWorkers = 1
	}

	targetChunk := len(data) / (maxWorkers * parallelParseChunksPerWorker)
	if targetChunk < parallelParseMinChunkBytes {
		targetChunk = parallelParseMinChunkBytes
	}

	var spans []chunkSpan
	pos := 0
	line := 1
	for pos < len(data) {
		end := pos + targetChunk
		if end >= len(data) {
			end = len(data)
		} else {
			// Extend to the next newline so chunks never split a JSON object.
			nl := bytes.IndexByte(data[end:], '\n')
			if nl < 0 {
				end = len(data)
			} else {
				end = end + nl + 1 // include the newline in this chunk
			}
		}
		spans = append(spans, chunkSpan{start: pos, end: end, startLine: line})
		// Count lines consumed by this chunk to seed the next chunk's startLine.
		line += countLines(data[pos:end])
		pos = end
	}

	results := make([]chunkResult, len(spans))

	// Bound concurrency to the usable CPUs, capped by the number of chunks
	// (no point spawning more workers than there is work to pull).
	workers := maxWorkers
	if workers > len(spans) {
		workers = len(spans)
	}

	// Central dispatcher: workers pull chunk indices off a buffered channel
	// (morsel-driven). Per-chunk pre-sizing keeps peak memory bounded — we never
	// materialize more than the per-chunk decoded issues plus the final slices.
	idxCh := make(chan int, len(spans))
	for i := range spans {
		idxCh <- i
	}
	close(idxCh)

	// worker pulls chunk indices off idxCh until it is drained and decodes each
	// chunk into its own results[ci] slot. It captures no loop variable: ci is a
	// fresh per-iteration range variable and the slots are disjoint, so there is
	// no shared mutable state (verified under `go test -race`). Defined once
	// outside the spawn loop so each goroutine shares this single closure.
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for ci := range idxCh {
			span := spans[ci]
			res := &results[ci]
			// Pre-size to the chunk's byte span (avg ~2KB/issue).
			if est := estimateIssueCap(int64(span.end - span.start)); est > 0 {
				res.issues = make([]model.Issue, 0, est)
				if usePool {
					res.poolRefs = make([]*model.Issue, 0, est)
				}
			}
			parseChunkLines(data[span.start:span.end], span.startLine, ci == 0, opts, usePool, maxCapacity, res)
		}
	}
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go worker()
	}
	wg.Wait()

	// Reassemble in original order and replay warnings in global line order.
	total := 0
	totalRefs := 0
	for i := range results {
		total += len(results[i].issues)
		totalRefs += len(results[i].poolRefs)
	}

	issues := make([]model.Issue, 0, total)
	var poolRefs []*model.Issue
	if usePool {
		poolRefs = make([]*model.Issue, 0, totalRefs)
	}
	var stats ParseStats
	for i := range results {
		issues = append(issues, results[i].issues...)
		if usePool {
			poolRefs = append(poolRefs, results[i].poolRefs...)
		}
		stats.Valid += results[i].stats.Valid
		stats.Errors += results[i].stats.Errors
		stats.Skipped += results[i].stats.Skipped
		// Warnings within a chunk are already in line order; chunks are in
		// order, so concatenating preserves global line order.
		for _, pw := range results[i].warns {
			warn(pw.msg)
		}
	}

	if opts.Stats != nil {
		opts.Stats.Valid += stats.Valid
		opts.Stats.Errors += stats.Errors
		opts.Stats.Skipped += stats.Skipped
	}

	return issues, poolRefs, nil
}

// parseChunkLines decodes one line-aligned chunk into res. It replicates the
// serial reader's per-line treatment: it splits on '\n', trims a trailing '\r'
// (bufio.Reader.ReadLine drops the CR of a CRLF), skips empty lines without
// consuming logic, strips the BOM from the very first line when isFirstChunk,
// enforces the per-line byte cap (lines longer than maxCapacity are skipped
// with the identical "line too long" warning and consume exactly one lineNum),
// and otherwise defers to processIssueLine. Warnings are buffered with their
// global line number for ordered replay by the caller.
func parseChunkLines(chunk []byte, startLine int, isFirstChunk bool, opts ParseOptions, usePool bool, maxCapacity int, res *chunkResult) {
	warn := func(lineNum int, msg string) {
		res.warns = append(res.warns, pendingWarn{lineNum: lineNum, msg: msg})
	}

	lineNum := startLine - 1
	for len(chunk) > 0 {
		lineNum++
		nl := bytes.IndexByte(chunk, '\n')
		var line []byte
		if nl < 0 {
			line = chunk
			chunk = nil
		} else {
			line = chunk[:nl]
			chunk = chunk[nl+1:]
		}
		// bufio.Reader.ReadLine strips the trailing CR of a CRLF line ending.
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}

		// Per-line byte cap. The serial path uses bufio.Reader.ReadLine, which
		// sets isPrefix (→ skip) once a line's content length REACHES the buffer
		// size, i.e. for len >= maxCapacity. Mirror that exactly (>=, not >), so a
		// line of length exactly maxCapacity is skipped identically to serial.
		if len(line) >= maxCapacity {
			warn(lineNum, fmt.Sprintf("skipping line %d: line too long (exceeds %d bytes)", lineNum, maxCapacity))
			continue
		}

		if len(line) == 0 {
			continue
		}

		if isFirstChunk && lineNum == 1 {
			line = stripBOM(line)
		}

		res.issues, res.poolRefs = processIssueLine(
			line, lineNum, opts, usePool,
			res.issues, res.poolRefs, &res.stats,
			func(msg string) { warn(lineNum, msg) },
		)
	}
}

// stripBOM removes the UTF-8 Byte Order Mark if present
func stripBOM(b []byte) []byte {
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		return b[3:]
	}
	return b
}

// recordType identifies the kind of record a beads JSONL line carries.
// `bd export` writes mixed records — issues by default plus memories,
// sprints, forecasts, etc. — and tags each line with a `_type` field
// (absent on the historical issue-only shape). Dispatching on this
// before unmarshalling lets the loader stop warning on every memory
// record (issue #145).
type recordType int

const (
	// recordTypeIssue is the default when `_type` is missing or "issue".
	recordTypeIssue recordType = iota
	recordTypeMemory
	recordTypeSprint
	recordTypeForecast
	recordTypeBurndown
	// recordTypeIgnore catches records the viewer currently has no use
	// for but that are valid beads output (e.g. `_type:"epic_link"`
	// in some forks); they should be skipped silently rather than
	// emit a malformed-JSON warning.
	recordTypeIgnore
	recordTypeUnknown
)

// recordTypeOf returns the record kind for a JSONL line by parsing
// only the `_type` field. Returns recordTypeIssue when `_type` is
// missing (the historical shape) or set to "issue".
func recordTypeOf(line []byte) recordType {
	// Fast path: most production lines are pre-v1.0-style issues with
	// no `_type` field at all. A bytes.Contains check avoids a JSON
	// decode for the common case.
	if !bytes.Contains(line, []byte(`"_type"`)) {
		return recordTypeIssue
	}
	var probe struct {
		Type string `json:"_type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		// Couldn't even parse the discriminator — fall through to
		// recordTypeIssue so the regular issue parser produces the
		// usual "skipping malformed JSON" warning at the existing
		// site, instead of being silently swallowed here.
		return recordTypeIssue
	}
	switch probe.Type {
	case "", "issue":
		return recordTypeIssue
	case "memory":
		return recordTypeMemory
	case "sprint":
		return recordTypeSprint
	case "forecast":
		return recordTypeForecast
	case "burndown":
		return recordTypeBurndown
	default:
		return recordTypeUnknown
	}
}

func normalizeIssueStatus(status model.Status) model.Status {
	trimmed := strings.TrimSpace(string(status))
	if trimmed == "" {
		return ""
	}
	return model.Status(strings.ToLower(trimmed))
}

func normalizeLoadedIssue(issue *model.Issue) {
	issue.Status = normalizeIssueStatus(issue.Status)
	for _, dep := range issue.Dependencies {
		if dep == nil {
			continue
		}
		if dep.IssueID == "" {
			dep.IssueID = issue.ID
		}
	}
}
