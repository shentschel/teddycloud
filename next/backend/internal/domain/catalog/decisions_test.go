package catalog

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func reviewFixture(t *testing.T) (CatalogReviewState, SourceRegistration, IngestCapability, ReviewCapability) {
	t.Helper()
	source, ingest := testRegistration(t, CoreOperator, TitleClaimKey, ModelClaimKey, ArticleClaimKey, FingerprintClaimKey, OwnershipClaimKey)
	reviewer, err := NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	capability, err := NewReviewCapability(reviewer, source)
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := ContentSubject(mustContentID(t, "cnt_"+idPayloadA))
	state, err := NewCatalogReviewState(subject, false)
	if err != nil {
		t.Fatal(err)
	}
	return state, source, ingest, capability
}

func reviewCommand(t *testing.T, state CatalogReviewState, source SourceRegistration, kind CatalogDecisionKind, support ...evidence.ID) CatalogReviewCommand {
	t.Helper()
	id, err := NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	key := TitleClaimKey
	if kind == RecordObservations {
		key = 0
	}
	return CatalogReviewCommand{ID: id, Kind: kind, Subject: state.Subject(), Source: source, Expected: state.Revision(), Key: key, Support: support, Reason: "explicit review"}
}

func applyReview(t *testing.T, state CatalogReviewState, capability ReviewCapability, command CatalogReviewCommand) (CatalogReviewState, CatalogDecisionResult) {
	t.Helper()
	next, result, err := state.Apply(capability, command)
	if err != nil {
		t.Fatal(err)
	}
	return next, result
}

func stageReview(t *testing.T, state CatalogReviewState, source SourceRegistration, capability ReviewCapability, observations ...CatalogObservation) CatalogReviewState {
	t.Helper()
	command := reviewCommand(t, state, source, RecordObservations)
	command.Observations = observations
	next, _ := applyReview(t, state, capability, command)
	return next
}

func rejectedReview(t *testing.T, state CatalogReviewState, capability ReviewCapability, command CatalogReviewCommand, want error) {
	t.Helper()
	before, err := state.RetainedEncodingSize()
	if err != nil {
		t.Fatal(err)
	}
	next, result, err := state.Apply(capability, command)
	if !errors.Is(err, want) || !reflect.DeepEqual(next, state) || result.Revision.Uint64() != 0 {
		t.Fatalf("rejection was not atomic: %v, want %v", err, want)
	}
	after, _ := next.RetainedEncodingSize()
	if before != after {
		t.Fatal("rejection changed retained bytes")
	}
}

func TestCatalogAcceptanceTransitions(t *testing.T) {
	state, source, ingest, capability := reviewFixture(t)
	a := testObservation(t, source, ingest, idPayloadA, "A")
	b := testObservation(t, source, ingest, "1123456789abcdefghjkmnpqrs", "B")
	c := testObservation(t, source, ingest, "2123456789abcdefghjkmnpqrs", "A")
	state = stageReview(t, state, source, capability, a, b, c)
	if state.Revision().Uint64() != 2 || state.Field(TitleClaimKey).Status != CatalogFactUnknown {
		t.Fatal("staging implicitly accepted evidence")
	}
	command := reviewCommand(t, state, source, AcceptSupport, a.ID(), c.ID())
	state, result := applyReview(t, state, capability, command)
	if value, ok := result.Field.Value(); !ok || value.Text() != "A" || len(result.Field.Alternatives[0].Support) != 2 {
		t.Fatal("agreeing support was lost")
	}
	state, result = applyReview(t, state, capability, reviewCommand(t, state, source, AcceptSupport, b.ID()))
	if result.Field.Status != CatalogFactConflict || len(result.Field.Alternatives) != 2 {
		t.Fatal("contrary accepted support did not expose conflict")
	}
	if _, ok := result.Field.Value(); ok {
		t.Fatal("conflict returned scalar winner")
	}
	state, result = applyReview(t, state, capability, reviewCommand(t, state, source, ResolveConflict, b.ID()))
	if value, ok := result.Field.Value(); !ok || value.Text() != "B" || len(state.Observations()) != 3 {
		t.Fatal("explicit resolution failed or discarded history")
	}
	state, result = applyReview(t, state, capability, reviewCommand(t, state, source, AcceptSupport, a.ID()))
	if result.Field.Status != CatalogFactConflict {
		t.Fatal("contrary accepted support could not reopen conflict")
	}
	rejectedReview(t, state, capability, reviewCommand(t, state, source, ResolveConflict, a.ID(), b.ID()), ErrCatalogUnsupported)
	stale := reviewCommand(t, state, source, AcceptSupport, c.ID())
	stale.Expected = CatalogRevision{value: 2}
	rejectedReview(t, state, capability, stale, ErrCatalogRevisionConflict)
	// Replayed result remains the originally accepted A after later resolution.
	_, replay := applyReview(t, state, capability, command)
	if !reflect.DeepEqual(replay.Field, CatalogField{Status: CatalogFactAccepted, Alternatives: []CatalogAlternative{{Claim: a.input.Claim, Support: []evidence.ID{a.ID(), c.ID()}}}}) {
		t.Fatal("replay reconstructed current projection instead of original result")
	}
}

