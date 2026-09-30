//go:build linux && !android

package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	linuxFSImmutableFlag = 0x00000010
	linuxFSAppendFlag    = 0x00000020
	linuxFSVerityFlag    = 0x00100000
	// User-controlled regular-file flags that can survive an inode-replacing
	// save. Read-only filesystem state such as EXTENTS is deliberately retained
	// from the freshly created replacement; verity is rejected above because it
	// authenticates the source inode and cannot be transferred.
	linuxAgentCopyableFlags = 0x00000001 | // SECRM
		0x00000002 | // UNRM
		0x00000004 | // COMPR
		0x00000008 | // SYNC
		0x00000040 | // NODUMP
		0x00000080 | // NOATIME
		0x00000400 | // NOCOMP
		0x00004000 | // JOURNAL_DATA
		0x00008000 | // NOTAIL
		0x00800000 // NOCOW
)

type agentFileMetadataSnapshot struct {
	uid       uint32
	gid       uint32
	mode      uint32
	linkCount uint64
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
		linkCount: uint64(stat.Nlink),
		ctimeSec:  int64(stat.Ctim.Sec),
		ctimeNsec: int64(stat.Ctim.Nsec),
	}, nil
}

func sameAgentFileMetadata(a, b agentFileMetadataSnapshot) bool {
	return a == b
}

func createAgentReplacementFile(locked *lockedAgentFile) (*os.File, string, os.FileInfo, error) {
	file, err := os.CreateTemp(filepath.Dir(locked.path), ".bv-replace-*")
	if err != nil {
		return nil, "", nil, err
	}
	path := file.Name()
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		// No trusted handle identity means the path cannot be cleaned safely.
		return nil, "", nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		_ = file.Close()
		removeAgentReplacementIfSame(path, openedInfo)
		return nil, "", nil, err
	}
	if !pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
		_ = file.Close()
		removeAgentReplacementIfSame(path, openedInfo)
		return nil, "", nil, fmt.Errorf("replacement path changed during creation")
	}
	return file, path, openedInfo, nil
}

func copyAgentPlatformFileFlags(source, replacement *os.File) error {
	sourceFlags, err := unix.IoctlGetInt(int(source.Fd()), unix.FS_IOC_GETFLAGS)
	if linuxFileFlagsUnsupported(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read source inode flags: %w", err)
	}
	if sourceFlags&(linuxFSImmutableFlag|linuxFSAppendFlag|linuxFSVerityFlag) != 0 {
		// Atomic rename cannot replace an immutable/append-only source, and
		// applying either flag to the temp would also make safe cleanup fail.
		// Verity authenticates the original inode's contents and cannot be
		// transferred to a replacement inode with FS_IOC_SETFLAGS, so replacing
		// such a file would silently discard its integrity protection.
		return fmt.Errorf("refusing to replace immutable, append-only, or verity-protected agent file")
	}

	replacementFlags, err := unix.IoctlGetInt(int(replacement.Fd()), unix.FS_IOC_GETFLAGS)
	if err != nil {
		return fmt.Errorf("read replacement inode flags: %w", err)
	}
	desiredFlags := (replacementFlags &^ linuxAgentCopyableFlags) | (sourceFlags & linuxAgentCopyableFlags)
	if desiredFlags != replacementFlags {
		// Despite the UAPI ioctl encoding, FS_IOC_SETFLAGS takes an int pointer.
		// x/sys deliberately supplies an int32-sized object, which is required on
		// 64-bit big-endian Linux as well as the usual little-endian targets.
		if err := unix.IoctlSetPointerInt(int(replacement.Fd()), unix.FS_IOC_SETFLAGS, desiredFlags); err != nil {
			return fmt.Errorf("preserve inode flags: %w", err)
		}
	}
	actualFlags, err := unix.IoctlGetInt(int(replacement.Fd()), unix.FS_IOC_GETFLAGS)
	if err != nil {
		return fmt.Errorf("verify replacement inode flags: %w", err)
	}
	if actualFlags&linuxAgentCopyableFlags != sourceFlags&linuxAgentCopyableFlags {
		return fmt.Errorf("replacement inode flags differ from source")
	}
	return nil
}

func linuxFileFlagsUnsupported(err error) bool {
	return errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}

func makeAgentReplacementRemovable(_ *os.File) error {
	return nil
}

func publishAgentFileExclusive(sourcePath, destinationPath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, sourcePath, unix.AT_FDCWD, destinationPath, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("atomic no-replace rename is unsupported: %w", err)
	}
	return err
}
