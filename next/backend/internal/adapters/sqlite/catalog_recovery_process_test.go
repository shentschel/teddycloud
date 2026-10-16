//go:build !windows

package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

// Reuse the restart readiness pipe, actual test executable, SIGKILL and reap.
// The two positions are transaction callback-before-return and durable
// transaction-return-before-response, not simulated SQL or power-loss evidence.
func TestCatalogHelperProcessRecovery(t *testing.T) {
	for _, phase := range []string{"uncommitted", "durable-response-lost"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.sqlite")
			helper := startRestartHelperFor(t, path, "TestCatalogRecoveryProcessHelper", "TC_CATALOG_RECOVERY_PHASE="+phase, "TMPDIR="+filepath.Dir(path))
			if err := helper.waitUntilWriteIsPending(); err != nil {
				_ = helper.terminate()
				t.Fatalf("helper readiness: %v\n%s", err, helper.output())
			}
			if err := helper.terminate(); err != nil {
				t.Fatalf("helper termination: %v\n%s", err, helper.output())
			}
			if phase == "uncommitted" {
				raw, err := os.ReadFile(path + ".deadline")
				deadline, parseErr := time.Parse(time.RFC3339Nano, string(raw))
				if err != nil || parseErr != nil || !time.Now().Before(deadline) {
					t.Fatal("cannot prove SIGKILL preceded transaction expiry", err, parseErr)
				}
			}
			status, ok := helper.command.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatalf("helper did not cross a real SIGKILL boundary: %v", helper.command.ProcessState)
			}
			wantFile := path + ".before.json"
			wantRevision := uint64(1)
			if phase == "durable-response-lost" {
				wantFile, wantRevision = path+".after.json", 2
			}
			want, err := os.ReadFile(wantFile)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = owner.Close(context.Background()) })
				if got := catalogProcessRows(t, owner); !bytes.Equal(got, want) {
					t.Fatalf("reopen %d differs from complete %s snapshot", attempt, phase)
				}
				if err := verifyForeignKeys(t.Context(), owner.database.db); err != nil {
					t.Fatal(err)
				}
				var escaped app.CatalogRepository
				err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
					escaped = r
					// Detached persistence readback checks the actual envelope digest,
					// revisions and normalized relationships without minting authority.
					subject, _ := domain.ContentSubject(catalogQueryContent(t, 1))
					state, found, err := r.LoadReview(t.Context(), subject)
					if err != nil || !found || state.AggregateRevision.Uint64() != wantRevision || state.State.Revision().Uint64() != wantRevision || len(state.State.Observations()) != int(wantRevision-1) {
						t.Fatalf("durable readback: %+v %t %v", state, found, err)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := owner.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				subject, _ := domain.ContentSubject(catalogQueryContent(t, 1))
				if _, _, err := escaped.LoadReview(t.Context(), subject); err != app.ErrRepositoryUnavailable {
					t.Fatal("escaped repository survived close", err)
				}
			}
			calls, err := os.ReadFile(path + ".callback")
			if err != nil || string(calls) != "1" {
				t.Fatal("helper callback was not executed exactly once", string(calls), err)
			}
			t.Logf("reaped actual SIGKILL at %s; exact rows and readback survived two reopens", phase)
		})
	}
}

