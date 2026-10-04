//go:build linux

package contentfs

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// The ext-family magic alone cannot distinguish ext2/ext3 from ext4. Match
// the descriptor's statx mount ID to its exact type in bounded mountinfo too.
func qualifyFilesystem(fd int, ops syscalls) error {
	if err := ops.check("filesystem"); err != nil {
		return err
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) != nil || (fs.Type != unix.EXT4_SUPER_MAGIC && fs.Type != unix.XFS_SUPER_MAGIC) {
		return ErrUnsupported
	}
	var sx unix.Statx_t
	if unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &sx) != nil || sx.Mask&unix.STATX_MNT_ID == 0 {
		return ErrUnsupported
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return ErrUnsupported
	}
	defer f.Close()
	const bound = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(f, bound+1))
	if err != nil || len(data) > bound {
		return ErrUnsupported
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), bound)
	found := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			return ErrUnsupported
		}
		mountID, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return ErrUnsupported
		}
		if mountID != sx.Mnt_id {
			continue
		}
		if found {
			return ErrUnsupported
		}
		found = true
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || separator+3 >= len(fields) {
			return ErrUnsupported
		}
		typeName := fields[separator+1]
		if !(typeName == "ext4" && fs.Type == unix.EXT4_SUPER_MAGIC || typeName == "xfs" && fs.Type == unix.XFS_SUPER_MAGIC) {
			return ErrUnsupported
		}
	}
	if scanner.Err() != nil || !found {
		return ErrUnsupported
	}
	return nil
}

type probeEntry struct {
	name string
	stat unix.Stat_t
}

// Scratch objects are retained on any interrupted/failed probe. Only a fully
// successful probe may remove its own recorded inode identities. No stage or
// blob is a cleanup target. This establishes syscall behavior, not power-loss
// or physical hardware durability.
func (s *platformStore) probe(ctx context.Context) error {
	if ctx.Err() != nil {
		return ErrCanceled
	}
	if err := s.ops.check("probe-case"); err != nil {
		return ErrUnsupported
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrUnsupported
	}
	base := hex.EncodeToString(random[:])
	a, b, target, link := "p-"+base, "P-"+base, "d-"+base, "l-"+base
	entries := make([]probeEntry, 0, 3)
	create := func(name string, value byte) (unix.Stat_t, error) {
		fd, err := s.ops.open(s.staging, name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR, 0600)
		if err != nil {
			return unix.Stat_t{}, err
		}
		defer unix.Close(fd)
		if n, err := unix.Write(fd, []byte{value}); err != nil || n != 1 {
			return unix.Stat_t{}, ErrUnsupported
		}
		if err := s.ops.sync(fd, "probe-file-sync"); err != nil {
			return unix.Stat_t{}, err
		}
		return s.checkFile(fd, 1)
	}
	statA, err := create(a, 17)
	if err != nil {
		return ErrUnsupported
	}
	statB, err := create(b, 31)
	if err != nil || statA.Ino == statB.Ino {
		return ErrUnsupported
	}
	if err = s.ops.rename(s.staging, a, s.staging, target); err != nil {
		return ErrUnsupported
	}
	entries = append(entries, probeEntry{target, statA}, probeEntry{b, statB})
	if err = s.ops.rename(s.staging, b, s.staging, target); err != unix.EEXIST {
		return ErrUnsupported
	}
	for _, item := range []struct {
		name  string
		value byte
		stat  unix.Stat_t
	}{{target, 17, statA}, {b, 31, statB}} {
		fd, e := s.ops.open(s.staging, item.name, unix.O_RDONLY|unix.O_NONBLOCK, 0)
		if e != nil {
			return ErrUnsupported
		}
		st, e := s.checkFile(fd, 1)
		var bytes [2]byte
		n, readErr := unix.Read(fd, bytes[:])
		closeErr := unix.Close(fd)
		if e != nil || st.Ino != item.stat.Ino || n != 1 || bytes[0] != item.value || readErr != nil || closeErr != nil {
			return ErrUnsupported
		}
	}
	if err = unix.Symlinkat(target, s.staging, link); err != nil {
		return ErrUnsupported
	}
	var linkStat unix.Stat_t
	if unix.Fstatat(s.staging, link, &linkStat, unix.AT_SYMLINK_NOFOLLOW) != nil || linkStat.Mode&unix.S_IFMT != unix.S_IFLNK {
		return ErrUnsupported
	}
	entries = append(entries, probeEntry{link, linkStat})
	fd, err := s.ops.open(s.staging, link, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if fd >= 0 {
		_ = unix.Close(fd)
	}
	if err != unix.ELOOP {
		return ErrUnsupported
	}
	for _, fd := range []int{s.root, s.staging, s.blobs, s.quarantine} {
		if err = s.ops.sync(fd, "probe-dir-sync"); err != nil {
			return ErrUnsupported
		}
	}
	if ctx.Err() != nil {
		return ErrCanceled
	}
	// Check every identity before beginning successful-probe cleanup. Trusted
	// root ownership excludes malicious concurrent same-owner replacement.
	for _, entry := range entries {
		var st unix.Stat_t
		if unix.Fstatat(s.staging, entry.name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Dev != entry.stat.Dev || st.Ino != entry.stat.Ino || st.Mode != entry.stat.Mode || st.Nlink != 1 {
			return ErrUnsupported
		}
	}
	for _, entry := range entries {
		if unix.Unlinkat(s.staging, entry.name, 0) != nil {
			return ErrUnsupported
		}
	}
	if err = s.ops.sync(s.staging, "probe-cleanup-sync"); err != nil {
		return ErrUnsupported
	}
	return nil
}
