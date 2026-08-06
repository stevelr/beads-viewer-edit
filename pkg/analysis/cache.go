package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	json "github.com/goccy/go-json"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
	"github.com/Dicklesworthstone/beads_viewer/pkg/xfetch"
)

const (
	robotAnalysisDiskCacheVersion      = 2
	robotAnalysisDiskCacheFileName     = "analysis_cache.json"
	robotAnalysisDiskCacheDirName      = "bv"
	robotAnalysisDiskCacheMaxEntries   = 10
	robotAnalysisDiskCacheMaxAge       = 24 * time.Hour
	robotAnalysisDiskCacheMaxEntrySize = 10 << 20 // 10MB
)

// Cache holds cached analysis results keyed by data hash.
// Thread-safe for concurrent access.
type Cache struct {
	mu         sync.RWMutex
	dataHash   string
	stats      *GraphStats
	computedAt time.Time
	ttl        time.Duration
}

// DefaultCacheTTL is the default time-to-live for cached results.
const DefaultCacheTTL = 5 * time.Minute

// globalCache is the package-level cache instance.
var globalCache = &Cache{
	ttl: DefaultCacheTTL,
}

// GetGlobalCache returns the global cache instance.
func GetGlobalCache() *Cache {
	return globalCache
}

// NewCache creates a new cache with the specified TTL.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl: ttl,
	}
}

// Get retrieves cached stats if the data hash matches and TTL hasn't expired.
// Returns (stats, true) on cache hit, (nil, false) on cache miss.
func (c *Cache) Get(issues []model.Issue) (*GraphStats, bool) {
	// Compute hash outside the lock (expensive operation)
	hash := ComputeDataHash(issues)
	return c.GetByHash(hash)
}

// GetByHash retrieves cached stats if the hash matches and TTL hasn't expired.
// This is more efficient when the hash has already been computed.
func (c *Cache) GetByHash(hash string) (*GraphStats, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.stats == nil {
		return nil, false
	}

	if hash == c.dataHash && time.Since(c.computedAt) < c.ttl {
		return c.stats, true
	}
	return nil, false
}

// Set stores analysis results in the cache.
func (c *Cache) Set(issues []model.Issue, stats *GraphStats) {
	// Compute hash outside the lock (expensive operation)
	hash := ComputeDataHash(issues)
	c.SetByHash(hash, stats)
}

// SetByHash stores analysis results with a pre-computed hash.
func (c *Cache) SetByHash(hash string, stats *GraphStats) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.dataHash = hash
	c.stats = stats
	c.computedAt = time.Now()
}

// Invalidate clears the cache.
func (c *Cache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.dataHash = ""
	c.stats = nil
	c.computedAt = time.Time{}
}

// SetTTL updates the cache TTL.
func (c *Cache) SetTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ttl = ttl
}

// Hash returns the current data hash, or empty string if no cached data.
func (c *Cache) Hash() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dataHash
}

// Stats returns cache statistics for debugging.
func (c *Cache) Stats() (hash string, age time.Duration, hasData bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.stats == nil {
		return "", 0, false
	}
	return c.dataHash, time.Since(c.computedAt), true
}

