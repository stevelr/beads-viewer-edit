package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
	"gonum.org/v1/gonum/graph/simple"
)

// Cover getter and configured analysis pathways that were previously untested.
func TestAnalyzerProfileAndGetters(t *testing.T) {
	issues := []model.Issue{
		{ID: "A", Title: "Alpha", Status: model.StatusOpen, Dependencies: []*model.Dependency{{DependsOnID: "B", Type: model.DepBlocks}}},
		{ID: "B", Title: "Beta", Status: model.StatusOpen},
	}

	custom := ConfigForSize(len(issues), 1)
	a := NewAnalyzer(issues)
	a.SetConfig(&custom)

	stats, profile := a.AnalyzeWithProfile(custom)
	if profile == nil || stats == nil {
		t.Fatalf("expected stats and profile")
	}
	if !stats.IsPhase2Ready() {
		t.Fatalf("phase2 should be ready after AnalyzeWithProfile")
	}

	_ = a.GetIssue("A")
	_ = stats.GetPageRankScore("A")
	_ = stats.GetBetweennessScore("A")
	_ = stats.GetEigenvectorScore("A")
	_ = stats.GetHubScore("A")
	_ = stats.GetAuthorityScore("A")
	_ = stats.GetCriticalPathScore("A")
}

func TestAnalyzerAnalyzeWithConfigCachesPhase2(t *testing.T) {
	issues := []model.Issue{{ID: "X", Status: model.StatusOpen}}
	a := NewAnalyzer(issues)
	cfg := FullAnalysisConfig()
	stats := a.AnalyzeWithConfig(cfg)
	stats.WaitForPhase2()
	if stats.NodeCount != 1 || stats.EdgeCount != 0 {
		t.Fatalf("unexpected counts: nodes=%d edges=%d", stats.NodeCount, stats.EdgeCount)
	}
	if stats.IsPhase2Ready() == false {
		t.Fatalf("expected phase2 ready")
	}
	// Ensure empty graph path still returns a complete, self-describing profile.
	a2 := NewAnalyzer(nil)
	emptyStats, profile := a2.AnalyzeWithProfile(cfg)
	if profile == nil {
		t.Fatalf("expected non-nil profile for empty graph")
	}
	status := emptyStats.Status()
	for name, entry := range map[string]statusEntry{
		"page_rank":    status.PageRank,
		"betweenness":  status.Betweenness,
		"eigenvector":  status.Eigenvector,
		"hits":         status.HITS,
		"critical":     status.Critical,
		"cycles":       status.Cycles,
		"k_core":       status.KCore,
		"articulation": status.Articulation,
		"slack":        status.Slack,
	} {
		if entry.State == "" || entry.State == "pending" {
			t.Fatalf("empty profiled graph left %s status incomplete: %+v", name, entry)
		}
	}
	// Tiny sleep to avoid zero durations in formatDuration paths
	time.Sleep(1 * time.Millisecond)
}

func TestAnalyzerAnalyzeAsync_ReusesStatsWhenGraphUnchanged(t *testing.T) {
	issues1 := []model.Issue{
		{
			ID:           "A",
			Title:        "Alpha",
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: "B", Type: model.DepBlocks}},
		},
		{ID: "B", Title: "Beta", Status: model.StatusOpen},
	}
	stats1 := NewAnalyzer(issues1).AnalyzeAsync(context.Background())
	if stats1 == nil {
		t.Fatalf("expected non-nil stats")
	}
	stats1.WaitForPhase2()
	if !stats1.IsPhase2Ready() {
		t.Fatal("first analysis did not complete Phase 2")
	}

	// Content-only changes (titles) shouldn't invalidate graph stats reuse.
	issues2 := []model.Issue{
		{
			ID:           "A",
			Title:        "Alpha updated",
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: "B", Type: model.DepBlocks}},
		},
		{ID: "B", Title: "Beta updated", Status: model.StatusOpen},
	}
	stats2 := NewAnalyzer(issues2).AnalyzeAsync(context.Background())
	if stats2 == nil {
		t.Fatalf("expected non-nil stats")
	}

	if stats1 != stats2 {
		t.Fatalf("expected graph stats to be reused for unchanged graph structure (got %p, want %p)", stats2, stats1)
	}

	stats2.WaitForPhase2()
	if !stats2.IsPhase2Ready() {
		t.Fatalf("expected phase2 ready after WaitForPhase2")
	}
}

