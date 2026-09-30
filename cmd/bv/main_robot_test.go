package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/analysis"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
	toon "github.com/Dicklesworthstone/toon-go"
)

func TestRobotNextSchemaRequiredFieldsMatchActualOutcomes(t *testing.T) {
	t.Setenv("BEADS_DIR", t.TempDir())
	t.Setenv("BEADS_DB", "")
	schema := generateRobotSchemas().Commands["robot-next"]
	for _, name := range []string{"empty", "unbound", "partial", "bound"} {
		t.Run(name, func(t *testing.T) {
			issues := []model.Issue{{ID: "test-1", Title: "Inspect ready work", Status: model.StatusOpen, IssueType: model.TypeTask, Priority: 1}}
			if name == "empty" {
				issues = nil
			} else if name == "bound" {
				// Explicit synthetic origin tests handler/schema agreement only;
				// real loader/executable routing belongs to the live E2E suite.
				issues[0].Origin = &model.IssueOrigin{LocalID: "test-1", WorkingDirectory: "/fixture", TrackerDirectory: "/fixture/.beads",
					Database: "/fixture/.beads/beads.db", Tracker: "br", Executable: "/fixture/br", SupportsClaim: true}
			}
			source := RobotSourceReport{Status: "loaded", Valid: len(issues), Visible: len(issues)}
			if name == "partial" {
				source.Errors = 1
			}
			var out bytes.Buffer
			ctx := RobotContext{Issues: issues, SourceAuthority: newRobotSourceAuthority([]RobotSourceReport{source}), Encoder: json.NewEncoder(&out)}
			if err := handleRobotNext(ctx, phaseThreeRobotHandlerConfig{}); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			for _, field := range schema["required"].([]string) {
				if _, ok := fields[field]; !ok {
					t.Errorf("documented required field %q is absent in actual %s response: %s", field, name, out.String())
				}
			}
			var actionable bool
			if err := json.Unmarshal(fields["actionable"], &actionable); err != nil || actionable != (name == "bound") {
				t.Fatalf("wrong handler branch: actionable%v err%v output%s", actionable, err, out.String())
			}
		})
	}
}

// TestRobotPlanAndPriorityIncludeMetadata runs the built binary against a tiny fixture project
// to assert that robot-plan and robot-priority include data_hash, analysis_config, and status.
func TestRobotPlanAndPriorityIncludeMetadata(t *testing.T) {
	dir := t.TempDir()
	// create minimal .beads directory with beads.jsonl
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	beads := `{"id":"TEST-1","title":"A","status":"open","priority":1,"issue_type":"task"}
{"id":"TEST-2","title":"B","status":"open","priority":2,"issue_type":"task","dependencies":[{"issue_id":"TEST-2","depends_on_id":"TEST-1","type":"blocks"}]}
`
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	exe := buildTestBinary(t)

	runAndCheck := func(flag string) {
		cmd := exec.Command(exe, flag)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s failed: %v, out=%s", flag, err, string(out))
		}
		var payload map[string]any
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("%s json: %v", flag, err)
		}
		if _, ok := payload["data_hash"]; !ok {
			t.Fatalf("%s missing data_hash", flag)
		}
		if _, ok := payload["analysis_config"]; !ok {
			t.Fatalf("%s missing analysis_config", flag)
		}
		statusAny, ok := payload["status"]
		if !ok {
			t.Fatalf("%s missing status", flag)
		}

		status, ok := statusAny.(map[string]any)
		if !ok {
			t.Fatalf("%s status not an object", flag)
		}

		// Ensure the status contract is usable at process exit (no pending/empty states).
		expected := []string{"PageRank", "Betweenness", "Eigenvector", "HITS", "Critical", "Cycles", "KCore", "Articulation", "Slack"}
		for _, metric := range expected {
			entryAny, ok := status[metric]
			if !ok {
				t.Fatalf("%s status missing %s", flag, metric)
			}
			entry, ok := entryAny.(map[string]any)
			if !ok {
				t.Fatalf("%s status.%s not an object", flag, metric)
			}
			stateAny, ok := entry["state"]
			if !ok {
				t.Fatalf("%s status.%s missing state", flag, metric)
			}
			state, _ := stateAny.(string)
			if state == "" {
				t.Fatalf("%s status.%s state empty", flag, metric)
			}
			if state == "pending" {
				t.Fatalf("%s status.%s still pending at exit", flag, metric)
			}
		}
	}

	runAndCheck("--robot-plan")
	runAndCheck("--robot-priority")
}