func TestCatalogCommandReplay(t *testing.T) {
	state, source, ingest, capability := reviewFixture(t)
	a := testObservation(t, source, ingest, idPayloadA, "A")
	b := testObservation(t, source, ingest, "1123456789abcdefghjkmnpqrs", "B")
	stage := reviewCommand(t, state, source, RecordObservations)
	stage.Observations = []CatalogObservation{b, a, a}
	state, staged := applyReview(t, state, capability, stage)
	stage.Observations = []CatalogObservation{a, b}
	replayed, again := applyReview(t, state, capability, stage)
	if replayed.Revision() != state.Revision() || !reflect.DeepEqual(staged, again) {
		t.Fatal("canonical observation replay advanced revision or changed result")
	}
	command := reviewCommand(t, state, source, AcceptSupport, b.ID(), a.ID(), a.ID())
	state, original := applyReview(t, state, capability, command)
	command.Support = []evidence.ID{a.ID(), b.ID()}
	replayed, again = applyReview(t, state, capability, command)
	if replayed.Revision() != state.Revision() || !reflect.DeepEqual(original, again) {
		t.Fatal("canonical support replay was not exact")
	}
	for _, change := range []func(*CatalogReviewCommand){
		func(c *CatalogReviewCommand) { c.Reason = "different reason" },
		func(c *CatalogReviewCommand) { c.Expected = CatalogRevision{value: 1} },
		func(c *CatalogReviewCommand) { c.Support = []evidence.ID{a.ID()} },
		func(c *CatalogReviewCommand) { c.Kind = ResolveConflict },
	} {
		changed := command
		change(&changed)
		rejectedReview(t, state, capability, changed, ErrCatalogCommandConflict)
	}
	otherReviewer, _ := NewIssuerPrincipal()
	otherCapability, _ := NewReviewCapability(otherReviewer, source)
	rejectedReview(t, state, otherCapability, command, ErrCatalogCommandConflict)
	found, ok, err := state.LookupCommand(capability, command)
	if err != nil || !ok || !reflect.DeepEqual(found, original) {
		t.Fatal("readback failed")
	}
	found.Field.Alternatives[0].Support[0] = evidence.ID{}
	original.Field.Alternatives[0].Support[0] = evidence.ID{}
	fresh, _, _ := state.LookupCommand(capability, command)
	if fresh.Field.Alternatives[0].Support[0].IsZero() {
		t.Fatal("caller mutated retained result")
	}
	unknown := reviewCommand(t, state, source, AcceptSupport, a.ID())
	if _, ok, err := state.LookupCommand(capability, unknown); err != nil || ok {
		t.Fatal("unknown command readback granted a result")
	}
	rejectedReview(t, state, ReviewCapability{}, command, ErrCatalogUnauthorized)
	source.Revoke()
	if source.Active() {
		t.Fatal("revoked registration remained active")
	}
	rejectedReview(t, state, capability, command, ErrCatalogUnauthorized)
	if _, _, err := state.LookupCommand(capability, command); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatal("readback restored revoked authority")
	}
	if _, err := NewCatalogObservation(ingest, source, a.Input()); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatal("revoked ingestion remained authorized")
	}
	if len(state.Observations()) != 2 || state.Field(TitleClaimKey).Status != CatalogFactConflict {
		t.Fatal("revocation altered history")
	}
}

