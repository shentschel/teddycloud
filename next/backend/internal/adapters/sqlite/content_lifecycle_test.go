package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
)

func TestContentStoreRevokedSession(t *testing.T) {
	owner := blobOwner(t)
	c := blobCommand(t, 1, false)
	key := blobKey(t, 1)
	var escaped contentstore.Session
	var escapedRepository contentstore.BlobRepository
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		escaped = s
		return s.WithinTransaction(ctx, func(r contentstore.BlobRepository) error {
			escapedRepository = r
			if err := s.WithinTransaction(ctx, func(contentstore.BlobRepository) error { t.Fatal("recursive tx admitted"); return nil }); !errors.Is(err, contentstore.ErrBusy) {
				t.Fatalf("recursive tx: %v", err)
			}
			_, err := r.RecordImport(ctx, key, c)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := escapedRepository.LookupImport(t.Context(), key, c); !errors.Is(err, contentstore.ErrRevoked) {
		t.Fatal(err)
	}
	if _, err := escapedRepository.RecordImport(t.Context(), key, c); !errors.Is(err, contentstore.ErrRevoked) {
		t.Fatal(err)
	}
	if err := escaped.WithinTransaction(t.Context(), func(contentstore.BlobRepository) error { t.Fatal("escaped session admitted"); return nil }); !errors.Is(err, contentstore.ErrRevoked) {
		t.Fatal(err)
	}
	// A stale generation is rejected even if adapter-private scope is still set.
	stale := &contentSession{owner: owner, database: owner.database, generation: owner.generation.Load() + 1, ctx: t.Context(), active: true}
	if err := stale.WithinTransaction(t.Context(), func(contentstore.BlobRepository) error { t.Fatal("stale generation admitted"); return nil }); !errors.Is(err, contentstore.ErrRevoked) {
		t.Fatal(err)
	}
}

func TestContentStoreLifecycleFence(t *testing.T) {
	for _, lifecycle := range []string{"restore", "close"} {
		t.Run(lifecycle, func(t *testing.T) {
			owner := blobOwner(t)
			c := blobCommand(t, 1, false)
			key := blobKey(t, 1)
			snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			done := make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			var escaped contentstore.Session
			go func() {
				done <- owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
					escaped = s
					close(entered)
					<-release
					return s.WithinTransaction(ctx, func(r contentstore.BlobRepository) error { _, err := r.RecordImport(ctx, key, c); return err })
				})
			}()
			<-entered
			if err := owner.WithinContentOperation(t.Context(), func(context.Context, contentstore.Session) error {
				t.Fatal("second content operation admitted")
				return nil
			}); !errors.Is(err, contentstore.ErrBusy) {
				t.Fatal(err)
			}
			deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			if err := owner.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			cancel()
			lifecycleDone := make(chan error, 1)
			go func() {
				if lifecycle == "restore" {
					lifecycleDone <- owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite"))
				} else {
					lifecycleDone <- owner.Close(t.Context())
				}
			}()
			waitForLifecycleWaiter(t, owner)
			select {
			case err := <-lifecycleDone:
				t.Fatalf("fence failed: %v", err)
			default:
			}
			unlock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := <-lifecycleDone; err != nil {
				t.Fatal(err)
			}
			if err := escaped.WithinTransaction(t.Context(), func(contentstore.BlobRepository) error { t.Fatal("old session admitted"); return nil }); !errors.Is(err, contentstore.ErrRevoked) {
				t.Fatal(err)
			}
			if lifecycle == "restore" {
				if _, found, err := blobLookup(t, owner, key, c); err != nil || found {
					t.Fatalf("stale handle/receipt: %v %v", found, err)
				}
				if _, err := blobRecord(t, owner, key, c); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := owner.WithinContentOperation(t.Context(), func(context.Context, contentstore.Session) error { t.Fatal("closed owner admitted"); return nil }); !errors.Is(err, contentstore.ErrUnavailable) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestContentStoreNoGateReentry(t *testing.T) {
	owner := blobOwner(t)
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		if err := owner.WithinTransaction(ctx, func(applicationcatalog.ContentRepository) error { t.Fatal("catalog gate reentry"); return nil }); !errors.Is(err, applicationcatalog.ErrRepositoryContention) {
			t.Fatal(err)
		}
		if err := owner.WithinTagTransaction(ctx, func(applicationtag.TagRepository) error { t.Fatal("tag gate reentry"); return nil }); !errors.Is(err, applicationtag.ErrRepositoryContention) {
			t.Fatal(err)
		}
		if err := owner.WithinContentOperation(ctx, func(context.Context, contentstore.Session) error { t.Fatal("nested content gate"); return nil }); !errors.Is(err, contentstore.ErrBusy) {
			t.Fatal(err)
		}
		return s.WithinTransaction(ctx, func(contentstore.BlobRepository) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestContentStoreCanceledTransaction(t *testing.T) {
	owner := blobOwner(t)
	c := blobCommand(t, 1, false)
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		short, cancel := context.WithCancel(ctx)
		defer cancel()
		return s.WithinTransaction(short, func(r contentstore.BlobRepository) error {
			if _, err := r.RecordImport(short, blobKey(t, 1), c); err != nil {
				return err
			}
			cancel()
			return nil
		})
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
	if _, err := blobRecord(t, owner, blobKey(t, 1), c); err != nil {
		t.Fatal(err)
	}
	var timeout int
	if err := owner.database.db.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("wait budget leaked: %d %v", timeout, err)
	}
}

func TestContentStoreAdmissionAndTransactionBounds(t *testing.T) {
	owner := blobOwner(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	go func() {
		done <- owner.WithinTransaction(t.Context(), func(applicationcatalog.ContentRepository) error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err := owner.WithinContentOperation(ctx, func(context.Context, contentstore.Session) error { t.Fatal("admission exceeded deadline"); return nil })
	cancel()
	if !errors.Is(err, contentstore.ErrBusy) {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	err = owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > contentstore.OperationTimeout {
			t.Fatal("missing operation bound")
		}
		return s.WithinTransaction(ctx, func(repository contentstore.BlobRepository) error {
			r := repository.(*blobRepository)
			deadline, ok := r.ctx.Deadline()
			if !ok || time.Until(deadline) > contentstore.TransactionTimeout {
				t.Fatal("missing SQL/pool bound")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestContentStoreRepositoryDrainsBeforeReturn(t *testing.T) {
	owner := blobOwner(t)
	c := blobCommand(t, 1, false)
	key := blobKey(t, 1)
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		return s.WithinTransaction(ctx, func(repository contentstore.BlobRepository) error {
			if _, err := repository.RecordImport(ctx, key, c); err != nil {
				return err
			}
			r := repository.(*blobRepository)
			// Hold the same lock an already-admitted repository call owns.
			r.mutex.Lock()
			revoked := make(chan struct{})
			go func() { r.revoke(); close(revoked) }()
			select {
			case <-revoked:
				t.Fatal("repository failed to drain")
			default:
			}
			r.mutex.Unlock()
			<-revoked
			if _, _, err := r.LookupImport(ctx, key, c); !errors.Is(err, contentstore.ErrRevoked) {
				t.Fatal(err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}
