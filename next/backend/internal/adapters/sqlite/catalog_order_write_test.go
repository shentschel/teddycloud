package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func orderWriteFixture(t *testing.T) (catalogStateFixture, domain.CatalogOrderState, app.OrderOperation, domain.CatalogOrderState, app.OrderResult) {
	t.Helper()
	f := newCatalogStateFixture(t, false)
	versions := []domain.ContentVersion{f.versions[0].Version, f.versions[1].Version}
	state, err := domain.NewCatalogOrderState(f.subject.ContentID(), versions)
	if err != nil {
		t.Fatal(err)
	}
	f.storeOrder(t, state, 1)
	id, _ := evidence.ParseID(catalogSchemaID("obs", 9))
	item, err := domain.NewCatalogSequenceEvidence(f.ingest, f.namespace, domain.CatalogSequenceInput{ID: id, Content: f.subject.ContentID(), Version: versions[0].ID(), Position: 7, Coordinate: f.coordinate(t)})
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := domain.NewCatalogCommandID()
	op := app.OrderOperation{ExpectedAggregate: state.Revision(), Capability: f.order, Command: domain.CatalogOrderCommand{ID: commandID, Content: f.subject.ContentID(), Namespace: f.namespace, Expected: state.Revision(), Reason: "explicit order", Evidence: []domain.CatalogSequenceEvidence{item}, Entries: []domain.CatalogOrderEntry{{Version: versions[0].ID(), Position: 7, Support: []evidence.ID{id}}}}}
	next, decision, err := state.Apply(f.order, op.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := domain.NewCatalogRevision(2)
	return f, state, op, next, app.OrderResult{AggregateRevision: revision, Decision: decision}
}

func commitOrderFixture(t *testing.T, f catalogStateFixture, op app.OrderOperation, next domain.CatalogOrderState, result app.OrderResult) error {
	t.Helper()
	return f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error { return r.CommitOrder(t.Context(), op, next, result) })
}

func assertOrderUnchanged(t *testing.T, f catalogStateFixture) {
	t.Helper()
	for table, want := range map[string]int{"tc_catalog_batches": 0, "tc_catalog_records": 0, "tc_catalog_order_states": 1, "tc_catalog_order_state_versions": 2, "tc_catalog_decisions": 0, "tc_catalog_observations": 0} {
		var count int
		if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatal(table, count, err)
		}
	}
	var aggregate int
	if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT aggregate_revision FROM tc_catalog_managed").Scan(&aggregate); err != nil || aggregate != 1 {
		t.Fatal(aggregate, err)
	}
}

