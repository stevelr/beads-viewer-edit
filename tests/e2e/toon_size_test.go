package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	toon "github.com/Dicklesworthstone/toon-go"
)

// TestToonSize_DocumentedWinsStaySmaller (I6) re-measures TOON against JSON
// on this repository. README documents --robot-graph as the payload where
// TOON is smaller; the test fails if TOON grows to more than 110% of JSON for
// it, and logs the full table so the README numbers can be refreshed. The
// commands where JSON is smaller are logged only: that is the documented
// state, not a failure.
func TestToonSize_DocumentedWinsStaySmaller(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Resolve relative encoder overrides in the same directory as the bv child.
	t.Chdir(repo)
	encoder, err := toon.TruPath()
	if err != nil {
		t.Skipf("toon_rust encoder unavailable through production discovery: %v; encoded-size assertions not run", err)
	}
	t.Logf("production TOON encoder: %s", encoder)
	if _, err := os.Stat(filepath.Join(repo, ".beads", "issues.jsonl")); err != nil {
		t.Skip("repository tracker not present")
	}
	bv := buildBvBinary(t)

	run := func(cmd *exec.Cmd) ([]byte, string) {
		t.Helper()
		cmd.Dir = repo
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

	requireToon := func(out []byte, stderr string) {
		t.Helper()
		if len(bytes.TrimSpace(out)) == 0 || json.Valid(out) || strings.Contains(stderr, "falling back to JSON") {
			t.Fatalf("expected actual TOON, not empty output or JSON fallback\nstdout:\n%s\nstderr:\n%s", out, stderr)
		}
		decode := exec.Command(encoder, "--decode")
		decode.Stdin = bytes.NewReader(out)
		decoded, decodeStderr := run(decode)
		if !json.Valid(decoded) {
			t.Fatalf("TOON decoder returned invalid JSON\nstdout:\n%s\nstderr:\n%s", decoded, decodeStderr)
		}
	}

	size := func(command, format string) int {
		t.Helper()
		out, stderr := run(exec.Command(bv, command, "--format", format))
		if format == "toon" {
			requireToon(out, stderr)
		} else if !json.Valid(out) {
			t.Fatalf("%s returned invalid JSON baseline\nstdout:\n%s\nstderr:\n%s", command, out, stderr)
		}
		return len(out)
	}

	wins := map[string]bool{"--robot-graph": true}
	for _, command := range []string{"--robot-graph", "--robot-next", "--robot-alerts", "--robot-label-health", "--robot-insights", "--robot-triage", "--robot-plan"} {
		jsonBytes := size(command, "json")
		toonBytes := size(command, "toon")
		ratio := float64(toonBytes) / float64(jsonBytes)
		t.Logf("%-22s json=%7d toon=%7d ratio=%.2f", command, jsonBytes, toonBytes, ratio)
		if wins[command] && ratio > 1.10 {
			t.Errorf("%s is documented as a TOON win but TOON is %.0f%% of JSON; update README and tests/artifacts/perf/toon_vs_json.md", command, ratio*100)
		}
	}

	// --stats must state the direction honestly.
	cmd := exec.Command(bv, "--robot-triage", "--format", "toon", "--stats")
	out, stderr := run(cmd)
	requireToon(out, stderr)
	stats := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "[stats]") {
			stats = line
		}
	}
	if stats == "" || !(strings.Contains(stats, "smaller") || strings.Contains(stats, "larger") || strings.Contains(stats, "same size")) {
		t.Fatalf("--stats should say whether TOON is smaller or larger, got %q", stats)
	}
	if strings.Contains(stats, "0% savings") {
		t.Fatalf("--stats still reports the misleading 0%% savings: %q", stats)
	}
}