func TestAnalyzerIncrementalCacheRejectsCanceledPhase2AndRetriesLive(t *testing.T) {
	t.Setenv("BV_ROBOT", "0")

	issues := []model.Issue{
		{
			ID:           "incremental-cancel-dependent",
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: "incremental-cancel-blocker", Type: model.DepBlocks}},
		},
		{ID: "incremental-cancel-blocker", Status: model.StatusOpen},
	}
	config := AnalysisConfig{
		RunToCompletion: true,
		ComputePageRank: true,
	}
	canceledAnalyzer := NewAnalyzer(issues)
	cacheKey := canceledAnalyzer.graphStructureHash() + "|" + ComputeConfigHash(&config)
	incrementalGraphStatsCacheMu.Lock()
	delete(incrementalGraphStatsCache, cacheKey)
	incrementalGraphStatsCacheMu.Unlock()
	t.Cleanup(func() {
		incrementalGraphStatsCacheMu.Lock()
		delete(incrementalGraphStatsCache, cacheKey)
		incrementalGraphStatsCacheMu.Unlock()
	})

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := canceledAnalyzer.AnalyzeAsyncWithConfig(canceledContext, config)
	canceled.WaitForPhase2()
	if canceled.IsPhase2Ready() {
		t.Fatal("canceled analysis unexpectedly published Phase 2")
	}

	live := NewAnalyzer(issues).AnalyzeAsyncWithConfig(context.Background(), config)
	if live == canceled {
		t.Fatal("live retry reused canceled incremental-cache result")
	}
	live.WaitForPhase2()
	if !live.IsPhase2Ready() {
		t.Fatal("live retry did not complete Phase 2")
	}
	if len(live.PageRank()) != len(issues) {
		t.Fatalf("live retry PageRank has %d entries, want %d", len(live.PageRank()), len(issues))
	}

	cached := NewAnalyzer(issues).AnalyzeAsyncWithConfig(context.Background(), config)
	if cached != live {
		t.Fatal("completed live retry was not published to the incremental cache")
	}
}

func TestAnalyzerAnalyzeAsyncAcceptsNilContext(t *testing.T) {
	t.Setenv("BV_ROBOT", "0")

	issues := []model.Issue{
		{
			ID:           "nil-context-dependent",
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: "nil-context-blocker", Type: model.DepBlocks}},
		},
		{ID: "nil-context-blocker", Status: model.StatusOpen},
	}
	config := AnalysisConfig{
		DisableCache:    true,
		RunToCompletion: true,
		ComputePageRank: true,
	}

	stats := NewAnalyzer(issues).AnalyzeAsyncWithConfig(nil, config)
	stats.WaitForPhase2()
	if !stats.IsPhase2Ready() {
		t.Fatal("nil context prevented Phase 2 publication")
	}
	if len(stats.PageRank()) != len(issues) {
		t.Fatalf("nil-context PageRank has %d entries, want %d", len(stats.PageRank()), len(issues))
	}
}

func TestAnalyzerAnalyzeAsync_DoesNotReuseStatsWhenGraphChanges(t *testing.T) {
	issues1 := []model.Issue{
		{
			ID:           "A",
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: "B", Type: model.DepBlocks}},
		},
		{ID: "B", Status: model.StatusOpen},
	}
	stats1 := NewAnalyzer(issues1).AnalyzeAsync(context.Background())
	if stats1 == nil {
		t.Fatalf("expected non-nil stats")
	}

	// Structural change: dependency edge A->B becomes A->C.
	issues2 := []model.Issue{
		{
			ID:           "A",
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: "C", Type: model.DepBlocks}},
		},
		{ID: "B", Status: model.StatusOpen},
		{ID: "C", Status: model.StatusOpen},
	}
	stats2 := NewAnalyzer(issues2).AnalyzeAsync(context.Background())
	if stats2 == nil {
		t.Fatalf("expected non-nil stats")
	}

	if stats1 == stats2 {
		t.Fatalf("expected graph stats to NOT be reused when graph structure changes")
	}
}