func TestCatalogCommitOrderRoundTripReplay(t *testing.T) {
	f, _, op, next, result := orderWriteFixture(t)
	if err := commitOrderFixture(t, f, op, next, result); err != nil {
		t.Fatal(err)
	}
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		s, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
		if err != nil || !found || s.AggregateRevision != result.AggregateRevision {
			t.Fatal(found, err)
		}
		got, _ := s.State.PersistenceEncoding()
		want, _ := next.PersistenceEncoding()
		if !bytes.Equal(got, want) {
			t.Fatal("readback differs")
		}
		storedNamespace := s.State.EffectiveOrder().Positions[0].Namespace
		if storedNamespace.Source().Active() {
			t.Fatal("stored source is active")
		}
		if _, err := domain.NewCatalogOrderCapability(f.review, storedNamespace); !errors.Is(err, domain.ErrCatalogSourceMismatch) && !errors.Is(err, domain.ErrCatalogUnauthorized) {
			t.Fatal("storage granted authority", err)
		}
		gotResult, found, err := r.LookupOrder(t.Context(), op)
		if err != nil || !found || !reflect.DeepEqual(gotResult, result) {
			t.Fatal("lookup", found, err)
		}
		if err := r.CommitOrder(t.Context(), op, next, result); err != nil {
			return err
		}
		var queryOnly int
		if err := r.(*catalogRepository).transaction.QueryRowContext(t.Context(), "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
			t.Fatal("query fence", queryOnly, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := next.PersistenceEncoding()
	sum := sha256.Sum256(raw)
	var stored, digest []byte
	var size int
	if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT state,state_bytes,state_sha256 FROM tc_catalog_order_states WHERE order_revision=2").Scan(&stored, &size, &digest); err != nil || !bytes.Equal(stored, raw) || size != len(raw) || !bytes.Equal(digest, sum[:]) {
		t.Fatal("envelope", err)
	}
	second := op
	second.Command.ID, _ = domain.NewCatalogCommandID()
	second.ExpectedAggregate, second.Command.Expected = result.AggregateRevision, next.Revision()
	second.Command.Evidence = nil
	second.Command.Reason = "reaffirm"
	later, decision, err := next.Apply(f.order, second.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := domain.NewCatalogRevision(3)
	if err := commitOrderFixture(t, f, second, later, app.OrderResult{AggregateRevision: revision, Decision: decision}); err != nil {
		t.Fatal(err)
	}
	if err := commitOrderFixture(t, f, op, next, result); err != nil {
		t.Fatal("old exact replay", err)
	}
	for table, want := range map[string]int{"tc_catalog_order_states": 3, "tc_catalog_order_state_versions": 6, "tc_catalog_batches": 1, "tc_catalog_records": 1} {
		var count int
		if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatal(table, count, err)
		}
	}
	op.Command.Reason = "changed payload"
	if err := commitOrderFixture(t, f, op, next, result); !errors.Is(err, domain.ErrCatalogCommandConflict) {
		t.Fatal("changed replay", err)
	}
}

func TestCatalogCommitOrderRollback(t *testing.T) {
	for _, failure := range []string{"callback", "swallowed constraint", "cancel before commit", "cancel call", "revoked before commit", "SQL revoked before commit", "corruption", "bad result", "bad state", "stale aggregate", "stale order", "SQL revoked", "live revoked", "wrong namespace", "constraint"} {
		t.Run(failure, func(t *testing.T) {
			f, state, op, next, result := orderWriteFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := app.ErrRepositoryUnavailable
			switch failure {
			case "constraint", "swallowed constraint":
				catalogSchemaExec(t, f.owner.database.db, "CREATE TRIGGER reject_order BEFORE INSERT ON tc_catalog_order_states WHEN NEW.order_revision>1 BEGIN SELECT RAISE(ABORT,'private'); END")
			case "corruption":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_order_states_no_update")
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_order_states SET state_sha256=randomblob(32)")
			case "bad result":
				result.Decision = domain.CatalogOrderResult{}
				want = app.ErrInvalidInput
			case "bad state":
				next = state
				want = app.ErrInvalidInput
			case "stale aggregate":
				op.ExpectedAggregate, _ = domain.NewCatalogRevision(7)
				want = domain.ErrCatalogRevisionConflict
			case "stale order":
				op.Command.Expected, _ = domain.NewCatalogRevision(7)
				want = domain.ErrCatalogRevisionConflict
			case "SQL revoked":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET revoked=1")
				want = domain.ErrCatalogUnauthorized
			case "live revoked":
				f.source.Revoke()
				want = domain.ErrCatalogUnauthorized
			case "wrong namespace":
				op.Command.Namespace, _ = domain.NewCatalogOrderNamespace(f.source, "other/1")
				want = domain.ErrCatalogSourceMismatch
			case "cancel before commit", "cancel call":
				want = context.Canceled
			case "revoked before commit", "SQL revoked before commit":
				want = domain.ErrCatalogUnauthorized
			}
			err := f.owner.WithinCatalogTransaction(ctx, func(r app.CatalogRepository) error {
				if failure == "cancel call" {
					cancel()
				}
				err := r.CommitOrder(ctx, op, next, result)
				if failure == "swallowed constraint" || failure == "cancel call" {
					if !errors.Is(err, want) {
						t.Fatal(err)
					}
					return nil
				}
				if err != nil {
					return err
				}
				switch failure {
				case "callback":
					return want
				case "cancel before commit":
					cancel()
				case "revoked before commit":
					f.source.Revoke()
				case "SQL revoked before commit":
					tx := r.(*catalogRepository).transaction
					if _, err := tx.ExecContext(ctx, "PRAGMA query_only=OFF"); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, "UPDATE tc_catalog_sources SET revoked=1"); err != nil {
						return err
					}
				}
				return nil
			})
			if !errors.Is(err, want) {
				t.Fatal("error", err, "want", want)
			}
			assertOrderUnchanged(t, f)
		})
	}
}

