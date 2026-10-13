package catalog

import (
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func orderFixture(t *testing.T) (CatalogOrderState, CatalogOrderNamespace, IngestCapability, CatalogOrderCapability, []ContentVersion) {
	t.Helper()
	source, ingest := testRegistration(t, CoreOperator, TitleClaimKey)
	reviewer, _ := NewIssuerPrincipal()
	review, _ := NewReviewCapability(reviewer, source)
	namespace, err := NewCatalogOrderNamespace(source, "explicit-sequence/1")
	if err != nil {
		t.Fatal(err)
	}
	capability, err := NewCatalogOrderCapability(review, namespace)
	if err != nil {
		t.Fatal(err)
	}
	content := mustContentID(t, "cnt_"+idPayloadA)
	pair := mustFingerprint(t, mustAudioID(t, math.MaxUint64), mustAudioHash(t, strings.Repeat("a", 40)))
	versions := []ContentVersion{
		mustVersion(t, "ver_"+idPayloadA, content, pair, mustOrder(t, "historical", 99)),
		mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", content, pair, mustOrder(t, "historical", 0)),
	}
	state, err := NewCatalogOrderState(content, versions)
	if err != nil {
		t.Fatal(err)
	}
	return state, namespace, ingest, capability, versions
}

func sequenceFixture(t *testing.T, namespace CatalogOrderNamespace, ingest IngestCapability, version ContentVersion, suffix string, position uint64) CatalogSequenceEvidence {
	t.Helper()
	id, err := evidence.ParseID("obs_" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := NewRecordSnapshot([]byte("{}"))
	coordinate, err := NewRecordCoordinate(testArtifact(t, namespace.Source(), ingest), 0, "record", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := NewCatalogSequenceEvidence(ingest, namespace, CatalogSequenceInput{ID: id, Content: version.ContentID(), Version: version.ID(), Position: position, Coordinate: coordinate})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func orderCommandFixture(t *testing.T, state CatalogOrderState, namespace CatalogOrderNamespace, observations ...CatalogSequenceEvidence) CatalogOrderCommand {
	t.Helper()
	id, err := NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	command := CatalogOrderCommand{ID: id, Content: state.content, Namespace: namespace, Expected: state.Revision(), Evidence: observations, Reason: "explicit sequence review"}
	for _, observation := range observations {
		command.Entries = append(command.Entries, CatalogOrderEntry{Version: observation.input.Version, Position: observation.input.Position, Support: []evidence.ID{observation.input.ID}})
	}
	return command
}

func applyOrder(t *testing.T, state CatalogOrderState, capability CatalogOrderCapability, command CatalogOrderCommand) (CatalogOrderState, CatalogOrderResult) {
	t.Helper()
	next, result, err := state.Apply(capability, command)
	if err != nil {
		t.Fatal(err)
	}
	return next, result
}

func rejectOrder(t *testing.T, state CatalogOrderState, capability CatalogOrderCapability, command CatalogOrderCommand, want error) {
	t.Helper()
	next, result, err := state.Apply(capability, command)
	if !errors.Is(err, want) || !reflect.DeepEqual(next, state) || result.View.Revision.Uint64() != 0 {
		t.Fatalf("non-atomic rejection: %v; want %v", err, want)
	}
}

func TestCatalogOrderDecisions(t *testing.T) {
	state, namespace, ingest, capability, versions := orderFixture(t)
	original := append([]ContentVersion(nil), versions...)
	if state.EffectiveOrder().Status != CatalogFactUnknown || state.Compare(versions[0], versions[1]) != VersionOrderingUnknown {
		t.Fatal("historical positions or AudioIDs granted effective order")
	}
	a := sequenceFixture(t, namespace, ingest, versions[0], idPayloadA, 0)
	b := sequenceFixture(t, namespace, ingest, versions[1], "1123456789abcdefghjkmnpqrs", 1)
	first := orderCommandFixture(t, state, namespace, a, b)
	state, firstResult := applyOrder(t, state, capability, first)
	if firstResult.View.Status != CatalogFactAccepted || state.Compare(versions[0], versions[1]) != VersionOrderingBefore || state.Compare(versions[1], versions[0]) != VersionOrderingAfter {
		t.Fatal("explicit sequence was not applied")
	}
	c := sequenceFixture(t, namespace, ingest, versions[0], "2123456789abcdefghjkmnpqrs", 2)
	correction := orderCommandFixture(t, state, namespace, c, b)
	correction.Supersedes = []CatalogCommandID{first.ID}
	state, result := applyOrder(t, state, capability, correction)
	if result.View.Revision.Uint64() != 3 || state.Compare(versions[0], versions[1]) != VersionOrderingAfter || len(state.History()) != 2 {
		t.Fatal("correction failed or deleted history")
	}
	readback, ok, err := state.LookupCommand(capability, first)
	if err != nil || !ok || !reflect.DeepEqual(readback, firstResult) {
		t.Fatal("superseded result lost")
	}
	rejectOrder(t, state, capability, orderCommandFixture(t, CatalogOrderState{content: state.content, revision: CatalogRevision{value: 2}}, namespace, a), ErrCatalogRevisionConflict)
	reopened := orderCommandFixture(t, state, namespace, a)
	state, result = applyOrder(t, state, capability, reopened)
	if result.View.Status != CatalogFactConflict || state.Compare(versions[0], versions[1]) != VersionOrderingConflict {
		t.Fatal("contrary active order silently overwrote correction")
	}
	if !reflect.DeepEqual(versions, original) || state.versions[0] != original[0] || state.versions[1] != original[1] {
		t.Fatal("order decision mutated immutable version tuple")
	}
	changed, _ := NewContentVersion(versions[0].ID(), versions[0].ContentID(), versions[0].Fingerprint(), UnknownVersionOrderEvidence())
	if state.Compare(changed, versions[0]) != VersionOrderingConflict {
		t.Fatal("changed immutable tuple reported same")
	}
}

func TestCatalogOrderReplay(t *testing.T) {
	state, namespace, ingest, capability, versions := orderFixture(t)
	a := sequenceFixture(t, namespace, ingest, versions[0], idPayloadA, 3)
	b := sequenceFixture(t, namespace, ingest, versions[1], "1123456789abcdefghjkmnpqrs", 8)
	command := orderCommandFixture(t, state, namespace, b, a)
	state, result := applyOrder(t, state, capability, command)
	command.Evidence = []CatalogSequenceEvidence{a, b, a}
	command.Entries = []CatalogOrderEntry{command.Entries[1], command.Entries[0]}
	command.Entries[0].Support = append(command.Entries[0].Support, a.input.ID)
	next, replay := applyOrder(t, state, capability, command)
	if next.Revision() != state.Revision() || !reflect.DeepEqual(result, replay) {
		t.Fatal("canonical replay advanced revision or changed result")
	}
	changed := command
	changed.Reason = "changed"
	rejectOrder(t, state, capability, changed, ErrCatalogCommandConflict)
	changed = command
	changed.Expected = CatalogRevision{value: 99}
	rejectOrder(t, state, capability, changed, ErrCatalogCommandConflict)
	changed = command
	changed.Entries = append([]CatalogOrderEntry(nil), command.Entries...)
	changed.Entries[0].Position++
	rejectOrder(t, state, capability, changed, ErrCatalogCommandConflict)
	replay.View.Positions[0].Entry.Support[0] = evidence.ID{}
	fresh, ok, err := state.LookupCommand(capability, command)
	if err != nil || !ok || fresh.View.Positions[0].Entry.Support[0].IsZero() {
		t.Fatal("result alias escaped")
	}
	unknown := orderCommandFixture(t, state, namespace, a)
	if _, ok, err := state.LookupCommand(capability, unknown); err != nil || ok {
		t.Fatal("unknown readback granted result")
	}
	rejectOrder(t, state, CatalogOrderCapability{}, command, ErrCatalogUnauthorized)
	namespace.Source().Revoke()
	rejectOrder(t, state, capability, command, ErrCatalogUnauthorized)
	if _, _, err := state.LookupCommand(capability, command); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatal("readback restored revoked authority")
	}
	if len(state.History()) != 1 || state.EffectiveOrder().Status != CatalogFactAccepted {
		t.Fatal("revocation deleted accepted history")
	}
}

func TestCatalogOrderBindings(t *testing.T) {
	state, namespace, ingest, capability, versions := orderFixture(t)
	a := sequenceFixture(t, namespace, ingest, versions[0], idPayloadA, 1)
	b := sequenceFixture(t, namespace, ingest, versions[1], "1123456789abcdefghjkmnpqrs", 2)
	state, _ = applyOrder(t, state, capability, orderCommandFixture(t, state, namespace, a, b))
	changedInput := a.Input()
	changedInput.Position++
	changed, _ := NewCatalogSequenceEvidence(ingest, namespace, changedInput)
	rejectOrder(t, state, capability, orderCommandFixture(t, state, namespace, changed), ErrCatalogObservationConflict)
	wrong := orderCommandFixture(t, state, namespace, a)
	wrong.Entries[0].Version = versions[1].ID()
	rejectOrder(t, state, capability, wrong, ErrCatalogUnsupported)
	wrong = orderCommandFixture(t, state, namespace, a)
	wrong.Entries[0].Position++
	rejectOrder(t, state, capability, wrong, ErrCatalogUnsupported)
	wrong = orderCommandFixture(t, state, namespace, a)
	wrong.Content = mustContentID(t, "cnt_2123456789abcdefghjkmnpqrs")
	rejectOrder(t, state, capability, wrong, ErrCatalogSubjectMismatch)
	otherNamespace, _ := NewCatalogOrderNamespace(namespace.Source(), "explicit-sequence/1")
	otherCapability, _ := NewCatalogOrderCapability(capability.review, otherNamespace)
	wrong = orderCommandFixture(t, state, otherNamespace, a)
	rejectOrder(t, state, otherCapability, wrong, ErrCatalogSourceMismatch)
	c := sequenceFixture(t, otherNamespace, ingest, versions[0], "2123456789abcdefghjkmnpqrs", 1)
	otherCommand := orderCommandFixture(t, state, otherNamespace, c)
	multiple, result := applyOrder(t, state, otherCapability, otherCommand)
	if result.View.Status != CatalogFactConflict || multiple.Compare(versions[0], versions[1]) != VersionOrderingConflict {
		t.Fatal("equal namespace labels merged authority")
	}
	otherCommand.ID, _ = NewCatalogCommandID()
	otherCommand.Expected = multiple.Revision()
	otherCommand.Supersedes = []CatalogCommandID{state.History()[0]}
	rejectOrder(t, multiple, otherCapability, otherCommand, ErrCatalogSourceMismatch)
	foreign := a.Input()
	foreign.Content = wrong.Content
	if foreign.Content == state.content {
		foreign.Content = mustContentID(t, "cnt_2123456789abcdefghjkmnpqrs")
	}
	foreignEvidence, _ := NewCatalogSequenceEvidence(ingest, namespace, foreign)
	rejectOrder(t, state, capability, orderCommandFixture(t, state, namespace, foreignEvidence), ErrCatalogSubjectMismatch)
}

func TestCatalogOrderPermutationsAndUnknown(t *testing.T) {
	base, namespace, ingest, capability, versions := orderFixture(t)
	a := sequenceFixture(t, namespace, ingest, versions[0], idPayloadA, 7)
	b := sequenceFixture(t, namespace, ingest, versions[1], "1123456789abcdefghjkmnpqrs", 7)
	var want CatalogEffectiveOrder
	for i, observations := range [][]CatalogSequenceEvidence{{a, b}, {b, a}} {
		state := base
		for _, observation := range observations {
			state, _ = applyOrder(t, state, capability, orderCommandFixture(t, state, namespace, observation))
		}
		view := state.EffectiveOrder()
		if i == 0 {
			want = view
		}
		if !reflect.DeepEqual(view, want) || view.Status != CatalogFactConflict || state.Compare(versions[0], versions[1]) != VersionOrderingConflict {
			t.Fatal("permutation changed tie conflict")
		}
	}
	partial, _ := applyOrder(t, base, capability, orderCommandFixture(t, base, namespace, a))
	if partial.EffectiveOrder().Status != CatalogFactUnknown || partial.Compare(versions[0], versions[1]) != VersionOrderingUnknown {
		t.Fatal("partial order invented missing position")
	}
	if partial.Compare(versions[0], versions[0]) != VersionOrderingSame {
		t.Fatal("identical immutable version was not same")
	}
}

func TestCatalogOrderLimits(t *testing.T) {
	state, namespace, ingest, capability, versions := orderFixture(t)
	a := sequenceFixture(t, namespace, ingest, versions[0], idPayloadA, 0)
	for _, count := range []int{64, 65} {
		command := orderCommandFixture(t, state, namespace, a)
		command.Evidence = make([]CatalogSequenceEvidence, count)
		for i := range command.Evidence {
			command.Evidence[i] = a
		}
		_, _, err := state.Apply(capability, command)
		if count == 64 && err != nil || count == 65 && err == nil {
			t.Fatalf("evidence boundary %d: %v", count, err)
		}
	}
	for _, count := range []int{256, 257} {
		command := orderCommandFixture(t, state, namespace, a)
		command.Entries[0].Support = make([]evidence.ID, count)
		for i := range command.Entries[0].Support {
			command.Entries[0].Support[i] = a.input.ID
		}
		_, _, err := state.Apply(capability, command)
		if count == 256 && err != nil || count == 257 && err == nil {
			t.Fatalf("support boundary %d: %v", count, err)
		}
	}
	for _, count := range []int{1024, 1025} {
		input := make([]ContentVersion, count)
		for i := range input {
			input[i] = versions[0]
		}
		_, err := NewCatalogOrderState(state.content, input)
		if count == 1024 && err != nil || count == 1025 && err == nil {
			t.Fatal("version input bound bypassed by duplicates")
		}
	}
	for _, links := range []uint64{RetainedSupportLinks.Maximum() - 1, RetainedSupportLinks.Maximum()} {
		candidate := state
		candidate.links = links
		_, _, err := candidate.Apply(capability, orderCommandFixture(t, candidate, namespace, a))
		if links == RetainedSupportLinks.Maximum()-1 && err != nil || links == RetainedSupportLinks.Maximum() && err == nil {
			t.Fatal("cumulative support boundary failed")
		}
	}
	atMax := state
	atMax.revision = CatalogRevision{value: math.MaxInt64}
	rejectOrder(t, atMax, capability, orderCommandFixture(t, atMax, namespace, a), ErrCatalogRevisionOverflow)
	atMax.revision = CatalogRevision{value: math.MaxInt64 - 1}
	last, _ := applyOrder(t, atMax, capability, orderCommandFixture(t, atMax, namespace, a))
	if last.Revision().Uint64() != math.MaxInt64 {
		t.Fatal("last positive revision rejected")
	}
	state, _ = applyOrder(t, state, capability, orderCommandFixture(t, state, namespace, a))
	size, err := state.RetainedEncodingSize()
	encoded := catalogEncoder{data: make([]byte, 0)}
	state.encodeOrderState(&encoded, nil, nil)
	if err != nil || encoded.err != nil || size != uint64(len(encoded.data)) {
		t.Fatal("retained byte accounting differs from encoding")
	}
}

func TestCatalogOrderEncodingGolden(t *testing.T) {
	binding := &sourceBinding{
		id: SourceID{value: [16]byte{1}}, revision: RegistrationRevision{value: [16]byte{2}},
		issuer: IssuerPrincipal{value: [16]byte{3}}, adapter: "a", adapterVersion: "1",
		scope: "s", acquisition: CoreOperator, claims: 1 << TitleClaimKey,
	}
	namespace := CatalogOrderNamespace{binding: &catalogOrderNamespace{id: [16]byte{5}, source: SourceRegistration{binding: binding}, contract: "sequence/1"}}
	reviewer := IssuerPrincipal{value: [16]byte{4}}
	version, _ := identity.ParseContentVersionID("ver_" + idPayloadA)
	id, _ := evidence.ParseID("obs_" + idPayloadA)
	command := CatalogOrderCommand{ID: CatalogCommandID{value: [16]byte{6}}, Content: mustContentID(t, "cnt_"+idPayloadA), Namespace: namespace, Expected: CatalogRevision{value: 7}, Reason: "r", Entries: []CatalogOrderEntry{{Version: version, Position: 9, Support: []evidence.ID{id}}}}
	canonical, err := canonicalOrderCommand(reviewer, command)
	const golden = "0000000000000014706930382d6f726465722d636f6d6d616e642f31000000000000000e706930382d636174616c6f672f3100000000000000203036303030303030303030303030303030303030303030303030303030303030000000000000001e636e745f3031323334353637383961626364656667686a6b6d6e7071727305000000000000000000000000000000000000000000002030313030303030303030303030303030303030303030303030303030303030300000000000000020303230303030303030303030303030303030303030303030303030303030303000000000000000203033303030303030303030303030303030303030303030303030303030303030000000000000000161000000000000000131000000000000000173000000000000000300000000000000010000000000000001000000000000000a73657175656e63652f310000000000000020303430303030303030303030303030303030303030303030303030303030303000000000000000070000000000000001720000000000000001000000000000001e7665725f3031323334353637383961626364656667686a6b6d6e7071727300000000000000090000000000000001000000000000001e6f62735f3031323334353637383961626364656667686a6b6d6e7071727300000000000000000000000000000000"
	if err != nil || hex.EncodeToString([]byte(canonical)) != golden {
		t.Fatalf("order command encoding changed: %v %x", err, canonical)
	}
}
