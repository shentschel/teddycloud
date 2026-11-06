package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

func assignmentActivateOwner(t *testing.T, f *assignmentRepositoryFixture) (*LifecycleOwner, *app.Service, string) {
	t.Helper()
	path := dbPathForSupersessionTest(t, f.db)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	f.db = owner.database
	service := app.New(owner)
	f.service = service
	return owner, service, path
}

func TestAssignmentSingleOwnerFence(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, service, _ := assignmentActivateOwner(t, f)

	var retained app.Port
	calls := 0
	if err := owner.WithinAssignmentTransaction(t.Context(), func(port app.Port) error {
		calls++
		retained = port
		nested := owner.WithinAssignmentTransaction(t.Context(), func(app.Port) error {
			t.Fatal("nested assignment callback entered")
			return nil
		})
		assignmentWantError(t, nested, app.ErrBusy)
		return nil
	}); err != nil || calls != 1 {
		t.Fatal("single owner callback", calls, err)
	}
	if _, err := retained.Authenticate(t.Context()); !errors.Is(err, app.ErrUnavailable) {
		t.Fatal("callback port survived revocation", err)
	}

	first := f.command(t, 1, app.Assign, 0)
	if _, err := service.Execute(t.Context(), first); err != nil {
		t.Fatal("real owner assignment", err)
	}
	staleTag := f.command(t, 2, app.Unassign, 1)
	staleTag.ExpectedTagRevision++
	if _, err := service.Execute(t.Context(), staleTag); !errors.Is(err, app.ErrDependencyConflict) {
		t.Fatal("Tag revision fence", err)
	}
	staleCatalog := f.command(t, 3, app.RecordOriginalTarget, 1)
	staleCatalog.ExpectedCatalogRevision++
	if _, err := service.Execute(t.Context(), staleCatalog); !errors.Is(err, app.ErrDependencyConflict) {
		t.Fatal("catalog revision fence", err)
	}
	var events int
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_assignment_events`).Scan(&events); err != nil || events != 1 {
		t.Fatal("dependency loser changed history", events, err)
	}

	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(t.Context(), first); !errors.Is(err, app.ErrUnavailable) {
		t.Fatal("closed owner admitted assignment", err)
	}
}

func TestAssignmentStorageBypassRejected(t *testing.T) {
	if !reflect.TypeFor[*LifecycleOwner]().Implements(reflect.TypeFor[app.Transactor]()) {
		t.Fatal("LifecycleOwner is not the assignment Transactor")
	}
	for _, name := range []string{"Save", "WithinAssignmentTransaction", "WithinCatalogTransaction", "WithinTagTransaction"} {
		if _, ok := reflect.TypeFor[app.Port]().MethodByName(name); ok {
			t.Fatal("assignment Port exposes bypass", name)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[*assignmentRepository](), reflect.TypeFor[*assignmentTransaction]()} {
		if typ.Implements(reflect.TypeFor[app.Transactor]()) {
			t.Fatal("alternate assignment writer", typ)
		}
	}

	f := newAssignmentRepositoryFixture(t)
	owner, _, _ := assignmentActivateOwner(t, f)
	var retained app.Port
	if err := owner.WithinAssignmentTransaction(t.Context(), func(port app.Port) error {
		retained = port
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := retained.Commit(t.Context(), 0, app.Snapshot{}); !errors.Is(err, app.ErrUnavailable) {
		t.Fatal("retained Port remained a writer", err)
	}
}

func TestAssignmentActivationCompatibility(t *testing.T) {
	t.Run("v9 catalog only", func(t *testing.T) {
		owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v9.sqlite")}, SchemaMigrations()[:9])
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close(context.Background())
		catalogCalls := 0
		if err := owner.WithinCatalogTransaction(t.Context(), func(catalogapp.CatalogRepository) error {
			catalogCalls++
			return nil
		}); err != nil || catalogCalls != 1 {
			t.Fatal("v9 catalog compatibility", catalogCalls, err)
		}
		assignmentCalls := 0
		err = owner.WithinAssignmentTransaction(t.Context(), func(app.Port) error {
			assignmentCalls++
			return nil
		})
		if !errors.Is(err, app.ErrUnsupportedState) || assignmentCalls != 0 {
			t.Fatal("v9 assignment authority", assignmentCalls, err)
		}
		if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 9 {
			t.Fatal("v9 read triggered upgrade", version, err)
		}
	})

	t.Run("v10 coordinated", func(t *testing.T) {
		owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v10.sqlite")}, SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close(context.Background())
		if err := owner.WithinCatalogTransaction(t.Context(), func(catalogapp.CatalogRepository) error { return nil }); err != nil {
			t.Fatal("v10 catalog", err)
		}
		calls := 0
		if err := owner.WithinAssignmentTransaction(t.Context(), func(app.Port) error {
			calls++
			return nil
		}); err != nil || calls != 1 {
			t.Fatal("v10 assignment", calls, err)
		}
	})

	t.Run("synthetic v11", func(t *testing.T) {
		migrations := append(SchemaMigrations(), Migration{Version: 11, ID: "0011-test-future", Statements: []string{`CREATE TABLE tc_test_future (id INTEGER) STRICT`}})
		owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v11.sqlite")}, migrations)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close(context.Background())
		assignmentCalls := 0
		if err := owner.WithinAssignmentTransaction(t.Context(), func(app.Port) error {
			assignmentCalls++
			return nil
		}); !errors.Is(err, app.ErrUnsupportedState) || assignmentCalls != 0 {
			t.Fatal("v11 assignment", assignmentCalls, err)
		}
		catalogCalls := 0
		if err := owner.WithinCatalogTransaction(t.Context(), func(catalogapp.CatalogRepository) error {
			catalogCalls++
			return nil
		}); !errors.Is(err, catalogapp.ErrUnsupported) || catalogCalls != 0 {
			t.Fatal("v11 catalog", catalogCalls, err)
		}
		var rows int
		if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_test_future`).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("v11 rejection wrote rows", rows, err)
		}
	})
}

