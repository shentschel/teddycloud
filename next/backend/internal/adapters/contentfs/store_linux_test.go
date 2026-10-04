//go:build linux

package contentfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
	"golang.org/x/sys/unix"
)

// A missing qualified location is a REQUIRED failure, never a skip or a mock
// success. CI provides a fresh ext4 filesystem; local callers may set the
// deployment-controlled test parent after inspecting its actual mount.
func qualifiedRoot(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("TEDDYCLOUD_CONTENTFS_TEST_ROOT")
	if parent == "" {
		parent = "/var/tmp"
	}
	fd, err := walkRoot(parent)
	if err != nil {
		t.Fatalf("test parent walk failed: %v", err)
	}
	err = qualifyFilesystem(fd, syscalls{})
	_ = unix.Close(fd)
	if err != nil {
		t.Fatalf("required qualified Linux filesystem unavailable: %v", err)
	}
	root, err := os.MkdirTemp(parent, "tc-contentfs-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	return root
}

func fixture(t *testing.T, payload uint64) taffixture.Fixture {
	t.Helper()
	f, err := taffixture.New(payload, 17, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := qualifiedRoot(t)
	s, err := Open(context.Background(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, root
}
func publishFixture(s *Store, f taffixture.Fixture) (content.TAFEnvelope, error) {
	return s.Publish(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), f.Open(), content.FiniteTAFSource)
}
func blobPath(root string, f taffixture.Fixture) string {
	digest := content.NewBlobID(f.BlobDigest()).String()[7:]
	return filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf")
}
func countStages(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
func assertNoBlob(t *testing.T, root string, f taffixture.Fixture) {
	t.Helper()
	if _, err := os.Lstat(blobPath(root, f)); !os.IsNotExist(err) {
		t.Fatalf("unexpected public blob: %v", err)
	}
}
func allBytes(t *testing.T, f taffixture.Fixture) []byte {
	t.Helper()
	source := f.Open()
	defer source.Close()
	var out bytes.Buffer
	buffer := make([]byte, 8192)
	for {
		n, err := source.Read(context.Background(), buffer)
		out.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func TestBlobPublishAndReuse(t *testing.T) {
	s, root := newStore(t)
	f := fixture(t, 8193)
	for attempt := 0; attempt < 2; attempt++ {
		e, err := publishFixture(s, f)
		if err != nil {
			t.Fatal(err)
		}
		if e.BlobID() != content.NewBlobID(f.BlobDigest()) || e.CompleteBytes() != f.CompleteBytes() || e.Profile() != 1 {
			t.Fatal("wrong evidence")
		}
	}
	actual, err := os.ReadFile(blobPath(root, f))
	if err != nil || !bytes.Equal(actual, allBytes(t, f)) {
		t.Fatal("original bytes changed", err)
	}
	if countStages(t, root) != 1 || s.stageEntries != 1 || s.stageBytes != f.CompleteBytes() {
		t.Fatal("duplicate stage was not retained/charged")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(context.Background(), root, content.DefaultTAFOptions()); err != ErrRetained {
		if reopened != nil {
			reopened.Close()
		}
		t.Fatal("retained stages require B reconciliation", err)
	}
}

type inputSource struct {
	data       []byte
	chunk      int
	readError  error
	cancel     context.CancelFunc
	closeError error
	closed     bool
	block      chan struct{}
	started    chan struct{}
	once       sync.Once
}

func (s *inputSource) Read(ctx context.Context, b []byte) (int, error) {
	if s.block != nil {
		s.once.Do(func() { close(s.started) })
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-s.block:
		}
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if len(s.data) == 0 {
		if s.readError != nil {
			return 0, s.readError
		}
		return 0, io.EOF
	}
	n := len(b)
	if s.chunk > 0 && n > s.chunk {
		n = s.chunk
	}
	if n > len(s.data) {
		n = len(s.data)
	}
	copy(b, s.data[:n])
	s.data = s.data[n:]
	return n, nil
}
func (s *inputSource) Close() error { s.closed = true; return s.closeError }

func TestBlobStageFailures(t *testing.T) {
	f := fixture(t, 65537)
	raw := allBytes(t, f)
	cases := []struct {
		name       string
		makeSource func(context.CancelFunc) *inputSource
		expected   error
	}{
		{"malformed", func(context.CancelFunc) *inputSource {
			b := append([]byte(nil), raw...)
			b[0] = 1
			return &inputSource{data: b}
		}, ErrInvalid},
		{"short", func(context.CancelFunc) *inputSource { return &inputSource{data: raw[:5000]} }, ErrMismatch},
		{"input-error", func(context.CancelFunc) *inputSource {
			return &inputSource{data: raw[:5000], readError: errors.New("private/path/secret")}
		}, ErrUnavailable},
		{"cancel", func(c context.CancelFunc) *inputSource { return &inputSource{data: raw, cancel: c} }, ErrCanceled},
		{"close-error", func(context.CancelFunc) *inputSource {
			return &inputSource{data: raw, closeError: errors.New("private close secret")}
		}, ErrUnavailable},
		{"trailing", func(context.CancelFunc) *inputSource {
			return &inputSource{data: append(append([]byte(nil), raw...), 1)}
		}, ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, root := newStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			src := tc.makeSource(cancel)
			_, err := s.Publish(ctx, content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), src, content.FiniteTAFSource)
			if err != tc.expected || !src.closed {
				t.Fatal("wrong boundary", err, src.closed)
			}
			assertNoBlob(t, root, f)
			if countStages(t, root) != 1 || s.stageEntries != 1 {
				t.Fatal("stage lost")
			}
			if strings.Contains(err.Error(), "secret") || errors.Unwrap(err) != nil {
				t.Fatal("raw source diagnostics escaped")
			}
		})
	}
	for _, chunk := range []int{1, 4096, 65536} {
		t.Run("partial-read-"+strconv.Itoa(chunk), func(t *testing.T) {
			s, _ := newStore(t)
			src := &inputSource{data: raw, chunk: chunk}
			if _, err := s.Publish(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), src, content.FiniteTAFSource); err != nil {
				t.Fatal(err)
			}
		})
	}
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run("disk-full-chunk", func(t *testing.T) {
			s, root := newStore(t)
			calls := 0
			s.ops.fault = func(point string) error {
				if point == "stage-write" {
					calls++
					if calls == failAt {
						return unix.ENOSPC
					}
				}
				return nil
			}
			if _, err := publishFixture(s, f); err != ErrUnavailable {
				t.Fatal(err)
			}
			assertNoBlob(t, root, f)
			if countStages(t, root) != 1 || s.stageBytes > f.CompleteBytes() {
				t.Fatal("retained accounting")
			}
		})
	}
	t.Run("digest-mismatch", func(t *testing.T) {
		s, root := newStore(t)
		id := content.NewBlobID([32]byte{7})
		if _, err := s.Publish(context.Background(), id, f.CompleteBytes(), f.Open(), content.FiniteTAFSource); err != ErrMismatch {
			t.Fatal(err)
		}
		assertNoBlob(t, root, f)
	})
}

func TestBlobFlushBoundaries(t *testing.T) {
	for _, point := range []string{"stage-sync", "new-dir-sync", "parent-sync", "publish-sync", "staging-sync"} {
		t.Run(point, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 5000)
			s.ops.fault = func(p string) error {
				if p == point {
					return unix.EIO
				}
				return nil
			}
			_, err := publishFixture(s, f)
			if err == nil {
				t.Fatal("acknowledged unflushed blob")
			}
			if point == "publish-sync" || point == "staging-sync" {
				if err != ErrUncertain {
					t.Fatal(err)
				}
				if _, e := os.Stat(blobPath(root, f)); e != nil {
					t.Fatal("possible orphan missing", e)
				}
				s.ops.fault = nil
				if _, e := publishFixture(s, f); e != nil {
					t.Fatal("verified orphan retry", e)
				}
			} else {
				assertNoBlob(t, root, f)
				if countStages(t, root) != 1 {
					t.Fatal("stage discarded")
				}
			}
		})
	}
}

func TestBlobDescriptorConfinement(t *testing.T) {
	for _, kind := range []string{"symlink", "magic", "fifo", "hardlink", "mode"} {
		t.Run(kind, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 5000)
			path := blobPath(root, f)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Symlink(filepath.Join(root, "outside"), path); err != nil {
					t.Fatal(err)
				}
			case "magic":
				if err := os.Symlink("/proc/self/fd/0", path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				original := filepath.Join(root, "outside")
				if err := os.WriteFile(original, allBytes(t, f), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(original, path); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.WriteFile(path, allBytes(t, f), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := publishFixture(s, f); err != ErrCorrupt {
				t.Fatal("unsafe destination admitted", err)
			}
			if countStages(t, root) != 1 {
				t.Fatal("stage not preserved")
			}
		})
	}
	t.Run("root-component-symlink", func(t *testing.T) {
		root := qualifiedRoot(t)
		link := root + "-link"
		if err := os.Symlink(root, link); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(link)
		if s, err := Open(context.Background(), link, content.DefaultTAFOptions()); err != ErrUnsupported {
			if s != nil {
				s.Close()
			}
			t.Fatal(err)
		}
	})
	t.Run("shard-symlink", func(t *testing.T) {
		s, root := newStore(t)
		f := fixture(t, 5000)
		outside := filepath.Join(root, "outside")
		os.Mkdir(outside, 0700)
		os.Symlink(outside, filepath.Join(root, "blobs", "sha256"))
		if _, err := publishFixture(s, f); err != ErrUnavailable {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(outside)
		if len(entries) != 0 {
			t.Fatal("escaped shard")
		}
	})
	t.Run("retained-root-path-swap", func(t *testing.T) {
		s, root := newStore(t)
		old := root + "-retained"
		if err := os.Rename(root, old); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(old)
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		f := fixture(t, 5000)
		if _, err := publishFixture(s, f); err != nil {
			t.Fatal(err)
		}
		assertNoBlob(t, root, f)
		if _, err := os.Stat(blobPath(old, f)); err != nil {
			t.Fatal("lost retained root", err)
		}
	})
	t.Run("stage-entry-swap", func(t *testing.T) {
		s, root := newStore(t)
		f := fixture(t, 5000)
		swapped := false
		s.ops.fault = func(point string) error {
			if point == "stage-sync" && !swapped {
				swapped = true
				entries, err := os.ReadDir(filepath.Join(root, "staging"))
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "staging", entries[0].Name())
				if err := os.Rename(path, path+"-held"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/proc/self/fd/0", path); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		}
		if _, err := publishFixture(s, f); err != ErrUnavailable {
			t.Fatal(err)
		}
		assertNoBlob(t, root, f)
	})
}

func TestBlobExistingMismatch(t *testing.T) {
	for _, mutation := range []string{"size", "digest", "header"} {
		t.Run(mutation, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 5000)
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(err)
			}
			data := allBytes(t, f)
			switch mutation {
			case "size":
				data = data[:len(data)-1]
			case "digest":
				data[len(data)-1] ^= 1
			case "header":
				data[0] = 1
			}
			if err := os.WriteFile(blobPath(root, f), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := publishFixture(s, f); err != ErrCorrupt {
				t.Fatal(err)
			}
			actual, err := os.ReadFile(blobPath(root, f))
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatal("corrupt destination replaced", err)
			}
			if countStages(t, root) != 1 {
				t.Fatal("stage lost")
			}
		})
	}
}

func TestBlobLimitsAndAdmission(t *testing.T) {
	t.Run("exact-and-one-over", func(t *testing.T) {
		root := qualifiedRoot(t)
		opts, _ := content.NewTAFOptions(4097, time.Second)
		s, err := Open(context.Background(), root, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		f := fixture(t, 1)
		if _, err := publishFixture(s, f); err != nil {
			t.Fatal(err)
		}
		if _, err := publishFixture(s, fixture(t, 2)); err != ErrInvalid {
			t.Fatal(err)
		}
	})
	t.Run("retained-entry-cap", func(t *testing.T) {
		s, root := newStore(t)
		f := fixture(t, 1)
		if _, err := publishFixture(s, f); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < maxStageEntries; i++ {
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(i, err)
			}
		}
		if _, err := publishFixture(s, f); err != ErrCapacity {
			t.Fatal(err)
		}
		if countStages(t, root) != maxStageEntries {
			t.Fatal("over-cap creation")
		}
	})
	t.Run("retained-byte-reservation", func(t *testing.T) {
		s, _ := newStore(t)
		f := fixture(t, 1)
		s.stageBytes = maxStageBytes - f.CompleteBytes()
		if _, err := publishFixture(s, f); err != nil {
			t.Fatal(err)
		}
		s.stageBytes = maxStageBytes - f.CompleteBytes() + 1
		if _, err := publishFixture(s, f); err != ErrCapacity {
			t.Fatal(err)
		}
	})
	t.Run("busy-cancel-and-close-drain", func(t *testing.T) {
		s, root := newStore(t)
		f := fixture(t, 5000)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		src := &inputSource{data: allBytes(t, f), block: make(chan struct{}), started: make(chan struct{})}
		done := make(chan error, 1)
		go func() {
			_, err := s.Publish(ctx, content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), src, content.FiniteTAFSource)
			done <- err
		}()
		<-src.started
		if _, err := publishFixture(s, f); err != ErrBusy {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		select {
		case <-closed:
			t.Fatal("close released active publisher")
		case <-time.After(10 * time.Millisecond):
		}
		cancel()
		if err := <-done; err != ErrCanceled {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		assertNoBlob(t, root, f)
		if !src.closed {
			t.Fatal("source leaked")
		}
	})
	t.Run("operation-deadline", func(t *testing.T) {
		root := qualifiedRoot(t)
		opts, _ := content.NewTAFOptions(8192, 10*time.Millisecond)
		s, err := Open(context.Background(), root, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		f := fixture(t, 1)
		src := &inputSource{data: allBytes(t, f), block: make(chan struct{}), started: make(chan struct{})}
		if _, err := s.Publish(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), src, content.FiniteTAFSource); err != ErrCanceled {
			t.Fatal(err)
		}
		assertNoBlob(t, root, f)
	})
}

func TestBlobShortWritesAndRetainedAccounting(t *testing.T) {
	for _, kind := range []string{"short", "partial-error"} {
		t.Run(kind, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 5000)
			s.ops.write = func(fd int, b []byte) (int, error) {
				n, err := unix.Write(fd, b[:1])
				if kind == "partial-error" {
					return n, unix.ENOSPC
				}
				return n, err
			}
			if _, err := publishFixture(s, f); err != ErrUnavailable {
				t.Fatal(err)
			}
			assertNoBlob(t, root, f)
			if s.stageEntries != 1 || s.stageBytes != 1 {
				t.Fatal("partial disk write not charged", s.stageBytes)
			}
			entries, err := os.ReadDir(filepath.Join(root, "staging"))
			if err != nil {
				t.Fatal(err)
			}
			st, err := entries[0].Info()
			if err != nil || st.Size() != 1 {
				t.Fatal("partial file not retained", err)
			}
		})
	}
}

func TestBlobDeclaredReadCeiling(t *testing.T) {
	s, root := newStore(t)
	f := fixture(t, 65537)
	raw := allBytes(t, f)
	src := &inputSource{data: raw}
	const declared = uint64(4097)
	if _, err := s.Publish(context.Background(), content.NewBlobID(f.BlobDigest()), declared, src, content.FiniteTAFSource); err != ErrUnavailable {
		t.Fatal(err)
	}
	if consumed := len(raw) - len(src.data); uint64(consumed) > declared+1 {
		t.Fatal("read exceeded reservation plus EOF proof", consumed)
	}
	if s.stageBytes > declared || countStages(t, root) != 1 {
		t.Fatal("over-bound stage")
	}
	assertNoBlob(t, root, f)
}

func TestBlobCancellationPublicationBoundaries(t *testing.T) {
	for _, point := range []string{"stage-sync", "renameat2", "publish-sync", "staging-sync"} {
		t.Run(point, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 5000)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.ops.fault = func(p string) error {
				if p == point {
					cancel()
				}
				return nil
			}
			if _, err := s.Publish(ctx, content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), f.Open(), content.FiniteTAFSource); err != ErrCanceled {
				t.Fatal("success after cancellation", err)
			}
			if point == "stage-sync" {
				assertNoBlob(t, root, f)
			}
			// A syscall already entered may finish publication. The method must
			// finish synchronously and report cancellation, never spawn a publisher.
			if point != "stage-sync" {
				if _, err := os.Stat(blobPath(root, f)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestBlobStageNameRetryAndCloseout(t *testing.T) {
	s, root := newStore(t)
	f := fixture(t, 1)
	attempts := 0
	s.ops.fault = func(p string) error {
		if p == "openat2" {
			attempts++
			return unix.EEXIST
		}
		return nil
	}
	src := &inputSource{data: allBytes(t, f)}
	if _, err := s.Publish(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), src, content.FiniteTAFSource); err != ErrUnavailable {
		t.Fatal(err)
	}
	if attempts != 3 || !src.closed || s.stageEntries != 0 || s.stageBytes != 0 || countStages(t, root) != 0 {
		t.Fatal("unbounded retry or leaked reservation/source", attempts)
	}
	s.ops.fault = nil
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal("owner unusable after failed create", err)
	}
}

func TestBlobCloseFailurePoisonsAdmission(t *testing.T) {
	s, _ := newStore(t)
	f := fixture(t, 1)
	src := &inputSource{data: allBytes(t, f), closeError: errors.New("raw/private/identifier")}
	if _, err := s.Publish(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), src, content.FiniteTAFSource); err != ErrUnavailable {
		t.Fatal(err)
	}
	if _, err := publishFixture(s, f); err != ErrUnavailable {
		t.Fatal("failed closeout reused owner", err)
	}
}

func TestBlobDescriptorLeaksAndZeroStore(t *testing.T) {
	var zero Store
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
	root := qualifiedRoot(t)
	fdCount := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := fdCount()
	for i := 0; i < 10; i++ {
		s, err := Open(context.Background(), root, content.DefaultTAFOptions())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), root, content.DefaultTAFOptions()); err != ErrBusy {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if after := fdCount(); after > before {
		t.Fatal("descriptor leak", before, after)
	}
}
