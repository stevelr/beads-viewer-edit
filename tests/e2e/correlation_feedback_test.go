package main_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

type feedbackHistoryPayload struct {
	Stats struct {
		FeedbackApplied *struct {
			Confirmed int `json:"confirmed"`
			Rejected  int `json:"rejected"`
			Ignored   int `json:"ignored"`
		} `json:"feedback_applied"`
	} `json:"stats"`
	Histories map[string]struct {
		Commits []struct {
			SHA        string  `json:"sha"`
			Message    string  `json:"message"`
			Confidence float64 `json:"confidence"`
			Confirmed  bool    `json:"confirmed"`
		} `json:"commits"`
	} `json:"histories"`
	CommitIndex map[string][]string `json:"commit_index"`
}

func runFeedbackHistory(t *testing.T, bv, repoDir string, args ...string) feedbackHistoryPayload {
	t.Helper()
	cmd := exec.Command(bv, append([]string{"--robot-history"}, args...)...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--robot-history %v failed: %v\n%s", args, err, out)
	}
	var payload feedbackHistoryPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json decode: %v\nout=%s", err, out)
	}
	return payload
}

func commitSHAs(p feedbackHistoryPayload, bead string) []string {
	var shas []string
	for _, c := range p.Histories[bead].Commits {
		shas = append(shas, c.SHA[:7]+" "+c.Message)
	}
	return shas
}

