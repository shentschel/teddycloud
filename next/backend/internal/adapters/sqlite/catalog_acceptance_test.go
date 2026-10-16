package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

// Ordinary review commands cannot implicitly adopt unmanaged baseline rows.
// Explicit adoption has its own capability and snapshot/revision contract.
func TestCatalogBaselineAdoptionRequiresRegistration(t *testing.T) {
	f, op, next, result := reviewWriteFixture(t, false)
	// Keep the valid core capabilities/command, but target a separate database
	// containing only legacy baseline rows, with all production triggers intact.
	f.owner = catalogQueryOwner(t)
	catalogSeedVersions(t, f.owner, 2)
	before := catalogRecoveryRows(t, f.owner.database.db)
	service, err := app.NewService(f.owner, f.source, f.review, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	command := app.ReviewCommand{ID: op.Command.ID, Subject: f.subject,
		ExpectedAggregate: op.ExpectedAggregate, ExpectedReview: op.Command.Expected,
		Observations: op.Command.Observations, Reason: op.Command.Reason}
	if _, err := service.RecordObservations(t.Context(), command); !errors.Is(err, app.ErrSubjectNotFound) {
		t.Fatalf("unmanaged service mutation: %v", err)
	}
	if err := commitReviewFixture(t, f, op, next, result); !errors.Is(err, app.ErrSubjectNotFound) {
		t.Fatalf("unmanaged adapter mutation: %v", err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("failed adoption changed stored rows")
	}
	page, err := catalogRead(t, f.owner, app.Query{Kind: app.GetContent,
		Content: f.subject.ContentID(), Page: app.PageRequest{Limit: 1}})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Review != app.CatalogUnmanaged || page.Entries[0].Title.Status != domain.CatalogFactUnknown {
		t.Fatalf("unmanaged baseline was promoted: %+v %v", page, err)
	}
}

func TestCatalogRawUnsupportedFieldRetention(t *testing.T) {
	f, op, _, _ := reviewWriteFixture(t, false)
	raw := []byte(`{ "title":"written title", "future": {"enum":"unknown", "values":[7,null,"<safe>"]}, "cover":"https://invalid.example/no-fetch" }`)
	digest := sha256.Sum256(raw)
	batch, err := domain.NewBatchID()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := domain.NewArtifact(f.ingest, f.source, domain.ArtifactFacts{ID: batch, SHA256: digest, ByteLength: uint64(len(raw)), RecordCount: 1, ParserRevision: "1", SourceRevision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := domain.NewRecordSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, err := domain.NewRecordCoordinate(artifact, 0, "record-0", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	input := op.Command.Observations[0].Input()
	input.Coordinate = coordinate
	observation, err := domain.NewCatalogObservation(f.ingest, f.source, input)
	if err != nil {
		t.Fatal(err)
	}
	op.Command.Observations = []domain.CatalogObservation{observation}
	state, err := domain.NewCatalogReviewState(f.subject, false)
	if err != nil {
		t.Fatal(err)
	}
	next, decision, err := state.Apply(f.review, op.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := domain.NewCatalogRevision(2)
	if err != nil {
		t.Fatal(err)
	}
	result := app.ReviewResult{AggregateRevision: revision, Decision: decision}
	if err := commitReviewFixture(t, f, op, next, result); err != nil {
		t.Fatal(err)
	}
	before := catalogRecoveryRows(t, f.owner.database.db)
	if err := commitReviewFixture(t, f, op, next, result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("exact replay changed retained evidence")
	}
	config := f.owner.config
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	var stored, storedDigest []byte
	var length uint64
	if err := reopened.database.db.QueryRowContext(t.Context(), "SELECT snapshot,artifact_sha256,byte_length FROM tc_catalog_records JOIN tc_catalog_batches USING(batch_id)").Scan(&stored, &storedDigest, &length); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, raw) || !bytes.Equal(storedDigest, digest[:]) || length != uint64(len(raw)) {
		t.Fatal("unknown field bytes or artifact identity changed after reopen")
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, reopened.database.db)) {
		t.Fatal("reopen changed rows")
	}
}

// Independent owners bypass the shared in-process lifecycle gate. Both read
// revision 1 before either writes, so SQLite must reject one snapshot promotion.
func TestCatalogRevisionRace(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	f, op, next, result := reviewWriteFixture(t, false)
	other, err := OpenLifecycleOwner(t.Context(), f.owner.config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	op2 := op
	op2.Command.ID, err = domain.NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	state, err := domain.NewCatalogReviewState(f.subject, false)
	if err != nil {
		t.Fatal(err)
	}
	next2, decision2, err := state.Apply(f.review, op2.Command)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	type outcome struct {
		index int
		err   error
		calls int
	}
	results := make(chan outcome, 2)
	owners := []*LifecycleOwner{f.owner, other}
	ops := []app.ReviewOperation{op, op2}
	states := []domain.CatalogReviewState{next, next2}
	envelopes := []app.ReviewResult{result, {AggregateRevision: result.AggregateRevision, Decision: decision2}}
	for index := range owners {
		go func() {
			calls := 0
			err := owners[index].WithinCatalogTransaction(ctx, func(r app.CatalogRepository) error {
				calls++
				loaded, found, err := r.LoadReview(t.Context(), f.subject)
				ready <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err != nil {
					return err
				}
				if !found || loaded.AggregateRevision.Uint64() != 1 {
					return app.ErrInvalidInput
				}
				return r.CommitReview(t.Context(), ops[index], states[index], envelopes[index])
			})
			results <- outcome{index, err, calls}
		}()
	}
	for range owners {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("writers did not reach the read barrier", ctx.Err())
		}
	}
	close(release)
	winner, loser := -1, -1
	for range owners {
		var got outcome
		select {
		case got = <-results:
		case <-ctx.Done():
			t.Fatal("writers did not finish", ctx.Err())
		}
		if got.calls != 1 {
			t.Fatalf("callback replayed: %d", got.calls)
		}
		if got.err == nil {
			if winner != -1 {
				t.Fatal("both stale writers committed")
			}
			winner = got.index
		} else if errors.Is(got.err, app.ErrRepositoryContention) || errors.Is(got.err, domain.ErrCatalogRevisionConflict) {
			loser = got.index
		} else {
			t.Fatal(got.err)
		}
	}
	if winner == -1 || loser == -1 {
		t.Fatal("race did not produce one winner and one rejection")
	}
	err = owners[loser].WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		return r.CommitReview(t.Context(), ops[loser], states[loser], envelopes[loser])
	})
	if !errors.Is(err, domain.ErrCatalogRevisionConflict) {
		t.Fatalf("fresh stale attempt: %v", err)
	}
	if err := verifyForeignKeys(t.Context(), f.owner.database.db); err != nil {
		t.Fatal(err)
	}
	var revision, decisions int
	if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT aggregate_revision,(SELECT count(*) FROM tc_catalog_decisions) FROM tc_catalog_managed").Scan(&revision, &decisions); err != nil || revision != 2 || decisions != 1 {
		t.Fatal(revision, decisions, err)
	}
}
