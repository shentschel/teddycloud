package tag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func TestTagEncodingCompleteRepresentation(t *testing.T) {
	view := completeRetainedView(t)
	wantLength, err := encodedLen(view)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	written, err := writeTREG1(view, &output)
	if err != nil {
		t.Fatal(err)
	}
	if written != wantLength || written != int64(output.Len()) {
		t.Fatalf("count/writer/buffer = %d/%d/%d", wantLength, written, output.Len())
	}

	id := view.id.String()
	obs0, obs1, obs2, obs3, obs4 := observationTexts(view)
	dec0 := view.decisions[0].id.String()
	want := `["TREG",1,"` + id + `","11:22:33:44:55:66:77:88",5,` +
		`[[1,["` + obs0 + `"]],[2,["` + obs1 + `"]],[3,["` + obs2 + `","` + obs3 + `"]],[0,[]]],` +
		`[["` + obs0 + `",2,"` + id + `",0,1,"source","r1","record","2000-01-02T03:04:05.000000006Z",2,1],` +
		`["` + obs1 + `",3,"` + id + `",1,0,"source","r1","record","2000-01-02T03:04:05.000000006Z",2,1],` +
		`["` + obs2 + `",4,"` + id + `",2,1,"source","r1","record","2000-01-02T03:04:05.000000006Z",2,1],` +
		`["` + obs3 + `",4,"` + id + `",2,0,"source","r1","record","2000-01-02T03:04:05.000000006Z",2,1],` +
		`["` + obs4 + `",2,"` + id + `",0,0,"source","r1","record","2000-01-02T03:04:05.000000006Z",2,0]],` +
		`[["` + dec0 + `",0,4,5,["` + obs0 + `"]]]]`
	if output.String() != want {
		t.Fatalf("encoding mismatch\n got: %s\nwant: %s", output.String(), want)
	}
}

