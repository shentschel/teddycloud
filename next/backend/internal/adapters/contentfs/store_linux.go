//go:build linux

package contentfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"golang.org/x/sys/unix"
)

const confined = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV

// Per-owner failpoints are adapter-private; every capability still executes
// the real kernel probe. Tests inject failures, never successful capabilities.
type syscalls struct {
	fault func(string) error
	write func(int, []byte) (int, error)
}

func (o syscalls) check(point string) error {
	if o.fault != nil {
		return o.fault(point)
	}
	return nil
}
func (o syscalls) open(parent int, name string, flags int, mode uint32) (int, error) {
	if err := o.check("openat2"); err != nil {
		return -1, err
	}
	return unix.Openat2(parent, name, &unix.OpenHow{Flags: uint64(flags | unix.O_NOFOLLOW | unix.O_CLOEXEC), Mode: uint64(mode), Resolve: confined})
}
func (o syscalls) sync(fd int, point string) error {
	if err := o.check(point); err != nil {
		return err
	}
	return unix.Fsync(fd)
}
func (o syscalls) rename(from int, name string, to int, dest string) error {
	if err := o.check("renameat2"); err != nil {
		return err
	}
	return unix.Renameat2(from, name, to, dest, unix.RENAME_NOREPLACE)
}

type platformStore struct {
	mu                                     sync.Mutex
	root, staging, blobs, quarantine, lock int
	device                                 uint64
	options                                content.TAFOptions
	ops                                    syscalls
	initialized, closed, poisoned          bool
	stageEntries                           int
	stageBytes                             uint64
}

func openStore(ctx context.Context, root string, options content.TAFOptions) (*Store, error) {
	return openWithSyscalls(ctx, root, options, syscalls{})
}

func openWithSyscalls(ctx context.Context, root string, options content.TAFOptions, ops syscalls) (_ *Store, result error) {
	fd, err := walkRoot(root)
	if err != nil {
		return nil, ErrUnsupported
	}
	s := &Store{platformStore: platformStore{initialized: true, root: fd, staging: -1, blobs: -1, quarantine: -1, lock: -1, options: options, ops: ops}}
	defer func() {
		if result != nil {
			_ = s.close()
		}
	}()
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil {
		return nil, ErrUnsupported
	}
	s.device = uint64(stat.Dev)
	if err = s.checkDir(fd); err != nil {
		return nil, ErrUnsupported
	}
	if err = qualifyFilesystem(fd, ops); err != nil {
		return nil, ErrUnsupported
	}
	s.lock, err = ops.open(fd, ".owner.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, ErrUnsupported
	}
	if _, err = s.checkFile(s.lock, -1); err != nil {
		return nil, ErrUnsupported
	}
	if err = unix.Flock(s.lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, ErrBusy
	}
	if err = ops.sync(s.lock, "lock-sync"); err != nil {
		return nil, ErrUnsupported
	}
	if err = ops.sync(fd, "root-sync"); err != nil {
		return nil, ErrUnsupported
	}
	for _, entry := range []struct {
		name string
		fd   *int
	}{{"staging", &s.staging}, {"quarantine", &s.quarantine}, {"blobs", &s.blobs}} {
		*entry.fd, err = s.directory(fd, entry.name)
		if err != nil {
			return nil, ErrUnsupported
		}
	}
	// B2 owns incremental inventory/reconciliation. Until it exists, startup
	// with any retained stage/quarantine entry denies mutations, rather than
	// silently resetting capacity counters or scanning an unbounded tree.
	for _, dir := range []int{s.staging, s.quarantine} {
		if err = emptyDirectory(dir); err != nil {
			return nil, ErrRetained
		}
	}
	if err = s.probe(ctx); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ErrCanceled
	}
	return s, nil
}

