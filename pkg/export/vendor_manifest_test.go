package export

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vendorManifest mirrors pkg/export/viewer_assets/vendor/MANIFEST.json.
type vendorManifest struct {
	ReviewedBy string `json:"reviewed_by"`
	Date       string `json:"date"`
	Files      []struct {
		Name         string `json:"name"`
		Upstream     string `json:"upstream"`
		Version      string `json:"version"`
		License      string `json:"license"`
		SHA256       string `json:"sha256"`
		SourceURL    string `json:"source_url"`
		BuildCommand string `json:"build_command"`
	} `json:"files"`
}

func vendorDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("viewer_assets", "vendor")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("vendor dir: %v", err)
	}
	return dir
}

func loadVendorManifest(t *testing.T, dir string) vendorManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "MANIFEST.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m vendorManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestVendorManifest_MatchesShippedAssets (G3): every vendored viewer asset is
// listed with provenance fields and its exact sha256, and nothing ships that
// the manifest does not name.
func TestVendorManifest_MatchesShippedAssets(t *testing.T) {
	dir := vendorDir(t)
	m := loadVendorManifest(t, dir)
	if m.ReviewedBy == "" || m.Date == "" || len(m.Files) == 0 {
		t.Fatalf("manifest needs reviewed_by, date and files: %+v", m)
	}

	listed := map[string]bool{}
	for _, f := range m.Files {
		if f.Name == "" || f.Upstream == "" || f.Version == "" || f.License == "" || f.SourceURL == "" || f.BuildCommand == "" {
			t.Errorf("manifest entry %q is missing a provenance field: %+v", f.Name, f)
		}
		if len(f.SHA256) != 64 {
			t.Errorf("manifest entry %q has a malformed sha256 %q", f.Name, f.SHA256)
			continue
		}
		listed[f.Name] = true
		path := filepath.Join(dir, f.Name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("manifest lists %s but it is not on disk: %v", f.Name, err)
			continue
		}
		if got := sha256File(t, path); got != f.SHA256 {
			t.Errorf("%s: sha256 %s does not match manifest %s (update MANIFEST.json when replacing an asset)", f.Name, got, f.SHA256)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read vendor dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "MANIFEST.json" {
			continue
		}
		if !listed[e.Name()] {
			t.Errorf("%s ships without a manifest entry", e.Name())
		}
	}
}

// TestVendorManifest_DetectsTamperedAsset proves the check is not vacuous: a
// copy of the vendor directory with one flipped byte fails the same
// verification.
func TestVendorManifest_DetectsTamperedAsset(t *testing.T) {
	src := vendorDir(t)
	m := loadVendorManifest(t, src)
	tmp := t.TempDir()
	for _, f := range m.Files {
		data, err := os.ReadFile(filepath.Join(src, f.Name))
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		if err := os.WriteFile(filepath.Join(tmp, f.Name), data, 0o644); err != nil {
			t.Fatalf("copy %s: %v", f.Name, err)
		}
	}
	victim := m.Files[0].Name
	data, _ := os.ReadFile(filepath.Join(tmp, victim))
	data[len(data)/2] ^= 0x01
	if err := os.WriteFile(filepath.Join(tmp, victim), data, 0o644); err != nil {
		t.Fatalf("tamper %s: %v", victim, err)
	}

	mismatches := 0
	for _, f := range m.Files {
		if sha256File(t, filepath.Join(tmp, f.Name)) != f.SHA256 {
			mismatches++
			if f.Name != victim {
				t.Errorf("untouched %s reported a mismatch", f.Name)
			}
		}
	}
	if mismatches != 1 {
		t.Fatalf("expected exactly the tampered %s to mismatch, got %d mismatches", victim, mismatches)
	}
	if !strings.HasSuffix(victim, ".js") && !strings.HasSuffix(victim, ".wasm") && !strings.HasSuffix(victim, ".woff2") {
		t.Fatalf("unexpected first manifest entry %q", victim)
	}
}
