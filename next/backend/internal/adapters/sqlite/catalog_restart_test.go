//go:build !windows

package sqlite

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

// Exercise a real pending Order commit against a populated catalog, including
// existing Review/Order history and PI-07 receipt/version facts. The helper's
// committed reference database is independent of the database we kill/reopen.
func TestCatalogRestart(t *testing.T) {
	for _, phase := range []string{"uncommitted", "durable-response-lost"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "restart.sqlite")
			helper := startRestartHelperFor(t, path, "TestCatalogRestartProcessHelper", "TC_CATALOG_B22_PHASE="+phase, "TMPDIR="+filepath.Dir(path))
			if err := helper.waitUntilWriteIsPending(); err != nil {
				_ = helper.terminate()
				t.Fatal("helper readiness", err, helper.output())
			}
			if err := helper.terminate(); err != nil {
				t.Fatal("terminate", err, helper.output())
			}
			status, ok := helper.command.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("expected actual reaped SIGKILL", helper.command.ProcessState)
			}
			if phase == "uncommitted" {
				raw, err := os.ReadFile(path + ".deadline")
				deadline, parseErr := time.Parse(time.RFC3339Nano, string(raw))
				if err != nil || parseErr != nil || !time.Now().Before(deadline) {
					t.Fatal("cannot prove kill before transaction expiry", err, parseErr)
				}
			}
			wantPath, revision := path+".before", uint64(5)
			if phase == "durable-response-lost" {
				wantPath, revision = path+".after", 6
			}
			want, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(path + ".calls")
			if err != nil || string(calls) != "1" {
				t.Fatal("callback count", string(calls), err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = owner.Close(context.Background()) })
				if got := catalogProcessRows(t, owner); !bytes.Equal(got, want) {
					t.Fatal("reopen changed exact committed rows, receipts or version facts", attempt)
				}
				err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
					content := catalogQueryContent(t, 1)
					order, found, err := r.LoadOrder(t.Context(), content)
					if err != nil || !found || order.AggregateRevision.Uint64() != revision || order.State.Revision().Uint64() != revision-2 {
						t.Fatal("durable order revision", order, found, err)
					}
					query, err := r.Query(t.Context(), app.Query{Kind: app.ListVersions, Content: content, Page: app.PageRequest{Limit: 2}})
					if err != nil || query.Revision.Uint64() != revision || len(query.Entries) != 2 || !query.Complete {
						t.Fatal("durable query readback", query, err)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(catalogProcessRows(t, owner), want) {
					t.Fatal("restart readback rewrote committed state")
				}
				if err := owner.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("reaped SIGKILL at %s; exact catalog revision %d and all receipt/version rows retained across two reopens", phase, revision)
		})
	}
}

func TestCatalogRestartProcessHelper(t *testing.T) {
	phase := os.Getenv("TC_CATALOG_B22_PHASE")
	if os.Getenv(restartHelperEnvironment) != "1" || phase == "" {
		return
	}
	if phase != "uncommitted" && phase != "durable-response-lost" {
		t.Fatal("invalid helper phase")
	}
	path := os.Getenv("TEDDYCLOUD_SQLITE_RESTART_PATH")
	f := catalogRecoveryCurrent(t)
	rows := catalogRecoveryRows(t, f.owner.database.db)
	for _, table := range []string{"tc_blob_imports", "tc_version_blobs", "tc_content_versions", "tc_catalog_decisions", "tc_catalog_order_states", "tc_catalog_review_states"} {
		if len(rows[table]) == 0 {
			t.Fatal("empty crash-recovery fixture", table)
		}
	}
	if _, err := f.owner.database.CreateBackup(t.Context(), path, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".before", catalogProcessRows(t, f.owner), 0600); err != nil {
		t.Fatal(err)
	}
	var op app.OrderOperation
	var next domain.CatalogOrderState
	var result app.OrderResult
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		state, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
		if err != nil || !found {
			t.Fatal("load committed order", found, err)
		}
		raw, err := state.State.PersistenceEncoding()
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
		op.ExpectedAggregate, op.Command.Expected = state.AggregateRevision, live.Revision()
		op.Command.Evidence = nil
		op.Command.Reason = "pending restart order"
		var decision domain.CatalogOrderResult
		next, decision, err = live.Apply(f.order, op.Command)
		if err != nil {
			return err
		}
		revision, err := domain.NewCatalogRevision(6)
		if err != nil {
			return err
		}
		result = app.OrderResult{AggregateRevision: revision, Decision: decision}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := commitOrderFixture(t, f.catalogStateFixture, op, next, result); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path + ".before")
	if err != nil || bytes.Equal(before, catalogProcessRows(t, f.owner)) {
		t.Fatal("reference commit did not change persisted order state", err)
	}
	if err := os.WriteFile(path+".after", catalogProcessRows(t, f.owner), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(3, "catalog-b22-ready")
	if ready == nil {
		t.Fatal("missing readiness pipe")
	}
	defer ready.Close()
	park := func() {
		if _, err := ready.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		<-time.After(time.Hour)
		t.Fatal("helper was not terminated")
	}
	err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		marker, err := os.OpenFile(path+".calls", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if _, err := marker.WriteString("1"); err != nil {
			_ = marker.Close()
			return err
		}
		if err := marker.Close(); err != nil {
			return err
		}
		if err := r.CommitOrder(t.Context(), op, next, result); err != nil {
			return err
		}
		if phase == "uncommitted" {
			var revision uint64
			if err := r.(*catalogRepository).transaction.QueryRowContext(t.Context(), "SELECT aggregate_revision FROM tc_catalog_managed WHERE content_id=?", f.subject.ContentID().String()).Scan(&revision); err != nil || revision != 6 {
				t.Fatal("pending SQL write missing", revision, err)
			}
			deadline, ok := r.(*catalogRepository).ctx.Deadline()
			if !ok {
				t.Fatal("unbounded catalog transaction")
			}
			if err := os.WriteFile(path+".deadline", []byte(deadline.Format(time.RFC3339Nano)), 0600); err != nil {
				return err
			}
			park()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	park()
}
