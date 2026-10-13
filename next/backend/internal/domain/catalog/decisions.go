package catalog

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

// These encodings and IDs are internal PI-08/1 contracts, not public V3.
const CatalogDecisionPolicy = "pi08-catalog/1"

type CatalogRevision struct{ value uint64 }

var ErrCatalogRevisionConflict = errors.New("catalog revision conflict")
var ErrCatalogRevisionOverflow = errors.New("catalog revision overflow")
var ErrCatalogCommandConflict = errors.New("catalog command identity conflict")
var ErrCatalogUnsupported = errors.New("catalog decision lacks supported evidence")

func NewCatalogRevision(value uint64) (CatalogRevision, error) {
	if value == 0 || value > math.MaxInt64 {
		return CatalogRevision{}, ErrInvalidCatalog
	}
	return CatalogRevision{value: value}, nil
}

func (r CatalogRevision) Uint64() uint64 { return r.value }

func (r CatalogRevision) next() (CatalogRevision, error) {
	if r.value == 0 {
		return CatalogRevision{}, ErrInvalidCatalog
	}
	if r.value >= math.MaxInt64 {
		return CatalogRevision{}, ErrCatalogRevisionOverflow
	}
	return CatalogRevision{value: r.value + 1}, nil
}

type CatalogCommandID struct{ value [16]byte }

func NewCatalogCommandID() (CatalogCommandID, error) {
	value, err := newCatalogOpaqueID()
	return CatalogCommandID{value: value}, err
}

func (id CatalogCommandID) String() string { return hex.EncodeToString(id.value[:]) }
func (id CatalogCommandID) IsZero() bool   { return id.value == [16]byte{} }

type CatalogDecisionKind uint8

const (
	RecordObservations CatalogDecisionKind = iota + 1
	AcceptSupport
	ResolveConflict
)

// A command has exactly one source revision. Each selected reference must bind
// to that source, this subject and this claim key. Separate explicit commands
// can accumulate support from independent sources, without granting priority.
type CatalogReviewCommand struct {
	ID           CatalogCommandID
	Kind         CatalogDecisionKind
	Subject      CatalogSubject
	Source       SourceRegistration
	Expected     CatalogRevision
	Key          ClaimKey
	Observations []CatalogObservation
	Support      []evidence.ID
	Reason       string
}

type CatalogFactStatus uint8

const (
	CatalogFactUnknown CatalogFactStatus = iota
	CatalogFactAccepted
	CatalogFactConflict
)

type CatalogAlternative struct {
	Claim   CatalogClaim
	Support []evidence.ID
}

type CatalogField struct {
	Status       CatalogFactStatus
	Alternatives []CatalogAlternative
}

// Value never returns a scalar for unknown or conflicting fields.
func (f CatalogField) Value() (CatalogClaim, bool) {
	if f.Status != CatalogFactAccepted || len(f.Alternatives) != 1 {
		return CatalogClaim{}, false
	}
	return f.Alternatives[0].Claim, true
}

type CatalogDecisionResult struct {
	Command         CatalogCommandID
	Revision        CatalogRevision
	ProtectedCustom bool
	Field           CatalogField
}

type catalogDecision struct {
	command   CatalogReviewCommand
	reviewer  IssuerPrincipal
	canonical string
	result    CatalogDecisionResult
}

// CatalogReviewState is an immutable, bounded domain value. Apply returns a new
// state; it does not synchronize concurrent storage writers. A1.3/A2 must commit
// it atomically under the same expected-revision fence.
type CatalogReviewState struct {
	subject      CatalogSubject
	revision     CatalogRevision
	protected    bool
	observations []CatalogObservation
	decisions    []catalogDecision
	links        uint64
}

// NewCatalogReviewState seeds existing identity at revision 1 with no accepted
// facts. Registration/adoption and immutable version creation are later commands.
func NewCatalogReviewState(subject CatalogSubject, protectedCustom bool) (CatalogReviewState, error) {
	if subject.content.IsZero() {
		return CatalogReviewState{}, ErrInvalidCatalog
	}
	return CatalogReviewState{subject: subject, revision: CatalogRevision{value: 1}, protected: protectedCustom}, nil
}

