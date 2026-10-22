package catalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// This owner double models only the application seam: callback lifetime,
// atomic staging, canonical replay and owner pre-commit checks. Real SQL write
// stages, revocation rows, cross-writer collisions and restore belong to C/D.
type registrationReceipt struct {
	canonical string
	result    domain.CatalogRegistrationResult
}

type registrationStore struct {
	receipts   map[domain.CatalogCommandID]registrationReceipt
	subjects   map[domain.CatalogSubject]bool
	aggregates map[identity.ContentID]domain.CatalogRevision
	orders     map[identity.ContentID]domain.CatalogRevision
	protected  map[identity.ContentID]bool
	writes     int
}

func (s registrationStore) clone() registrationStore {
	next := registrationStore{
		receipts: make(map[domain.CatalogCommandID]registrationReceipt), subjects: make(map[domain.CatalogSubject]bool),
		aggregates: make(map[identity.ContentID]domain.CatalogRevision), orders: make(map[identity.ContentID]domain.CatalogRevision),
		protected: make(map[identity.ContentID]bool), writes: s.writes,
	}
	for k, v := range s.receipts {
		next.receipts[k] = v
	}
	for k, v := range s.subjects {
		next.subjects[k] = v
	}
	for k, v := range s.aggregates {
		next.aggregates[k] = v
	}
	for k, v := range s.orders {
		next.orders[k] = v
	}
	for k, v := range s.protected {
		next.protected[k] = v
	}
	return next
}

type registrationOwner struct {
	store                   registrationStore
	calls, lookups, commits int
	skip, repeat, nilPort   bool
	noExtension             bool
	persistedRevoked        bool
	lookupError             error
	commitError             error
	finishError             error
	durable                 bool
	afterLookup             func()
	afterWrite              func()
	beforeCommit            func()
	afterCommit             func()
	response                func(*domain.CatalogRegistrationResult)
	leaked                  *registrationPort
	deadline                time.Time
	remaining               time.Duration
}

type registrationPort struct {
	CatalogRepository
	owner  *registrationOwner
	store  *registrationStore
	active bool
	used   []RegistrationOperation
}

func (p *registrationPort) authorize(ctx context.Context, op RegistrationOperation) error {
	if !p.active {
		return ErrRepositoryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.owner.persistedRevoked {
		return domain.ErrCatalogUnauthorized
	}
	return op.Capability.Authorize(op.Command)
}

func (p *registrationPort) LookupRegistration(ctx context.Context, op RegistrationOperation) (domain.CatalogRegistrationResult, bool, error) {
	if err := p.authorize(ctx, op); err != nil {
		return domain.CatalogRegistrationResult{}, false, err
	}
	p.owner.lookups++
	p.used = append(p.used, op)
	if p.owner.lookupError != nil {
		return domain.CatalogRegistrationResult{}, false, p.owner.lookupError
	}
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		return domain.CatalogRegistrationResult{}, false, err
	}
	receipt, found := p.store.receipts[op.Command.ID]
	if found && receipt.canonical != string(raw) {
		return domain.CatalogRegistrationResult{}, false, domain.ErrCatalogCommandConflict
	}
	result := receipt.result
	if p.owner.afterLookup != nil {
		p.owner.afterLookup()
	}
	if p.owner.response != nil {
		p.owner.response(&result)
	}
	return result, found, nil
}

