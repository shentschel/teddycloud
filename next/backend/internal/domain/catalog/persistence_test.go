package catalog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func persistenceReview(t *testing.T) (CatalogReviewState, CatalogPersistenceBindings, ReviewCapability, CatalogReviewCommand) {
	t.Helper()
	state, source, ingest, capability := reviewFixture(t)
	a := testObservation(t, source, ingest, idPayloadA, "A")
	b := testObservation(t, source, ingest, "1123456789abcdefghjkmnpqrs", "B")
	state = stageReview(t, state, source, capability, b, a)
	command := reviewCommand(t, state, source, AcceptSupport, a.ID())
	state, _ = applyReview(t, state, capability, command)
	contrary := reviewCommand(t, state, source, AcceptSupport, b.ID())
	state, _ = applyReview(t, state, capability, contrary)
	resolve := reviewCommand(t, state, source, ResolveConflict, b.ID())
	state, _ = applyReview(t, state, capability, resolve)
	bindings := CatalogPersistenceBindings{Sources: []SourceRegistration{source}, Reviewers: []IssuerPrincipal{capability.Reviewer()}}
	return state, bindings, capability, command
}

func TestCatalogReviewPersistenceRoundTrip(t *testing.T) {
	state, bindings, capability, command := persistenceReview(t)
	raw, err := state.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	size, err := state.RetainedEncodingSize()
	if err != nil || uint64(len(raw)) != size {
		t.Fatal("retained encoding size mismatch", err)
	}
	restored, err := RehydrateCatalogReviewState(raw, bindings)
	if err != nil || !reflect.DeepEqual(state, restored) {
		t.Fatal("review state changed", err)
	}
	if !reflect.DeepEqual(state.Field(TitleClaimKey), restored.Field(TitleClaimKey)) {
		t.Fatal("field changed")
	}
	want, found, err := state.LookupCommand(capability, command)
	if err != nil || !found {
		t.Fatal(err)
	}
	got, found, err := restored.LookupCommand(capability, command)
	if err != nil || !found || !reflect.DeepEqual(want, got) {
		t.Fatal("original result lost", err)
	}
	command.Reason += " changed"
	if _, _, err := restored.LookupCommand(capability, command); !errors.Is(err, ErrCatalogCommandConflict) {
		t.Fatal("replay conflict lost", err)
	}
	detached, err := RehydrateCatalogReviewState(raw, CatalogPersistenceBindings{AllowUnbound: true})
	if err != nil {
		t.Fatal("detached review", err)
	}
	again, err := detached.PersistenceEncoding()
	if err != nil || !bytes.Equal(raw, again) || !reflect.DeepEqual(state.Field(TitleClaimKey), detached.Field(TitleClaimKey)) {
		t.Fatal("detached provenance changed", err)
	}
	source := detached.observations[0].input.Coordinate.artifact.source
	if source.Active() || source.ID() != bindings.Sources[0].ID() || source.Revision() != bindings.Sources[0].Revision() {
		t.Fatal("detached source gained authority or changed identity")
	}
	grant, err := NewReviewCapability(capability.Reviewer(), source)
	if err != nil || grant.Authorize(source, TitleClaimKey) == nil {
		t.Fatal("persisted source authorized review")
	}
	if _, err := NewCatalogOrderNamespace(source, "spoof"); err == nil {
		t.Fatal("persisted source authorized order namespace")
	}
	bindings.Sources[0].Revoke()
	restored, err = RehydrateCatalogReviewState(raw, bindings)
	if err != nil || restored.observations[0].input.Coordinate.artifact.source.Active() {
		t.Fatal("revoked history was rejected or reactivated", err)
	}
	if _, _, err := restored.LookupCommand(capability, command); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatal("revoked authority restored", err)
	}
}

