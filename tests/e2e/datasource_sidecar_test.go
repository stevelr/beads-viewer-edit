package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Sidecar files that live beside issues.jsonl and used to hijack loading when
// fresher: br's base snapshots, a leftover legacy export, bv's own sprints.jsonl
// and correlation_feedback.jsonl, and bd's memories/interactions.
func writeSidecarBeadsDir(t *testing.T, repoDir string) {
	t.Helper()
	const a = `{"id":"bv-a","title":"alpha","status":"open","issue_type":"task","priority":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`
	const b = `{"id":"bv-b","title":"beta","status":"open","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`
	const stale = `{"id":"bv-zombie","title":"only in the stale snapshot","status":"open","issue_type":"task","priority":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`
	writeIssuesJSONL(t, repoDir, a+"\n"+b+"\n")
	beads := filepath.Join(repoDir, ".beads")
	issuesPath := filepath.Join(beads, "issues.jsonl")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(issuesPath, old, old); err != nil {
		t.Fatal(err)
	}
	sidecars := map[string]string{
		"beads.base.jsonl":           a + "\n" + b + "\n" + stale + "\n",
		"beads.jsonl":                a + "\n" + b + "\n" + stale + "\n",
		"sync_base.jsonl":            a + "\n" + b + "\n" + stale + "\n",
		"sprints.jsonl":              `{"id":"sprint-1","name":"Sprint 1","start_date":"2026-01-01T00:00:00Z","end_date":"2026-01-14T00:00:00Z","bead_ids":["bv-a"]}` + "\n",
		"correlation_feedback.jsonl": `{"commit_sha":"abc","bead_id":"bv-a","type":"confirm"}` + "\n",
		"memories.jsonl":             `{"_type":"memory","id":"m1"}` + "\n",
		"interactions.jsonl":         `{"_type":"interaction","id":"i1"}` + "\n",
	}
	newer := time.Now().Add(time.Hour)
	for name, content := range sidecars {
		p := filepath.Join(beads, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, newer, newer); err != nil {
			t.Fatal(err)
		}
	}
}

