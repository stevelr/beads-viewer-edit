package workspace

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/Dicklesworthstone/beads_viewer/pkg/loader"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// LoadResult contains the result of loading a single repository
type LoadResult struct {
	// RepoName is the name of the repository
	RepoName string

	// Prefix is the namespace prefix used for IDs
	Prefix string

	// Issues are the loaded issues with namespaced IDs
	Issues []model.Issue

	// Error is set if loading failed
	Error error
}

// AggregateLoader loads issues from multiple repositories in a workspace
type AggregateLoader struct {
	config        *Config
	workspaceRoot string
	logger        *log.Logger
}

// NewAggregateLoader creates a new aggregate loader for the given workspace config
func NewAggregateLoader(config *Config, workspaceRoot string) *AggregateLoader {
	return &AggregateLoader{
		config:        config,
		workspaceRoot: workspaceRoot,
		// Silence by default. Callers can opt-in via SetLogger.
		// This avoids polluting stderr (e.g., breaking robot JSON consumers that
		// capture combined stdout/stderr).
		logger: log.New(io.Discard, "", 0),
	}
}

// SetLogger sets a custom logger for error reporting.
// Passing nil substitutes a discard logger to prevent nil pointer dereferences.
func (l *AggregateLoader) SetLogger(logger *log.Logger) {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	l.logger = logger
}

// LoadAll loads issues from all enabled repositories in the workspace.
// Returns the merged list of issues with namespaced IDs.
// Failed repos are logged and tolerated as long as at least one repo loads.
func (l *AggregateLoader) LoadAll(ctx context.Context) ([]model.Issue, []LoadResult, error) {
	if l.config == nil {
		return nil, nil, fmt.Errorf("workspace config is nil")
	}

	// Collect enabled repos
	enabledRepos, err := l.getEnabledRepos()
	if err != nil {
		return nil, nil, err
	}
	if len(enabledRepos) == 0 {
		return nil, nil, fmt.Errorf("no enabled repositories in workspace")
	}

	// Load repos in parallel using errgroup
	results, err := l.loadReposParallel(ctx, enabledRepos)
	if err != nil {
		return nil, results, fmt.Errorf("fatal error during parallel loading: %w", err)
	}

	// Merge all successfully loaded issues
	var allIssues []model.Issue
	var failedRepoNames []string
	var firstRepoErr error
	for _, result := range results {
		if result.Error != nil {
			// Log but continue - individual repo failures don't break the whole load
			l.logRepoError(result.RepoName, result.Error)
			failedRepoNames = append(failedRepoNames, result.RepoName)
			if firstRepoErr == nil {
				firstRepoErr = result.Error
			}
			continue
		}
		allIssues = append(allIssues, result.Issues...)
	}

	if len(failedRepoNames) == len(results) {
		return nil, results, fmt.Errorf("all %d enabled repositories failed to load (%s): %w",
			len(results), strings.Join(failedRepoNames, ", "), firstRepoErr)
	}

	return allIssues, results, nil
}

// getEnabledRepos returns all explicitly configured and discovered enabled repos.
func (l *AggregateLoader) getEnabledRepos() ([]RepoConfig, error) {
	var enabled []RepoConfig
	seenPaths := make(map[string]bool)
	seenPrefixes := make(map[string]bool)

	addRepo := func(repo RepoConfig) error {
		repo = l.applyDefaults(repo)

		pathKey, err := l.repoPathKey(repo)
		if err != nil {
			return err
		}
		if seenPaths[pathKey] {
			return nil
		}
		seenPaths[pathKey] = true

		if !repo.IsEnabled() {
			return nil
		}

		prefixKey := strings.ToLower(repo.GetPrefix())
		if seenPrefixes[prefixKey] {
			return fmt.Errorf("duplicate workspace prefix %q", repo.GetPrefix())
		}

		seenPrefixes[prefixKey] = true
		enabled = append(enabled, repo)
		return nil
	}

	for _, repo := range l.config.Repos {
		if err := addRepo(repo); err != nil {
			return nil, err
		}
	}

	if l.config.Discovery.Enabled {
		discovered, err := l.discoverRepos()
		if err != nil {
			return nil, err
		}
		for _, repo := range discovered {
			if err := addRepo(repo); err != nil {
				return nil, err
			}
		}
	}

	return enabled, nil
}

func (l *AggregateLoader) applyDefaults(repo RepoConfig) RepoConfig {
	if repo.BeadsPath == "" && l.config != nil && l.config.Defaults.BeadsPath != "" {
		repo.BeadsPath = l.config.Defaults.BeadsPath
	}
	return repo
}