func TestCatalogReviewPersistenceRejectsTampering(t *testing.T) {
	state, bindings, _, _ := persistenceReview(t)
	raw, _ := state.PersistenceEncoding()
	for _, mutate := range []func(*CatalogReviewState){
		func(s *CatalogReviewState) { s.revision.value++ },
		func(s *CatalogReviewState) { s.links++ },
		func(s *CatalogReviewState) { s.protected = !s.protected },
		func(s *CatalogReviewState) { s.observations = s.observations[1:] },
		func(s *CatalogReviewState) { s.observations[0], s.observations[1] = s.observations[1], s.observations[0] },
		func(s *CatalogReviewState) { s.decisions[1].canonical += "x" },
		func(s *CatalogReviewState) { s.decisions[1].result.Revision.value++ },
		func(s *CatalogReviewState) { s.decisions[1].result.Field.Status = CatalogFactUnknown },
		func(s *CatalogReviewState) { s.decisions[1].command.Expected.value++ },
		func(s *CatalogReviewState) { s.decisions[1].reviewer = IssuerPrincipal{} },
		func(s *CatalogReviewState) { s.decisions[1].command.ID = s.decisions[0].command.ID },
	} {
		copy := state
		copy.observations = append([]CatalogObservation(nil), state.observations...)
		copy.decisions = append([]catalogDecision(nil), state.decisions...)
		mutate(&copy)
		bad, err := copy.PersistenceEncoding()
		if err != nil {
			continue
		}
		if got, err := RehydrateCatalogReviewState(bad, bindings); err == nil || got.revision.value != 0 {
			t.Fatal("tampered review accepted")
		}
	}
	for cut := range len(raw) {
		if _, err := RehydrateCatalogReviewState(raw[:cut], bindings); err == nil {
			t.Fatalf("truncated review accepted at %d", cut)
		}
	}
	if _, err := RehydrateCatalogReviewState(append(raw, 0), bindings); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	if _, err := RehydrateCatalogReviewState(raw, CatalogPersistenceBindings{}); err == nil {
		t.Fatal("missing trusted bindings accepted")
	}
	oversized := make([]byte, RetainedBytes.Maximum()+1)
	if _, err := RehydrateCatalogReviewState(oversized, bindings); err == nil {
		t.Fatal("one-over byte limit accepted")
	}
	// At-limit input is still parsed and rejected, never silently truncated.
	if _, err := RehydrateCatalogReviewState(oversized[:RetainedBytes.Maximum()], bindings); err == nil {
		t.Fatal("invalid at-limit input accepted")
	}
	bad := append([]byte(nil), raw...)
	header := 8 + len("pi08-review-state/1") + 8 + 30 + 8 + 8 + 8 + 8
	binary.BigEndian.PutUint64(bad[header:header+8], RetainedObservations.Maximum()+1)
	if _, err := RehydrateCatalogReviewState(bad, bindings); err == nil {
		t.Fatal("one-over observation count accepted")
	}
}

