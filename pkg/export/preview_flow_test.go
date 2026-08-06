package export

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPreviewServer_StartWithGracefulShutdown_ReturnsStartError(t *testing.T) {
	server := NewPreviewServer("/path/does/not/exist", 9010)
	if err := server.StartWithGracefulShutdown(); err == nil {
		t.Fatal("Expected StartWithGracefulShutdown to return error for missing bundle path")
	}
}

func TestStartPreview_ReturnsBundleError(t *testing.T) {
	if err := StartPreview("/path/does/not/exist"); err == nil {
		t.Fatal("Expected StartPreview to return error for missing bundle path")
	}
}

func TestStartPreviewWithConfig_PortInUseReturnsError(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("<!doctype html><title>ok</title>"), 0644); err != nil {
		t.Fatalf("WriteFile index.html: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	port := listener.Addr().(*net.TCPAddr).Port
	cfg := PreviewConfig{
		BundlePath:  bundleDir,
		Port:        port,
		OpenBrowser: false,
		Quiet:       true,
	}

	if err := StartPreviewWithConfig(cfg); err == nil {
		t.Fatal("Expected StartPreviewWithConfig to return error when port is already in use")
	}
}

func TestStartPreviewWithConfig_PortInUseDoesNotOpenBrowser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs not supported on windows in this test")
	}

	browserCommand, _, err := browserOpenCommandForGOOS(runtime.GOOS, "http://127.0.0.1:1")
	if err != nil {
		t.Skipf("browser open command is unsupported on this platform: %v", err)
	}

	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, "index.html"), []byte("<!doctype html><title>ok</title>"), 0644); err != nil {
		t.Fatalf("WriteFile index.html: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	binDir := t.TempDir()
	browserLog := filepath.Join(binDir, "browser.log")
	browserScript := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$BV_BROWSER_LOG"
`
	writeExecutable(t, binDir, browserCommand, browserScript)

	t.Setenv("PATH", binDir)
	t.Setenv("BV_BROWSER_LOG", browserLog)
	t.Setenv("BV_NO_BROWSER", "")
	t.Setenv("BV_TEST_MODE", "")

	cfg := PreviewConfig{
		BundlePath:  bundleDir,
		Port:        listener.Addr().(*net.TCPAddr).Port,
		OpenBrowser: true,
		Quiet:       true,
	}

	if err := StartPreviewWithConfig(cfg); err == nil {
		t.Fatal("Expected StartPreviewWithConfig to return error when port is already in use")
	}

	time.Sleep(700 * time.Millisecond)

	content, err := os.ReadFile(browserLog)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("ReadFile browser log: %v", err)
	}
	if strings.TrimSpace(string(content)) != "" {
		t.Fatalf("browser opener ran even though preview server failed to bind: %q", string(content))
	}
}
