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

func reviewWriteFixture(t *testing.T, version bool) (catalogStateFixture, app.ReviewOperation, domain.CatalogReviewState, app.ReviewResult) {
	t.Helper()
	f := newCatalogStateFixture(t, version)
	state, _ := domain.NewCatalogReviewState(f.subject, false)
	f.storeReview(t, state, 1)
	claim, _ := domain.TitleClaim("written title")
	if version {
		claim, _ = domain.FingerprintClaim(f.versions[0].Version.Fingerprint())
	}
	id, _ := evidence.ParseID(catalogSchemaID("obs", 7))
	observation, err := domain.NewCatalogObservation(f.ingest, f.source, domain.ObservationInput{ID: id, Subject: f.subject, Claim: claim, Coordinate: f.coordinate(t), ObservedAt: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), Confidence: evidence.Confidence(1)})
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := domain.NewCatalogCommandID()
	op := app.ReviewOperation{ExpectedAggregate: state.Revision(), Capability: f.review, Command: domain.CatalogReviewCommand{ID: commandID, Kind: domain.RecordObservations, Subject: f.subject, Source: f.source, Expected: state.Revision(), Observations: []domain.CatalogObservation{observation}, Reason: "retain"}}
	next, decision, err := state.Apply(f.review, op.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := domain.NewCatalogRevision(2)
	return f, op, next, app.ReviewResult{AggregateRevision: revision, Decision: decision}
}

func commitReviewFixture(t *testing.T, f catalogStateFixture, op app.ReviewOperation, next domain.CatalogReviewState, result app.ReviewResult) error {
	t.Helper()
	return f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error { return r.CommitReview(t.Context(), op, next, result) })
}

func assertReviewUnchanged(t *testing.T, f catalogStateFixture) {
	t.Helper()
	for _, table := range []string{"tc_catalog_batches", "tc_catalog_records", "tc_catalog_observations", "tc_catalog_decisions", "tc_catalog_decision_support"} {
		var count int
		if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	var aggregate, review, envelopes int
	if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT aggregate_revision,review_revision,(SELECT count(*) FROM tc_catalog_review_states) FROM tc_catalog_managed JOIN tc_catalog_subjects USING(content_id)").Scan(&aggregate, &review, &envelopes); err != nil || aggregate != 1 || review != 1 || envelopes != 1 {
		t.Fatal(aggregate, review, envelopes, err)
	}
}

func TestCatalogCommitReviewRoundTripReplayAndSupport(t *testing.T) {
	for _, version := range []bool{false, true} {
		t.Run(map[bool]string{false: "content", true: "version"}[version], func(t *testing.T) {
			f, op, next, result := reviewWriteFixture(t, version)
			if err := commitReviewFixture(t, f, op, next, result); err != nil {
				t.Fatal(err)
			}
			err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
				s, found, err := r.LoadReview(t.Context(), f.subject)
				if err != nil || !found || s.AggregateRevision != result.AggregateRevision {
					t.Fatal(found, err)
				}
				got, _ := s.State.PersistenceEncoding()
				want, _ := next.PersistenceEncoding()
				if !bytes.Equal(got, want) {
					t.Fatal("readback differs")
				}
				gotResult, found, err := r.LookupReview(t.Context(), op)
				if err != nil || !found || !reflect.DeepEqual(gotResult, result) {
					t.Fatal("lookup", found, err)
				}
				return r.CommitReview(t.Context(), op, next, result)
			})
			if err != nil {
				t.Fatal("exact replay", err)
			}
			raw, _ := next.PersistenceEncoding()
			sum := sha256.Sum256(raw)
			var stored, digest []byte
			var size int
			if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT state,state_bytes,state_sha256 FROM tc_catalog_review_states WHERE review_revision=2").Scan(&stored, &size, &digest); err != nil || !bytes.Equal(stored, raw) || size != len(raw) || !bytes.Equal(digest, sum[:]) {
				t.Fatal("envelope", err)
			}
			commandID, _ := domain.NewCatalogCommandID()
			accept := app.ReviewOperation{ExpectedAggregate: result.AggregateRevision, Capability: f.review, Command: domain.CatalogReviewCommand{ID: commandID, Kind: domain.AcceptSupport, Subject: f.subject, Source: f.source, Expected: next.Revision(), Key: op.Command.Observations[0].Input().Claim.Key(), Support: []evidence.ID{op.Command.Observations[0].ID()}, Reason: "accept"}}
			accepted, decision, err := next.Apply(f.review, accept.Command)
			if err != nil {
				t.Fatal(err)
			}
			revision, _ := domain.NewCatalogRevision(3)
			if err = commitReviewFixture(t, f, accept, accepted, app.ReviewResult{AggregateRevision: revision, Decision: decision}); err != nil {
				t.Fatal(err)
			}
			if err = commitReviewFixture(t, f, op, next, result); err != nil {
				t.Fatal("old exact replay", err)
			}
			for table, want := range map[string]int{"tc_catalog_observations": 1, "tc_catalog_decisions": 2, "tc_catalog_decision_support": 1, "tc_catalog_review_states": 3} {
				var count int
				if err = f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
					t.Fatal(table, count, err)
				}
			}
			op.Command.Reason = "changed"
			if err = commitReviewFixture(t, f, op, next, result); !errors.Is(err, domain.ErrCatalogCommandConflict) {
				t.Fatal("changed replay", err)
			}
		})
	}
}