func TestAnalyzerAnalyzeAsyncWithConfig_DisableCacheForcesFreshAnalysis(t *testing.T) {
	t.Setenv("BV_ROBOT", "0")

	const dependentID = "disable-cache-dependent"
	issues := []model.Issue{
		{
			ID:           dependentID,
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: "disable-cache-blocker", Type: model.DepBlocks}},
		},
		{ID: "disable-cache-blocker", Status: model.StatusOpen},
	}

	cachedConfig := NoPhase2Config()
	cachedFirst := NewAnalyzer(issues).AnalyzeAsyncWithConfig(context.Background(), cachedConfig)
	cachedFirst.WaitForPhase2()
	cachedFirst.OutDegree[dependentID] = 99
	cachedSecond := NewAnalyzer(issues).AnalyzeAsyncWithConfig(context.Background(), cachedConfig)
	if cachedSecond != cachedFirst || cachedSecond.OutDegree[dependentID] != 99 {
		t.Fatalf("default config should reuse cached graph stats")
	}

	freshConfig := cachedConfig
	freshConfig.DisableCache = true
	freshFirst := NewAnalyzer(issues).AnalyzeAsyncWithConfig(context.Background(), freshConfig)
	freshFirst.WaitForPhase2()
	freshFirst.OutDegree[dependentID] = 99
	freshSecond := NewAnalyzer(issues).AnalyzeAsyncWithConfig(context.Background(), freshConfig)
	freshSecond.WaitForPhase2()
	if freshSecond == freshFirst {
		t.Fatalf("DisableCache analysis reused the previous stats pointer")
	}
	if got := freshSecond.OutDegree[dependentID]; got != 1 {
		t.Fatalf("DisableCache analysis reused mutated cached data: got out-degree %d, want 1", got)
	}

	outerCache := NewCache(time.Minute)
	cachedAnalyzer := NewCachedAnalyzer(issues, outerCache)
	cachedAnalyzer.SetConfig(&freshConfig)
	outerCache.SetByHash(cachedAnalyzer.dataHash+"|"+cachedAnalyzer.configHash, freshFirst)
	outerFresh := cachedAnalyzer.AnalyzeAsync(context.Background())
	outerFresh.WaitForPhase2()
	if cachedAnalyzer.WasCacheHit() || outerFresh == freshFirst {
		t.Fatalf("DisableCache analysis should bypass the CachedAnalyzer cache")
	}
	if got := outerFresh.OutDegree[dependentID]; got != 1 {
		t.Fatalf("DisableCache CachedAnalyzer reused mutated cached data: got out-degree %d, want 1", got)
	}
}

func TestAnalyzerRunToCompletionIgnoresMetricTimeoutRaces(t *testing.T) {
	t.Setenv("BV_ROBOT", "1")
	t.Setenv(EnvSourceDateEpoch, "1234567890")
	t.Setenv(EnvSkipPhase2, "")
	t.Setenv(EnvPhase2TimeoutSeconds, "")

	// A non-trivial directed ring makes every metric produce data and ensures a
	// zero-duration timer would win well before an asynchronous worker completed.
	const nodeCount = 64
	issues := make([]model.Issue, nodeCount)
	for i := range issues {
		id := fmt.Sprintf("N%02d", i)
		dependency := fmt.Sprintf("N%02d", (i+1)%nodeCount)
		issues[i] = model.Issue{
			ID:           id,
			Status:       model.StatusOpen,
			Dependencies: []*model.Dependency{{DependsOnID: dependency, Type: model.DepBlocks}},
		}
	}

	type metricSnapshot struct {
		pageRank     map[string]float64
		betweenness  map[string]float64
		hubs         map[string]float64
		authorities  map[string]float64
		cycles       [][]string
		metricStates []string
	}
	var want metricSnapshot

	for i, timeout := range []time.Duration{0, time.Nanosecond} {
		cfg := ApplyEnvOverrides(AnalysisConfig{
			DisableCache:       true,
			ComputePageRank:    true,
			PageRankTimeout:    timeout,
			ComputeBetweenness: true,
			BetweennessMode:    BetweennessExact,
			BetweennessTimeout: timeout,
			ComputeHITS:        true,
			HITSTimeout:        timeout,
			ComputeCycles:      true,
			CyclesTimeout:      timeout,
			MaxCyclesToStore:   10,
		})
		if !cfg.RunToCompletion {
			t.Fatal("valid SOURCE_DATE_EPOCH did not enable RunToCompletion")
		}

		stats, profile := NewAnalyzer(issues).AnalyzeWithProfile(cfg)
		if profile.PageRankTO || profile.BetweennessTO || profile.HITSTO || profile.CyclesTO {
			t.Fatalf("run-to-completion analysis timed out with configured deadline %v: %+v", timeout, profile)
		}
		status := stats.Status()
		got := metricSnapshot{
			pageRank:    stats.PageRank(),
			betweenness: stats.Betweenness(),
			hubs:        stats.Hubs(),
			authorities: stats.Authorities(),
			cycles:      stats.Cycles(),
			metricStates: []string{
				status.PageRank.State,
				status.Betweenness.State,
				status.HITS.State,
				status.Cycles.State,
			},
		}
		if len(got.pageRank) != len(issues) || len(got.betweenness) != len(issues) ||
			len(got.hubs) != len(issues) || len(got.authorities) != len(issues) || len(got.cycles) == 0 {
			t.Fatalf("run-to-completion metrics are incomplete for deadline %v: %+v", timeout, got)
		}
		for _, state := range got.metricStates {
			if state != "computed" {
				t.Fatalf("run-to-completion metric state=%q for deadline %v, want computed", state, timeout)
			}
		}

		if i == 0 {
			want = got
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("metric results changed with deadline %v\nwant: %#v\n got: %#v", timeout, want, got)
		}
	}
}

