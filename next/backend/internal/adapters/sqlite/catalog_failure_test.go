package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	sqliteDriver "modernc.org/sqlite"
)

// Wrap the actual driver at its commit/connection-cleanup boundary. All SQL,
// validation, persistence and readback still use the production adapter. No
// production hook or synthetic in-memory commit stands in for durability.
type catalogFaultConnector struct {
	driver.Connector
	fault *catalogCommitFault
}

type catalogCommitFault struct {
	mode    string
	commits atomic.Int32
	cleanup atomic.Bool
	cancel  context.CancelFunc
}

var catalogPrivateFailure = errors.New("/private/catalog.sqlite: SQL tc_catalog_order_states; raw={token:secret-value}")

func (c catalogFaultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &catalogFaultConn{Conn: conn, fault: c.fault}, nil
}

type catalogFaultConn struct {
	driver.Conn
	fault *catalogCommitFault
}

func (c *catalogFaultConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &catalogFaultTx{Tx: tx, fault: c.fault}, nil
}

func (c *catalogFaultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if query == "PRAGMA query_only=OFF" && c.fault.cleanup.CompareAndSwap(true, false) {
		return nil, catalogPrivateFailure
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

type catalogFaultTx struct {
	driver.Tx
	fault *catalogCommitFault
}

func (tx *catalogFaultTx) Commit() error {
	if tx.fault.commits.Add(1) != 1 {
		return tx.Tx.Commit()
	}
	if tx.fault.mode == "before-commit" {
		if err := tx.Tx.Rollback(); err != nil {
			return err
		}
		return catalogPrivateFailure
	}
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.fault.mode == "cleanup" {
		tx.fault.cleanup.Store(true)
		return nil
	}
	if tx.fault.cancel != nil {
		tx.fault.cancel()
	}
	return catalogPrivateFailure
}

func catalogInstallFault(t *testing.T, owner *LifecycleOwner, fault *catalogCommitFault) {
	t.Helper()
	dsn, _, err := connectionDSN(owner.config)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteDriver.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.database.db.Close(); err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(catalogFaultConnector{connector, fault})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	owner.database.db = db
}

func catalogPendingOrder(t *testing.T, f catalogRecoveryFixture) (app.OrderOperation, domain.CatalogOrderState, app.OrderResult) {
	t.Helper()
	var op app.OrderOperation
	var next domain.CatalogOrderState
	var result app.OrderResult
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		s, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
		if err != nil || !found {
			return fmt.Errorf("load order: found=%t: %w", found, err)
		}
		raw, err := s.State.PersistenceEncoding()
		if err != nil {
			return err
		}
		live, err := domain.RehydrateCatalogOrderState(raw, domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domain.SourceRegistration{f.source}, Reviewers: []domain.IssuerPrincipal{f.review.Reviewer()}, Namespaces: []domain.CatalogOrderNamespace{f.namespace}})
		if err != nil {
			return err
		}
		op = f.orderOp
		op.Command.ID, err = domain.NewCatalogCommandID()
		if err != nil {
			return err
		}
		op.ExpectedAggregate, op.Command.Expected = s.AggregateRevision, live.Revision()
		op.Command.Evidence, op.Command.Reason = nil, "bounded failure recovery"
		var decision domain.CatalogOrderResult
		next, decision, err = live.Apply(f.order, op.Command)
		if err != nil {
			return err
		}
		revision, err := domain.NewCatalogRevision(s.AggregateRevision.Uint64() + 1)
		result = app.OrderResult{AggregateRevision: revision, Decision: decision}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return op, next, result
}

func catalogOrderCommand(op app.OrderOperation) app.OrderCommand {
	c := op.Command
	return app.OrderCommand{ID: c.ID, Content: c.Content, ExpectedAggregate: op.ExpectedAggregate, ExpectedOrder: c.Expected, Entries: c.Entries, Evidence: c.Evidence, Supersedes: c.Supersedes, Reason: c.Reason}
}

type catalogCountedTransactor struct {
	owner *LifecycleOwner
	calls int
}

func (tx *catalogCountedTransactor) WithinCatalogTransaction(ctx context.Context, fn func(app.CatalogRepository) error) error {
	return tx.owner.WithinCatalogTransaction(ctx, func(r app.CatalogRepository) error {
		tx.calls++
		return fn(r)
	})
}

func TestCatalogUncertainCommit(t *testing.T) {
	for _, mode := range []string{"before-commit", "after-commit", "after-commit-cancel", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f := catalogRecoveryCurrent(t)
			before := catalogRecoveryRows(t, f.owner.database.db)
			for _, table := range []string{"tc_blob_imports", "tc_content_versions", "tc_version_blobs"} {
				if len(before[table]) == 0 {
					t.Fatal("empty protected facts", table)
				}
			}
			op, next, want := catalogPendingOrder(t, f)
			if want.AggregateRevision.Uint64() != 6 || want.Decision.View.Revision.Uint64() != 4 {
				t.Fatal("unexpected recovery revision fixture", want)
			}
			backup, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "before.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			if err := commitOrderFixture(t, f.catalogStateFixture, op, next, want); err != nil {
				t.Fatal(err)
			}
			after := catalogRecoveryRows(t, f.owner.database.db)
			if reflect.DeepEqual(before, after) {
				t.Fatal("reference mutation absent")
			}
			for _, table := range []string{"tc_blob_imports", "tc_content_versions", "tc_version_blobs"} {
				if !reflect.DeepEqual(before[table], after[table]) {
					t.Fatal("reference changed immutable facts", table)
				}
			}
			owner, err := OpenLifecycleOwner(t.Context(), Config{Path: backup.Path}, SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = owner.Close(context.Background()) })
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			fault := &catalogCommitFault{mode: mode}
			if mode == "after-commit-cancel" {
				fault.cancel = cancel
			}
			catalogInstallFault(t, owner, fault)
			counted := &catalogCountedTransactor{owner: owner}
			service, err := app.NewService(counted, f.source, f.review, f.namespace)
			if err != nil {
				t.Fatal(err)
			}
			got, err := service.DecideOrder(ctx, catalogOrderCommand(op))
			if err != app.ErrIndeterminate || !reflect.DeepEqual(got, app.OrderResult{}) || errors.Unwrap(err) != nil {
				t.Fatalf("uncertain commit must discard result and private cause: result=%v err=%v", got, err)
			}
			if fault.commits.Load() != 1 || counted.calls != 1 {
				t.Fatal("commit or callback retried", fault.commits.Load(), counted.calls)
			}
			wantRows, durable := after, mode != "before-commit"
			if !durable {
				wantRows = before
			}
			if !reflect.DeepEqual(catalogRecoveryRows(t, owner.database.db), wantRows) {
				t.Fatal("partial mutation or wrong durable outcome")
			}
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			owner, err = OpenLifecycleOwner(t.Context(), Config{Path: backup.Path}, SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			service, err = app.NewService(owner, f.source, f.review, f.namespace)
			if err != nil {
				t.Fatal(err)
			}
			read, found, err := service.LookupOrder(t.Context(), catalogOrderCommand(op))
			if err != nil || found != durable || durable && !reflect.DeepEqual(read, want) || !durable && !reflect.DeepEqual(read, app.OrderResult{}) {
				t.Fatal("recovery readback", read, found, err)
			}
			if !reflect.DeepEqual(catalogRecoveryRows(t, owner.database.db), wantRows) {
				t.Fatal("lookup mutated persistence")
			}
			retry, err := service.DecideOrder(t.Context(), catalogOrderCommand(op))
			if err != nil || !reflect.DeepEqual(retry, want) {
				t.Fatal("exact retry", retry, err)
			}
			if !reflect.DeepEqual(catalogRecoveryRows(t, owner.database.db), after) {
				t.Fatal("retry changed exact revision, receipts, versions or history")
			}
			changed := catalogOrderCommand(op)
			changed.Reason = "changed same identity"
			if got, err := service.DecideOrder(t.Context(), changed); err != app.ErrCommandConflict || !reflect.DeepEqual(got, app.OrderResult{}) {
				t.Fatal("changed retry accepted", got, err)
			}
			if !reflect.DeepEqual(catalogRecoveryRows(t, owner.database.db), after) {
				t.Fatal("conflict changed persistence")
			}
		})
	}
}