func TestAssignmentRestartSmoke(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, service, path := assignmentActivateOwner(t, f)
	first := f.command(t, 1, app.Assign, 0)
	want, err := service.Execute(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	second := f.command(t, 2, app.Unassign, 1)
	if _, err := service.Execute(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	f.db = restarted.database
	service = app.New(restarted)
	got, err := service.Execute(t.Context(), first)
	if err != nil || got != want {
		t.Fatal("exact command replay after restart", got, err)
	}
	if err := restarted.WithinAssignmentTransaction(t.Context(), func(port app.Port) error {
		snapshot, found, err := port.Load(t.Context(), f.tag)
		if err != nil || !found || len(snapshot.Events) != 2 ||
			snapshot.Events[0].Command.CommandID != first.CommandID ||
			snapshot.Events[1].Command.CommandID != second.CommandID {
			t.Fatal("complete restart history", found, len(snapshot.Events), err)
		}
		event, found, err := port.LookupCommand(t.Context(), first.CommandID)
		if err != nil || !found || event.Result != want {
			t.Fatal("restart command index", found, event.Result, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentPreUpgradeRestoreSmoke(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	backup, ok := f.db.UpgradeBackup()
	if !ok || backup.SchemaVersion != 9 {
		t.Fatal("missing verified pre-v10 backup")
	}
	owner, service, newerPath := assignmentActivateOwner(t, f)
	first := f.command(t, 1, app.Assign, 0)
	if _, err := service.Execute(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	var stale app.Port
	if err := owner.WithinAssignmentTransaction(t.Context(), func(port app.Port) error {
		stale = port
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	media := filepath.Join(t.TempDir(), "newer-media.taf")
	if err := os.WriteFile(media, []byte("preserve newer media"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleDatabase := owner.database
	restoredPath := filepath.Join(t.TempDir(), "restored-v9.sqlite")
	if err := owner.Restore(t.Context(), backup, restoredPath); err != nil {
		t.Fatal(err)
	}
	if _, err := staleDatabase.CurrentVersion(t.Context()); err == nil {
		t.Fatal("restore left stale v10 database handle usable")
	}
	if _, err := stale.Authenticate(t.Context()); !errors.Is(err, app.ErrUnavailable) {
		t.Fatal("restore left stale assignment Port live", err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 9 {
		t.Fatal("restore did not select exact v9", version, err)
	}
	if _, err := service.Execute(t.Context(), first); !errors.Is(err, app.ErrUnsupportedState) {
		t.Fatal("restored v9 accepted assignment", err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 9 {
		t.Fatal("assignment read upgraded restored v9", version, err)
	}
	if raw, err := os.ReadFile(media); err != nil || string(raw) != "preserve newer media" {
		t.Fatal("newer media not preserved", err)
	}

	newer, err := Open(t.Context(), Config{Path: newerPath}, SchemaMigrations())
	if err != nil {
		t.Fatal("newer v10 database not preserved", err)
	}
	defer newer.Close()
	var retained int
	if err := newer.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_assignment_events`).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("newer assignment history not retained", retained, err)
	}
	var restoredTables int
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'tc_assignment%'`).Scan(&restoredTables); err != nil || restoredTables != 0 {
		t.Fatal("v9 restore retained newer assignment schema", restoredTables, err)
	}
}
