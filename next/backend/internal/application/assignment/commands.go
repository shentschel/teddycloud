package assignment

import (
	"encoding/json"
	"sort"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type Kind uint8

const (
	Assign Kind = iota + 1
	Unassign
	RecordClassification
	ResolveClassification
	RecordOriginalTarget
	ResolveOriginalTarget
	RetireAssignment
)
const PolicyRevision uint64 = 1

type OptionalURI struct {
	Present bool
	Value   string
}

// Actor is an optional assertion checked against the authenticated principal;
// it can never establish authority. Inactive fields reject rather than vanish
// from replay identity. ReviewID is independently allocated, not CommandID text.
type Command struct {
	CommandID                                                      d.CommandID
	AssignmentID                                                   d.ID
	TagID                                                          identity.TagID
	Kind                                                           Kind
	ExpectedRevision, ExpectedTagRevision, ExpectedCatalogRevision uint64
	PolicyRevision                                                 uint64
	Actor, Reason                                                  string
	EffectiveAt                                                    time.Time
	Target                                                         d.OriginalTarget
	URI                                                            OptionalURI
	ReviewID                                                       d.ReviewID
	Evidence                                                       d.Evidence
	Classification                                                 d.PhysicalClassification
	Support                                                        []d.EvidenceID
}

func (k Kind) review() bool { return k >= RecordClassification && k <= ResolveOriginalTarget }
func (k Kind) target() bool {
	return k == Assign || k == RecordOriginalTarget || k == ResolveOriginalTarget
}
func (k Kind) decision() d.DecisionKind {
	switch k {
	case Assign:
		return d.Assign
	case Unassign:
		return d.Unassign
	case RetireAssignment:
		return d.Retire
	}
	return 0
}
func normalizedCommand(in Command, actor string) (Command, error) {
	if in.URI.Present || in.URI.Value != "" {
		return Command{}, ErrURIUnsupported
	}
	if in.Actor != "" && in.Actor != actor {
		return Command{}, ErrUnauthorized
	}
	in.Actor = "" // the bound actor is retained separately in canonical identity
	if _, err := d.ParseCommandID(in.CommandID.String()); err != nil {
		return Command{}, ErrInvalidInput
	}
	if _, err := d.ParseID(in.AssignmentID.String()); err != nil {
		return Command{}, ErrInvalidInput
	}
	if _, err := identity.ParseTagID(in.TagID.String()); err != nil {
		return Command{}, ErrInvalidInput
	}
	if in.Kind < Assign || in.Kind > RetireAssignment || in.PolicyRevision != PolicyRevision ||
		in.ExpectedRevision > d.MaxDecisionRevision || in.ExpectedTagRevision == 0 || in.ExpectedTagRevision > d.MaxDecisionRevision {
		return Command{}, ErrInvalidInput
	}
	if in.Kind.target() {
		if _, err := in.Target.CanonicalByteLen(); err != nil {
			return Command{}, ErrInvalidInput
		}
		if in.ExpectedCatalogRevision == 0 || in.ExpectedCatalogRevision > d.MaxDecisionRevision {
			return Command{}, ErrInvalidInput
		}
	} else if in.Target != (d.OriginalTarget{}) || in.ExpectedCatalogRevision != 0 {
		return Command{}, ErrInvalidInput
	}
	if !in.Kind.review() {
		if !in.ReviewID.IsZero() || in.Evidence != (d.Evidence{}) || in.Classification != d.Unknown || len(in.Support) != 0 {
			return Command{}, ErrInvalidInput
		}
		command, err := d.NewDecisionCommand(in.decisionInput(actor, in.ExpectedRevision))
		if err != nil {
			return Command{}, sanitize(err)
		}
		in.EffectiveAt = command.Input().EffectiveAt
	} else {
		if !in.EffectiveAt.IsZero() {
			return Command{}, ErrInvalidInput
		}
		if _, err := d.ParseReviewID(in.ReviewID.String()); err != nil {
			return Command{}, ErrInvalidInput
		}
		if _, err := d.NewReviewRequest(in.ReviewID, in.ExpectedRevision, in.PolicyRevision, in.Reason); err != nil {
			return Command{}, ErrInvalidInput
		}
		class := in.Kind == RecordClassification || in.Kind == ResolveClassification
		if class && in.Classification != d.Original && in.Classification != d.CustomCard {
			return Command{}, ErrInvalidInput
		}
		if !class && in.Classification != d.Unknown {
			return Command{}, ErrInvalidInput
		}
		claim := in.Kind == RecordClassification || in.Kind == RecordOriginalTarget
		if claim {
			if _, err := in.Evidence.CanonicalByteLen(); err != nil || in.Evidence.Subject() != in.TagID || len(in.Support) != 0 {
				return Command{}, ErrInvalidInput
			}
		} else {
			if in.Evidence != (d.Evidence{}) || len(in.Support) == 0 {
				return Command{}, ErrInvalidInput
			}
			if uint64(len(in.Support)) > d.MaxSelectedSupport {
				return Command{}, ErrLimit
			}
		}
	}
	if uint64(len(in.Support)) > d.MaxSelectedSupport {
		return Command{}, ErrLimit
	}
	in.Support = append([]d.EvidenceID(nil), in.Support...)
	sort.Slice(in.Support, func(i, j int) bool { return in.Support[i].String() < in.Support[j].String() })
	for i, id := range in.Support {
		if _, err := d.ParseEvidenceID(id.String()); err != nil {
			return Command{}, ErrInvalidInput
		}
		if i > 0 && id == in.Support[i-1] {
			return Command{}, ErrInvalidInput
		}
	}
	return in, nil
}
func (in Command) decisionInput(actor string, localRevision uint64) d.DecisionInput {
	return d.DecisionInput{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID,
		Kind: in.Kind.decision(), ExpectedRevision: localRevision, ExpectedTagRevision: in.ExpectedTagRevision,
		ExpectedCatalogRevision: in.ExpectedCatalogRevision, PolicyRevision: in.PolicyRevision,
		Actor: actor, Reason: in.Reason, EffectiveAt: in.EffectiveAt, Target: in.Target}
}

// TCAS/1 is an internal canonical envelope, not a public API or durable codec.
// Fixed struct field order, JSON escaping, explicit absent URI/profile and sorted
// support reserve B1's slot without interpreting any URI. Domain primitive bytes
// preserve A1b/A1c formats; canonical input also binds the GLOBAL expected head.
type canonicalInput struct {
	Format                                         string
	Command, Assignment, Tag                       string
	Kind                                           Kind
	Expected, TagRevision, CatalogRevision, Policy uint64
	Actor, Reason                                  string
	Effective                                      string
	Content, Version, Review                       string
	Evidence                                       []byte
	Classification                                 d.PhysicalClassification
	Support                                        []string
	URI                                            struct {
		Present          bool
		Profile, Locator string
	}
}

func canonicalCommand(in Command, actor string) ([]byte, error) {
	in, err := normalizedCommand(in, actor)
	if err != nil {
		return nil, err
	}
	v := canonicalInput{Format: "TCAS/1", Command: in.CommandID.String(), Assignment: in.AssignmentID.String(), Tag: in.TagID.String(),
		Kind: in.Kind, Expected: in.ExpectedRevision, TagRevision: in.ExpectedTagRevision, CatalogRevision: in.ExpectedCatalogRevision,
		Policy: in.PolicyRevision, Actor: actor, Reason: in.Reason, Content: in.Target.ContentID().String(), Version: in.Target.VersionID().String(),
		Review: in.ReviewID.String(), Classification: in.Classification, Support: []string{}}
	if !in.EffectiveAt.IsZero() {
		v.Effective = in.EffectiveAt.Format(time.RFC3339Nano)
	}
	if in.Evidence != (d.Evidence{}) {
		v.Evidence, err = in.Evidence.CanonicalBytes()
		if err != nil {
			return nil, err
		}
	}
	for _, id := range in.Support {
		v.Support = append(v.Support, id.String())
	}
	return json.Marshal(v)
}
