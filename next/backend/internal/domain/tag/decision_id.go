package tag

import (
	"errors"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const decisionIDLength = 30

// ErrInvalidDecisionID means a decision identity is not in its canonical form.
var ErrInvalidDecisionID = errors.New("invalid decision identity")

// DecisionID is the stable internal identity of one retained tag decision.
type DecisionID struct{ value string }

// ParseDecisionID accepts only dec_ followed by the PI04 opaque-ID payload.
func ParseDecisionID(text string) (DecisionID, error) {
	if len(text) != decisionIDLength {
		return DecisionID{}, ErrInvalidDecisionID
	}
	if text[:4] != "dec_" {
		return DecisionID{}, ErrInvalidDecisionID
	}
	if _, err := identity.ParseContentID("cnt_" + text[4:]); err != nil {
		return DecisionID{}, ErrInvalidDecisionID
	}
	return DecisionID{value: text}, nil
}

func (id DecisionID) String() string { return id.value }

func (id DecisionID) IsZero() bool { return id.value == "" }
