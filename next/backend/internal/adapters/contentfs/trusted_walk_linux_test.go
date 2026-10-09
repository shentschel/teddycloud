//go:build linux

package contentfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

func shardDirectories(path string) []string {
	b := filepath.Dir(path)
	a := filepath.Dir(b)
	return []string{filepath.Dir(a), a, b}
}

func TestBlobTrustedReadDirectories(t *testing.T) {
	for level := 0; level < 3; level++ {
		for _, mode := range []os.FileMode{0777, 0700} {
			t.Run([]string{"sha256", "first-shard", "second-shard"}[level]+"/"+mode.String(), func(t *testing.T) {
				s, root := newStore(t)
				f := fixture(t, 10)
				if _, err := publishFixture(s, f); err != nil {
					t.Fatal(err)
				}
				path := blobPath(root, f)
				if err := os.Chmod(shardDirectories(path)[level], mode); err != nil {
					t.Fatal(err)
				}
				id := content.NewBlobID(f.BlobDigest())
				want := error(nil)
				if mode == 0777 {
					want = ErrUnavailable
				}
				if err := s.Verify(context.Background(), id, f.CompleteBytes()); err != want {
					t.Fatalf("Verify: got %v want %v", err, want)
				}
				sink := &rangeSink{}
				err := s.ReadRange(context.Background(), id, f.CompleteBytes(), requested(t, 0, 1), sink)
				if err != want {
					t.Fatalf("ReadRange: got %v want %v", err, want)
				}
				if want != nil && sink.Len() != 0 {
					t.Fatal("untrusted path emitted bytes")
				}
				if want == nil && !bytes.Equal(sink.Bytes(), allBytes(t, f)[:1]) {
					t.Fatal("trusted control did not deliver bytes")
				}
			})
		}
	}
}

func TestBlobTrustedQuarantineDirectories(t *testing.T) {
	for level := 0; level < 4; level++ {
		for _, late := range []bool{false, true} {
			t.Run([]string{"sha256", "first-shard", "second-shard", "destination"}[level]+"/"+map[bool]string{false: "initial", true: "after-verification"}[late], func(t *testing.T) {
				s, root := newStore(t)
				id, size, raw, path := corruptFixture(t, s, root)
				dirs := append(shardDirectories(path), filepath.Join(root, "quarantine"))
				change := func() {
					if err := os.Chmod(dirs[level], 0777); err != nil {
						t.Fatal(err)
					}
				}
				if late {
					s.ops.fault = func(point string) error {
						if point == "quarantine-verified" {
							change()
						}
						return nil
					}
				} else {
					change()
				}
				if err := s.Quarantine(context.Background(), id, size); err != ErrUnavailable {
					t.Fatal("untrusted quarantine accepted", err)
				}
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, raw) {
					t.Fatal("source changed on rejection", err)
				}
				entries, err := os.ReadDir(filepath.Join(root, "quarantine"))
				if err != nil || len(entries) != 0 || s.quarantineEntries != 0 {
					t.Fatal("rejected quarantine mutated destination", err)
				}
				s.ops.fault = nil
				if err := os.Chmod(dirs[level], 0700); err != nil {
					t.Fatal(err)
				}
				if err := s.Quarantine(context.Background(), id, size); err != nil {
					t.Fatal("trusted control rejected", err)
				}
			})
		}
	}
}

func TestBlobTrustedReadRechecksBeforeOutput(t *testing.T) {
	for level := 0; level < 3; level++ {
		for _, mutation := range []string{"mode", "swap"} {
			t.Run([]string{"sha256", "first-shard", "second-shard"}[level]+"/"+mutation, func(t *testing.T) {
				s, root := newStore(t)
				f := fixture(t, 10)
				if _, err := publishFixture(s, f); err != nil {
					t.Fatal(err)
				}
				path := shardDirectories(blobPath(root, f))[level]
				s.ops.fault = func(point string) error {
					if point != "range-verified" {
						return nil
					}
					if mutation == "mode" {
						return os.Chmod(path, 0777)
					}
					if err := os.Rename(path, path+"-held"); err != nil {
						return err
					}
					return os.Symlink(path+"-held", path)
				}
				sink := &rangeSink{}
				err := s.ReadRange(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), requested(t, 0, 1), sink)
				if err != ErrUnavailable || sink.Len() != 0 {
					t.Fatal("changed chain emitted bytes", err)
				}
			})
		}
	}
}

func TestBlobTrustedWalkMissingIsReadOnly(t *testing.T) {
	for level := 0; level < 3; level++ {
		t.Run([]string{"sha256", "first-shard", "second-shard"}[level], func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 10)
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(err)
			}
			path := shardDirectories(blobPath(root, f))[level]
			if err := os.Rename(path, path+"-held"); err != nil {
				t.Fatal(err)
			}
			id := content.NewBlobID(f.BlobDigest())
			if err := s.Verify(context.Background(), id, f.CompleteBytes()); err != ErrMissing {
				t.Fatal(err)
			}
			if err := s.Quarantine(context.Background(), id, f.CompleteBytes()); err != ErrMissing {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("read or quarantine created missing component", err)
			}
		})
	}
}
