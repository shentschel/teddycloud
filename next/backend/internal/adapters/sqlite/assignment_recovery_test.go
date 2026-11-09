package sqlite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

type assignmentRestoreInvalidator struct{ run func() error }

func (invalidator assignmentRestoreInvalidator) InvalidateInventory() error { return invalidator.run() }

func assignmentWaitForRestoreFence(t *testing.T, owner *LifecycleOwner) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		owner.gate.mutex.Lock()
		waiting := owner.gate.lifecycleWaiters > 0
		owner.gate.mutex.Unlock()
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("restore did not reach lifecycle fence")
		}
		runtime.Gosched()
	}
}

func assignmentMutateBackup(t *testing.T, snapshot BackupFile, failure string) BackupFile {
	t.Helper()
	if failure == "integrity" {
		raw, err := os.ReadFile(snapshot.Path)
		if err != nil {
			t.Fatal(err)
		}
		copy(raw[:16], "not a sqlite db!")
		if err := os.WriteFile(snapshot.Path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		raw := openRawDatabase(t, snapshot.Path)
		switch failure {
		case "assignment foreign key":
			catalogSchemaExec(t, raw, "PRAGMA foreign_keys=OFF")
			catalogSchemaExec(t, raw, "DROP TRIGGER tc_assignment_events_no_delete")
			catalogSchemaExec(t, raw, "DELETE FROM tc_assignment_events WHERE revision=1")
		case "assignment migration ledger":
			catalogSchemaExec(t, raw, "UPDATE tc_schema_migrations SET checksum='rejected' WHERE version=10")
		case "assignment retained ledger":
			catalogSchemaExec(t, raw, "DROP TRIGGER tc_assignment_commands_no_update")
			catalogSchemaExec(t, raw, "UPDATE tc_assignment_commands SET result_sha256=zeroblob(32)")
		default:
			t.Fatalf("unknown backup mutation %q", failure)
		}
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	snapshot.SHA256, err = fileDigest(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestAssignmentRestoreFence(t *testing.T) {
	t.Run("generation revokes live scope before commit", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		tx, err := f.db.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var generation atomic.Uint64
		scope := assignmentTransaction{generation: &generation}
		err = scope.run(t.Context(), tx, assignmentOwnerActor, func(port app.Port) error {
			if _, err := port.Authenticate(t.Context()); err != nil {
				t.Fatal("initial generation was not admitted", err)
			}
			generation.Add(1)
			if _, err := port.Authenticate(t.Context()); !errors.Is(err, app.ErrUnavailable) {
				t.Fatal("restored generation did not revoke live session", err)
			}
			return nil
		})
		if !errors.Is(err, app.ErrUnavailable) {
			t.Fatal("revoked generation reached transaction finalization", err)
		}
	})

	t.Run("current schema switch is one revocation boundary", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		owner, service, source := assignmentActivateOwner(t, f)
		first := f.command(t, 1, app.Assign, 0)
		one := f.execute(t, first)
		second := f.command(t, 2, app.RecordClassification, 1)
		two := f.execute(t, second)
		later := f.command(t, 3, app.Unassign, 2)
		query := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: later.EffectiveAt}, Limit: 1}
		oldPage := assignmentHistoryPage(t, service, query, one)
		if oldPage.Next == "" {
			t.Fatal("restore fixture did not issue a cursor")
		}
		oldContinuation := query
		oldContinuation.Cursor = oldPage.Next
		wantView, err := service.GetAssignment(t.Context(), query.EffectiveQuery)
		if err != nil {
			t.Fatal(err)
		}
		wantRows := registrationSchemaRows(t, owner.database.db)
		snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "current.sqlite"), SchemaMigrations())
		if err != nil || snapshot.SchemaVersion != 10 {
			t.Fatal(snapshot, err)
		}
		three := f.execute(t, later)
		media := filepath.Join(t.TempDir(), "external-media.taf")
		mediaBytes := []byte("restore must not touch external media")
		if err := os.WriteFile(media, mediaBytes, 0o600); err != nil {
			t.Fatal(err)
		}

		entered := make(chan struct{})
		release := make(chan struct{})
		sessionDone := make(chan error, 1)
		var oldSession app.Port
		go func() {
			sessionDone <- owner.WithinAssignmentTransaction(t.Context(), func(port app.Port) error {
				oldSession = port
				close(entered)
				<-release
				return nil
			})
		}()
		<-entered
		restoredPath := filepath.Join(t.TempDir(), "restored-current.sqlite")
		restoreDone := make(chan error, 1)
		go func() { restoreDone <- owner.Restore(t.Context(), snapshot, restoredPath) }()
		assignmentWaitForRestoreFence(t, owner)
		select {
		case err := <-restoreDone:
			t.Fatal("restore crossed a live assignment session", err)
		default:
		}
		close(release)
		if err := <-sessionDone; err != nil {
			t.Fatal("drained assignment session", err)
		}
		if err := <-restoreDone; err != nil {
			t.Fatal("restore current snapshot", err)
		}
		if _, err := oldSession.Authenticate(t.Context()); !errors.Is(err, app.ErrUnavailable) {
			t.Fatal("pre-restore session remained live", err)
		}
		assignmentHistoryError(t, service, oldContinuation, app.ErrStaleCursor)
		if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 10 {
			t.Fatal("current-schema restore version", version, err)
		}
		if got := registrationSchemaRows(t, owner.database.db); !reflect.DeepEqual(got, wantRows) {
			t.Fatal("current-schema restore changed retained rows or receipts")
		}
		if got, err := service.GetAssignment(t.Context(), query.EffectiveQuery); err != nil || !reflect.DeepEqual(got, wantView) {
			t.Fatal("current-schema restore changed exact lookup or references", err)
		}
		for _, receipt := range []struct {
			command app.Command
			result  app.Result
			found   bool
		}{{first, one, true}, {second, two, true}, {later, app.Result{}, false}} {
			assignmentAssertReceipt(t, service, receipt.command, receipt.result, receipt.found)
		}
		if got, err := service.Execute(t.Context(), first); err != nil || got != one {
			t.Fatal("restored exact replay", got, err)
		}
		fresh := assignmentHistoryPage(t, service, query, one)
		if fresh.HeadRevision != 2 || fresh.Next == "" {
			t.Fatal("restored history did not report the snapshot head", fresh)
		}
		query.Cursor = fresh.Next
		if page := assignmentHistoryPage(t, service, query, two); page.Next != "" || page.HeadRevision != 2 {
			t.Fatal("restored history continuation", page)
		}
		if got, err := os.ReadFile(media); err != nil || !bytes.Equal(got, mediaBytes) {
			t.Fatal("external media changed", err)
		}
		newer, err := Open(t.Context(), Config{Path: source}, SchemaMigrations())
		if err != nil {
			t.Fatal("original newer database was not preserved", err)
		}
		defer newer.Close()
		var events int
		if err := newer.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_assignment_events").Scan(&events); err != nil || events != 3 {
			t.Fatal("original newer history changed", events, err)
		}
		assignmentAssertReceipt(t, app.New(&assignmentTransactionHarness{db: newer.db, actor: assignmentOwnerActor}), later, three, true)
	})

	for _, failure := range []string{"integrity", "assignment foreign key", "assignment migration ledger"} {
		t.Run("preflight rejects "+failure, func(t *testing.T) {
			f := newAssignmentRepositoryFixture(t)
			owner, service, _ := assignmentActivateOwner(t, f)
			command := f.command(t, 1, app.Assign, 0)
			result := f.execute(t, command)
			before := registrationSchemaRows(t, owner.database.db)
			snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			snapshot = assignmentMutateBackup(t, snapshot, failure)
			target := filepath.Join(t.TempDir(), "must-not-select.sqlite")
			if err := owner.Restore(t.Context(), snapshot, target); !errors.Is(err, ErrBackupInvalid) {
				t.Fatal("restore accepted invalid assignment snapshot", err)
			}
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid restore candidate was published", err)
			}
			if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 10 {
				t.Fatal("preflight failure replaced current owner", version, err)
			}
			assignmentAssertReceipt(t, service, command, result, true)
			assignmentAssertRows(t, owner.database.db, before)
		})
	}

	t.Run("restored retained ledger rejects every assignment callback", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		owner, service, _ := assignmentActivateOwner(t, f)
		first := f.command(t, 1, app.Assign, 0)
		f.execute(t, first)
		second := f.command(t, 2, app.Unassign, 1)
		snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		snapshot = assignmentMutateBackup(t, snapshot, "assignment retained ledger")
		if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
			t.Fatal("fixture did not isolate Assignment ledger validation", err)
		}
		if err := owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite")); err != nil {
			t.Fatal("generic verified restore", err)
		}
		before := registrationSchemaRows(t, owner.database.db)
		entered := false
		if err := owner.WithinAssignmentTransaction(t.Context(), func(app.Port) error { entered = true; return nil }); !errors.Is(err, app.ErrUnavailable) || entered {
			t.Fatal("corrupt restored ledger granted callback authority", entered, err)
		}
		query := app.EffectiveQuery{TagID: f.tag, At: first.EffectiveAt}
		if got, err := service.GetAssignment(t.Context(), query); got != (app.AssignmentView{}) || !errors.Is(err, app.ErrUnavailable) {
			t.Fatal("corrupt restored ledger exposed a partial read", got, err)
		}
		if got, err := service.Execute(t.Context(), second); got != (app.Result{}) || !errors.Is(err, app.ErrUnavailable) {
			t.Fatal("corrupt restored ledger accepted a write", got, err)
		}
		assignmentAssertRows(t, owner.database.db, before)
	})

	t.Run("failed replacement closes authority and preserves source", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		owner, service, source := assignmentActivateOwner(t, f)
		command := f.command(t, 1, app.Assign, 0)
		result := f.execute(t, command)
		snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		owner.contentInventory = assignmentRestoreInvalidator{run: func() error {
			return os.WriteFile(snapshot.Path, []byte("changed after restore preflight"), 0o600)
		}}
		target := filepath.Join(t.TempDir(), "must-not-select.sqlite")
		if err := owner.Restore(t.Context(), snapshot, target); !errors.Is(err, ErrLifecycleRestore) {
			t.Fatal("post-preflight restore error", err)
		}
		if _, err := owner.CurrentVersion(t.Context()); !errors.Is(err, ErrLifecycleClosed) {
			t.Fatal("failed replacement left an owner handle", err)
		}
		if got, err := service.Execute(t.Context(), command); got != (app.Result{}) || !errors.Is(err, app.ErrUnavailable) {
			t.Fatal("failed replacement admitted assignment", got, err)
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unverified replacement was published", err)
		}
		reopened, err := OpenLifecycleOwner(t.Context(), Config{Path: source}, SchemaMigrations())
		if err != nil {
			t.Fatal("original source was not preserved", err)
		}
		defer reopened.Close(context.Background())
		assignmentAssertReceipt(t, app.New(reopened), command, result, true)
	})

	t.Run("invalid live assignment foreign key publishes no backup", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		owner, service, _ := assignmentActivateOwner(t, f)
		command := f.command(t, 1, app.Assign, 0)
		f.execute(t, command)
		catalogSchemaExec(t, owner.database.db, "PRAGMA foreign_keys=OFF")
		catalogSchemaExec(t, owner.database.db, "DROP TRIGGER tc_assignment_events_no_delete")
		catalogSchemaExec(t, owner.database.db, "DELETE FROM tc_assignment_events WHERE revision=1")
		catalogSchemaExec(t, owner.database.db, "PRAGMA foreign_keys=ON")
		target := filepath.Join(t.TempDir(), "must-not-publish.sqlite")
		if snapshot, err := owner.database.CreateBackup(t.Context(), target, SchemaMigrations()); snapshot != (BackupFile{}) || !errors.Is(err, ErrBackupInvalid) {
			t.Fatal("invalid assignment backup was published", snapshot, err)
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid backup destination exists", err)
		}
		if got, err := service.GetAssignment(t.Context(), app.EffectiveQuery{TagID: f.tag, At: command.EffectiveAt}); got != (app.AssignmentView{}) || !errors.Is(err, app.ErrUnavailable) {
			t.Fatal("corrupt live ledger exposed a partial assignment", got, err)
		}
	})
}

