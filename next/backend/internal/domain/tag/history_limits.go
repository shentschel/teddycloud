package tag

import (
	"errors"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

const (
	MaxRetainedObservations         = evidence.MaxObservations
	MaxRetainedDecisions            = 4096
	MaxDecisionSupportLinks         = 4096
	MaxRetainedDecisionSupportLinks = 16384
	MaxNewObservationsPerCommand    = 64
)

// ErrHistoryLimit reports that a structural Tag history count exceeds a fixed
// retention or command limit. It deliberately contains no caller input.
var ErrHistoryLimit = errors.New("tag history count limit exceeded")

// CheckRetainedHistoryCounts is a count-only structural gate over the actual
// retained slices. It does not validate observation or support semantics,
// uniqueness, encoded size, or storage representation. Every support entry,
// including duplicates and superseded selections, consumes capacity.
//
// Call this before cloning inputs or constructing an aggregate representation.
func CheckRetainedHistoryCounts(observations []evidence.Observation, decisionSupport [][]evidence.ID) error {
	// Check top-level lengths before inspecting any per-decision slice.
	if len(observations) > MaxRetainedObservations || len(decisionSupport) > MaxRetainedDecisions {
		return ErrHistoryLimit
	}

	totalSupport := 0
	for _, support := range decisionSupport {
		if len(support) > MaxDecisionSupportLinks {
			return ErrHistoryLimit
		}
		var ok bool
		totalSupport, ok = checkedAddWithin(totalSupport, len(support), MaxRetainedDecisionSupportLinks)
		if !ok {
			return ErrHistoryLimit
		}
	}
	return nil
}

// CheckNewObservationCount is a count-only structural gate for one command.
// Observation semantics remain the responsibility of evidence validation.
func CheckNewObservationCount(observations []evidence.Observation) error {
	if len(observations) > MaxNewObservationsPerCommand {
		return ErrHistoryLimit
	}
	return nil
}

func checkedAddWithin(total, add, limit int) (int, bool) {
	if total < 0 || add < 0 || limit < 0 || total > limit || add > limit-total {
		return 0, false
	}
	return total + add, true
}
