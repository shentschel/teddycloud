package catalog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const payload = "0123456789abcdefghjkmnpqrs"

type reviewReceipt struct {
	operation ReviewOperation
	result    ReviewResult
}

type orderReceipt struct {
	operation OrderOperation
	result    OrderResult
}

type contractRepository struct {
	review   ReviewSnapshot
	order    OrderSnapshot
	reviews  map[domaincatalog.CatalogCommandID]reviewReceipt
	orders   map[domaincatalog.CatalogCommandID]orderReceipt
	page     QueryResult
	failure  error
	onCommit func()
	writes   int
}

func (r *contractRepository) LoadReview(context.Context, domaincatalog.CatalogSubject) (ReviewSnapshot, bool, error) {
	return r.review, true, r.failure
}

func (r *contractRepository) LoadOrder(context.Context, identity.ContentID) (OrderSnapshot, bool, error) {
	return r.order, true, r.failure
}

func (r *contractRepository) LookupReview(_ context.Context, operation ReviewOperation) (ReviewResult, bool, error) {
	if r.failure != nil {
		return ReviewResult{}, false, r.failure
	}
	receipt, found := r.reviews[operation.Command.ID]
	if !found {
		return ReviewResult{}, false, nil
	}
	if receipt.operation.ExpectedAggregate != operation.ExpectedAggregate {
		return ReviewResult{}, false, ErrCommandConflict
	}
	decision, ok, err := r.review.State.LookupCommand(operation.Capability, operation.Command)
	if err != nil || !ok {
		return ReviewResult{}, false, err
	}
	result := receipt.result
	result.Decision = decision
	return result, true, nil
}

func (r *contractRepository) LookupOrder(_ context.Context, operation OrderOperation) (OrderResult, bool, error) {
	if r.failure != nil {
		return OrderResult{}, false, r.failure
	}
	receipt, found := r.orders[operation.Command.ID]
	if !found {
		return OrderResult{}, false, nil
	}
	if receipt.operation.ExpectedAggregate != operation.ExpectedAggregate {
		return OrderResult{}, false, ErrCommandConflict
	}
	decision, ok, err := r.order.State.LookupCommand(operation.Capability, operation.Command)
	if err != nil || !ok {
		return OrderResult{}, false, err
	}
	result := receipt.result
	result.Decision = decision
	return result, true, nil
}

func (r *contractRepository) CommitReview(_ context.Context, operation ReviewOperation, next domaincatalog.CatalogReviewState, result ReviewResult) error {
	if operation.ExpectedAggregate != r.review.AggregateRevision {
		return ErrRevisionConflict
	}
	r.writes++
	r.review = ReviewSnapshot{AggregateRevision: result.AggregateRevision, State: next}
	r.order.AggregateRevision = result.AggregateRevision
	r.reviews[operation.Command.ID] = reviewReceipt{operation, result}
	if r.onCommit != nil {
		r.onCommit()
	}
	return nil
}

func (r *contractRepository) CommitOrder(_ context.Context, operation OrderOperation, next domaincatalog.CatalogOrderState, result OrderResult) error {
	if operation.ExpectedAggregate != r.order.AggregateRevision {
		return ErrRevisionConflict
	}
	r.writes++
	r.order = OrderSnapshot{AggregateRevision: result.AggregateRevision, State: next}
	r.review.AggregateRevision = result.AggregateRevision
	r.orders[operation.Command.ID] = orderReceipt{operation, result}
	if r.onCommit != nil {
		r.onCommit()
	}
	return nil
}

func (r *contractRepository) Query(context.Context, Query) (QueryResult, error) {
	return r.page, r.failure
}

type contractTransaction struct {
	repository contractRepository
	calls      int
	repeat     bool
	skip       bool
}