func (s CatalogReviewState) Subject() CatalogSubject   { return s.subject }
func (s CatalogReviewState) Revision() CatalogRevision { return s.revision }
func (s CatalogReviewState) ProtectedCustom() bool     { return s.protected }
func (s CatalogReviewState) Observations() []CatalogObservation {
	return append([]CatalogObservation(nil), s.observations...)
}

// Field retains all active alternatives in deterministic claim order. Resolve
// supersedes active support only; old observations, commands and results remain.
func (s CatalogReviewState) Field(key ClaimKey) CatalogField {
	return s.fieldWith(key, nil, nil)
}

func (s CatalogReviewState) fieldWith(key ClaimKey, next *CatalogReviewCommand, added []CatalogObservation) CatalogField {
	selected := make(map[evidence.ID]bool)
	apply := func(command CatalogReviewCommand) {
		if command.Key != key || command.Kind == RecordObservations {
			return
		}
		if command.Kind == ResolveConflict {
			clear(selected)
		}
		for _, id := range command.Support {
			selected[id] = true
		}
	}
	for _, decision := range s.decisions {
		apply(decision.command)
	}
	if next != nil {
		apply(*next)
	}
	byClaim := make(map[CatalogClaim][]evidence.ID)
	for _, observations := range [][]CatalogObservation{s.observations, added} {
		for _, observation := range observations {
			if selected[observation.ID()] {
				claim := observation.input.Claim
				byClaim[claim] = append(byClaim[claim], observation.ID())
			}
		}
	}
	var field CatalogField
	for claim, support := range byClaim {
		sort.Slice(support, func(i, j int) bool { return support[i].String() < support[j].String() })
		field.Alternatives = append(field.Alternatives, CatalogAlternative{Claim: claim, Support: support})
	}
	sort.Slice(field.Alternatives, func(i, j int) bool {
		return claimOrder(field.Alternatives[i].Claim) < claimOrder(field.Alternatives[j].Claim)
	})
	if len(field.Alternatives) == 1 {
		field.Status = CatalogFactAccepted
	} else if len(field.Alternatives) > 1 {
		field.Status = CatalogFactConflict
	}
	return field
}

func claimOrder(claim CatalogClaim) string {
	e := catalogEncoder{data: make([]byte, 0)}
	e.claim(claim)
	return string(e.data)
}