// TestCorrelationFeedback_ShapesHistoryAndExplain is the C5 loop: reject a
// correlated commit and it leaves the history, index and stats; confirm another
// and it is pinned to 1.0; explain still works on the rejected pair and says
// why it is gone; the stats command counts both decisions.
func TestCorrelationFeedback_ShapesHistoryAndExplain(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := createCorrelationRepo(t)
	const bead = "CORR-1"

	before := runFeedbackHistory(t, bv, repoDir, "--bead-history", bead)
	t.Logf("%s commits before feedback: %v", bead, commitSHAs(before, bead))
	var rejectSHA, confirmSHA string
	var rejectConf, confirmConf float64
	for _, c := range before.Histories[bead].Commits {
		switch {
		case strings.Contains(c.Message, "add token generation"):
			rejectSHA, rejectConf = c.SHA, c.Confidence
		case strings.Contains(c.Message, "closes CORR-1"):
			confirmSHA, confirmConf = c.SHA, c.Confidence
		}
	}
	if rejectSHA == "" || confirmSHA == "" {
		t.Fatalf("fixture did not correlate the expected commits to %s: %v", bead, commitSHAs(before, bead))
	}
	if fa := before.Stats.FeedbackApplied; fa != nil && (fa.Confirmed != 0 || fa.Rejected != 0 || fa.Ignored != 0) {
		t.Fatalf("no feedback stored yet, feedback_applied should count nothing: %+v", fa)
	}
	if confirmConf >= 1.0 {
		t.Fatalf("precondition: %s should start below 1.0 confidence, got %v", confirmSHA[:7], confirmConf)
	}
	t.Logf("reject %s (conf %.2f), confirm %s (conf %.2f)", rejectSHA[:7], rejectConf, confirmSHA[:7], confirmConf)

	// 1. Reject.
	reject := exec.Command(bv, "--robot-reject-correlation", rejectSHA+":"+bead, "--correlation-reason", "touched the file by accident")
	reject.Dir = repoDir
	if out, err := reject.CombinedOutput(); err != nil {
		t.Fatalf("--robot-reject-correlation failed: %v\n%s", err, out)
	} else if !strings.Contains(string(out), `"status":"rejected"`) {
		t.Fatalf("reject output should report status rejected:\n%s", out)
	}

	afterReject := runFeedbackHistory(t, bv, repoDir, "--bead-history", bead)
	t.Logf("%s commits after reject: %v", bead, commitSHAs(afterReject, bead))
	for _, c := range afterReject.Histories[bead].Commits {
		if c.SHA == rejectSHA {
			t.Fatalf("rejected commit %s still listed for %s", rejectSHA[:7], bead)
		}
	}
	if fa := afterReject.Stats.FeedbackApplied; fa == nil || fa.Rejected != 1 || fa.Confirmed != 0 {
		t.Fatalf("stats.feedback_applied after reject = %+v; want rejected=1", fa)
	}
	if len(afterReject.Histories[bead].Commits) != len(before.Histories[bead].Commits)-1 {
		t.Fatalf("expected exactly one commit removed: before=%d after=%d", len(before.Histories[bead].Commits), len(afterReject.Histories[bead].Commits))
	}
	full := runFeedbackHistory(t, bv, repoDir)
	for _, b := range full.CommitIndex[rejectSHA] {
		if b == bead {
			t.Fatalf("commit_index[%s] still lists %s after rejection: %v", rejectSHA[:7], bead, full.CommitIndex[rejectSHA])
		}
	}

	// 2. Confirm.
	confirm := exec.Command(bv, "--robot-confirm-correlation", confirmSHA+":"+bead, "--correlation-by", "reviewer")
	confirm.Dir = repoDir
	if out, err := confirm.CombinedOutput(); err != nil {
		t.Fatalf("--robot-confirm-correlation failed: %v\n%s", err, out)
	}
	afterConfirm := runFeedbackHistory(t, bv, repoDir, "--bead-history", bead)
	t.Logf("%s commits after confirm: %v", bead, commitSHAs(afterConfirm, bead))
	var sawConfirmed bool
	for _, c := range afterConfirm.Histories[bead].Commits {
		if c.SHA == confirmSHA {
			sawConfirmed = true
			if c.Confidence != 1.0 || !c.Confirmed {
				t.Fatalf("confirmed commit %s: confidence=%v confirmed=%v; want 1.0/true", confirmSHA[:7], c.Confidence, c.Confirmed)
			}
		}
	}
	if !sawConfirmed {
		t.Fatalf("confirmed commit %s missing from %s history", confirmSHA[:7], bead)
	}
	if fa := afterConfirm.Stats.FeedbackApplied; fa == nil || fa.Rejected != 1 || fa.Confirmed != 1 {
		t.Fatalf("stats.feedback_applied after confirm = %+v; want rejected=1 confirmed=1", fa)
	}

	// 3. Explain the rejected pair: still resolvable, and it says why.
	explain := exec.Command(bv, "--robot-explain-correlation", rejectSHA+":"+bead)
	explain.Dir = repoDir
	explainOut, err := explain.CombinedOutput()
	if err != nil {
		t.Fatalf("--robot-explain-correlation on a rejected pair failed: %v\n%s", err, explainOut)
	}
	var explanation struct {
		CommitSHA      string  `json:"commit_sha"`
		Confidence     float64 `json:"confidence"`
		Recommendation string  `json:"recommendation"`
		Feedback       *struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"feedback"`
	}
	if err := json.Unmarshal(explainOut, &explanation); err != nil {
		t.Fatalf("explain json decode: %v\nout=%s", err, explainOut)
	}
	if explanation.CommitSHA != rejectSHA {
		t.Fatalf("explain commit_sha=%q; want %q", explanation.CommitSHA, rejectSHA)
	}
	if !strings.Contains(explanation.Recommendation, "rejected by feedback") || !strings.Contains(explanation.Recommendation, "touched the file by accident") {
		t.Fatalf("explain recommendation=%q; want 'rejected by feedback' with the reason", explanation.Recommendation)
	}
	if explanation.Feedback == nil || explanation.Feedback.Type != "reject" {
		t.Fatalf("explain feedback=%+v; want the stored reject decision", explanation.Feedback)
	}
	if explanation.Confidence != rejectConf {
		t.Fatalf("explain should show the raw strategy confidence %.2f, got %.2f", rejectConf, explanation.Confidence)
	}

	// 4. Stats.
	stats := exec.Command(bv, "--robot-correlation-stats")
	stats.Dir = repoDir
	statsOut, err := stats.CombinedOutput()
	if err != nil {
		t.Fatalf("--robot-correlation-stats failed: %v\n%s", err, statsOut)
	}
	var statsPayload struct {
		TotalFeedback int `json:"total_feedback"`
		Confirmed     int `json:"confirmed"`
		Rejected      int `json:"rejected"`
	}
	if err := json.Unmarshal(statsOut, &statsPayload); err != nil {
		t.Fatalf("stats json decode: %v\nout=%s", err, statsOut)
	}
	if statsPayload.TotalFeedback != 2 || statsPayload.Confirmed != 1 || statsPayload.Rejected != 1 {
		t.Fatalf("correlation stats=%+v; want total=2 confirmed=1 rejected=1", statsPayload)
	}
}
