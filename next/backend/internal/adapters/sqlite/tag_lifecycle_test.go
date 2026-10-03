package sqlite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

func TestTagTransactionFailures(t *testing.T) {
	first, second := openContentionPair(t, 75*time.Millisecond)
	contender := testRegistryTag(t, '1', domaintag.UIDFromBytes([8]byte{1}))
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()

	go func() {
		done <- first.withinTagTransaction(t.Context(), func(repository applicationtag.TagRepository) error {
			if err := repository.Insert(t.Context(), testRegistryTag(t, '0', domaintag.UID{})); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	waitForWriteLock(t, held, done)

	for _, test := range []struct {
		name   string
		ctx    func() (context.Context, context.CancelFunc)
		want   error
		called int
	}{
		{
			name: "bounded contention executes callback once",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(t.Context(), time.Second)
			},
			want:   applicationtag.ErrRepositoryContention,
			called: 1,
		},
		{
			name: "canceled before admission never executes callback",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx, func() {}
			},
			want: context.Canceled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.ctx()
			defer cancel()
			calls := 0
			err := second.withinTagTransaction(ctx, func(repository applicationtag.TagRepository) error {
				calls++
				return repository.Insert(ctx, contender)
			})
			assertTagError(t, err, test.want)
			if calls != test.called {
				t.Fatalf("callback calls = %d, want %d", calls, test.called)
			}
		})
	}

	finish()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := second.withinTagTransaction(t.Context(), func(applicationtag.TagRepository) error { return nil }); err != nil {
		t.Fatalf("subsequent access did not recover: %v", err)
	}
}

func TestTagLifecycleFence(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	service := applicationtag.New(owner)
	snapshotTag := registerMetadataTag(t, service)
	snapshotContent := testContent(t, "snapshot Content", true)
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), snapshotContent)
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := owner.database.CreateBackup(t.Context(), t.TempDir()+"/snapshot.sqlite", SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	newerTag := testRegistryTag(t, '1', domaintag.UIDFromBytes([8]byte{1}))
	if err := owner.WithinTagTransaction(t.Context(), func(repository applicationtag.TagRepository) error {
		return repository.Insert(t.Context(), newerTag)
	}); err != nil {
		t.Fatal(err)
	}
	newerContent := testContent(t, "newer Content", true)
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), newerContent)
	}); err != nil {
		t.Fatal(err)
	}

	held, release, heldDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		heldDone <- owner.WithinTagTransaction(t.Context(), func(applicationtag.TagRepository) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	restored := make(chan error, 1)
	go func() { restored <- owner.Restore(t.Context(), snapshot, t.TempDir()+"/restored.sqlite") }()
	waitForLifecycleWaiter(t, owner)

	tagCalls, contentCalls := 0, 0
	tagDone := make(chan error, 1)
	contentDone := make(chan error, 1)
	go func() {
		tagDone <- owner.WithinTagTransaction(t.Context(), func(repository applicationtag.TagRepository) error {
			tagCalls++
			_, found, err := repository.FindByID(t.Context(), newerTag.ID())
			if err != nil {
				return err
			}
			if found {
				return fmt.Errorf("queued Tag callback used the pre-restore handle")
			}
			return nil
		})
	}()
	go func() {
		contentDone <- owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			contentCalls++
			got, found, err := repository.FindByID(t.Context(), newerContent.ID())
			if err != nil {
				return err
			}
			if !found || got.Facts().Title() != snapshotContent.Facts().Title() {
				return fmt.Errorf("queued Content callback used the pre-restore handle")
			}
			return nil
		})
	}()

	close(release)
	if err := <-heldDone; err != nil {
		t.Fatal(err)
	}
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	if err := <-tagDone; err != nil {
		t.Fatal(err)
	}
	if err := <-contentDone; err != nil {
		t.Fatal(err)
	}
	if tagCalls != 1 || contentCalls != 1 {
		t.Fatalf("post-restore callbacks = tag %d, content %d", tagCalls, contentCalls)
	}
	got, err := applicationtag.New(owner).FindByID(t.Context(), snapshotTag.ID().String())
	if err != nil || !got.Equal(snapshotTag) {
		t.Fatalf("selected handle did not contain snapshot Tag: %v", err)
	}
}