func runBVCapture(t *testing.T, bv, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(bv, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	t.Logf("bv %s: exit=%v stderr=%q", strings.Join(args, " "), err, errb.String())
	return out.String(), errb.String(), err
}

// Robot commands must load issues.jsonl (2 issues) regardless of fresher
// sidecars, report a data_hash equal to the sidecar-free run, and keep stderr
// clean.
func TestSidecars_RobotCommandsIgnoreFresherSidecars(t *testing.T) {
	bv := buildBvBinary(t)

	// Reference run with no sidecars at all.
	cleanDir := t.TempDir()
	writeIssuesJSONL(t, cleanDir,
		`{"id":"bv-a","title":"alpha","status":"open","issue_type":"task","priority":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`+"\n"+
			`{"id":"bv-b","title":"beta","status":"open","issue_type":"task","priority":2,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`+"\n")
	refOut, _, err := runBVCapture(t, bv, cleanDir, "--robot-insights")
	if err != nil {
		t.Fatalf("reference --robot-insights: %v", err)
	}
	var ref struct {
		DataHash string `json:"data_hash"`
	}
	if err := json.Unmarshal([]byte(refOut), &ref); err != nil || ref.DataHash == "" {
		t.Fatalf("reference decode: %v out=%s", err, refOut)
	}

	repoDir := t.TempDir()
	writeSidecarBeadsDir(t, repoDir)

	for _, args := range [][]string{{"--robot-triage"}, {"--robot-insights"}, {"--robot-plan"}} {
		out, stderr, err := runBVCapture(t, bv, repoDir, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if stderr != "" {
			t.Errorf("%v: stderr must be clean, got %q", args, stderr)
		}
		var payload struct {
			DataHash   string `json:"data_hash"`
			SourcePath string `json:"source_path"`
			SourceKind string `json:"source_kind"`
			Triage     *struct {
				Meta struct {
					IssueCount int `json:"issue_count"`
				} `json:"meta"`
			} `json:"triage"`
			LoadStats *struct {
				SourcePath string `json:"source_path"`
			} `json:"load_stats"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("%v: decode: %v\n%s", args, err, out)
		}
		if payload.DataHash != ref.DataHash {
			t.Errorf("%v: data_hash %s != sidecar-free hash %s (a sidecar was loaded)", args, payload.DataHash, ref.DataHash)
		}
		// The envelope must name the file that backed the payload.
		if filepath.Base(payload.SourcePath) != "issues.jsonl" || payload.SourceKind != "jsonl_local" {
			t.Errorf("%v: source_path=%q source_kind=%q, want .../issues.jsonl and jsonl_local", args, payload.SourcePath, payload.SourceKind)
		}
		if payload.Triage != nil && payload.Triage.Meta.IssueCount != 2 {
			t.Errorf("%v: issue_count = %d, want 2 (stale sync_base.jsonl has 3)", args, payload.Triage.Meta.IssueCount)
		}
		if strings.Contains(out, "bv-zombie") {
			t.Errorf("%v: payload contains the stale-snapshot-only issue", args)
		}
	}
}

// Human-mode commands used to print "skipping invalid issue on line 1" while
// probing sprints.jsonl. They must stay silent and still export 2 issues.
func TestSidecars_HumanCommandsDoNotLeakProbeWarnings(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := t.TempDir()
	writeSidecarBeadsDir(t, repoDir)

	mdPath := filepath.Join(repoDir, "report.md")
	out, stderr, err := runBVCapture(t, bv, repoDir, "--export-md", mdPath)
	if err != nil {
		t.Fatalf("--export-md: %v\n%s\n%s", err, out, stderr)
	}
	combined := out + stderr
	if strings.Contains(combined, "skipping invalid issue") || strings.Contains(combined, "Warning:") {
		t.Errorf("--export-md leaked a probe warning:\n%s", combined)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "bv-a") || strings.Contains(string(md), "bv-zombie") {
		t.Errorf("export should contain bv-a and not the stale-only issue; got:\n%s", string(md)[:min(len(md), 600)])
	}

	// --check-drift with no baseline exits 1 by design; only the warning leak matters here.
	out, stderr, _ = runBVCapture(t, bv, repoDir, "--check-drift")
	if strings.Contains(out+stderr, "skipping invalid issue") {
		t.Errorf("--check-drift leaked a probe warning:\n%s", out+stderr)
	}
}

func TestSidecars_ExplicitSnapshotOverrides(t *testing.T) {
	bv := buildBvBinary(t)
	for _, name := range []string{"beads.base.jsonl", "sync_base.jsonl"} {
		for _, mode := range []string{"flag", "environment"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				repoDir := t.TempDir()
				writeSidecarBeadsDir(t, repoDir)
				path := filepath.Join(repoDir, ".beads", name)
				args := []string{"--robot-insights"}
				if mode == "flag" {
					args = append(args, "--db", path)
				} else {
					t.Setenv("BEADS_DB", path)
				}
				out, stderr, err := runBVCapture(t, bv, repoDir, args...)
				if err != nil || stderr != "" {
					t.Fatalf("explicit %s override: err=%v stderr=%s", mode, err, stderr)
				}
				var payload struct {
					SourcePath string `json:"source_path"`
					Authority  struct {
						Valid int `json:"valid"`
					} `json:"source_authority"`
				}
				if err := json.Unmarshal([]byte(out), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.SourcePath != path || payload.Authority.Valid != 3 || !strings.Contains(out, "bv-zombie") {
					t.Fatalf("explicit snapshot was not loaded: %s", out)
				}
			})
		}
	}
}

func TestSidecars_MergeArtifactsWarnOnlyForHumanCommands(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := t.TempDir()
	writeSidecarBeadsDir(t, repoDir)
	for _, name := range []string{"beads.left.jsonl", "beads.right.jsonl"} {
		if err := os.WriteFile(filepath.Join(repoDir, ".beads", name), []byte(`{"id":"merge-only","title":"stale","status":"open"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mdPath := filepath.Join(repoDir, "report.md")
	_, stderr, err := runBVCapture(t, bv, repoDir, "--export-md", mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stderr, "Warning:") != 1 || !strings.Contains(stderr, "beads.left.jsonl") || !strings.Contains(stderr, "beads.right.jsonl") {
		t.Fatalf("human export must report both ignored merge artifacts once: %s", stderr)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "bv-a") || strings.Contains(string(md), "merge-only") || strings.Contains(string(md), "bv-zombie") {
		t.Fatalf("human export used stale data: %s", md)
	}
	out, stderr, err := runBVCapture(t, bv, repoDir, "--robot-plan")
	if err != nil || stderr != "" || !json.Valid([]byte(out)) || strings.Contains(out, "merge-only") || strings.Contains(out, "bv-zombie") {
		t.Fatalf("robot output must remain clean and canonical: err=%v stderr=%s stdout=%s", err, stderr, out)
	}
}
