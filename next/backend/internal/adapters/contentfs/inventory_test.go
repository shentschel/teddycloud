//go:build linux

package contentfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

func noReferences(context.Context, content.BlobID) (bool, error) { return false, nil }

func collectInventory(t *testing.T, s *Store, refs ReferenceLookup) []InventoryEntry {
	t.Helper()
	var all []InventoryEntry
	cursor := ""
	for calls := 0; calls < 100; calls++ {
		page, err := s.Inventory(context.Background(), cursor, 0, refs)
		if err != nil {
			t.Fatal(err)
		}
		if page.Inspected > MaxInventoryInspected || len(page.Entries) > DefaultInventoryPage || len(page.Cursor) > MaxInventoryCursor {
			t.Fatal("unbounded page")
		}
		if s.scan != nil && len(s.scan.stack) > 4 {
			t.Fatal("unbounded descriptors")
		}
		all = append(all, page.Entries...)
		if page.Complete {
			if page.Cursor != "" || s.scan != nil {
				t.Fatal("complete scan retained cursor")
			}
			return all
		}
		cursor = page.Cursor
	}
	t.Fatal("inventory did not complete")
	return nil
}

func writeRetained(t *testing.T, root, dir, prefix string, i int, bytes []byte) {
	t.Helper()
	name := fmt.Sprintf("%s%032x.taf", prefix, i)
	if err := os.WriteFile(filepath.Join(root, dir, name), bytes, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBlobInventoryClassificationAndRetention(t *testing.T) {
	s, root := newStore(t)
	a, b := fixture(t, 41), fixture(t, 42)
	for _, f := range []uint64{41, 42} {
		if _, err := publishFixture(s, fixture(t, f)); err != nil {
			t.Fatal(err)
		}
	}
	writeRetained(t, root, "staging", "s-", 1, []byte("stage"))
	writeRetained(t, root, "quarantine", "q-", 2, []byte("quarantine"))
	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("unknown"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp", filepath.Join(root, "blobs", "outside")); err != nil {
		t.Fatal(err)
	}
	refs := func(_ context.Context, id content.BlobID) (bool, error) {
		return id == content.NewBlobID(a.BlobDigest()), nil
	}
	entries := collectInventory(t, s, refs)
	counts := map[InventoryKind]int{}
	for _, e := range entries {
		counts[e.Kind]++
		if e.Kind == UnreferencedCanonical && e.BlobID != content.NewBlobID(b.BlobDigest()) {
			t.Fatal("wrong digest")
		}
	}
	for _, kind := range []InventoryKind{ReferencedCanonical, UnreferencedCanonical, StagingEntry, QuarantineEntry} {
		if counts[kind] != 1 {
			t.Fatalf("classification %s: %d", kind, counts[kind])
		}
	}
	if counts[UnexpectedEntry] != 2 || s.capacityKnown {
		t.Fatal("unexpected entries hidden/admitted")
	}
	if _, err := os.Stat(blobPath(root, a)); err != nil {
		t.Fatal("inventory deleted blob", err)
	}
	if _, err := os.Stat(filepath.Join(root, "unexpected")); err != nil {
		t.Fatal("inventory deleted unknown", err)
	}
}

func TestBlobInventoryBoundsAndCursors(t *testing.T) {
	s, root := newStore(t)
	for i := 0; i < 130; i++ {
		writeRetained(t, root, "staging", "s-", i, nil)
	}
	page, err := s.Inventory(context.Background(), "", 0, noReferences)
	if err != nil || len(page.Entries) != 128 || page.Complete || page.Cursor == "" {
		t.Fatal(page, err)
	}
	if _, err = s.Inventory(context.Background(), "", 1, noReferences); err != ErrBusy {
		t.Fatal("second scan", err)
	}
	if _, err = s.Inventory(context.Background(), strings.Repeat("x", 257), 1, noReferences); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err = s.Inventory(context.Background(), page.Cursor, MaxInventoryPage+1, noReferences); err != ErrInvalid {
		t.Fatal("page ceiling", err)
	}
	if s.scan == nil || s.scan.cursor != page.Cursor {
		t.Fatal("invalid page request changed scan")
	}
	next, err := s.Inventory(context.Background(), page.Cursor, 1, noReferences)
	if err != nil || next.Cursor == page.Cursor {
		t.Fatal("cursor not single-use", err)
	}
	if _, err = s.Inventory(context.Background(), page.Cursor, 1, noReferences); err != ErrCursor {
		t.Fatal("stale cursor", err)
	}
	page = next
	s.scan.last = time.Now().Add(-InventoryIdleExpiry)
	if _, err = s.Inventory(context.Background(), page.Cursor, 1, noReferences); err != ErrCursor {
		t.Fatal("idle expiry", err)
	}
	page, err = s.Inventory(context.Background(), "", 1, noReferences)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publishFixture(s, fixture(t, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Inventory(context.Background(), page.Cursor, 1, noReferences); err != ErrCursor {
		t.Fatal("mutation cursor survived", err)
	}
	page, err = s.Inventory(context.Background(), "", 1, noReferences)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenForInventory(context.Background(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.Inventory(context.Background(), page.Cursor, 1, noReferences); err != ErrCursor {
		t.Fatal("restart cursor survived", err)
	}
	if _, err = publishFixture(reopened, fixture(t, 2)); err != ErrRetained {
		t.Fatal("mutation before reconciliation", err)
	}
	collectInventory(t, reopened, noReferences)
	if reopened.stageEntries != 130 || reopened.capacityKnown {
		t.Fatal("over-cap stages admitted")
	}
}

func TestBlobInventoryInspectedLimitAndDescriptorDepth(t *testing.T) {
	s, root := newStore(t)
	alg := filepath.Join(root, "blobs", "sha256")
	if err := os.Mkdir(alg, 0700); err != nil {
		t.Fatal(err)
	}
	for a := 0; a < 5; a++ {
		first := filepath.Join(alg, fmt.Sprintf("%02x", a))
		if err := os.Mkdir(first, 0700); err != nil {
			t.Fatal(err)
		}
		for b := 0; b < 256; b++ {
			if err := os.Mkdir(filepath.Join(first, fmt.Sprintf("%02x", b)), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	maxDepth := 0
	s.ops.fault = func(p string) error {
		if p == "inventory-entry" && s.scan != nil && len(s.scan.stack) > maxDepth {
			maxDepth = len(s.scan.stack)
		}
		return nil
	}
	page, err := s.Inventory(context.Background(), "", MaxInventoryPage, noReferences)
	if err != nil || page.Inspected != 1024 || page.Complete || page.Cursor == "" || maxDepth != 4 {
		t.Fatal(page.Inspected, maxDepth, err)
	}
	collect := page.Cursor
	for !page.Complete {
		page, err = s.Inventory(context.Background(), collect, 128, noReferences)
		if err != nil {
			t.Fatal(err)
		}
		collect = page.Cursor
	}
	if maxDepth != 4 {
		t.Fatal(maxDepth)
	}
}

func TestBlobInventoryCancellationAndBusy(t *testing.T) {
	s, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Inventory(ctx, "", 0, noReferences); err != ErrCanceled {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, err := s.Inventory(context.Background(), "", 0, noReferences)
	s.mu.Unlock()
	if err != ErrBusy {
		t.Fatal(err)
	}
	if _, err := publishFixture(s, fixture(t, 1)); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.Inventory(context.Background(), "", 0, func(ctx context.Context, id content.BlobID) (bool, error) {
			close(started)
			<-release
			return false, nil
		})
		done <- err
	}()
	<-started
	if _, err := s.Inventory(context.Background(), "", 0, noReferences); err != ErrBusy {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBlobInventoryRestartReconcilesCapacity(t *testing.T) {
	s, root := newStore(t)
	f := fixture(t, 1)
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenForInventory(context.Background(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	collectInventory(t, reopened, noReferences)
	if !reopened.capacityKnown || reopened.stageEntries != 1 || reopened.stageBytes != f.CompleteBytes() {
		t.Fatal("capacity not recovered")
	}
	if _, err := publishFixture(reopened, fixture(t, 2)); err != nil {
		t.Fatal(err)
	}
}

func TestBlobInventoryDeadlineAndRestoreInvalidation(t *testing.T) {
	s, _ := newStore(t)
	s.ops.fault = func(point string) error {
		if point == "inventory-entry" {
			time.Sleep(InventoryDuration + time.Millisecond)
		}
		return nil
	}
	page, err := s.Inventory(context.Background(), "", 0, noReferences)
	if err != nil || page.Complete || page.Cursor == "" || page.Inspected != 0 {
		t.Fatal("deadline advertised completeness", page, err)
	}
	s.ops.fault = nil
	if err = s.InvalidateInventory(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Inventory(context.Background(), page.Cursor, 0, noReferences); err != ErrCursor {
		t.Fatal("restore cursor survived", err)
	}
	collectInventory(t, s, noReferences)
	if cappedInventoryBytes(maxStageBytes, ^uint64(0), maxStageBytes) != maxStageBytes+1 {
		t.Fatal("accounting overflow")
	}
}
