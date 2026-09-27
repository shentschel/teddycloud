package tag

import (
	"errors"
	"sort"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const MaxEncodedTagBytes int64 = 8 * 1024 * 1024

var (
	// ErrInvalidRetainedEncoding is intentionally generic: byte/field validity
	// is not a claim that future B1 transition semantics have been validated.
	ErrInvalidRetainedEncoding = errors.New("invalid retained tag encoding")
	ErrEncodedTagLimit         = errors.New("retained tag encoding limit exceeded")
)

type retainedFactView struct {
	state  MetadataState
	active []evidence.ID
}

type retainedObservationView struct {
	observation        evidence.Observation
	introducedRevision Revision
}

type retainedDecisionView struct {
	id               DecisionID
	key              retainedFactKey
	expectedRevision Revision
	resultRevision   Revision
	selected         []evidence.ID
}

// retainedTagView borrows every slice. The counter traverses these real slices
// directly and never accepts declared counts or cached byte totals.
type retainedTagView struct {
	id           identity.TagID
	uid          UID
	revision     Revision
	facts        [retainedFactKeyCount]retainedFactView
	observations []retainedObservationView
	decisions    []retainedDecisionView
}

type retainedFactKey uint8

const (
	retainedProtocolValid retainedFactKey = iota
	retainedClaimed
	retainedCloudAuth
	retainedOwned
	retainedFactKeyCount
)

func checkRetainedViewCounts(view retainedTagView) error {
	// Reject top-level counts before inspecting any member.
	if len(view.observations) > MaxRetainedObservations || len(view.decisions) > MaxRetainedDecisions {
		return ErrHistoryLimit
	}

	activeTotal := int64(0)
	for key := retainedFactKey(0); key < retainedFactKeyCount; key++ {
		active := view.facts[key].active
		if len(active) > MaxDecisionSupportLinks {
			return ErrHistoryLimit
		}
		var ok bool
		activeTotal, ok = checkedAddInt64(activeTotal, int64(len(active)), int64(MaxRetainedObservations))
		if !ok {
			return ErrHistoryLimit
		}
	}
	if activeTotal > int64(len(view.observations)) {
		return ErrHistoryLimit
	}

	retainedLinks := int64(0)
	for i := range view.decisions {
		selected := view.decisions[i].selected
		if len(selected) > MaxDecisionSupportLinks {
			return ErrHistoryLimit
		}
		var ok bool
		retainedLinks, ok = checkedAddInt64(retainedLinks, int64(len(selected)), int64(MaxRetainedDecisionSupportLinks))
		if !ok {
			return ErrHistoryLimit
		}
	}
	return nil
}

// canonicalEncodingView is called only after successful byte preflight. It
// sorts clones for deterministic output and rejects duplicate encoded keys.
func canonicalEncodingView(view retainedTagView) (retainedTagView, error) {
	canonical := view
	for key := retainedFactKey(0); key < retainedFactKeyCount; key++ {
		canonical.facts[key].active = append([]evidence.ID(nil), view.facts[key].active...)
		sortEvidenceIDs(canonical.facts[key].active)
		if hasDuplicateEvidenceID(canonical.facts[key].active) {
			return retainedTagView{}, ErrInvalidRetainedEncoding
		}
	}

	canonical.observations = append([]retainedObservationView(nil), view.observations...)
	sort.Slice(canonical.observations, func(i, j int) bool {
		return canonical.observations[i].observation.ID().String() < canonical.observations[j].observation.ID().String()
	})
	for i := 1; i < len(canonical.observations); i++ {
		if canonical.observations[i-1].observation.ID() == canonical.observations[i].observation.ID() {
			return retainedTagView{}, ErrInvalidRetainedEncoding
		}
	}

	canonical.decisions = make([]retainedDecisionView, len(view.decisions))
	for i := range view.decisions {
		canonical.decisions[i] = view.decisions[i]
		canonical.decisions[i].selected = append([]evidence.ID(nil), view.decisions[i].selected...)
		sortEvidenceIDs(canonical.decisions[i].selected)
		if hasDuplicateEvidenceID(canonical.decisions[i].selected) {
			return retainedTagView{}, ErrInvalidRetainedEncoding
		}
	}
	sort.Slice(canonical.decisions, func(i, j int) bool {
		return canonical.decisions[i].id.String() < canonical.decisions[j].id.String()
	})
	for i := 1; i < len(canonical.decisions); i++ {
		if canonical.decisions[i-1].id == canonical.decisions[i].id {
			return retainedTagView{}, ErrInvalidRetainedEncoding
		}
	}
	return canonical, nil
}

func sortEvidenceIDs(ids []evidence.ID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
}

func hasDuplicateEvidenceID(ids []evidence.ID) bool {
	for i := 1; i < len(ids); i++ {
		if ids[i-1] == ids[i] {
			return true
		}
	}
	return false
}

func checkedAddInt64(total, add, limit int64) (int64, bool) {
	if total < 0 || add < 0 || limit < 0 || total > limit || add > limit-total {
		return 0, false
	}
	return total + add, true
}