func TestCatalogOrderPersistenceRoundTrip(t *testing.T) {
	state, namespace, ingest, capability, versions := orderFixture(t)
	a := sequenceFixture(t, namespace, ingest, versions[0], idPayloadA, 0)
	b := sequenceFixture(t, namespace, ingest, versions[1], "1123456789abcdefghjkmnpqrs", 1)
	command := orderCommandFixture(t, state, namespace, a, b)
	state, original := applyOrder(t, state, capability, command)
	c := sequenceFixture(t, namespace, ingest, versions[0], "2123456789abcdefghjkmnpqrs", 2)
	correction := orderCommandFixture(t, state, namespace, c, b)
	correction.Supersedes = []CatalogCommandID{command.ID}
	state, _ = applyOrder(t, state, capability, correction)
	bindings := CatalogPersistenceBindings{Sources: []SourceRegistration{namespace.Source()}, Namespaces: []CatalogOrderNamespace{namespace}, Reviewers: []IssuerPrincipal{capability.review.Reviewer()}}
	raw, err := state.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RehydrateCatalogOrderState(raw, bindings)
	if err != nil || !reflect.DeepEqual(state, restored) || !reflect.DeepEqual(state.EffectiveOrder(), restored.EffectiveOrder()) || state.Compare(versions[0], versions[1]) != restored.Compare(versions[0], versions[1]) {
		t.Fatal("order state changed", err)
	}
	got, found, err := restored.LookupCommand(capability, command)
	if err != nil || !found || !reflect.DeepEqual(original, got) {
		t.Fatal("superseded original result lost", err)
	}
	command.Reason += " changed"
	if _, _, err := restored.LookupCommand(capability, command); !errors.Is(err, ErrCatalogCommandConflict) {
		t.Fatal("order replay conflict lost", err)
	}
	detached, err := RehydrateCatalogOrderState(raw, CatalogPersistenceBindings{AllowUnbound: true})
	if err != nil {
		t.Fatal("detached order", err)
	}
	again, err := detached.PersistenceEncoding()
	if err != nil || !bytes.Equal(raw, again) || detached.decisions[0].command.Namespace.Source().Active() {
		t.Fatal("detached order changed or gained authority", err)
	}
	review, _ := NewReviewCapability(capability.review.Reviewer(), detached.decisions[0].command.Namespace.Source())
	if _, err := NewCatalogOrderCapability(review, detached.decisions[0].command.Namespace); err == nil {
		t.Fatal("detached order capability granted")
	}
	for _, mutate := range []func(*CatalogOrderState){
		func(s *CatalogOrderState) { s.links++ },
		func(s *CatalogOrderState) { s.revision.value++ },
		func(s *CatalogOrderState) { s.versions = s.versions[:1] },
		func(s *CatalogOrderState) { s.evidence = s.evidence[1:] },
		func(s *CatalogOrderState) { s.decisions[0].canonical += "x" },
		func(s *CatalogOrderState) { s.decisions[0].result.View.Status = CatalogFactUnknown },
		func(s *CatalogOrderState) { s.decisions[1].command.Expected.value++ },
		func(s *CatalogOrderState) { s.decisions[1].command.Supersedes = []CatalogCommandID{correction.ID} },
	} {
		copy := state
		copy.decisions = append([]catalogOrderDecision(nil), state.decisions...)
		mutate(&copy)
		bad, err := copy.PersistenceEncoding()
		if err != nil {
			continue
		}
		if got, err := RehydrateCatalogOrderState(bad, bindings); err == nil || got.revision.value != 0 {
			t.Fatal("tampered order accepted")
		}
	}
	namespace.Source().Revoke()
	if got, err := RehydrateCatalogOrderState(raw, bindings); err != nil || got.decisions[0].command.Namespace.Source().Active() {
		t.Fatal("revoked order history changed", err)
	}
	for cut := range len(raw) {
		if _, err := RehydrateCatalogOrderState(raw[:cut], bindings); err == nil {
			t.Fatalf("truncated order accepted at %d", cut)
		}
	}
}