func (p *registrationPort) CommitRegistration(ctx context.Context, op RegistrationOperation) (domain.CatalogRegistrationResult, error) {
	if err := p.authorize(ctx, op); err != nil {
		return domain.CatalogRegistrationResult{}, err
	}
	p.owner.commits++
	if p.owner.commitError != nil {
		return domain.CatalogRegistrationResult{}, p.owner.commitError
	}
	if p.store.subjects[op.Command.Subject] {
		return domain.CatalogRegistrationResult{}, domain.ErrCatalogCommandConflict
	}
	content := op.Command.Subject.ContentID()
	order, _ := domain.NewCatalogRevision(1)
	if op.Command.Kind == domain.RegisterCatalogVersion {
		revision, found := p.store.aggregates[content]
		if !found {
			return domain.CatalogRegistrationResult{}, ErrSubjectNotFound
		}
		if revision != op.Command.Expected {
			return domain.CatalogRegistrationResult{}, domain.ErrCatalogRevisionConflict
		}
		order, _ = domain.NewCatalogRevision(p.store.orders[content].Uint64() + 1)
	}
	_, result, err := domain.NewRegisteredCatalogReviewState(op.Capability, op.Command, domain.CatalogRegistrationContext{OrderRevision: order, ProtectedCustom: p.store.protected[content]})
	if err != nil {
		return domain.CatalogRegistrationResult{}, err
	}
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		return domain.CatalogRegistrationResult{}, err
	}
	p.store.receipts[op.Command.ID] = registrationReceipt{string(raw), result}
	p.store.subjects[op.Command.Subject] = true
	p.store.aggregates[content] = result.AggregateRevision
	p.store.orders[content] = result.OrderRevision
	p.store.protected[content] = result.ProtectedCustom
	p.store.writes++
	if p.owner.afterWrite != nil {
		p.owner.afterWrite()
	}
	if p.owner.response != nil {
		p.owner.response(&result)
	}
	return result, nil
}