func TestAssignmentPreSchemaRestore(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	backup, found := f.db.UpgradeBackup()
	if !found || backup.SchemaVersion != 9 {
		t.Fatal("missing verified pre-assignment v9 backup", backup)
	}
	owner, service, source := assignmentActivateOwner(t, f)
	first := f.command(t, 1, app.Assign, 0)
	one := f.execute(t, first)
	second := f.command(t, 2, app.Unassign, 1)
	two := f.execute(t, second)
	query := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: second.EffectiveAt}, Limit: 1}
	page := assignmentHistoryPage(t, service, query, one)
	if page.Next == "" {
		t.Fatal("v10 history did not produce a cursor")
	}
	query.Cursor = page.Next
	newerRows := registrationSchemaRows(t, owner.database.db)
	var stale app.Port
	if err := owner.WithinAssignmentTransaction(t.Context(), func(port app.Port) error { stale = port; return nil }); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(t.TempDir(), "newer-media.taf")
	mediaBytes := []byte("pre-schema restore keeps external media")
	if err := os.WriteFile(media, mediaBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := owner.Restore(t.Context(), backup, filepath.Join(t.TempDir(), "restored-v9.sqlite")); err != nil {
		t.Fatal("restore exact v9", err)
	}
	if _, err := stale.Authenticate(t.Context()); !errors.Is(err, app.ErrUnavailable) {
		t.Fatal("v10 session survived v9 restore", err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 9 {
		t.Fatal("v9 restore upgraded schema", version, err)
	}
	var assignmentTables int
	if err := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE name LIKE 'tc_assignment%'").Scan(&assignmentTables); err != nil || assignmentTables != 0 {
		t.Fatal("v9 restore contains assignment schema", assignmentTables, err)
	}
	if got, err := service.Execute(t.Context(), first); got != (app.Result{}) || !errors.Is(err, app.ErrUnsupportedState) {
		t.Fatal("v9 restore accepted assignment command", got, err)
	}
	if got, err := service.GetAssignment(t.Context(), query.EffectiveQuery); got != (app.AssignmentView{}) || !errors.Is(err, app.ErrUnsupportedState) {
		t.Fatal("v9 assignment read reported false empty history", got, err)
	}
	if got, err := service.ListAssignmentHistory(t.Context(), query); !reflect.DeepEqual(got, app.HistoryPage{}) || !errors.Is(err, app.ErrUnsupportedState) {
		t.Fatal("v9 history read did not report unsupported omitted history", got, err)
	}
	if got, found, err := service.LookupAssignmentCommand(t.Context(), first); got != (app.Result{}) || found || !errors.Is(err, app.ErrUnsupportedState) {
		t.Fatal("v9 command lookup exposed newer receipt", got, found, err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 9 {
		t.Fatal("assignment reads upgraded v9", version, err)
	}
	if err := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE name LIKE 'tc_assignment%'").Scan(&assignmentTables); err != nil || assignmentTables != 0 {
		t.Fatal("assignment reads created v10 tables", assignmentTables, err)
	}
	catalogCalls := 0
	if err := owner.WithinCatalogTransaction(t.Context(), func(catalogapp.CatalogRepository) error { catalogCalls++; return nil }); err != nil || catalogCalls != 1 {
		t.Fatal("supported v9 catalog behavior changed", catalogCalls, err)
	}
	if got, err := os.ReadFile(media); err != nil || !bytes.Equal(got, mediaBytes) {
		t.Fatal("v9 restore changed external media", err)
	}
	newer, err := OpenLifecycleOwner(t.Context(), Config{Path: source}, SchemaMigrations())
	if err != nil {
		t.Fatal("original newer database was not preserved", err)
	}
	defer newer.Close(context.Background())
	if got := registrationSchemaRows(t, newer.database.db); !reflect.DeepEqual(got, newerRows) {
		t.Fatal("original newer database changed during v9 restore")
	}
	assignmentAssertReceipt(t, app.New(newer), first, one, true)
	assignmentAssertReceipt(t, app.New(newer), second, two, true)
}
