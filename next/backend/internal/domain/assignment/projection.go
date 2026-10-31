package assignment

import (
	"errors"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var ErrKnowledgeRevision = errors.New("unknown assignment knowledge revision")

type DesiredState uint8

const (
	NoAssignment DesiredState = iota
	Assigned
	Unassigned
	Retired
)

// DesiredAssignment is a projection, never proof of applied/playback state.
// Until is the next transition known at KnowledgeRevision, exclusive; zero
// means no known end. NoAssignment has no transition or target. A tombstone is
// retained, with historical assigned targets still readable at earlier times.
type DesiredAssignment struct {
	AssignmentID                                        ID
	TagID                                               identity.TagID
	HeadRevision, KnowledgeRevision, TransitionRevision uint64
	State                                               DesiredState
	Target                                              OriginalTarget
	From, Until                                         time.Time
}

// Project selects explicit effective time and optional retained knowledge.
// nil means head; pointer-to-zero means before every event. Future knowledge
// rejects rather than clamps. Neither TagEvidence nor catalog is an input:
// baseline, metadata, availability and supersession can never select a target.
func (s DecisionLedger) Project(at time.Time, knowledge *uint64) (DesiredAssignment, error) {
	if s.id.IsZero() || s.tag.IsZero() {
		return DesiredAssignment{}, ErrInvalidReference
	}
	at, err := decisionTime(at)
	if err != nil {
		return DesiredAssignment{}, err
	}
	r := s.revision
	if knowledge != nil {
		r = *knowledge
	}
	if r > s.revision {
		return DesiredAssignment{}, ErrKnowledgeRevision
	}
	p := DesiredAssignment{AssignmentID: s.id, TagID: s.tag, HeadRevision: s.revision, KnowledgeRevision: r}
	// A1c retains only transitions, with strictly increasing effective times.
	// Revision bounds retained knowledge; it never resolves equal timeline times.
	// Audit RecordedAt has no bearing on precedence. Future non-transition
	// review kinds must retain revision order without moving this timeline.
	for _, event := range s.events {
		result, in := event.Result, event.Result.Command.input
		if result.Revision > r {
			break
		}
		if in.EffectiveAt.After(at) {
			if p.TransitionRevision != 0 {
				p.Until = in.EffectiveAt
			}
			break
		}
		p.TransitionRevision, p.From = result.Revision, in.EffectiveAt
		p.Target = OriginalTarget{}
		switch in.Kind {
		case Assign:
			p.State, p.Target = Assigned, in.Target
		case Unassign:
			p.State = Unassigned
		case Retire:
			p.State = Retired
		}
	}
	return p, nil
}