func (tx *registrationOwner) WithinCatalogTransaction(ctx context.Context, callback func(CatalogRepository) error) error {
	tx.calls++
	tx.deadline, _ = ctx.Deadline()
	tx.remaining = time.Until(tx.deadline)
	if tx.skip {
		return nil
	}
	if tx.nilPort {
		return callback(nil)
	}
	if tx.noExtension {
		return callback(&contractRepository{})
	}
	staged := tx.store.clone()
	port := &registrationPort{owner: tx, store: &staged, active: true}
	tx.leaked = port
	defer func() { port.active = false }()
	err := callback(port)
	if tx.repeat {
		return callback(port)
	}
	if err != nil {
		return err
	}
	if tx.beforeCommit != nil {
		tx.beforeCommit()
	}
	// The owner, not the service, closes the callback/commit authorization gap.
	for _, op := range port.used {
		if err := port.authorize(ctx, op); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx.finishError != nil && !tx.durable {
		if tx.afterCommit != nil {
			tx.afterCommit()
		}
		return tx.finishError
	}
	tx.store = staged
	if tx.afterCommit != nil {
		tx.afterCommit()
	}
	return tx.finishError
}

type registrationFixture struct {
	service   RegistrationService
	owner     *registrationOwner
	operator  domain.SourceRegistration
	offline   domain.SourceRegistration
	opIngest  domain.IngestCapability
	offIngest domain.IngestCapability
	reviewer  domain.IssuerPrincipal
	reviews   []domain.ReviewCapability
	content   RegistrationCommand
	version   RegistrationCommand
}

func newRegistrationFixture(t *testing.T) registrationFixture {
	t.Helper()
	f := registrationFixture{owner: &registrationOwner{store: (registrationStore{}).clone()}}
	f.reviewer, _ = domain.NewIssuerPrincipal()
	issuer, _ := domain.NewIssuerPrincipal()
	opID, _ := domain.NewSourceID()
	offID, _ := domain.NewSourceID()
	var err error
	f.operator, f.opIngest, err = domain.NewSourceRegistration(opID, issuer, domain.SourceSpec{Adapter: "operator", AdapterVersion: "1", Scope: "catalog", Acquisition: domain.CoreOperator, AllowedClaims: []domain.ClaimKey{domain.TitleClaimKey, domain.OwnershipClaimKey}})
	require(t, err)
	f.offline, f.offIngest, err = domain.NewSourceRegistration(offID, issuer, domain.SourceSpec{Adapter: "offline", AdapterVersion: "1", Scope: "catalog", Acquisition: domain.OfflineCustom, AllowedClaims: []domain.ClaimKey{domain.TitleClaimKey, domain.FingerprintClaimKey}})
	require(t, err)
	opReview, err := domain.NewReviewCapability(f.reviewer, f.operator)
	require(t, err)
	offReview, err := domain.NewReviewCapability(f.reviewer, f.offline)
	require(t, err)
	f.reviews = []domain.ReviewCapability{opReview, offReview}
	capability, err := domain.NewCatalogRegistrationCapability(opReview, f.reviews)
	require(t, err)
	namespace, err := domain.NewCatalogOrderNamespace(f.operator, "registration/1")
	require(t, err)
	base, err := NewService(f.owner, f.operator, opReview, namespace)
	require(t, err)
	f.service, err = NewRegistrationService(base, capability)
	require(t, err)
	contentID, err := identity.ParseContentID("cnt_" + payload)
	require(t, err)
	versionID, err := identity.ParseContentVersionID("ver_" + payload)
	require(t, err)
	contentSubject, _ := domain.ContentSubject(contentID)
	versionSubject, _ := domain.VersionSubject(contentID, versionID)
	contentCommand, _ := domain.NewCatalogCommandID()
	versionCommand, _ := domain.NewCatalogCommandID()
	title, _ := domain.TitleClaim("supported title")
	owner, _ := domain.OwnershipClaim(contentID)
	audio, _ := domain.NewAudioID(1)
	hash, _ := domain.ParseAudioHash(strings.Repeat("11", 20))
	pair, _ := domain.NewAudioFingerprint(audio, hash)
	fingerprint, _ := domain.FingerprintClaim(pair)
	titleObservation := f.observation(t, f.offline, f.offIngest, contentSubject, title, payload)
	pairObservation := f.observation(t, f.offline, f.offIngest, versionSubject, fingerprint, payload)
	ownerObservation := f.observation(t, f.operator, f.opIngest, versionSubject, owner, "1123456789abcdefghjkmnpqrs")
	f.content = RegistrationCommand{ID: contentCommand, Subject: contentSubject, Reason: "register content", Observations: []domain.CatalogObservation{titleObservation}, Selections: []domain.CatalogRegistrationSelection{{Key: domain.TitleClaimKey, Observation: titleObservation.ID()}}}
	one, _ := domain.NewCatalogRevision(1)
	f.version = RegistrationCommand{ID: versionCommand, Subject: versionSubject, ExpectedKind: domain.CatalogExpectedRevision, Expected: one, Reason: "register version", Observations: []domain.CatalogObservation{pairObservation, ownerObservation}, Selections: []domain.CatalogRegistrationSelection{{Key: domain.FingerprintClaimKey, Observation: pairObservation.ID()}, {Key: domain.OwnershipClaimKey, Observation: ownerObservation.ID()}}}
	return f
}

func (f registrationFixture) observation(t *testing.T, source domain.SourceRegistration, ingest domain.IngestCapability, subject domain.CatalogSubject, claim domain.CatalogClaim, id string) domain.CatalogObservation {
	t.Helper()
	batch, _ := domain.NewBatchID()
	artifact, err := domain.NewArtifact(ingest, source, domain.ArtifactFacts{ID: batch, SHA256: sha256.Sum256([]byte("{}")), ByteLength: 2, RecordCount: 1, ParserRevision: "1"})
	require(t, err)
	snapshot, err := domain.NewRecordSnapshot([]byte("{}"))
	require(t, err)
	coordinate, err := domain.NewRecordCoordinate(artifact, 0, "0", snapshot)
	require(t, err)
	observationID, err := evidence.ParseID("obs_" + id)
	require(t, err)
	observation, err := domain.NewCatalogObservation(ingest, source, domain.ObservationInput{ID: observationID, Subject: subject, Claim: claim, Coordinate: coordinate, ObservedAt: time.Unix(1, 0)})
	require(t, err)
	return observation
}

func assertRegistrationFailure(t *testing.T, result domain.CatalogRegistrationResult, err, want error) {
	t.Helper()
	if err != want || errors.Unwrap(err) != nil || !reflect.DeepEqual(result, domain.CatalogRegistrationResult{}) {
		t.Fatalf("result=%+v error=%v; want zero result and exact %v", result, err, want)
	}
}

func assertRegistrationUnchanged(t *testing.T, before registrationStore, owner *registrationOwner) {
	t.Helper()
	if !reflect.DeepEqual(before, owner.store) {
		t.Fatal("rejected operation changed committed state")
	}
}

func TestRegistrationAuthority(t *testing.T) {
	t.Run("zero/ingest-only/detached handles", func(t *testing.T) {
		f := newRegistrationFixture(t)
		if _, err := NewRegistrationService(f.service.service, domain.CatalogRegistrationCapability{}); err != ErrUnauthorized {
			t.Fatal(err)
		}
		if _, err := domain.NewCatalogRegistrationCapability(domain.ReviewCapability{}, nil); err != domain.ErrCatalogUnauthorized {
			t.Fatal("ingest-only source gained registration authority", err)
		}
		raw, err := f.operator.PersistenceEncoding()
		require(t, err)
		detached, err := domain.RehydrateSourceRegistration(raw, domain.CatalogPersistenceBindings{AllowUnbound: true})
		require(t, err)
		review, err := domain.NewReviewCapability(f.reviewer, detached)
		require(t, err)
		if _, err := domain.NewCatalogRegistrationCapability(review, []domain.ReviewCapability{review}); err != domain.ErrCatalogUnauthorized {
			t.Fatal("detached provenance gained authority", err)
		}
	})
	t.Run("spoofed reviewer", func(t *testing.T) {
		f := newRegistrationFixture(t)
		other, _ := domain.NewIssuerPrincipal()
		op, _ := domain.NewReviewCapability(other, f.operator)
		off, _ := domain.NewReviewCapability(other, f.offline)
		capability, err := domain.NewCatalogRegistrationCapability(op, []domain.ReviewCapability{op, off})
		require(t, err)
		if _, err := NewRegistrationService(f.service.service, capability); err != ErrUnauthorized {
			t.Fatal("wrapper accepted a different operator reviewer", err)
		}
	})
	t.Run("wrong live source or revision", func(t *testing.T) {
		f := newRegistrationFixture(t)
		other, _, err := domain.NewSourceRegistration(f.operator.ID(), f.operator.Issuer(), f.operator.Spec())
		require(t, err)
		review, _ := domain.NewReviewCapability(f.reviewer, other)
		capability, err := domain.NewCatalogRegistrationCapability(review, []domain.ReviewCapability{review})
		require(t, err)
		if _, err := NewRegistrationService(f.service.service, capability); err != ErrSourceMismatch {
			t.Fatal("same labels rebound a different revision", err)
		}
	})
	for _, source := range []string{"operator", "evidence"} {
		for _, stage := range []string{"before lookup", "after lookup", "after write", "before commit"} {
			t.Run("revoked source/"+source+"/"+stage, func(t *testing.T) {
				f := newRegistrationFixture(t)
				_, err := f.service.RegisterContent(t.Context(), f.content)
				require(t, err)
				before := f.owner.store.clone()
				revoke := f.operator.Revoke
				if source == "evidence" {
					revoke = f.offline.Revoke
				}
				switch stage {
				case "before lookup":
					revoke()
				case "after lookup":
					f.owner.afterLookup = revoke
				case "after write":
					f.owner.afterWrite = revoke
				case "before commit":
					f.owner.beforeCommit = revoke
				}
				got, err := f.service.RegisterCatalogVersion(t.Context(), f.version)
				assertRegistrationFailure(t, got, err, ErrUnauthorized)
				assertRegistrationUnchanged(t, before, f.owner)
				got, found, err := f.service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
				assertRegistrationFailure(t, got, err, ErrUnauthorized)
				if found {
					t.Fatal("revoked source read committed registration")
				}
			})
		}
	}
	t.Run("persisted revocation", func(t *testing.T) {
		f := newRegistrationFixture(t)
		_, err := f.service.RegisterContent(t.Context(), f.content)
		require(t, err)
		before := f.owner.store.clone()
		f.owner.persistedRevoked = true
		got, err := f.service.RegisterContent(t.Context(), f.content)
		assertRegistrationFailure(t, got, err, ErrUnauthorized)
		got, found, err := f.service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
		assertRegistrationFailure(t, got, err, ErrUnauthorized)
		if found {
			t.Fatal("persisted revocation bypassed by readback")
		}
		assertRegistrationUnchanged(t, before, f.owner)
	})
	t.Run("forbidden ownership from offline source", func(t *testing.T) {
		f := newRegistrationFixture(t)
		_, err := f.service.RegisterContent(t.Context(), f.content)
		require(t, err)
		// An offline-only registration capability has no operator grant.
		if _, err := domain.NewCatalogRegistrationCapability(f.reviews[1], f.reviews); err != domain.ErrCatalogUnauthorized {
			t.Fatal(err)
		}
		if err := f.reviews[1].Authorize(f.offline, domain.OwnershipClaimKey); err != domain.ErrCatalogUnauthorized {
			t.Fatal(err)
		}
	})
	t.Run("current authorization precedes malformed input", func(t *testing.T) {
		f := newRegistrationFixture(t)
		f.operator.Revoke()
		got, err := f.service.RegisterContent(t.Context(), RegistrationCommand{})
		assertRegistrationFailure(t, got, err, ErrUnauthorized)
		if f.owner.lookups != 0 {
			t.Fatal("lookup ran before authorization")
		}
	})
	t.Run("exact trusted restart rebind", func(t *testing.T) {
		f := newRegistrationFixture(t)
		want, err := f.service.RegisterContent(t.Context(), f.content)
		require(t, err)
		raw, err := f.operator.PersistenceEncoding()
		require(t, err)
		op, err := domain.RehydrateSourceRegistration(raw, domain.CatalogPersistenceBindings{Sources: []domain.SourceRegistration{f.operator}, Reviewers: []domain.IssuerPrincipal{f.reviewer}})
		require(t, err)
		review, _ := domain.NewReviewCapability(f.reviewer, op)
		namespace, _ := domain.NewCatalogOrderNamespace(op, "registration/1")
		base, err := NewService(f.owner, op, review, namespace)
		require(t, err)
		capability, err := domain.NewCatalogRegistrationCapability(review, []domain.ReviewCapability{review, f.reviews[1]})
		require(t, err)
		service, err := NewRegistrationService(base, capability)
		require(t, err)
		got, found, err := service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
		require(t, err)
		if !found || !reflect.DeepEqual(got, want) || f.owner.store.writes != 1 {
			t.Fatal("trusted rebind failed to recover the original result", got, found)
		}
	})
}

func TestRegistrationReplay(t *testing.T) {
	t.Run("exact replay after later revision and restart", func(t *testing.T) {
		f := newRegistrationFixture(t)
		content, err := f.service.RegisterContent(t.Context(), f.content)
		require(t, err)
		version, err := f.service.RegisterCatalogVersion(t.Context(), f.version)
		require(t, err)
		if content.AggregateRevision.Uint64() != 1 || version.AggregateRevision.Uint64() != 2 || version.ReviewRevision.Uint64() != 1 || version.OrderRevision.Uint64() != 2 || !version.ProtectedCustom {
			t.Fatal("incorrect initial registration result", content, version)
		}
		// Model a later managed aggregate revision and a reopened wrapper over
		// retained receipts; only the original command result may be returned.
		f.owner.store.aggregates[f.content.Subject.ContentID()], _ = domain.NewCatalogRevision(7)
		service, err := NewRegistrationService(f.service.service, f.service.capability)
		require(t, err)
		before := f.owner.store.clone()
		got, err := service.RegisterCatalogVersion(t.Context(), f.version)
		require(t, err)
		if !reflect.DeepEqual(got, version) {
			t.Fatal("replay reconstructed current state", got)
		}
		got, found, err := service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
		require(t, err)
		if !found || !reflect.DeepEqual(got, content) {
			t.Fatal("readback changed original facts", got, found)
		}
		assertRegistrationUnchanged(t, before, f.owner)
		if f.owner.commits != 2 {
			t.Fatal("exact replay charged a second write")
		}
		// Returned nested slices must not let callers alter retained results.
		got.Fields[0].Field.Alternatives[0].Support[0] = evidence.ID{}
		again, _, err := service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
		require(t, err)
		if !reflect.DeepEqual(again, content) {
			t.Fatal("caller mutated retained replay result")
		}
	})
	for _, change := range []string{"payload", "expected revision", "reviewer", "cross-kind key collision", "different command for occupied identity", "missing selection"} {
		t.Run("changed key/"+change, func(t *testing.T) {
			f := newRegistrationFixture(t)
			_, err := f.service.RegisterContent(t.Context(), f.content)
			require(t, err)
			_, err = f.service.RegisterCatalogVersion(t.Context(), f.version)
			require(t, err)
			before := f.owner.store.clone()
			command := f.version
			service := f.service
			switch change {
			case "payload":
				command.Reason = "changed"
			case "expected revision":
				command.Expected, _ = domain.NewCatalogRevision(2)
			case "reviewer":
				other, _ := domain.NewIssuerPrincipal()
				op, _ := domain.NewReviewCapability(other, f.operator)
				off, _ := domain.NewReviewCapability(other, f.offline)
				capability, err := domain.NewCatalogRegistrationCapability(op, []domain.ReviewCapability{op, off})
				require(t, err)
				base, err := NewService(f.owner, f.operator, op, f.service.service.namespace)
				require(t, err)
				service, err = NewRegistrationService(base, capability)
				require(t, err)
			case "cross-kind key collision":
				command.ID = f.content.ID
			case "different command for occupied identity":
				command.ID, _ = domain.NewCatalogCommandID()
			case "missing selection":
				command.Selections = nil // canonical conflict precedes evidence validation
			}
			got, err := service.RegisterCatalogVersion(t.Context(), command)
			assertRegistrationFailure(t, got, err, ErrCommandConflict)
			assertRegistrationUnchanged(t, before, f.owner)
		})
	}
	for _, state := range []string{"absent managed parent", "stale aggregate"} {
		t.Run(state, func(t *testing.T) {
			f := newRegistrationFixture(t)
			want := ErrSubjectNotFound
			if state == "stale aggregate" {
				_, err := f.service.RegisterContent(t.Context(), f.content)
				require(t, err)
				f.version.Expected, _ = domain.NewCatalogRevision(2)
				want = ErrRevisionConflict
			}
			// Subject/revision rejection must precede semantic support rejection.
			f.version.Selections = nil
			before := f.owner.store.clone()
			got, err := f.service.RegisterCatalogVersion(t.Context(), f.version)
			assertRegistrationFailure(t, got, err, want)
			assertRegistrationUnchanged(t, before, f.owner)
		})
	}
	t.Run("syntax and bounds precede lookup", func(t *testing.T) {
		for _, malformed := range []string{"zero ID", "reason", "duplicate support", "over bound", "wrong expected union"} {
			t.Run(malformed, func(t *testing.T) {
				f := newRegistrationFixture(t)
				command := f.content
				want := ErrInvalidInput
				switch malformed {
				case "zero ID":
					command.ID = domain.CatalogCommandID{}
				case "reason":
					command.Reason = "\nprivate"
				case "duplicate support":
					command.Selections = append(command.Selections, command.Selections[0])
				case "over bound":
					command.Selections = make([]domain.CatalogRegistrationSelection, 257)
					want = ErrLimitExceeded
				case "wrong expected union":
					command.ExpectedKind = domain.CatalogExpectedRevision
				}
				before := f.owner.store.clone()
				got, err := f.service.RegisterContent(t.Context(), command)
				assertRegistrationFailure(t, got, err, want)
				if f.owner.lookups != 0 || f.owner.commits != 0 {
					t.Fatal("invalid input reached repository")
				}
				assertRegistrationUnchanged(t, before, f.owner)
			})
		}
	})
}

func TestRegistrationAtomicRecovery(t *testing.T) {
	for _, stage := range []string{"before start", "after lookup", "after write", "between callback and commit"} {
		t.Run("cancellation/"+stage, func(t *testing.T) {
			f := newRegistrationFixture(t)
			before := f.owner.store.clone()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch stage {
			case "before start":
				cancel()
			case "after lookup":
				f.owner.afterLookup = cancel
			case "after write":
				f.owner.afterWrite = cancel
			case "between callback and commit":
				f.owner.beforeCommit = cancel
			}
			got, err := f.service.RegisterContent(ctx, f.content)
			assertRegistrationFailure(t, got, err, context.Canceled)
			assertRegistrationUnchanged(t, before, f.owner)
			if stage == "before start" && f.owner.calls != 0 || stage == "after lookup" && f.owner.commits != 0 {
				t.Fatal("canceled work continued")
			}
		})
	}
	t.Run("deadline and bounded transaction", func(t *testing.T) {
		f := newRegistrationFixture(t)
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()
		got, err := f.service.RegisterContent(ctx, f.content)
		assertRegistrationFailure(t, got, err, context.DeadlineExceeded)
		ctx, cancel = context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		deadline, _ := ctx.Deadline()
		_, err = f.service.RegisterContent(ctx, f.content)
		require(t, err)
		if f.owner.deadline.After(deadline) {
			t.Fatal("caller deadline extended")
		}
		_, _, err = f.service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
		require(t, err)
		if f.owner.remaining <= 0 || f.owner.remaining > domain.CatalogTransactionTimeout {
			t.Fatal("transaction deadline exceeded")
		}
	})
	for _, violation := range []string{"skip callback", "repeat callback", "nil repository", "missing extension"} {
		t.Run("callback-once/"+violation, func(t *testing.T) {
			f := newRegistrationFixture(t)
			before := f.owner.store.clone()
			want := ErrRepositoryUnavailable
			switch violation {
			case "skip callback":
				f.owner.skip = true
			case "repeat callback":
				f.owner.repeat = true
			case "nil repository":
				f.owner.nilPort = true
			case "missing extension":
				f.owner.noExtension = true
				want = ErrUnsupported
			}
			got, err := f.service.RegisterContent(t.Context(), f.content)
			assertRegistrationFailure(t, got, err, want)
			assertRegistrationUnchanged(t, before, f.owner)
			if f.owner.commits > 1 {
				t.Fatal("callback retry caused another mutation")
			}
		})
	}
	for _, durable := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit attempted with uncertain outcome/durable=%t/canceled=%t", durable, canceled), func(t *testing.T) {
				f := newRegistrationFixture(t)
				before := f.owner.store.clone()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				f.owner.finishError = fmt.Errorf("private SQL/path: %w", ErrIndeterminate)
				f.owner.durable = durable
				if canceled {
					f.owner.afterCommit = cancel
				}
				got, err := f.service.RegisterContent(ctx, f.content)
				assertRegistrationFailure(t, got, err, ErrIndeterminate)
				if f.owner.commits != 1 || f.owner.calls != 1 {
					t.Fatal("uncertainty automatically retried")
				}
				if !durable {
					assertRegistrationUnchanged(t, before, f.owner)
				}
				f.owner.finishError, f.owner.afterCommit = nil, nil
				got, found, err := f.service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
				require(t, err)
				if found != durable || !durable && !reflect.DeepEqual(got, domain.CatalogRegistrationResult{}) {
					t.Fatal("authorized readback lied about durability", got, found)
				}
				// Earlier operation has returned and its scoped port is revoked;
				// only now may an explicit exact retry be issued after absence.
				_, err = f.service.RegisterContent(t.Context(), f.content)
				require(t, err)
				if f.owner.store.writes != 1 {
					t.Fatal("lost response recovery duplicated creation")
				}
			})
		}
	}
	for _, failure := range []string{"rollback failure", "cleanup without commit", "commit-before failure"} {
		t.Run(failure, func(t *testing.T) {
			f := newRegistrationFixture(t)
			before := f.owner.store.clone()
			f.owner.finishError = errors.New("private cleanup failed without attempted commit")
			got, err := f.service.RegisterContent(t.Context(), f.content)
			assertRegistrationFailure(t, got, err, ErrRepositoryUnavailable)
			assertRegistrationUnchanged(t, before, f.owner)
		})
	}
	t.Run("absent/conflicting/unavailable authorized lookup", func(t *testing.T) {
		f := newRegistrationFixture(t)
		got, found, err := f.service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
		require(t, err)
		if found || !reflect.DeepEqual(got, domain.CatalogRegistrationResult{}) || f.owner.commits != 0 {
			t.Fatal("absent lookup fabricated success or wrote")
		}
		_, err = f.service.RegisterContent(t.Context(), f.content)
		require(t, err)
		before := f.owner.store.clone()
		changed := f.content
		changed.Reason = "changed command"
		got, found, err = f.service.ReadRegistration(t.Context(), domain.RegisterContent, changed)
		assertRegistrationFailure(t, got, err, ErrCommandConflict)
		if found {
			t.Fatal("conflicting lookup returned success")
		}
		f.owner.lookupError = errors.New("private unavailable database")
		got, found, err = f.service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
		assertRegistrationFailure(t, got, err, ErrRepositoryUnavailable)
		if found {
			t.Fatal("unavailable lookup returned success")
		}
		assertRegistrationUnchanged(t, before, f.owner)
	})
	t.Run("leaked repository", func(t *testing.T) {
		f := newRegistrationFixture(t)
		_, err := f.service.RegisterContent(t.Context(), f.content)
		require(t, err)
		before := f.owner.store.clone()
		op := f.owner.leaked.used[0]
		got, _, err := f.owner.leaked.LookupRegistration(t.Context(), op)
		assertRegistrationFailure(t, got, err, ErrRepositoryUnavailable)
		got, err = f.owner.leaked.CommitRegistration(t.Context(), op)
		assertRegistrationFailure(t, got, err, ErrRepositoryUnavailable)
		assertRegistrationUnchanged(t, before, f.owner)
	})
}