func catalogProcessRows(t *testing.T, owner *LifecycleOwner) []byte {
	t.Helper()
	raw, err := json.Marshal(catalogRecoveryRows(t, owner.database.db))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCatalogRecoveryProcessHelper(t *testing.T) {
	phase := os.Getenv("TC_CATALOG_RECOVERY_PHASE")
	if os.Getenv(restartHelperEnvironment) != "1" || phase == "" {
		return
	}
	if phase != "uncommitted" && phase != "durable-response-lost" {
		t.Fatal("unknown recovery phase")
	}
	path := os.Getenv("TEDDYCLOUD_SQLITE_RESTART_PATH")
	f, op, next, result := reviewWriteFixture(t, false)
	// Prepare an independent exact expected database using the real commit, then
	// operate on the precommit backup. No rollback expectation is derived from
	// the database being recovered.
	if _, err := f.owner.database.CreateBackup(t.Context(), path, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".before.json", catalogProcessRows(t, f.owner), 0600); err != nil {
		t.Fatal(err)
	}
	if err := commitReviewFixture(t, f, op, next, result); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".after.json", catalogProcessRows(t, f.owner), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	f.owner = owner
	ready := os.NewFile(3, "catalog-recovery-ready")
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
		marker, err := os.OpenFile(path+".callback", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
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
		if err := r.CommitReview(t.Context(), op, next, result); err != nil {
			return err
		}
		if phase == "uncommitted" {
			// The rows exist on the leased transaction, not just in memory.
			var count int
			if err := r.(*catalogRepository).transaction.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_catalog_decisions").Scan(&count); err != nil || count != 1 {
				t.Fatal("staged decision missing", count, err)
			}
			deadline, ok := r.(*catalogRepository).ctx.Deadline()
			if !ok {
				t.Fatal("transaction has no bounded deadline")
			}
			if err := os.WriteFile(path+".deadline", []byte(deadline.Format(time.RFC3339Nano)), 0600); err != nil {
				t.Fatal(err)
			}
			park()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	park() // Transaction committed; caller has not received a response.
}

// Core capabilities intentionally do not cross a serialization boundary.
// Retain those trusted handles across an owner reopen to test Lookup and exact
// replay separately from the process-kill/detached-readback evidence above.
func TestCatalogHelperRecoveryReplayAndFences(t *testing.T) {
	f, op, next, result := reviewWriteFixture(t, false)
	holder := openRawDatabase(t, f.owner.config.Path)
	defer holder.Close()
	lock, err := holder.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.ExecContext(t.Context(), "UPDATE tc_catalog_managed SET aggregate_revision=aggregate_revision"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	started := time.Now()
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		calls++
		return r.CommitReview(t.Context(), op, next, result)
	})
	if err != app.ErrRepositoryContention || calls != 1 || time.Since(started) > 2*time.Second {
		t.Fatal("busy write was not bounded/sanitized or repeated callback", calls, err)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertReviewUnchanged(t, f)
	query := app.Query{Kind: app.ListVersions, Content: f.subject.ContentID(), Page: app.PageRequest{Limit: 1}}
	page, err := catalogRead(t, f.owner, query)
	if err != nil || page.Next == "" {
		t.Fatal("cursor fixture", err)
	}
	var escaped app.CatalogRepository
	calls = 0
	if err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		calls++
		escaped = r
		return r.CommitReview(t.Context(), op, next, result)
	}); err != nil || calls != 1 {
		t.Fatal("commit callback", calls, err)
	}
	before := catalogRecoveryRows(t, f.owner.database.db)
	page, err = catalogRead(t, f.owner, query)
	if err != nil || page.Next == "" {
		t.Fatal("postcommit cursor fixture", err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = page.Next, page.Revision
	if _, err := catalogRead(t, f.owner, query); err != nil {
		t.Fatal("cursor already stale before reopen", err)
	}
	old, config := f.owner, f.owner.config
	var session contentstore.Session
	err = old.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		session = s
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	f.owner = owner
	if err := session.WithinTransaction(t.Context(), func(contentstore.BlobRepository) error { t.Error("old session invoked callback"); return nil }); err != contentstore.ErrRevoked {
		t.Fatal("old session", err)
	}
	if _, _, err := escaped.LoadReview(t.Context(), f.subject); err != app.ErrRepositoryUnavailable {
		t.Fatal("old repository", err)
	}
	if err := old.WithinCatalogTransaction(t.Context(), func(app.CatalogRepository) error { t.Error("old owner invoked callback"); return nil }); err != app.ErrRepositoryUnavailable {
		t.Fatal("old owner", err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = page.Next, page.Revision
	if _, err := catalogRead(t, owner, query); err != app.ErrStaleCursor {
		t.Fatal("old cursor", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		calls = 0
		err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
			calls++
			got, found, err := r.LookupReview(t.Context(), op)
			if err != nil || !found || !reflect.DeepEqual(got, result) {
				t.Fatal("lost-response lookup", found, err)
			}
			return r.CommitReview(t.Context(), op, next, result)
		})
		if err != nil || calls != 1 || !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
			t.Fatal("replay duplicated command or callback", calls, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls = 0
	started = time.Now()
	err = owner.WithinCatalogTransaction(ctx, func(app.CatalogRepository) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() || calls != 0 || time.Since(started) > time.Second {
		t.Fatal("canceled admission was not bounded/sanitized", calls, err)
	}
}
