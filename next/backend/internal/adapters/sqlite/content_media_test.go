//go:build linux

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	return mediaServiceWithOptions(t, content.DefaultTAFOptions())
}

func mediaServiceWithOptions(t *testing.T, options content.TAFOptions) (*LifecycleOwner, *contentfs.Store, *contentstore.Service, string, taffixture.Fixture) {
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
	store, err := contentfs.Open(t.Context(), root, options)
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
	point        *publishCommitFailpoint
	transactions int
}

func (p *publishCommitFailpoint) WithinContentOperation(ctx context.Context, callback func(context.Context, contentstore.Session) error) error {
	return p.LifecycleOwner.WithinContentOperation(ctx, func(ctx context.Context, s contentstore.Session) error {
		return callback(ctx, &failpointSession{Session: s, point: p})
	})
}
func (s *failpointSession) WithinTransaction(ctx context.Context, callback func(contentstore.BlobRepository) error) error {
	s.transactions++
	if s.transactions == 2 && s.point.fail {
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
	// A lost response is recovered by the exact same Import, not only readback.
	// Publication validates a fresh source, verifies the existing canonical,
	// and retains the duplicate stage under the current no-delete contract.
	retry, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource)
	if err != nil || retry != got {
		t.Fatalf("lost-response exact retry: %+v %v", retry, err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
	var canonical, stages int
	for _, entry := range mediaInventory(t, service) {
		switch entry.Kind {
		case contentstore.ReferencedCanonical:
			canonical++
		case contentstore.StagingEntry:
			stages++
		default:
			t.Fatalf("unexpected retained entry after exact retry: %+v", entry)
		}
	}
	if canonical != 1 || stages != 1 {
		t.Fatalf("lost-response retry retention: canonical=%d stages=%d", canonical, stages)
	}
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

type lifecycleRangeSink struct {
	ctx          context.Context
	writeEntered chan struct{}
	writeRelease chan struct{}
	closeEntered chan struct{}
	closeRelease chan struct{}
	writeOnce    sync.Once
	closeOnce    sync.Once
	data         []byte
}

func (s *lifecycleRangeSink) Write(ctx context.Context, b []byte) (int, error) {
	s.writeOnce.Do(func() { close(s.writeEntered) })
	select {
	case <-s.writeRelease:
		s.data = append(s.data, b...)
		return len(b), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (s *lifecycleRangeSink) Close() error {
	s.closeOnce.Do(func() { close(s.closeEntered) })
	select {
	case <-s.closeRelease:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// Observe the gate notification under its lock: no scheduling sleeps and no
// inference from a goroutine merely having started.
func awaitRangeLifecycleWaiter(t *testing.T, ctx context.Context, owner *LifecycleOwner) {
	t.Helper()
	for {
		owner.gate.mutex.Lock()
		waiting := owner.gate.lifecycleWaiters > 0
		changed := owner.gate.changed
		owner.gate.mutex.Unlock()
		if waiting {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("lifecycle waiter not observed:", ctx.Err())
		}
	}
}

func awaitRangeSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal("range phase not reached:", ctx.Err())
	}
}

func awaitRangeResult(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("range/lifecycle did not finish:", ctx.Err())
		return ctx.Err()
	}
}

func TestContentStoreRealReadRangeLifecycleFence(t *testing.T) {
	for _, action := range []string{"restore", "close", "cancel-restore"} {
		t.Run(action, func(t *testing.T) {
			owner, store, service, _, fixture := mediaService(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command, key := blobCommand(t, 1, false), blobKey(t, 1)
			// An empty snapshot makes a successful switch observably different
			// from cancellation, which must retain the imported reference.
			snapshot, err := owner.database.CreateBackup(ctx, filepath.Join(t.TempDir(), "empty.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			want, err := service.Import(ctx, key, command, fixture.Open(), content.FiniteTAFSource)
			if err != nil {
				t.Fatal(err)
			}
			oldDB, oldPath, oldGeneration := owner.database, owner.config.Path, owner.generation.Load()
			destination := filepath.Join(t.TempDir(), "restored.sqlite")
			sink := &lifecycleRangeSink{
				ctx: ctx, writeEntered: make(chan struct{}), writeRelease: make(chan struct{}),
				closeEntered: make(chan struct{}), closeRelease: make(chan struct{}),
			}
			byteRange, err := contentfs.NewByteRange(0, 32)
			if err != nil {
				t.Fatal(err)
			}
			var oldSession contentstore.Session
			var oldReference contentstore.ReferenceLookup
			readDone := make(chan error, 1)
			lifecycleDone := make(chan error, 1)
			go func() {
				readDone <- owner.WithinContentOperation(ctx, func(ctx context.Context, session contentstore.Session) error {
					oldSession, oldReference = session, session.References
					return store.ReadRange(ctx, command.BlobID(), command.CompleteBytes(), byteRange, sink)
				})
			}()
			// Cancellation releases both sink phases even if an assertion fails.
			// Drain the workers before fixture cleanup closes their owner/store.
			lifecycleStarted, lifecycleReceived, readReceived := false, false, false
			t.Cleanup(func() {
				cancel()
				drain, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				if !readReceived {
					_ = awaitRangeResult(t, drain, readDone)
				}
				if lifecycleStarted && !lifecycleReceived {
					_ = awaitRangeResult(t, drain, lifecycleDone)
				}
			})
			awaitRangeSignal(t, ctx, sink.writeEntered)
			lifecycleCtx, cancelLifecycle := context.WithCancel(ctx)
			defer cancelLifecycle()
			lifecycleStarted = true
			go func() {
				if action == "close" {
					lifecycleDone <- owner.Close(lifecycleCtx)
				} else {
					lifecycleDone <- owner.Restore(lifecycleCtx, snapshot, destination)
				}
			}()
			awaitRangeLifecycleWaiter(t, ctx, owner)
			assertFenced := func() {
				t.Helper()
				select {
				case err := <-lifecycleDone:
					lifecycleReceived = true
					t.Fatalf("lifecycle crossed blocked sink: %v", err)
				default:
				}
				select {
				case err := <-readDone:
					readReceived = true
					t.Fatalf("read returned before sink release: %v", err)
				default:
				}
				owner.gate.mutex.Lock()
				active, waiters := owner.gate.active, owner.gate.lifecycleWaiters
				owner.gate.mutex.Unlock()
				if !active || waiters != 1 {
					t.Fatalf("fence state: active=%v waiters=%d", active, waiters)
				}
			}
			assertFenced()
			close(sink.writeRelease)
			awaitRangeSignal(t, ctx, sink.closeEntered)
			assertFenced()
			if action == "cancel-restore" {
				cancelLifecycle()
				err := awaitRangeResult(t, ctx, lifecycleDone)
				lifecycleReceived = true
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("waiting restore cancellation: %v", err)
				}
			}
			close(sink.closeRelease)
			err = awaitRangeResult(t, ctx, readDone)
			readReceived = true
			if err != nil {
				t.Fatal(err)
			}
			if !lifecycleReceived {
				err = awaitRangeResult(t, ctx, lifecycleDone)
				lifecycleReceived = true
				if err != nil {
					t.Fatal(err)
				}
			}
			header := fixture.Header()
			if string(sink.data) != string(header[:32]) {
				t.Fatal("real range delivered incorrect fixture bytes")
			}
			if err := oldSession.WithinTransaction(ctx, func(contentstore.BlobRepository) error {
				t.Error("revoked session invoked callback")
				return nil
			}); !errors.Is(err, contentstore.ErrRevoked) {
				t.Fatalf("old session: %v", err)
			}
			if _, err := oldReference(ctx, command.BlobID()); !errors.Is(err, contentstore.ErrRevoked) {
				t.Fatalf("old reference callback: %v", err)
			}
			switch action {
			case "restore":
				if owner.database == oldDB || owner.config.Path != destination || owner.generation.Load() != oldGeneration+1 {
					t.Fatal("restore did not switch owner")
				}
				assertBlobCounts(t, owner, 0, 0, 0, 0)
			case "close":
				if owner.database != nil {
					t.Fatal("close retained selected database")
				}
				if _, err := owner.CurrentVersion(ctx); !errors.Is(err, ErrLifecycleClosed) {
					t.Fatalf("closed owner usable: %v", err)
				}
			case "cancel-restore":
				if owner.database != oldDB || owner.config.Path != oldPath || owner.generation.Load() != oldGeneration {
					t.Fatal("canceled restore changed active owner")
				}
				if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("canceled restore created destination: %v", err)
				}
				got, found, err := service.LookupImport(ctx, key, command)
				if err != nil || !found || got != want {
					t.Fatalf("canceled restore lost metadata: %+v %v %v", got, found, err)
				}
				assertBlobCounts(t, owner, 1, 1, 1, 1)
				if err := service.Availability(ctx, command.BlobID(), command.CompleteBytes()); err != nil {
					t.Fatal("active owner/media unusable:", err)
				}
			}
			if err := store.Verify(ctx, command.BlobID(), command.CompleteBytes()); err != nil {
				t.Fatal("lifecycle changed media:", err)
			}
		})
	}
}

type importProbeSource struct {
	content.TAFSource
	reads, closes atomic.Int32
}

func (s *importProbeSource) Read(ctx context.Context, b []byte) (int, error) {
	s.reads.Add(1)
	return s.TAFSource.Read(ctx, b)
}

func (s *importProbeSource) Close() error {
	s.closes.Add(1)
	return s.TAFSource.Close()
}

// PI07-T-F07: the owner rejects overlapping calls with Busy, leaving their
// sources caller-owned. Retrying after the admitted call completes must be
// idempotent. Receipt conflicts must be rejected before publishing any bytes;
// matching receipts must still validate the supplied source.
func TestContentStoreConcurrentImportRetryMatrix(t *testing.T) {
	for _, scenario := range []string{"identical", "different-command", "different-bytes", "identical-bad-source"} {
		t.Run(scenario, func(t *testing.T) {
			owner, _, service, root, fixture := mediaService(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command, key := blobCommand(t, 1, false), blobKey(t, 1)
			retryCommand, retryFixture := command, fixture
			if scenario == "different-command" {
				retryCommand = blobCommand(t, 2, false)
			}
			if scenario == "different-bytes" {
				var err error
				retryFixture, err = taffixture.New(8193, 456, []uint32{0, 1})
				if err != nil {
					t.Fatal(err)
				}
				audio, err := catalog.NewAudioID(456)
				if err != nil {
					t.Fatal(err)
				}
				fp, err := catalog.NewAudioFingerprint(audio, command.Version().Fingerprint().Hash())
				if err != nil {
					t.Fatal(err)
				}
				version, err := catalog.NewContentVersion(blobCommand(t, 2, false).Version().ID(), command.Version().ContentID(), fp, command.Version().OrderEvidence())
				if err != nil {
					t.Fatal(err)
				}
				retryCommand, err = content.NewImportCommand(version, content.NewBlobID(retryFixture.BlobDigest()), retryFixture.CompleteBytes(), command.Profile())
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "identical-bad-source" {
				var err error
				retryFixture, err = taffixture.New(8193, 456, []uint32{0, 1})
				if err != nil {
					t.Fatal(err)
				}
			}
			blocked := &blockedImportSource{TAFSource: fixture.Open(), entered: make(chan struct{}), release: make(chan struct{})}
			var first contentstore.ImportResult
			done := make(chan error, 1)
			received := false
			t.Cleanup(func() {
				cancel()
				blocked.closing.Do(func() { close(blocked.release) })
				if !received {
					drain, stop := context.WithTimeout(context.Background(), 2*time.Second)
					defer stop()
					_ = awaitRangeResult(t, drain, done)
				}
			})
			go func() {
				var err error
				first, err = service.Import(ctx, key, command, blocked, content.FiniteTAFSource)
				done <- err
			}()
			awaitRangeSignal(t, ctx, blocked.entered)
			source := &importProbeSource{TAFSource: retryFixture.Open()}
			t.Cleanup(func() {
				if source.closes.Load() == 0 {
					_ = source.Close()
				}
			})
			got, err := service.Import(ctx, key, retryCommand, source, content.FiniteTAFSource)
			if !errors.Is(err, contentstore.ErrBusy) || got != (contentstore.ImportResult{}) {
				t.Fatalf("overlap: %+v %v", got, err)
			}
			if source.reads.Load() != 0 || source.closes.Load() != 0 {
				t.Fatal("Busy admission consumed caller-owned source")
			}
			assertBlobCounts(t, owner, 0, 0, 0, 0)
			blocked.closing.Do(func() { close(blocked.release) })
			err = awaitRangeResult(t, ctx, done)
			received = true
			if err != nil || first != importResult(command) {
				t.Fatalf("admitted import: %+v %v", first, err)
			}
			digest := command.BlobID().String()[7:]
			path := filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf")
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err = service.Import(ctx, key, retryCommand, source, content.FiniteTAFSource)
			if scenario == "identical" {
				if err != nil || got != first {
					t.Fatalf("exact retry: %+v %v", got, err)
				}
			} else if scenario == "identical-bad-source" {
				if !errors.Is(err, contentstore.ErrMismatch) || got != (contentstore.ImportResult{}) {
					t.Fatalf("matching receipt bypassed source validation: %+v %v", got, err)
				}
			} else if !errors.Is(err, contentstore.ErrConflict) || got != (contentstore.ImportResult{}) {
				t.Fatalf("conflicting retry: %+v %v", got, err)
			}
			if scenario == "different-command" || scenario == "different-bytes" {
				if source.reads.Load() != 0 || source.closes.Load() != 0 {
					t.Fatal("receipt conflict consumed caller-owned source")
				}
				if entries := mediaInventory(t, service); len(entries) != 1 {
					t.Fatalf("receipt conflict created retained bytes: %+v", entries)
				}
			} else if source.reads.Load() == 0 || source.closes.Load() != 1 {
				t.Fatal("admitted retry did not validate and close its source")
			}
			assertBlobCounts(t, owner, 1, 1, 1, 1)
			got, found, err := service.LookupImport(ctx, key, command)
			if err != nil || !found || got != first {
				t.Fatalf("original receipt changed: %+v %v %v", got, found, err)
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("canonical file replaced: %v", err)
			}
			if err := service.Availability(ctx, command.BlobID(), command.CompleteBytes()); err != nil {
				t.Fatal(err)
			}
			var referenced, unreferenced int
			for _, entry := range mediaInventory(t, service) {
				switch entry.Kind {
				case contentstore.ReferencedCanonical:
					referenced++
				case contentstore.UnreferencedCanonical:
					unreferenced++
				}
			}
			// Existing publication retains duplicate staging bytes on EEXIST;
			// these are not extra canonical blobs. Do not hide a new canonical
			// from a rejected conflicting command behind that retention policy.
			if referenced != 1 || unreferenced != 0 {
				t.Fatalf("conflicting retry published extra canonical: referenced=%d unreferenced=%d", referenced, unreferenced)
			}
		})
	}
}