// Apply checks current authority even for replay, then canonical identity before
// stale revision. No failure returns a partially changed state or result.
func (s CatalogReviewState) Apply(capability ReviewCapability, command CatalogReviewCommand) (CatalogReviewState, CatalogDecisionResult, error) {
	fail := func(err error) (CatalogReviewState, CatalogDecisionResult, error) {
		return s, CatalogDecisionResult{}, err
	}
	if s.revision.value == 0 || s.subject.content.IsZero() {
		return fail(ErrInvalidCatalog)
	}
	if command.Subject != s.subject {
		return fail(ErrCatalogSubjectMismatch)
	}
	normalized, err := normalizeReviewCommand(capability, command)
	if err != nil {
		return fail(err)
	}
	canonical, err := canonicalReviewCommand(capability.Reviewer(), normalized)
	if err != nil {
		return fail(err)
	}
	for _, decision := range s.decisions {
		if decision.command.ID == normalized.ID {
			if decision.canonical != canonical {
				return fail(ErrCatalogCommandConflict)
			}
			return s, cloneDecisionResult(decision.result), nil
		}
	}
	if normalized.Expected != s.revision {
		return fail(ErrCatalogRevisionConflict)
	}
	revision, err := s.revision.next()
	if err != nil {
		return fail(err)
	}
	if err := CheckLimit(RetainedDecisions, uint64(len(s.decisions)), 1); err != nil {
		return fail(err)
	}
	if err := CheckLimit(RetainedSupportLinks, s.links, uint64(len(normalized.Support))); err != nil {
		return fail(err)
	}

	var added []CatalogObservation
	protected := s.protected
	if normalized.Kind == RecordObservations {
		for _, observation := range normalized.Observations {
			exists := false
			for _, old := range s.observations {
				if err := CheckArtifactIdentity(old.input.Coordinate.artifact, observation.input.Coordinate.artifact); err != nil {
					return fail(err)
				}
				if old.ID() == observation.ID() {
					if old != observation {
						return fail(ErrCatalogObservationConflict)
					}
					exists = true
				}
			}
			if !exists {
				added = append(added, observation)
			}
		}
		if normalized.Source.Spec().Acquisition == OfflineCustom {
			protected = true
		}
	} else {
		var agreeing CatalogClaim
		for _, id := range normalized.Support {
			var found CatalogObservation
			for _, observation := range s.observations {
				if observation.ID() == id {
					found = observation
					break
				}
			}
			if found.ID().IsZero() {
				return fail(ErrCatalogUnsupported)
			}
			if found.input.Subject != normalized.Subject {
				return fail(ErrCatalogSubjectMismatch)
			}
			if found.input.Coordinate.artifact.source != normalized.Source {
				return fail(ErrCatalogSourceMismatch)
			}
			if found.input.Claim.key != normalized.Key {
				return fail(ErrCatalogUnsupported)
			}
			if normalized.Kind == ResolveConflict {
				if agreeing.key != 0 && agreeing != found.input.Claim {
					return fail(ErrCatalogUnsupported)
				}
				agreeing = found.input.Claim
			}
		}
		// Fingerprint and ownership are immutable version facts. Corrections
		// require a new version and supersession, outside this checkpoint.
		if normalized.Kind == ResolveConflict && (normalized.Key == FingerprintClaimKey || normalized.Key == OwnershipClaimKey) {
			return fail(ErrCatalogUnsupported)
		}
	}
	if err := CheckLimit(RetainedObservations, uint64(len(s.observations)), uint64(len(added))); err != nil {
		return fail(err)
	}
	result := CatalogDecisionResult{
		Command:         normalized.ID,
		Revision:        revision,
		ProtectedCustom: protected,
		Field:           s.fieldWith(normalized.Key, &normalized, added),
	}
	decision := catalogDecision{command: normalized, reviewer: capability.Reviewer(), canonical: canonical, result: result}
	counter := catalogEncoder{}
	s.encodeProspective(&counter, added, &decision)
	if counter.err != nil {
		return fail(counter.err)
	}
	// Full retained encoding was counted before aggregate slice copies.
	next := s
	next.revision, next.protected = revision, protected
	next.links += uint64(len(normalized.Support))
	next.observations = append(append([]CatalogObservation(nil), s.observations...), added...)
	sort.Slice(next.observations, func(i, j int) bool { return next.observations[i].ID().String() < next.observations[j].ID().String() })
	next.decisions = append(append([]catalogDecision(nil), s.decisions...), decision)
	return next, cloneDecisionResult(result), nil
}

func normalizeReviewCommand(capability ReviewCapability, command CatalogReviewCommand) (CatalogReviewCommand, error) {
	if command.ID.IsZero() || command.Expected.value == 0 || command.Expected.value > math.MaxInt64 {
		return CatalogReviewCommand{}, ErrInvalidCatalog
	}
	if err := checkCatalogText(command.Reason, TextBytes, false); err != nil {
		return CatalogReviewCommand{}, err
	}
	switch command.Kind {
	case RecordObservations:
		if command.Key != 0 || len(command.Support) != 0 || len(command.Observations) == 0 {
			return CatalogReviewCommand{}, ErrInvalidCatalog
		}
		if err := CheckLimit(MutationObservations, 0, uint64(len(command.Observations))); err != nil {
			return CatalogReviewCommand{}, err
		}
		for _, observation := range command.Observations {
			if err := capability.Authorize(command.Source, observation.input.Claim.key); err != nil {
				return CatalogReviewCommand{}, err
			}
			if err := ValidateObservationBinding(observation, command.Subject, command.Source); err != nil {
				return CatalogReviewCommand{}, err
			}
		}
		observations, err := CanonicalCatalogObservations(command.Observations)
		if err != nil {
			return CatalogReviewCommand{}, err
		}
		for i, observation := range observations {
			for _, earlier := range observations[:i] {
				if err := CheckArtifactIdentity(earlier.input.Coordinate.artifact, observation.input.Coordinate.artifact); err != nil {
					return CatalogReviewCommand{}, err
				}
			}
		}
		command.Observations = observations
		command.Support = nil
	case AcceptSupport, ResolveConflict:
		if err := capability.Authorize(command.Source, command.Key); err != nil {
			return CatalogReviewCommand{}, err
		}
		if len(command.Observations) != 0 || len(command.Support) == 0 {
			return CatalogReviewCommand{}, ErrInvalidCatalog
		}
		if err := CheckLimit(SelectedSupport, 0, uint64(len(command.Support))); err != nil {
			return CatalogReviewCommand{}, err
		}
		for _, id := range command.Support {
			if id.IsZero() {
				return CatalogReviewCommand{}, ErrInvalidCatalog
			}
		}
		support := append([]evidence.ID(nil), command.Support...)
		sort.Slice(support, func(i, j int) bool { return support[i].String() < support[j].String() })
		command.Observations = nil
		command.Support = support[:0]
		for _, id := range support {
			if len(command.Support) == 0 || command.Support[len(command.Support)-1] != id {
				command.Support = append(command.Support, id)
			}
		}
	default:
		return CatalogReviewCommand{}, ErrInvalidCatalog
	}
	return command, nil
}

