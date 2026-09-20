// Package evidence holds internal provenance prototypes, not PI-13 wire schema.
// Confidence and review are independent; confidence never auto-accepts a fact.
package evidence

import (
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const (
	MaxText         = 4096
	MaxObservations = 4096
)

var (
	ErrInvalid     = errors.New("invalid evidence")
	ErrConflict    = errors.New("conflicting evidence identity or accepted facts")
	ErrUnsupported = errors.New("fact lacks reviewed supporting evidence")
	ErrLimit       = errors.New("evidence limit exceeded")
)

type ID struct{ value string }

func ParseID(text string) (ID, error) {
	if len(text) != 30 || text[:4] != "obs_" {
		return ID{}, ErrInvalid
	}
	if _, err := identity.ParseContentID("cnt_" + text[4:]); err != nil {
		return ID{}, ErrInvalid
	}
	return ID{value: text}, nil
}
func (id ID) String() string { return id.value }
func (id ID) IsZero() bool   { return id.value == "" }

// Source is a logical source, revision and record locator, never a fetched URI.
// Revision is opaque: lexical/numeric order does not establish source priority.
type Source struct{ name, revision, record string }

func NewSource(name, revision, record string) (Source, error) {
	if !ValidText(name, 128) || !ValidText(revision, 128) || !ValidText(record, 128) {
		return Source{}, ErrInvalid
	}
	return Source{name: name, revision: revision, record: record}, nil
}
func (s Source) Name() string     { return s.name }
func (s Source) Revision() string { return s.revision }
func (s Source) Record() string   { return s.record }
func (s Source) IsZero() bool     { return s.name == "" }

// ValidText checks length before scanning and rejects control text. It neither
// interprets markup nor sanitizes values into different evidence.
func ValidText(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return false
		}
	}
	return true
}

type Confidence uint8

const (
	ConfidenceUnknown Confidence = iota
	Tentative
	Corroborated
)

type Review uint8

const (
	Pending Review = iota
	Accepted
	Disputed
	Rejected
)

// Claim keys are internal caller vocabulary. Subject is an opaque reference;
// this package assigns no identity or semantic meaning from a text value.
type Claim struct{ Subject, Key, Value string }

func (c Claim) valid() bool {
	return ValidText(c.Subject, 128) && ValidText(c.Key, 128) && ValidText(c.Value, MaxText)
}

type Observation struct {
	id         ID
	source     Source
	claim      Claim
	observedAt time.Time
	confidence Confidence
	review     Review
}

func NewObservation(id ID, source Source, claim Claim, observedAt time.Time, confidence Confidence, review Review) (Observation, error) {
	if id.IsZero() || source.IsZero() || !claim.valid() || observedAt.IsZero() || confidence > Corroborated || review > Rejected {
		return Observation{}, ErrInvalid
	}
	return Observation{id: id, source: source, claim: claim, observedAt: observedAt.Round(0).UTC(), confidence: confidence, review: review}, nil
}
func (o Observation) ID() ID                 { return o.id }
func (o Observation) Source() Source         { return o.source }
func (o Observation) Claim() Claim           { return o.claim }
func (o Observation) ObservedAt() time.Time  { return o.observedAt }
func (o Observation) Confidence() Confidence { return o.confidence }
func (o Observation) Review() Review         { return o.review }

// Canonical retains disagreements under different IDs and rejects ID reuse.
// It copies inputs, sorts by opaque ID and coalesces only exact same-ID replays.
func Canonical(observations []Observation) ([]Observation, error) {
	if len(observations) > MaxObservations {
		return nil, ErrLimit
	}
	sorted := append([]Observation(nil), observations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id.value < sorted[j].id.value })
	result := make([]Observation, 0, len(sorted))
	for _, o := range sorted {
		if _, err := NewObservation(o.id, o.source, o.claim, o.observedAt, o.confidence, o.review); err != nil {
			return nil, err
		}
		if len(result) > 0 && result[len(result)-1].id == o.id {
			if result[len(result)-1] != o {
				return nil, ErrConflict
			}
			continue
		}
		result = append(result, o)
	}
	return result, nil
}

// Fact preserves all supplied observations, including dissent, and separately
// identifies reviewed support. Another accepted value for the same subject/key
// is an unresolved conflict, never a source-ranking or first-match decision.
type Fact struct {
	claim        Claim
	observations []Observation
	support      []ID
}

func Accept(claim Claim, observations []Observation) (Fact, error) {
	if !claim.valid() {
		return Fact{}, ErrInvalid
	}
	canonical, err := Canonical(observations)
	if err != nil {
		return Fact{}, err
	}
	var support []ID
	for _, o := range canonical {
		if o.claim.Subject != claim.Subject || o.claim.Key != claim.Key || o.review != Accepted {
			continue
		}
		if o.claim.Value != claim.Value {
			return Fact{}, ErrConflict
		}
		support = append(support, o.id)
	}
	if len(support) == 0 {
		return Fact{}, ErrUnsupported
	}
	return Fact{claim: claim, observations: canonical, support: support}, nil
}
func (f Fact) Claim() Claim                { return f.claim }
func (f Fact) Observations() []Observation { return append([]Observation(nil), f.observations...) }
func (f Fact) Support() []ID               { return append([]ID(nil), f.support...) }
