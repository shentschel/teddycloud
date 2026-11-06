package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type assignmentTransactionHarness struct {
	db           *sql.DB
	scope        assignmentTransaction
	actor        app.AuthenticatedActor
	beforeCommit func()
	afterCommit  func() error
	calls        atomic.Uint64
}

func newAssignmentTransactionHarness(db *sql.DB) *assignmentTransactionHarness {
	return &assignmentTransactionHarness{db: db, actor: app.AuthenticatedActor{Reference: "transaction operator", Read: true, Decide: true, Review: true}}
}

func (h *assignmentTransactionHarness) WithinAssignmentTransaction(ctx context.Context, operation func(app.Port) error) error {
	h.calls.Add(1)
	if ctx == nil || operation == nil {
		return app.ErrInvalidInput
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = h.scope.run(ctx, tx, h.actor, func(port app.Port) error {
		return operation(&assignmentHookPort{Port: port, beforeCommit: h.beforeCommit, afterCommit: h.afterCommit})
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

type assignmentHookPort struct {
	app.Port
	beforeCommit func()
	afterCommit  func() error
}

func (port *assignmentHookPort) Commit(ctx context.Context, expected uint64, snapshot app.Snapshot) error {
	if port.beforeCommit != nil {
		port.beforeCommit()
	}
	if err := port.Port.Commit(ctx, expected, snapshot); err != nil {
		return err
	}
	if port.afterCommit != nil {
		return port.afterCommit()
	}
	return nil
}

func assignmentSecondConnection(t *testing.T, database *Database) *Database {
	t.Helper()
	second, err := Open(t.Context(), Config{Path: dbPathForSupersessionTest(t, database), BusyTimeout: 250 * time.Millisecond}, assignmentSchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	return second
}

func assignmentNonAssignmentRows(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	all := registrationSchemaRows(t, db)
	for table := range all {
		if len(table) >= len("tc_assignment") && table[:len("tc_assignment")] == "tc_assignment" {
			delete(all, table)
		}
	}
	return all
}

func TestAssignmentTransactionScope(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	tx, err := f.db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scope := &assignmentTransaction{}
	actor := app.AuthenticatedActor{Reference: "scoped operator", Read: true, Decide: true, Review: true}
	sentinel := errors.New("callback failure")
	var retained app.Port
	calls := 0
	err = scope.run(t.Context(), tx, actor, func(port app.Port) error {
		calls++
		retained = port
		got, err := port.Authenticate(t.Context())
		if err != nil || got != actor {
			t.Fatal("authenticated actor", got, err)
		}
		nested := scope.run(t.Context(), tx, actor, func(app.Port) error {
			t.Fatal("nested callback entered")
			return nil
		})
		assignmentWantError(t, nested, app.ErrBusy)
		return sentinel
	})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatal("callback result", calls, err)
	}
	if _, err := retained.Authenticate(t.Context()); !errors.Is(err, app.ErrUnavailable) {
		t.Fatal("retained port remained live", err)
	}
	if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(new(int)); err != nil {
		t.Fatal("scope ended supplied transaction", err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := scope.run(canceled, tx, actor, func(app.Port) error {
		t.Fatal("canceled callback entered")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled scope", err)
	}
	if err := scope.run(t.Context(), tx, actor, nil); !errors.Is(err, app.ErrInvalidInput) {
		t.Fatal("nil callback", err)
	}
	if err := scope.run(t.Context(), tx, actor, func(port app.Port) error {
		_ = port.Commit(t.Context(), 0, app.Snapshot{})
		return nil
	}); !errors.Is(err, app.ErrInvalidInput) {
		t.Fatal("ignored port failure allowed callback success", err)
	}
}

func TestAssignmentTransactionInert(t *testing.T) {
	if reflect.TypeFor[*assignmentTransaction]().Implements(reflect.TypeFor[app.Transactor]()) {
		t.Fatal("private scope became public Transactor")
	}
	if !reflect.TypeFor[*LifecycleOwner]().Implements(reflect.TypeFor[app.Transactor]()) {
		t.Fatal("LifecycleOwner did not activate assignment Transactor")
	}
	if !reflect.TypeFor[*assignmentTransactionPort]().Implements(reflect.TypeFor[app.Port]()) {
		t.Fatal("callback scope lost Port contract")
	}
	if reflect.TypeFor[*assignmentTransaction]().NumMethod() != 0 {
		t.Fatal("scope exposes exported methods")
	}
	if app.OperationTimeout != 10*time.Second || app.TransactionTimeout != 5*time.Second {
		t.Fatal("assignment deadlines changed")
	}
	if migrations := SchemaMigrations(); len(migrations) != 10 || migrations[9].Version != 10 {
		t.Fatal("coordinated v10 activation missing")
	}
	for _, name := range []string{"Save", "WithinAssignmentTransaction"} {
		if _, exists := reflect.TypeFor[*assignmentTransactionPort]().MethodByName(name); exists {
			t.Fatal("unscoped writer exposed", name)
		}
	}
}

func TestAssignmentSameRevisionRace(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	first := f.command(t, 1, app.Assign, 0)
	f.execute(t, first)
	secondDB := assignmentSecondConnection(t, f.db)

	ready := make(chan int, 2)
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	harnesses := []*assignmentTransactionHarness{newAssignmentTransactionHarness(f.db.db), newAssignmentTransactionHarness(secondDB.db)}
	for i := range harnesses {
		i := i
		harnesses[i].beforeCommit = func() {
			ready <- i
			<-release[i]
		}
	}
	commands := []app.Command{f.command(t, 2, app.Unassign, 1), f.command(t, 3, app.Unassign, 1)}
	type outcome struct {
		index  int
		result app.Result
		err    error
	}
	outcomes := make(chan outcome, 2)
	for i := range harnesses {
		i := i
		go func() {
			result, err := app.New(harnesses[i]).Execute(t.Context(), commands[i])
			outcomes <- outcome{i, result, err}
		}()
	}
	seen := map[int]bool{<-ready: true, <-ready: true}
	if len(seen) != 2 {
		t.Fatal("both transactions did not reach same-revision fence", seen)
	}
	close(release[0])
	firstOutcome := <-outcomes
	if firstOutcome.index != 0 || firstOutcome.err != nil {
		t.Fatal("selected winner", firstOutcome.index, firstOutcome.err)
	}
	close(release[1])
	loser := <-outcomes
	if loser.index != 1 || !errors.Is(loser.err, app.ErrRevisionConflict) {
		t.Fatal("stale loser", loser.index, loser.err)
	}
	if harnesses[0].calls.Load() != 1 || harnesses[1].calls.Load() != 1 {
		t.Fatal("callback retried", harnesses[0].calls.Load(), harnesses[1].calls.Load())
	}

	r, tx := f.reader(t)
	snapshot, found, err := r.Load(t.Context(), f.tag)
	if err != nil || !found || len(snapshot.Events) != 2 || snapshot.Events[1].Command.CommandID != commands[0].CommandID {
		t.Fatal("winner history", found, len(snapshot.Events), err)
	}
	if _, found, err := r.LookupCommand(t.Context(), commands[1].CommandID); err != nil || found {
		t.Fatal("loser retained partial command", found, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentCommitAtomicity(t *testing.T) {
	t.Run("pre-commit failure and cancellation", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		before := registrationSchemaRows(t, f.db.db)
		sentinel := errors.New("injected before SQL commit")
		harness := newAssignmentTransactionHarness(f.db.db)
		harness.afterCommit = func() error { return sentinel }
		command := f.command(t, 1, app.Assign, 0)
		if _, err := app.New(harness).Execute(t.Context(), command); !errors.Is(err, app.ErrUnavailable) {
			t.Fatal("injected failure", err)
		}
		if !reflect.DeepEqual(before, registrationSchemaRows(t, f.db.db)) {
			t.Fatal("ledger/projection/indices/usage did not roll back together")
		}

		ctx, cancel := context.WithCancel(t.Context())
		harness.afterCommit = func() error { cancel(); return nil }
		if _, err := app.New(harness).Execute(ctx, command); !errors.Is(err, context.Canceled) {
			t.Fatal("callback cancellation", err)
		}
		if !reflect.DeepEqual(before, registrationSchemaRows(t, f.db.db)) {
			t.Fatal("canceled callback retained assignment state")
		}
	})

	t.Run("replay collision and receipts", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		before := assignmentNonAssignmentRows(t, f.db.db)
		command := f.command(t, 1, app.Assign, 0)
		result := f.execute(t, command)
		replayed, err := f.service.Execute(t.Context(), command)
		if err != nil || replayed != result {
			t.Fatal("exact replay", replayed, err)
		}
		conflict := command
		conflict.TagID, _ = identity.ParseTagID(catalogSchemaID("tag", 2))
		conflict.AssignmentID, _ = d.ParseID(catalogSchemaID("asn", 2))
		if _, err := f.service.Execute(t.Context(), conflict); !errors.Is(err, app.ErrCommandConflict) {
			t.Fatal("global command collision", err)
		}
		if !reflect.DeepEqual(before, assignmentNonAssignmentRows(t, f.db.db)) {
			t.Fatal("assignment changed Tag/catalog/media receipts")
		}
	})

	for _, dependency := range []string{"Tag", "catalog"} {
		t.Run("concurrent "+dependency+" revision", func(t *testing.T) {
			f := newAssignmentRepositoryFixture(t)
			second := assignmentSecondConnection(t, f.db)
			ready := make(chan struct{})
			release := make(chan struct{})
			harness := newAssignmentTransactionHarness(f.db.db)
			harness.beforeCommit = func() { close(ready); <-release }
			command := f.command(t, 1, app.Assign, 0)
			result := make(chan error, 1)
			go func() {
				_, err := app.New(harness).Execute(t.Context(), command)
				result <- err
			}()
			<-ready
			query, args := `UPDATE tc_tags SET revision=revision+1 WHERE tag_id=?`, []any{f.tag.String()}
			if dependency == "catalog" {
				query, args = `UPDATE tc_catalog_managed SET aggregate_revision=aggregate_revision+1 WHERE content_id=?`, []any{f.target.ContentID().String()}
			}
			changed, err := second.db.ExecContext(t.Context(), query, args...)
			if err != nil {
				t.Fatal("concurrent dependency change", err)
			}
			if rows, err := changed.RowsAffected(); err != nil || rows != 1 {
				t.Fatal("dependency rows", rows, err)
			}
			close(release)
			if err := <-result; !errors.Is(err, app.ErrRevisionConflict) && !errors.Is(err, app.ErrDependencyConflict) {
				t.Fatal("dependency fence", err)
			}
			var assignments uint64
			if err := f.db.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_assignments`).Scan(&assignments); err != nil || assignments != 0 {
				t.Fatal("dependency loser retained assignment", assignments, err)
			}
		})
	}
}
