package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Locally patched dependencies live under third_party/<name>/ as complete Go
// modules and reach the build through `replace` directives in go.mod, so
// `go mod vendor` copies the patched sources instead of reverting them to the
// upstream module cache. This test pins the remaining failure modes: a patch
// applied to vendor/ by hand (lost on the next vendor run), a change under
// third_party/ that was never vendored, and a third_party/ copy no replace
// directive points at. See docs/PROVENANCE.md.
func TestThirdPartyReplacementsMatchVendor(t *testing.T) {
	root := findRepoRoot(t)
	replacements := thirdPartyReplacements(t, root)
	if len(replacements) == 0 {
		t.Fatal("go.mod declares no replace directive into third_party/; the vendored patches would be lost by go mod vendor")
	}
	referenced := map[string]bool{}
	for modulePath, dir := range replacements {
		referenced[dir] = true
		src := filepath.Join(root, dir)
		dst := filepath.Join(root, "vendor", filepath.FromSlash(modulePath))
		if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
			t.Errorf("%s: replacement %s is not a Go module (missing go.mod): %v", modulePath, dir, err)
			continue
		}
		compareTrees(t, modulePath, src, dst)
	}
	entries, err := os.ReadDir(filepath.Join(root, "third_party"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && !referenced["third_party/"+entry.Name()] {
			t.Errorf("third_party/%s has no replace directive in go.mod; it is dead weight, not a vendored patch", entry.Name())
		}
	}
}

// thirdPartyReplacements maps module path to its slash-separated replacement
// directory for every replace directive that points into third_party/.
func thirdPartyReplacements(t *testing.T, root string) map[string]string {
	t.Helper()
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go mod edit -json: %v", err)
	}
	var modFile struct {
		Replace []struct {
			Old struct{ Path string }
			New struct{ Path string }
		}
	}
	if err := json.Unmarshal(out, &modFile); err != nil {
		t.Fatalf("decode go.mod: %v", err)
	}
	replacements := map[string]string{}
	for _, r := range modFile.Replace {
		dir := filepath.ToSlash(filepath.Clean(r.New.Path))
		if strings.HasPrefix(dir, "third_party/") {
			replacements[r.Old.Path] = dir
		}
	}
	return replacements
}

// compareTrees requires every buildable file under src to exist byte-for-byte
// under dst and vice versa. Module metadata, test files and testdata are not
// copied by `go mod vendor`, so they are ignored on the source side.
func compareTrees(t *testing.T, modulePath, src, dst string) {
	t.Helper()
	skipSource := func(rel string, d os.DirEntry) bool {
		if d.IsDir() {
			return d.Name() == "testdata"
		}
		return rel == "go.mod" || rel == "go.sum" || strings.HasSuffix(rel, "_test.go")
	}
	walk := func(dir string, skip func(string, os.DirEntry) bool, visit func(rel string)) {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				return relErr
			}
			if rel == "." {
				return nil
			}
			if skip != nil && skip(rel, d) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				visit(rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: walk %s: %v", modulePath, dir, err)
		}
	}
	walk(src, skipSource, func(rel string) {
		want, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("%s: %s exists in the replacement but not in vendor/; run go mod vendor", modulePath, rel)
			return
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s: vendor/%s differs from third_party copy %s; edit the third_party module and run go mod vendor, never vendor/ directly", modulePath, rel, rel)
		}
	})
	walk(dst, nil, func(rel string) {
		if _, err := os.Stat(filepath.Join(src, rel)); err != nil {
			t.Errorf("%s: vendor/%s has no counterpart in the replacement module; vendor/ is stale", modulePath, rel)
		}
	})
}
