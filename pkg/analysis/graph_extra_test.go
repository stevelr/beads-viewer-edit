package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
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
	// Ensure empty graph path still returns non-nil profile
	a2 := NewAnalyzer(nil)
	if _, profile := a2.AnalyzeWithProfile(cfg); profile == nil {
		t.Fatalf("expected non-nil profile for empty graph")
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
