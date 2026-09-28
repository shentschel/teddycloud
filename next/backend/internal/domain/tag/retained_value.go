package tag

import (
	"io"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

// retainedValue owns a validated, canonical copy of every retained slice.
// Keeping both the type and its view private prevents mutable slice access from
// escaping the domain package.
type retainedValue struct {
	view retainedTagView
}

func newRetainedValue(view retainedTagView) (retainedValue, error) {
	if err := validateRetainedSemantics(view); err != nil {
		return retainedValue{}, err
	}
	canonical, err := canonicalEncodingView(view)
	if err != nil {
		return retainedValue{}, err
	}
	return retainedValue{view: canonical}, nil
}

// equal compares canonical semantic fields directly. Constructor admission
// bounds every traversed collection and string, so equality never materializes
// an encoded aggregate.
func (value retainedValue) equal(other retainedValue) bool {
	left, right := value.view, other.view
	if left.id != right.id || left.uid != right.uid || left.revision != right.revision {
		return false
	}
	for key := retainedFactKey(0); key < retainedFactKeyCount; key++ {
		if left.facts[key].state != right.facts[key].state ||
			!equalEvidenceIDs(left.facts[key].active, right.facts[key].active) {
			return false
		}
	}
	if len(left.observations) != len(right.observations) {
		return false
	}
	for index := range left.observations {
		if !equalRetainedObservation(left.observations[index], right.observations[index]) {
			return false
		}
	}
	if len(left.decisions) != len(right.decisions) {
		return false
	}
	for index := range left.decisions {
		if !equalRetainedDecision(left.decisions[index], right.decisions[index]) {
			return false
		}
	}
	return true
}

func (value retainedValue) writeTo(writer io.Writer) (int64, error) {
	return writeTREG1(value.view, writer)
}

func equalEvidenceIDs(left, right []evidence.ID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalRetainedObservation(left, right retainedObservationView) bool {
	if left.introducedRevision != right.introducedRevision {
		return false
	}
	leftObservation, rightObservation := left.observation, right.observation
	leftSource, rightSource := leftObservation.Source(), rightObservation.Source()
	leftClaim, rightClaim := leftObservation.Claim(), rightObservation.Claim()
	return leftObservation.ID() == rightObservation.ID() &&
		leftSource.Name() == rightSource.Name() &&
		leftSource.Revision() == rightSource.Revision() &&
		leftSource.Record() == rightSource.Record() &&
		leftClaim == rightClaim &&
		leftObservation.ObservedAt().Equal(rightObservation.ObservedAt()) &&
		leftObservation.Confidence() == rightObservation.Confidence() &&
		leftObservation.Review() == rightObservation.Review()
}

func equalRetainedDecision(left, right retainedDecisionView) bool {
	return left.id == right.id &&
		left.key == right.key &&
		left.expectedRevision == right.expectedRevision &&
		left.resultRevision == right.resultRevision &&
		equalEvidenceIDs(left.selected, right.selected)
}