func TestCatalogCommitReviewRollback(t *testing.T) {
	for _, failure := range []string{"callback", "swallowed constraint", "cancel before commit", "revoked before commit", "corruption", "bad result", "stale aggregate", "stale review", "SQL revoked", "live revoked", "constraint"} {
		t.Run(failure, func(t *testing.T) {
			f, op, next, result := reviewWriteFixture(t, false)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := app.ErrRepositoryUnavailable
			switch failure {
			case "constraint", "swallowed constraint":
				catalogSchemaExec(t, f.owner.database.db, "CREATE TRIGGER reject_review BEFORE INSERT ON tc_catalog_review_states WHEN NEW.review_revision>1 BEGIN SELECT RAISE(ABORT,'private SQL detail'); END")
			case "corruption":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_review_states_no_update")
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_review_states SET state_sha256=randomblob(32)")
			case "bad result":
				result.Decision = domain.CatalogDecisionResult{}
				want = app.ErrInvalidInput
			case "stale aggregate":
				op.ExpectedAggregate, _ = domain.NewCatalogRevision(7)
				want = domain.ErrCatalogRevisionConflict
			case "stale review":
				op.Command.Expected, _ = domain.NewCatalogRevision(7)
				want = domain.ErrCatalogRevisionConflict
			case "SQL revoked":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET revoked=1")
				want = domain.ErrCatalogUnauthorized
			case "live revoked":
				f.source.Revoke()
				want = domain.ErrCatalogUnauthorized
			case "cancel before commit":
				want = context.Canceled
			case "revoked before commit":
				want = domain.ErrCatalogUnauthorized
			}
			err := f.owner.WithinCatalogTransaction(ctx, func(r app.CatalogRepository) error {
				err := r.CommitReview(ctx, op, next, result)
				if failure == "swallowed constraint" {
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
				}
				return nil
			})
			if !errors.Is(err, want) {
				t.Fatal("error", err, "want", want)
			}
			assertReviewUnchanged(t, f)
		})
	}
}

func TestCatalogCommitReviewConcurrentRevisionFence(t *testing.T) {
	f, op, next, result := reviewWriteFixture(t, false)
	op2 := op
	op2.Command.ID, _ = domain.NewCatalogCommandID()
	state, _ := domain.NewCatalogReviewState(f.subject, false)
	next2, decision2, err := state.Apply(f.review, op2.Command)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct {
		op     app.ReviewOperation
		state  domain.CatalogReviewState
		result app.ReviewResult
	}{{op, next, result}, {op2, next2, app.ReviewResult{AggregateRevision: result.AggregateRevision, Decision: decision2}}} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- commitReviewFixture(t, f, item.op, item.state, item.result) }()
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

