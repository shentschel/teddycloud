//go:build linux

package contentfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"golang.org/x/sys/unix"
)

// Synthetic values only: every injected tree carries every protected data class.
var fsPrivateCanaries = []string{
	"/private/f05b/media.taf",
	"SELECT cloud_auth FROM private_tags",
	"credential=f05b-password token=f05b-token",
	"uid=E0:04:00:00:00:00:00:01 ruid=01000000000004E0",
	"import=f05b-import content=f05b-content",
	"provider-response={private:f05b}",
}

func fsPrivateTree() error {
	return fmt.Errorf("foreign wrapper: %w", errors.Join(
		errors.New(strings.Join(fsPrivateCanaries, " | ")),
		fmt.Errorf("foreign nested: %w", unix.EIO),
	))
}

// Check each reachable node, not just Error() or errors.Is on the root.
// Terminal nodes must be one of the specifically required stable categories.
func fsErrorTreeProblem(err error, required []error, foreign error) string {
	if err == nil {
		return "missing failure"
	}
	for _, category := range required {
		if !errors.Is(err, category) {
			return "missing stable category"
		}
	}
	var walk func(error) string
	walk = func(node error) string {
		if node == nil {
			return ""
		}
		for _, canary := range fsPrivateCanaries {
			if strings.Contains(node.Error(), canary) {
				return "private canary in reachable node"
			}
		}
		if foreign != nil && errors.Is(node, foreign) {
			return "foreign cause retained"
		}
		var children []error
		switch e := node.(type) {
		case interface{ Unwrap() []error }:
			children = e.Unwrap()
		case interface{ Unwrap() error }:
			children = []error{e.Unwrap()}
		}
		hasChild := false
		for _, child := range children {
			if child != nil {
				hasChild = true
				if problem := walk(child); problem != "" {
					return problem
				}
			}
		}
		if !hasChild {
			for _, category := range required {
				if node == category {
					return ""
				}
			}
			return "non-category leaf retained"
		}
		return ""
	}
	return walk(err)
}

func assertFSErrorTree(t *testing.T, err, want, foreign error) {
	t.Helper()
	if problem := fsErrorTreeProblem(err, []error{want}, foreign); problem != "" {
		t.Fatal(problem)
	}
}

// A masked outer message ensures recursion, including both unwrap shapes,
// really detects causes which are not visible through the root Error string.
type fsMaskedError struct{ cause error }

func (e fsMaskedError) Error() string { return "masked" }
func (e fsMaskedError) Unwrap() error { return e.cause }

func TestBlobSanitizedContentFSAssertion(t *testing.T) {
	foreign := fsPrivateTree()
	valid := errors.Join(ErrUncertain, fmt.Errorf("safe: %w", ErrUnavailable))
	if problem := fsErrorTreeProblem(valid, []error{ErrUncertain, ErrUnavailable}, foreign); problem != "" {
		t.Fatal(problem)
	}
	for _, bad := range []error{
		nil, ErrUnavailable,
		errors.Join(ErrUncertain, fsMaskedError{foreign}),
		errors.Join(ErrUncertain, fsMaskedError{errors.New("foreign leaf")}),
	} {
		if fsErrorTreeProblem(bad, []error{ErrUncertain}, foreign) == "" {
			t.Fatal("assertion accepted missing category or foreign cause")
		}
	}
	for _, canary := range fsPrivateCanaries {
		bad := errors.Join(ErrUncertain, fsMaskedError{errors.New(canary)})
		if fsErrorTreeProblem(bad, []error{ErrUncertain}, nil) == "" {
			t.Fatal("assertion missed nested canary")
		}
	}
}

func fsFault(point string, foreign error, hits *int) func(string) error {
	return func(p string) error {
		if p == point {
			*hits++
			return foreign
		}
		return nil
	}
}

