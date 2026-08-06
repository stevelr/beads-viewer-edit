package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRelease_FindPlatformAsset(t *testing.T) {
	rel := &Release{TagName: "v1.2.3"}
	target := stableAssetName()
	rel.Assets = []Asset{
		{Name: "other.tar.gz"},
		{Name: target, BrowserDownloadURL: "http://example.com/bv.tgz"},
	}

	asset := rel.FindPlatformAsset()
	if asset == nil {
		t.Fatalf("expected platform asset %q", target)
	}
	if asset.Name != target {
		t.Fatalf("expected %q, got %q", target, asset.Name)
	}
}

func TestRelease_FindPlatformAsset_LegacyVersionedFallback(t *testing.T) {
	rel := &Release{TagName: "v1.2.3"}
	legacyTarget := getAssetName(rel.TagName)
	rel.Assets = []Asset{
		{Name: "other.tar.gz"},
		{Name: legacyTarget, BrowserDownloadURL: "http://example.com/bv.tgz"},
	}

	asset := rel.FindPlatformAsset()
	if asset == nil {
		t.Fatalf("expected legacy platform asset %q", legacyTarget)
	}
	if asset.Name != legacyTarget {
		t.Fatalf("expected %q, got %q", legacyTarget, asset.Name)
	}
}

func TestRelease_FindPlatformAssetWithChecksumFallsBackToCheckedLegacyName(t *testing.T) {
	rel := &Release{TagName: "v1.2.3"}
	stableTarget := stableAssetName()
	legacyTarget := getAssetName(rel.TagName)
	rel.Assets = []Asset{
		{Name: stableTarget, BrowserDownloadURL: "http://example.com/stable"},
		{Name: legacyTarget, BrowserDownloadURL: "http://example.com/legacy"},
	}

	asset := rel.findPlatformAssetWithChecksum(map[string]string{
		legacyTarget: "hash",
	})
	if asset == nil {
		t.Fatalf("expected checked legacy asset %q", legacyTarget)
	}
	if asset.Name != legacyTarget {
		t.Fatalf("expected %q, got %q", legacyTarget, asset.Name)
	}
}

func TestRelease_FindPlatformAssetWithChecksumPrefersCheckedStableName(t *testing.T) {
	rel := &Release{TagName: "v1.2.3"}
	stableTarget := stableAssetName()
	legacyTarget := getAssetName(rel.TagName)
	rel.Assets = []Asset{
		{Name: legacyTarget, BrowserDownloadURL: "http://example.com/legacy"},
		{Name: stableTarget, BrowserDownloadURL: "http://example.com/stable"},
	}

	asset := rel.findPlatformAssetWithChecksum(map[string]string{
		stableTarget: "hash",
		legacyTarget: "hash",
	})
	if asset == nil {
		t.Fatalf("expected checked stable asset %q", stableTarget)
	}
	if asset.Name != stableTarget {
		t.Fatalf("expected %q, got %q", stableTarget, asset.Name)
	}
}

func TestRelease_FindChecksumAsset(t *testing.T) {
	rel := &Release{
		Assets: []Asset{
			{Name: "bv_v1.0.0_darwin_arm64.tar.gz"},
			{Name: "checksums.txt", BrowserDownloadURL: "http://example.com/checksums"},
		},
	}
	asset := rel.FindChecksumAsset()
	if asset == nil || asset.Name != "checksums.txt" {
		t.Fatalf("expected checksums.txt asset, got %#v", asset)
	}
}

func TestGetAssetName_UsesRuntimeAndTrimsV(t *testing.T) {
	name := getAssetName("v9.8.7")
	want := "bv_9.8.7_" + runtime.GOOS + "_" + runtime.GOARCH + platformArchiveExtension(runtime.GOOS)
	if name != want {
		t.Fatalf("getAssetName mismatch: got %q want %q", name, want)
	}
}

func TestPlatformArchiveExtension(t *testing.T) {
	if got := platformArchiveExtension("windows"); got != ".zip" {
		t.Fatalf("windows extension=%q, want .zip", got)
	}
	if got := platformArchiveExtension("linux"); got != ".tar.gz" {
		t.Fatalf("linux extension=%q, want .tar.gz", got)
	}
}

func TestParseChecksums(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "checksums.txt")

	content := "" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  bv_1.0.0_darwin_arm64.tar.gz\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  checksums.txt\n" +
		"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	m, err := parseChecksums(path)
	if err != nil {
		t.Fatalf("parseChecksums failed: %v", err)
	}
	if got := m["bv_1.0.0_darwin_arm64.tar.gz"]; got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected checksum for archive: %q", got)
	}
	if got := m["checksums.txt"]; got != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("unexpected checksum for checksums.txt: %q", got)
	}
}

func TestParseChecksums_FilenamesWithSpaces(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "checksums.txt")

	content := "" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  bv 1.0.0 windows amd64.tar.gz\n" +
		"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	m, err := parseChecksums(path)
	if err != nil {
		t.Fatalf("parseChecksums failed: %v", err)
	}
	if got := m["bv 1.0.0 windows amd64.tar.gz"]; got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected checksum for spaced filename: %q", got)
	}
}

func TestParseChecksums_NormalizesUppercaseAndSkipsInvalidHashes(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "checksums.txt")

	content := "" +
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA  bv_1.0.0_linux_amd64.tar.gz\n" +
		"not-a-sha256  ignored.tar.gz\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	m, err := parseChecksums(path)
	if err != nil {
		t.Fatalf("parseChecksums failed: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected one valid checksum, got %d: %#v", len(m), m)
	}
	if got := m["bv_1.0.0_linux_amd64.tar.gz"]; got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected normalized checksum: %q", got)
	}
}

func TestParseChecksums_RejectsDuplicateFilename(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "checksums.txt")

	content := "" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  bv.tar.gz\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  bv.tar.gz\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	if _, err := parseChecksums(path); err == nil {
		t.Fatalf("expected duplicate checksum entry error")
	}
}

func TestVerifyChecksum(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "file.bin")
	data := []byte("hello updater")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	sum := sha256.Sum256(data)
	okHash := hex.EncodeToString(sum[:])

	if err := verifyChecksum(path, okHash); err != nil {
		t.Fatalf("verifyChecksum expected ok, got %v", err)
	}
	if err := verifyChecksum(path, "deadbeef"); err == nil {
		t.Fatalf("expected checksum mismatch error")
	}
}