func (l *AggregateLoader) defaultBeadsPath() string {
	if l.config != nil && l.config.Defaults.BeadsPath != "" {
		return l.config.Defaults.BeadsPath
	}
	return ".beads"
}

func (l *AggregateLoader) repoPathKey(repo RepoConfig) (string, error) {
	path := l.resolveRepoPath(repo.Path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve repo path %q: %w", repo.Path, err)
	}
	return filepath.Clean(abs), nil
}

func (l *AggregateLoader) resolveRepoPath(repoPath string) string {
	if !filepath.IsAbs(repoPath) {
		repoPath = filepath.Join(l.workspaceRoot, repoPath)
	}
	return repoPath
}

func (l *AggregateLoader) discoverRepos() ([]RepoConfig, error) {
	root := l.workspaceRoot
	if root == "" {
		root = "."
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %q: %w", root, err)
	}

	patterns := l.config.Discovery.Patterns
	if len(patterns) == 0 {
		patterns = DefaultDiscoveryPatterns()
	}
	excludes := l.config.Discovery.Exclude
	if len(excludes) == 0 {
		excludes = DefaultExcludePatterns()
	}
	maxDepth := l.config.Discovery.MaxDepth
	if maxDepth == 0 {
		maxDepth = 2
	}

	var repos []RepoConfig
	seen := make(map[string]bool)
	for _, pattern := range patterns {
		glob := filepath.Join(rootAbs, filepath.FromSlash(pattern))
		matches, err := filepath.Glob(glob)
		if err != nil {
			return nil, fmt.Errorf("invalid discovery pattern %q: %w", pattern, err)
		}
		sort.Strings(matches)

		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(rootAbs, match)
			if err != nil {
				return nil, fmt.Errorf("resolve discovered repo %q: %w", match, err)
			}
			rel = filepath.ToSlash(filepath.Clean(rel))
			if rel == "." {
				rel = "."
			}
			if discoveryDepth(rel) > maxDepth || discoveryExcluded(rel, excludes) {
				continue
			}
			if seen[rel] {
				continue
			}

			beadsPath := l.defaultBeadsPath()
			if _, err := loader.FindJSONLPath(filepath.Join(match, beadsPath)); err != nil {
				continue
			}

			repos = append(repos, RepoConfig{
				Path:      rel,
				BeadsPath: beadsPath,
			})
			seen[rel] = true
		}
	}

	return repos, nil
}

func discoveryDepth(rel string) int {
	if rel == "" || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

func discoveryExcluded(rel string, excludes []string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	base := filepath.Base(rel)
	parts := strings.Split(rel, "/")

	for _, raw := range excludes {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		pattern = filepath.ToSlash(filepath.Clean(pattern))
		if pattern == rel || pattern == base {
			return true
		}
		if ok, _ := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(rel)); ok {
			return true
		}
		if ok, _ := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(base)); ok {
			return true
		}
		for _, part := range parts {
			if pattern == part {
				return true
			}
		}
	}

	return false
}

// loadReposParallel loads issues from all repos concurrently using errgroup
func (l *AggregateLoader) loadReposParallel(ctx context.Context, repos []RepoConfig) ([]LoadResult, error) {
	results := make([]LoadResult, len(repos))
	knownPrefixes := knownRepoPrefixes(repos)

	g, ctx := errgroup.WithContext(ctx)
	// Limit concurrency to avoid resource exhaustion (file descriptors, memory)
	g.SetLimit(32)

	for i, repo := range repos {
		i, repo := i, repo // capture loop variables

		g.Go(func() error {
			select {
			case <-ctx.Done():
				results[i] = LoadResult{
					RepoName: repo.GetName(),
					Prefix:   repo.GetPrefix(),
					Error:    ctx.Err(),
				}
				return nil // Don't propagate context errors as fatal
			default:
			}

			issues, err := l.loadSingleRepo(repo, knownPrefixes)

			results[i] = LoadResult{
				RepoName: repo.GetName(),
				Prefix:   repo.GetPrefix(),
				Issues:   issues,
				Error:    err,
			}

			return nil // Individual repo errors are captured in results, not propagated
		})
	}

	// Wait for all goroutines to complete
	if err := g.Wait(); err != nil {
		return results, err
	}

	if l.logger != nil {
		l.logger.Printf("Finished parallel loading of %d repos", len(repos))
	}

	return results, nil
}