func cloneDecisionResult(result CatalogDecisionResult) CatalogDecisionResult {
	result.Field.Alternatives = append([]CatalogAlternative(nil), result.Field.Alternatives...)
	for i := range result.Field.Alternatives {
		result.Field.Alternatives[i].Support = append([]evidence.ID(nil), result.Field.Alternatives[i].Support...)
	}
	return result
}

// LookupCommand requires the complete canonical command and current capability,
// returning the original result. Unknown commands grant no creation permission.
func (s CatalogReviewState) LookupCommand(capability ReviewCapability, command CatalogReviewCommand) (CatalogDecisionResult, bool, error) {
	if command.Subject != s.subject {
		return CatalogDecisionResult{}, false, ErrCatalogSubjectMismatch
	}
	command, err := normalizeReviewCommand(capability, command)
	if err != nil {
		return CatalogDecisionResult{}, false, err
	}
	canonical, err := canonicalReviewCommand(capability.Reviewer(), command)
	if err != nil {
		return CatalogDecisionResult{}, false, err
	}
	for _, decision := range s.decisions {
		if decision.command.ID == command.ID {
			if decision.canonical != canonical {
				return CatalogDecisionResult{}, false, ErrCatalogCommandConflict
			}
			return cloneDecisionResult(decision.result), true, nil
		}
	}
	return CatalogDecisionResult{}, false, nil
}

// catalogEncoder uses unsigned big-endian 64-bit integers and UTF-8 strings
// framed by their byte length (also uint64). Empty strings/sets have length 0;
// sets are normalized before encoding. Snapshots retain exact admitted bytes.
// Counting mode uses the identical encoder, before allocating output.
type catalogEncoder struct {
	data []byte
	size uint64
	err  error
}

func (e *catalogEncoder) raw(value string) {
	if e.err != nil {
		return
	}
	if err := CheckLimit(RetainedBytes, e.size, uint64(len(value))); err != nil {
		e.err = err
		return
	}
	e.size += uint64(len(value))
	if e.data != nil {
		e.data = append(e.data, value...)
	}
}

func (e *catalogEncoder) number(value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	e.raw(string(raw[:]))
}

func (e *catalogEncoder) text(value string) {
	e.number(uint64(len(value)))
	e.raw(value)
}

func (e *catalogEncoder) subject(subject CatalogSubject) {
	e.text(subject.content.String())
	e.text(subject.version.String())
}

func (e *catalogEncoder) source(source SourceRegistration) {
	e.text(source.ID().String())
	e.text(source.Revision().String())
	e.text(source.Issuer().String())
	spec := source.Spec()
	e.text(spec.Adapter)
	e.text(spec.AdapterVersion)
	e.text(spec.Scope)
	e.number(uint64(spec.Acquisition))
	e.number(uint64(len(spec.AllowedClaims)))
	for _, key := range spec.AllowedClaims {
		e.number(uint64(key))
	}
}

