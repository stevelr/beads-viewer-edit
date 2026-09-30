//go:build (darwin || linux) && !android

package agents

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestAgentFileLockContentionIsBounded(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()

	started := time.Now()
	second, unlock, err := openAndLockAgentFileForMutation(filePath, 40*time.Millisecond)
	if second != nil {
		_ = second.Close()
	}
	if unlock != nil {
		unlock()
	}
	if !errors.Is(err, errAgentFileBusy) {
		t.Fatalf("contended lock error=%v, want busy", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("contended lock took %s, want a bounded failure", elapsed)
	}
}

func TestWriteReplacementPreservesExtendedAttributes(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	const attribute = "user.beadsviewer.test"
	want := []byte("preserve-me")
	if err := unix.Setxattr(filePath, attribute, want, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM) {
			t.Skipf("filesystem does not support a writable test xattr: %v", err)
		}
		t.Fatal(err)
	}

	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()
	if err := locked.replace([]byte("replacement")); err != nil {
		t.Fatal(err)
	}

	size, err := unix.Getxattr(filePath, attribute, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, size)
	if _, err := unix.Getxattr(filePath, attribute, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("extended attribute=%q, want %q", got, want)
	}
}

func TestCopyAgentExtendedAttributesRemovesReplacementOnlyAttributes(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.md")
	replacementPath := filepath.Join(dir, "replacement.md")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	const sourceAttribute = "user.beadsviewer.source"
	const replacementOnlyAttribute = "user.beadsviewer.inherited"
	if err := unix.Setxattr(sourcePath, sourceAttribute, []byte("source-value"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM) {
			t.Skipf("filesystem does not support writable test xattrs: %v", err)
		}
		t.Fatal(err)
	}
	if err := unix.Setxattr(replacementPath, replacementOnlyAttribute, []byte("remove-me"), 0); err != nil {
		t.Fatal(err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	replacement, err := os.OpenFile(replacementPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := copyAgentExtendedAttributes(source, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Getxattr(replacementPath, replacementOnlyAttribute, nil); err == nil {
		t.Fatalf("replacement-only extended attribute %q survived exact copy", replacementOnlyAttribute)
	}
	size, err := unix.Getxattr(replacementPath, sourceAttribute, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := make([]byte, size)
	if _, err := unix.Getxattr(replacementPath, sourceAttribute, value); err != nil {
		t.Fatal(err)
	}
	if string(value) != "source-value" {
		t.Fatalf("copied source extended attribute=%q, want source-value", value)
	}
}

func TestContentBoundIntegrityAttributesRequireRecomputation(t *testing.T) {
	for _, name := range []string{"security.ima", "security.evm"} {
		if !agentExtendedAttributeRequiresRecomputation(name) {
			t.Fatalf("%q was not classified as content-bound", name)
		}
	}
	for _, name := range []string{"user.beadsviewer.test", "security.selinux", "com.apple.quarantine"} {
		if agentExtendedAttributeRequiresRecomputation(name) {
			t.Fatalf("%q was incorrectly classified as content-bound", name)
		}
	}
}

func TestWriteReplacementPreservesLinuxNoDumpFlag(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux inode-flag regression")
	}
	filePath := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("chattr", "+d", filePath).CombinedOutput(); err != nil {
		t.Skipf("cannot create a Linux nodump fixture: %v: %s", err, output)
	}
	if !linuxLSAttrContains(t, filePath, 'd') {
		t.Skip("filesystem accepted chattr but did not expose nodump")
	}

	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()
	if err := locked.replace([]byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if !linuxLSAttrContains(t, filePath, 'd') {
		t.Fatal("Linux nodump flag was lost during replacement")
	}
}

func linuxLSAttrContains(t *testing.T, path string, flag byte) bool {
	t.Helper()
	output, err := exec.Command("lsattr", "-d", path).Output()
	if err != nil {
		t.Fatalf("lsattr failed: %v", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		t.Fatalf("lsattr returned no fields: %q", output)
	}
	return strings.ContainsRune(fields[0], rune(flag))
}

func TestDarwinReplacementRejectsSourceACLItCannotSecurelyReproduce(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin ACL regression")
	}
	filePath := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	userOutput, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Fatal(err)
	}
	acl := strings.TrimSpace(string(userOutput)) + " allow read"
	if output, err := exec.Command("chmod", "+a", acl, filePath).CombinedOutput(); err != nil {
		t.Skipf("cannot create a Darwin ACL fixture: %v: %s", err, output)
	}
	before := darwinACLListing(t, filePath)
	if before == "" {
		t.Fatal("ACL fixture has no visible ACL entries")
	}

	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()
	if err := locked.replace([]byte("replacement")); err == nil || !strings.Contains(err.Error(), "replacement ACL differs") {
		t.Fatalf("replace error=%v, want source-ACL refusal", err)
	}
	after := darwinACLListing(t, filePath)
	if after != before {
		t.Fatalf("ACL changed:\n before: %q\n  after: %q", before, after)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("source changed despite source-ACL refusal: %q", content)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(filePath), ".bv-replace-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("source-ACL refusal left replacement artifacts: %v", matches)
	}
}

func TestDarwinReplacementRejectsNewlyInheritedACL(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin ACL-inheritance regression")
	}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if before := darwinACLListing(t, filePath); before != "" {
		t.Fatalf("source unexpectedly started with an ACL: %q", before)
	}
	userOutput, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Fatal(err)
	}
	inheritableACL := strings.TrimSpace(string(userOutput)) + " allow read,file_inherit"
	if output, err := exec.Command("chmod", "+a", inheritableACL, dir).CombinedOutput(); err != nil {
		t.Skipf("cannot create a Darwin inheritable ACL fixture: %v: %s", err, output)
	}

	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()
	if err := locked.replace([]byte("replacement")); err == nil || !strings.Contains(err.Error(), "replacement ACL differs") {
		t.Fatalf("replace error=%v, want inherited-ACL refusal", err)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("source changed despite inherited-ACL refusal: %q", content)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".bv-replace-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("inherited-ACL refusal left replacement artifacts: %v", matches)
	}
}

func TestDarwinImmutableSourceIsRejectedWithoutReplacementArtifact(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin immutable-flag regression")
	}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("chflags", "uchg", filePath).CombinedOutput(); err != nil {
		t.Skipf("cannot create a Darwin immutable fixture: %v: %s", err, output)
	}
	defer func() {
		if output, err := exec.Command("chflags", "nouchg", filePath).CombinedOutput(); err != nil {
			t.Errorf("clear Darwin immutable fixture: %v: %s", err, output)
		}
	}()

	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()
	if err := locked.replace([]byte("replacement")); err == nil || !strings.Contains(err.Error(), "immutable or append-only") {
		t.Fatalf("replace error=%v, want immutable-source refusal", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".bv-replace-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("immutable-source refusal left replacement artifacts: %v", matches)
	}
}

func darwinACLListing(t *testing.T, path string) string {
	t.Helper()
	output, err := exec.Command("ls", "-le", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) <= 1 {
		return ""
	}
	for i := range lines[1:] {
		lines[i+1] = strings.TrimSpace(lines[i+1])
	}
	return strings.Join(lines[1:], "\n")
}
