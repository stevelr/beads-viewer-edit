package main_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// #190: an issue failing Issue.Validate() (updated_at < created_at) must not be
// silently dropped from robot output. Robot stderr stays clean (the contract
// pinned by TestRobotTriage_MalformedIssuesLine_NoStderr), but the envelope
// carries a load_stats block so agents can distinguish "issue absent from
// data" from "issue dropped by the loader". This replicates the exact 1-line
// repro from the issue: previously total=0 with empty stderr and no trace.
func TestRobotTriage_TimestampInvertedIssue_SurfacedInLoadStats(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := t.TempDir()

	issues := `{"id":"bd-test1","title":"timestamp-inverted issue","status":"open","issue_type":"task","priority":2,"created_at":"2026-07-06T17:42:31Z","updated_at":"2026-07-06T10:43:40Z"}` + "\n"
	writeIssuesJSONL(t, repoDir, issues)

	cmd := exec.Command(bv, "--robot-triage")
	cmd.Dir = repoDir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("--robot-triage failed: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected empty stderr (robot stderr-clean contract); got:\n%s", got)
	}

	var payload struct {
		LoadStats *struct {
			SourcePath string   `json:"source_path"`
			Valid      int      `json:"valid"`
			Errors     int      `json:"errors"`
			Skipped    int      `json:"skipped"`
			Warnings   []string `json:"warnings"`
		} `json:"load_stats"`
		Triage any `json:"triage"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json decode: %v\nout=%s", err, stdout.String())
	}
	if payload.LoadStats == nil {
		t.Fatalf("expected load_stats block when an issue was dropped during load; out=%s", stdout.String())
	}
	if payload.LoadStats.Errors != 1 {
		t.Errorf("load_stats.errors = %d, want 1", payload.LoadStats.Errors)
	}
	if payload.LoadStats.Valid != 0 {
		t.Errorf("load_stats.valid = %d, want 0", payload.LoadStats.Valid)
	}
	if len(payload.LoadStats.Warnings) != 1 {
		t.Fatalf("load_stats.warnings = %v, want exactly one skip reason", payload.LoadStats.Warnings)
	}
	if w := payload.LoadStats.Warnings[0]; !strings.Contains(w, "created_at") {
		t.Errorf("warning %q should carry the validation reason (updated_at before created_at)", w)
	}
	if payload.LoadStats.SourcePath == "" {
		t.Errorf("load_stats.source_path should name the JSONL file")
	}
}

// A healthy dataset must NOT grow a load_stats block: the field appears only
// when records were dropped, so existing robot consumers see byte-identical
// output on clean loads.
func TestRobotTriage_CleanLoad_NoLoadStats(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := t.TempDir()

	issues := `{"id":"bd-ok","title":"healthy issue","status":"open","issue_type":"task","priority":2,"created_at":"2026-07-06T10:00:00Z","updated_at":"2026-07-06T11:00:00Z"}` + "\n"
	writeIssuesJSONL(t, repoDir, issues)

	cmd := exec.Command(bv, "--robot-triage")
	cmd.Dir = repoDir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("--robot-triage failed: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var payload struct {
		LoadStats *json.RawMessage `json:"load_stats"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json decode: %v\nout=%s", err, stdout.String())
	}
	if payload.LoadStats != nil {
		t.Fatalf("expected no load_stats on a clean load, got %s", string(*payload.LoadStats))
	}
}