func (tx *contractTransaction) WithinCatalogTransaction(ctx context.Context, callback func(CatalogRepository) error) error {
	tx.calls++
	if tx.skip {
		return nil
	}
	staged := tx.repository
	staged.reviews = make(map[domaincatalog.CatalogCommandID]reviewReceipt)
	staged.orders = make(map[domaincatalog.CatalogCommandID]orderReceipt)
	for id, receipt := range tx.repository.reviews {
		staged.reviews[id] = receipt
	}
	for id, receipt := range tx.repository.orders {
		staged.orders[id] = receipt
	}
	err := callback(&staged)
	if tx.repeat {
		return callback(&staged)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.repository = staged
	return nil
}

type fixture struct {
	service    Service
	tx         *contractTransaction
	source     domaincatalog.SourceRegistration
	ingest     domaincatalog.IngestCapability
	namespace  domaincatalog.CatalogOrderNamespace
	subject    domaincatalog.CatalogSubject
	coordinate domaincatalog.RecordCoordinate
	versions   []domaincatalog.ContentVersion
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	sourceID, err := domaincatalog.NewSourceID()
	require(t, err)
	principal, err := domaincatalog.NewIssuerPrincipal()
	require(t, err)
	source, ingest, err := domaincatalog.NewSourceRegistration(sourceID, principal, domaincatalog.SourceSpec{Adapter: "synthetic", AdapterVersion: "1", Scope: "catalog", Acquisition: domaincatalog.OfflineCustom, AllowedClaims: []domaincatalog.ClaimKey{domaincatalog.TitleClaimKey}})
	require(t, err)
	reviewer, err := domaincatalog.NewIssuerPrincipal()
	require(t, err)
	review, err := domaincatalog.NewReviewCapability(reviewer, source)
	require(t, err)
	namespace, err := domaincatalog.NewCatalogOrderNamespace(source, "sequence/1")
	require(t, err)
	content, err := identity.ParseContentID("cnt_" + payload)
	require(t, err)
	subject, err := domaincatalog.ContentSubject(content)
	require(t, err)
	state, err := domaincatalog.NewCatalogReviewState(subject, false)
	require(t, err)
	audio, _ := domaincatalog.NewAudioID(99)
	hash, _ := domaincatalog.ParseAudioHash(strings.Repeat("a", 40))
	pair, _ := domaincatalog.NewAudioFingerprint(audio, hash)
	var versions []domaincatalog.ContentVersion
	for _, suffix := range []string{payload, "1123456789abcdefghjkmnpqrs"} {
		id, err := identity.ParseContentVersionID("ver_" + suffix)
		require(t, err)
		version, err := domaincatalog.NewContentVersion(id, content, pair, domaincatalog.UnknownVersionOrderEvidence())
		require(t, err)
		versions = append(versions, version)
	}
	order, err := domaincatalog.NewCatalogOrderState(content, versions)
	require(t, err)
	revision, _ := domaincatalog.NewCatalogRevision(1)
	tx := &contractTransaction{repository: contractRepository{review: ReviewSnapshot{revision, state}, order: OrderSnapshot{revision, order}, reviews: make(map[domaincatalog.CatalogCommandID]reviewReceipt), orders: make(map[domaincatalog.CatalogCommandID]orderReceipt)}}
	service, err := NewService(tx, source, review, namespace)
	require(t, err)
	batch, _ := domaincatalog.NewBatchID()
	artifact, err := domaincatalog.NewArtifact(ingest, source, domaincatalog.ArtifactFacts{ID: batch, SHA256: [32]byte{1}, ByteLength: 100, RecordCount: 2, ParserRevision: "1"})
	require(t, err)
	snapshot, _ := domaincatalog.NewRecordSnapshot([]byte("{}"))
	coordinate, err := domaincatalog.NewRecordCoordinate(artifact, 0, "private-locator", snapshot)
	require(t, err)
	return fixture{service, tx, source, ingest, namespace, subject, coordinate, versions}
}

func (f fixture) observation(t *testing.T, suffix, title string) domaincatalog.CatalogObservation {
	t.Helper()
	id, _ := evidence.ParseID("obs_" + suffix)
	claim, _ := domaincatalog.TitleClaim(title)
	observation, err := domaincatalog.NewCatalogObservation(f.ingest, f.source, domaincatalog.ObservationInput{ID: id, Subject: f.subject, Claim: claim, Coordinate: f.coordinate, ObservedAt: time.Unix(1, 0), Confidence: evidence.Corroborated})
	require(t, err)
	return observation
}

func (f fixture) command(t *testing.T) ReviewCommand {
	t.Helper()
	id, err := domaincatalog.NewCatalogCommandID()
	require(t, err)
	return ReviewCommand{ID: id, Subject: f.subject, ExpectedAggregate: f.tx.repository.review.AggregateRevision, ExpectedReview: f.tx.repository.review.State.Revision(), Reason: "explicit review"}
}

func TestCatalogServiceReviewAndReplay(t *testing.T) {
	f := newFixture(t)
	a := f.observation(t, payload, "A")
	b := f.observation(t, "1123456789abcdefghjkmnpqrs", "B")
	stage := f.command(t)
	stage.Observations = []domaincatalog.CatalogObservation{b, a}
	staged, err := f.service.RecordObservations(context.Background(), stage)
	require(t, err)
	if !staged.Decision.ProtectedCustom || staged.Decision.Field.Status != domaincatalog.CatalogFactUnknown {
		t.Fatal("staging dropped protection or confidence accepted evidence")
	}
	accept := f.command(t)
	accept.Key, accept.Support = domaincatalog.TitleClaimKey, []evidence.ID{a.ID(), b.ID()}
	accepted, err := f.service.AcceptSupport(context.Background(), accept)
	require(t, err)
	if accepted.Decision.Field.Status != domaincatalog.CatalogFactConflict {
		t.Fatal("contrary support did not expose conflict")
	}
	if _, ok := accepted.Decision.Field.Value(); ok {
		t.Fatal("conflict selected scalar")
	}
	resolve := f.command(t)
	resolve.Key, resolve.Support = domaincatalog.TitleClaimKey, []evidence.ID{b.ID()}
	_, err = f.service.ResolveConflict(context.Background(), resolve)
	require(t, err)
	writes := f.tx.repository.writes
	replayed, err := f.service.AcceptSupport(context.Background(), accept)
	require(t, err)
	if !reflect.DeepEqual(replayed, accepted) || writes != f.tx.repository.writes {
		t.Fatal("replay reconstructed current result or wrote again")
	}
	readback, found, err := f.service.LookupReview(context.Background(), domaincatalog.AcceptSupport, accept)
	require(t, err)
	if !found || !reflect.DeepEqual(readback, accepted) {
		t.Fatal("readback lost original result")
	}
	changed := accept
	changed.ExpectedAggregate = resolve.ExpectedAggregate
	_, err = f.service.AcceptSupport(context.Background(), changed)
	if !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed aggregate envelope did not conflict")
	}
	changed = accept
	changed.Reason = "different"
	_, err = f.service.AcceptSupport(context.Background(), changed)
	if !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed canonical command did not conflict")
	}
	f.source.Revoke()
	_, _, err = f.service.LookupReview(context.Background(), domaincatalog.AcceptSupport, accept)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal("replay restored revoked capability")
	}
}

