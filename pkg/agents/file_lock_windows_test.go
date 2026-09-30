//go:build windows

package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestAgentFileMutexContentionIsBounded(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()

	result := make(chan error, 1)
	go func() {
		file, unlock, err := openAndLockAgentFileForMutation(filePath, 40*time.Millisecond)
		if unlock != nil {
			_ = unlock()
		}
		if file != nil {
			_ = file.Close()
		}
		result <- err
	}()
	if err := <-result; !errors.Is(err, errAgentFileBusy) {
		t.Fatalf("contended mutex error=%v, want busy", err)
	}
}

func TestAgentFilePathInfoIsComparableWhileSourceHandleIsOpen(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	handleInfo, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	pathInfo, err := agentFilePathInfo(filePath)
	if err != nil {
		t.Fatalf("path identity probe conflicted with open source handle: %v", err)
	}
	if !os.SameFile(handleInfo, pathInfo) {
		t.Fatal("handle and path identity probes disagreed for the same file")
	}
}

func TestReplacementHandleDeniesPeerReadWriteAndDeleteSharing(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()
	replacement, replacementPath, _, err := createAgentReplacementFile(locked)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()

	peerRead, err := os.Open(replacementPath)
	if peerRead != nil {
		_ = peerRead.Close()
	}
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("peer read-open error=%v, want Windows sharing violation", err)
	}

	peer, err := os.OpenFile(replacementPath, os.O_WRONLY, 0)
	if peer != nil {
		_ = peer.Close()
	}
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("peer write-open error=%v, want Windows sharing violation", err)
	}
	if err := os.Rename(replacementPath, filepath.Join(dir, "peer-delete")); err == nil {
		t.Fatal("peer renamed replacement despite delete sharing being denied")
	}
	if _, err := os.Lstat(replacementPath); err != nil {
		t.Fatalf("replacement path disappeared after denied peer rename: %v", err)
	}
}

func TestWriteReplacementPreservesProtectedDACL(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		filePath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(
		filePath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION,
	)
	if err != nil {
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

	after, err := windows.GetNamedSecurityInfo(
		filePath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatalf("security descriptor changed:\n before: %s\n  after: %s", before.String(), after.String())
	}
}

func TestReplaceFileFailureRetainsCompleteRecoveryFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "AGENTS.md")
	displacedPath := filepath.Join(dir, "AGENTS.displaced.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()

	originalReplace := replaceAgentFile
	defer func() { replaceAgentFile = originalReplace }()
	replaceAgentFile = func(destination, _ *uint16) error {
		if err := os.Rename(windows.UTF16PtrToString(destination), displacedPath); err != nil {
			return err
		}
		return windows.ERROR_UNABLE_TO_MOVE_REPLACEMENT
	}

	err = locked.replace([]byte("complete replacement"))
	if err == nil || !strings.Contains(err.Error(), "retained") {
		t.Fatalf("replace error=%v, want retained recovery-file diagnostic", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".bv-replace-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("recovery replacements=%v, want exactly one", matches)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "complete replacement" {
		t.Fatalf("recovery content=%q, want complete replacement", content)
	}
	original, err := os.ReadFile(displacedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "original" {
		t.Fatalf("displaced original=%q", original)
	}
}

func TestReplaceFileFailureCleansTempWhenOriginalIsProvenIntact(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked, err := lockAgentFileForMutation(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.close()

	originalReplace := replaceAgentFile
	defer func() { replaceAgentFile = originalReplace }()
	replaceAgentFile = func(_, _ *uint16) error {
		return windows.ERROR_ACCESS_DENIED
	}

	if err := locked.replace([]byte("replacement")); err == nil {
		t.Fatal("expected injected ReplaceFileW error")
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".bv-replace-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("safe failure left temporary files: %v", matches)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("original changed after safe failure: %q", content)
	}
}
