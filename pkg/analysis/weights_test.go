package analysis

import (
	"math"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

func TestDefaultWeights_MatchConstantsAndSumToOne(t *testing.T) {
	w := DefaultWeights()
	if w.PageRank != WeightPageRank || w.Betweenness != WeightBetweenness || w.BlockerRatio != WeightBlockerRatio ||
		w.Staleness != WeightStaleness || w.PriorityBoost != WeightPriorityBoost || w.TimeToImpact != WeightTimeToImpact ||
		w.Urgency != WeightUrgency || w.Risk != WeightRisk {
		t.Fatalf("DefaultWeights() = %+v does not match the package constants", w)
	}
	if math.Abs(w.Sum()-1.0) > 1e-9 {
		t.Fatalf("default weights sum to %f, want 1.0", w.Sum())
	}
	if got := (Weights{}).Normalized(); !weightsClose(got, DefaultWeights()) {
		t.Fatalf("zero weights must normalize to the defaults, got %+v", got)
	}
	doubled := Weights{PageRank: 2, Betweenness: 2}.Normalized()
	if math.Abs(doubled.PageRank-0.5) > 1e-9 || math.Abs(doubled.Betweenness-0.5) > 1e-9 || doubled.Risk != 0 {
		t.Fatalf("Normalized() = %+v, want 0.5/0.5", doubled)
	}
	roundTrip := WeightsFromMap(DefaultWeights().AsMap())
	if !weightsClose(roundTrip, DefaultWeights()) {
		t.Fatalf("AsMap/WeightsFromMap round trip changed the weights: %+v", roundTrip)
	}
}

// weightsFixture is a small open graph where structure and staleness pull in
// different directions, so changing the weights changes the scores.
func weightsFixture(now time.Time) []model.Issue {
	old := now.Add(-90 * 24 * time.Hour)
	fresh := now.Add(-time.Hour)
	return []model.Issue{
		// hub: blocks three others, fresh
		{ID: "hub", Title: "Hub", Status: model.StatusOpen, IssueType: model.TypeTask, Priority: 2, CreatedAt: fresh, UpdatedAt: fresh},
		{ID: "d1", Title: "Dependent 1", Status: model.StatusOpen, IssueType: model.TypeTask, Priority: 2, CreatedAt: fresh, UpdatedAt: fresh,
			Dependencies: []*model.Dependency{{IssueID: "d1", DependsOnID: "hub", Type: model.DepBlocks}}},
		{ID: "d2", Title: "Dependent 2", Status: model.StatusOpen, IssueType: model.TypeTask, Priority: 2, CreatedAt: fresh, UpdatedAt: fresh,
			Dependencies: []*model.Dependency{{IssueID: "d2", DependsOnID: "hub", Type: model.DepBlocks}}},
		{ID: "d3", Title: "Dependent 3", Status: model.StatusOpen, IssueType: model.TypeTask, Priority: 2, CreatedAt: fresh, UpdatedAt: fresh,
			Dependencies: []*model.Dependency{{IssueID: "d3", DependsOnID: "hub", Type: model.DepBlocks}}},
		// stale: no structure, very old
		{ID: "stale", Title: "Stale loner", Status: model.StatusOpen, IssueType: model.TypeTask, Priority: 2, CreatedAt: old, UpdatedAt: old},
	}
}

// weightsClose compares two Weights field by field within floating-point noise.
func weightsClose(a, b Weights) bool {
	const eps = 1e-9
	ma, mb := a.AsMap(), b.AsMap()
	for k, va := range ma {
		if math.Abs(va-mb[k]) > eps {
			return false
		}
	}
	return true
}

func scoreOf(scores []ImpactScore, id string) float64 {
	for _, s := range scores {
		if s.IssueID == id {
			return s.Score
		}
	}
	return math.NaN()
}

func TestScoreBreakdown_UsesProvidedWeights(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	issues := weightsFixture(now)

	defaults := NewAnalyzer(issues)
	defaults.SetNow(now)
	base := defaults.ComputeImpactScores()

	// Same analyzer with the defaults set explicitly must reproduce base exactly.
	explicit := NewAnalyzer(issues)
	explicit.SetNow(now)
	explicit.SetWeights(DefaultWeights())
	for _, id := range []string{"hub", "stale"} {
		if got, want := scoreOf(explicit.ComputeImpactScores(), id), scoreOf(base, id); got != want {
			t.Fatalf("explicit default weights changed %s: %f != %f", id, got, want)
		}
	}

	// Weights that only value staleness must rank the stale loner above the hub.
	staleOnly := NewAnalyzer(issues)
	staleOnly.SetNow(now)
	staleOnly.SetWeights(Weights{Staleness: 1})
	s := staleOnly.ComputeImpactScores()
	if scoreOf(s, "stale") <= scoreOf(s, "hub") {
		t.Fatalf("staleness-only weights: stale=%f should beat hub=%f", scoreOf(s, "stale"), scoreOf(s, "hub"))
	}
	// Weights that only value structure must rank the hub above the stale loner.
	structOnly := NewAnalyzer(issues)
	structOnly.SetNow(now)
	structOnly.SetWeights(Weights{PageRank: 1, BlockerRatio: 1})
	s2 := structOnly.ComputeImpactScores()
	if scoreOf(s2, "hub") <= scoreOf(s2, "stale") {
		t.Fatalf("structure-only weights: hub=%f should beat stale=%f", scoreOf(s2, "hub"), scoreOf(s2, "stale"))
	}
	if scoreOf(s, "hub") == scoreOf(base, "hub") && scoreOf(s2, "hub") == scoreOf(base, "hub") {
		t.Fatalf("changing weights left the hub score unchanged (%f); weights are not applied", scoreOf(base, "hub"))
	}
}

func TestFeedback_WeightsApplyOnlyAboveMinSamples(t *testing.T) {
	fb := DefaultFeedbackData()
	if fb.Applies() {
		t.Fatal("no events must not apply")
	}
	if got := fb.Weights(); !weightsClose(got, DefaultWeights()) {
		t.Fatalf("untouched feedback weights = %+v, want defaults", got)
	}
	// Push the Staleness adjustment up and PageRank down via the public API.
	breakdown := ScoreBreakdown{StalenessNorm: 1.0, PageRankNorm: 0.0}
	for i := 0; i < MinFeedbackSamples; i++ {
		fb.RecordFeedback("x", "accept", 0.5, breakdown)
	}
	if !fb.Applies() {
		t.Fatalf("%d accepts must apply (MinFeedbackSamples=%d)", MinFeedbackSamples, MinFeedbackSamples)
	}
	w := fb.Weights()
	if w.Staleness <= DefaultWeights().Staleness {
		t.Fatalf("accepting a stale item must raise the staleness weight: %f <= %f", w.Staleness, DefaultWeights().Staleness)
	}
	if math.Abs(w.Sum()-1.0) > 1e-9 {
		t.Fatalf("feedback weights must be normalized, sum=%f", w.Sum())
	}
	js := fb.ToJSON()
	if !js.Applied || js.MinSamples != MinFeedbackSamples || js.TotalEvents != MinFeedbackSamples {
		t.Fatalf("ToJSON() = %+v, want applied with %d samples", js, MinFeedbackSamples)
	}
}

func TestTriage_FeedbackWeightsChangeRecommendationOrder(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	issues := weightsFixture(now)

	rank := func(res TriageResult, id string) int {
		for i, r := range res.Recommendations {
			if r.ID == id {
				return i
			}
		}
		return -1
	}

	base := ComputeTriageWithOptionsAndTime(issues, TriageOptions{WaitForPhase2: true}, now)
	if rank(base, "hub") < 0 || rank(base, "stale") < 0 {
		t.Fatalf("fixture issues missing from recommendations: %+v", base.Recommendations)
	}
	if rank(base, "hub") > rank(base, "stale") {
		t.Fatalf("with default weights the hub (%d) should rank above the stale loner (%d)", rank(base, "hub"), rank(base, "stale"))
	}

	stale := Weights{Staleness: 1}
	tuned := ComputeTriageWithOptionsAndTime(issues, TriageOptions{WaitForPhase2: true, Weights: &stale}, now)
	if rank(tuned, "stale") > rank(tuned, "hub") {
		t.Fatalf("with staleness-only weights the stale loner (%d) should rank above the hub (%d)", rank(tuned, "stale"), rank(tuned, "hub"))
	}
}