// ComputeDataHash generates a deterministic hash of issue data.
// The hash includes issue IDs, content hashes, and dependency relationships.
// Issues are sorted by ID to ensure consistent hashing regardless of input order.
func ComputeDataHash(issues []model.Issue) string {
	if len(issues) == 0 {
		return "empty"
	}

	// Sort issues by ID for deterministic ordering
	sorted := make([]model.Issue, len(issues))
	copy(sorted, issues)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	h := sha256.New()
	for _, issue := range sorted {
		// Core identity
		h.Write([]byte(issue.ID))
		h.Write([]byte{0})

		// Important scalar fields
		h.Write([]byte(issue.Title))
		h.Write([]byte{0})
		h.Write([]byte(issue.Description))
		h.Write([]byte{0})
		h.Write([]byte(issue.Notes))
		h.Write([]byte{0})
		h.Write([]byte(issue.Design))
		h.Write([]byte{0})
		h.Write([]byte(issue.AcceptanceCriteria))
		h.Write([]byte{0})
		h.Write([]byte(issue.Assignee))
		h.Write([]byte{0})
		h.Write([]byte(issue.SourceRepo))
		h.Write([]byte{0})
		if issue.ExternalRef != nil {
			h.Write([]byte(*issue.ExternalRef))
		}
		h.Write([]byte{0})

		h.Write([]byte(issue.Status))
		h.Write([]byte{0})
		h.Write([]byte(issue.IssueType))
		h.Write([]byte{0})

		// Numeric fields
		h.Write([]byte(strconv.Itoa(issue.Priority)))
		h.Write([]byte{0})
		if issue.EstimatedMinutes != nil {
			h.Write([]byte(strconv.Itoa(*issue.EstimatedMinutes)))
		}
		h.Write([]byte{0})
		h.Write([]byte(issue.CreatedAt.UTC().Format(time.RFC3339Nano)))
		h.Write([]byte{0})
		h.Write([]byte(issue.UpdatedAt.UTC().Format(time.RFC3339Nano)))
		h.Write([]byte{0})
		if issue.ClosedAt != nil {
			h.Write([]byte(issue.ClosedAt.UTC().Format(time.RFC3339Nano)))
		}
		h.Write([]byte{0})

		// Labels (sorted for determinism)
		if len(issue.Labels) > 0 {
			labels := append([]string(nil), issue.Labels...)
			sort.Strings(labels)
			for _, lbl := range labels {
				h.Write([]byte(lbl))
				h.Write([]byte{0})
			}
		}
		h.Write([]byte{0})

		// Dependencies (sorted)
		if len(issue.Dependencies) > 0 {
			type depKey struct {
				dependsOn string
				depType   string
				createdAt string
				createdBy string
			}
			deps := make([]depKey, 0, len(issue.Dependencies))
			for _, dep := range issue.Dependencies {
				if dep == nil {
					continue
				}
				deps = append(deps, depKey{
					dependsOn: dep.DependsOnID,
					depType:   string(dep.Type),
					createdAt: dep.CreatedAt.UTC().Format(time.RFC3339Nano),
					createdBy: dep.CreatedBy,
				})
			}
			sort.Slice(deps, func(i, j int) bool {
				if deps[i].dependsOn != deps[j].dependsOn {
					return deps[i].dependsOn < deps[j].dependsOn
				}
				if deps[i].depType != deps[j].depType {
					return deps[i].depType < deps[j].depType
				}
				if deps[i].createdAt != deps[j].createdAt {
					return deps[i].createdAt < deps[j].createdAt
				}
				return deps[i].createdBy < deps[j].createdBy
			})
			for _, dep := range deps {
				h.Write([]byte(dep.dependsOn))
				h.Write([]byte{0})
				h.Write([]byte(dep.depType))
				h.Write([]byte{0})
				h.Write([]byte(dep.createdAt))
				h.Write([]byte{0})
				h.Write([]byte(dep.createdBy))
				h.Write([]byte{0})
			}
		}
		h.Write([]byte{0})

		// Comments (sorted)
		if len(issue.Comments) > 0 {
			type commentKey struct {
				id        string
				author    string
				text      string
				createdAt string
			}
			comments := make([]commentKey, 0, len(issue.Comments))
			for _, comment := range issue.Comments {
				if comment == nil {
					continue
				}
				comments = append(comments, commentKey{
					id:        comment.ID,
					author:    comment.Author,
					text:      comment.Text,
					createdAt: comment.CreatedAt.UTC().Format(time.RFC3339Nano),
				})
			}
			sort.Slice(comments, func(i, j int) bool {
				if comments[i].id != comments[j].id {
					return comments[i].id < comments[j].id
				}
				if comments[i].createdAt != comments[j].createdAt {
					return comments[i].createdAt < comments[j].createdAt
				}
				if comments[i].author != comments[j].author {
					return comments[i].author < comments[j].author
				}
				return comments[i].text < comments[j].text
			})
			for _, comment := range comments {
				h.Write([]byte(comment.id))
				h.Write([]byte{0})
				h.Write([]byte(comment.author))
				h.Write([]byte{0})
				h.Write([]byte(comment.text))
				h.Write([]byte{0})
				h.Write([]byte(comment.createdAt))
				h.Write([]byte{0})
			}
		}

		h.Write([]byte{1}) // issue separator
	}

	return hex.EncodeToString(h.Sum(nil))[:16] // Use first 16 chars for brevity
}

// IssueFingerprint represents a per-issue hash split across content and dependencies.
// It supports fast diffing between snapshots without a full rebuild.
type IssueFingerprint struct {
	ID             string
	ContentHash    string
	DependencyHash string
}

// IssueDiff captures a per-issue diff between two snapshots.
type IssueDiff struct {
	Added             []string
	Removed           []string
	Modified          []string
	ContentChanged    []string
	DependencyChanged []string
	Unchanged         []string
}

// ComputeIssueFingerprint returns the fingerprint for a single issue.
func ComputeIssueFingerprint(issue model.Issue) IssueFingerprint {
	return IssueFingerprint{
		ID:             issue.ID,
		ContentHash:    computeIssueContentHash(issue),
		DependencyHash: computeIssueDependencyHash(issue),
	}
}

// ComputeIssueDiff compares old and new issue slices and returns an IssueDiff.
func ComputeIssueDiff(oldIssues, newIssues []model.Issue) IssueDiff {
	oldFP := make(map[string]IssueFingerprint, len(oldIssues))
	for i := range oldIssues {
		fp := ComputeIssueFingerprint(oldIssues[i])
		oldFP[fp.ID] = fp
	}
	newFP := make(map[string]IssueFingerprint, len(newIssues))
	for i := range newIssues {
		fp := ComputeIssueFingerprint(newIssues[i])
		newFP[fp.ID] = fp
	}

	var diff IssueDiff
	for id, newIssue := range newFP {
		oldIssue, exists := oldFP[id]
		if !exists {
			diff.Added = append(diff.Added, id)
			continue
		}
		contentChanged := oldIssue.ContentHash != newIssue.ContentHash
		dependencyChanged := oldIssue.DependencyHash != newIssue.DependencyHash
		if contentChanged || dependencyChanged {
			diff.Modified = append(diff.Modified, id)
			if contentChanged {
				diff.ContentChanged = append(diff.ContentChanged, id)
			}
			if dependencyChanged {
				diff.DependencyChanged = append(diff.DependencyChanged, id)
			}
			continue
		}
		diff.Unchanged = append(diff.Unchanged, id)
	}

	for id := range oldFP {
		if _, exists := newFP[id]; !exists {
			diff.Removed = append(diff.Removed, id)
		}
	}

	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Modified)
	sort.Strings(diff.ContentChanged)
	sort.Strings(diff.DependencyChanged)
	sort.Strings(diff.Unchanged)
	return diff
}