func TestRegistrationSanitizedErrors(t *testing.T) {
	cases := []struct {
		name        string
		cause, want error
	}{
		{"unknown", errors.New("private SQL schema token raw-content"), ErrRepositoryUnavailable},
		{"unauthorized", domain.ErrCatalogUnauthorized, ErrUnauthorized},
		{"source", domain.ErrCatalogSourceMismatch, ErrSourceMismatch},
		{"subject", domain.ErrCatalogSubjectMismatch, ErrSubjectMismatch},
		{"missing", ErrSubjectNotFound, ErrSubjectNotFound},
		{"revision", domain.ErrCatalogRevisionConflict, ErrRevisionConflict},
		{"overflow", domain.ErrCatalogRevisionOverflow, ErrRevisionOverflow},
		{"command", domain.ErrCatalogCommandConflict, ErrCommandConflict},
		{"evidence", domain.ErrCatalogObservationConflict, ErrEvidenceConflict},
		{"artifact", domain.ErrCatalogArtifactConflict, ErrEvidenceConflict},
		{"limit", domain.LimitError{Resource: domain.RetainedObservations}, ErrLimitExceeded},
		{"unsupported", domain.ErrCatalogUnsupported, ErrUnsupported},
		{"busy", ErrRepositoryContention, ErrRepositoryContention},
		{"indeterminate", ErrIndeterminate, ErrIndeterminate},
	}
	for _, tc := range cases {
		for _, stage := range []string{"lookup", "commit"} {
			t.Run(tc.name+"/"+stage, func(t *testing.T) {
				f := newRegistrationFixture(t)
				before := f.owner.store.clone()
				failure := fmt.Errorf("/private/catalog.sqlite secret SQL: %w", tc.cause)
				if stage == "lookup" {
					f.owner.lookupError = failure
				} else {
					f.owner.commitError = failure
				}
				got, err := f.service.RegisterContent(t.Context(), f.content)
				assertRegistrationFailure(t, got, err, tc.want)
				if stage == "lookup" {
					got, found, err := f.service.ReadRegistration(t.Context(), domain.RegisterContent, f.content)
					assertRegistrationFailure(t, got, err, tc.want)
					if found {
						t.Fatal("failed readback returned a result")
					}
				}
				assertRegistrationUnchanged(t, before, f.owner)
			})
		}
	}
}