func TestCatalogDecisionBindingsAndConfidence(t *testing.T) {
	state, source, ingest, capability := reviewFixture(t)
	a := testObservation(t, source, ingest, idPayloadA, "A")
	state = stageReview(t, state, source, capability, a)
	changed := a.Input()
	changed.Claim, _ = TitleClaim("changed")
	collision, _ := NewCatalogObservation(ingest, source, changed)
	record := reviewCommand(t, state, source, RecordObservations)
	record.Observations = []CatalogObservation{collision}
	rejectedReview(t, state, capability, record, ErrCatalogObservationConflict)
	for _, confidence := range []evidence.Confidence{evidence.ConfidenceUnknown, evidence.Tentative, evidence.Corroborated} {
		changed := a.Input()
		changed.Confidence = confidence
		changed.ID, _ = evidence.ParseID("obs_2123456789abcdefghjkmnpqrs")
		candidate, err := NewCatalogObservation(ingest, source, changed)
		if err != nil {
			t.Fatal(err)
		}
		pending := stageReview(t, state, source, capability, candidate)
		command := reviewCommand(t, pending, source, AcceptSupport, candidate.ID())
		rejectedReview(t, pending, ReviewCapability{}, command, ErrCatalogUnauthorized)
		if pending.Field(TitleClaimKey).Status != CatalogFactUnknown {
			t.Fatal("confidence accepted evidence")
		}
	}
	other, otherIngest := testRegistration(t, CoreOperator, TitleClaimKey)
	otherCapability, _ := NewReviewCapability(capability.Reviewer(), other)
	b := testObservation(t, other, otherIngest, "1123456789abcdefghjkmnpqrs", "B")
	state = stageReview(t, state, other, otherCapability, b)
	rejectedReview(t, state, capability, reviewCommand(t, state, source, AcceptSupport, b.ID()), ErrCatalogSourceMismatch)
	wrongKey := reviewCommand(t, state, source, AcceptSupport, a.ID())
	wrongKey.Key = ModelClaimKey
	rejectedReview(t, state, capability, wrongKey, ErrCatalogUnsupported)
	wrongSubject := reviewCommand(t, state, source, AcceptSupport, a.ID())
	wrongSubject.Subject, _ = ContentSubject(mustContentID(t, "cnt_3123456789abcdefghjkmnpqrs"))
	rejectedReview(t, state, capability, wrongSubject, ErrCatalogSubjectMismatch)
	foreignInput := a.Input()
	foreignInput.Subject = wrongSubject.Subject
	foreign, _ := NewCatalogObservation(ingest, source, foreignInput)
	record.Observations = []CatalogObservation{foreign}
	rejectedReview(t, state, capability, record, ErrCatalogSubjectMismatch)
	missing, _ := evidence.ParseID("obs_3123456789abcdefghjkmnpqrs")
	rejectedReview(t, state, capability, reviewCommand(t, state, source, AcceptSupport, missing), ErrCatalogUnsupported)
}

func TestCatalogDecisionPermutations(t *testing.T) {
	base, source, ingest, capability := reviewFixture(t)
	a := testObservation(t, source, ingest, idPayloadA, "A")
	b := testObservation(t, source, ingest, "1123456789abcdefghjkmnpqrs", "B")
	c := testObservation(t, source, ingest, "2123456789abcdefghjkmnpqrs", "A")
	var want CatalogField
	for _, permutation := range [][]CatalogObservation{{a, b, c}, {a, c, b}, {b, a, c}, {b, c, a}, {c, a, b}, {c, b, a}} {
		state := stageReview(t, base, source, capability, permutation...)
		for _, observation := range permutation {
			state, _ = applyReview(t, state, capability, reviewCommand(t, state, source, AcceptSupport, observation.ID()))
		}
		field := state.Field(TitleClaimKey)
		if want.Status == CatalogFactUnknown {
			want = field
		}
		if !reflect.DeepEqual(field, want) {
			t.Fatal("arrival order changed conflict alternatives or support")
		}
	}
	// Independent concurrent calculations share immutable state only; the
	// application transaction must later choose one revision-fenced successor.
	state := stageReview(t, base, source, capability, a, b)
	left := reviewCommand(t, state, source, AcceptSupport, a.ID())
	right := reviewCommand(t, state, source, AcceptSupport, b.ID())
	var wg sync.WaitGroup
	for _, command := range []CatalogReviewCommand{left, right} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := state.Apply(capability, command); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	winner, _ := applyReview(t, state, capability, left)
	rejectedReview(t, winner, capability, right, ErrCatalogRevisionConflict)
	if state.Field(TitleClaimKey).Status != CatalogFactUnknown {
		t.Fatal("successor mutated original state")
	}
}