func TestCatalogCommitReviewBusyRollback(t *testing.T) {
	f, op, next, result := reviewWriteFixture(t, false)
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
	if _, err = tx.ExecContext(t.Context(), "UPDATE tc_catalog_managed SET aggregate_revision=aggregate_revision"); err != nil {
		t.Fatal(err)
	}
	if err = commitReviewFixture(t, f, op, next, result); !errors.Is(err, app.ErrRepositoryContention) {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertReviewUnchanged(t, f)
}

func TestCatalogCommitReviewNewSourceAndFailureStages(t *testing.T) {
	for _, table := range []string{"", "tc_catalog_sources", "tc_catalog_source_claims", "tc_catalog_batches", "tc_catalog_records", "tc_catalog_observations", "tc_catalog_decisions", "tc_catalog_review_states"} {
		t.Run("insert_"+table, func(t *testing.T) {
			f, op, _, _ := reviewWriteFixture(t, false)
			id, _ := domain.NewSourceID()
			issuer, _ := domain.NewIssuerPrincipal()
			var err error
			f.source, f.ingest, err = domain.NewSourceRegistration(id, issuer, f.source.Spec())
			if err != nil {
				t.Fatal(err)
			}
			reviewer, _ := domain.NewIssuerPrincipal()
			f.review, err = domain.NewReviewCapability(reviewer, f.source)
			if err != nil {
				t.Fatal(err)
			}
			input := op.Command.Observations[0].Input()
			input.Coordinate = f.coordinate(t)
			observation, err := domain.NewCatalogObservation(f.ingest, f.source, input)
			if err != nil {
				t.Fatal(err)
			}
			op.Capability, op.Command.Source, op.Command.Observations = f.review, f.source, []domain.CatalogObservation{observation}
			state, _ := domain.NewCatalogReviewState(f.subject, false)
			next, decision, err := state.Apply(f.review, op.Command)
			if err != nil {
				t.Fatal(err)
			}
			revision, _ := domain.NewCatalogRevision(2)
			if table != "" {
				catalogSchemaExec(t, f.owner.database.db, fmt.Sprintf("CREATE TRIGGER injected_failure BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT,'private'); END", table))
			}
			err = commitReviewFixture(t, f, op, next, app.ReviewResult{AggregateRevision: revision, Decision: decision})
			if table == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, app.ErrRepositoryUnavailable) {
					t.Fatal(err)
				}
				assertReviewUnchanged(t, f)
			}
			var count int
			if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_catalog_sources WHERE source_id=?", id.String()).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if table == "" && count != 1 || table != "" && count != 0 {
				t.Fatal("source rollback", count)
			}
		})
	}
}

func TestCatalogCommitReviewV5AndOrderUnsupported(t *testing.T) {
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v5.sqlite")}, SchemaMigrations()[:5])
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		return r.CommitReview(t.Context(), app.ReviewOperation{}, domain.CatalogReviewState{}, app.ReviewResult{})
	})
	if !errors.Is(err, app.ErrUnsupported) {
		t.Fatal(err)
	}
	f, op, next, result := reviewWriteFixture(t, false)
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		if err := r.CommitReview(t.Context(), op, next, result); err != nil {
			return err
		}
		if err := r.CommitOrder(t.Context(), app.OrderOperation{}, domain.CatalogOrderState{}, app.OrderResult{}); !errors.Is(err, app.ErrUnsupported) {
			t.Fatal(err)
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
}

func TestCatalogCommitReviewSwallowedCanceledCallRollsBack(t *testing.T) {
	f, op, next, result := reviewWriteFixture(t, false)
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		if err := r.CommitReview(t.Context(), op, next, result); err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := r.CommitReview(ctx, op, next, result); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertReviewUnchanged(t, f)
}