// buildTestBinary builds the current module's bv binary for testing.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "bv-testbin")
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = "." // build current package
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build bv: %v, out=%s", err, string(out))
	}
	return exe
}

func requireTOONTestEncoder(t *testing.T) string {
	t.Helper()
	encoder, err := toon.TruPath()
	if err != nil {
		t.Skipf("toon_rust encoder unavailable through production discovery: %v; real encoding assertions not run", err)
	}
	encoder, err = filepath.Abs(encoder)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture commands use another working directory. Preserve the encoder
	// selected by production discovery, including relative configured paths.
	t.Setenv("TOON_TRU_BIN", encoder)
	t.Logf("production TOON encoder: %s", encoder)
	return encoder
}

func runTOONTestCommand(t *testing.T, cmd *exec.Cmd) ([]byte, string) {
	t.Helper()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if stderr.Len() > 0 {
		t.Logf("%v stderr:\n%s", cmd.Args, stderr.String())
	}
	if err != nil {
		t.Fatalf("%v: %v\nstdout:\n%s\nstderr:\n%s", cmd.Args, err, out, stderr.String())
	}
	return out, stderr.String()
}

func decodeTOONTestOutput(t *testing.T, encoder string, out []byte, stderr string) map[string]any {
	t.Helper()
	if len(bytes.TrimSpace(out)) == 0 || json.Valid(out) || strings.Contains(stderr, "falling back to JSON") {
		t.Fatalf("expected actual TOON, not empty output or JSON fallback\nstdout:\n%s\nstderr:\n%s", out, stderr)
	}
	cmd := exec.Command(encoder, "--decode")
	cmd.Stdin = bytes.NewReader(out)
	decoded, decodeStderr := runTOONTestCommand(t, cmd)
	var payload map[string]any
	if err := json.Unmarshal(decoded, &payload); err != nil || len(payload) == 0 {
		t.Fatalf("decoded TOON must be a nonempty JSON object: %v\nstdout:\n%s\nstderr:\n%s", err, decoded, decodeStderr)
	}
	return payload
}

// TestTOONOutputFormat verifies that --format=toon produces valid TOON output (bd-2lmf)
func TestTOONOutputFormat(t *testing.T) {
	encoder := requireTOONTestEncoder(t)

	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}

	beads := `{"id":"TEST-1","title":"Test Issue","status":"open","priority":1,"issue_type":"task"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	exe := buildTestBinary(t)

	// Test TOON output for robot-next
	cmd := exec.Command(exe, "--robot-next", "--format=toon")
	cmd.Dir = dir
	out, stderr := runTOONTestCommand(t, cmd)
	decodeTOONTestOutput(t, encoder, out, stderr)

	toonOut := string(out)

	// Should contain key: value pattern typical of TOON
	if !containsKeyValuePattern(toonOut) {
		t.Fatalf("TOON output doesn't look like TOON: %s", toonOut[:min(200, len(toonOut))])
	}
}

func TestRobotNextFailClosedWhenNoClaimableItem(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}

	beads := `{"id":"BLOCKED-1","title":"Blocked high impact","status":"blocked","priority":0,"issue_type":"bug"}
{"id":"OWNED-1","title":"Already owned","status":"open","assignee":"OtherAgent","priority":1,"issue_type":"task"}
`
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	exe := buildTestBinary(t)
	cmd := exec.Command(exe, "--robot-next", "--format=json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("robot-next failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("robot-next json: %v\n%s", err, out)
	}
	if got := payload["actionable"]; got != false {
		t.Fatalf("actionable = %v, want false; payload=%v", got, payload)
	}
	if _, ok := payload["claim_command"]; ok {
		t.Fatalf("fail-closed robot-next must not emit claim_command: %s", out)
	}
	if _, ok := payload["status"].(map[string]any); !ok {
		t.Fatalf("robot-next missing metric status: %s", out)
	}
	degraded, ok := payload["degraded"].([]any)
	if !ok || len(degraded) == 0 {
		t.Fatalf("robot-next fail-closed response missing degraded[]: %s", out)
	}
	first, ok := degraded[0].(map[string]any)
	if !ok || first["code"] != "no_actionable_recommendation" {
		t.Fatalf("unexpected degraded payload: %v", degraded)
	}
}

func TestRobotNextPreservesSafeUnboundTopPick(t *testing.T) {
	// This metadata-free input tests diagnostic readiness. Actual command
	// emission belongs to TestRobotActionRoutesLiveTrackers, which initializes
	// and inspects real trackers; ordinary Go tests need no installed br.
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")
	t.Setenv("BV_NO_GITIGNORE", "1")
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}

	beads := `{"id":"READY-1","title":"Ready work","status":"open","priority":1,"issue_type":"task"}