// Walk from / without resolving symlinks. Mount transitions are permitted only
// while locating the configured root; all internal opens forbid them.
func walkRoot(root string) (int, error) {
	if !filepath.IsAbs(root) || root == "/" || filepath.Clean(root) != root || strings.IndexByte(root, 0) >= 0 {
		return -1, ErrInvalid
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, component := range strings.Split(root[1:], "/") {
		next, e := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if e != nil {
			return -1, e
		}
		fd = next
	}
	return fd, nil
}

func (s *platformStore) checkDir(fd int) error {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || uint64(st.Dev) != s.device || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return ErrUnsupported
	}
	flags, err := unix.IoctlGetInt(fd, unix.FS_IOC_GETFLAGS)
	if err != nil || flags&0x40000000 != 0 {
		return ErrUnsupported
	} // FS_CASEFOLD_FL
	return nil
}
func (s *platformStore) checkFile(fd int, size int64) (unix.Stat_t, error) {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || uint64(st.Dev) != s.device || st.Mode&07777 != 0600 || size >= 0 && st.Size != size {
		return st, ErrCorrupt
	}
	return st, nil
}
func (s *platformStore) directory(parent int, name string) (int, error) {
	fd, err := s.ops.open(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err == unix.ENOENT {
		if err = s.ops.check("mkdir"); err != nil {
			return -1, err
		}
		if err = unix.Mkdirat(parent, name, 0700); err != nil {
			if err != unix.EEXIST {
				return -1, err
			}
		}
		fd, err = s.ops.open(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return -1, err
		}
	}
	if err != nil {
		return -1, err
	}
	if err = s.checkDir(fd); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	// Synchronize existing directories too: a previous failed mkdir/flush may
	// have left a visible but not yet durable shard. Retrying must repair that
	// durability boundary before admitting publication.
	if err = s.ops.sync(fd, "new-dir-sync"); err == nil {
		err = s.ops.sync(parent, "parent-sync")
	}
	if err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func emptyDirectory(fd int) error {
	// Open a new file description so inspection doesn't change retained offsets.
	dup, err := unix.Openat2(fd, ".", &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: confined})
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(dup), "directory")
	_, err = f.Readdirnames(1)
	closeErr := f.Close()
	if err == io.EOF && closeErr == nil {
		return nil
	}
	return ErrRetained
}

func (s *platformStore) newStage() (int, string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var entropy [16]byte
		if err := s.ops.check("random"); err != nil {
			return -1, "", err
		}
		if _, err := rand.Read(entropy[:]); err != nil {
			return -1, "", err
		}
		name := "s-" + hex.EncodeToString(entropy[:]) + ".taf"
		fd, err := s.ops.open(s.staging, name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR, 0600)
		if err == unix.EEXIST {
			continue
		}
		return fd, name, err
	}
	return -1, "", ErrUnavailable
}

