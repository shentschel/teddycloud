package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

func TestTagTransactionRollbackAndScope(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			value := testRegistryTag(t, '0', domaintag.UID{})
			failure := errors.New("caller failure")
			var escaped applicationtag.TagRepository
			calls := 0
			func() {
				if outcome == "panic" {
					defer func() {
						if recover() != failure {
							t.Fatal("panic not preserved")
						}
					}()
				}
				err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error {
					calls++
					escaped = r
					if err := r.Insert(t.Context(), value); err != nil {
						return err
					}
					if outcome == "panic" {
						panic(failure)
					}
					if outcome == "rollback" {
						return failure
					}
					return nil
				})
				if outcome == "commit" && err != nil {
					t.Fatal(err)
				}
				if outcome == "rollback" && err != failure {
					t.Fatalf("callback error not preserved: %v", err)
				}
			}()
			if calls != 1 {
				t.Fatalf("callback calls = %d", calls)
			}
			assertTagError(t, escaped.Insert(t.Context(), value), applicationtag.ErrRepositoryUnavailable)
			_, _, err := escaped.FindByID(t.Context(), value.ID())
			assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
			_, _, err = escaped.FindByUID(t.Context(), value.UID())
			assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
			got, err := applicationtag.New(owner).FindByID(t.Context(), value.ID().String())
			if outcome == "commit" {
				if err != nil || !got.Equal(value) {
					t.Fatalf("commit lost: %v", err)
				}
			} else {
				assertTagError(t, err, applicationtag.ErrTagNotFound)
			}
		})
	}
}

func TestTagTransactionUnavailableAndContext(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	assertTagError(t, owner.WithinTagTransaction(t.Context(), nil), applicationtag.ErrInvalidInput)
	var absent *LifecycleOwner
	assertTagError(t, absent.WithinTagTransaction(t.Context(), func(applicationtag.TagRepository) error { t.Fatal("nil owner callback"); return nil }), applicationtag.ErrRepositoryUnavailable)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assertTagError(t, owner.WithinTagTransaction(ctx, func(applicationtag.TagRepository) error { t.Fatal("canceled callback"); return nil }), context.Canceled)
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertTagError(t, owner.WithinTagTransaction(t.Context(), func(applicationtag.TagRepository) error { t.Fatal("closed callback"); return nil }), applicationtag.ErrRepositoryUnavailable)
}

func TestTagTransactionSanitizesCombinedFailures(t *testing.T) {
	err := tagBoundaryError(t.Context(), errors.Join(
		applicationtag.ErrIdentityConflict,
		applicationcatalog.ErrRepositoryUnavailable,
		errors.New("tc_tags sqlite storage diagnostic"),
	))
	assertTagError(t, err, applicationtag.ErrIdentityConflict)
	assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
	if errors.Is(err, applicationcatalog.ErrRepositoryUnavailable) {
		t.Fatal("catalog taxonomy crossed Tag port")
	}
}

func TestTagTransactionContentionAndCancellation(t *testing.T) {
	for _, cancelCall := range []bool{false, true} {
		t.Run(map[bool]string{false: "contention", true: "cancellation"}[cancelCall], func(t *testing.T) {
			first, second := openContentionPair(t, 100*time.Millisecond)
			value := testRegistryTag(t, '0', domaintag.UID{})
			held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			defer finish()
			go func() {
				done <- first.withinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error {
					if err := r.Insert(t.Context(), value); err != nil {
						return err
					}
					close(held)
					<-release
					return nil
				})
			}()
			waitForWriteLock(t, held, done)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelCall {
				timer := time.AfterFunc(20*time.Millisecond, cancel)
				defer timer.Stop()
			}
			calls := 0
			err := second.withinTagTransaction(ctx, func(r applicationtag.TagRepository) error { calls++; return r.Insert(ctx, value) })
			want := applicationtag.ErrRepositoryContention
			if cancelCall {
				want = context.Canceled
			}
			assertTagError(t, err, want)
			if calls != 1 {
				t.Fatalf("callback replayed: %d", calls)
			}
			finish()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var timeout int
			if err := second.db.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 100 {
				t.Fatalf("timeout = %d, %v", timeout, err)
			}
			if err := second.withinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error {
				_, _, err := r.FindByID(t.Context(), value.ID())
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTagTransactionLifecycleGateAndSelectedHandle(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	value := testRegistryTag(t, '0', domaintag.UID{})
	if err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), value) }); err != nil {
		t.Fatal(err)
	}
	content := testContent(t, "Snapshot content", false)
	if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error { return r.Save(t.Context(), content) }); err != nil {
		t.Fatal(err)
	}
	snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	newer := testRegistryTag(t, '1', domaintag.UIDFromBytes([8]byte{1}))
	held, release, heldDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	go func() {
		heldDone <- owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error {
			if err := r.Insert(t.Context(), newer); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	waitForWriteLock(t, held, heldDone)
	for _, call := range []func(context.Context) error{
		func(ctx context.Context) error {
			return owner.WithinTagTransaction(ctx, func(applicationtag.TagRepository) error { t.Error("Tag crossed held gate"); return nil })
		},
		func(ctx context.Context) error {
			return owner.WithinTransaction(ctx, func(applicationcatalog.ContentRepository) error { t.Error("Content crossed held gate"); return nil })
		},
		owner.Close,
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		err := call(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("gate error = %v", err)
		}
	}
	restored := make(chan error, 1)
	go func() {
		restored <- owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite"))
	}()
	waitForLifecycleWaiter(t, owner)
	later := make(chan error, 1)
	go func() { _, err := applicationtag.New(owner).FindByID(t.Context(), newer.ID().String()); later <- err }()
	finish()
	if err := <-heldDone; err != nil {
		t.Fatal(err)
	}
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	assertTagError(t, <-later, applicationtag.ErrTagNotFound)
	got, err := applicationtag.New(owner).FindByID(t.Context(), value.ID().String())
	if err != nil || !got.Equal(value) {
		t.Fatalf("restored Tag = %v", err)
	}
	if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		got, found, err := r.FindByID(t.Context(), content.ID())
		if err == nil && (!found || got.Facts().Title() != content.Facts().Title()) {
			t.Fatal("restored Content missing")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTagTransactionPreTagSchemaRemainsUnavailable(t *testing.T) {
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v1.sqlite")}, SchemaMigrations()[:1])
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	value := testRegistryTag(t, '0', domaintag.UID{})
	for _, run := range []func(applicationtag.TagRepository) error{
		func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), value) },
		func(r applicationtag.TagRepository) error {
			_, _, err := r.FindByID(t.Context(), value.ID())
			return err
		},
	} {
		assertTagError(t, owner.WithinTagTransaction(t.Context(), run), applicationtag.ErrRepositoryUnavailable)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 1 {
		t.Fatalf("schema changed: %d, %v", version, err)
	}
}
