package export

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInitAndPush_ReturnsLeaseErrorWithoutUnsafeForceFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}

	binDir := t.TempDir()
	gitLogPath := filepath.Join(binDir, "git.log")

	ghScript := `#!/bin/sh
set -eu
if [ "${1-}" = "api" ]; then
  # RepoHasContent calls: gh api repos/<repo>/contents -q length
  echo "1"
  exit 0
fi
exit 0
`
	gitScript := `#!/bin/sh
set -eu
cmd="${1-}"
shift || true

case "$cmd" in
  remote)
    sub="${1-}"
    shift || true
    case "$sub" in
      get-url)
        # Pretend there is no existing origin.
        exit 1
        ;;
      remove)
        exit 0
        ;;
      add)
        exit 0
        ;;
    esac
    ;;
  init)
    exit 0
    ;;
  add)
    exit 0
    ;;
  commit)
    echo "nothing to commit"
    exit 1
    ;;
  branch)
    echo "already"
    exit 1
    ;;
  fetch)
    printf '%s\n' "fetch $*" >> "$GIT_LOG_FILE"
    exit 0
    ;;
  push)
    printf '%s\n' "push $*" >> "$GIT_LOG_FILE"
    if echo "$*" | grep -q -- "--force-with-lease"; then
      echo "cannot be resolved"
      exit 1
    fi
    echo "unsafe raw force push attempted"
    exit 2
    ;;
esac

exit 0
`

	writeExecutable(t, binDir, "gh", ghScript)
	writeExecutable(t, binDir, "git", gitScript)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fmt.Sprintf("%s%c%s", binDir, os.PathListSeparator, origPath))
	t.Setenv("GIT_LOG_FILE", gitLogPath)

	bundleDir := t.TempDir()
	// Ensure the directory contains at least one file for realism.
	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("<!doctype html>"), 0644); err != nil {
		t.Fatalf("WriteFile index.html: %v", err)
	}

	err := InitAndPush(bundleDir, "alice/repo", true)
	if err == nil {
		t.Fatalf("expected InitAndPush to return the force-with-lease push error")
	}
	if !strings.Contains(err.Error(), "cannot be resolved") {
		t.Fatalf("expected lease failure in InitAndPush error, got: %v", err)
	}

	gitLog, readErr := os.ReadFile(gitLogPath)
	if readErr != nil {
		t.Fatalf("read git log: %v", readErr)
	}
	logText := string(gitLog)
	if !strings.Contains(logText, "fetch --depth=1 origin main") {
		t.Fatalf("expected InitAndPush to fetch origin/main before force-with-lease push, log:\n%s", logText)
	}
	if !strings.Contains(logText, "push -u origin main --force-with-lease") {
		t.Fatalf("expected InitAndPush to use force-with-lease, log:\n%s", logText)
	}
	if strings.Contains(logText, "push -u origin main --force\n") {
		t.Fatalf("InitAndPush attempted unsafe raw force fallback, log:\n%s", logText)
	}
}

func TestInitAndPush_RequiresForceOverwriteWhenRepoHasContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}

	binDir := t.TempDir()

	ghScript := `#!/bin/sh
set -eu
if [ "${1-}" = "api" ]; then
  echo "1"
  exit 0
fi
exit 0
`
	writeExecutable(t, binDir, "gh", ghScript)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fmt.Sprintf("%s%c%s", binDir, os.PathListSeparator, origPath))

	bundleDir := t.TempDir()
	if err := InitAndPush(bundleDir, "alice/repo", false); err == nil {
		t.Fatal("Expected InitAndPush to return error when repo has content and ForceOverwrite=false")
	} else if !strings.Contains(strings.ToLower(err.Error()), "forceoverwrite") {
		t.Fatalf("Unexpected InitAndPush error: %v", err)
	}
}

func TestRepoHasContent_FailsClosedOnAPIError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}

	binDir := t.TempDir()
	ghScript := `#!/bin/sh
set -eu
if [ "${1-}" = "api" ]; then
  echo "api rate limit exceeded"
  exit 1
fi
exit 0
`
	writeExecutable(t, binDir, "gh", ghScript)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fmt.Sprintf("%s%c%s", binDir, os.PathListSeparator, origPath))

	hasContent, err := RepoHasContent("alice/repo")
	if err == nil {
		t.Fatalf("expected RepoHasContent to return an error for API failure")
	}
	if hasContent {
		t.Fatalf("hasContent = true on API failure; want false")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		t.Fatalf("expected API failure context in error, got: %v", err)
	}
}

func TestRepoHasContent_TreatsNotFoundAsEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}

	binDir := t.TempDir()
	ghScript := `#!/bin/sh
set -eu
if [ "${1-}" = "api" ]; then
  echo "gh: Not Found (HTTP 404)"
  exit 1
fi
exit 0
`
	writeExecutable(t, binDir, "gh", ghScript)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fmt.Sprintf("%s%c%s", binDir, os.PathListSeparator, origPath))

	hasContent, err := RepoHasContent("alice/repo")
	if err != nil {
		t.Fatalf("RepoHasContent returned error for not-found response: %v", err)
	}
	if hasContent {
		t.Fatalf("hasContent = true for not-found response; want false")
	}
}

func TestInitAndPush_PropagatesRepoContentCheckFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}

	binDir := t.TempDir()
	gitLogPath := filepath.Join(binDir, "git.log")

	ghScript := `#!/bin/sh
set -eu
if [ "${1-}" = "api" ]; then
  echo "api rate limit exceeded"
  exit 1
fi
exit 0
`
	gitScript := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$GIT_LOG_FILE"
