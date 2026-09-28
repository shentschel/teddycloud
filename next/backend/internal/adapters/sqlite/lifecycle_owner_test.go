package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

func TestLifecycleRestoreDrainsAndSwitchesBeforeLaterCallbacks(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.sqlite")
	backupPath := filepath.Join(directory, "snapshot.sqlite")
	destination := filepath.Join(directory, "restored.sqlite")
	migrations := SchemaMigrations()

	database, err := Open(t.Context(), Config{Path: source}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	snapshotContent := testContent(t, "Snapshot title", true)
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), snapshotContent)
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.CreateBackup(t.Context(), backupPath, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: source}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })

	heldEntered := make(chan struct{})
	releaseHeld := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHeld) }) }
	defer release()
	heldDone := make(chan error, 1)
	newerContent := testContent(t, "Committed after snapshot", true)
	go func() {
		heldDone <- owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			if err := repository.Save(t.Context(), newerContent); err != nil {
				return err
			}
			close(heldEntered)
			<-releaseHeld
			return nil
		})
	}()
	<-heldEntered
	assertSidecarsExist(t, source)

	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- owner.Restore(t.Context(), snapshot, destination)
	}()
	waitForLifecycleWaiter(t, owner)

	var timedCallbackEntered atomic.Bool
	waitContext, cancelWait := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancelWait()
	if err := owner.WithinTransaction(waitContext, func(applicationcatalog.ContentRepository) error {
		timedCallbackEntered.Store(true)
		return nil
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded transaction error = %v, want deadline", err)
	}
	if timedCallbackEntered.Load() {
		t.Fatal("deadline-bounded transaction entered its callback")
	}

	laterEntered := make(chan struct{})
	laterDone := make(chan error, 1)
	go func() {
		laterDone <- owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			content, found, err := repository.FindByID(t.Context(), snapshotContent.ID())
			if err != nil {
				return err
			}
			if !found || content.Facts().Title() != "Snapshot title" {
				return errors.New("callback did not observe restored snapshot")
			}
			close(laterEntered)
			return nil
		})
	}()
	select {
	case <-laterEntered:
		t.Fatal("later callback entered before restore drained and switched")
	default:
	}

	release()
	if err := <-heldDone; err != nil {
		t.Fatalf("held transaction: %v", err)
	}
	if err := <-restoreDone; err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := <-laterDone; err != nil {
		t.Fatalf("post-restore transaction: %v", err)
	}
	assertSidecarsExist(t, source)

	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != snapshot.SchemaVersion {
		t.Fatalf("selected version = %d, %v; want %d", version, err, snapshot.SchemaVersion)
	}
	assertOwnerTitle(t, owner, "Snapshot title")
	assertStoredTitle(t, source, migrations, "Committed after snapshot")
}