func TestCatalogServiceOrderAndAggregateFence(t *testing.T) {
	f := newFixture(t)
	id, _ := domaincatalog.NewCatalogCommandID()
	command := OrderCommand{ID: id, Content: f.subject.ContentID(), ExpectedAggregate: f.tx.repository.order.AggregateRevision, ExpectedOrder: f.tx.repository.order.State.Revision(), Reason: "explicit order"}
	for i, version := range f.versions {
		suffix := payload
		if i == 1 {
			suffix = "1123456789abcdefghjkmnpqrs"
		}
		observationID, _ := evidence.ParseID("obs_" + suffix)
		observation, err := domaincatalog.NewCatalogSequenceEvidence(f.ingest, f.namespace, domaincatalog.CatalogSequenceInput{ID: observationID, Content: command.Content, Version: version.ID(), Position: uint64(i), Coordinate: f.coordinate})
		require(t, err)
		command.Evidence = append(command.Evidence, observation)
		command.Entries = append(command.Entries, domaincatalog.CatalogOrderEntry{Version: version.ID(), Position: uint64(i), Support: []evidence.ID{observationID}})
	}
	result, err := f.service.DecideOrder(context.Background(), command)
	require(t, err)
	if result.Decision.View.Status != domaincatalog.CatalogFactAccepted || result.AggregateRevision.Uint64() != 2 {
		t.Fatal("order failed")
	}
	readback, found, err := f.service.LookupOrder(context.Background(), command)
	require(t, err)
	if !found || !reflect.DeepEqual(result, readback) {
		t.Fatal("order readback failed")
	}
	replayed, err := f.service.DecideOrder(context.Background(), command)
	require(t, err)
	if !reflect.DeepEqual(result, replayed) || f.tx.repository.writes != 1 {
		t.Fatal("order replay wrote again")
	}
	stage := f.command(t)
	stage.Observations = []domaincatalog.CatalogObservation{f.observation(t, "2123456789abcdefghjkmnpqrs", "new")}
	if stage.ExpectedAggregate.Uint64() != 2 || stage.ExpectedReview.Uint64() != 1 {
		t.Fatal("aggregate and decision revisions were conflated")
	}
	_, err = f.service.RecordObservations(context.Background(), stage)
	require(t, err)
	stale := stage
	stale.ID, _ = domaincatalog.NewCatalogCommandID()
	calls := f.tx.calls
	_, err = f.service.RecordObservations(context.Background(), stale)
	if !errors.Is(err, ErrRevisionConflict) || f.tx.calls != calls+1 {
		t.Fatal("revision fence failed or service retried")
	}
}