func (e *catalogEncoder) claim(claim CatalogClaim) {
	e.number(uint64(claim.key))
	e.text(claim.text)
	e.number(claim.fingerprint.AudioID().Uint64())
	e.text(claim.fingerprint.Hash().String())
	e.text(claim.owner.String())
}

func (e *catalogEncoder) observation(observation CatalogObservation) {
	input := observation.input
	e.text(input.ID.String())
	e.subject(input.Subject)
	e.claim(input.Claim)
	coordinate := input.Coordinate
	e.source(coordinate.artifact.source)
	facts := coordinate.artifact.facts
	e.text(hex.EncodeToString(facts.ID.value[:]))
	e.raw(string(facts.SHA256[:]))
	e.number(facts.ByteLength)
	e.number(facts.RecordCount)
	e.text(facts.ParserRevision)
	e.text(facts.SourceRevision)
	e.number(coordinate.index)
	e.text(coordinate.locator)
	e.text(coordinate.snapshot.raw)
	e.text(input.AssertedOrigin)
	e.text(input.ObservedAt.Format(time.RFC3339Nano))
	e.number(uint64(input.Confidence))
}

func (e *catalogEncoder) command(reviewer IssuerPrincipal, command CatalogReviewCommand) {
	e.text("pi08-command/1")
	e.text(CatalogDecisionPolicy)
	e.text(command.ID.String())
	e.number(uint64(command.Kind))
	e.subject(command.Subject)
	e.source(command.Source)
	e.text(reviewer.String())
	e.number(command.Expected.value)
	e.number(uint64(command.Key))
	e.text(command.Reason)
	e.number(uint64(len(command.Observations)))
	for _, observation := range command.Observations {
		e.observation(observation)
	}
	e.number(uint64(len(command.Support)))
	for _, id := range command.Support {
		e.text(id.String())
	}
}

func canonicalReviewCommand(reviewer IssuerPrincipal, command CatalogReviewCommand) (string, error) {
	counter := catalogEncoder{}
	counter.command(reviewer, command)
	if counter.err != nil {
		return "", counter.err
	}
	e := catalogEncoder{data: make([]byte, 0, int(counter.size))}
	e.command(reviewer, command)
	return string(e.data), e.err
}

func (e *catalogEncoder) result(result CatalogDecisionResult) {
	e.text(result.Command.String())
	e.number(result.Revision.value)
	if result.ProtectedCustom {
		e.number(1)
	} else {
		e.number(0)
	}
	e.number(uint64(result.Field.Status))
	e.number(uint64(len(result.Field.Alternatives)))
	for _, alternative := range result.Field.Alternatives {
		e.claim(alternative.Claim)
		e.number(uint64(len(alternative.Support)))
		for _, id := range alternative.Support {
			e.text(id.String())
		}
	}
}

func (s CatalogReviewState) encodeProspective(e *catalogEncoder, added []CatalogObservation, next *catalogDecision) {
	e.text("pi08-review-state/1")
	e.subject(s.subject)
	revision, protected, links := s.revision, s.protected, s.links
	if next != nil {
		revision, protected = next.result.Revision, next.result.ProtectedCustom
		links += uint64(len(next.command.Support))
	}
	e.number(revision.value)
	if protected {
		e.number(1)
	} else {
		e.number(0)
	}
	e.number(links)
	e.number(uint64(len(s.observations) + len(added)))
	for _, observations := range [][]CatalogObservation{s.observations, added} {
		for _, observation := range observations {
			e.observation(observation)
		}
	}
	count := len(s.decisions)
	if next != nil {
		count++
	}
	e.number(uint64(count))
	encodeDecision := func(decision catalogDecision) {
		// Charge both retained typed command and canonical replay bytes.
		e.command(decision.reviewer, decision.command)
		e.text(decision.canonical)
		e.result(decision.result)
	}
	for _, decision := range s.decisions {
		encodeDecision(decision)
	}
	if next != nil {
		encodeDecision(*next)
	}
}

// RetainedEncodingSize charges snapshots, support indices, commands and original
// result snapshots. There is no purge, unbounded export or implicit truncation.
func (s CatalogReviewState) RetainedEncodingSize() (uint64, error) {
	e := catalogEncoder{}
	s.encodeProspective(&e, nil, nil)
	return e.size, e.err
}
