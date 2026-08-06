//go:build !windows

package main_test

// E2E coverage for issue #166: `bv --robot-triage` must stay live (bounded,
// parseable output) even when the git-history correlation prologue stalls.
// A PATH-shimmed `git` makes every history-extraction command (`git log`,
// `git cat-file`) hang while passing the cheap plumbing commands through to
// the real git, reproducing the "robot triage hangs on a slow/huge repo"
// pathology. The prologue must time out, triage must emit valid JSON with
// meta.history_status == "timeout", and the killed git children must not
// leak.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// createLivenessRepo seeds a git repo with a committed beads file so the
// robot-triage history prologue actually runs (correlation.ValidateRepository
// requires .git plus a beads file, and the prologue only runs when open
// issues exist).
func createLivenessRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	beads := `{"id":"LIVE-1","title":"Open issue","status":"open","priority":1,"issue_type":"task"}
{"id":"LIVE-2","title":"Done issue","status":"closed","priority":2,"issue_type":"task"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads.jsonl: %v", err)
	}

	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("add", ".beads/beads.jsonl")
	git("commit", "-m", "seed LIVE-1, LIVE-2")
	return repoDir
}

// installSlowGitShim writes a `git` shim that hangs (sleeps far longer than
// any sane budget) on the history-extraction subcommands the correlation
// prologue uses, and passes every other subcommand through to the real git so
// issue loading stays fast. Each intercepted invocation appends its PID to
// pidFile before exec-ing sleep, so the test can verify the prologue's
// context cancellation actually killed the child instead of leaking it.
func installSlowGitShim(t *testing.T) (shimDir, pidFile string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	shimDir = t.TempDir()
	pidFile = filepath.Join(shimDir, "pids")
	script := fmt.Sprintf(`#!/bin/sh
# Slow-git shim (issue #166 e2e): history-extraction commands hang; everything
# else passes through to the real git. exec keeps the PID stable so the kill
# from exec.CommandContext lands on the sleeping process itself.
case "$1" in
  log|cat-file)
    echo $$ >> %q
    exec sleep 300
    ;;
esac
exec %q "$@"
`, pidFile, realGit)
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	return shimDir, pidFile
}

// runRobotTriageBounded runs bv --robot-triage with the given extra env under
// a hard 60s harness deadline (the shim sleeps 300s, so finishing at all
// proves the internal bound). It returns stdout and the elapsed wall time.
func runRobotTriageBounded(t *testing.T, repoDir string, extraEnv []string, args ...string) ([]byte, time.Duration) {
	t.Helper()
	bv := buildBvBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bv, append([]string{"--robot-triage"}, args...)...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), extraEnv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	start := time.Now()
	out, err := cmd.Output()
	elapsed := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("bv --robot-triage did not finish within 60s with a stalled git (liveness bound broken, #166)\nstderr: %s", stderr.String())
	}
	if err != nil {
		t.Fatalf("robot-triage failed: %v\nstderr: %s\nstdout: %s", err, stderr.String(), out)
	}
	return out, elapsed
}

// triageHistoryStatus extracts triage.meta.history_status from robot output,
// failing the test if the output is not parseable JSON of the expected shape.
func triageHistoryStatus(t *testing.T, out []byte) string {
	t.Helper()
	var parsed struct {
		Triage struct {
			Meta struct {
				HistoryStatus string `json:"history_status"`
			} `json:"meta"`
			QuickRef struct {
				OpenCount *int `json:"open_count"`
			} `json:"quick_ref"`
		} `json:"triage"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("robot-triage output is not valid JSON: %v\n%s", err, out)
	}
	if parsed.Triage.QuickRef.OpenCount == nil {
		t.Fatalf("robot-triage output missing quick_ref.open_count (degraded output must stay complete):\n%s", out)
	}
	return parsed.Triage.Meta.HistoryStatus
}

// assertNoOrphanedShimProcs verifies every PID the shim recorded is gone:
// the timed-out prologue must kill its in-flight git child (via
// exec.CommandContext) rather than leaving a 300s sleeper behind.
func assertNoOrphanedShimProcs(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			// The budget can, on a heavily loaded machine, fire before the
			// prologue goroutine reaches its first shimmed git call. In that
			// interleaving exec.CommandContext refuses to start the child at
			// all, so there is nothing to leak — but there is also nothing to
			// assert, so just record it.
			t.Log("git shim was never reached before the timeout fired; no child to check")
			return
		}
		t.Fatalf("read shim pid file: %v", err)
	}
	pids := strings.Fields(string(data))
	deadline := time.Now().Add(5 * time.Second)
	for _, pidStr := range pids {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("shimmed git child (pid %d) still alive after bv exited: the timed-out prologue leaked work", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestRobotTriage_HistoryTimeoutFlagBoundsStalledGit covers the
// --robot-history-timeout-ms flag: with git history extraction stalled, the
// command must complete within the harness deadline, emit parseable JSON with
// meta.history_status == "timeout", and leave no orphaned git children.
func TestRobotTriage_HistoryTimeoutFlagBoundsStalledGit(t *testing.T) {
	repoDir := createLivenessRepo(t)
	shimDir, pidFile := installSlowGitShim(t)

	out, elapsed := runRobotTriageBounded(t, repoDir,
		[]string{"PATH=" + shimDir + string(os.PathListSeparator) + os.Getenv("PATH")},
		"--robot-history-timeout-ms", "750",
	)

	if elapsed > 30*time.Second {
		t.Errorf("robot-triage took %v with a 750ms history budget; expected well-bounded completion", elapsed)
	}
	if status := triageHistoryStatus(t, out); status != "timeout" {
		t.Errorf("meta.history_status = %q, want %q", status, "timeout")
	}
	assertNoOrphanedShimProcs(t, pidFile)
}

// TestRobotTriage_HistoryTimeoutEnvBoundsStalledGit covers the
// BV_ROBOT_HISTORY_TIMEOUT_MS environment override (no flag passed).
func TestRobotTriage_HistoryTimeoutEnvBoundsStalledGit(t *testing.T) {
	repoDir := createLivenessRepo(t)
	shimDir, pidFile := installSlowGitShim(t)

	out, elapsed := runRobotTriageBounded(t, repoDir, []string{
		"PATH=" + shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BV_ROBOT_HISTORY_TIMEOUT_MS=750",
	})

	if elapsed > 30*time.Second {
		t.Errorf("robot-triage took %v with a 750ms env history budget; expected well-bounded completion", elapsed)
	}
	if status := triageHistoryStatus(t, out); status != "timeout" {
		t.Errorf("meta.history_status = %q, want %q", status, "timeout")
	}
	assertNoOrphanedShimProcs(t, pidFile)
}

// TestRobotTriage_HistoryStatusOKWithHealthyGit pins the healthy-path
// contract: with a real git the prologue completes and reports
// meta.history_status == "ok".
func TestRobotTriage_HistoryStatusOKWithHealthyGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir := createLivenessRepo(t)

	out, _ := runRobotTriageBounded(t, repoDir, nil)

	if status := triageHistoryStatus(t, out); status != "ok" {
		t.Errorf("meta.history_status = %q, want %q", status, "ok")
	}
}