func (s *platformStore) publish(ctx context.Context, id content.BlobID, size uint64, source content.TAFSource, mode content.TAFSourceMode) (result content.TAFEnvelope, failure error) {
	if size < content.MinTAFBytes || size > s.options.MaxBytes() || mode != content.FiniteTAFSource {
		return result, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.options.Duration())
	defer cancel()
	defer func() {
		if failure == nil && ctx.Err() != nil {
			result = content.TAFEnvelope{}
			failure = ErrCanceled
		}
	}()
	if ctx.Err() != nil {
		return result, ErrCanceled
	}
	if !s.mu.TryLock() {
		return result, ErrBusy
	}
	defer s.mu.Unlock()
	if !s.initialized || s.closed || s.poisoned {
		return result, ErrUnavailable
	}
	if s.stageEntries >= maxStageEntries || size > maxStageBytes-s.stageBytes {
		return result, ErrCapacity
	}
	closedSource := false
	defer func() {
		if !closedSource {
			if err := source.Close(); err != nil {
				s.poisoned = true
				failure = ErrUnavailable
				result = content.TAFEnvelope{}
			}
		}
	}()
	// Reserve before file creation. Failed stages are retained; successful
	// publication consumes its reservation. No deletion compensation exists.
	s.stageEntries++
	s.stageBytes += size
	fd, name, err := s.newStage()
	if err != nil {
		s.stageEntries--
		s.stageBytes -= size
		return result, ErrUnavailable
	}
	stage := os.NewFile(uintptr(fd), "stage")
	bytesWritten := uint64(0)
	moved := false
	defer func() {
		if moved {
			s.stageEntries--
			s.stageBytes -= size
		} else {
			s.stageBytes -= size - bytesWritten
		}
		if err := stage.Close(); err != nil {
			s.poisoned = true
			result = content.TAFEnvelope{}
			failure = ErrUnavailable
		}
	}()
	if _, err = s.checkFile(fd, 0); err != nil {
		return result, ErrUnavailable
	}
	tee := &stageSource{source: source, file: stage, max: size, ops: s.ops, written: &bytesWritten}
	result, err = content.ValidateTAF(ctx, tee, mode, s.options)
	if err != nil {
		return content.TAFEnvelope{}, validationError(err)
	}
	if result.BlobID() != id || result.CompleteBytes() != size {
		return content.TAFEnvelope{}, ErrMismatch
	}
	if err = source.Close(); err != nil {
		closedSource = true
		s.poisoned = true
		return content.TAFEnvelope{}, ErrUnavailable
	}
	closedSource = true
	if ctx.Err() != nil {
		return content.TAFEnvelope{}, ErrCanceled
	}
	if _, err = s.checkFile(fd, int64(size)); err != nil {
		return content.TAFEnvelope{}, ErrUnavailable
	}
	if err = s.ops.sync(fd, "stage-sync"); err != nil {
		return content.TAFEnvelope{}, ErrUnavailable
	}
	shard, dest, err := s.shard(id)
	if err != nil {
		return content.TAFEnvelope{}, ErrUnavailable
	}
	defer func() {
		if unix.Close(shard) != nil {
			s.poisoned = true
			result = content.TAFEnvelope{}
			failure = ErrUnavailable
		}
	}()
	if ctx.Err() != nil {
		return content.TAFEnvelope{}, ErrCanceled
	}
	// Check the stage directory entry still refers to our open descriptor.
	if !sameEntry(s.staging, name, fd) {
		return content.TAFEnvelope{}, ErrUnavailable
	}
	err = s.ops.rename(s.staging, name, shard, dest)
	if err == unix.EEXIST {
		if err = s.verify(ctx, shard, dest, id, size); err != nil {
			return content.TAFEnvelope{}, err
		}
	} else if err != nil {
		return content.TAFEnvelope{}, ErrUnavailable
	} else {
		moved = true
	}
	if err = s.ops.sync(shard, "publish-sync"); err != nil {
		return content.TAFEnvelope{}, ErrUncertain
	}
	if err = s.ops.sync(s.staging, "staging-sync"); err != nil {
		return content.TAFEnvelope{}, ErrUncertain
	}
	if ctx.Err() != nil {
		return content.TAFEnvelope{}, ErrCanceled
	}
	if moved && !sameEntry(shard, dest, fd) {
		return content.TAFEnvelope{}, ErrUncertain
	}
	return result, nil
}

func (s *platformStore) shard(id content.BlobID) (int, string, error) {
	digest := id.String()[7:]
	alg, err := s.directory(s.blobs, "sha256")
	if err != nil {
		return -1, "", err
	}
	defer unix.Close(alg)
	a, err := s.directory(alg, digest[:2])
	if err != nil {
		return -1, "", err
	}
	defer unix.Close(a)
	b, err := s.directory(a, digest[2:4])
	return b, digest + ".taf", err
}