func TestTagEncodingSourceEscapes(t *testing.T) {
	view := minimalRetainedView(t)
	source, err := evidence.NewSource(`a/"\<>&`, "x\u2028y\u2029z", strings.Repeat("<", 128))
	if err != nil {
		t.Fatal(err)
	}
	view.observations[0].observation = mustObservation(t, 0, view.id, "protocol_valid", "true", source, 0, evidence.Accepted, time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC))
	var output bytes.Buffer
	if _, err := writeTREG1(view, &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`a/\"\\\u003c\u003e\u0026`, `x\u2028y\u2029z`, `"` + strings.Repeat(`\u003c`, 128) + `"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing escaped field %q", expected)
		}
	}
	if _, err := evidence.NewSource(strings.Repeat("é", 64)+"a", "r", "x"); err == nil {
		t.Fatal("129-byte UTF-8 source accepted")
	}
	if _, err := evidence.NewSource("bad\x7f", "r", "x"); err == nil {
		t.Fatal("control source accepted")
	}
}

func TestTagEncodingBorrowedPayloadForcesFreshPreflight(t *testing.T) {
	view := minimalRetainedView(t)
	before, err := encodedLen(view)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := evidence.NewSource(strings.Repeat("<", 128), "r1", "record")
	view.observations[0].observation = mustObservation(t, 0, view.id, "protocol_valid", "true", source, 0, evidence.Accepted, time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC))
	after, err := encodedLen(view)
	if err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Fatalf("changed borrowed payload reused stale bytes: %d <= %d", after, before)
	}
}

func TestTagEncodingPreflightAllocationCheckpoint(t *testing.T) {
	accepted := completeRetainedView(t)
	rejected := accepted
	rejected.facts[retainedProtocolValid].state = MetadataState(4)

	tests := []struct {
		name    string
		view    retainedTagView
		wantErr error
	}{
		{name: "accepted", view: accepted},
		{name: "rejected", view: rejected, wantErr: ErrInvalidRetainedEncoding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotErr error
			allocations := testing.AllocsPerRun(1000, func() {
				_, gotErr = encodedLen(test.view)
			})
			if !errors.Is(gotErr, test.wantErr) {
				t.Fatalf("byte preflight error = %v, want %v", gotErr, test.wantErr)
			}
			if allocations != 0 {
				t.Fatalf("byte preflight allocations = %v, want zero", allocations)
			}
		})
	}
}

func TestTagEncodingTimestampRevisionAndClosedMappings(t *testing.T) {
	for _, year := range []int{0, 9999} {
		view := minimalRetainedView(t)
		view.observations[0].observation = observationAt(t, view.id, time.Date(year, 1, 2, 3, 4, 5, 6, time.FixedZone("offset", 3600)))
		if _, err := encodedLen(view); err != nil {
			t.Fatalf("year %d rejected: %v", year, err)
		}
	}
	for _, year := range []int{-1, 10000} {
		view := minimalRetainedView(t)
		view.observations[0].observation = observationAt(t, view.id, time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC))
		if _, err := encodedLen(view); !errors.Is(err, ErrInvalidRetainedEncoding) {
			t.Fatalf("year %d error = %v", year, err)
		}
	}
	view := minimalRetainedView(t)
	view.revision = Revision(^uint64(0) >> 1)
	view.observations[0].introducedRevision = 2
	var output bytes.Buffer
	if _, err := writeTREG1(view, &output); err != nil || !strings.Contains(output.String(), "9223372036854775807") {
		t.Fatalf("max revision encoding: %v", err)
	}
	view.facts[0].state = MetadataState(4)
	if _, err := encodedLen(view); !errors.Is(err, ErrInvalidRetainedEncoding) {
		t.Fatalf("open state mapping error = %v", err)
	}
}

func TestTREG1EnumMappings(t *testing.T) {
	metadataStates := []struct {
		value MetadataState
		want  byte
		ok    bool
	}{
		{value: MetadataUnknown, want: 0, ok: true},
		{value: MetadataObservedTrue, want: 1, ok: true},
		{value: MetadataObservedFalse, want: 2, ok: true},
		{value: MetadataConflict, want: 3, ok: true},
		{value: MetadataState(255)},
	}
	for _, test := range metadataStates {
		got, ok := treg1MetadataState(test.value)
		if got != test.want || ok != test.ok {
			t.Errorf("metadata state %d mapping = (%d, %t), want (%d, %t)", test.value, got, ok, test.want, test.ok)
		}
	}

	confidences := []struct {
		value evidence.Confidence
		want  byte
		ok    bool
	}{
		{value: evidence.ConfidenceUnknown, want: 0, ok: true},
		{value: evidence.Tentative, want: 1, ok: true},
		{value: evidence.Corroborated, want: 2, ok: true},
		{value: evidence.Confidence(255)},
	}
	for _, test := range confidences {
		got, ok := treg1Confidence(test.value)
		if got != test.want || ok != test.ok {
			t.Errorf("confidence %d mapping = (%d, %t), want (%d, %t)", test.value, got, ok, test.want, test.ok)
		}
	}

	reviews := []struct {
		value evidence.Review
		want  byte
		ok    bool
	}{
		{value: evidence.Pending, want: 0, ok: true},
		{value: evidence.Accepted, want: 1, ok: true},
		{value: evidence.Disputed, want: 2, ok: true},
		{value: evidence.Rejected, want: 3, ok: true},
		{value: evidence.Review(255)},
	}
	for _, test := range reviews {
		got, ok := treg1Review(test.value)
		if got != test.want || ok != test.ok {
			t.Errorf("review %d mapping = (%d, %t), want (%d, %t)", test.value, got, ok, test.want, test.ok)
		}
	}
}

func TestTagEncodingWriterErrorsAndInvalidPreflight(t *testing.T) {
	view := minimalRetainedView(t)
	view.id = identity.TagID{}
	var invalid bytes.Buffer
	if _, err := writeTREG1(view, &invalid); !errors.Is(err, ErrInvalidRetainedEncoding) || invalid.Len() != 0 {
		t.Fatalf("invalid write = (%d, %v)", invalid.Len(), err)
	}

	view = minimalRetainedView(t)
	if _, err := writeTREG1(view, shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v", err)
	}
	want := errors.New("writer failed")
	if _, err := writeTREG1(view, failingWriter{err: want}); !errors.Is(err, want) {
		t.Fatalf("writer error = %v", err)
	}
}

func TestTagEncodingExactBoundary(t *testing.T) {
	maximal := boundaryRetainedView(t, 0, 0)
	sink := countSink{limit: 12_000_000}
	if err := traverseTREG1(maximal, &sink); err != nil {
		t.Fatal(err)
	}
	if sink.written != 10_816_539 {
		t.Fatalf("maximal bytes = %d, want 10816539", sink.written)
	}

	exact := boundaryRetainedView(t, 485_585, 1)
	length, err := encodedLen(exact)
	if err != nil || length != MaxEncodedTagBytes {
		t.Fatalf("exact boundary = (%d, %v)", length, err)
	}
	written, err := writeTREG1(exact, io.Discard)
	if err != nil || written != MaxEncodedTagBytes {
		t.Fatalf("exact writer = (%d, %v)", written, err)
	}

	oneOver := boundaryRetainedView(t, 485_586, 0)
	sink = countSink{limit: MaxEncodedTagBytes + 1}
	if err := traverseTREG1(oneOver, &sink); err != nil || sink.written != MaxEncodedTagBytes+1 {
		t.Fatalf("one-over fixture = (%d, %v)", sink.written, err)
	}
	var output bytes.Buffer
	if _, err := writeTREG1(oneOver, &output); !errors.Is(err, ErrEncodedTagLimit) || output.Len() != 0 {
		t.Fatalf("one-over write = (%d, %v)", output.Len(), err)
	}
}

func minimalRetainedView(t *testing.T) retainedTagView {
	t.Helper()
	id := mustTagIDNumber(t, 0)
	source, _ := evidence.NewSource("source", "r1", "record")
	observation := mustObservation(t, 0, id, "protocol_valid", "true", source, evidence.Corroborated, evidence.Accepted, time.Date(2000, 1, 2, 3, 4, 5, 6, time.UTC))
	view := retainedTagView{id: id, uid: UIDFromBytes([8]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}), revision: 2}
	view.observations = []retainedObservationView{{observation: observation, introducedRevision: 2}}
	view.facts[retainedProtocolValid] = retainedFactView{state: MetadataObservedTrue, active: []evidence.ID{observation.ID()}}
	return view
}

func completeRetainedView(t *testing.T) retainedTagView {
	t.Helper()
	view := minimalRetainedView(t)
	view.revision = 5
	source, _ := evidence.NewSource("source", "r1", "record")
	claims := []struct {
		key, value string
		review     evidence.Review
		introduced Revision
	}{
		{"protocol_valid", "true", evidence.Accepted, 2},
		{"claimed", "false", evidence.Accepted, 3},
		{"cloud_auth", "true", evidence.Accepted, 4},
		{"cloud_auth", "false", evidence.Accepted, 4},
		{"protocol_valid", "false", evidence.Pending, 2},
	}
	view.observations = nil
	for index, claim := range claims {
		observation := mustObservation(t, index, view.id, claim.key, claim.value, source, evidence.Corroborated, claim.review, time.Date(2000, 1, 2, 3, 4, 5, 6, time.UTC))
		view.observations = append(view.observations, retainedObservationView{observation: observation, introducedRevision: claim.introduced})
	}
	view.facts[retainedProtocolValid] = retainedFactView{state: MetadataObservedTrue, active: []evidence.ID{view.observations[0].observation.ID()}}
	view.facts[retainedClaimed] = retainedFactView{state: MetadataObservedFalse, active: []evidence.ID{view.observations[1].observation.ID()}}
	view.facts[retainedCloudAuth] = retainedFactView{state: MetadataConflict, active: []evidence.ID{view.observations[3].observation.ID(), view.observations[2].observation.ID()}}
	view.decisions = []retainedDecisionView{{id: mustDecisionIDNumber(t, 0), key: retainedProtocolValid, expectedRevision: 4, resultRevision: 5, selected: []evidence.ID{view.observations[0].observation.ID()}}}
	return view
}

func boundaryRetainedView(t *testing.T, replacements, removals int) retainedTagView {
	t.Helper()
	view := retainedTagView{id: mustTagIDNumber(t, 0), uid: UIDFromBytes([8]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}), revision: 8193}
	ids := make([]evidence.ID, 4096)
	view.observations = make([]retainedObservationView, 4096)
	field := func() string {
		length := 128
		if removals > 0 {
			length--
			removals--
		}
		replaced := replacements
		if replaced > length {
			replaced = length
		}
		replacements -= replaced
		return strings.Repeat("a", replaced) + strings.Repeat("<", length-replaced)
	}
	when := time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC)
	for index := range view.observations {
		ids[index] = mustObservationIDNumber(t, index)
		source, err := evidence.NewSource(field(), field(), field())
		if err != nil {
			t.Fatal(err)
		}
		observation, err := evidence.NewObservation(ids[index], source, evidence.Claim{Subject: view.id.String(), Key: "protocol_valid", Value: "true"}, when, evidence.Corroborated, evidence.Accepted)
		if err != nil {
			t.Fatal(err)
		}
		view.observations[index] = retainedObservationView{observation: observation, introducedRevision: Revision(index + 2)}
	}
	if replacements != 0 || removals != 0 {
		t.Fatalf("boundary adjustment did not fit: replacements=%d removals=%d", replacements, removals)
	}
	view.facts[retainedProtocolValid] = retainedFactView{state: MetadataObservedTrue, active: ids}
	view.decisions = make([]retainedDecisionView, 4096)
	for index := range view.decisions {
		var selected []evidence.ID
		switch {
		case index == 0:
			selected = ids[:6]
		case index == 4095:
			selected = ids
		default:
			start := index % (len(ids) - 2)
			selected = ids[start : start+3]
		}
		result := Revision(4098 + index)
		view.decisions[index] = retainedDecisionView{id: mustDecisionIDNumber(t, index), key: retainedProtocolValid, expectedRevision: result - 1, resultRevision: result, selected: selected}
	}
	return view
}

func mustObservation(t *testing.T, number int, subject identity.TagID, key, value string, source evidence.Source, confidence evidence.Confidence, review evidence.Review, observedAt time.Time) evidence.Observation {
	t.Helper()
	observation, err := evidence.NewObservation(mustObservationIDNumber(t, number), source, evidence.Claim{Subject: subject.String(), Key: key, Value: value}, observedAt, confidence, review)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func observationAt(t *testing.T, subject identity.TagID, observedAt time.Time) evidence.Observation {
	t.Helper()
	source, _ := evidence.NewSource("source", "r1", "record")
	return mustObservation(t, 0, subject, "protocol_valid", "true", source, evidence.Corroborated, evidence.Accepted, observedAt)
}

func mustTagIDNumber(t *testing.T, number int) identity.TagID {
	t.Helper()
	id, err := identity.ParseTagID("tag_" + fmt.Sprintf("%026d", number))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustObservationIDNumber(t *testing.T, number int) evidence.ID {
	t.Helper()
	id, err := evidence.ParseID("obs_" + fmt.Sprintf("%026d", number))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustDecisionIDNumber(t *testing.T, number int) DecisionID {
	t.Helper()
	id, err := ParseDecisionID("dec_" + fmt.Sprintf("%026d", number))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func observationTexts(view retainedTagView) (string, string, string, string, string) {
	return view.observations[0].observation.ID().String(), view.observations[1].observation.ID().String(), view.observations[2].observation.ID().String(), view.observations[3].observation.ID().String(), view.observations[4].observation.ID().String()
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	return len(value) - 1, nil
}

type failingWriter struct{ err error }

func (writer failingWriter) Write([]byte) (int, error) { return 0, writer.err }