func TestAnalyzerRunToCompletionIsIndependentOfIssueAndDependencyOrder(t *testing.T) {
	t.Setenv("BV_ROBOT", "1")
	t.Setenv(EnvSourceDateEpoch, "1234567890")
	t.Setenv(EnvSkipPhase2, "")
	t.Setenv(EnvPhase2TimeoutSeconds, "")

	const nodeCount = 64
	issues := make([]model.Issue, nodeCount)
	for i := range issues {
		dependencies := []*model.Dependency{
			{DependsOnID: fmt.Sprintf("N%02d", (i+1)%nodeCount), Type: model.DepBlocks},
			{DependsOnID: fmt.Sprintf("N%02d", (i+7)%nodeCount), Type: model.DepBlocks},
		}
		issues[i] = model.Issue{
			ID:           fmt.Sprintf("N%02d", i),
			Status:       model.StatusOpen,
			Dependencies: dependencies,
		}
	}

	permuted := make([]model.Issue, len(issues))
	for i := range issues {
		issue := issues[len(issues)-1-i]
		issue.Dependencies = append([]*model.Dependency(nil), issue.Dependencies...)
		for left, right := 0, len(issue.Dependencies)-1; left < right; left, right = left+1, right-1 {
			issue.Dependencies[left], issue.Dependencies[right] = issue.Dependencies[right], issue.Dependencies[left]
		}
		permuted[i] = issue
	}
	if first, second := ComputeDataHash(issues), ComputeDataHash(permuted); first != second {
		t.Fatalf("order-independent inputs produced different data hashes: %s != %s", first, second)
	}

	config := ApplyEnvOverrides(AnalysisConfig{
		DisableCache:          true,
		ComputePageRank:       true,
		PageRankTimeout:       0,
		ComputeBetweenness:    true,
		BetweennessMode:       BetweennessApproximate,
		BetweennessSampleSize: 17,
		BetweennessTimeout:    0,
		ComputeEigenvector:    true,
		ComputeHITS:           true,
		HITSTimeout:           0,
		ComputeCriticalPath:   true,
		ComputeCycles:         true,
		CyclesTimeout:         0,
		MaxCyclesToStore:      10,
		ComputeKCore:          true,
		ComputeArticulation:   true,
		ComputeSlack:          true,
	})

	type resultSnapshot struct {
		pageRank     map[string]float64
		betweenness  map[string]float64
		eigenvector  map[string]float64
		hubs         map[string]float64
		authorities  map[string]float64
		criticalPath map[string]float64
		cycles       [][]string
		topological  []string
		metricStates []string
	}
	analyze := func(input []model.Issue) resultSnapshot {
		stats, profile := NewAnalyzer(input).AnalyzeWithProfile(config)
		if profile.PageRankTO || profile.BetweennessTO || profile.HITSTO || profile.CyclesTO {
			t.Fatalf("reproducible approximate analysis timed out: %+v", profile)
		}
		status := stats.Status()
		return resultSnapshot{
			pageRank:     stats.PageRank(),
			betweenness:  stats.Betweenness(),
			eigenvector:  stats.Eigenvector(),
			hubs:         stats.Hubs(),
			authorities:  stats.Authorities(),
			criticalPath: stats.CriticalPathScore(),
			cycles:       stats.Cycles(),
			topological:  append([]string(nil), stats.TopologicalOrder...),
			metricStates: []string{
				status.PageRank.State,
				status.Betweenness.State,
				status.Eigenvector.State,
				status.HITS.State,
				status.Critical.State,
				status.Cycles.State,
			},
		}
	}

	want := analyze(issues)
	for i := 0; i < 3; i++ {
		if got := analyze(permuted); !reflect.DeepEqual(got, want) {
			t.Fatalf("reproducible analysis changed for input permutation on run %d\nwant: %#v\n got: %#v", i, want, got)
		}
	}
}

