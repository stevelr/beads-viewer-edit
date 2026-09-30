package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// Update-check integration tests
// ============================================================================

func TestCheckForUpdates_WithMockServer(t *testing.T) {
	// Create realistic archive/checksum metadata for a mock GitHub API response.
	binaryContent := []byte("#!/bin/sh\necho 'mock bv v99.0.0'")

	// Create tar.gz archive
	var archiveBuf bytes.Buffer
	gzw := gzip.NewWriter(&archiveBuf)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "bv",
		Mode: 0o755,
		Size: int64(len(binaryContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	archiveBytes := archiveBuf.Bytes()

	// Calculate checksum
	h := sha256.New()
	if _, err := h.Write(archiveBytes); err != nil {
		t.Fatalf("hash archive: %v", err)
	}
	archiveHash := hex.EncodeToString(h.Sum(nil))
	assetName := getAssetName("v99.0.0")
	checksumContent := fmt.Sprintf("%s  %s\n", archiveHash, assetName)
	checksumHash := sha256.Sum256([]byte(checksumContent))

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		release := Release{
			TagName: "v99.0.0",
			HTMLURL: "https://github.com/stevelr/beads-viewer-edit/releases/tag/v99.0.0",
			Assets: []Asset{
				{
					Name:               assetName,
					BrowserDownloadURL: "https://github.com/stevelr/beads-viewer-edit/releases/download/v99.0.0/" + assetName,
					Size:               int64(len(archiveBytes)),
					Digest:             "sha256:" + archiveHash,
					State:              "uploaded",
				},
				{
					Name:               "checksums.txt",
					BrowserDownloadURL: "https://github.com/stevelr/beads-viewer-edit/releases/download/v99.0.0/checksums.txt",
					Size:               int64(len(checksumContent)),
					Digest:             "sha256:" + hex.EncodeToString(checksumHash[:]),
					State:              "uploaded",
				},
			},
		}
		if err := json.NewEncoder(w).Encode(release); err != nil {
			t.Errorf("encode release: %v", err)
		}
	})
	mux.HandleFunc("/archive", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(archiveBytes)))
		if _, err := w.Write(archiveBytes); err != nil {
			t.Errorf("write archive response: %v", err)
		}
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(checksumContent)))
		if _, err := w.Write([]byte(checksumContent)); err != nil {
			t.Errorf("write checksum response: %v", err)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Test that the mocked release can be parsed
	client := srv.Client()
	rewriteGitHubDownloadsToServer(t, client, srv.URL)
	tag, url, err := checkForUpdates(client, srv.URL+"/releases/latest")
	if err != nil {
		t.Fatalf("checkForUpdates failed: %v", err)
	}

	if tag != "v99.0.0" {
		t.Errorf("expected tag v99.0.0, got %s", tag)
	}
	if url != "https://github.com/stevelr/beads-viewer-edit/releases/tag/v99.0.0" {
		t.Errorf("expected release URL, got %s", url)
	}
}

// TestCheckUpdateAvailable_NoUpdateNeeded verifies no update when remote is older
func TestCheckUpdateAvailable_NoUpdateNeeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		release := Release{
			TagName: "v0.0.1", // Lower than any real version
			HTMLURL: "https://github.com/stevelr/beads-viewer-edit/releases/tag/v0.0.1",
		}
		if err := json.NewEncoder(w).Encode(release); err != nil {
			t.Errorf("encode release: %v", err)
		}
	}))
	defer srv.Close()

	client := srv.Client()
	tag, _, err := checkForUpdates(client, srv.URL)
	if err != nil {
		t.Fatalf("checkForUpdates failed: %v", err)
	}

	// Should be empty (no update) since v0.0.1 < current version
	if tag != "" {
		t.Errorf("expected no update (empty tag), got %s", tag)
	}
}

// ============================================================================
// Edge cases for extraction
// ============================================================================

func TestExtractBinary_NoBinaryInArchive(t *testing.T) {
	// Create archive without bv binary
	var archiveBuf bytes.Buffer
	gzw := gzip.NewWriter(&archiveBuf)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "other_file.txt",
		Mode: 0o644,
		Size: 5,
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte("hello"))
	tw.Close()
	gzw.Close()

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "archive.tar.gz")
	destPath := filepath.Join(tmpDir, "bv")

	if err := os.WriteFile(archivePath, archiveBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	err := extractBinary(archivePath, destPath)
	if err == nil {
		t.Fatal("expected error when binary not found in archive")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestExtractBinary_NestedPath(t *testing.T) {
	// Create archive with nested bv binary
	content := []byte("nested binary")
	var archiveBuf bytes.Buffer
	gzw := gzip.NewWriter(&archiveBuf)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "some/nested/path/bv",
		Mode: 0o755,
		Size: int64(len(content)),
	}
	tw.WriteHeader(hdr)
	tw.Write(content)
	tw.Close()
	gzw.Close()

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "archive.tar.gz")
	destPath := filepath.Join(tmpDir, "bv")

	if err := os.WriteFile(archivePath, archiveBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	// Should find bv even in nested path
	err := extractBinary(archivePath, destPath)
	if err != nil {
		t.Fatalf("extractBinary failed: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

// TestDownloadFile_404_ReturnsError verifies HTTP error handling
func TestDownloadFile_404_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "download.bin")

	err := downloadFile(srv.URL, destPath, 0)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

// TestParseChecksums_EmptyLinesIgnored verifies empty line handling
func TestParseChecksums_EmptyLinesIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	checksumPath := filepath.Join(tmpDir, "checksums.txt")

	content := `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  file1.tar.gz

bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  file2.tar.gz

cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  file3.tar.gz`

	if err := os.WriteFile(checksumPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	checksums, err := parseChecksums(checksumPath)
	if err != nil {
		t.Fatalf("parseChecksums failed: %v", err)
	}

	if len(checksums) != 3 {
		t.Errorf("expected 3 checksums (ignoring empty lines), got %d", len(checksums))
	}
}

// TestGetAssetName_HandlesVersionWithoutV verifies version without v prefix
func TestGetAssetName_HandlesVersionWithoutV(t *testing.T) {
	name := getAssetName("2.0.0")
	if !strings.HasPrefix(name, "bv_2.0.0_") {
		t.Errorf("expected asset name to start with bv_2.0.0_, got %s", name)
	}
}

// TestRelease_FindPlatformAsset_NoAssets verifies empty asset list handling
func TestRelease_FindPlatformAsset_NoAssets(t *testing.T) {
	rel := Release{
		TagName: "v1.0.0",
		Assets:  []Asset{},
	}

	asset := rel.FindPlatformAsset()
	if asset != nil {
		t.Errorf("expected nil for empty assets, got %+v", asset)
	}
}
