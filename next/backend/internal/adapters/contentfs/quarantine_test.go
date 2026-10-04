//go:build linux

package contentfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"golang.org/x/sys/unix"
)

func corruptFixture(t *testing.T, s *Store, root string) (content.BlobID, uint64, []byte, string) {
	t.Helper()
	f := fixture(t, 10)
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	raw := allBytes(t, f)
	raw[len(raw)-1] ^= 1
	path := blobPath(root, f)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), raw, path
}

func TestBlobQuarantineFreshVerificationAndRetention(t *testing.T) {
	s, root := newStore(t)
	f := fixture(t, 10)
	id := content.NewBlobID(f.BlobDigest())
	if err := s.Quarantine(context.Background(), id, f.CompleteBytes()); err != ErrMissing {
		t.Fatal(err)
	}
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	if err := s.Quarantine(context.Background(), id, f.CompleteBytes()); err != ErrInvalid {
		t.Fatal("healthy moved", err)
	}
	if err := s.Quarantine(context.Background(), id, f.CompleteBytes()+1); err != ErrInvalid {
		t.Fatal("incorrect declaration moved healthy content", err)
	}
	raw := allBytes(t, f)
	raw[len(raw)-1] ^= 1
	path := blobPath(root, f)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	page, err := s.Inventory(context.Background(), "", 1, noReferences)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Quarantine(context.Background(), id, f.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Inventory(context.Background(), page.Cursor, 1, noReferences); err != ErrCursor {
		t.Fatal("quarantine cursor survived", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("source still present", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "quarantine"))
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	if !generatedName(entries[0].Name(), "q-") {
		t.Fatal("wrong generated name")
	}
	actual, err := os.ReadFile(filepath.Join(root, "quarantine", entries[0].Name()))
	if err != nil || !bytes.Equal(raw, actual) {
		t.Fatal("bytes not retained", err)
	}
	if s.quarantineEntries != 1 || s.quarantineBytes != uint64(len(raw)) {
		t.Fatal("wrong capacity")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenForInventory(context.Background(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	collectInventory(t, reopened, noReferences)
	if reopened.quarantineEntries != 1 || reopened.quarantineBytes != uint64(len(raw)) {
		t.Fatal("restart lost retained accounting")
	}
}

func TestBlobQuarantineNoReplaceAndCaps(t *testing.T) {
	for _, kind := range []string{"entries", "bytes", "collision"} {
		t.Run(kind, func(t *testing.T) {
			s, root := newStore(t)
			id, size, raw, path := corruptFixture(t, s, root)
			if kind == "entries" {
				s.quarantineEntries = maxQuarantineEntries
			}
			if kind == "bytes" {
				s.quarantineBytes = maxQuarantineBytes
			}
			if kind != "collision" {
				if err := s.Quarantine(context.Background(), id, size); err != ErrCapacity {
					t.Fatal(err)
				}
				actual, _ := os.ReadFile(path)
				if !bytes.Equal(actual, raw) {
					t.Fatal("full quarantine moved source")
				}
				return
			}
			collisions := 0
			s.ops.fault = func(point string) error {
				if point == "renameat2" {
					collisions++
					return unix.EEXIST
				}
				return nil
			}
			if err := s.Quarantine(context.Background(), id, size); err != ErrUnavailable || collisions != 3 {
				t.Fatal(collisions, err)
			}
			actual, _ := os.ReadFile(path)
			if !bytes.Equal(actual, raw) {
				t.Fatal("collision replaced source")
			}
			// Exercise the actual primitive independently of the injected collision.
			writeRetained(t, root, "quarantine", "q-", 1, []byte("old"))
			writeRetained(t, root, "quarantine", "q-", 2, []byte("new"))
			s.ops.fault = nil
			if err := s.ops.rename(s.quarantine, "q-00000000000000000000000000000002.taf", s.quarantine, "q-00000000000000000000000000000001.taf"); err != unix.EEXIST {
				t.Fatal("no-replace capability", err)
			}
			old, err := os.ReadFile(filepath.Join(root, "quarantine", "q-00000000000000000000000000000001.taf"))
			if err != nil || string(old) != "old" {
				t.Fatal("no-replace changed destination", err)
			}
		})
	}
}

func TestBlobQuarantineActiveRangeAndCancellation(t *testing.T) {
	s, _ := newStore(t)
	f := fixture(t, 10)
	id := content.NewBlobID(f.BlobDigest())
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Quarantine(ctx, id, f.CompleteBytes()); err != ErrCanceled {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	sink := &rangeSink{write: func(ctx context.Context, b []byte) (int, error) { close(started); <-release; return len(b), nil }}
	r := requested(t, 0, 1)
	go func() { done <- s.ReadRange(context.Background(), id, f.CompleteBytes(), r, sink) }()
	<-started
	if err := s.Quarantine(context.Background(), id, f.CompleteBytes()); err != ErrBusy {
		t.Fatal("active range not fenced", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBlobQuarantineFlushFailuresAndFreshRefusal(t *testing.T) {
	for _, point := range []string{"quarantine-source-sync", "quarantine-dest-sync", "quarantine-verified"} {
		t.Run(point, func(t *testing.T) {
			s, root := newStore(t)
			id, size, raw, path := corruptFixture(t, s, root)
			seen := map[string]bool{}
			s.ops.fault = func(p string) error {
				seen[p] = true
				if p == point {
					return unix.EIO
				}
				return nil
			}
			err := s.Quarantine(context.Background(), id, size)
			if point == "quarantine-verified" {
				if err != ErrUnavailable {
					t.Fatal(err)
				}
				actual, _ := os.ReadFile(path)
				if !bytes.Equal(actual, raw) {
					t.Fatal("moved before verification boundary")
				}
			} else {
				if err != ErrUncertain || !seen["quarantine-source-sync"] || !seen["quarantine-dest-sync"] {
					t.Fatal(err, seen)
				}
				entries, _ := os.ReadDir(filepath.Join(root, "quarantine"))
				if len(entries) != 1 {
					t.Fatal("uncertain bytes lost")
				}
			}
		})
	}
	t.Run("prior-corrupt-result-does-not-authorize", func(t *testing.T) {
		s, root := newStore(t)
		id, size, _, path := corruptFixture(t, s, root)
		digest := id.String()[7:]
		if err := s.verify(context.Background(), s.blobs, "sha256/"+digest[:2]+"/"+digest[2:4]+"/"+digest+".taf", id, size); err != ErrCorrupt {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, allBytes(t, fixture(t, 10)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := s.Quarantine(context.Background(), id, size); err != ErrInvalid {
			t.Fatal("stale corruption authorized move", err)
		}
	})
}