func TestCatalogServiceCancellationAndCallbackOnce(t *testing.T) {
	for _, mode := range []string{"canceled", "commit-cancel", "repeat", "skip"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			command := f.command(t)
			command.Observations = []domaincatalog.CatalogObservation{f.observation(t, payload, "A")}
			switch mode {
			case "canceled":
				cancel()
			case "commit-cancel":
				f.tx.repository.onCommit = cancel
			case "repeat":
				f.tx.repeat = true
			case "skip":
				f.tx.skip = true
			}
			result, err := f.service.RecordObservations(ctx, command)
			if err == nil || result.AggregateRevision.Uint64() != 0 || f.tx.repository.writes != 0 {
				t.Fatal("cancellation/invalid callback committed")
			}
			if mode == "canceled" && f.tx.calls != 0 {
				t.Fatal("canceled operation entered transaction")
			}
		})
	}
}

func TestCatalogServiceSanitizedErrorsAndOverflow(t *testing.T) {
	f := newFixture(t)
	command := f.command(t)
	command.Observations = []domaincatalog.CatalogObservation{f.observation(t, payload, "A")}
	for _, failure := range []error{errors.New("credential snapshot private-locator"), fmt.Errorf("private-locator: %w", ErrRevisionConflict), fmt.Errorf("private-locator: %w", ErrIndeterminate)} {
		f.tx.repository.failure = failure
		_, err := f.service.RecordObservations(context.Background(), command)
		if err == nil || strings.Contains(err.Error(), "private-locator") || strings.Contains(err.Error(), "credential") {
			t.Fatal("private cause escaped")
		}
	}
	f.tx.repository.failure = nil
	maximum, _ := domaincatalog.NewCatalogRevision(math.MaxInt64)
	f.tx.repository.review.AggregateRevision = maximum
	command.ExpectedAggregate = maximum
	_, err := f.service.RecordObservations(context.Background(), command)
	if !errors.Is(err, ErrRevisionOverflow) || f.tx.repository.writes != 0 {
		t.Fatal("aggregate overflow committed")
	}
	if _, err := NewService(f.tx, f.source, domaincatalog.ReviewCapability{}, f.namespace); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("empty authority installed")
	}
}

func TestCatalogServiceQueries(t *testing.T) {
	f := newFixture(t)
	query := Query{Kind: FindVersionsByPair, Fingerprint: f.versions[0].Fingerprint(), Page: PageRequest{Limit: 128}}
	revision, _ := domaincatalog.NewCatalogRevision(1)
	records := []CatalogRecord{
		{Content: f.subject.ContentID(), Version: f.versions[0], ProtectedCustom: true, Media: CatalogOnly},
		{Content: f.subject.ContentID(), Version: f.versions[1], Media: MediaBoundUnchecked},
	}
	for _, kind := range []ResultKind{NoMatch, Found, Ambiguous, Conflicted, Incomplete} {
		page := QueryResult{Kind: kind, Revision: revision, Complete: kind != Incomplete, Inspected: 2, EncodedBytes: 100}
		switch kind {
		case Found, Conflicted, Incomplete:
			page.Entries = records[:1]
		case Ambiguous:
			page.Entries = records
		}
		f.tx.repository.page = page
		result, err := f.service.Query(context.Background(), query)
		require(t, err)
		if !reflect.DeepEqual(result, page) {
			t.Fatal("query chose first candidate or changed typed media state")
		}
	}
	f.tx.repository.page = QueryResult{Kind: Ambiguous, Entries: []CatalogRecord{records[1], records[0]}, Revision: revision, Complete: true, EncodedBytes: 100}
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrRepositoryUnavailable) {
		t.Fatal("unstable page ordering accepted")
	}
	query.Page.Cursor = strings.Repeat("x", 257)
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("cursor bound ignored")
	}
	query.Page.Cursor = "opaque"
	query.Page.ExpectedRevision, _ = domaincatalog.NewCatalogRevision(2)
	f.tx.repository.page = QueryResult{Kind: Found, Entries: records[:1], Revision: revision, Complete: true, EncodedBytes: 100}
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrStaleCursor) {
		t.Fatal("stale revision page accepted")
	}
	query.Page = PageRequest{Limit: 128}
	f.tx.repository.page.EncodedBytes = domaincatalog.QueryBytes.Maximum() + 1
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("query byte bound ignored")
	}
}
