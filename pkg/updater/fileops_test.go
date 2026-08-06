package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyFile_PreservesContentAndMode(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.bin")
	dst := filepath.Join(tmpDir, "dst.bin")

	data := []byte("copy me")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatalf("chmod src: %v", err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("content mismatch: got %q want %q", got, data)
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat src: %v", err)
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if dstInfo.Mode() != srcInfo.Mode() {
		t.Fatalf("mode mismatch: dst=%v src=%v", dstInfo.Mode(), srcInfo.Mode())
	}
}

func TestSafeAssetName(t *testing.T) {
	got, err := safeAssetName("bv_0.11.0_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("safeAssetName returned error for valid name: %v", err)
	}
	if got != "bv_0.11.0_linux_amd64.tar.gz" {
		t.Fatalf("safeAssetName = %q, want original name", got)
	}

	for _, name := range []string{"", ".", "..", "../bv.tar.gz", "nested/bv.tar.gz"} {
		t.Run(name, func(t *testing.T) {
			if got, err := safeAssetName(name); err == nil {
				t.Fatalf("safeAssetName(%q) = %q, nil error; want error", name, got)
			}
		})
	}
}

func TestDecodeReleaseMetadata_LimitsBody(t *testing.T) {
	rel, err := decodeReleaseMetadata(strings.NewReader(`{"tag_name":"v9.9.9","html_url":"https://example.com","assets":[]}`))
	if err != nil {
		t.Fatalf("decodeReleaseMetadata valid body: %v", err)
	}
	if rel.TagName != "v9.9.9" {
		t.Fatalf("tag_name = %q, want v9.9.9", rel.TagName)
	}

	oversized := `{"tag_name":"` + strings.Repeat("x", maxReleaseMetadataBytes) + `"}`
	if _, err := decodeReleaseMetadata(strings.NewReader(oversized)); err == nil {
		t.Fatalf("decodeReleaseMetadata accepted body larger than limit")
	}
}

func TestDecodeReleaseMetadata_RejectsTrailingJSONValues(t *testing.T) {
	body := `{"tag_name":"v9.9.9","html_url":"https://example.com","assets":[]} {}`
	if _, err := decodeReleaseMetadata(strings.NewReader(body)); err == nil {
		t.Fatalf("decodeReleaseMetadata accepted trailing JSON value")
	}
}

func TestExtractBinary_FromArchive(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "bv.tar.gz")
	destPath := filepath.Join(tmpDir, "bv")

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	payload := []byte("fake-binary")
	hdr := &tar.Header{
		Name: "bv",
		Mode: 0o755,
		Size: int64(len(payload)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("write tar payload: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	if err := extractBinary(archivePath, destPath); err != nil {
		t.Fatalf("extractBinary failed: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
}

func TestExtractBinary_TarRejectsOversizedBinary(t *testing.T) {
	oldLimit := maxExtractedBinaryBytes
	maxExtractedBinaryBytes = 5
	t.Cleanup(func() { maxExtractedBinaryBytes = oldLimit })

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "bv.tar.gz")
	destPath := filepath.Join(tmpDir, "bv")

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	payload := []byte("too-big")
	if err := tw.WriteHeader(&tar.Header{
		Name: "bv",
		Mode: 0o755,
		Size: int64(len(payload)),
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("write tar payload: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	err := extractBinary(archivePath, destPath)
	if err == nil {
		t.Fatal("expected oversized tar binary to fail")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("expected size cap error, got %v", err)
	}
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Fatalf("destination should not be created for oversized tar member, stat err=%v", statErr)
	}
}

func TestExtractBinary_SkipsTarDirectoryNamedBinary(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "bv.tar.gz")
	destPath := filepath.Join(tmpDir, "bv")

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	if err := tw.WriteHeader(&tar.Header{
		Name:     "nested/bv",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	}); err != nil {
		t.Fatalf("write tar dir header: %v", err)
	}

	payload := []byte("real-binary")
	if err := tw.WriteHeader(&tar.Header{
		Name: "bv",
		Mode: 0o755,
		Size: int64(len(payload)),
	}); err != nil {
		t.Fatalf("write tar file header: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("write tar payload: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	if err := extractBinary(archivePath, destPath); err != nil {
		t.Fatalf("extractBinary failed: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
}

func TestExtractBinary_FromZipArchive(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "bv.zip")
	destPath := filepath.Join(tmpDir, "bv.exe")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("nested/bv.exe")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	payload := []byte("fake-windows-binary")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write zip payload: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	if err := extractBinary(archivePath, destPath); err != nil {
		t.Fatalf("extractBinary failed: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
}

func TestExtractBinary_ZipRejectsOversizedBinary(t *testing.T) {
	oldLimit := maxExtractedBinaryBytes
	maxExtractedBinaryBytes = 5
	t.Cleanup(func() { maxExtractedBinaryBytes = oldLimit })

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "bv.zip")
	destPath := filepath.Join(tmpDir, "bv.exe")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("bv.exe")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write([]byte("too-big")); err != nil {
		t.Fatalf("write zip payload: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	err = extractBinary(archivePath, destPath)
	if err == nil {
		t.Fatal("expected oversized zip binary to fail")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("expected size cap error, got %v", err)
	}
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Fatalf("destination should not be created for oversized zip member, stat err=%v", statErr)
	}
}

func TestExtractBinary_SkipsZipDirectoryNamedBinary(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "bv.zip")
	destPath := filepath.Join(tmpDir, "bv.exe")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("nested/bv.exe/"); err != nil {
		t.Fatalf("create zip dir entry: %v", err)
	}
	w, err := zw.Create("bv.exe")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	payload := []byte("real-windows-binary")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("write zip payload: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	if err := extractBinary(archivePath, destPath); err != nil {
		t.Fatalf("extractBinary failed: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
}

func TestRollback_NoBackup(t *testing.T) {
	err := Rollback()
	if err == nil {
		t.Fatalf("expected rollback to fail when no backup exists")
	}
	if !strings.Contains(err.Error(), "no backup found") {
		t.Fatalf("unexpected error: %v", err)
	}
}