`
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	exe := buildTestBinary(t)
	cmd := exec.Command(exe, "--robot-next", "--format=json")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("robot-next failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("robot-next json: %v\n%s", err, out)
	}
	if payload["actionable"] != false || payload["id"] != nil || payload["claim_command"] != nil || payload["show_command"] != nil {
		t.Fatalf("unbound issue emitted a live tracker command: %s", out)
	}
	diagnostic, ok := payload["diagnostic_top_pick"].(map[string]any)
	if !ok || diagnostic["id"] != "READY-1" || diagnostic["title"] != "Ready work" {
		t.Fatalf("ready issue must remain the useful diagnostic candidate: %s", out)
	}
	authority, ok := payload["source_authority"].(map[string]any)
	if !ok || authority["claim_safe"] != true {
		t.Fatalf("unbound route must not erase complete graph authority: %s", out)
	}
	actions, ok := payload["actions"].(map[string]any)
	if !ok || actions["claim"] != nil || actions["show"] != nil {
		t.Fatalf("unbound route must explicitly withhold nested commands: %s", out)
	}
	if reason, ok := actions["unavailable_reason"].(string); !ok || reason == "" {
		t.Fatalf("unbound route must explain why commands are unavailable: %s", out)
	}
	if _, ok := payload["status"].(map[string]any); !ok {
		t.Fatalf("robot-next missing metric status: %s", out)
	}
}

func TestRobotNextClaimablePickRejectsAssignedTopPick(t *testing.T) {
	picks := []analysis.TopPick{{
		ID:    "ASSIGNED-1",
		Title: "Already owned",
		Score: 100,
	}}
	issues := []model.Issue{{
		ID:        "ASSIGNED-1",
		Title:     "Already owned",
		Status:    model.StatusOpen,
		IssueType: model.TypeTask,
		Assignee:  " cc11 ",
	}}

	_, diagnostic, reasons, ok := robotNextClaimablePick(picks, issues, nil, time.Time{})
	if ok {
		t.Fatalf("assigned top pick must not be claimable")
	}
	if diagnostic == nil || diagnostic.ID != "ASSIGNED-1" {
		t.Fatalf("diagnostic = %+v, want ASSIGNED-1", diagnostic)
	}
	if len(reasons) == 0 || !strings.Contains(strings.Join(reasons, "; "), "assigned") {
		t.Fatalf("reasons = %v, want assigned reason", reasons)
	}
}

// TestTOONRoundTrip verifies that TOON output can be decoded back to JSON (bd-2lmf)
func TestTOONRoundTrip(t *testing.T) {
	encoder := requireTOONTestEncoder(t)

	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")

	beads := `{"id":"TEST-1","title":"Round Trip Test","status":"open","priority":2,"issue_type":"task"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	exe := buildTestBinary(t)

	// Get TOON output
	cmd := exec.Command(exe, "--robot-next", "--format=toon")
	cmd.Dir = dir
	toonOut, stderr := runTOONTestCommand(t, cmd)
	payload := decodeTOONTestOutput(t, encoder, toonOut, stderr)

	// The source is complete but has no live tracker metadata. Encoding must
	// preserve the useful candidate and the explicit refusal to claim it.
	if payload["actionable"] != false || payload["id"] != nil || payload["title"] != nil || payload["claim_command"] != nil || payload["show_command"] != nil {
		t.Fatalf("metadata-free issue emitted a proven pick or tracker command: %+v", payload)
	}
	diagnostic, ok := payload["diagnostic_top_pick"].(map[string]any)
	if !ok || diagnostic["id"] != "TEST-1" || diagnostic["title"] != "Round Trip Test" {
		t.Fatalf("decoded diagnostic lost the fixture issue: %+v", payload)
	}
	authority, ok := payload["source_authority"].(map[string]any)
	if !ok || authority["claim_safe"] != true {
		t.Fatalf("metadata-free route must retain complete graph authority: %+v", payload)
	}
	actions, ok := payload["actions"].(map[string]any)
	if !ok || actions["claim"] != nil || actions["show"] != nil {
		t.Fatalf("metadata-free route emitted a nested tracker action: %+v", payload)
	}
	if reason, ok := actions["unavailable_reason"].(string); !ok || reason == "" {
		t.Fatalf("missing explanation for unavailable actions: %+v", payload)
	}
	generatedAt, ok := payload["generated_at"].(string)
	if !ok {
		t.Fatalf("decoded payload missing generated_at: %+v", payload)
	}
	if _, err := time.Parse(time.RFC3339, generatedAt); err != nil {
		t.Fatalf("invalid generated_at %q: %v", generatedAt, err)
	}
	if payload["output_format"] != "toon" {
		t.Fatalf("decoded payload lost requested output format: %+v", payload)
	}
}