func TestCatalogPersistenceOpaqueAndBindingAdmission(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("0", 32), strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("a", 33)} {
		if id, err := RehydrateCatalogCommandID(value); err == nil || !id.IsZero() {
			t.Fatal("invalid opaque identity accepted")
		}
	}
	id, err := RehydrateCatalogCommandID(strings.Repeat("a", 32))
	if err != nil || id.String() != strings.Repeat("a", 32) {
		t.Fatal("opaque identity changed", err)
	}
	_, namespace, _, capability, _ := orderFixture(t)
	source := namespace.Source()
	bindings := CatalogPersistenceBindings{Sources: []SourceRegistration{source}, Namespaces: []CatalogOrderNamespace{namespace}, Reviewers: []IssuerPrincipal{capability.review.Reviewer()}}
	sourceRaw, _ := source.PersistenceEncoding()
	if got, err := RehydrateSourceRegistration(sourceRaw, bindings); err != nil || got != source {
		t.Fatal("source binding identity changed", err)
	}
	namespaceRaw, _ := namespace.PersistenceEncoding()
	mixed := CatalogPersistenceBindings{AllowUnbound: true, Sources: []SourceRegistration{source}}
	detachedNamespace, err := RehydrateCatalogOrderNamespace(namespaceRaw, mixed)
	if err != nil {
		t.Fatal("mixed namespace reconstruction", err)
	}
	if _, err := NewCatalogOrderCapability(capability.review, detachedNamespace); err == nil {
		t.Fatal("unbound namespace gained authority from a trusted source")
	}
	if got, err := RehydrateCatalogOrderNamespace(namespaceRaw, bindings); err != nil || got != namespace {
		t.Fatal("namespace identity changed", err)
	}
	spoofed := append([]byte(nil), sourceRaw...)
	spoofed[16] ^= 1
	if _, err := RehydrateSourceRegistration(spoofed, bindings); err == nil {
		t.Fatal("source spoof accepted")
	}
	bindings.Sources = append(bindings.Sources, source)
	if _, err := RehydrateSourceRegistration(sourceRaw, bindings); err == nil {
		t.Fatal("duplicate binding accepted")
	}
	bindings.Sources = make([]SourceRegistration, SourceRegistrations.Maximum()+1)
	if _, err := RehydrateSourceRegistration(sourceRaw, bindings); err == nil {
		t.Fatal("one-over binding limit accepted")
	}
}

func TestCatalogPersistenceResultBounds(t *testing.T) {
	state, bindings, _, command := persistenceReview(t)
	claim, _ := TitleClaim("retained alternative")
	result := CatalogDecisionResult{Command: command.ID, Revision: state.Revision()}
	for _, count := range []uint64{SelectedSupport.Maximum() + 1, RetainedObservations.Maximum(), RetainedObservations.Maximum() + 1} {
		result.Field.Alternatives = make([]CatalogAlternative, count)
		for i := range result.Field.Alternatives {
			result.Field.Alternatives[i].Claim = claim
		}
		e := catalogEncoder{data: make([]byte, 0)}
		e.result(result)
		d, err := newCatalogPersistenceDecoder(e.data, bindings)
		if err != nil {
			t.Fatal(err)
		}
		got := d.decisionResult()
		if count <= RetainedObservations.Maximum() {
			if d.err != nil || len(got.Field.Alternatives) != int(count) || d.offset != len(e.data) {
				t.Fatal("retained alternatives used mutation bound", d.err)
			}
		} else if d.err == nil {
			t.Fatal("one-over retained alternatives accepted")
		}
	}
	_, namespace, _, capability, versions := orderFixture(t)
	orderBindings := CatalogPersistenceBindings{Sources: []SourceRegistration{namespace.Source()}, Namespaces: []CatalogOrderNamespace{namespace}, Reviewers: []IssuerPrincipal{capability.review.Reviewer()}}
	for _, count := range []uint64{SelectedSupport.Maximum() + 1, RetainedSupportLinks.Maximum(), RetainedSupportLinks.Maximum() + 1} {
		entry := CatalogOrderEntry{Version: versions[0].ID(), Support: make([]evidence.ID, count)}
		for i := range entry.Support {
			entry.Support[i] = state.observations[0].ID()
		}
		orderResult := CatalogOrderResult{Command: command.ID, View: CatalogEffectiveOrder{Revision: state.Revision(), Positions: []CatalogEffectivePosition{{Namespace: namespace, Entry: entry}}}}
		e := catalogEncoder{data: make([]byte, 0)}
		e.orderResult(orderResult)
		d, err := newCatalogPersistenceDecoder(e.data, orderBindings)
		if err != nil {
			t.Fatal(err)
		}
		got := d.orderResult()
		if count <= RetainedSupportLinks.Maximum() {
			if d.err != nil || len(got.View.Positions) != 1 || len(got.View.Positions[0].Entry.Support) != int(count) || d.offset != len(e.data) {
				t.Fatal("retained order result used mutation bound", d.err)
			}
		} else if d.err == nil {
			t.Fatal("one-over retained order support accepted")
		}
	}
}
