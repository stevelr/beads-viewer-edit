package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func writeSprints(t *testing.T, dir, content string) {
	t.Helper()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "sprints.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write sprints: %v", err)
	}
}

func TestRobotBurndown_CurrentSprint(t *testing.T) {
	bv := buildBvBinary(t)
	env := t.TempDir()

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	end := start.AddDate(0, 0, 4)

	// Keep closures safely in the past (relative to test runtime) to avoid time-of-day flakes.
	closed1 := start.Add(1 * time.Hour).Format(time.RFC3339)
	closed2 := start.Add(2 * time.Hour).Format(time.RFC3339)
	t0 := start.Format(time.RFC3339)

	writeBeads(t, env, fmt.Sprintf(
		`{"id":"A","title":"Done 1","status":"closed","priority":1,"issue_type":"task","created_at":"%s","updated_at":"%s","closed_at":"%s"}
{"id":"B","title":"Done 2","status":"closed","priority":1,"issue_type":"task","created_at":"%s","updated_at":"%s","closed_at":"%s"}
{"id":"C","title":"Open","status":"open","priority":1,"issue_type":"task","created_at":"%s","updated_at":"%s"}`,
		t0, t0, closed1,
		t0, t0, closed2,
		t0, t0,
	))

	writeSprints(t, env, fmt.Sprintf(
		`{"id":"sprint-1","name":"Sprint 1","start_date":"%s","end_date":"%s","bead_ids":["A","B","C"]}`,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	))

	cmd := exec.Command(bv, "--robot-burndown", "current")
	cmd.Dir = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--robot-burndown failed: %v\n%s", err, out)
	}

	var payload struct {
		SprintID        string `json:"sprint_id"`
		TotalDays       int    `json:"total_days"`
		ElapsedDays     int    `json:"elapsed_days"`
		TotalIssues     int    `json:"total_issues"`
		CompletedIssues int    `json:"completed_issues"`
		RemainingIssues int    `json:"remaining_issues"`
		DailyPoints     []struct {
			Completed int `json:"completed"`
			Remaining int `json:"remaining"`
		} `json:"daily_points"`
		IdealLine []struct {
			Remaining int `json:"remaining"`
		} `json:"ideal_line"`
		AtRisk []struct {
			ID      string   `json:"id"`
			Signals []string `json:"signals"`
			Detail  string   `json:"detail"`
		} `json:"at_risk"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json decode: %v\nout=%s", err, out)
	}

	if payload.SprintID != "sprint-1" {
		t.Fatalf("sprint_id=%q; want sprint-1", payload.SprintID)
	}
	if payload.TotalIssues != 3 || payload.CompletedIssues != 2 || payload.RemainingIssues != 1 {
		t.Fatalf("issue counts mismatch: total=%d completed=%d remaining=%d", payload.TotalIssues, payload.CompletedIssues, payload.RemainingIssues)
	}
	if payload.ElapsedDays <= 0 || payload.TotalDays <= 0 {
		t.Fatalf("invalid day counts: elapsed=%d total=%d", payload.ElapsedDays, payload.TotalDays)
	}
	if len(payload.DailyPoints) != payload.ElapsedDays {
		t.Fatalf("daily_points=%d; want elapsed_days=%d", len(payload.DailyPoints), payload.ElapsedDays)
	}
	last := payload.DailyPoints[len(payload.DailyPoints)-1]
	if last.Completed != 2 || last.Remaining != 1 {
		t.Fatalf("last daily point mismatch: %+v", last)
	}
	if len(payload.IdealLine) != payload.TotalDays+1 {
		t.Fatalf("ideal_line=%d; want %d", len(payload.IdealLine), payload.TotalDays+1)
	}
	if payload.IdealLine[len(payload.IdealLine)-1].Remaining != 0 {
		t.Fatalf("ideal_line should end at 0 remaining, got %d", payload.IdealLine[len(payload.IdealLine)-1].Remaining)
	}
	// C was touched yesterday and is not blocked: nothing is at risk, and the
	// field must still be present as an empty array rather than omitted.
	if !bytes.Contains(out, []byte(`"at_risk":[]`)) {
		t.Fatalf("expected an empty at_risk array in payload:\n%s", out)
	}
	if len(payload.AtRisk) != 0 {
		t.Fatalf("at_risk=%+v; want none", payload.AtRisk)
	}
}

func TestRobotBurndown_AtRiskFlagsStalledBlockedBead(t *testing.T) {
	bv := buildBvBinary(t)
	env := t.TempDir()

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -10)
	end := start.AddDate(0, 0, 20)
	stale := start.Format(time.RFC3339)
	fresh := now.Add(-time.Hour).Format(time.RFC3339)

	writeBeads(t, env, fmt.Sprintf(
		`{"id":"STUCK","title":"Stuck","status":"blocked","priority":2,"issue_type":"task","created_at":"%s","updated_at":"%s"}
{"id":"FRESH","title":"Fresh","status":"in_progress","priority":2,"issue_type":"task","created_at":"%s","updated_at":"%s"}
{"id":"OUTSIDE","title":"Stuck but not in sprint","status":"blocked","priority":0,"issue_type":"task","created_at":"%s","updated_at":"%s"}`,
		stale, stale,
		stale, fresh,
		stale, stale,
	))
	writeSprints(t, env, fmt.Sprintf(
		`{"id":"sprint-2","name":"Sprint 2","start_date":"%s","end_date":"%s","bead_ids":["STUCK","FRESH"]}`,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	))

	cmd := exec.Command(bv, "--robot-burndown", "sprint-2")
	cmd.Dir = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--robot-burndown failed: %v\n%s", err, out)
	}

	var payload struct {
		AtRisk []struct {
			ID      string   `json:"id"`
			Signals []string `json:"signals"`
			Detail  string   `json:"detail"`
		} `json:"at_risk"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json decode: %v\nout=%s", err, out)
	}
	if len(payload.AtRisk) != 1 || payload.AtRisk[0].ID != "STUCK" {
		t.Fatalf("at_risk=%+v; want only STUCK (FRESH is active, OUTSIDE is not in the sprint)", payload.AtRisk)
	}
	signals := map[string]bool{}
	for _, s := range payload.AtRisk[0].Signals {
		signals[s] = true
	}
	if !signals["blocked_too_long"] || !signals["no_activity"] {
		t.Fatalf("STUCK signals=%v; want blocked_too_long and no_activity", payload.AtRisk[0].Signals)
	}
	if payload.AtRisk[0].Detail == "" {
		t.Fatalf("at_risk detail should explain the flags: %+v", payload.AtRisk[0])
	}
}
