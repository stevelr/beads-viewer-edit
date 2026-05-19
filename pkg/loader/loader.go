package loader

import (
	"bufio"
	"bytes"
	"context"
	stdjson "encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	json "github.com/goccy/go-json"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// beadsMetadata is the subset of .beads/metadata.json we care about.
type beadsMetadata struct {
	Backend     string `json:"backend"`
	JSONLExport string `json:"jsonl_export"`
}

type brWhereOutput struct {
	Path           string `json:"path"`
	RedirectedFrom string `json:"redirected_from"`
	JSONLPath      string `json:"jsonl_path"`
	DatabasePath   string `json:"database_path"`
}

// BeadsDirEnvVar is the name of the environment variable for custom beads directory
const BeadsDirEnvVar = "BEADS_DIR"

// BeadsDBEnvVar is the name of the environment variable for a specific database file
// or .beads directory path. Takes priority over BEADS_DIR.
// Can point to a specific file (e.g., /path/to/.beads/beads.jsonl) or a .beads directory.
const BeadsDBEnvVar = "BEADS_DB"

// PreferredJSONLNames defines the priority order for looking up beads data files.
// Priority order matches bd's canonical naming (beads.jsonl) to ensure bv watches
// the same file that bd writes to in stealth/direct mode. Fixes bv-96.
var PreferredJSONLNames = []string{"beads.jsonl", "issues.jsonl", "beads.base.jsonl"}

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
		return resolveBRRedirectedBeadsDir(repoPath, beadsDir), nil
	}

	// If not found, check if we're in a git worktree and look in the main repo
	mainRepoRoot, err := getMainRepoRoot(repoPath)
	if err == nil && mainRepoRoot != "" && mainRepoRoot != repoPath {
		mainBeadsDir := filepath.Join(mainRepoRoot, ".beads")
		if _, err := os.Stat(mainBeadsDir); err == nil {
			return resolveBRRedirectedBeadsDir(repoPath, mainBeadsDir), nil
		}
	}

	// Return the original path even if .beads doesn't exist
	// (caller will handle the error)
	return beadsDir, nil
}

func resolveBRRedirectedBeadsDir(repoPath, beadsDir string) string {
	resolved, ok := brWhereBeadsDir(repoPath, beadsDir)
	if !ok {
		if trackerDir, trackerOK := siblingTrackerBeadsDir(repoPath, beadsDir); trackerOK {
			return trackerDir
		}
		return beadsDir
	}
	return resolved
}

func brWhereBeadsDir(repoPath, beadsDir string) (string, bool) {
	if repoPath == "" || beadsDir == "" {
		return "", false
	}
	if _, err := exec.LookPath("br"); err != nil {
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "br", "where", "--json")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}

	var where brWhereOutput
	if err := stdjson.Unmarshal(out, &where); err != nil {
		return "", false
	}
	if strings.TrimSpace(where.Path) == "" {
		return "", false
	}

	local := cleanAbsPath(beadsDir)
	active := cleanAbsPath(where.Path)
	redirectedFrom := cleanAbsPath(where.RedirectedFrom)
	if active == "" {
		return "", false
	}
	if redirectedFrom != "" {
		if redirectedFrom == local {
			return active, true
		}
		return "", false
	}
	if active == local {
		return active, true
	}
	return "", false
}

func cleanAbsPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func siblingTrackerBeadsDir(repoPath, beadsDir string) (string, bool) {
	repoPath = cleanAbsPath(repoPath)
	beadsDir = cleanAbsPath(beadsDir)
	if repoPath == "" || beadsDir == "" {
		return "", false
	}
	if beadsDir != cleanAbsPath(filepath.Join(repoPath, ".beads")) {
		return "", false
	}

	trackerBeadsDir := filepath.Join(repoPath+".tracker", ".beads")
	info, err := os.Stat(trackerBeadsDir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	if _, err := FindJSONLPath(trackerBeadsDir); err != nil {
		return "", false
	}
	return trackerBeadsDir, true
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
// Prefers beads.jsonl (canonical per bd) over issues.jsonl (legacy) to match
// the file that bd writes to in stealth/direct mode. Fixes bv-96.
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
	// Default (br stack): metadata.json jsonl_export, then beads.jsonl -> issues.jsonl -> beads.base.jsonl
	// In bd workspaces: issues.jsonl is the canonical compatibility export
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
	var issues []model.Issue
	var poolRefs []*model.Issue
	if f, ok := r.(*os.File); ok {
		if info, err := f.Stat(); err == nil {
			// Heuristic: average issue line ~2KB. Prefer conservative underestimation to
			// avoid large over-allocations for big files.
			const avgIssueBytes = 2 * 1024
			const minCap = 64
			const maxCap = 200_000

			est := int(info.Size() / avgIssueBytes)
			if est < minCap && info.Size() > 0 {
				est = minCap
			}
			if est > maxCap {
				est = maxCap
			}
			if est > 0 {
				issues = make([]model.Issue, 0, est)
				if usePool {
					poolRefs = make([]*model.Issue, 0, est)
				}
			}
		}
	}

	// Determine buffer size
	maxCapacity := opts.BufferSize
	if maxCapacity <= 0 {
		maxCapacity = DefaultMaxBufferSize
	}

	reader := bufio.NewReaderSize(r, maxCapacity)

	// Default warning handler prints to stderr (suppressed in robot mode).
	warn := opts.WarningHandler
	if warn == nil {
		if os.Getenv("BV_ROBOT") == "1" {
			warn = func(string) {}
		} else {
			warn = func(msg string) {
				fmt.Fprintf(os.Stderr, "Warning: %s\n", msg)
			}
		}
	}

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
			continue
		default:
			// Unknown _type: don't fail, but don't pretend it was an
			// issue either. A debug-level breadcrumb is enough; the
			// noisy "issue ID cannot be empty" warning was the actual
			// bug being reported.
			continue
		}

		if usePool {
			issue := GetIssue()
			if err := json.Unmarshal(line, issue); err != nil {
				PutIssue(issue)
				// Skip malformed lines but warn
				warn(fmt.Sprintf("skipping malformed JSON on line %d: %v", lineNum, err))
				continue
			}

			normalizeLoadedIssue(issue)

			// Validate issue
			if err := issue.Validate(); err != nil {
				PutIssue(issue)
				// Skip invalid issues
				warn(fmt.Sprintf("skipping invalid issue on line %d: %v", lineNum, err))
				continue
			}

			if opts.IssueFilter != nil && !opts.IssueFilter(issue) {
				PutIssue(issue)
				continue
			}

			// Append the struct value first, then deep-copy slice fields on the VALUE
			// copy to break sharing with pooled backing arrays. This ensures that when
			// the pooled issue is returned to the pool and its backing arrays are reused,
			// the copied issue in the snapshot is not affected (bv-fn4b).
			issues = append(issues, *issue)
			DeepCopyIssueSlices(&issues[len(issues)-1])
			poolRefs = append(poolRefs, issue)
		} else {
			var issue model.Issue
			if err := json.Unmarshal(line, &issue); err != nil {
				// Skip malformed lines but warn
				warn(fmt.Sprintf("skipping malformed JSON on line %d: %v", lineNum, err))
				continue
			}

			normalizeLoadedIssue(&issue)

			// Validate issue
			if err := issue.Validate(); err != nil {
				// Skip invalid issues
				warn(fmt.Sprintf("skipping invalid issue on line %d: %v", lineNum, err))
				continue
			}

			if opts.IssueFilter != nil && !opts.IssueFilter(&issue) {
				continue
			}

			issues = append(issues, issue)
		}
	}

	return issues, poolRefs, nil
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
