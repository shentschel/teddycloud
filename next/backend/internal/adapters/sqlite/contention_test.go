package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

func TestContentionIsBoundedStableAndDoesNotReplayOperation(t *testing.T) {
	const busyTimeout = 40 * time.Millisecond
	first, second := openContentionPair(t, busyTimeout)
	content := testContent(t, "Contended", false)
	holderRollback := errors.New("holder rollback")
	locked := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)

	go func() {
		holderDone <- first.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			if err := repository.Save(t.Context(), content); err != nil {
				return err
			}
			close(locked)
			<-release
			return holderRollback
		})
	}()
	waitForWriteLock(t, locked, holderDone)

	operationCalls := 0
	started := time.Now()
	err := second.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		operationCalls++
		return repository.Save(t.Context(), content)
	})
	elapsed := time.Since(started)

	close(release)
	if holderErr := <-holderDone; !errors.Is(holderErr, holderRollback) {
		t.Fatalf("holder transaction error = %v, want %v", holderErr, holderRollback)
	}
	if !errors.Is(err, applicationcatalog.ErrRepositoryContention) {
		t.Fatalf("contended save error = %v, want %v", err, applicationcatalog.ErrRepositoryContention)
	}
	if operationCalls != 1 {
		t.Fatalf("operation calls = %d, want 1", operationCalls)
	}
	if elapsed < busyTimeout/2 || elapsed > time.Second {
		t.Fatalf("contention wait = %s, want bounded wait near %s", elapsed, busyTimeout)
	}

	assertContentAbsent(t, second, content.ID())
	if err := second.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), content)
	}); err != nil {
		t.Fatalf("save after contention: %v", err)
	}
}

func TestContentionHonorsContextCancellation(t *testing.T) {
	first, second := openContentionPair(t, 2*time.Second)
	content := testContent(t, "Canceled", false)
	holderRollback := errors.New("holder rollback")
	locked := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)

	go func() {
		holderDone <- first.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			if err := repository.Save(t.Context(), content); err != nil {
				return err
			}
			close(locked)
			<-release
			return holderRollback
		})
	}()
	waitForWriteLock(t, locked, holderDone)

	ctx, cancel := context.WithCancel(t.Context())
	cancelTimer := time.AfterFunc(40*time.Millisecond, cancel)
	operationCalls := 0
	started := time.Now()
	err := second.WithinTransaction(ctx, func(repository applicationcatalog.ContentRepository) error {
		operationCalls++
		return repository.Save(ctx, content)
	})
	elapsed := time.Since(started)
	cancelTimer.Stop()
	cancel()

	close(release)
	if holderErr := <-holderDone; !errors.Is(holderErr, holderRollback) {
		t.Fatalf("holder transaction error = %v, want %v", holderErr, holderRollback)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error = %v, want %v", err, context.Canceled)
	}
	if errors.Is(err, applicationcatalog.ErrRepositoryContention) {
		t.Fatalf("canceled save error = %v, unexpectedly classified as contention", err)
	}
	if operationCalls != 1 {
		t.Fatalf("operation calls = %d, want 1", operationCalls)
	}
	if elapsed > time.Second {
		t.Fatalf("context cancellation took %s, want less than 1s", elapsed)
	}
	assertContentAbsent(t, second, content.ID())
	var gotTimeout int
	if err := second.db.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&gotTimeout); err != nil {
		t.Fatal(err)
	}
	if gotTimeout != 2000 {
		t.Fatalf("busy timeout after cancellation = %d, want 2000", gotTimeout)
	}
}

// Cancel at the two return boundaries instead of relying on a timer landing
// between SQLite's BUSY response, error mapping and transaction rollback.
func TestContentionCancellationDominatesReturnBoundaries(t *testing.T) {
	for _, boundary := range []string{"error mapping", "callback return"} {
		t.Run(boundary, func(t *testing.T) {
			first, second := openContentionPair(t, 40*time.Millisecond)
			content := testContent(t, "Canceled at return boundary", false)
			holder, err := first.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = holder.Rollback() })
			if _, err := holder.ExecContext(t.Context(), "UPDATE tc_catalog_content SET title = title"); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			var operationErr error
			err = second.withinSQLTransaction(ctx, func(transaction *sql.Tx) error {
				calls++
				if boundary == "error mapping" {
					operationErr = execWithContention(ctx, transaction, func(ctx context.Context, err error) error {
						mapped := repositoryError(ctx, err)
						if errors.Is(mapped, applicationcatalog.ErrRepositoryContention) {
							cancel()
						}
						return mapped
					}, "UPDATE tc_catalog_content SET title = title")
				} else {
					operationErr = (contentRepository{transaction: transaction}).Save(ctx, content)
					if !errors.Is(operationErr, applicationcatalog.ErrRepositoryContention) {
						t.Errorf("uncanceled save error = %v, want contention", operationErr)
					}
					cancel()
				}
				return operationErr
			})
			if boundary == "error mapping" && !errors.Is(operationErr, context.Canceled) {
				t.Errorf("operation error = %v, want cancellation", operationErr)
			}
			if !errors.Is(err, context.Canceled) || errors.Is(err, applicationcatalog.ErrRepositoryContention) || errors.Is(err, applicationcatalog.ErrRepositoryUnavailable) {
				t.Errorf("transaction error = %v, want cancellation without contention or cleanup classifications", err)
			}
			if calls != 1 {
				t.Errorf("callback calls = %d, want 1", calls)
			}
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			assertContentAbsent(t, second, content.ID())
			var timeout int
			if err := second.db.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
				t.Fatal(err)
			}
			if timeout != 40 {
				t.Errorf("busy timeout = %d, want 40", timeout)
			}
			if err := second.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
				return repository.Save(t.Context(), content)
			}); err != nil {
				t.Fatalf("save after canceled transaction: %v", err)
			}
		})
	}
}

func openContentionPair(t *testing.T, busyTimeout time.Duration) (*Database, *Database) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contention.sqlite")
	config := Config{Path: path, BusyTimeout: busyTimeout}

	first, err := Open(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatalf("open first database handle: %v", err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close first database handle: %v", err)
		}
	})

	second, err := Open(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatalf("open second database handle: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second database handle: %v", err)
		}
	})
	return first, second
}

func waitForWriteLock(t *testing.T, locked <-chan struct{}, holderDone <-chan error) {
	t.Helper()
	select {
	case <-locked:
	case err := <-holderDone:
		t.Fatalf("holder transaction ended before acquiring lock: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for holder write lock")
	}
}