func TestBlobSanitizedContentFSOpen(t *testing.T) {
	for _, point := range []string{
		"filesystem", "openat2", "lock-sync", "root-sync", "mkdir",
		"new-dir-sync", "parent-sync", "probe-case", "probe-file-sync",
		"renameat2", "probe-dir-sync", "probe-cleanup-sync",
	} {
		t.Run(point, func(t *testing.T) {
			root := qualifiedRoot(t)
			foreign, hits := fsPrivateTree(), 0
			s, err := openWithSyscalls(context.Background(), root, content.DefaultTAFOptions(),
				syscalls{fault: fsFault(point, foreign, &hits)})
			if s != nil {
				s.Close()
				t.Fatal("failed admission returned an owner")
			}
			assertFSErrorTree(t, err, ErrUnsupported, foreign)
			if hits == 0 {
				t.Fatal("seam not exercised")
			}
		})
	}
}

func TestBlobSanitizedContentFSPublish(t *testing.T) {
	for _, point := range []string{
		"source-read", "source-close", "source-read-and-close", "write",
		"random", "openat2", "stage-write", "stage-sync", "mkdir",
		"new-dir-sync", "parent-sync", "renameat2", "publish-sync", "staging-sync", "existing-sync",
	} {
		t.Run(point, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 100)
			raw := allBytes(t, f)
			if point == "existing-sync" {
				if _, err := publishFixture(s, f); err != nil {
					t.Fatal(err)
				}
			}
			foreign, hits := fsPrivateTree(), 0
			src := &inputSource{data: raw}
			switch point {
			case "source-read", "source-read-and-close":
				src.data, src.readError = raw[:4096], foreign
				if point == "source-read-and-close" {
					src.closeError = foreign
				}
			case "source-close":
				src.closeError = foreign
			case "write":
				s.ops.write = func(int, []byte) (int, error) { hits++; return 0, foreign }
			default:
				s.ops.fault = fsFault(point, foreign, &hits)
			}
			envelope, err := s.Publish(context.Background(), content.NewBlobID(f.BlobDigest()),
				f.CompleteBytes(), src, content.FiniteTAFSource)
			want := ErrUnavailable
			published := point == "publish-sync" || point == "staging-sync" || point == "existing-sync"
			if published {
				want = ErrUncertain
			}
			assertFSErrorTree(t, err, want, foreign)
			if !envelope.BlobID().IsZero() || !src.closed {
				t.Fatal("failed publication acknowledged content or leaked source")
			}
			if !strings.HasPrefix(point, "source-") && hits == 0 {
				t.Fatal("seam not exercised")
			}
			if !published {
				assertNoBlob(t, root, f)
			} else {
				got, readErr := os.ReadFile(blobPath(root, f))
				if readErr != nil || !bytes.Equal(got, raw) {
					t.Fatal("uncertain publication lost bytes", readErr)
				}
			}
		})
	}
}

type fsFailingSink struct {
	bytes.Buffer
	writeErr, closeErr error
	closes             int
}

func (s *fsFailingSink) Write(_ context.Context, b []byte) (int, error) {
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	return s.Buffer.Write(b)
}
func (s *fsFailingSink) Close() error { s.closes++; return s.closeErr }

func TestBlobSanitizedContentFSRange(t *testing.T) {
	for _, point := range []string{"openat2", "range-verified", "pread", "sink-write", "sink-close", "write-and-close", "range-delivered"} {
		t.Run(point, func(t *testing.T) {
			s, _ := newStore(t)
			f := fixture(t, 100)
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(err)
			}
			foreign, hits := fsPrivateTree(), 0
			sink := &fsFailingSink{}
			switch point {
			case "pread":
				s.ops.pread = func(int, []byte, int64) (int, error) { hits++; return 0, foreign }
			case "sink-write":
				sink.writeErr = foreign
			case "sink-close":
				sink.closeErr = foreign
			case "write-and-close":
				sink.writeErr, sink.closeErr = foreign, foreign
			default:
				s.ops.fault = fsFault(point, foreign, &hits)
			}
			err := s.ReadRange(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), requested(t, 0, 10), sink)
			assertFSErrorTree(t, err, ErrUnavailable, foreign)
			if sink.closes != 1 {
				t.Fatal("sink close not exactly once")
			}
			// Delivery/Close failure cannot retract an already accepted chunk.
			wantBytes := 0
			if point == "sink-close" || point == "range-delivered" {
				wantBytes = 10
			}
			if sink.Len() != wantBytes {
				t.Fatal("unexpected output at failure boundary")
			}
			if point != "sink-write" && point != "sink-close" && point != "write-and-close" && hits == 0 {
				t.Fatal("seam not exercised")
			}
		})
	}
	for _, point := range []string{"openat2", "range-verified", "range-delivered"} {
		t.Run("verify-"+point, func(t *testing.T) {
			s, _ := newStore(t)
			f := fixture(t, 100)
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(err)
			}
			foreign, hits := fsPrivateTree(), 0
			s.ops.fault = fsFault(point, foreign, &hits)
			err := s.Verify(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes())
			assertFSErrorTree(t, err, ErrUnavailable, foreign)
			if hits == 0 {
				t.Fatal("seam not exercised")
			}
		})
	}
}