func TestCatalogProtectedCustom(t *testing.T) {
	state, public, ingest, publicReview := reviewFixture(t)
	custom, customIngest := testRegistration(t, OfflineCustom, TitleClaimKey)
	customReview, _ := NewReviewCapability(publicReview.Reviewer(), custom)
	a := testObservation(t, custom, customIngest, idPayloadA, "shared")
	state = stageReview(t, state, custom, customReview, a)
	b := testObservation(t, public, ingest, "1123456789abcdefghjkmnpqrs", "shared")
	state = stageReview(t, state, public, publicReview, b)
	state, _ = applyReview(t, state, publicReview, reviewCommand(t, state, public, ResolveConflict, b.ID()))
	if !state.ProtectedCustom() || state.Subject() != a.Input().Subject || len(state.Observations()) != 2 {
		t.Fatal("public collision removed protection or identity")
	}
	otherSubject, _ := ContentSubject(mustContentID(t, "cnt_3123456789abcdefghjkmnpqrs"))
	other, _ := NewCatalogReviewState(otherSubject, true)
	command := reviewCommand(t, other, public, RecordObservations)
	command.Observations = []CatalogObservation{b}
	rejectedReview(t, other, publicReview, command, ErrCatalogSubjectMismatch)
	if other.Subject() == state.Subject() || !other.ProtectedCustom() {
		t.Fatal("shared metadata merged identities")
	}
}

func TestCatalogDecisionRevisionAndLimits(t *testing.T) {
	for _, value := range []uint64{0, math.MaxInt64 + 1, math.MaxUint64} {
		if _, err := NewCatalogRevision(value); err == nil {
			t.Fatal("invalid revision admitted")
		}
	}
	if _, err := NewCatalogReviewState(CatalogSubject{}, false); err == nil {
		t.Fatal("zero subject admitted")
	}
	state, source, ingest, capability := reviewFixture(t)
	a := testObservation(t, source, ingest, idPayloadA, "A")
	state = stageReview(t, state, source, capability, a)
	atMax := state
	atMax.revision, _ = NewCatalogRevision(math.MaxInt64)
	rejectedReview(t, atMax, capability, reviewCommand(t, atMax, source, AcceptSupport, a.ID()), ErrCatalogRevisionOverflow)
	atBoundary := state
	atBoundary.revision, _ = NewCatalogRevision(math.MaxInt64 - 1)
	advanced, _ := applyReview(t, atBoundary, capability, reviewCommand(t, atBoundary, source, AcceptSupport, a.ID()))
	if advanced.Revision().Uint64() != math.MaxInt64 {
		t.Fatal("last positive revision rejected")
	}
	for _, count := range []int{64, 65} {
		command := reviewCommand(t, state, source, RecordObservations)
		command.Observations = make([]CatalogObservation, count)
		for i := range command.Observations {
			command.Observations[i] = a
		}
		_, _, err := state.Apply(capability, command)
		if count == 64 && err != nil || count == 65 && err == nil {
			t.Fatalf("mutation observation boundary %d: %v", count, err)
		}
	}
	for _, count := range []int{256, 257} {
		command := reviewCommand(t, state, source, AcceptSupport)
		command.Support = make([]evidence.ID, count)
		for i := range command.Support {
			command.Support[i] = a.ID()
		}
		_, _, err := state.Apply(capability, command)
		if count == 256 && err != nil || count == 257 && err == nil {
			t.Fatalf("support boundary %d: %v", count, err)
		}
	}
	for _, count := range []uint64{RetainedSupportLinks.Maximum() - 1, RetainedSupportLinks.Maximum()} {
		candidate := state
		candidate.links = count
		_, _, err := candidate.Apply(capability, reviewCommand(t, candidate, source, AcceptSupport, a.ID()))
		if count == RetainedSupportLinks.Maximum()-1 && err != nil || count == RetainedSupportLinks.Maximum() && err == nil {
			t.Fatal("cumulative support boundary failed")
		}
	}
	for _, length := range []int{4096, 4097} {
		command := reviewCommand(t, state, source, AcceptSupport, a.ID())
		command.Reason = strings.Repeat("é", length/2) + strings.Repeat("x", length%2)
		_, _, err := state.Apply(capability, command)
		if length == 4096 && err != nil || length == 4097 && err == nil {
			t.Fatal("reason byte boundary failed")
		}
	}
	// Same encoder counts the full graph and emits exactly those bytes.
	size, err := state.RetainedEncodingSize()
	encoded := catalogEncoder{data: make([]byte, 0)}
	state.encodeProspective(&encoded, nil, nil)
	if err != nil || encoded.err != nil || size != uint64(len(encoded.data)) || !bytes.Contains(encoded.data, a.Input().Coordinate.Snapshot().Bytes()) {
		t.Fatal("retained encoding omitted or mischarged snapshots")
	}
	for _, resource := range []Resource{RetainedBytes} {
		for _, size := range []uint64{resource.Maximum(), resource.Maximum() + 1} {
			e := catalogEncoder{}
			e.raw(strings.Repeat("x", int(size)))
			if size == resource.Maximum() && e.err != nil || size > resource.Maximum() && e.err == nil {
				t.Fatal("encoded byte boundary failed")
			}
		}
	}
}