func TestRegistrationResponseValidation(t *testing.T) {
	for _, corrupt := range []string{"zero", "command", "subject", "aggregate", "review", "order", "protection", "field"} {
		for _, replay := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/replay=%t", corrupt, replay), func(t *testing.T) {
				f := newRegistrationFixture(t)
				if replay {
					_, err := f.service.RegisterContent(t.Context(), f.content)
					require(t, err)
				}
				before := f.owner.store.clone()
				f.owner.response = func(result *domain.CatalogRegistrationResult) {
					if result.Command.IsZero() {
						return
					}
					switch corrupt {
					case "zero":
						*result = domain.CatalogRegistrationResult{}
					case "command":
						result.Command = f.version.ID
					case "subject":
						result.Subject = f.version.Subject
					case "aggregate":
						result.AggregateRevision, _ = domain.NewCatalogRevision(2)
					case "review":
						result.ReviewRevision, _ = domain.NewCatalogRevision(2)
					case "order":
						result.OrderRevision, _ = domain.NewCatalogRevision(2)
					case "protection":
						result.ProtectedCustom = false
					case "field":
						result.Fields = nil
					}
				}
				got, err := f.service.RegisterContent(t.Context(), f.content)
				assertRegistrationFailure(t, got, err, ErrRepositoryUnavailable)
				assertRegistrationUnchanged(t, before, f.owner)
			})
		}
	}
}
