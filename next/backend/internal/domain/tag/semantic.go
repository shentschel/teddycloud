package tag

import "github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"

type semanticObservation struct {
	observation evidence.Observation
	key         retainedFactKey
	value       byte
	introduced  Revision
}

// validateRetainedSemantics validates cross-references and projections only
// after the allocation-free count, field and byte admission has succeeded.
func validateRetainedSemantics(view retainedTagView) error {
	if _, err := encodedLen(view); err != nil {
		return err
	}

	observations := make(map[evidence.ID]semanticObservation, len(view.observations))
	revisionKeys := make(map[Revision]retainedFactKey, len(view.observations)+len(view.decisions))
	for _, retained := range view.observations {
		observation := retained.observation
		if _, exists := observations[observation.ID()]; exists {
			return ErrInvalidRetainedEncoding
		}
		claim := observation.Claim()
		key, keyOK := retainedKeyForClaim(claim.Key)
		value, valueOK := retainedBoolForClaim(claim.Value)
		if !keyOK || !valueOK || claim.Subject != view.id.String() ||
			!recordRevisionKey(revisionKeys, retained.introducedRevision, key) {
			return ErrInvalidRetainedEncoding
		}
		observations[observation.ID()] = semanticObservation{
			observation: observation,
			key:         key,
			value:       value,
			introduced:  retained.introducedRevision,
		}
	}

	decisionIDs := make(map[DecisionID]struct{}, len(view.decisions))
	decisionRevisions := make(map[Revision]struct{}, len(view.decisions))
	latestDecision := [retainedFactKeyCount]int{}
	hasLatestDecision := [retainedFactKeyCount]bool{}
	for index := range view.decisions {
		decision := &view.decisions[index]
		if _, exists := decisionIDs[decision.id]; exists {
			return ErrInvalidRetainedEncoding
		}
		decisionIDs[decision.id] = struct{}{}
		if _, exists := decisionRevisions[decision.resultRevision]; exists {
			return ErrInvalidRetainedEncoding
		}
		decisionRevisions[decision.resultRevision] = struct{}{}
		if !recordRevisionKey(revisionKeys, decision.resultRevision, decision.key) ||
			len(decision.selected) == 0 {
			return ErrInvalidRetainedEncoding
		}

		selectedIDs := make(map[evidence.ID]struct{}, len(decision.selected))
		selected := make([]evidence.Observation, 0, len(decision.selected))
		var claim evidence.Claim
		for selectedIndex, id := range decision.selected {
			if _, exists := selectedIDs[id]; exists {
				return ErrInvalidRetainedEncoding
			}
			selectedIDs[id] = struct{}{}
			indexed, exists := observations[id]
			if !exists || indexed.key != decision.key ||
				indexed.observation.Review() != evidence.Accepted ||
				indexed.introduced > decision.resultRevision {
				return ErrInvalidRetainedEncoding
			}
			if selectedIndex == 0 {
				claim = indexed.observation.Claim()
			}
			selected = append(selected, indexed.observation)
		}
		if _, err := evidence.Accept(claim, selected); err != nil {
			return ErrInvalidRetainedEncoding
		}

		key := decision.key
		if !hasLatestDecision[key] ||
			decision.resultRevision > view.decisions[latestDecision[key]].resultRevision {
			latestDecision[key] = index
			hasLatestDecision[key] = true
		}
	}

	if int64(len(revisionKeys)) != int64(view.revision)-int64(InitialRevision) {
		return ErrInvalidRetainedEncoding
	}

	for key := retainedFactKey(0); key < retainedFactKeyCount; key++ {
		expected := make(map[evidence.ID]semanticObservation)
		latestRevision := Revision(0)
		if hasLatestDecision[key] {
			decision := view.decisions[latestDecision[key]]
			latestRevision = decision.resultRevision
			for _, id := range decision.selected {
				expected[id] = observations[id]
			}
		}
		for id, observation := range observations {
			if observation.key == key && observation.observation.Review() == evidence.Accepted &&
				(!hasLatestDecision[key] || observation.introduced > latestRevision) {
				expected[id] = observation
			}
		}

		actual := make(map[evidence.ID]struct{}, len(view.facts[key].active))
		for _, id := range view.facts[key].active {
			if _, duplicate := actual[id]; duplicate {
				return ErrInvalidRetainedEncoding
			}
			actual[id] = struct{}{}
			observation, exists := observations[id]
			if !exists || observation.key != key || observation.observation.Review() != evidence.Accepted {
				return ErrInvalidRetainedEncoding
			}
		}
		if len(actual) != len(expected) {
			return ErrInvalidRetainedEncoding
		}
		for id := range expected {
			if _, exists := actual[id]; !exists {
				return ErrInvalidRetainedEncoding
			}
		}
		if view.facts[key].state != projectedMetadataState(expected) {
			return ErrInvalidRetainedEncoding
		}
	}

	return nil
}

func recordRevisionKey(revisions map[Revision]retainedFactKey, revision Revision, key retainedFactKey) bool {
	if existing, exists := revisions[revision]; exists {
		return existing == key
	}
	revisions[revision] = key
	return true
}

func projectedMetadataState(observations map[evidence.ID]semanticObservation) MetadataState {
	hasFalse := false
	hasTrue := false
	for _, observation := range observations {
		if observation.value == 0 {
			hasFalse = true
		} else {
			hasTrue = true
		}
	}
	switch {
	case hasFalse && hasTrue:
		return MetadataConflict
	case hasTrue:
		return MetadataObservedTrue
	case hasFalse:
		return MetadataObservedFalse
	default:
		return MetadataUnknown
	}
}
