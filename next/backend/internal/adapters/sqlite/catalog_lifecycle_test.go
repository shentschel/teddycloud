package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	tagapp "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
)

func TestCatalogLifecycleFence(t *testing.T) {
	for _, action := range []string{"restore", "close"} {
		t.Run(action, func(t *testing.T) {
			f := catalogRecoveryCurrent(t)
			owner := f.owner
			before := catalogRecoveryRows(t, owner.database.db)
			snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.WithinTransaction(t.Context(), func(r app.ContentRepository) error {
				return r.Save(t.Context(), testContent(t, "committed after lifecycle snapshot", true))
			}); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
				t.Fatal("post-snapshot mutation missing")
			}
			query := app.Query{Kind: app.ListVersions, Content: f.subject.ContentID(), Page: app.PageRequest{Limit: 1}}
			page, err := catalogRead(t, owner, query)
			if err != nil || page.Next == "" {
				t.Fatal("cursor fixture", err)
			}
			query.Page.Cursor, query.Page.ExpectedRevision = page.Next, page.Revision
			if _, err := catalogRead(t, owner, query); err != nil {
				t.Fatal("cursor invalid before lifecycle", err)
			}
			// Keep the old generation artificially active to distinguish generation
			// fencing from the ordinary callback-scope revocation check.
			stale := &contentSession{owner: owner, database: owner.database, generation: owner.generation.Load(), ctx: t.Context(), active: true}
			var escaped app.CatalogRepository
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			go func() {
				done <- owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
					escaped = r
					close(entered)
					select {
					case <-release:
					case <-t.Context().Done():
						return t.Context().Err()
					}
					_, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
					if err == nil && !found {
						return fmt.Errorf("active catalog handle lost its order state")
					}
					return err
				})
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatal("catalog admission", err)
			case <-time.After(2 * time.Second):
				t.Fatal("catalog admission timed out")
			}
			destination := filepath.Join(t.TempDir(), "restored.sqlite")
			lifecycle := func(ctx context.Context) error {
				if action == "restore" {
					return owner.Restore(ctx, snapshot, destination)
				}
				return owner.Close(ctx)
			}
			bounded, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			started := time.Now()
			err = lifecycle(bounded)
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
				t.Fatal("lifecycle wait was not bounded", err)
			}
			if owner.database != stale.database || owner.generation.Load() != stale.generation {
				t.Fatal("canceled lifecycle replaced an active handle")
			}
			finished := make(chan error, 1)
			go func() { finished <- lifecycle(t.Context()) }()
			waitForLifecycleWaiter(t, owner)
			// Cancellation behind a pending lifecycle must execute no callback.
			ctx, stop := context.WithTimeout(t.Context(), 30*time.Millisecond)
			canceledCalls := 0
			err = owner.WithinCatalogTransaction(ctx, func(app.CatalogRepository) error {
				canceledCalls++
				return nil
			})
			stop()
			if !errors.Is(err, context.DeadlineExceeded) || canceledCalls != 0 {
				t.Fatal("queued catalog cancellation", err)
			}
			queued := make(chan error, 3)
			check := func() error {
				if owner.database == stale.database || owner.generation.Load() == stale.generation {
					return fmt.Errorf("queued callback used old lifecycle handle")
				}
				return nil
			}
			go func() {
				queued <- owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
					if err := check(); err != nil {
						return err
					}
					s, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
					if err == nil && (!found || s.AggregateRevision.Uint64() != 5) {
						return fmt.Errorf("restored order revision differs")
					}
					return err
				})
			}()
			go func() {
				queued <- owner.WithinTagTransaction(t.Context(), func(tagapp.TagRepository) error { return check() })
			}()
			go func() {
				queued <- owner.WithinTransaction(t.Context(), func(app.ContentRepository) error { return check() })
			}()
			select {
			case err := <-finished:
				t.Fatal("lifecycle bypassed active catalog callback", err)
			default:
			}
			unlock()
			if err := catalogFenceResult(t, done); err != nil {
				t.Fatal("active callback", err)
			}
			if err := catalogFenceResult(t, finished); err != nil {
				t.Fatal("lifecycle", err)
			}
			for i := 0; i < 3; i++ {
				err := catalogFenceResult(t, queued)
				if action == "restore" && err != nil {
					t.Fatal("queued callback", err)
				}
				if action == "close" && !errors.Is(err, app.ErrRepositoryUnavailable) && !errors.Is(err, tagapp.ErrRepositoryUnavailable) && !errors.Is(err, ErrLifecycleClosed) {
					t.Fatal("closed owner did not reject queued callback with unavailable error", err)
				}
			}
			if _, _, err := escaped.LoadOrder(t.Context(), f.subject.ContentID()); err != app.ErrRepositoryUnavailable {
				t.Fatal("escaped catalog repository", err)
			}
			if err := stale.WithinTransaction(t.Context(), func(contentstore.BlobRepository) error { t.Error("old session invoked callback"); return nil }); err != contentstore.ErrRevoked {
				t.Fatal("stale session", err)
			}
			if action == "restore" {
				if _, err := catalogRead(t, owner, query); err != app.ErrStaleCursor {
					t.Fatal("restore accepted old cursor", err)
				}
				if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
					t.Fatal("restore or queued reads changed committed facts")
				}
			} else {
				if _, err := catalogRead(t, owner, query); err != app.ErrRepositoryUnavailable {
					t.Fatal("closed owner accepted cursor", err)
				}
			}
		})
	}
}

func catalogFenceResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle operation failed to finish within bound")
		return nil
	}
}