exit 0
`
	writeExecutable(t, binDir, "gh", ghScript)
	writeExecutable(t, binDir, "git", gitScript)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fmt.Sprintf("%s%c%s", binDir, os.PathListSeparator, origPath))
	t.Setenv("GIT_LOG_FILE", gitLogPath)

	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("<!doctype html>"), 0644); err != nil {
		t.Fatalf("WriteFile index.html: %v", err)
	}

	err := InitAndPush(bundleDir, "alice/repo", false)
	if err == nil {
		t.Fatalf("expected InitAndPush to return the content-check error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		t.Fatalf("expected API failure context in InitAndPush error, got: %v", err)
	}

	if gitLog, readErr := os.ReadFile(gitLogPath); readErr == nil && strings.TrimSpace(string(gitLog)) != "" {
		t.Fatalf("InitAndPush ran git commands after content-check failure:\n%s", gitLog)
	}
}

func TestPushToGHPagesBranch_UsesForceWithLeaseAfterFetchingRemoteBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}

	binDir := t.TempDir()
	gitLogPath := filepath.Join(binDir, "git.log")

	gitScript := `#!/bin/sh
set -eu
cmd="${1-}"
shift || true
printf '%s\n' "$cmd $*" >> "$GIT_LOG_FILE"

case "$cmd" in
  checkout|add|commit)
    exit 0
    ;;
  fetch)
    if [ "$*" = "--depth=1 origin gh-pages" ]; then
      exit 0
    fi
    exit 1
    ;;
  push)
    if echo "$*" | grep -q -- "--force-with-lease"; then
      exit 0
    fi
    echo "unsafe raw force push attempted"
    exit 2
    ;;
esac

exit 0
`
	writeExecutable(t, binDir, "git", gitScript)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fmt.Sprintf("%s%c%s", binDir, os.PathListSeparator, origPath))
	t.Setenv("GIT_LOG_FILE", gitLogPath)

	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("<!doctype html>"), 0644); err != nil {
		t.Fatalf("WriteFile index.html: %v", err)
	}

	if err := PushToGHPagesBranch(bundleDir, "alice/repo"); err != nil {
		t.Fatalf("PushToGHPagesBranch returned error: %v", err)
	}

	gitLog, err := os.ReadFile(gitLogPath)
	if err != nil {
		t.Fatalf("read git log: %v", err)
	}
	logText := string(gitLog)
	if !strings.Contains(logText, "fetch --depth=1 origin gh-pages") {
		t.Fatalf("expected PushToGHPagesBranch to fetch gh-pages before lease push, log:\n%s", logText)
	}
	if !strings.Contains(logText, "push -u origin HEAD:gh-pages --force-with-lease") {
		t.Fatalf("expected PushToGHPagesBranch to use force-with-lease, log:\n%s", logText)
	}
	if strings.Contains(logText, "push -u origin HEAD:gh-pages --force\n") {
		t.Fatalf("PushToGHPagesBranch attempted unsafe raw force push, log:\n%s", logText)
	}
}

func TestPushToGHPagesBranch_PushesCurrentBundleWhenBranchExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	tmpDir := t.TempDir()
	remoteDir := filepath.Join(tmpDir, "remote.git")
	bundleDir := filepath.Join(tmpDir, "bundle")
	if err := os.Mkdir(bundleDir, 0755); err != nil {
		t.Fatalf("create bundle dir: %v", err)
	}

	runGit(t, "", "init", "--bare", remoteDir)
	runGit(t, bundleDir, "init")
	runGit(t, bundleDir, "config", "user.name", "BV Test")
	runGit(t, bundleDir, "config", "user.email", "bv-test@example.com")
	runGit(t, bundleDir, "config", "commit.gpgsign", "false")
	runGit(t, bundleDir, "remote", "add", "origin", remoteDir)

	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("initial"), 0644); err != nil {
		t.Fatalf("write initial index: %v", err)
	}
	runGit(t, bundleDir, "add", ".")
	runGit(t, bundleDir, "commit", "-m", "initial main")
	runGit(t, bundleDir, "branch", "-M", "main")
	runGit(t, bundleDir, "push", "-u", "origin", "main")

	runGit(t, bundleDir, "checkout", "--orphan", "gh-pages")
	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("stale gh-pages"), 0644); err != nil {
		t.Fatalf("write stale gh-pages index: %v", err)
	}
	runGit(t, bundleDir, "add", ".")
	runGit(t, bundleDir, "commit", "-m", "stale gh-pages")
	runGit(t, bundleDir, "push", "-u", "origin", "gh-pages")

	runGit(t, bundleDir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("fresh bundle"), 0644); err != nil {
		t.Fatalf("write fresh bundle index: %v", err)
	}
	runGit(t, bundleDir, "add", ".")
	runGit(t, bundleDir, "commit", "-m", "fresh main")

	if err := PushToGHPagesBranch(bundleDir, "alice/repo"); err != nil {
		t.Fatalf("PushToGHPagesBranch returned error: %v", err)
	}

	got := runGitOutput(t, "", "--git-dir", remoteDir, "show", "gh-pages:index.html")
	if strings.TrimSpace(got) != "fresh bundle" {
		t.Fatalf("remote gh-pages index = %q, want fresh bundle", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = runGitOutput(t, dir, args...)
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}