func computeIssueContentHash(issue model.Issue) string {
	h := sha256.New()

	writeStringHash(h, issue.Title)
	writeStringHash(h, issue.Description)
	writeStringHash(h, issue.Design)
	writeStringHash(h, issue.AcceptanceCriteria)
	writeStringHash(h, issue.Notes)
	writeStringHash(h, issue.Assignee)
	writeStringHash(h, issue.SourceRepo)
	writeStringPtrHash(h, issue.ExternalRef)

	writeStringHash(h, string(issue.Status))
	writeStringHash(h, string(issue.IssueType))
	writeIntHash(h, issue.Priority)
	writeIntPtrHash(h, issue.EstimatedMinutes)
	writeTimeHash(h, issue.CreatedAt)
	writeTimeHash(h, issue.UpdatedAt)
	writeTimePtrHash(h, issue.DueDate)
	writeTimePtrHash(h, issue.ClosedAt)

	writeIntHash(h, issue.CompactionLevel)
	writeTimePtrHash(h, issue.CompactedAt)
	writeStringPtrHash(h, issue.CompactedAtCommit)
	writeIntHash(h, issue.OriginalSize)

	if len(issue.Labels) > 0 {
		labels := append([]string(nil), issue.Labels...)
		sort.Strings(labels)
		for _, label := range labels {
			writeStringHash(h, label)
		}
	}
	writeStringHash(h, "")

	if len(issue.Comments) > 0 {
		comments := make([]*model.Comment, 0, len(issue.Comments))
		for _, comment := range issue.Comments {
			if comment != nil {
				comments = append(comments, comment)
			}
		}
		sort.Slice(comments, func(i, j int) bool {
			if comments[i].ID != comments[j].ID {
				return comments[i].ID < comments[j].ID
			}
			return comments[i].CreatedAt.Before(comments[j].CreatedAt)
		})
		for _, comment := range comments {
			writeStringHash(h, comment.ID)
			writeStringHash(h, comment.IssueID)
			writeStringHash(h, comment.Author)
			writeStringHash(h, comment.Text)
			writeTimeHash(h, comment.CreatedAt)
		}
	}
	writeStringHash(h, "")

	return hex.EncodeToString(h.Sum(nil))[:16]
}