func TestCatalogCommitOrderConcurrentRevisionFence(t *testing.T) {
	f, state, op, next, result := orderWriteFixture(t)
	op2 := op
	op2.Command.ID, _ = domain.NewCatalogCommandID()
	next2, decision2, err := state.Apply(f.order, op2.Command)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct {
		op     app.OrderOperation
		state  domain.CatalogOrderState
		result app.OrderResult
	}{{op, next, result}, {op2, next2, app.OrderResult{AggregateRevision: result.AggregateRevision, Decision: decision2}}} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- commitOrderFixture(t, f, item.op, item.state, item.result) }()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, domain.ErrCatalogRevisionConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}

func TestCatalogCommitOrderBusyRollback(t *testing.T) {
	f, _, op, next, result := orderWriteFixture(t)
	var sequence int
	var name, path string
	if err := f.owner.database.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), "UPDATE tc_catalog_managed SET aggregate_revision=aggregate_revision"); err != nil {
		t.Fatal(err)
	}
	if err := commitOrderFixture(t, f, op, next, result); !errors.Is(err, app.ErrRepositoryContention) {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertOrderUnchanged(t, f)
}

func TestCatalogCommitOrderProjectionFailureStages(t *testing.T) {
	for _, table := range []string{"tc_catalog_batches", "tc_catalog_records", "tc_catalog_order_states", "tc_catalog_order_state_versions"} {
		t.Run(table, func(t *testing.T) {
			f, _, op, next, result := orderWriteFixture(t)
			catalogSchemaExec(t, f.owner.database.db, fmt.Sprintf("CREATE TRIGGER injected_failure BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT,'private'); END", table))
			if err := commitOrderFixture(t, f, op, next, result); !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal(err)
			}
			assertOrderUnchanged(t, f)
		})
	}
}

func TestCatalogCommitOrderV5Unsupported(t *testing.T) {
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v5.sqlite")}, SchemaMigrations()[:5])
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		return r.CommitOrder(t.Context(), app.OrderOperation{}, domain.CatalogOrderState{}, app.OrderResult{})
	})
	if !errors.Is(err, app.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestCatalogCommitOrderInterleavedReview(t *testing.T) {
	f, _, op, next, result := orderWriteFixture(t)
	review, _ := domain.NewCatalogReviewState(f.subject, true)
	f.storeReview(t, review, 1)
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_managed SET protected_custom=1")
	if err := commitOrderFixture(t, f, op, next, result); err != nil {
		t.Fatal(err)
	}
	id, _ := domain.NewCatalogCommandID()
	observationID, _ := evidence.ParseID(catalogSchemaID("obs", 10))
	claim, _ := domain.TitleClaim("interleaved title")
	observation, err := domain.NewCatalogObservation(f.ingest, f.source, domain.ObservationInput{ID: observationID, Subject: f.subject, Claim: claim, Coordinate: f.coordinate(t), ObservedAt: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), Confidence: evidence.Confidence(1)})
	if err != nil {
		t.Fatal(err)
	}
	reviewOp := app.ReviewOperation{ExpectedAggregate: result.AggregateRevision, Capability: f.review, Command: domain.CatalogReviewCommand{ID: id, Kind: domain.RecordObservations, Subject: f.subject, Source: f.source, Expected: review.Revision(), Reason: "retain", Observations: []domain.CatalogObservation{observation}}}
	protected, decision, err := review.Apply(f.review, reviewOp.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := domain.NewCatalogRevision(3)
	if err := commitReviewFixture(t, f, reviewOp, protected, app.ReviewResult{AggregateRevision: revision, Decision: decision}); err != nil {
		t.Fatal(err)
	}
	if err := commitOrderFixture(t, f, op, next, result); err != nil {
		t.Fatal("old replay after review", err)
	}
	second := op
	second.Command.ID, _ = domain.NewCatalogCommandID()
	second.ExpectedAggregate, second.Command.Expected = revision, next.Revision()
	second.Command.Evidence = nil
	second.Command.Reason = "reaffirm after review"
	later, orderDecision, err := next.Apply(f.order, second.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ = domain.NewCatalogRevision(4)
	if err := commitOrderFixture(t, f, second, later, app.OrderResult{AggregateRevision: revision, Decision: orderDecision}); err != nil {
		t.Fatal(err)
	}
	var protectedSQL int
	if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT protected_custom FROM tc_catalog_managed").Scan(&protectedSQL); err != nil || protectedSQL != 1 {
		t.Fatal("order changed protection", protectedSQL, err)
	}
}
