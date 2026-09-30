package export

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A prepared browser opener must carry the PATH resolution and environment of
// the moment it was prepared, so a delayed launch cannot pick up a later
// caller's (or the next test's) environment. This is the regression test for
// the flaky TestStartPreviewWithConfig_PortInUseDoesNotOpenBrowser, where a
// previous test's 500 ms-delayed opener fired inside the next test's stub PATH.
func TestPrepareBrowserOpen_ResolvesPathAndEnvAtPrepareTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows")
	}
	browserCommand, _, err := browserOpenCommandForGOOS(runtime.GOOS, "http://127.0.0.1:1")
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	log := filepath.Join(binDir, "browser.log")
	writeExecutable(t, binDir, browserCommand, "#!/bin/sh\nset -eu\nprintf '%s|%s\\n' \"$*\" \"${BV_STAMP:-}\" >> \"$BV_BROWSER_LOG\"\n")

	t.Setenv("BV_NO_BROWSER", "")
	t.Setenv("BV_TEST_MODE", "")
	t.Setenv("PATH", binDir)
	t.Setenv("BV_BROWSER_LOG", log)
	t.Setenv("BV_STAMP", "first")

	cmd, err := prepareBrowserOpen("http://127.0.0.1:9")
	if err != nil {
		t.Fatalf("prepareBrowserOpen: %v", err)
	}
	if cmd == nil {
		t.Fatal("opener must be prepared when browser opening is enabled")
	}

	// Change the world the way the next test would: empty PATH, different
	// log, different stamp, browser disabled.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BV_BROWSER_LOG", filepath.Join(t.TempDir(), "other.log"))
	t.Setenv("BV_STAMP", "second")
	t.Setenv("BV_NO_BROWSER", "1")

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start with prepared command: %v", err)
	}
	_ = cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for {
		content, err := os.ReadFile(log)
		if err == nil && strings.TrimSpace(string(content)) != "" {
			got := strings.TrimSpace(string(content))
			if !strings.Contains(got, "http://127.0.0.1:9|first") {
				t.Fatalf("opener ran with the wrong URL or environment: %q", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("prepared opener did not run into the original log (err=%v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPrepareBrowserOpen_DisabledByEnv(t *testing.T) {
	for _, v := range []string{"BV_NO_BROWSER", "BV_TEST_MODE"} {
		t.Setenv("BV_NO_BROWSER", "")
		t.Setenv("BV_TEST_MODE", "")
		t.Setenv(v, "1")
		cmd, err := prepareBrowserOpen("http://127.0.0.1:9")
		if err != nil || cmd != nil {
			t.Fatalf("%s=1 must disable the opener, got cmd=%v err=%v", v, cmd, err)
		}
	}
}
