package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	sqliteDriver "modernc.org/sqlite"
)

var errAssignmentFault = errors.New("injected SQLite boundary fault")

type assignmentTransactorFunc func(context.Context, func(app.Port) error) error

func (f assignmentTransactorFunc) WithinAssignmentTransaction(ctx context.Context, run func(app.Port) error) error {
	return f(ctx, run)
}

// Install faults at the driver boundary of the real owner's serialized pool.
// Non-faulting operations still run against the same on-disk SQLite database.
type assignmentFaultConnector struct {
	driver.Connector
	commit   func(driver.Tx) error
	rollback func(driver.Tx) error
	exec     func(string) error
}

func (c *assignmentFaultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &assignmentFaultConnection{assignmentDriverConnection: conn.(assignmentDriverConnection), faults: c}, nil
}

type assignmentDriverConnection interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ExecerContext
	driver.QueryerContext
	driver.ConnPrepareContext
	persistWALController
}

type assignmentFaultConnection struct {
	assignmentDriverConnection
	faults *assignmentFaultConnector
}

func (c *assignmentFaultConnection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.assignmentDriverConnection.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &assignmentFaultTx{Tx: tx, faults: c.faults}, nil
}

func (c *assignmentFaultConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.faults.exec != nil {
		if err := c.faults.exec(query); err != nil {
			return nil, err
		}
	}
	return c.assignmentDriverConnection.ExecContext(ctx, query, args)
}

type assignmentFaultTx struct {
	driver.Tx
	faults *assignmentFaultConnector
}

func (tx *assignmentFaultTx) Commit() error {
	if tx.faults.commit != nil {
		return tx.faults.commit(tx.Tx)
	}
	return tx.Tx.Commit()
}

func (tx *assignmentFaultTx) Rollback() error {
	if tx.faults.rollback != nil {
		return tx.faults.rollback(tx.Tx)
	}
	return tx.Tx.Rollback()
}

func assignmentInstallFaults(t *testing.T, owner *LifecycleOwner) *assignmentFaultConnector {
	t.Helper()
	dsn, _, err := connectionDSN(owner.config)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteDriver.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	faults := &assignmentFaultConnector{Connector: connector}
	db := sql.OpenDB(faults)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := owner.database.Close(); err != nil {
		t.Fatal(err)
	}
	owner.database.db = db
	if err := preserveWALSidecars(t.Context(), owner.database); err != nil {
		t.Fatal(err)
	}
	return faults
}

func assignmentAssertReceipt(t *testing.T, s *app.Service, in app.Command, want app.Result, found bool) {
	t.Helper()
	got, ok, err := s.LookupAssignmentCommand(t.Context(), in)
	if err != nil || ok != found || got != want {
		t.Fatalf("receipt: %+v, found %t, error %v", got, ok, err)
	}
}

func assignmentAssertRows(t *testing.T, db *sql.DB, before map[string][][]any) {
	t.Helper()
	after := registrationSchemaRows(t, db)
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Errorf("unexpected changes to %s", table)
		}
	}
	if len(after) != len(before) {
		t.Fatal("table set changed")
	}
}

func TestAssignmentPrecommitOutcomes(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, service, _ := assignmentActivateOwner(t, f)
	faults := assignmentInstallFaults(t, owner)
	f.execute(t, f.command(t, 1, app.Assign, 0))
	in := f.command(t, 2, app.RecordClassification, 1)
	before := registrationSchemaRows(t, owner.database.db)

	t.Run("late SQL rejection", func(t *testing.T) {
		// Fail after event, evidence, support, receipt and usage have been staged.
		catalogSchemaExec(t, owner.database.db, `CREATE TEMP TRIGGER reject_projection BEFORE UPDATE ON tc_assignment_projections BEGIN SELECT RAISE(ABORT,'projection fault'); END`)
		got, err := service.Execute(t.Context(), in)
		if got != (app.Result{}) || err != app.ErrUnavailable {
			t.Fatalf("rejected append: %+v, %v", got, err)
		}
		catalogSchemaExec(t, owner.database.db, `DROP TRIGGER reject_projection`)
		assignmentAssertRows(t, owner.database.db, before)
		assignmentAssertReceipt(t, service, in, app.Result{}, false)
	})

	for _, tc := range []struct {
		name string
		want error
	}{
		{"callback rejection", app.ErrDependencyConflict},
		{"cancellation after append", context.Canceled},
		{"rollback failure with staged rows", app.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var retained app.Port
			rollbacks := 0
			faults.rollback = func(tx driver.Tx) error {
				rollbacks++
				if tc.want == app.ErrUnavailable {
					// Leave the real transaction active. Returning the physical
					// connection to the pool would expose the uncommitted rows.
					return errAssignmentFault
				}
				return tx.Rollback()
			}
			commits := 0
			faults.commit = func(tx driver.Tx) error {
				commits++
				return tx.Commit()
			}
			s := app.New(assignmentTransactorFunc(func(ctx context.Context, run func(app.Port) error) error {
				return owner.WithinAssignmentTransaction(ctx, func(port app.Port) error {
					retained = port
					if err := run(port); err != nil {
						return err
					}
					if tc.want == context.Canceled {
						cancel()
						return nil
					}
					return app.ErrDependencyConflict
				})
			}))
			got, err := s.Execute(ctx, in)
			if got != (app.Result{}) || err != tc.want || rollbacks != 1 || commits != 0 {
				t.Fatalf("precommit outcome: %+v, %v, rollbacks %d, commits %d", got, err, rollbacks, commits)
			}
			faults.rollback, faults.commit = nil, nil
			if _, err := retained.Authenticate(t.Context()); err != app.ErrUnavailable {
				t.Fatal("callback authority survived rollback", err)
			}
			assignmentAssertRows(t, owner.database.db, before)
			assignmentAssertReceipt(t, service, in, app.Result{}, false)
		})
	}
}

