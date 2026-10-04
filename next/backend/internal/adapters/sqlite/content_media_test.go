//go:build linux

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/adapters/contentfs"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

func mediaService(t *testing.T) (*LifecycleOwner, *contentfs.Store, *contentstore.Service, string, taffixture.Fixture) {
	t.Helper()
	parent := os.Getenv("TEDDYCLOUD_CONTENTFS_TEST_ROOT")
	if parent == "" {
		parent = "/var/tmp"
	}
	root, err := os.MkdirTemp(parent, "tc-media-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	store, err := contentfs.Open(t.Context(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatalf("qualified media filesystem required: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	owner := blobOwner(t) // owner cleanup precedes store cleanup
	service, err := contentstore.NewService(t.Context(), owner, store)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := taffixture.New(8193, 123, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	return owner, store, service, root, fixture
}

func mediaInventory(t *testing.T, service *contentstore.Service) []contentstore.InventoryEntry {
	t.Helper()
	var entries []contentstore.InventoryEntry
	cursor := ""
	for i := 0; i < 100; i++ {
		page, err := service.Inventory(t.Context(), cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, page.Entries...)
		if page.Complete {
			return entries
		}
		cursor = page.Cursor
	}
	t.Fatal("bounded test scan never completed")
	return nil
}

func TestContentStoreQuarantinePreservesReferences(t *testing.T) {
	owner, _, service, root, f := mediaService(t)
	c, key := blobCommand(t, 1, false), blobKey(t, 1)
	want, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Quarantine(t.Context(), c.BlobID(), c.CompleteBytes()); !errors.Is(err, contentstore.ErrInvalidInput) {
		t.Fatalf("healthy quarantine: %v", err)
	}
	digest := c.BlobID().String()[7:]
	path := filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf")
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteAt([]byte{0}, 4096)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); !errors.Is(err, contentstore.ErrCorrupt) {
		t.Fatal(err)
	}
	if err := service.Quarantine(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); !errors.Is(err, contentstore.ErrMissing) {
		t.Fatalf("availability after move: %v", err)
	}
	got, found, err := service.LookupImport(t.Context(), key, c)
	if err != nil || !found || got != want {
		t.Fatalf("metadata changed: %v %v %v", got, found, err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
	entries := mediaInventory(t, service)
	if len(entries) != 1 || entries[0].Kind != contentstore.QuarantineEntry || entries[0].Bytes != f.CompleteBytes() {
		t.Fatalf("retention: %+v", entries)
	}
	if _, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource); err != nil {
		t.Fatal(err)
	}
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
}

func TestBlobInventoryRestoreAndMutationGeneration(t *testing.T) {
	owner, store, service, _, f := mediaService(t)
	c, key := blobCommand(t, 1, false), blobKey(t, 1)
	snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "before.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource); err != nil {
		t.Fatal(err)
	}
	page, err := service.Inventory(t.Context(), "", 1)
	if err != nil || page.Complete || page.Cursor == "" || page.Entries[0].Kind != contentstore.ReferencedCanonical {
		t.Fatalf("page: %+v %v", page, err)
	}
	// A metadata-only reference write also invalidates cursor state under the gate.
	if _, err := blobRecord(t, owner, blobKey(t, 2), blobCommand(t, 2, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inventory(t.Context(), page.Cursor, 1); !errors.Is(err, contentstore.ErrCursor) {
		t.Fatalf("mutation cursor: %v", err)
	}
	page, err = service.Inventory(t.Context(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inventory(t.Context(), page.Cursor, 1); !errors.Is(err, contentstore.ErrCursor) {
		t.Fatalf("restore cursor: %v", err)
	}
	entries := mediaInventory(t, service)
	if len(entries) != 1 || entries[0].Kind != contentstore.UnreferencedCanonical {
		t.Fatalf("selected DB references: %+v", entries)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := contentstore.NewService(t.Context(), owner, store); !errors.Is(err, contentstore.ErrConflict) {
		t.Fatalf("second attachment: %v", err)
	}
}

// Fail before the DB callback after actual durable publication. This models
// the application boundary deterministically; it is NOT process-kill evidence.
type publishCommitFailpoint struct {
	*LifecycleOwner
	fail bool
}
type failpointSession struct {
	contentstore.Session
	point *publishCommitFailpoint
}

func (p *publishCommitFailpoint) WithinContentOperation(ctx context.Context, callback func(context.Context, contentstore.Session) error) error {
	return p.LifecycleOwner.WithinContentOperation(ctx, func(ctx context.Context, s contentstore.Session) error { return callback(ctx, failpointSession{s, p}) })
}
func (s failpointSession) WithinTransaction(ctx context.Context, callback func(contentstore.BlobRepository) error) error {
	if s.point.fail {
		s.point.fail = false
		return contentstore.ErrUnavailable
	}
	return s.Session.WithinTransaction(ctx, callback)
}
func TestImportCrashAfterPublishDeterministic(t *testing.T) {
	owner, store, service, _, f := mediaService(t)
	point := &publishCommitFailpoint{owner, true}
	// The media owner is already attached; exercise the boundary via the service
	// with a transparent owner wrapper that does not register a second store.
	wrapped := &attachedOwner{point}
	service, err := contentstore.NewService(t.Context(), wrapped, store)
	if err != nil {
		t.Fatal(err)
	}
	c, key := blobCommand(t, 1, false), blobKey(t, 1)
	if _, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource); !errors.Is(err, contentstore.ErrUnavailable) {
		t.Fatal(err)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
	entries := mediaInventory(t, service)
	if len(entries) != 1 || entries[0].Kind != contentstore.UnreferencedCanonical {
		t.Fatalf("orphan: %+v", entries)
	}
	if _, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource); err != nil {
		t.Fatal(err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	entries = mediaInventory(t, service)
	var canonical, stages int
	for _, entry := range entries {
		if entry.Kind == contentstore.ReferencedCanonical {
			canonical++
		}
		if entry.Kind == contentstore.StagingEntry {
			stages++
		}
	}
	if canonical != 1 || stages != 1 {
		t.Fatalf("retry should reuse retained path: %+v", entries)
	}
}

type attachedOwner struct{ contentstore.OperationOwner }

func (*attachedOwner) AttachContentInventory(context.Context, contentstore.InventoryInvalidator) error {
	return nil
}

func TestContentStoreEnvelopeMismatchNoMetadata(t *testing.T) {
	owner, _, service, _, f := mediaService(t)
	c := blobCommand(t, 1, false)
	audio, _ := catalog.NewAudioID(456)
	fingerprint, _ := catalog.NewAudioFingerprint(audio, c.Version().Fingerprint().Hash())
	version, err := catalog.NewContentVersion(c.Version().ID(), c.Version().ContentID(), fingerprint, c.Version().OrderEvidence())
	if err != nil {
		t.Fatal(err)
	}
	mismatch, err := content.NewImportCommand(version, c.BlobID(), c.CompleteBytes(), c.Profile())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Import(t.Context(), blobKey(t, 1), mismatch, f.Open(), content.FiniteTAFSource)
	if !errors.Is(err, contentstore.ErrMismatch) || result != (contentstore.ImportResult{}) {
		t.Fatalf("mismatch acknowledged: %+v %v", result, err)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
	entries := mediaInventory(t, service)
	if len(entries) != 1 || entries[0].Kind != contentstore.UnreferencedCanonical {
		t.Fatalf("orphan must be retained: %+v", entries)
	}
}

type lostResponseOwner struct {
	contentstore.OperationOwner
	lose bool
}

func (o *lostResponseOwner) WithinContentOperation(ctx context.Context, f func(context.Context, contentstore.Session) error) error {
	err := o.OperationOwner.WithinContentOperation(ctx, f)
	if err == nil && o.lose {
		o.lose = false
		return contentstore.ErrCommitUncertain
	}
	return err
}
func TestImportLostResponseApplicationReadback(t *testing.T) {
	owner, store, _, _, f := mediaService(t)
	lost := &lostResponseOwner{owner, true}
	service, err := contentstore.NewService(t.Context(), &attachedOwner{lost}, store)
	if err != nil {
		t.Fatal(err)
	}
	c, key := blobCommand(t, 1, false), blobKey(t, 1)
	result, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource)
	if !errors.Is(err, contentstore.ErrCommitUncertain) || result != (contentstore.ImportResult{}) {
		t.Fatalf("lost response: %+v %v", result, err)
	}
	got, found, err := service.LookupImport(t.Context(), key, c)
	if err != nil || !found || got != importResult(c) {
		t.Fatalf("readback: %+v %v %v", got, found, err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
}

func TestContentStoreMediaRestorePreV4(t *testing.T) {
	owner, store, service, _, f := mediaService(t)
	old, err := Open(t.Context(), Config{Path: filepath.Join(t.TempDir(), "old.sqlite")}, SchemaMigrations()[:3])
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	snapshot, err := old.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "old-snapshot.sqlite"), SchemaMigrations()[:3])
	if err != nil {
		t.Fatal(err)
	}
	c := blobCommand(t, 1, false)
	if _, err := service.Import(t.Context(), blobKey(t, 1), c, f.Open(), content.FiniteTAFSource); err != nil {
		t.Fatal(err)
	}
	page, err := service.Inventory(t.Context(), "", 1)
	if err != nil || page.Cursor == "" {
		t.Fatal(err)
	}
	if err := owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored-old.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inventory(t.Context(), page.Cursor, 1); !errors.Is(err, contentstore.ErrSchemaUnavailable) {
		t.Fatal(err)
	}
	// The hook invalidated the FS cursor even though the selected schema now
	// refuses application content admission. No implicit upgrade or lost media.
	if _, err := store.Inventory(t.Context(), page.Cursor, 1, func(context.Context, content.BlobID) (bool, error) {
		t.Fatal("stale cursor queried DB")
		return false, nil
	}); !errors.Is(err, contentstore.ErrCursor) {
		t.Fatal(err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 3 {
		t.Fatalf("version: %d %v", version, err)
	}
	if err := store.Verify(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
}

func TestContentReferenceLookupBoundsAndRevocation(t *testing.T) {
	owner := blobOwner(t)
	c := blobCommand(t, 1, false)
	var escaped contentstore.Session
	if err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		escaped = s
		found, err := s.References(ctx, c.BlobID())
		if err != nil || found {
			t.Fatalf("empty lookup: %v %v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.References(t.Context(), c.BlobID()); !errors.Is(err, contentstore.ErrRevoked) {
		t.Fatal(err)
	}
	if _, err := blobRecord(t, owner, blobKey(t, 1), c); err != nil {
		t.Fatal(err)
	}
	// Populate bindings with bounded generated SQL fixtures, retaining existing
	// valid facts. One overflow row proves absence cannot be falsely reported.
	for n := 2; n <= referenceScanLimit+1; n++ {
		id := fmt.Sprintf("ver_%026d", n)
		if _, err := owner.database.db.ExecContext(t.Context(), `INSERT INTO tc_content_versions SELECT ?,content_id,audio_id,audio_sha1,order_known,order_namespace,order_position FROM tc_content_versions WHERE version_id=?`, id, c.Version().ID().String()); err != nil {
			t.Fatal(err)
		}
		digest := c.BlobID().Digest()
		if _, err := owner.database.db.ExecContext(t.Context(), `INSERT INTO tc_version_blobs VALUES(?,?)`, id, digest[:]); err != nil {
			t.Fatal(err)
		}
		if n == referenceScanLimit {
			if err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
				found, err := s.References(ctx, content.NewBlobID([32]byte{99}))
				if err != nil || found {
					t.Fatalf("exact bound absence: %v %v", found, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	unknown := content.NewBlobID([32]byte{99})
	if err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		if found, err := s.References(ctx, c.BlobID()); err != nil || !found {
			t.Fatalf("present: %v %v", found, err)
		}
		if _, err := s.References(ctx, unknown); !errors.Is(err, contentstore.ErrUnavailable) {
			t.Fatalf("overflow absence: %v", err)
		}
		short, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.References(short, c.BlobID()); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stats := owner.database.db.Stats(); stats.InUse != 0 {
		t.Fatalf("leaked connection: %+v", stats)
	}
}

type blockedImportSource struct {
	content.TAFSource
	entered, release chan struct{}
	first, closing   sync.Once
}

func (s *blockedImportSource) Read(ctx context.Context, b []byte) (int, error) {
	s.first.Do(func() { close(s.entered) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return s.TAFSource.Read(ctx, b)
}
func (s *blockedImportSource) Close() error {
	s.closing.Do(func() { close(s.release) })
	return s.TAFSource.Close()
}
func TestContentStoreImportFencesMaintenanceAndRestore(t *testing.T) {
	owner, _, service, _, f := mediaService(t)
	c := blobCommand(t, 1, false)
	snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	source := &blockedImportSource{TAFSource: f.Open(), entered: make(chan struct{}), release: make(chan struct{})}
	defer source.Close()
	done := make(chan error, 1)
	go func() {
		_, err := service.Import(t.Context(), blobKey(t, 1), c, source, content.FiniteTAFSource)
		done <- err
	}()
	<-source.entered
	if err := service.Quarantine(t.Context(), c.BlobID(), c.CompleteBytes()); !errors.Is(err, contentstore.ErrBusy) {
		t.Fatal(err)
	}
	if _, err := service.Inventory(t.Context(), "", 1); !errors.Is(err, contentstore.ErrBusy) {
		t.Fatal(err)
	}
	restored := make(chan error, 1)
	go func() {
		restored <- owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite"))
	}()
	waitForLifecycleWaiter(t, owner)
	select {
	case err := <-restored:
		t.Fatalf("restore passed live source: %v", err)
	default:
	}
	source.closing.Do(func() { close(source.release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
	entries := mediaInventory(t, service)
	if len(entries) != 1 || entries[0].Kind != contentstore.UnreferencedCanonical {
		t.Fatalf("restore retained orphan: %+v", entries)
	}
}

type blockedVerify struct {
	contentstore.MediaStore
	entered, release chan struct{}
}

func (s *blockedVerify) Verify(ctx context.Context, id content.BlobID, size uint64) error {
	close(s.entered)
	<-s.release
	return s.MediaStore.Verify(ctx, id, size)
}
func TestContentStoreReadQuarantineRestoreFence(t *testing.T) {
	owner, store, _, _, f := mediaService(t)
	blocked := &blockedVerify{store, make(chan struct{}), make(chan struct{})}
	// Keep the original store's invalidator, use a wrapped read solely to pause
	// at the already-admitted operation boundary.
	wrapper := &attachedOwner{&publishCommitFailpoint{LifecycleOwner: owner}}
	service, err := contentstore.NewService(t.Context(), wrapper, blocked)
	if err != nil {
		t.Fatal(err)
	}
	c := blobCommand(t, 1, false)
	if _, err := service.Import(t.Context(), blobKey(t, 1), c, f.Open(), content.FiniteTAFSource); err != nil {
		t.Fatal(err)
	}
	snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { close(blocked.release) }) }
	defer release()
	readDone := make(chan error, 1)
	go func() { readDone <- service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()) }()
	<-blocked.entered
	if err := service.Quarantine(t.Context(), c.BlobID(), c.CompleteBytes()); !errors.Is(err, contentstore.ErrBusy) {
		t.Fatal(err)
	}
	if _, err := service.Inventory(t.Context(), "", 1); !errors.Is(err, contentstore.ErrBusy) {
		t.Fatal(err)
	}
	if _, err := service.Import(t.Context(), blobKey(t, 2), blobCommand(t, 2, false), f.Open(), content.FiniteTAFSource); !errors.Is(err, contentstore.ErrBusy) {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	if err := owner.Restore(short, snapshot, filepath.Join(t.TempDir(), "deadline.sqlite")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	cancel()
	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite"))
	}()
	waitForLifecycleWaiter(t, owner)
	select {
	case err := <-restoreDone:
		t.Fatalf("restore crossed active read: %v", err)
	default:
	}
	release()
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-restoreDone; err != nil {
		t.Fatal(err)
	}
	if stats := owner.database.db.Stats(); stats.InUse != 0 {
		t.Fatalf("leaked connection: %+v", stats)
	}
}