func TestAnalyzerOwnsIssueSnapshotForGraphAndDataHash(t *testing.T) {
	deferUntil := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	// The fixture points at deferUntil itself, and the caller mutation below
	// writes through that pointer; keep the expected value in a separate copy.
	wantDefer := deferUntil
	issues := []model.Issue{
		{
			ID:         "A",
			Title:      "original",
			Status:     model.StatusOpen,
			DeferUntil: &deferUntil,
			Labels:     []string{"stable"},
			Dependencies: []*model.Dependency{
				{IssueID: "A", DependsOnID: "B", Type: model.DepBlocks},
			},
			Comments: []*model.Comment{{ID: "comment-1", IssueID: "A", Text: "original"}},
		},
		{
			ID:           "B",
			Title:        "blocker",
			Status:       model.StatusOpen,
			Labels:       []string{"blocker-label"},
			Dependencies: []*model.Dependency{{IssueID: "B", DependsOnID: "A", Type: model.DepRelated}},
			Comments:     []*model.Comment{{ID: "comment-2", IssueID: "B", Text: "blocker comment"}},
		},
	}
	wantHash := ComputeDataHash(issues)
	analyzer := NewAnalyzer(issues)

	returned := analyzer.GetIssue("A")
	if returned == nil {
		t.Fatal("GetIssue(A) returned nil")
	}
	returned.Title = "mutated through getter"
	returned.Labels[0] = "mutated through getter"
	returned.Dependencies[0].DependsOnID = "missing"
	returned.Comments[0].Text = "mutated through getter"
	*returned.DeferUntil = deferUntil.Add(48 * time.Hour)
	actionable := analyzer.GetActionableIssues()
	if len(actionable) != 1 || actionable[0].ID != "B" {
		t.Fatalf("unexpected initial actionable issues: %+v", actionable)
	}
	actionable[0].Title = "mutated through actionable getter"
	actionable[0].Labels[0] = "mutated through actionable getter"
	actionable[0].Dependencies[0].DependsOnID = "missing"
	actionable[0].Comments[0].Text = "mutated through actionable getter"

	issues[0].Title = "mutated"
	issues[0].Status = model.StatusClosed
	issues[0].Labels[0] = "mutated"
	issues[0].Dependencies[0].DependsOnID = "missing"
	issues[0].Comments[0].Text = "mutated"
	*issues[0].DeferUntil = deferUntil.Add(24 * time.Hour)

	if got := analyzer.DataHash(); got != wantHash {
		t.Fatalf("caller mutation changed analyzer data hash: got %s, want %s", got, wantHash)
	}
	stable := analyzer.GetIssue("A")
	if stable == nil || stable.Title != "original" || stable.Labels[0] != "stable" || stable.Dependencies[0].DependsOnID != "B" || stable.Comments[0].Text != "original" || !stable.DeferUntil.Equal(wantDefer) {
		t.Fatalf("GetIssue exposed analyzer-owned nested data: %+v", stable)
	}
	if stableB := analyzer.GetIssue("B"); stableB == nil || stableB.Title != "blocker" || stableB.Labels[0] != "blocker-label" || stableB.Dependencies[0].DependsOnID != "A" || stableB.Comments[0].Text != "blocker comment" {
		t.Fatalf("GetActionableIssues exposed analyzer-owned data: %+v", stableB)
	}
	config := NoPhase2Config()
	config.DisableCache = true
	stats := analyzer.AnalyzeAsyncWithConfig(context.Background(), config)
	stats.WaitForPhase2()
	if stats.OutDegree["A"] != 1 || stats.InDegree["B"] != 1 {
		t.Fatalf("caller mutation changed analyzer graph: out=%v in=%v", stats.OutDegree, stats.InDegree)
	}
}

func TestAnalyzerDAGMetricsAreIndependentOfIssueAndDependencyOrder(t *testing.T) {
	issues := []model.Issue{
		{
			ID: "A", Status: model.StatusOpen,
			Dependencies: []*model.Dependency{
				{DependsOnID: "C", Type: model.DepBlocks},
				{DependsOnID: "B", Type: model.DepBlocks},
			},
		},
		{ID: "B", Status: model.StatusOpen, Dependencies: []*model.Dependency{{DependsOnID: "D", Type: model.DepBlocks}}},
		{ID: "C", Status: model.StatusOpen, Dependencies: []*model.Dependency{{DependsOnID: "D", Type: model.DepBlocks}}},
		{ID: "D", Status: model.StatusOpen, Dependencies: []*model.Dependency{{DependsOnID: "E", Type: model.DepBlocks}}},
		{ID: "E", Status: model.StatusOpen},
	}
	permuted := []model.Issue{issues[4], issues[3], issues[2], issues[1], issues[0]}
	permuted[4].Dependencies = []*model.Dependency{
		{DependsOnID: "B", Type: model.DepBlocks},
		{DependsOnID: "C", Type: model.DepBlocks},
	}

	config := AnalysisConfig{
		DisableCache:        true,
		RunToCompletion:     true,
		ComputeCriticalPath: true,
		ComputeKCore:        true,
		ComputeArticulation: true,
		ComputeSlack:        true,
	}
	type dagSnapshot struct {
		topological  []string
		criticalPath map[string]float64
		core         map[string]int
		articulation []string
		slack        map[string]float64
	}
	analyze := func(input []model.Issue) dagSnapshot {
		stats, _ := NewAnalyzer(input).AnalyzeWithProfile(config)
		return dagSnapshot{
			topological:  append([]string(nil), stats.TopologicalOrder...),
			criticalPath: stats.CriticalPathScore(),
			core:         stats.CoreNumber(),
			articulation: stats.ArticulationPoints(),
			slack:        stats.Slack(),
		}
	}

	want := analyze(issues)
	if len(want.topological) != len(issues) || len(want.criticalPath) == 0 || len(want.slack) == 0 {
		t.Fatalf("DAG fixture did not exercise requested metrics: %#v", want)
	}
	if got := analyze(permuted); !reflect.DeepEqual(got, want) {
		t.Fatalf("DAG metrics changed for input permutation\nwant: %#v\n got: %#v", want, got)
	}
}

