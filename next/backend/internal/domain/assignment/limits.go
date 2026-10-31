package assignment

import "errors"

const (
	MaxEvidenceEvents        uint64 = 4096
	MaxEvidenceObservations  uint64 = 4096
	MaxEvidenceSupportLinks  uint64 = 16384
	MaxEvidenceRetainedBytes uint64 = 16 << 20
	MaxNewObservations       uint64 = 64
	MaxSelectedSupport       uint64 = 64
	MaxReviewReasonBytes            = 1024
	MaxReviewReferenceBytes         = 128
)

var (
	ErrEvidenceEventLimit   = errors.New("assignment evidence event limit exceeded")
	ErrObservationLimit     = errors.New("assignment observation limit exceeded")
	ErrSupportLinkLimit     = errors.New("assignment support link limit exceeded")
	ErrRetainedByteLimit    = errors.New("assignment retained byte limit exceeded")
	ErrCommandEvidenceLimit = errors.New("assignment command evidence limit exceeded")
)

// EvidenceDelta charges all retained material, not only the visible projection.
// A future encoder/writer must supply complete canonical command/result,
// snapshot, index, support and escaped-text bytes. This is resource arithmetic,
// not proof that an encoded aggregate can reach an isolated count boundary.
type EvidenceDelta struct {
	Events, Observations, SupportLinks, RetainedBytes uint64
}

// EvidenceUsage is an immutable, simultaneously bounded resource value. Its
// zero value is empty. It never evicts evidence or silently truncates a delta.
type EvidenceUsage struct {
	events, observations, supportLinks, retainedBytes uint64
}

func NewEvidenceUsage(events, observations, supportLinks, retainedBytes uint64) (EvidenceUsage, error) {
	u := EvidenceUsage{events, observations, supportLinks, retainedBytes}
	if err := u.validate(); err != nil {
		return EvidenceUsage{}, err
	}
	return u, nil
}

func (u EvidenceUsage) Events() uint64        { return u.events }
func (u EvidenceUsage) Observations() uint64  { return u.observations }
func (u EvidenceUsage) SupportLinks() uint64  { return u.supportLinks }
func (u EvidenceUsage) RetainedBytes() uint64 { return u.retainedBytes }

func (u EvidenceUsage) validate() error {
	switch {
	case u.events > MaxEvidenceEvents:
		return ErrEvidenceEventLimit
	case u.observations > MaxEvidenceObservations:
		return ErrObservationLimit
	case u.supportLinks > MaxEvidenceSupportLinks:
		return ErrSupportLinkLimit
	case u.retainedBytes > MaxEvidenceRetainedBytes:
		return ErrRetainedByteLimit
	}
	return nil
}

// Prospective checks every resource before adding anything. Subtraction after
// validating the current state avoids unsigned wraparound, including MaxUint64
// deltas. A failure returns the original value unchanged.
func (u EvidenceUsage) Prospective(d EvidenceDelta) (EvidenceUsage, error) {
	if err := u.validate(); err != nil {
		return u, err
	}
	if d.Observations > MaxNewObservations || d.SupportLinks > MaxSelectedSupport {
		return u, ErrCommandEvidenceLimit
	}
	switch {
	case d.Events > MaxEvidenceEvents-u.events:
		return u, ErrEvidenceEventLimit
	case d.Observations > MaxEvidenceObservations-u.observations:
		return u, ErrObservationLimit
	case d.SupportLinks > MaxEvidenceSupportLinks-u.supportLinks:
		return u, ErrSupportLinkLimit
	case d.RetainedBytes > MaxEvidenceRetainedBytes-u.retainedBytes:
		return u, ErrRetainedByteLimit
	}
	return EvidenceUsage{
		u.events + d.Events, u.observations + d.Observations,
		u.supportLinks + d.SupportLinks, u.retainedBytes + d.RetainedBytes,
	}, nil
}