func TestLifecycleGateBoundsVersionRestoreAndClose(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	heldEntered := make(chan struct{})
	releaseHeld := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHeld) }) }
	defer release()
	heldDone := make(chan error, 1)
	go func() {
		heldDone <- owner.WithinTransaction(t.Context(), func(applicationcatalog.ContentRepository) error {
			close(heldEntered)
			<-releaseHeld
			return nil
		})
	}()
	<-heldEntered

	for _, call := range []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "version", run: func(ctx context.Context) error {
			_, err := owner.CurrentVersion(ctx)
			return err
		}},
		{name: "restore", run: func(ctx context.Context) error {
			return owner.Restore(ctx, BackupFile{}, filepath.Join(t.TempDir(), "unused.sqlite"))
		}},
		{name: "close", run: owner.Close},
	} {
		t.Run(call.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if err := call.run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("gate error = %v, want deadline", err)
			}
		})
	}

	release()
	if err := <-heldDone; err != nil {
		t.Fatal(err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != len(SchemaMigrations()) {
		t.Fatalf("owner did not recover after canceled wait: version %d, %v", version, err)
	}
}

func TestLifecycleRestorePreflightFailuresKeepCurrentUsable(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, string, string, BackupFile) BackupFile
		want    error
	}{
		{
			name: "same destination",
			prepare: func(_ *testing.T, _, _ string, snapshot BackupFile) BackupFile {
				return snapshot
			},
			want: ErrRestoreSameDestination,
		},
		{
			name: "existing destination",
			prepare: func(t *testing.T, _, destination string, snapshot BackupFile) BackupFile {
				if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				return snapshot
			},
			want: ErrRestoreExists,
		},
		{
			name: "stale wal",
			prepare: func(t *testing.T, _, destination string, snapshot BackupFile) BackupFile {
				if err := os.WriteFile(destination+"-wal", []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				return snapshot
			},
			want: ErrRestoreDestinationSidecar,
		},
		{
			name: "stale shm",
			prepare: func(t *testing.T, _, destination string, snapshot BackupFile) BackupFile {
				if err := os.WriteFile(destination+"-shm", []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				return snapshot
			},
			want: ErrRestoreDestinationSidecar,
		},
		{
			name: "tampered snapshot",
			prepare: func(t *testing.T, _, _ string, snapshot BackupFile) BackupFile {
				if err := os.WriteFile(snapshot.Path, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
				return snapshot
			},
			want: ErrBackupInvalid,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner, snapshot, source, destination := lifecycleFixture(t)
			if test.name == "same destination" {
				destination = source
			}
			snapshot = test.prepare(t, source, destination, snapshot)
			if err := owner.Restore(t.Context(), snapshot, destination); !errors.Is(err, test.want) {
				t.Fatalf("restore error = %v, want %v", err, test.want)
			}
			if version, err := owner.CurrentVersion(t.Context()); err != nil || version != len(SchemaMigrations()) {
				t.Fatalf("current owner unusable: version %d, %v", version, err)
			}
			assertOwnerTitle(t, owner, "Current title")
		})
	}
}

func TestLifecycleRestoreFailureAfterCloseFailsClosedAndKeepsOriginal(t *testing.T) {
	owner, snapshot, source, _ := lifecycleFixture(t)
	destination := filepath.Join(t.TempDir(), "missing", "restored.sqlite")
	if err := owner.Restore(t.Context(), snapshot, destination); !errors.Is(err, ErrLifecycleRestore) {
		t.Fatalf("restore error = %v, want %v", err, ErrLifecycleRestore)
	}
	if _, err := owner.CurrentVersion(t.Context()); !errors.Is(err, ErrLifecycleClosed) {
		t.Fatalf("version after failed switch = %v, want closed", err)
	}
	var entered atomic.Bool
	err := owner.WithinTransaction(t.Context(), func(applicationcatalog.ContentRepository) error {
		entered.Store(true)
		return nil
	})
	assertApplicationRepositoryFailure(t, err)
	if entered.Load() {
		t.Fatal("callback entered after failed switch")
	}
	assertStoredTitle(t, source, SchemaMigrations(), "Current title")
}

func TestLifecycleRestoreReopensExactSnapshotSchema(t *testing.T) {
	directory := t.TempDir()
	migrations := testMigrations()
	versionOnePath := filepath.Join(directory, "version-one.sqlite")
	snapshotPath := filepath.Join(directory, "snapshot.sqlite")
	currentPath := filepath.Join(directory, "current.sqlite")
	restoredPath := filepath.Join(directory, "restored.sqlite")

	versionOne, err := Open(t.Context(), Config{Path: versionOnePath}, migrations[:1])
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := versionOne.CreateBackup(t.Context(), snapshotPath, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := versionOne.Close(); err != nil {
		t.Fatal(err)
	}

	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: currentPath}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 2 {
		t.Fatalf("initial version = %d, %v", version, err)
	}
	if err := owner.Restore(t.Context(), snapshot, restoredPath); err != nil {
		t.Fatal(err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 1 {
		t.Fatalf("restored version = %d, %v; snapshot was not reopened exactly", version, err)
	}
}

func TestLifecycleTransactionSanitizesRepositoryAndDriverFailures(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	if _, err := owner.database.db.ExecContext(t.Context(), `DROP TABLE tc_catalog_content`); err != nil {
		t.Fatal(err)
	}
	err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		_, _, err := repository.FindByID(t.Context(), testContent(t, "Missing", false).ID())
		return err
	})
	assertApplicationRepositoryFailure(t, err)

	if err := owner.database.Close(); err != nil {
		t.Fatal(err)
	}
	entered := false
	err = owner.WithinTransaction(t.Context(), func(applicationcatalog.ContentRepository) error {
		entered = true
		return errors.New("must not enter")
	})
	if entered {
		t.Fatal("callback entered through a closed lifecycle database")
	}
	assertApplicationRepositoryFailure(t, err)
}

func lifecycleFixture(t *testing.T) (*LifecycleOwner, BackupFile, string, string) {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "source.sqlite")
	backupPath := filepath.Join(directory, "snapshot.sqlite")
	destination := filepath.Join(directory, "restored.sqlite")
	migrations := SchemaMigrations()
	database, err := Open(t.Context(), Config{Path: source}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	snapshotContent := testContent(t, "Snapshot title", true)
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), snapshotContent)
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.CreateBackup(t.Context(), backupPath, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: source}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	currentContent := testContent(t, "Current title", true)
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), currentContent)
	}); err != nil {
		t.Fatal(err)
	}
	return owner, snapshot, source, destination
}

func openLifecycleApplicationOwner(t *testing.T) *LifecycleOwner {
	t.Helper()
	owner, err := OpenLifecycleOwner(
		t.Context(),
		Config{Path: filepath.Join(t.TempDir(), "application.sqlite")},
		SchemaMigrations(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	return owner
}

func assertOwnerTitle(t *testing.T, owner *LifecycleOwner, title string) {
	t.Helper()
	want := testContent(t, title, true)
	var got domaincatalog.Content
	var found bool
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		var err error
		got, found, err = repository.FindByID(t.Context(), want.ID())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !found || got.Facts().Title() != title {
		t.Fatalf("owner title = %q, found %t; want %q", got.Facts().Title(), found, title)
	}
}

func assertSidecarsExist(t *testing.T, path string) {
	t.Helper()
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if info, err := os.Stat(sidecar); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("sidecar %s was not preserved: %v", filepath.Base(sidecar), err)
		}
	}
}

func waitForLifecycleWaiter(t *testing.T, owner *LifecycleOwner) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		owner.gate.mutex.Lock()
		waiters := owner.gate.lifecycleWaiters
		owner.gate.mutex.Unlock()
		if waiters > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("lifecycle operation did not wait for the active transaction")
		}
		time.Sleep(time.Millisecond)
	}
}

func containsStorageDetail(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sql logic") ||
		strings.Contains(message, "no such table") ||
		strings.Contains(message, "database is closed")
}