func TestRunMetricSafelyContainsPanics(t *testing.T) {
	if got, completed := runMetricSafely(func() int { return 42 }); !completed || got != 42 {
		t.Fatalf("successful metric result=(%d, %v), want (42, true)", got, completed)
	}
	if got, completed := runMetricSafely(func() int { panic("metric failure") }); completed || got != 0 {
		t.Fatalf("panicking metric result=(%d, %v), want (0, false)", got, completed)
	}
}

func TestAnalyzerNegativeCycleLimitUsesSafeDefault(t *testing.T) {
	issues := []model.Issue{
		{ID: "A", Dependencies: []*model.Dependency{{DependsOnID: "B", Type: model.DepBlocks}}},
		{ID: "B", Dependencies: []*model.Dependency{{DependsOnID: "A", Type: model.DepBlocks}}},
	}
	config := AnalysisConfig{
		DisableCache:     true,
		RunToCompletion:  true,
		ComputeCycles:    true,
		MaxCyclesToStore: -1,
	}

	stats, profile := NewAnalyzer(issues).AnalyzeWithProfile(config)
	if profile.CyclesTO {
		t.Fatal("negative cycle limit unexpectedly timed out")
	}
	if got := stats.Cycles(); len(got) != 1 {
		t.Fatalf("negative cycle limit returned %d cycles, want safe-default result", len(got))
	}
}

func TestMetricStatusClaimUnsafeReasonsFailsClosedOnUnknownStates(t *testing.T) {
	status := MetricStatus{
		PageRank:    statusEntry{},
		Betweenness: statusEntry{State: "unexpected", Reason: "corrupt cache"},
	}
	reasons := status.ClaimUnsafeReasons()
	if len(reasons) != 2 {
		t.Fatalf("unsafe reasons = %v, want both unknown metric states", reasons)
	}
	if reasons[0] != "PageRank unknown" || reasons[1] != "Betweenness unexpected: corrupt cache" {
		t.Fatalf("unsafe reasons = %v, want explicit fail-closed diagnostics", reasons)
	}

	safe := MetricStatus{
		PageRank:    statusEntry{State: "computed"},
		Betweenness: statusEntry{State: "approx"},
	}
	if reasons := safe.ClaimUnsafeReasons(); len(reasons) != 0 {
		t.Fatalf("computed metric states reported unsafe: %v", reasons)
	}
}