func TestAssignmentContentionAndDeadline(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, service, _ := assignmentActivateOwner(t, f)
	in := f.command(t, 1, app.Assign, 0)
	before := registrationSchemaRows(t, owner.database.db)
	second := assignmentSecondConnection(t, owner.database)
	tx, err := second.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `UPDATE tc_tags SET revision=revision+1 WHERE tag_id=?`, f.tag.String()); err != nil {
		t.Fatal(err)
	}
	if result, err := service.Execute(t.Context(), in); result != (app.Result{}) || err != app.ErrContention {
		t.Fatalf("competing SQLite writer: %+v, %v", result, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	checkDeadline := func(t *testing.T) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		result, err := service.Execute(ctx, in)
		if result != (app.Result{}) || err != context.DeadlineExceeded || time.Since(started) >= time.Second {
			t.Fatalf("contention with earlier deadline: %+v, %v after %s", result, err, time.Since(started))
		}
	}
	t.Run("owner gate", func(t *testing.T) {
		if err := owner.gate.enterOperation(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer owner.gate.leave()
		checkDeadline(t)
	})
	t.Run("connection acquisition", func(t *testing.T) {
		connection, err := owner.database.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		release := time.AfterFunc(time.Second, func() { _ = connection.Close() })
		defer release.Stop()
		defer connection.Close()
		checkDeadline(t)
	})
	assignmentAssertRows(t, owner.database.db, before)
	assignmentAssertReceipt(t, service, in, app.Result{}, false)
}

func TestAssignmentCommitUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name      string
		committed bool
		cleanup   bool
	}{
		{"commit rejected", false, false},
		{"commit acknowledgment lost", true, false},
		{"cleanup failed after commit", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAssignmentRepositoryFixture(t)
			owner, service, _ := assignmentActivateOwner(t, f)
			faults := assignmentInstallFaults(t, owner)
			in := f.command(t, 1, app.Assign, 0)
			before := registrationSchemaRows(t, owner.database.db)
			calls := 0
			faults.commit = func(tx driver.Tx) error {
				calls++
				if tc.committed {
					if err := tx.Commit(); err != nil {
						t.Error("real commit failed", err)
						return err
					}
				}
				if tc.cleanup {
					return nil
				}
				return errAssignmentFault
			}
			if tc.cleanup {
				faults.exec = func(query string) error {
					if query == busyTimeoutPragma(5000) {
						return errAssignmentFault
					}
					return nil
				}
			}
			if result, err := service.Execute(t.Context(), in); result != (app.Result{}) || err != app.ErrIndeterminate || calls != 1 {
				t.Fatalf("uncertain commit: %+v, %v, attempts %d", result, err, calls)
			}
			faults.commit, faults.exec = nil, nil
			want := app.Result{}
			if tc.committed {
				want = app.Result{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID, Revision: 1}
			} else {
				assignmentAssertRows(t, owner.database.db, before)
			}
			assignmentAssertReceipt(t, service, in, want, tc.committed)
		})
	}

	t.Run("read cleanup failure has no uncertain commit", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		owner, service, _ := assignmentActivateOwner(t, f)
		faults := assignmentInstallFaults(t, owner)
		in := f.command(t, 1, app.Assign, 0)
		faults.exec = func(query string) error {
			if query == busyTimeoutPragma(5000) {
				return errAssignmentFault
			}
			return nil
		}
		if got, found, err := service.LookupAssignmentCommand(t.Context(), in); got != (app.Result{}) || found || err != app.ErrUnavailable {
			t.Fatalf("read cleanup: %+v, %t, %v", got, found, err)
		}
		faults.exec = nil
		assignmentAssertReceipt(t, service, in, app.Result{}, false)
	})
}