func TestCatalogSanitizedErrors(t *testing.T) {
	for _, mode := range []string{"constraint", "swallowed-constraint", "cancel-before-commit", "canceled-admission", "deadline-admission", "busy"} {
		t.Run(mode, func(t *testing.T) {
			f, _, op, next, want := orderWriteFixture(t)
			before := catalogRecoveryRows(t, f.owner.database.db)
			if mode == "constraint" || mode == "swallowed-constraint" {
				catalogSchemaExec(t, f.owner.database.db, "CREATE TEMP TRIGGER catalog_private_failure BEFORE INSERT ON tc_catalog_order_state_versions BEGIN SELECT RAISE(ABORT, '/private/catalog.sqlite SQL tc_catalog_order_state_versions token=secret-value'); END")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if mode == "canceled-admission" {
				cancel()
			}
			if mode == "deadline-admission" {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				defer stop()
			}
			if mode == "busy" {
				other, err := Open(t.Context(), f.owner.config, SchemaMigrations())
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				blocker, err := other.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback()
				if _, err := blocker.ExecContext(t.Context(), "UPDATE tc_catalog_managed SET aggregate_revision=aggregate_revision"); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			started := time.Now()
			err := f.owner.WithinCatalogTransaction(ctx, func(r app.CatalogRepository) error {
				calls++
				failure := r.CommitOrder(ctx, op, next, want)
				if mode == "cancel-before-commit" {
					if failure != nil {
						return failure
					}
					cancel()
					return nil
				}
				if mode == "swallowed-constraint" {
					if failure != app.ErrRepositoryUnavailable {
						t.Fatal("expected injected failure", failure)
					}
					return nil
				}
				return failure
			})
			expected := error(app.ErrRepositoryUnavailable)
			if mode == "cancel-before-commit" || mode == "canceled-admission" {
				expected = context.Canceled
			}
			if mode == "deadline-admission" {
				expected = context.DeadlineExceeded
			}
			if mode == "busy" {
				expected = app.ErrRepositoryContention
			}
			if err != expected || errors.Unwrap(err) != nil {
				t.Fatalf("typed, sanitized error: got %v want %v", err, expected)
			}
			wantCalls := 1
			if mode == "canceled-admission" || mode == "deadline-admission" {
				wantCalls = 0
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("failure exceeded operation bound")
			}
			if calls != wantCalls {
				t.Fatal("callback execution", calls, wantCalls)
			}
			if !reflect.DeepEqual(catalogRecoveryRows(t, f.owner.database.db), before) {
				t.Fatal("rejected mutation retained partial state")
			}
		})
	}
}
