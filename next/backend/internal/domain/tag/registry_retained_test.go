package tag

import (
	"errors"
	"testing"
)

func TestTagEqualUsesCanonicalRetainedHistory(t *testing.T) {
	ordered := reorderableRetainedView(t)
	permuted := reorderableRetainedView(t)
	reverseObservations(permuted.observations)
	reverseDecisions(permuted.decisions)
	reverseEvidenceIDs(permuted.facts[retainedProtocolValid].active)
	for index := range permuted.decisions {
		reverseEvidenceIDs(permuted.decisions[index].selected)
	}

	left := mustTagFromRetainedView(t, ordered)
	right := mustTagFromRetainedView(t, permuted)
	if !left.Equal(right) || !right.Equal(left) {
		t.Fatal("canonical retained histories compared unequal")
	}

	differentHistory := reorderableRetainedView(t)
	differentHistory.decisions[0].id = mustDecisionIDNumber(t, 99)
	different := mustTagFromRetainedView(t, differentHistory)
	if left.Equal(different) || different.Equal(left) {
		t.Fatal("different retained histories compared equal")
	}
}

func TestTagRetainedConstructorOwnsCallerSlices(t *testing.T) {
	view := completeRetainedView(t)
	registered := mustTagFromRetainedView(t, view)
	want := mustTagFromRetainedView(t, completeRetainedView(t))

	foreignID := mustObservationIDNumber(t, 99)
	view.facts[retainedProtocolValid].active[0] = foreignID
	view.observations[0] = view.observations[1]
	view.decisions[0].selected[0] = foreignID
	view.decisions[0].id = mustDecisionIDNumber(t, 99)

	if !registered.Equal(want) {
		t.Fatal("caller slice mutation changed Tag")
	}
	if registered.ID() != want.ID() || registered.UID() != want.UID() ||
		registered.Revision() != want.Revision() {
		t.Fatal("caller slice mutation changed public Tag fields")
	}
	if registered.Metadata() != want.Metadata() {
		t.Fatal("caller slice mutation changed projected metadata")
	}
}

func TestTagRetainedConstructorValidatesBeforeCopy(t *testing.T) {
	view := completeRetainedView(t)
	view.facts[retainedProtocolValid].active[0] = mustObservationIDNumber(t, 99)
	if _, err := newTagFromRetainedView(view); !errors.Is(err, ErrInvalidRetainedEncoding) {
		t.Fatalf("newTagFromRetainedView error = %v, want invalid retained encoding", err)
	}
}

func mustTagFromRetainedView(t *testing.T, view retainedTagView) Tag {
	t.Helper()
	registered, err := newTagFromRetainedView(view)
	if err != nil {
		t.Fatal(err)
	}
	return registered
}