func TestAssignmentCommitFence(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, service, path := assignmentActivateOwner(t, f)
	faults := assignmentInstallFaults(t, owner)
	in := f.command(t, 1, app.Assign, 0)
	entered := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	guard, stop := context.WithTimeout(t.Context(), 4*time.Second)
	defer stop()
	faults.commit = func(tx driver.Tx) error {
		close(entered)
		select {
		case <-release:
		case <-guard.Done():
			return guard.Err()
		}
		if err := tx.Commit(); err != nil {
			t.Error("real commit failed", err)
			return err
		}
		return errAssignmentFault
	}
	result := make(chan error, 1)
	go func() {
		got, err := service.Execute(ctx, in)
		if got != (app.Result{}) {
			t.Error("uncertain execution released receipt", got)
		}
		result <- err
	}()
	select {
	case <-entered:
	case err := <-result:
		t.Fatal("command exited before commit fence", err)
	case <-guard.Done():
		t.Fatal(guard.Err())
	}
	unblock := true
	defer func() {
		if unblock {
			close(release)
		}
	}()
	<-ctx.Done()
	select {
	case err := <-result:
		t.Fatal("commit returned before driver released", err)
	default:
	}
	if _, _, err := app.New(owner).LookupAssignmentCommand(guard, in); err != app.ErrBusy {
		t.Fatal("lookup entered while commit remained possible", err)
	}
	closeCtx, closeCancel := context.WithTimeout(guard, 50*time.Millisecond)
	defer closeCancel()
	if err := owner.Close(closeCtx); err != context.DeadlineExceeded {
		t.Fatal("close passed active commit fence", err)
	}
	close(release)
	unblock = false
	select {
	case err := <-result:
		if err != app.ErrIndeterminate {
			t.Fatal("deadline hid commit uncertainty", err)
		}
	case <-guard.Done():
		t.Fatal(guard.Err())
	}
	if err := owner.Close(guard); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.LookupAssignmentCommand(guard, in); err != app.ErrUnavailable {
		t.Fatal("closed owner still resolved commands", err)
	}
	reopened, err := OpenLifecycleOwner(guard, Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	assignmentAssertReceipt(t, app.New(reopened), in,
		app.Result{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID, Revision: 1}, true)
}

func TestAssignmentCorruptCommandReadback(t *testing.T) {
	for _, tc := range []string{"missing command", "digest mismatch", "valid digest with divergent result"} {
		t.Run(tc, func(t *testing.T) {
			f := newAssignmentRepositoryFixture(t)
			owner, service, _ := assignmentActivateOwner(t, f)
			in := f.command(t, 1, app.Assign, 0)
			f.execute(t, in)
			f.execute(t, f.command(t, 2, app.Unassign, 1))
			db := owner.database.db
			switch tc {
			case "missing command":
				catalogSchemaExec(t, db, `DROP TRIGGER tc_assignment_commands_no_delete`)
				catalogSchemaExec(t, db, `DELETE FROM tc_assignment_commands WHERE command_id=?`, in.CommandID.String())
			case "digest mismatch":
				catalogSchemaExec(t, db, `DROP TRIGGER tc_assignment_commands_no_update`)
				catalogSchemaExec(t, db, `UPDATE tc_assignment_commands SET result_sha256=zeroblob(32) WHERE command_id=?`, in.CommandID.String())
			default:
				catalogSchemaExec(t, db, `DROP TRIGGER tc_assignment_commands_no_update`)
				var other []byte
				if err := db.QueryRowContext(t.Context(), `SELECT canonical_result FROM tc_assignment_commands WHERE revision=2`).Scan(&other); err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256(other)
				catalogSchemaExec(t, db, `UPDATE tc_assignment_commands SET canonical_result=?,result_sha256=? WHERE command_id=?`, other, hash[:], in.CommandID.String())
			}
			before := registrationSchemaRows(t, db)
			got, found, err := service.LookupAssignmentCommand(t.Context(), in)
			if got != (app.Result{}) || found || err != app.ErrUnavailable {
				t.Fatalf("corrupt lookup: %+v, %t, %v", got, found, err)
			}
			if got, err := service.Execute(t.Context(), in); got != (app.Result{}) || err != app.ErrUnavailable {
				t.Fatalf("corrupt replay: %+v, %v", got, err)
			}
			assignmentAssertRows(t, db, before)
		})
	}
}

// Lose the response only after the real owner has returned from a successful
// SQLite commit. The caller must recover through the public command API.
func assignmentLostResponse(owner *LifecycleOwner) *app.Service {
	return app.New(assignmentTransactorFunc(func(ctx context.Context, run func(app.Port) error) error {
		if err := owner.WithinAssignmentTransaction(ctx, run); err != nil {
			return err
		}
		return app.ErrIndeterminate
	}))
}

func TestAssignmentLostResponse(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, service, _ := assignmentActivateOwner(t, f)
	in := f.command(t, 1, app.Assign, 0)
	if got, err := assignmentLostResponse(owner).Execute(t.Context(), in); got != (app.Result{}) || err != app.ErrIndeterminate {
		t.Fatalf("lost response: %+v, %v", got, err)
	}
	before := registrationSchemaRows(t, owner.database.db)
	want := app.Result{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID, Revision: 1}
	assignmentAssertReceipt(t, service, in, want, true)
	if got, err := service.Execute(t.Context(), in); got != want || err != nil {
		t.Fatalf("same-ID replay: %+v, %v", got, err)
	}
	assignmentAssertRows(t, owner.database.db, before)
}