// TestTOONTokenStats verifies that --stats produces token statistics on stderr (bd-2lmf)
func TestTOONTokenStats(t *testing.T) {
	encoder := requireTOONTestEncoder(t)

	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}

	beads := `{"id":"TEST-1","title":"Stats Test Issue","status":"open","priority":1,"issue_type":"task"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	exe := buildTestBinary(t)

	// Test --stats flag with TOON output
	cmd := exec.Command(exe, "--robot-next", "--format=toon", "--stats")
	cmd.Dir = dir
	out, stderr := runTOONTestCommand(t, cmd)
	decodeTOONTestOutput(t, encoder, out, stderr)
	stats := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "[stats]") {
			stats = line
		}
	}
	var jsonTokens, toonTokens int
	if n, err := fmt.Sscanf(stats, "[stats] JSON≈%d tok, TOON≈%d tok", &jsonTokens, &toonTokens); err != nil || n != 2 || jsonTokens <= 0 || toonTokens <= 0 {
		t.Fatalf("--stats must report both positive token estimates: %q (%v)", stats, err)
	}
	switch {
	case strings.Contains(stats, "% smaller)"):
		if toonTokens >= jsonTokens {
			t.Fatalf("smaller contradicts token estimates: %q", stats)
		}
	case strings.Contains(stats, "% larger;"):
		if toonTokens <= jsonTokens {
			t.Fatalf("larger contradicts token estimates: %q", stats)
		}
	case strings.Contains(stats, "same size"):
		difference := toonTokens - jsonTokens
		if difference < 0 {
			difference = -difference
		}
		if difference*100 >= jsonTokens {
			t.Fatalf("same size exceeds the displayed whole-percent precision: %q", stats)
		}
	default:
		t.Fatalf("--stats must state smaller, larger, or same size: %q", stats)
	}
	if strings.Contains(stats, "0% savings") {
		t.Fatalf("--stats reports misleading zero savings: %q", stats)
	}
}

// TestTOONSchemaOutput verifies that --robot-schema works with TOON format (bd-2lmf)
func TestTOONSchemaOutput(t *testing.T) {
	encoder := requireTOONTestEncoder(t)

	exe := buildTestBinary(t)

	// Test --robot-schema with TOON format
	cmd := exec.Command(exe, "--robot-schema", "--format=toon")
	out, stderr := runTOONTestCommand(t, cmd)
	payload := decodeTOONTestOutput(t, encoder, out, stderr)
	if schemaVersion, ok := payload["schema_version"].(string); !ok || schemaVersion == "" {
		t.Fatalf("decoded schema lacks its version: %+v", payload)
	}
	if envelope, ok := payload["envelope"].(map[string]any); !ok || envelope["type"] != "object" {
		t.Fatalf("decoded schema lacks the envelope contract: %+v", payload)
	}
	commands, ok := payload["commands"].(map[string]any)
	if !ok {
		t.Fatalf("decoded schema lacks command definitions: %+v", payload)
	}
	for _, name := range []string{"robot-next", "robot-triage", "robot-plan", "robot-insights"} {
		if schema, ok := commands[name].(map[string]any); !ok || schema["type"] != "object" {
			t.Errorf("decoded schema missing %s object contract: %+v", name, commands[name])
		}
	}
}

// containsKeyValuePattern checks if the string looks like TOON format
func containsKeyValuePattern(s string) bool {
	// TOON format typically has lines like "key: value" without the JSON braces/quotes
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Look for key: value pattern (not JSON's "key": value)
		if strings.Contains(trimmed, ": ") && !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "\"") {
			return true
		}
	}
	return false
}