// originalGraphStructureHash preserves the algorithm from 30417526 for a
// differential check of the canonical bytes, independently of successor order.
func originalGraphStructureHash(a *Analyzer) string {
	if a == nil || a.g == nil {
		return "none"
	}
	nodesIt := a.g.Nodes()
	ids := make([]string, 0, nodesIt.Len())
	for nodesIt.Next() {
		if id, ok := a.nodeToID[nodesIt.Node().ID()]; ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	type edgeKey struct{ from, to string }
	edgesIt := a.g.Edges()
	edges := make([]edgeKey, 0, edgesIt.Len())
	for edgesIt.Next() {
		e := edgesIt.Edge()
		from, to := a.nodeToID[e.From().ID()], a.nodeToID[e.To().ID()]
		if from != "" && to != "" {
			edges = append(edges, edgeKey{from: from, to: to})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		return edges[i].to < edges[j].to
	})
	edgesDedup := edges[:0]
	for i := range edges {
		if i == 0 || edges[i] != edges[i-1] {
			edgesDedup = append(edgesDedup, edges[i])
		}
	}
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0})
	}
	h.Write([]byte{1})
	for _, edge := range edgesDedup {
		h.Write([]byte(edge.from))
		h.Write([]byte{0})
		h.Write([]byte(edge.to))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func TestGraphStructureHashFrozenParity(t *testing.T) {
	chain := []model.Issue{
		{ID: "A", Status: model.StatusOpen, Dependencies: []*model.Dependency{{DependsOnID: "B", Type: model.DepBlocks}}},
		{ID: "B", Status: model.StatusOpen, Dependencies: []*model.Dependency{{DependsOnID: "C", Type: model.DepBlocks}}},
		{ID: "C", Status: model.StatusOpen},
	}
	changedContent := make([]model.Issue, len(chain))
	for i := range chain {
		changedContent[i] = chain[i].Clone()
		changedContent[i].Title = "changed title"
		changedContent[i].Description = "changed description"
		changedContent[i].Labels = []string{"changed label"}
		changedContent[i].Status = []model.Status{model.StatusClosed, model.StatusTombstone, model.StatusDeferred}[i]
	}
	if ComputeDataHash(chain) == ComputeDataHash(changedContent) {
		t.Fatal("content/status fixture did not change the issue data hash")
	}
	branch := []model.Issue{chain[0].Clone(), chain[1].Clone(), chain[2].Clone()}
	branch[0].Dependencies = append(branch[0].Dependencies, &model.Dependency{DependsOnID: "C", Type: model.DepBlocks})
	permuted := []model.Issue{branch[2].Clone(), branch[1].Clone(), branch[0].Clone(), branch[0].Clone()}
	for i := range permuted {
		deps := permuted[i].Dependencies
		for left, right := 0, len(deps)-1; left < right; left, right = left+1, right-1 {
			deps[left], deps[right] = deps[right], deps[left]
		}
		if len(deps) != 0 {
			permuted[i].Dependencies = append(deps, deps[0])
		}
	}
	mixed := []model.Issue{chain[0].Clone(), chain[1].Clone(), chain[2].Clone()}
	mixed[0].Dependencies = append(mixed[0].Dependencies, nil,
		&model.Dependency{DependsOnID: "missing", Type: model.DepBlocks},
		&model.Dependency{DependsOnID: "C", Type: model.DepParentChild},
		&model.Dependency{DependsOnID: "C", Type: model.DepRelated},
		&model.Dependency{DependsOnID: "C", Type: model.DepDiscoveredFrom})
	mixed[1].Dependencies[0].Type = ""                       // Legacy untyped blocking edge.
	mixed[1].Dependencies[0].IssueID = "absent-record-owner" // The containing issue owns the edge.
	lastRecordWins := []model.Issue{{ID: "A", Dependencies: []*model.Dependency{{DependsOnID: "C", Type: model.DepBlocks}}}, chain[2], chain[1], chain[0]}

	// Numeric node IDs, insertion order, and mapped IDs deliberately disagree.
	// Two physical nodes share A; their successor union must deduplicate A->z.
	// The empty and unmapped nodes participate in edges that the hash excludes.
	generic := simple.NewDirectedGraph()
	for _, id := range []int64{60, 40, 10, 50, 30, 20} {
		generic.AddNode(simple.Node(id))
	}
	for _, edge := range [][2]int64{{30, 40}, {10, 20}, {30, 20}, {20, 10}, {40, 30}, {40, 20}, {50, 20}, {20, 50}, {60, 20}, {10, 60}} {
		generic.SetEdge(simple.Edge{F: simple.Node(edge[0]), T: simple.Node(edge[1])})
	}
	genericAnalyzer := &Analyzer{g: generic, nodeToID: map[int64]string{10: "A", 30: "A", 20: "z", 40: "中", 50: ""}}

	compact := newCompactDirectedGraph(4)
	for _, edge := range [][2]int64{{1, 0}, {1, 2}, {1, 2}, {2, 0}, {0, 1}, {3, 1}, {1, 3}} {
		compact.addEdge(edge[0], edge[1])
	}
	compactAnalyzer := &Analyzer{g: compact, nodeToID: map[int64]string{0: "C", 1: "A", 2: "B", 3: ""}}
	cloneAdjacency := func(rows [][]int64) [][]int64 {
		clone := make([][]int64, len(rows))
		for i := range rows {
			clone[i] = append([]int64(nil), rows[i]...)
		}
		return clone
	}
	wantOut, wantIn := cloneAdjacency(compact.out), cloneAdjacency(compact.in)

	// Digests are frozen against the original 30417526 algorithm and explicit
	// node-NUL/edge-NUL byte streams; never regenerate them from the optimized path.
	cases := []struct {
		name     string
		analyzer *Analyzer
		want     string
	}{
		{"nil analyzer", nil, "none"},
		{"nil graph", &Analyzer{}, "none"},
		{"nil issues", NewAnalyzer(nil), "4bf5122f344554c5"},
		{"empty issues", NewAnalyzer([]model.Issue{}), "4bf5122f344554c5"},
		{"empty ID", NewAnalyzer([]model.Issue{{ID: ""}}), "b413f47d13ee2fe6"},
		{"isolated nodes", NewAnalyzer([]model.Issue{{ID: "C"}, {ID: "A"}, {ID: "B"}}), "64c95a2e88566478"},
		{"chain", NewAnalyzer(chain), "7787aa859aa8a2d7"},
		{"changed content and statuses", NewAnalyzer(changedContent), "7787aa859aa8a2d7"},
		{"branch", NewAnalyzer(branch), "647a8d23cecda430"},
		{"duplicate and reordered rows and dependencies", NewAnalyzer(permuted), "647a8d23cecda430"},
		{"missing and nonblocking endpoints", NewAnalyzer(mixed), "7787aa859aa8a2d7"},
		{"duplicate ID last record wins", NewAnalyzer(lastRecordWins), "7787aa859aa8a2d7"},
		{"changed edge", NewAnalyzer([]model.Issue{{ID: "A", Dependencies: []*model.Dependency{{DependsOnID: "C", Type: model.DepBlocks}}}, chain[1], chain[2]}), "acf03b4a885264ea"},
		{"self edge", NewAnalyzer([]model.Issue{{ID: "A", Dependencies: []*model.Dependency{{DependsOnID: "A", Type: model.DepBlocks}}}}), "e0de5902812b881c"},
		{"cycle and self edge", NewAnalyzer([]model.Issue{
			{ID: "A", Dependencies: []*model.Dependency{{DependsOnID: "B", Type: model.DepBlocks}, {DependsOnID: "A", Type: model.DepBlocks}}},
			{ID: "B", Dependencies: []*model.Dependency{{DependsOnID: "A", Type: model.DepBlocks}}},
		}), "4b692b7d6bc5b32b"},
		{"Unicode NUL and empty IDs", NewAnalyzer([]model.Issue{
			{ID: "漢🙂", Dependencies: []*model.Dependency{{DependsOnID: "é", Type: model.DepBlocks}}},
			{ID: "", Dependencies: []*model.Dependency{{DependsOnID: "A\x00Z", Type: model.DepBlocks}}},
			{ID: "é", Dependencies: []*model.Dependency{{DependsOnID: "A\x00Z", Type: model.DepBlocks}}},
			{ID: "A\x00Z", Dependencies: []*model.Dependency{{DependsOnID: "漢🙂", Type: model.DepBlocks}, {DependsOnID: "", Type: model.DepBlocks}}},
		}), "074814ceda30226f"},
		{"generic unordered graph with aliases and missing mappings", genericAnalyzer, "b801f0a546e968f3"},
		{"compact unsorted duplicate adjacency", compactAnalyzer, "bab6fd33e625ca91"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if reference := originalGraphStructureHash(tc.analyzer); reference != tc.want {
				t.Fatalf("original hash = %q, want frozen %q", reference, tc.want)
			}
			for repeat := 0; repeat < 3; repeat++ {
				got := tc.analyzer.graphStructureHash()
				if got != tc.want {
					t.Fatalf("hash on repeat %d = %q, want %q", repeat, got, tc.want)
				}
			}
			t.Logf("frozen graph hash=%s", tc.want)
		})
	}
	if !reflect.DeepEqual(compact.out, wantOut) || !reflect.DeepEqual(compact.in, wantIn) {
		t.Fatal("hashing mutated graph adjacency order or duplicate edges")
	}
}

