package tag

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func TestRetainedValueOwnsCanonicalSliceCopies(t *testing.T) {
	view := completeRetainedView(t)
	value, err := newRetainedValue(view)
	if err != nil {
		t.Fatal(err)
	}
	var before bytes.Buffer
	if _, err := value.writeTo(&before); err != nil {
		t.Fatal(err)
	}

	foreignID := mustObservationIDNumber(t, 99)
	view.facts[retainedProtocolValid].active[0] = foreignID
	view.observations[0] = view.observations[1]
	view.decisions[0].selected[0] = foreignID
	view.decisions[0].id = mustDecisionIDNumber(t, 99)

	var after bytes.Buffer
	if _, err := value.writeTo(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("caller slice mutation changed retained encoding")
	}
	fresh, err := newRetainedValue(completeRetainedView(t))
	if err != nil {
		t.Fatal(err)
	}
	if !value.equal(fresh) {
		t.Fatal("caller slice mutation changed retained equality")
	}
}

func TestRetainedValueCanonicalReorderEquality(t *testing.T) {
	ordered := reorderableRetainedView(t)
	permuted := reorderableRetainedView(t)
	reverseObservations(permuted.observations)
	reverseDecisions(permuted.decisions)
	reverseEvidenceIDs(permuted.facts[retainedProtocolValid].active)
	for index := range permuted.decisions {
		reverseEvidenceIDs(permuted.decisions[index].selected)
	}

	left, err := newRetainedValue(ordered)
	if err != nil {
		t.Fatal(err)
	}
	right, err := newRetainedValue(permuted)
	if err != nil {
		t.Fatal(err)
	}
	if !left.equal(right) || !right.equal(left) {
		t.Fatal("canonical reorder compared unequal")
	}
}

func TestRetainedValueEqualityIncludesObservationPayloadAndDecisionSupport(t *testing.T) {
	t.Run("observation payload", func(t *testing.T) {
		leftView := reorderableRetainedView(t)
		rightView := reorderableRetainedView(t)
		original := rightView.observations[0]
		source, err := evidence.NewSource("different source", "r1", "record")
		if err != nil {
			t.Fatal(err)
		}
		observation, err := evidence.NewObservation(
			original.observation.ID(), source, original.observation.Claim(),
			original.observation.ObservedAt(), original.observation.Confidence(), original.observation.Review(),
		)
		if err != nil {
			t.Fatal(err)
		}
		rightView.observations[0].observation = observation
		left := mustRetainedValue(t, leftView)
		right := mustRetainedValue(t, rightView)
		if left.equal(right) {
			t.Fatal("different observation payload compared equal")
		}
	})

	t.Run("decision support", func(t *testing.T) {
		leftView := reorderableRetainedView(t)
		rightView := reorderableRetainedView(t)
		selected := rightView.decisions[len(rightView.decisions)-1].selected[:1]
		rightView.decisions[len(rightView.decisions)-1].selected = selected
		rightView.facts[retainedProtocolValid].active = append([]evidence.ID(nil), selected...)
		left := mustRetainedValue(t, leftView)
		right := mustRetainedValue(t, rightView)
		if left.equal(right) {
			t.Fatal("different decision support compared equal")
		}
	})
}

func TestNewRetainedValueRejectsInvalidReferencesBeforeCanonicalCopy(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*retainedTagView)
	}{
		{
			name: "active reference",
			mutate: func(view *retainedTagView) {
				view.facts[retainedProtocolValid].active[0] = mustObservationIDNumber(t, 99)
			},
		},
		{
			name: "decision reference",
			mutate: func(view *retainedTagView) {
				view.decisions[0].selected[0] = mustObservationIDNumber(t, 99)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := completeRetainedView(t)
			test.mutate(&view)
			if _, err := newRetainedValue(view); !errors.Is(err, ErrInvalidRetainedEncoding) {
				t.Fatalf("newRetainedValue error = %v", err)
			}
		})
	}
}

func TestNewRetainedValueRejectsByteOverBeforeAllocation(t *testing.T) {
	view := boundaryRetainedView(t, 485_586, 0)
	var got error
	allocations := testing.AllocsPerRun(100, func() {
		_, got = newRetainedValue(view)
	})
	if !errors.Is(got, ErrEncodedTagLimit) {
		t.Fatalf("newRetainedValue error = %v", got)
	}
	if allocations >= 0.05 {
		t.Fatalf("byte-over admission allocated %.2f times per call", allocations)
	}
}

func TestNewRetainedValueAcceptsEmptyRegistrationRevision(t *testing.T) {
	view := retainedTagView{
		id:       mustTagIDNumber(t, 7),
		uid:      UIDFromBytes([8]byte{1, 2, 3, 4, 5, 6, 7, 8}),
		revision: InitialRevision,
	}
	value, err := newRetainedValue(view)
	if err != nil {
		t.Fatal(err)
	}
	if !value.equal(mustRetainedValue(t, view)) {
		t.Fatal("equal registration values compared unequal")
	}
	var output bytes.Buffer
	if _, err := value.writeTo(&output); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 {
		t.Fatal("empty registration emitted no bytes")
	}
}

func reorderableRetainedView(t *testing.T) retainedTagView {
	t.Helper()
	view := minimalRetainedView(t)
	source, err := evidence.NewSource("source", "r1", "record")
	if err != nil {
		t.Fatal(err)
	}
	second := mustObservation(
		t, 1, view.id, "protocol_valid", "true", source,
		evidence.Corroborated, evidence.Accepted,
		time.Date(2000, 1, 2, 3, 4, 5, 7, time.UTC),
	)
	view.revision = 4
	view.observations = append(view.observations, retainedObservationView{
		observation: second, introducedRevision: 2,
	})
	ids := []evidence.ID{view.observations[0].observation.ID(), second.ID()}
	view.facts[retainedProtocolValid] = retainedFactView{state: MetadataObservedTrue, active: append([]evidence.ID(nil), ids...)}
	view.decisions = []retainedDecisionView{
		{id: mustDecisionIDNumber(t, 0), key: retainedProtocolValid, expectedRevision: 2, resultRevision: 3, selected: append([]evidence.ID(nil), ids...)},
		{id: mustDecisionIDNumber(t, 1), key: retainedProtocolValid, expectedRevision: 3, resultRevision: 4, selected: append([]evidence.ID(nil), ids...)},
	}
	return view
}

func mustRetainedValue(t *testing.T, view retainedTagView) retainedValue {
	t.Helper()
	value, err := newRetainedValue(view)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func reverseEvidenceIDs(values []evidence.ID) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseObservations(values []retainedObservationView) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseDecisions(values []retainedDecisionView) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
