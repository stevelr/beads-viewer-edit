package main_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The feedback loop (bv-90) must actually change scoring: accept/ignore events
// tune the factor weights, --robot-triage reports feedback.applied and the
// effective weights, the accepted item's score moves, and --feedback-reset
// restores the baseline byte-for-byte (except generated_at).
func TestFeedbackEffect_AcceptIgnoreChangesTriageAndResetRestores(t *testing.T) {
	repoDir := t.TempDir()
	// Pin the robot clock so staleness (and generated_at) are identical across
	// runs; otherwise scores drift in the 1e-8 range between invocations.
	t.Setenv("SOURCE_DATE_EPOCH", "1756771200")

	// A hub that blocks three fresh tasks, and a stale loner: structure and
	// staleness pull in different directions so the weights matter.
	issues := strings.Join([]string{
		`{"id":"hub","title":"Hub","status":"open","issue_type":"task","priority":2,"created_at":"2026-08-30T00:00:00Z","updated_at":"2026-08-31T00:00:00Z"}`,
		`{"id":"d1","title":"Dependent 1","status":"open","issue_type":"task","priority":2,"created_at":"2026-08-30T00:00:00Z","updated_at":"2026-08-31T00:00:00Z","dependencies":[{"issue_id":"d1","depends_on_id":"hub","type":"blocks"}]}`,
		`{"id":"d2","title":"Dependent 2","status":"open","issue_type":"task","priority":2,"created_at":"2026-08-30T00:00:00Z","updated_at":"2026-08-31T00:00:00Z","dependencies":[{"issue_id":"d2","depends_on_id":"hub","type":"blocks"}]}`,
		`{"id":"d3","title":"Dependent 3","status":"open","issue_type":"task","priority":2,"created_at":"2026-08-30T00:00:00Z","updated_at":"2026-08-31T00:00:00Z","dependencies":[{"issue_id":"d3","depends_on_id":"hub","type":"blocks"}]}`,
		`{"id":"stale","title":"Stale loner","status":"open","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`,
	}, "\n") + "\n"
	writeIssuesJSONL(t, repoDir, issues)

	type rec struct {
		ID    string  `json:"id"`
		Score float64 `json:"score"`
	}
	type payload struct {
		GeneratedAt string `json:"generated_at"`
		Triage      struct {
			Recommendations []rec `json:"recommendations"`
		} `json:"triage"`
		Feedback *struct {
			Enabled          bool               `json:"enabled"`
			Applied          bool               `json:"applied"`
			MinSamples       int                `json:"min_samples"`
			TotalEvents      int                `json:"total_events"`
			EffectiveWeights map[string]float64 `json:"effective_weights"`
		} `json:"feedback"`
	}
	triage := func(label string) (payload, []byte) {
		out, err := runBVCommand(t, repoDir, "--robot-triage")
		if err != nil {
			t.Fatalf("%s: --robot-triage: %v\n%s", label, err, out)
		}
		var p payload
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatalf("%s: decode: %v\n%s", label, err, out)
		}
		t.Logf("%s: feedback=%+v recs=%+v", label, p.Feedback, p.Triage.Recommendations)
		return p, out
	}
	scoreOf := func(p payload, id string) float64 {
		for _, r := range p.Triage.Recommendations {
			if r.ID == id {
				return r.Score
			}
		}
		t.Fatalf("recommendation %s missing from %+v", id, p.Triage.Recommendations)
		return 0
	}
	stripGenerated := func(b []byte) string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "generated_at")
		out, _ := json.Marshal(m)
		return string(out)
	}

	base, baseRaw := triage("baseline")
	if base.Feedback != nil {
		t.Fatalf("baseline must carry no feedback block, got %+v", base.Feedback)
	}
	baseStale := scoreOf(base, "stale")

	// Three accepts of the stale loner and three ignores of the hub: enough
	// samples (min_samples) for the adjusted weights to apply.
	for i := 0; i < 3; i++ {
		if out, err := runBVCommand(t, repoDir, "--feedback-accept", "stale"); err != nil {
			t.Fatalf("--feedback-accept: %v\n%s", err, out)
		}
		if out, err := runBVCommand(t, repoDir, "--feedback-ignore", "hub"); err != nil {
			t.Fatalf("--feedback-ignore: %v\n%s", err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".beads", "feedback.json")); err != nil {
		t.Fatalf("feedback.json not written: %v", err)
	}

	tuned, _ := triage("after feedback")
	if tuned.Feedback == nil || !tuned.Feedback.Enabled || !tuned.Feedback.Applied {
		t.Fatalf("after 6 events feedback must be enabled and applied, got %+v", tuned.Feedback)
	}
	if tuned.Feedback.TotalEvents != 6 || tuned.Feedback.MinSamples <= 0 {
		t.Errorf("feedback counters = %+v", tuned.Feedback)
	}
	if w := tuned.Feedback.EffectiveWeights; math.Abs(w["Staleness"]-0.05) < 1e-9 && math.Abs(w["PageRank"]-0.22) < 1e-9 {
		t.Errorf("effective weights unchanged from defaults after accept/ignore: %v", w)
	}
	if tunedStale := scoreOf(tuned, "stale"); tunedStale == baseStale {
		t.Errorf("accepted item's score did not move: %f before and after feedback", baseStale)
	}

	// --feedback-show reports the same state.
	show, err := runBVCommand(t, repoDir, "--feedback-show")
	if err != nil || !strings.Contains(string(show), "effective_weights") {
		t.Fatalf("--feedback-show: err=%v out=%s", err, show)
	}

	// Reset restores the baseline payload exactly (generated_at aside).
	if out, err := runBVCommand(t, repoDir, "--feedback-reset"); err != nil {
		t.Fatalf("--feedback-reset: %v\n%s", err, out)
	}
	reset, resetRaw := triage("after reset")
	if reset.Feedback != nil && reset.Feedback.Applied {
		t.Errorf("after reset feedback must not apply, got %+v", reset.Feedback)
	}
	if stripGenerated(resetRaw) != stripGenerated(baseRaw) {
		t.Errorf("reset payload differs from baseline:\nbase=%s\nreset=%s", stripGenerated(baseRaw), stripGenerated(resetRaw))
	}
}
