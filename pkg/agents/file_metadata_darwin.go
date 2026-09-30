//go:build darwin

package agents

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinAgentFileACLMaxBytes = 1 << 20
	darwinFileSecMagic         = 0x012cc16d
	darwinFileSecNoACL         = ^uint32(0)
	darwinFileSecACLHeaderSize = 44 // magic + owner GUID + group GUID + ACL count/flags
	darwinFileSecACESize       = 24 // applicable GUID + flags + rights
	darwinFileSecMaxACECount   = 128
	// Only user-controlled presentation/backup flags are semantically portable
	// to rewritten bytes. Compression, datavault, restricted, nounlink, tracked,
	// dataless, archived, and other kernel-managed flags are rejected.
	darwinAgentCopyableFlags  = unix.UF_NODUMP | unix.UF_HIDDEN
	darwinAgentImmutableFlags = unix.UF_IMMUTABLE | unix.UF_APPEND | unix.SF_IMMUTABLE | unix.SF_APPEND
)

type agentFileMetadataSnapshot struct {
	uid       uint32
	gid       uint32
	mode      uint16
	flags     uint32
	linkCount uint16
	ctimeSec  int64
	ctimeNsec int64
}

func snapshotAgentFileMetadata(file *os.File) (agentFileMetadataSnapshot, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return agentFileMetadataSnapshot{}, err
	}
	return agentFileMetadataSnapshot{
		uid:       stat.Uid,
		gid:       stat.Gid,
		mode:      stat.Mode,
		flags:     stat.Flags,
		linkCount: stat.Nlink,
		ctimeSec:  stat.Ctim.Sec,
		ctimeNsec: stat.Ctim.Nsec,
	}, nil
}

func sameAgentFileMetadata(a, b agentFileMetadataSnapshot) bool {
	return a == b
}

// createAgentReplacementFile creates an empty, mode-000 candidate and verifies
// through open descriptors that its Darwin ACL is byte-for-byte identical to
// the source before callers write any replacement bytes. clonefile cannot be
// used safely here: it publishes source bytes and may add inherited ACL entries
// before the caller can inspect the clone. If a source ACL cannot be reproduced
// by secure same-directory creation, replacement fails closed.
func createAgentReplacementFile(locked *lockedAgentFile) (*os.File, string, os.FileInfo, error) {
	if locked.metadata.flags&darwinAgentImmutableFlags != 0 {
		return nil, "", nil, fmt.Errorf("refusing to replace immutable or append-only Darwin agent file")
	}
	if unsupported := locked.metadata.flags &^ darwinAgentCopyableFlags; unsupported != 0 {
		// Other unsupported flags describe content managed by APFS or platform
		// security policy. Reject them before creating a candidate.
		return nil, "", nil, fmt.Errorf("refusing to replace agent file with unsupported Darwin flags %#x", unsupported)
	}
	dir := filepath.Dir(locked.path)
	for attempt := 0; attempt < 64; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", nil, fmt.Errorf("generate replacement name: %w", err)
		}
		path := filepath.Join(dir, ".bv-replace-"+hex.EncodeToString(random[:]))
		fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, "", nil, fmt.Errorf("create secure replacement: %w", err)
		}
		file := os.NewFile(uintptr(fd), path)
		openedInfo, err := file.Stat()
		if err != nil {
			// Without a handle-derived identity, pathname cleanup could delete a
			// peer replacement. Leave this empty mode-000 artifact for recovery.
			_ = file.Close()
			return nil, "", nil, fmt.Errorf("stat secure replacement: %w", err)
		}
		cleanup := func() {
			_ = file.Close()
			removeAgentReplacementIfSame(path, openedInfo)
		}
		pathInfo, err := os.Lstat(path)
		if err != nil {
			cleanup()
			return nil, "", nil, fmt.Errorf("stat secure replacement path: %w", err)
		}
		if !pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
			cleanup()
			return nil, "", nil, fmt.Errorf("secure replacement path changed during creation")
		}

		sourceACL, err := darwinAgentFileACL(locked.file)
		if err != nil {
			cleanup()
			return nil, "", nil, fmt.Errorf("read source ACL before replacement: %w", err)
		}
		replacementACL, err := darwinAgentFileACL(file)
		if err != nil {
			cleanup()
			return nil, "", nil, fmt.Errorf("read secure replacement ACL: %w", err)
		}
		if !bytes.Equal(sourceACL, replacementACL) {
			cleanup()
			return nil, "", nil, fmt.Errorf("replacement ACL differs from source; refusing to expose bytes under a different policy")
		}
		return file, path, openedInfo, nil
	}
	return nil, "", nil, fmt.Errorf("could not allocate a unique replacement name")
}