func (s *platformStore) verify(ctx context.Context, parent int, name string, id content.BlobID, size uint64) (failure error) {
	fd, err := s.ops.open(parent, name, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrCorrupt
	}
	f := os.NewFile(uintptr(fd), "blob")
	defer func() {
		if f.Close() != nil {
			s.poisoned = true
			failure = ErrUnavailable
		}
	}()
	before, err := s.checkFile(fd, int64(size))
	if err != nil {
		return ErrCorrupt
	}
	e, err := content.ValidateTAF(ctx, &fileSource{f}, content.FiniteTAFSource, s.options)
	if err != nil {
		if errors.Is(err, content.ErrTAFCanceled) {
			return ErrCanceled
		}
		if errors.Is(err, content.ErrTAFIO) {
			return ErrUnavailable
		}
		return ErrCorrupt
	}
	after, err := s.checkFile(fd, int64(size))
	// Reading may update atime. All identity/content mutation fields must stay
	// stable; do not mistake an ordinary access timestamp for corruption.
	before.Atim = after.Atim
	if err != nil || before != after || e.BlobID() != id || e.Profile() != content.TAFProfileV1 || e.CompleteBytes() != size || !sameEntry(parent, name, fd) {
		return ErrCorrupt
	}
	if err = s.ops.sync(fd, "existing-sync"); err != nil {
		return ErrUncertain
	}
	if ctx.Err() != nil {
		return ErrCanceled
	}
	return nil
}

func sameEntry(parent int, name string, fd int) bool {
	var a, b unix.Stat_t
	return unix.Fstat(fd, &a) == nil && unix.Fstatat(parent, name, &b, unix.AT_SYMLINK_NOFOLLOW) == nil && a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Nlink == b.Nlink
}

type stageSource struct {
	source  content.TAFSource
	file    *os.File
	max     uint64
	ops     syscalls
	written *uint64
}

func (r *stageSource) Read(ctx context.Context, b []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ErrCanceled
	}
	// Even a malicious header cannot make the source consume more than the
	// declared reservation plus the single EOF-proof byte. Never store that
	// extra byte and never allocate a whole input buffer.
	remaining := r.max - *r.written
	input := b
	if uint64(len(input)) > remaining+1 {
		input = input[:int(remaining+1)]
	}
	n, err := r.source.Read(ctx, input)
	if ctx.Err() != nil {
		return 0, ErrCanceled
	}
	if n < 0 || n > len(input) {
		return 0, ErrUnavailable
	}
	if uint64(n) > r.max-*r.written {
		return 0, ErrMismatch
	}
	if n > 0 {
		if e := r.ops.check("stage-write"); e != nil {
			return 0, ErrUnavailable
		}
		write := r.ops.write
		if write == nil {
			write = unix.Write
		}
		written, e := write(int(r.file.Fd()), b[:n])
		if written < 0 || written > n {
			return 0, ErrUnavailable
		}
		*r.written += uint64(written)
		if e != nil || written != n {
			return 0, ErrUnavailable
		}
	}
	return n, err
}
func (*stageSource) Close() error { return nil }

type fileSource struct{ file *os.File }

func (r *fileSource) Read(ctx context.Context, b []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return r.file.Read(b)
}
func (r *fileSource) Close() error { return r.file.Close() }
func validationError(err error) error {
	switch {
	case errors.Is(err, content.ErrTAFCanceled):
		return ErrCanceled
	case errors.Is(err, content.ErrTAFIO):
		return ErrUnavailable
	case errors.Is(err, content.ErrTAFLimit):
		return ErrCapacity
	case errors.Is(err, content.ErrUnsupportedTAF):
		return ErrInvalid
	default:
		return ErrMismatch
	}
}

func (s *platformStore) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if !s.initialized {
		s.closed = true
		return nil
	}
	s.closed = true
	var failed bool
	// Reverse ownership order; releasing the lock is the final operation.
	for _, ptr := range []*int{&s.blobs, &s.quarantine, &s.staging, &s.root, &s.lock} {
		if *ptr >= 0 {
			if unix.Close(*ptr) != nil {
				failed = true
			}
			*ptr = -1
		}
	}
	if failed {
		return ErrUnavailable
	}
	return nil
}