// loadSingleRepo loads issues from a single repository and namespaced them
func (l *AggregateLoader) loadSingleRepo(repo RepoConfig, knownPrefixes map[string]bool) ([]model.Issue, error) {
	// Resolve the repo path relative to workspace root
	repo = l.applyDefaults(repo)
	repoPath := l.resolveRepoPath(repo.Path)

	// Load raw issues from the repo, respecting custom beads path if provided
	beadsDir := filepath.Join(repoPath, repo.GetBeadsPath())
	jsonlPath, err := loader.FindJSONLPath(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load issues from %s: %w", repo.GetName(), err)
	}
	issues, err := loader.LoadIssuesFromFile(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load issues from %s: %w", repo.GetName(), err)
	}

	// Build map of local IDs for conflict resolution
	localIDs := make(map[string]bool, len(issues))
	for _, issue := range issues {
		localIDs[issue.ID] = true
	}

	// Apply namespacing to all IDs
	prefix := repo.GetPrefix()
	namespacedIssues := l.namespaceIssues(issues, prefix, localIDs, knownPrefixes)

	return namespacedIssues, nil
}

func knownRepoPrefixes(repos []RepoConfig) map[string]bool {
	prefixes := make(map[string]bool, len(repos))
	for _, repo := range repos {
		prefix := repo.GetPrefix()
		if prefix != "" {
			prefixes[prefix] = true
		}
	}
	return prefixes
}

func sourceRepoKeyFromPrefix(prefix string) string {
	key := strings.TrimSpace(prefix)
	key = strings.TrimRight(key, "-:_")
	return strings.ToLower(key)
}

// namespaceIssues adds the prefix to all issue IDs and dependency references
// It mutates the issues slice in place to reduce allocations.
func (l *AggregateLoader) namespaceIssues(issues []model.Issue, prefix string, localIDs map[string]bool, knownPrefixes map[string]bool) []model.Issue {
	sourceRepo := sourceRepoKeyFromPrefix(prefix)

	for i := range issues {
		// Mutate issue in place
		issue := &issues[i]
		issue.ID = QualifyID(issue.ID, prefix)
		issue.SourceRepo = sourceRepo

		// Namespace dependency references in place
		for _, dep := range issue.Dependencies {
			if dep == nil {
				continue
			}
			dep.IssueID = QualifyID(dep.IssueID, prefix)

			// Resolve DependsOnID
			if localIDs[dep.DependsOnID] {
				dep.DependsOnID = QualifyID(dep.DependsOnID, prefix)
			} else if hasKnownPrefix(dep.DependsOnID, knownPrefixes) {
				// External reference, keep as is
			} else {
				// Assume local
				dep.DependsOnID = QualifyID(dep.DependsOnID, prefix)
			}
		}

		// Namespace comment issue references in place
		for _, comment := range issue.Comments {
			if comment == nil {
				continue
			}
			comment.IssueID = QualifyID(comment.IssueID, prefix)
		}
	}

	return issues
}

// hasKnownPrefix checks if an ID already has a known namespace prefix.
func hasKnownPrefix(id string, knownPrefixes map[string]bool) bool {
	for prefix := range knownPrefixes {
		if len(id) > len(prefix) && id[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// logRepoError logs an error for a repo that failed to load
func (l *AggregateLoader) logRepoError(repoName string, err error) {
	if l.logger != nil {
		l.logger.Printf("WARNING: Failed to load repo %q: %v", repoName, err)
	}
}

// LoadAllFromConfig is a convenience function that loads a workspace config and all its repos
func LoadAllFromConfig(ctx context.Context, configPath string) ([]model.Issue, []LoadResult, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load workspace config: %w", err)
	}

	workspaceRoot := filepath.Dir(filepath.Dir(configPath)) // .bv/workspace.yaml -> workspace root
	loader := NewAggregateLoader(config, workspaceRoot)

	return loader.LoadAll(ctx)
}

// Summary returns a summary of load results
type LoadSummary struct {
	TotalRepos      int
	SuccessfulRepos int
	FailedRepos     int
	TotalIssues     int
	FailedRepoNames []string
	RepoPrefixes    []string // Prefixes of successfully loaded repos
}

// Summarize returns a summary of the load results
func Summarize(results []LoadResult) LoadSummary {
	summary := LoadSummary{
		TotalRepos: len(results),
	}

	for _, result := range results {
		if result.Error != nil {
			summary.FailedRepos++
			summary.FailedRepoNames = append(summary.FailedRepoNames, result.RepoName)
		} else {
			summary.SuccessfulRepos++
			summary.TotalIssues += len(result.Issues)
			if result.Prefix != "" {
				summary.RepoPrefixes = append(summary.RepoPrefixes, result.Prefix)
			}
		}
	}

	return summary
}
