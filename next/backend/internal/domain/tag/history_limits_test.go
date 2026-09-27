package tag

import (
	"errors"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func TestRetainedHistoryCountLimits(t *testing.T) {
	exactObservations := make([]evidence.Observation, MaxRetainedObservations)
	exactDecisions := make([][]evidence.ID, MaxRetainedDecisions)
	exactDecisionSupport := make([]evidence.ID, MaxDecisionSupportLinks)
	exactCumulativeSupport := [][]evidence.ID{
		exactDecisionSupport,
		exactDecisionSupport,
		exactDecisionSupport,
		exactDecisionSupport,
	}

	accepted := []struct {
		name         string
		observations []evidence.Observation
		support      [][]evidence.ID
	}{
		{"exact observations", exactObservations, nil},
		{"exact decisions", nil, exactDecisions},
		{"exact support per decision", nil, [][]evidence.ID{exactDecisionSupport}},
		{"exact cumulative support with repeated IDs", nil, exactCumulativeSupport},
	}
	for _, test := range accepted {
		t.Run(test.name, func(t *testing.T) {
			if err := CheckRetainedHistoryCounts(test.observations, test.support); err != nil {
				t.Fatalf("exact limit rejected: %v", err)
			}
		})
	}

	overCumulativeSupport := append([][]evidence.ID(nil), exactCumulativeSupport...)
	overCumulativeSupport = append(overCumulativeSupport, make([]evidence.ID, 1))
	rejected := []struct {
		name         string
		observations []evidence.Observation
		support      [][]evidence.ID
	}{
		{"observations one over", make([]evidence.Observation, MaxRetainedObservations+1), nil},
		{"decisions one over", nil, make([][]evidence.ID, MaxRetainedDecisions+1)},
		{"support per decision one over", nil, [][]evidence.ID{make([]evidence.ID, MaxDecisionSupportLinks+1)}},
		{"cumulative support one over", nil, overCumulativeSupport},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			if err := CheckRetainedHistoryCounts(test.observations, test.support); !errors.Is(err, ErrHistoryLimit) {
				t.Fatalf("one-over count error = %v, want %v", err, ErrHistoryLimit)
			}
		})
	}
}

func TestNewObservationCountLimit(t *testing.T) {
	if err := CheckNewObservationCount(make([]evidence.Observation, MaxNewObservationsPerCommand)); err != nil {
		t.Fatalf("64 observations rejected: %v", err)
	}
	if err := CheckNewObservationCount(make([]evidence.Observation, MaxNewObservationsPerCommand+1)); !errors.Is(err, ErrHistoryLimit) {
		t.Fatalf("65 observations error = %v, want %v", err, ErrHistoryLimit)
	}
}

func TestHistoryCountGateDoesNotClaimSemanticValidation(t *testing.T) {
	malformedObservations := []evidence.Observation{{}}
	malformedSupport := [][]evidence.ID{{{}}}
	if err := CheckRetainedHistoryCounts(malformedObservations, malformedSupport); err != nil {
		t.Fatalf("count-only gate scanned malformed members: %v", err)
	}
	if err := CheckNewObservationCount(malformedObservations); err != nil {
		t.Fatalf("command count gate scanned malformed members: %v", err)
	}

	// Top-level count rejection occurs before any per-decision member can matter.
	tooManyDecisions := make([][]evidence.ID, MaxRetainedDecisions+1)
	tooManyDecisions[0] = []evidence.ID{{}}
	if err := CheckRetainedHistoryCounts(malformedObservations, tooManyDecisions); !errors.Is(err, ErrHistoryLimit) {
		t.Fatalf("oversized decision history error = %v, want %v", err, ErrHistoryLimit)
	}
}

func TestCheckedAddWithinIsOverflowSafeOnNativeInt(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	half := maxInt / 2

	if got, ok := checkedAddWithin(maxInt, 0, maxInt); !ok || got != maxInt {
		t.Fatalf("max int boundary = (%d, %t), want (%d, true)", got, ok, maxInt)
	}
	if _, ok := checkedAddWithin(maxInt, 1, maxInt); ok {
		t.Fatal("max int plus one accepted")
	}
	if got, ok := checkedAddWithin(half, maxInt-half, maxInt); !ok || got != maxInt {
		t.Fatalf("split max int boundary = (%d, %t), want (%d, true)", got, ok, maxInt)
	}
	if _, ok := checkedAddWithin(half, maxInt-half+1, maxInt); ok {
		t.Fatal("split max int plus one accepted")
	}
	for _, input := range [][3]int{{-1, 0, maxInt}, {0, -1, maxInt}, {0, 0, -1}} {
		if _, ok := checkedAddWithin(input[0], input[1], input[2]); ok {
			t.Fatalf("negative arithmetic input accepted: %v", input)
		}
	}
}

func TestHistoryCountChecksAllocateNothing(t *testing.T) {
	observations := make([]evidence.Observation, MaxRetainedObservations)
	support := make([]evidence.ID, MaxDecisionSupportLinks)
	decisions := [][]evidence.ID{support, support, support, support}
	overObservations := make([]evidence.Observation, MaxRetainedObservations+1)
	command := observations[:MaxNewObservationsPerCommand]
	overCommand := observations[:MaxNewObservationsPerCommand+1]

	checks := []struct {
		name string
		want error
		call func() error
	}{
		{"accepted history", nil, func() error { return CheckRetainedHistoryCounts(observations, decisions) }},
		{"rejected history", ErrHistoryLimit, func() error { return CheckRetainedHistoryCounts(overObservations, decisions) }},
		{"accepted command", nil, func() error { return CheckNewObservationCount(command) }},
		{"rejected command", ErrHistoryLimit, func() error { return CheckNewObservationCount(overCommand) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			var got error
			allocations := testing.AllocsPerRun(1000, func() {
				got = check.call()
			})
			if !errors.Is(got, check.want) {
				t.Fatalf("error = %v, want %v", got, check.want)
			}
			if allocations != 0 {
				t.Fatalf("allocations = %v, want 0", allocations)
			}
		})
	}
}