func computeIssueDependencyHash(issue model.Issue) string {
	if len(issue.Dependencies) == 0 {
		return "none"
	}
	type depKey struct {
		dependsOn string
		depType   string
		createdAt string
		createdBy string
	}
	deps := make([]depKey, 0, len(issue.Dependencies))
	for _, dep := range issue.Dependencies {
		if dep == nil {
			continue
		}
		deps = append(deps, depKey{
			dependsOn: dep.DependsOnID,
			depType:   string(dep.Type),
			createdAt: dep.CreatedAt.UTC().Format(time.RFC3339Nano),
			createdBy: dep.CreatedBy,
		})
	}
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].dependsOn != deps[j].dependsOn {
			return deps[i].dependsOn < deps[j].dependsOn
		}
		if deps[i].depType != deps[j].depType {
			return deps[i].depType < deps[j].depType
		}
		if deps[i].createdAt != deps[j].createdAt {
			return deps[i].createdAt < deps[j].createdAt
		}
		return deps[i].createdBy < deps[j].createdBy
	})

	h := sha256.New()
	for _, dep := range deps {
		writeStringHash(h, dep.dependsOn)
		writeStringHash(h, dep.depType)
		writeStringHash(h, dep.createdAt)
		writeStringHash(h, dep.createdBy)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func writeStringHash(w io.Writer, v string) {
	if v != "" {
		_, _ = io.WriteString(w, v)
	}
	_, _ = w.Write([]byte{0})
}

func writeStringPtrHash(w io.Writer, v *string) {
	if v != nil {
		_, _ = io.WriteString(w, *v)
	}
	_, _ = w.Write([]byte{0})
}

func writeIntHash(w io.Writer, v int) {
	_, _ = io.WriteString(w, strconv.Itoa(v))
	_, _ = w.Write([]byte{0})
}

func writeIntPtrHash(w io.Writer, v *int) {
	if v != nil {
		_, _ = io.WriteString(w, strconv.Itoa(*v))
	}
	_, _ = w.Write([]byte{0})
}

func writeInt64Hash(w io.Writer, v int64) {
	_, _ = io.WriteString(w, strconv.FormatInt(v, 10))
	_, _ = w.Write([]byte{0})
}

func writeTimeHash(w io.Writer, t time.Time) {
	if !t.IsZero() {
		_, _ = io.WriteString(w, t.UTC().Format(time.RFC3339Nano))
	}
	_, _ = w.Write([]byte{0})
}

func writeTimePtrHash(w io.Writer, t *time.Time) {
	if t != nil {
		_, _ = io.WriteString(w, t.UTC().Format(time.RFC3339Nano))
	}
	_, _ = w.Write([]byte{0})
}

// ComputeConfigHash generates a deterministic hash of the analysis configuration.
func ComputeConfigHash(config *AnalysisConfig) string {
	if config == nil {
		return "dynamic"
	}
	h := sha256.New()
	// Using %#v is stable enough for configuration struct
	h.Write([]byte(fmt.Sprintf("%#v", *config)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// CachedAnalyzer wraps an Analyzer with caching support.
type CachedAnalyzer struct {
	*Analyzer
	cache      *Cache
	issues     []model.Issue
	dataHash   string // Hash of the issue data
	configHash string // Hash of the configuration
	cacheHit   bool   // Set by AnalyzeAsync to track if it was a cache hit
}

// NewCachedAnalyzer creates an analyzer that checks the cache before computing.
// The Analyzer is always created because it may be needed for GenerateRecommendations
// even on cache hit. Creating the Analyzer (graph building) is O(V+E) which is fast;
// the expensive part is the analysis itself, which we skip on cache hit.
func NewCachedAnalyzer(issues []model.Issue, cache *Cache) *CachedAnalyzer {
	if cache == nil {
		cache = globalCache
	}
	analyzer := NewAnalyzer(issues)
	return &CachedAnalyzer{
		Analyzer: analyzer,
		cache:    cache,
		issues:   issues,
		// Reuse the analyzer's memoized data hash so the disk-cache key path
		// (AnalyzeAsyncWithConfig) and this struct share one SHA256 computation.
		dataHash:   analyzer.DataHash(),
		configHash: "dynamic",
	}
}

// SetConfig updates the analyzer configuration and the configuration hash.
func (ca *CachedAnalyzer) SetConfig(config *AnalysisConfig) {
	ca.Analyzer.SetConfig(config)
	ca.configHash = ComputeConfigHash(config)
}

// AnalyzeAsync returns cached stats if available, otherwise computes and caches.
func (ca *CachedAnalyzer) AnalyzeAsync(ctx context.Context) *GraphStats {
	// Combined key: dataHash|configHash
	fullHash := ca.dataHash + "|" + ca.configHash

	// Check cache first
	if stats, ok := ca.cache.GetByHash(fullHash); ok {
		ca.cacheHit = true
		return stats
	}

	// Cache miss - compute fresh
	ca.cacheHit = false
	stats := ca.Analyzer.AnalyzeAsync(ctx)

	// Store in cache when Phase 2 completes
	go func() {
		stats.WaitForPhase2()
		ca.cache.SetByHash(fullHash, stats)
	}()

	return stats
}

// Analyze returns cached stats if available, otherwise computes synchronously.
// Note: This returns a value copy that shares map references with the original.
// This is safe because the maps are immutable after Phase 2 completion.
func (ca *CachedAnalyzer) Analyze() GraphStats {
	stats := ca.AnalyzeAsync(context.Background())
	stats.WaitForPhase2()
	return GraphStats{
		OutDegree:         stats.OutDegree,
		InDegree:          stats.InDegree,
		TopologicalOrder:  stats.TopologicalOrder,
		Density:           stats.Density,
		NodeCount:         stats.NodeCount,
		EdgeCount:         stats.EdgeCount,
		Config:            stats.Config,
		pageRank:          stats.pageRank,
		betweenness:       stats.betweenness,
		eigenvector:       stats.eigenvector,
		hubs:              stats.hubs,
		authorities:       stats.authorities,
		criticalPathScore: stats.criticalPathScore,
		pageRankRank:      stats.pageRankRank,
		betweennessRank:   stats.betweennessRank,
		eigenvectorRank:   stats.eigenvectorRank,
		hubsRank:          stats.hubsRank,
		authoritiesRank:   stats.authoritiesRank,
		criticalPathRank:  stats.criticalPathRank,
		inDegreeRank:      stats.inDegreeRank,
		outDegreeRank:     stats.outDegreeRank,
		coreNumber:        stats.coreNumber,
		articulation:      stats.articulation,
		slack:             stats.slack,
		cycles:            stats.cycles,
		phase2Ready:       true,
		status:            stats.status,
	}
}

// DataHash returns the computed hash for the analyzer's issue data.
func (ca *CachedAnalyzer) DataHash() string {
	return ca.dataHash
}

// WasCacheHit returns true if the last AnalyzeAsync call was a cache hit.
func (ca *CachedAnalyzer) WasCacheHit() bool {
	return ca.cacheHit
}

type robotAnalysisDiskCacheFile struct {
	Version int                                    `json:"version"`
	Entries map[string]robotAnalysisDiskCacheEntry `json:"entries"`
}

type robotAnalysisDiskCacheEntry struct {
	CreatedAt       time.Time           `json:"created_at"`
	AccessedAt      time.Time           `json:"accessed_at"`
	DataHash        string              `json:"data_hash"`
	ConfigHash      string              `json:"config_hash"`
	ComputeDuration time.Duration       `json:"compute_duration"` // For XFetch probabilistic refresh
	Result          graphStatsCacheBlob `json:"result"`
}

type graphStatsCacheBlob struct {
	OutDegree        map[string]int `json:"out_degree"`
	InDegree         map[string]int `json:"in_degree"`
	TopologicalOrder []string       `json:"topological_order"`
	Density          float64        `json:"density"`
	NodeCount        int            `json:"node_count"`
	EdgeCount        int            `json:"edge_count"`
	Config           AnalysisConfig `json:"config"`

	PageRank          map[string]float64 `json:"page_rank"`
	Betweenness       map[string]float64 `json:"betweenness"`
	Eigenvector       map[string]float64 `json:"eigenvector"`
	Hubs              map[string]float64 `json:"hubs"`
	Authorities       map[string]float64 `json:"authorities"`
	CriticalPathScore map[string]float64 `json:"critical_path_score"`
	CoreNumber        map[string]int     `json:"core_number"`
	Articulation      []string           `json:"articulation"`
	Slack             map[string]float64 `json:"slack"`
	Cycles            [][]string         `json:"cycles"`
	Status            MetricStatus       `json:"status"`
}

// graphStatsCacheSoA is the on-disk (serialized) form of graphStatsCacheBlob.
//
// Instead of ~10 separate map[string]T objects that each repeat every node-ID
// string as a JSON key (~10×N repeated strings + per-map rehash on decode), it
// uses a struct-of-arrays / dictionary-encoding layout: the node-ID strings are
// stored exactly once in Nodes, and every per-node metric is a positional array
// aligned to Nodes (index i → value for Nodes[i]).
//
// Nodes is the sorted union of the key sets of all per-node maps. Per-node
// metrics are dense over this set in practice (PageRank/InDegree/... all cover
// the whole graph), so each *Vals array is full length and a metric needs no
// presence info in the common case. To remain exactly round-trippable for the
// rare partial/nil map, each metric also carries:
//   - a *Set bool: distinguishes a non-nil map (possibly empty) from a nil map,
//     so toGraphStats() restores nil-ness exactly.
//   - an optional *Idx []int32: when a non-nil map does NOT cover every node in
//     Nodes, Idx lists the Nodes indices that ARE present and *Vals is aligned
//     to Idx instead of Nodes. When Idx is nil the metric is dense (covers all
//     of Nodes) and *Vals is aligned to Nodes directly. This keeps absent vs.
//     present-zero distinct without a per-node presence string.
//
// This is purely the serialized shape: graphStatsCacheBlob and the in-memory
// GraphStats it expands to are unchanged.
type graphStatsCacheSoA struct {
	Version int `json:"v"` // SoA payload version (matches robotAnalysisDiskCacheVersion intent)

	Nodes []string `json:"nodes"`

	TopologicalOrder []string       `json:"topological_order"`
	Density          float64        `json:"density"`
	NodeCount        int            `json:"node_count"`
	EdgeCount        int            `json:"edge_count"`
	Config           AnalysisConfig `json:"config"`
	Articulation     []string       `json:"articulation"`
	Cycles           [][]string     `json:"cycles"`
	Status           MetricStatus   `json:"status"`

	// Float metrics (positional, aligned to Nodes unless *Idx present).
	PageRankSet bool      `json:"pr_set,omitempty"`
	PageRankIdx []int32   `json:"pr_idx,omitempty"`
	PageRank    []float64 `json:"pr,omitempty"`

	BetweennessSet bool      `json:"bt_set,omitempty"`
	BetweennessIdx []int32   `json:"bt_idx,omitempty"`
	Betweenness    []float64 `json:"bt,omitempty"`

	EigenvectorSet bool      `json:"ev_set,omitempty"`
	EigenvectorIdx []int32   `json:"ev_idx,omitempty"`
	Eigenvector    []float64 `json:"ev,omitempty"`

	HubsSet bool      `json:"hub_set,omitempty"`
	HubsIdx []int32   `json:"hub_idx,omitempty"`
	Hubs    []float64 `json:"hub,omitempty"`

	AuthoritiesSet bool      `json:"auth_set,omitempty"`
	AuthoritiesIdx []int32   `json:"auth_idx,omitempty"`
	Authorities    []float64 `json:"auth,omitempty"`

	CriticalPathScoreSet bool      `json:"cp_set,omitempty"`
	CriticalPathScoreIdx []int32   `json:"cp_idx,omitempty"`
	CriticalPathScore    []float64 `json:"cp,omitempty"`

	SlackSet bool      `json:"sl_set,omitempty"`
	SlackIdx []int32   `json:"sl_idx,omitempty"`
	Slack    []float64 `json:"sl,omitempty"`

	// Int metrics (positional, aligned to Nodes unless *Idx present).
	OutDegreeSet bool    `json:"od_set,omitempty"`
	OutDegreeIdx []int32 `json:"od_idx,omitempty"`
	OutDegree    []int   `json:"od,omitempty"`

	InDegreeSet bool    `json:"id_set,omitempty"`
	InDegreeIdx []int32 `json:"id_idx,omitempty"`
	InDegree    []int   `json:"id,omitempty"`

	CoreNumberSet bool    `json:"kc_set,omitempty"`
	CoreNumberIdx []int32 `json:"kc_idx,omitempty"`
	CoreNumber    []int   `json:"kc,omitempty"`
}

// MarshalJSON flattens the string-keyed maps into the compact SoA layout.
func (b graphStatsCacheBlob) MarshalJSON() ([]byte, error) {
	// Build the shared node index: sorted union of every per-node map's keys.
	nodeSet := make(map[string]struct{})
	addFloatKeys := func(m map[string]float64) {
		for k := range m {
			nodeSet[k] = struct{}{}
		}
	}
	addIntKeys := func(m map[string]int) {
		for k := range m {
			nodeSet[k] = struct{}{}
		}
	}
	addFloatKeys(b.PageRank)
	addFloatKeys(b.Betweenness)
	addFloatKeys(b.Eigenvector)
	addFloatKeys(b.Hubs)
	addFloatKeys(b.Authorities)
	addFloatKeys(b.CriticalPathScore)
	addFloatKeys(b.Slack)
	addIntKeys(b.OutDegree)
	addIntKeys(b.InDegree)
	addIntKeys(b.CoreNumber)

	nodes := make([]string, 0, len(nodeSet))
	for k := range nodeSet {
		nodes = append(nodes, k)
	}
	sort.Strings(nodes)

	soa := graphStatsCacheSoA{
		Version:          robotAnalysisDiskCacheVersion,
		Nodes:            nodes,
		TopologicalOrder: b.TopologicalOrder,
		Density:          b.Density,
		NodeCount:        b.NodeCount,
		EdgeCount:        b.EdgeCount,
		Config:           b.Config,
		Articulation:     b.Articulation,
		Cycles:           b.Cycles,
		Status:           b.Status,
	}

	soa.PageRankSet, soa.PageRankIdx, soa.PageRank = flattenFloat(b.PageRank, nodes)
	soa.BetweennessSet, soa.BetweennessIdx, soa.Betweenness = flattenFloat(b.Betweenness, nodes)
	soa.EigenvectorSet, soa.EigenvectorIdx, soa.Eigenvector = flattenFloat(b.Eigenvector, nodes)
	soa.HubsSet, soa.HubsIdx, soa.Hubs = flattenFloat(b.Hubs, nodes)
	soa.AuthoritiesSet, soa.AuthoritiesIdx, soa.Authorities = flattenFloat(b.Authorities, nodes)
	soa.CriticalPathScoreSet, soa.CriticalPathScoreIdx, soa.CriticalPathScore = flattenFloat(b.CriticalPathScore, nodes)
	soa.SlackSet, soa.SlackIdx, soa.Slack = flattenFloat(b.Slack, nodes)

	soa.OutDegreeSet, soa.OutDegreeIdx, soa.OutDegree = flattenInt(b.OutDegree, nodes)
	soa.InDegreeSet, soa.InDegreeIdx, soa.InDegree = flattenInt(b.InDegree, nodes)
	soa.CoreNumberSet, soa.CoreNumberIdx, soa.CoreNumber = flattenInt(b.CoreNumber, nodes)

	return json.Marshal(soa)
}

// UnmarshalJSON expands the compact SoA layout back into the string-keyed maps.
func (b *graphStatsCacheBlob) UnmarshalJSON(data []byte) error {
	var soa graphStatsCacheSoA
	if err := json.Unmarshal(data, &soa); err != nil {
		return err
	}

	b.TopologicalOrder = soa.TopologicalOrder
	b.Density = soa.Density
	b.NodeCount = soa.NodeCount
	b.EdgeCount = soa.EdgeCount
	b.Config = soa.Config
	b.Articulation = soa.Articulation
	b.Cycles = soa.Cycles
	b.Status = soa.Status

	b.PageRank = expandFloat(soa.PageRankSet, soa.PageRankIdx, soa.PageRank, soa.Nodes)
	b.Betweenness = expandFloat(soa.BetweennessSet, soa.BetweennessIdx, soa.Betweenness, soa.Nodes)
	b.Eigenvector = expandFloat(soa.EigenvectorSet, soa.EigenvectorIdx, soa.Eigenvector, soa.Nodes)
	b.Hubs = expandFloat(soa.HubsSet, soa.HubsIdx, soa.Hubs, soa.Nodes)
	b.Authorities = expandFloat(soa.AuthoritiesSet, soa.AuthoritiesIdx, soa.Authorities, soa.Nodes)
	b.CriticalPathScore = expandFloat(soa.CriticalPathScoreSet, soa.CriticalPathScoreIdx, soa.CriticalPathScore, soa.Nodes)
	b.Slack = expandFloat(soa.SlackSet, soa.SlackIdx, soa.Slack, soa.Nodes)

	b.OutDegree = expandInt(soa.OutDegreeSet, soa.OutDegreeIdx, soa.OutDegree, soa.Nodes)
	b.InDegree = expandInt(soa.InDegreeSet, soa.InDegreeIdx, soa.InDegree, soa.Nodes)
	b.CoreNumber = expandInt(soa.CoreNumberSet, soa.CoreNumberIdx, soa.CoreNumber, soa.Nodes)

	return nil
}

// flattenFloat columnarizes a string-keyed float map against the shared Nodes
// index. Returns (set, idx, vals): set=false ⇒ nil map; idx=nil ⇒ vals is dense
// over Nodes; idx non-nil ⇒ vals[i] is the value for Nodes[idx[i]].
func flattenFloat(m map[string]float64, nodes []string) (bool, []int32, []float64) {
	if m == nil {
		return false, nil, nil
	}
	if len(m) == len(nodes) {
		// Dense: every node in the shared index has a value (the union is built
		// from these maps' keys, so equal length ⇒ identical key set).
		vals := make([]float64, len(nodes))
		for i, n := range nodes {
			vals[i] = m[n]
		}
		return true, nil, vals
	}
	// Sparse: emit (index, value) pairs in Nodes order for determinism.
	idx := make([]int32, 0, len(m))
	vals := make([]float64, 0, len(m))
	for i, n := range nodes {
		if v, ok := m[n]; ok {
			idx = append(idx, int32(i))
			vals = append(vals, v)
		}
	}
	return true, idx, vals
}

func flattenInt(m map[string]int, nodes []string) (bool, []int32, []int) {
	if m == nil {
		return false, nil, nil
	}
	if len(m) == len(nodes) {
		vals := make([]int, len(nodes))
		for i, n := range nodes {
			vals[i] = m[n]
		}
		return true, nil, vals
	}
	idx := make([]int32, 0, len(m))
	vals := make([]int, 0, len(m))
	for i, n := range nodes {
		if v, ok := m[n]; ok {
			idx = append(idx, int32(i))
			vals = append(vals, v)
		}
	}
	return true, idx, vals
}

func expandFloat(set bool, idx []int32, vals []float64, nodes []string) map[string]float64 {
	if !set {
		return nil
	}
	if idx == nil {
		m := make(map[string]float64, len(vals))
		for i := range vals {
			if i < len(nodes) {
				m[nodes[i]] = vals[i]
			}
		}
		return m
	}
	m := make(map[string]float64, len(idx))
	for i, ni := range idx {
		// ni >= 0 guards a corrupt/hand-edited cache file with a negative sparse
		// index: nodes[-1] would panic and crash the whole bv command. A bad cache
		// must degrade to a miss, never panic.
		if ni >= 0 && int(ni) < len(nodes) && i < len(vals) {
			m[nodes[ni]] = vals[i]
		}
	}
	return m
}

func expandInt(set bool, idx []int32, vals []int, nodes []string) map[string]int {
	if !set {
		return nil
	}
	if idx == nil {
		m := make(map[string]int, len(vals))
		for i := range vals {
			if i < len(nodes) {
				m[nodes[i]] = vals[i]
			}
		}
		return m
	}
	m := make(map[string]int, len(idx))
	for i, ni := range idx {
		// ni >= 0: see expandFloat — guards against a negative index in a corrupt
		// cache file panicking instead of degrading to a miss.
		if ni >= 0 && int(ni) < len(nodes) && i < len(vals) {
			m[nodes[ni]] = vals[i]
		}
	}
	return m
}

func (b graphStatsCacheBlob) toGraphStats() *GraphStats {
	stats := &GraphStats{
		OutDegree:        b.OutDegree,
		InDegree:         b.InDegree,
		TopologicalOrder: b.TopologicalOrder,
		Density:          b.Density,
		NodeCount:        b.NodeCount,
		EdgeCount:        b.EdgeCount,
		Config:           b.Config,

		phase2Ready: true,
		phase2Done:  make(chan struct{}),

		pageRank:          b.PageRank,
		betweenness:       b.Betweenness,
		eigenvector:       b.Eigenvector,
		hubs:              b.Hubs,
		authorities:       b.Authorities,
		criticalPathScore: b.CriticalPathScore,
		coreNumber:        b.CoreNumber,
		slack:             b.Slack,
		cycles:            b.Cycles,
		status:            b.Status,
	}

	if len(b.Articulation) > 0 {
		art := make(map[string]bool, len(b.Articulation))
		for _, id := range b.Articulation {
			art[id] = true
		}
		stats.articulation = art
	}

	// Rank maps are derived for UI optimization, so recompute rather than persist.
	stats.inDegreeRank = computeIntRanks(stats.InDegree)
	stats.outDegreeRank = computeIntRanks(stats.OutDegree)
	stats.pageRankRank = computeFloatRanks(stats.pageRank)
	stats.betweennessRank = computeFloatRanks(stats.betweenness)
	stats.eigenvectorRank = computeFloatRanks(stats.eigenvector)
	stats.hubsRank = computeFloatRanks(stats.hubs)
	stats.authoritiesRank = computeFloatRanks(stats.authorities)
	stats.criticalPathRank = computeFloatRanks(stats.criticalPathScore)

	close(stats.phase2Done)
	return stats
}

func robotDiskCacheEnabled() bool {
	return os.Getenv("BV_ROBOT") == "1" && os.Getenv("BV_NO_CACHE") != "1"
}

// beadsDirModTime returns the most recent modification time of the .beads/
// directory. This is used as a staleness signal: if the directory has been
// modified more recently than a cache entry was created, the entry is stale
// because bead data may have changed (e.g., a bead was closed in br).
// Returns zero time on any error (which disables the mtime check).
func beadsDirModTime() time.Time {
	// Check BEADS_DB first, then BEADS_DIR, then cwd/.beads
	beadsDir := ""
	if dbPath := os.Getenv("BEADS_DB"); dbPath != "" {
		info, err := os.Stat(dbPath)
		if err == nil {
			if !info.IsDir() {
				return info.ModTime()
			}
			beadsDir = dbPath
		} else if looksLikeBeadsDBFile(dbPath) {
			beadsDir = filepath.Dir(dbPath)
		}
	}

	if beadsDir == "" {
		beadsDir = os.Getenv("BEADS_DIR")
		if beadsDir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return time.Time{}
			}
			beadsDir = filepath.Join(cwd, ".beads")
		}
	}

	return beadsTreeModTime(beadsDir)
}

func looksLikeBeadsDBFile(dbPath string) bool {
	switch strings.ToLower(filepath.Ext(dbPath)) {
	case ".jsonl", ".db", ".sqlite", ".sqlite3":
		return true
	default:
		return false
	}
}

func beadsTreeModTime(beadsDir string) time.Time {
	info, err := os.Stat(beadsDir)
	if err != nil {
		return time.Time{}
	}
	if !info.IsDir() {
		return time.Time{}
	}

	// Scan the directory tree for the most recent file mtime.
	// We walk subdirectories (e.g. .beads/history/) because modifications
	// inside them don't always update parent directory mtime.
	latest := info.ModTime()
	if err := filepath.WalkDir(beadsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil // continue into subdirs but don't use dir mtime
		}
		finfo, err := d.Info()
		if err != nil {
			return err
		}
		if finfo.ModTime().After(latest) {
			latest = finfo.ModTime()
		}
		return nil
	}); err != nil {
		return time.Time{}
	}
	return latest
}

