//go:build linux

package contentfs

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/adapters/sqlite"
	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
	"golang.org/x/sys/unix"
)

// Use the real application/SQLite owner so failed publication is checked against
// persisted receipts and references, not a reference-lookup stub.
func closeoutService(t *testing.T, root, db string) (*Store, *sqlite.LifecycleOwner, *contentstore.Service) {
	t.Helper()
	store, err := OpenForInventory(t.Context(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	owner, err := sqlite.OpenLifecycleOwner(t.Context(), sqlite.Config{Path: db}, sqlite.SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	id, err := identity.ParseContentID("cnt_0123456789abcdefghjkmnpqrs")
	if err != nil {
		t.Fatal(err)
	}
	item, err := catalog.NewContent(id, catalog.NewContentFacts("Synthetic closeout fixture", catalog.NewProductIdentifiers(nil, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		return r.Save(t.Context(), item)
	}); err != nil {
		t.Fatal(err)
	}
	service, err := contentstore.NewService(t.Context(), owner, store)
	if err != nil {
		t.Fatal(err)
	}
	return store, owner, service
}

func closeoutCommand(t *testing.T, f taffixture.Fixture) (content.ImportKey, content.ImportCommand) {
	t.Helper()
	id, err := identity.ParseContentID("cnt_0123456789abcdefghjkmnpqrs")
	if err != nil {
		t.Fatal(err)
	}
	versionID, err := identity.ParseContentVersionID("ver_00000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := catalog.NewAudioID(17)
	if err != nil {
		t.Fatal(err)
	}
	h := f.PayloadSHA1()
	hash, err := catalog.ParseAudioHash(hex.EncodeToString(h[:]))
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := catalog.NewAudioFingerprint(audio, hash)
	if err != nil {
		t.Fatal(err)
	}
	version, err := catalog.NewContentVersion(versionID, id, fingerprint, catalog.UnknownVersionOrderEvidence())
	if err != nil {
		t.Fatal(err)
	}
	command, err := content.NewImportCommand(version, content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), content.TAFProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	key, err := content.ParseImportKey("imp_00000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	return key, command
}

func closeoutInventory(t *testing.T, service *contentstore.Service) []InventoryEntry {
	t.Helper()
	var entries []InventoryEntry
	cursor := ""
	for calls := 0; calls < 100; calls++ {
		page, err := service.Inventory(t.Context(), cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > 1 || page.Inspected > MaxInventoryInspected || len(page.Cursor) > MaxInventoryCursor {
			t.Fatal("unbounded inventory page", page)
		}
		entries = append(entries, page.Entries...)
		if page.Complete {
			if page.Cursor != "" {
				t.Fatal("completed scan retained cursor")
			}
			return entries
		}
		if page.Cursor == "" || page.Cursor == cursor {
			t.Fatal("scan did not advance")
		}
		cursor = page.Cursor
	}
	t.Fatal("inventory failed to complete within page bound")
	return nil
}

func assertCloseoutMetadata(t *testing.T, owner *sqlite.LifecycleOwner, service *contentstore.Service, key content.ImportKey, command content.ImportCommand, want bool) {
	t.Helper()
	result, found, err := service.LookupImport(t.Context(), key, command)
	if err != nil || found != want {
		t.Fatalf("receipt: found=%v want=%v error=%v", found, want, err)
	}
	if !want && result != (contentstore.ImportResult{}) {
		t.Fatal("failed import returned a receipt", result)
	}
	if err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, session contentstore.Session) error {
		found, err := session.References(ctx, command.BlobID())
		if err == nil && found != want {
			t.Errorf("reference: found=%v want=%v", found, want)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBlobPartialWriteRestartAndRetry(t *testing.T) {
	for _, kind := range []string{"short", "partial-ENOSPC"} {
		t.Run(kind, func(t *testing.T) {
			root, db := qualifiedRoot(t), filepath.Join(t.TempDir(), "metadata.sqlite")
			store, owner, service := closeoutService(t, root, db)
			closeoutInventory(t, service)
			f := fixture(t, 5000)
			key, command := closeoutCommand(t, f)
			store.ops.write = func(fd int, b []byte) (int, error) {
				n, err := unix.Write(fd, b[:1])
				if err == nil && kind == "partial-ENOSPC" {
					err = unix.ENOSPC
				}
				return n, err
			}
			result, err := service.Import(t.Context(), key, command, f.Open(), content.FiniteTAFSource)
			if !errors.Is(err, ErrUnavailable) || result != (contentstore.ImportResult{}) {
				t.Fatal("partial write acknowledged", result, err)
			}
			assertNoBlob(t, root, f)
			assertCloseoutMetadata(t, owner, service, key, command, false)
			if store.stageEntries != 1 || store.stageBytes != 1 {
				t.Fatal("actual partial byte not accounted")
			}
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, owner, service = closeoutService(t, root, db)
			if store.capacityKnown {
				t.Fatal("restart trusted unreconciled staging")
			}
			entries := closeoutInventory(t, service)
			if len(entries) != 1 || entries[0].Kind != StagingEntry || entries[0].Bytes != 1 || !entries[0].BlobID.IsZero() {
				t.Fatalf("restart retained inventory: %+v", entries)
			}
			if !store.capacityKnown || store.stageEntries != 1 || store.stageBytes != 1 || store.scan != nil {
				t.Fatal("completed scan did not reconcile exact retained capacity")
			}
			assertNoBlob(t, root, f)
			assertCloseoutMetadata(t, owner, service, key, command, false)
			result, err = service.Import(t.Context(), key, command, f.Open(), content.FiniteTAFSource)
			if err != nil || result.BlobID != command.BlobID() || result.CompleteBytes != f.CompleteBytes() {
				t.Fatal("valid retry failed", result, err)
			}
			assertCloseoutMetadata(t, owner, service, key, command, true)
			if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
				t.Fatal(err)
			}
			entries = closeoutInventory(t, service)
			var stages, canonical int
			for _, entry := range entries {
				switch entry.Kind {
				case StagingEntry:
					stages++
					if entry.Bytes != 1 {
						t.Fatal("retry changed retained partial bytes")
					}
				case ReferencedCanonical:
					canonical++
					if entry.BlobID != command.BlobID() || entry.Bytes != f.CompleteBytes() {
						t.Fatal("wrong canonical entry")
					}
				default:
					t.Fatal("unexpected retry entry", entry)
				}
			}
			if stages != 1 || canonical != 1 {
				t.Fatal("retry lost staging or duplicated publication", entries)
			}
		})
	}
}

type closeoutBlockedSource struct {
	content.TAFSource
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (s *closeoutBlockedSource) Close() error {
	s.calls.Add(1)
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.TAFSource.Close()
}

func TestBlobBlockedSourceCloseImportAndRetry(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			root := qualifiedRoot(t)
			store, owner, service := closeoutService(t, root, filepath.Join(t.TempDir(), "metadata.sqlite"))
			closeoutInventory(t, service)
			f := fixture(t, 5000)
			key, command := closeoutCommand(t, f)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 2*time.Second)
				defer deadlineCancel()
			}
			source := &closeoutBlockedSource{TAFSource: f.Open(), entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(source.release) }) }
			type outcome struct {
				result contentstore.ImportResult
				err    error
			}
			done := make(chan outcome, 1)
			finished := make(chan struct{})
			go func() {
				result, err := service.Import(ctx, key, command, source, content.FiniteTAFSource)
				done <- outcome{result, err}
				close(finished)
			}()
			// Always unblock and drain before owner/store cleanup, including failures.
			defer func() {
				release()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("import did not drain after source release")
				}
			}()
			select {
			case <-source.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("source Close not reached")
			}
			if mode == "cancel" {
				cancel()
			}
			if mode == "deadline" {
				<-ctx.Done() // actual deadline event; never sleep-based synchronization
			}
			select {
			case got := <-done:
				t.Fatal("import returned before source Close completed", got)
			default:
			}
			assertNoBlob(t, root, f)
			if source.calls.Load() != 1 {
				t.Fatal("source Close must be called exactly once")
			}
			if store.mu.TryLock() {
				store.mu.Unlock()
				t.Fatal("publisher released ownership while source Close blocked")
			}
			if _, _, err := service.LookupImport(t.Context(), key, command); !errors.Is(err, ErrBusy) {
				t.Fatal("application owner fence released during Close", err)
			}
			release()
			var got outcome
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("import did not finish")
			}
			wantSuccess := mode == "success"
			if wantSuccess {
				if got.err != nil || got.result.BlobID != command.BlobID() {
					t.Fatal("released source not published", got)
				}
			} else {
				// The SQLite owner exposes the caller's context error at the
				// application boundary; direct Publish uses ErrCanceled.
				wantErr := error(context.Canceled)
				if mode == "deadline" {
					wantErr = context.DeadlineExceeded
				}
				if !errors.Is(got.err, wantErr) || got.result != (contentstore.ImportResult{}) {
					t.Fatalf("canceled close: result=%+v error=%v (%T), want %v", got.result, got.err, got.err, wantErr)
				}
				assertNoBlob(t, root, f)
			}
			if source.calls.Load() != 1 {
				t.Fatal("source closed more than once")
			}
			assertCloseoutMetadata(t, owner, service, key, command, wantSuccess)
			retry, err := service.Import(t.Context(), key, command, f.Open(), content.FiniteTAFSource)
			if err != nil || retry.BlobID != command.BlobID() || wantSuccess && retry != got.result {
				t.Fatal("valid exact retry failed", retry, err)
			}
			assertCloseoutMetadata(t, owner, service, key, command, true)
			if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
				t.Fatal(err)
			}
			entries := closeoutInventory(t, service)
			var stages, canonical int
			for _, entry := range entries {
				if entry.Kind == StagingEntry && entry.Bytes == f.CompleteBytes() {
					stages++
				} else if entry.Kind == ReferencedCanonical && entry.BlobID == command.BlobID() {
					canonical++
				} else {
					t.Fatal("unexpected close/retry inventory", entry)
				}
			}
			if stages != 1 || canonical != 1 {
				t.Fatal("wrong retained/publication retry semantics", entries)
			}
			if _, err := os.Stat(blobPath(root, f)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