func copyAgentPlatformFileFlags(source, replacement *os.File) error {
	var sourceStat unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &sourceStat); err != nil {
		return fmt.Errorf("stat source file flags: %w", err)
	}
	if unsupported := sourceStat.Flags &^ darwinAgentCopyableFlags; unsupported != 0 {
		return fmt.Errorf("refusing to preserve unsupported Darwin flags %#x", unsupported)
	}
	if err := unix.Fchflags(int(replacement.Fd()), int(sourceStat.Flags&darwinAgentCopyableFlags)); err != nil {
		return fmt.Errorf("preserve file flags: %w", err)
	}
	var replacementStat unix.Stat_t
	if err := unix.Fstat(int(replacement.Fd()), &replacementStat); err != nil {
		return fmt.Errorf("verify replacement file flags: %w", err)
	}
	if replacementStat.Flags != sourceStat.Flags&darwinAgentCopyableFlags {
		return fmt.Errorf("replacement Darwin flags %#x differ from source transferable flags %#x", replacementStat.Flags, sourceStat.Flags&darwinAgentCopyableFlags)
	}

	// Recheck the ACL after every other metadata mutation. Committing a broader
	// or otherwise different ACL would be a silent security-policy mutation.
	sourceACL, err := darwinAgentFileACL(source)
	if err != nil {
		return fmt.Errorf("read source ACL: %w", err)
	}
	replacementACL, err := darwinAgentFileACL(replacement)
	if err != nil {
		return fmt.Errorf("read replacement ACL: %w", err)
	}
	if !bytes.Equal(sourceACL, replacementACL) {
		return fmt.Errorf("replacement ACL differs from source after clone inheritance")
	}
	return nil
}

func publishAgentFileExclusive(sourcePath, destinationPath string) error {
	return unix.RenamexNp(sourcePath, destinationPath, unix.RENAME_EXCL)
}

// darwinAgentFileACL returns the ATTR_CMN_EXTENDED_SECURITY payload for an
// open file descriptor. The attribute is variable-length, so the first call
// asks the kernel for the full size and the bounded retries handle metadata
// growth between calls. Returning just the referenced payload avoids comparing
// buffer offsets or size bookkeeping that are not part of the ACL itself.
func darwinAgentFileACL(file *os.File) ([]byte, error) {
	if file == nil {
		return nil, fmt.Errorf("ACL file handle is nil")
	}
	attrList := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr:  unix.ATTR_CMN_EXTENDED_SECURITY,
	}
	buffer := make([]byte, 12) // uint32 length followed by attrreference_t
	for attempt := 0; attempt < 3; attempt++ {
		if err := darwinFgetattrlist(file, &attrList, buffer); err != nil {
			return nil, err
		}
		reported := int(binary.LittleEndian.Uint32(buffer[:4]))
		if reported < 4 {
			return nil, fmt.Errorf("invalid extended-security buffer length %d", reported)
		}
		if reported > darwinAgentFileACLMaxBytes {
			return nil, fmt.Errorf("extended-security buffer length %d exceeds %d-byte limit", reported, darwinAgentFileACLMaxBytes)
		}
		if reported > len(buffer) {
			buffer = make([]byte, reported)
			continue
		}
		buffer = buffer[:reported]
		if len(buffer) == 4 {
			// The filesystem did not return the requested attribute.
			return nil, nil
		}
		if len(buffer) < 12 {
			return nil, fmt.Errorf("truncated extended-security attribute reference")
		}

		dataOffset := int64(int32(binary.LittleEndian.Uint32(buffer[4:8])))
		dataLength := int64(binary.LittleEndian.Uint32(buffer[8:12]))
		if dataLength == 0 {
			return []byte{}, nil
		}
		dataStart := int64(4) + dataOffset
		dataEnd := dataStart + dataLength
		if dataOffset < 8 || dataStart < 12 || dataEnd < dataStart || dataEnd > int64(len(buffer)) {
			return nil, fmt.Errorf("invalid extended-security attribute reference offset=%d length=%d buffer=%d", dataOffset, dataLength, len(buffer))
		}
		fileSecurity := buffer[dataStart:dataEnd]
		if len(fileSecurity) < darwinFileSecACLHeaderSize {
			return nil, fmt.Errorf("extended-security payload is only %d bytes", len(fileSecurity))
		}
		if magic := binary.LittleEndian.Uint32(fileSecurity[:4]); magic != darwinFileSecMagic {
			return nil, fmt.Errorf("extended-security payload has magic %#x, want %#x", magic, darwinFileSecMagic)
		}
		entryCount := binary.LittleEndian.Uint32(fileSecurity[36:40])
		expectedSize := darwinFileSecACLHeaderSize
		if entryCount != darwinFileSecNoACL {
			if entryCount > darwinFileSecMaxACECount {
				return nil, fmt.Errorf("extended-security ACL has %d entries, maximum is %d", entryCount, darwinFileSecMaxACECount)
			}
			expectedSize += int(entryCount) * darwinFileSecACESize
		}
		if len(fileSecurity) != expectedSize {
			return nil, fmt.Errorf("extended-security ACL size is %d, want %d for %d entries", len(fileSecurity), expectedSize, entryCount)
		}
		// Owner and group are preserved separately. Compare only the ACL count,
		// flags, and ACE bytes so CLONE_NOOWNERCOPY cannot create a false mismatch.
		return bytes.Clone(fileSecurity[36:]), nil
	}
	return nil, fmt.Errorf("extended-security metadata changed while it was being read")
}

func darwinFgetattrlist(file *os.File, attrList *unix.Attrlist, buffer []byte) error {
	if len(buffer) < 4 {
		return fmt.Errorf("extended-security buffer is too small")
	}
	_, _, errno := unix.Syscall6(
		unix.SYS_FGETATTRLIST,
		file.Fd(),
		uintptr(unsafe.Pointer(attrList)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unix.FSOPT_REPORT_FULLSIZE),
		0,
	)
	runtime.KeepAlive(file)
	runtime.KeepAlive(attrList)
	runtime.KeepAlive(buffer)
	if errno != 0 {
		return errno
	}
	return nil
}

func makeAgentReplacementRemovable(file *os.File) error {
	if file == nil {
		return nil
	}
	return unix.Fchflags(int(file.Fd()), 0)
}
