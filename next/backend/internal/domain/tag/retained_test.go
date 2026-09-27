package tag

import (
	"errors"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func TestRetainedViewCountsUseActualSlices(t *testing.T) {
	view := minimalRetainedView(t)
	view.observations = make([]retainedObservationView, MaxRetainedObservations)
	view.decisions = make([]retainedDecisionView, 4)
	for index := range view.decisions {
		view.decisions[index].selected = make([]evidence.ID, MaxDecisionSupportLinks)
	}
	if err := checkRetainedViewCounts(view); err != nil {
		t.Fatalf("exact retained counts rejected: %v", err)
	}

	view.decisions = append(view.decisions, retainedDecisionView{selected: []evidence.ID{{}}})
	if err := checkRetainedViewCounts(view); !errors.Is(err, ErrHistoryLimit) {
		t.Fatalf("16385 retained links error = %v, want %v", err, ErrHistoryLimit)
	}
}

func TestRetainedViewCountRejectsTopLevelBeforeMembers(t *testing.T) {
	view := retainedTagView{
		observations: make([]retainedObservationView, MaxRetainedObservations+1),
		decisions: []retainedDecisionView{{
			selected: make([]evidence.ID, MaxDecisionSupportLinks+1),
		}},
	}
	if err := checkRetainedViewCounts(view); !errors.Is(err, ErrHistoryLimit) {
		t.Fatalf("top-level overflow error = %v", err)
	}
}

func TestRetainedViewActiveCountsAreIndependentAndBounded(t *testing.T) {
	view := retainedTagView{observations: make([]retainedObservationView, MaxRetainedObservations)}
	view.facts[retainedProtocolValid].active = make([]evidence.ID, MaxRetainedObservations)
	if err := checkRetainedViewCounts(view); err != nil {
		t.Fatalf("exact active links rejected: %v", err)
	}
	view.facts[retainedClaimed].active = []evidence.ID{{}}
	if err := checkRetainedViewCounts(view); !errors.Is(err, ErrHistoryLimit) {
		t.Fatalf("active total one-over error = %v", err)
	}
}

func TestCheckedAddInt64Boundaries(t *testing.T) {
	const max = int64(^uint64(0) >> 1)
	if got, ok := checkedAddInt64(max, 0, max); !ok || got != max {
		t.Fatalf("max boundary = (%d, %t)", got, ok)
	}
	for _, input := range [][3]int64{
		{max, 1, max},
		{-1, 0, max},
		{0, -1, max},
		{0, 0, -1},
	} {
		if _, ok := checkedAddInt64(input[0], input[1], input[2]); ok {
			t.Fatalf("invalid arithmetic accepted: %v", input)
		}
	}
}

func TestRetainedViewCountGateAllocatesNothing(t *testing.T) {
	view := retainedTagView{
		observations: make([]retainedObservationView, MaxRetainedObservations),
		decisions:    make([]retainedDecisionView, 4),
	}
	for index := range view.decisions {
		view.decisions[index].selected = make([]evidence.ID, MaxDecisionSupportLinks)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		if err := checkRetainedViewCounts(view); err != nil {
			t.Fatal(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("count allocations = %v, want 0", allocations)
	}
}

func TestRetainedViewByteValidityDoesNotClaimSemanticValidity(t *testing.T) {
	view := minimalRetainedView(t)
	// This support is syntactically encodable but does not belong to retained
	// history. Cross-reference and projection validation remain future A1/B1.
	view.decisions = []retainedDecisionView{{
		id:               mustDecisionIDNumber(t, 0),
		key:              retainedProtocolValid,
		expectedRevision: 1,
		resultRevision:   2,
		selected:         []evidence.ID{mustObservationIDNumber(t, 99)},
	}}
	view.revision = 2
	if _, err := encodedLen(view); err != nil {
		t.Fatalf("byte-valid view was treated as semantically validated: %v", err)
	}
}