func robotAnalysisDiskCachePath(create bool) (string, error) {
	base := os.Getenv("BV_CACHE_DIR")
	if base == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("getting user cache dir: %w", err)
		}
		base = filepath.Join(dir, robotAnalysisDiskCacheDirName)
	}
	if create {
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", fmt.Errorf("creating cache dir: %w", err)
		}
	}
	return filepath.Join(base, robotAnalysisDiskCacheFileName), nil
}

func readRobotDiskCacheLocked(f *os.File) robotAnalysisDiskCacheFile {
	if _, err := f.Seek(0, 0); err != nil {
		return robotAnalysisDiskCacheFile{Version: robotAnalysisDiskCacheVersion, Entries: map[string]robotAnalysisDiskCacheEntry{}}
	}

	// Stream-decode directly from the file via goccy/go-json's buffered decoder,
	// avoiding a full ReadAll + Unmarshal of the (potentially multi-MB) cache.
	// This is the hot read path: it runs on EVERY `bv --robot-*` invocation.
	var cf robotAnalysisDiskCacheFile
	if err := json.NewDecoder(f).Decode(&cf); err != nil || cf.Version != robotAnalysisDiskCacheVersion {
		return robotAnalysisDiskCacheFile{Version: robotAnalysisDiskCacheVersion, Entries: map[string]robotAnalysisDiskCacheEntry{}}
	}
	if cf.Entries == nil {
		cf.Entries = map[string]robotAnalysisDiskCacheEntry{}
	}
	return cf
}