// Excludes access timestamps; reading directories is intentionally observational.
func fsTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[rel] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBlobSanitizedContentFSInventory(t *testing.T) {
	for _, point := range []string{"reference", "reference-canceled", "inventory-entry", "openat2"} {
		t.Run(point, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 100)
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(err)
			}
			before := fsTreeSnapshot(t, root)
			foreign, hits := fsPrivateTree(), 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lookup := noReferences
			want := ErrUnavailable
			if strings.HasPrefix(point, "reference") {
				lookup = func(context.Context, content.BlobID) (bool, error) {
					hits++
					if point == "reference-canceled" {
						cancel()
					}
					return true, foreign
				}
				if point == "reference-canceled" {
					want = ErrCanceled
				}
			} else {
				s.ops.fault = fsFault(point, foreign, &hits)
			}
			page, err := s.Inventory(ctx, "", MaxInventoryPage, lookup)
			assertFSErrorTree(t, err, want, foreign)
			if hits == 0 || len(page.Entries) != 0 || page.Cursor != "" || page.Complete || s.scan != nil {
				t.Fatal("failed inventory exposed partial evidence or retained cursor")
			}
			if !reflect.DeepEqual(before, fsTreeSnapshot(t, root)) {
				t.Fatal("failed inventory mutated media")
			}
		})
	}
}

func TestBlobSanitizedContentFSQuarantine(t *testing.T) {
	for _, point := range []string{"openat2", "quarantine-verified", "renameat2", "quarantine-source-sync", "quarantine-dest-sync"} {
		t.Run(point, func(t *testing.T) {
			s, root := newStore(t)
			id, size, raw, path := corruptFixture(t, s, root)
			before := fsTreeSnapshot(t, root)
			foreign, hits := fsPrivateTree(), 0
			s.ops.fault = fsFault(point, foreign, &hits)
			err := s.Quarantine(context.Background(), id, size)
			want := ErrUnavailable
			uncertain := strings.HasSuffix(point, "-sync")
			if uncertain {
				want = ErrUncertain
			}
			assertFSErrorTree(t, err, want, foreign)
			if hits == 0 {
				t.Fatal("seam not exercised")
			}
			if !uncertain {
				if !reflect.DeepEqual(before, fsTreeSnapshot(t, root)) {
					t.Fatal("quarantine mutated media before admission")
				}
			} else {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("source not moved", err)
				}
				entries, err := os.ReadDir(filepath.Join(root, "quarantine"))
				if err != nil || len(entries) != 1 {
					t.Fatal("uncertain bytes lost", err)
				}
				got, err := os.ReadFile(filepath.Join(root, "quarantine", entries[0].Name()))
				if err != nil || !bytes.Equal(got, raw) {
					t.Fatal("uncertain bytes changed", err)
				}
			}
		})
	}
}

func TestBlobSanitizedContentFSClose(t *testing.T) {
	s, _ := newStore(t)
	if err := unix.Close(s.blobs); err != nil {
		t.Fatal(err)
	}
	// Impossible descriptor avoids racing descriptor reuse after manual close.
	s.blobs = 1 << 30
	assertFSErrorTree(t, s.Close(), ErrUnavailable, unix.EBADF)
	if err := s.Close(); err != nil {
		t.Fatal("close not idempotent", err)
	}
}