func TestGraphStructureHashAllocationsDoNotScaleWithEdges(t *testing.T) {
	const nodeCount = 256
	measure := func(successors int) float64 {
		issues := make([]model.Issue, nodeCount)
		for i := range issues {
			issues[i].ID = fmt.Sprintf("node-%03d", i)
			for offset := 1; offset <= successors; offset++ {
				issues[i].Dependencies = append(issues[i].Dependencies, &model.Dependency{
					DependsOnID: fmt.Sprintf("node-%03d", (i+offset)%nodeCount), Type: model.DepBlocks,
				})
			}
		}
		analyzer := NewAnalyzer(issues)
		if edges := analyzer.g.Edges().Len(); edges != nodeCount*successors {
			t.Fatalf("fixture has %d edges, want %d", edges, nodeCount*successors)
		}
		want := originalGraphStructureHash(analyzer)
		var got string
		allocations := testing.AllocsPerRun(10, func() { got = analyzer.graphStructureHash() })
		if got != want {
			t.Fatalf("measured hash = %q, want original %q", got, want)
		}
		t.Logf("nodes=%d edges=%d hash=%s allocations=%g", nodeCount, nodeCount*successors, got, allocations)
		return allocations
	}
	sparse, dense := measure(1), measure(64)
	// Node count and nonempty successor lists are identical. Allow bounded
	// growth of the one reusable successor slice, not one allocation per edge.
	if dense > sparse+16 {
		t.Fatalf("hash allocations grew with edges: sparse=%g dense=%g; want at most 16 additional allocations", sparse, dense)
	}
}