func TestCatalogImmutableVersionDecision(t *testing.T) {
	state, source, ingest, capability := reviewFixture(t)
	version, _ := identity.ParseContentVersionID("ver_" + idPayloadA)
	subject, _ := VersionSubject(state.Subject().ContentID(), version)
	state, _ = NewCatalogReviewState(subject, false)
	input := testObservation(t, source, ingest, idPayloadA, "seed").Input()
	input.Subject = subject
	input.Claim, _ = FingerprintClaim(mustFingerprint(t, mustAudioID(t, math.MaxUint64), mustAudioHash(t, strings.Repeat("a", 40))))
	a, err := NewCatalogObservation(ingest, source, input)
	if err != nil {
		t.Fatal(err)
	}
	state = stageReview(t, state, source, capability, a)
	command := reviewCommand(t, state, source, AcceptSupport, a.ID())
	command.Key = FingerprintClaimKey
	state, _ = applyReview(t, state, capability, command)
	command = reviewCommand(t, state, source, ResolveConflict, a.ID())
	command.Key = FingerprintClaimKey
	rejectedReview(t, state, capability, command, ErrCatalogUnsupported)
}

func TestCatalogCommandEncodingGolden(t *testing.T) {
	binding := &sourceBinding{
		id: SourceID{value: [16]byte{1}}, revision: RegistrationRevision{value: [16]byte{2}},
		issuer: IssuerPrincipal{value: [16]byte{3}}, adapter: "a", adapterVersion: "1",
		scope: "s", acquisition: CoreOperator, claims: 1 << TitleClaimKey,
	}
	source := SourceRegistration{binding: binding}
	reviewer := IssuerPrincipal{value: [16]byte{4}}
	subject, _ := ContentSubject(mustContentID(t, "cnt_"+idPayloadA))
	support, _ := evidence.ParseID("obs_" + idPayloadA)
	command := CatalogReviewCommand{ID: CatalogCommandID{value: [16]byte{5}}, Kind: AcceptSupport, Subject: subject, Source: source, Expected: CatalogRevision{value: 7}, Key: TitleClaimKey, Reason: "r", Support: []evidence.ID{support}}
	canonical, err := canonicalReviewCommand(reviewer, command)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "000000000000000e706930382d636f6d6d616e642f31000000000000000e706930382d636174616c6f672f31000000000000002030353030303030303030303030303030303030303030303030303030303030300000000000000002000000000000001e636e745f3031323334353637383961626364656667686a6b6d6e707172730000000000000000000000000000002030313030303030303030303030303030303030303030303030303030303030300000000000000020303230303030303030303030303030303030303030303030303030303030303000000000000000203033303030303030303030303030303030303030303030303030303030303030000000000000000161000000000000000131000000000000000173000000000000000300000000000000010000000000000001000000000000002030343030303030303030303030303030303030303030303030303030303030300000000000000007000000000000000100000000000000017200000000000000000000000000000001000000000000001e6f62735f3031323334353637383961626364656667686a6b6d6e70717273"
	if hex.EncodeToString([]byte(canonical)) != golden {
		t.Fatalf("internal command encoding changed: %s", hex.EncodeToString([]byte(canonical)))
	}
	// Equivalent timestamps are canonical observations; payload times and
	// confidence remain encoded identity, never freshness or acceptance rules.
	state, active, ingest, capability := reviewFixture(t)
	a := testObservation(t, active, ingest, idPayloadA, "A")
	stage := reviewCommand(t, state, active, RecordObservations)
	stage.Observations = []CatalogObservation{a}
	state, _ = applyReview(t, state, capability, stage)
	input := a.Input()
	input.ObservedAt = input.ObservedAt.In(time.FixedZone("equivalent", -7200))
	equivalent, _ := NewCatalogObservation(ingest, active, input)
	stage.Observations = []CatalogObservation{equivalent}
	replayed, _ := applyReview(t, state, capability, stage)
	if replayed.Revision() != state.Revision() {
		t.Fatal("equivalent timestamp changed canonical command")
	}
}