func writeRobotDiskCacheLocked(f *os.File, cf robotAnalysisDiskCacheFile) error {
	if cf.Entries == nil {
		cf.Entries = map[string]robotAnalysisDiskCacheEntry{}
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	// Compact (no SetIndent): this cache is never read by humans, so compact
	// JSON is smaller on disk and faster to encode/decode. goccy/go-json's
	// streaming encoder writes directly to the file.
	if err := json.NewEncoder(f).Encode(cf); err != nil {
		return err
	}
	return f.Sync()
}

func pruneRobotDiskCacheEntries(now time.Time, entries map[string]robotAnalysisDiskCacheEntry) {
	for k, e := range entries {
		if e.CreatedAt.IsZero() || now.Sub(e.CreatedAt) > robotAnalysisDiskCacheMaxAge {
			delete(entries, k)
		}
	}
}

func evictRobotDiskCacheLRU(entries map[string]robotAnalysisDiskCacheEntry) {
	if len(entries) <= robotAnalysisDiskCacheMaxEntries {
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
	for len(entries) > robotAnalysisDiskCacheMaxEntries && len(items) > 0 {
		delete(entries, items[0].key)
		items = items[1:]
	}
}

// getRobotDiskCachedStats returns cached stats, whether XFetch suggests early refresh, and cache hit.
// The xfetchRefresh flag uses probabilistic early refresh to prevent cache stampedes:
// if true, the caller should consider recomputing in the background while still using the cached result.
func getRobotDiskCachedStats(fullKey string) (stats *GraphStats, xfetchRefresh bool, cacheHit bool) {
	if !robotDiskCacheEnabled() {
		return nil, false, false
	}

	path, err := robotAnalysisDiskCachePath(false)
	if err != nil {
		return nil, false, false
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, false
	}
	defer f.Close()

	if err := lockFile(f); err != nil {
		return nil, false, false
	}
	defer func() { _ = unlockFile(f) }()

	now := time.Now()
	cf := readRobotDiskCacheLocked(f)
	pruneRobotDiskCacheEntries(now, cf.Entries)

	entry, ok := cf.Entries[fullKey]
	if !ok {
		// Best-effort: persist prunes.
		_ = writeRobotDiskCacheLocked(f, cf)
		return nil, false, false
	}

	// Mtime-based staleness check: if the .beads/ directory (or any file
	// inside it) has been modified after this cache entry was created, the
	// bead data may have changed (e.g., a bead was closed in br). In that
	// case the cached GraphStats are stale and must be recomputed.
	dirMtime := beadsDirModTime()
	if !dirMtime.IsZero() && dirMtime.After(entry.CreatedAt) {
		delete(cf.Entries, fullKey)
		_ = writeRobotDiskCacheLocked(f, cf)
		return nil, false, false
	}

	// XFetch: probabilistically suggest early refresh to prevent cache stampedes.
	// Do not refresh again before at least one prior compute-duration window has
	// elapsed, otherwise newly written entries can get selected immediately.
	shouldXFetchRefresh := entry.ComputeDuration > 0 &&
		!now.Before(entry.CreatedAt.Add(entry.ComputeDuration)) &&
		xfetch.ShouldRefresh(entry.CreatedAt, entry.ComputeDuration, 1.0, now)

	// Pure read-hit: do NOT rewrite the entire cache file just to bump the LRU
	// AccessedAt timestamp. Rewriting the full (multi-MB) file with encoding/json
	// on every robot invocation dominates the cost of a cache hit, and the
	// bookkeeping it persists is not load-bearing for correctness:
	//   - Staleness uses beadsDirModTime vs entry.CreatedAt, never AccessedAt.
	//   - Pruning (maxAge) and LRU eviction (maxEntries) are re-applied, and
	//     persisted, on the write path (putRobotDiskCachedStats) that runs after
	//     every real recompute, plus on the miss/stale branches above when the
	//     entry set actually changes.
	// Skipping the write here means a frequently-read-but-never-recomputed entry
	// won't have its LRU recency persisted; eviction falls back to CreatedAt for
	// such entries, which is an acceptable LRU approximation.
	return entry.Result.toGraphStats(), shouldXFetchRefresh, true
}

func putRobotDiskCachedStats(fullKey, dataHash, configHash string, stats *GraphStats, computeDuration time.Duration) {
	if !robotDiskCacheEnabled() {
		return
	}
	if stats == nil || !stats.IsPhase2Ready() {
		return
	}

	stats.mu.RLock()
	blob := graphStatsCacheBlob{
		OutDegree:        stats.OutDegree,
		InDegree:         stats.InDegree,
		TopologicalOrder: stats.TopologicalOrder,
		Density:          stats.Density,
		NodeCount:        stats.NodeCount,
		EdgeCount:        stats.EdgeCount,
		Config:           stats.Config,

		PageRank:          stats.pageRank,
		Betweenness:       stats.betweenness,
		Eigenvector:       stats.eigenvector,
		Hubs:              stats.hubs,
		Authorities:       stats.authorities,
		CriticalPathScore: stats.criticalPathScore,
		CoreNumber:        stats.coreNumber,
		Slack:             stats.slack,
		Cycles:            stats.cycles,
		Status:            stats.status,
	}
	if stats.articulation != nil {
		blob.Articulation = make([]string, 0, len(stats.articulation))
		for id := range stats.articulation {
			blob.Articulation = append(blob.Articulation, id)
		}
		sort.Strings(blob.Articulation)
	}
	stats.mu.RUnlock()

	if b, err := json.Marshal(blob); err != nil || len(b) > robotAnalysisDiskCacheMaxEntrySize {
		return
	}

	path, err := robotAnalysisDiskCachePath(true)
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
	cf := readRobotDiskCacheLocked(f)
	pruneRobotDiskCacheEntries(now, cf.Entries)

	if cf.Entries == nil {
		cf.Entries = map[string]robotAnalysisDiskCacheEntry{}
	}

	cf.Entries[fullKey] = robotAnalysisDiskCacheEntry{
		CreatedAt:       now,
		AccessedAt:      now,
		DataHash:        dataHash,
		ConfigHash:      configHash,
		ComputeDuration: computeDuration,
		Result:          blob,
	}

	evictRobotDiskCacheLRU(cf.Entries)
	_ = writeRobotDiskCacheLocked(f, cf)
}
