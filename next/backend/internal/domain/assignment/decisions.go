package assignment

import (
	"bytes"
	"errors"
	"math"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const MaxDecisionRevision uint64 = math.MaxInt64

var (
	ErrInvalidDecision    = errors.New("invalid assignment decision")
	ErrCommandConflict    = errors.New("assignment command identity conflict")
	ErrDependencyConflict = errors.New("assignment dependency revision conflict")
	ErrDecisionRetired    = errors.New("assignment decision ledger is retired")
	ErrRevisionOverflow   = errors.New("assignment decision revision exhausted")
)

// CommandID is independently allocated, never derived from content or metadata.
// Cross-aggregate uniqueness must be fenced by the future scoped command store.
type CommandID struct{ value string }

func ParseCommandID(s string) (CommandID, error) {
	if len(s) != 32 || s == "00000000000000000000000000000000" {
		return CommandID{}, ErrInvalidID
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return CommandID{}, ErrInvalidID
		}
	}
	return CommandID{s}, nil
}
func (id CommandID) String() string { return id.value }
func (id CommandID) IsZero() bool   { return id.value == "" }

type DecisionKind uint8

const (
	Assign DecisionKind = iota + 1
	Unassign
	Retire
)

// DecisionInput has no URI or review support input: those writers are outside
// A1c. Actor is descriptive replay material, NOT an authentication capability.
// A future application must bind it to its authenticated authority before lookup.
type DecisionInput struct {
	CommandID                                                      CommandID
	AssignmentID                                                   ID
	TagID                                                          identity.TagID
	Kind                                                           DecisionKind
	ExpectedRevision, ExpectedTagRevision, ExpectedCatalogRevision uint64
	PolicyRevision                                                 uint64
	Actor, Reason                                                  string
	EffectiveAt                                                    time.Time
	Target                                                         OriginalTarget
}

type DecisionCommand struct{ input DecisionInput }

func decisionTime(t time.Time) (time.Time, error) {
	t = t.Round(0).UTC()
	if t.IsZero() || t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}, ErrInvalidDecision
	}
	return t, nil
}

func NewDecisionCommand(in DecisionInput) (DecisionCommand, error) {
	if _, err := ParseCommandID(in.CommandID.String()); err != nil {
		return DecisionCommand{}, err
	}
	if _, err := ParseID(in.AssignmentID.String()); err != nil {
		return DecisionCommand{}, err
	}
	if _, err := identity.ParseTagID(in.TagID.String()); err != nil {
		return DecisionCommand{}, ErrInvalidReference
	}
	if in.ExpectedRevision > MaxDecisionRevision || in.ExpectedTagRevision == 0 ||
		in.ExpectedTagRevision > MaxDecisionRevision || in.PolicyRevision == 0 || in.PolicyRevision > MaxDecisionRevision ||
		!safeReviewText(in.Actor, MaxReviewReferenceBytes) || !safeReviewText(in.Reason, MaxReviewReasonBytes) {
		return DecisionCommand{}, ErrInvalidDecision
	}
	var err error
	in.EffectiveAt, err = decisionTime(in.EffectiveAt)
	if err != nil {
		return DecisionCommand{}, err
	}
	switch in.Kind {
	case Assign:
		if _, err := in.Target.CanonicalByteLen(); err != nil {
			return DecisionCommand{}, ErrInvalidReference
		}
		if in.ExpectedCatalogRevision == 0 || in.ExpectedCatalogRevision > MaxDecisionRevision {
			return DecisionCommand{}, ErrInvalidDecision
		}
	case Unassign, Retire:
		if in.Target != (OriginalTarget{}) || in.ExpectedCatalogRevision != 0 {
			return DecisionCommand{}, ErrInvalidDecision
		}
	default:
		return DecisionCommand{}, ErrInvalidDecision
	}
	return DecisionCommand{in}, nil
}

func (c DecisionCommand) Input() DecisionInput              { return c.input }
func (c DecisionCommand) CanonicalBytes() ([]byte, error)   { return canonicalBytes(c) }
func (c DecisionCommand) CanonicalByteLen() (uint64, error) { return canonicalByteLen(c) }

// TCAD v1 is distinct from A1b's TCAE primitive format. It reuses A1b's
// big-endian integers, length-framed strings/nesting and exact target encoding.
// Types: command=1, result=2, event=3. Command fields follow DecisionInput order:
// command, aggregate, Tag, kind, expected aggregate/Tag/catalog, policy, actor,
// reason, effective time, target (absent for non-Assign), absent URI (u8=0),
// support count (u32=0). Times are Unix seconds (two's complement u64) + u32 ns.
// Result: nested command, revision, prior command (empty at revision 1).
// Event: nested result, recorded-at. Recorded-at is outside replay identity.
// No decoder or durable format is activated by this domain checkpoint.
func decisionHeader(e *canonicalEncoder, kind byte) {
	e.raw([]byte{'T', 'C', 'A', 'D', CanonicalEncodingVersion, kind})
}
func encodeDecisionTime(e *canonicalEncoder, t time.Time) {
	e.u64(uint64(t.Unix()))
	e.u32(uint32(t.Nanosecond()))
}
func (c DecisionCommand) appendCanonical(e *canonicalEncoder) error {
	validated, err := NewDecisionCommand(c.input)
	if err != nil {
		return err
	}
	in := validated.input
	decisionHeader(e, 1)
	e.text(in.CommandID.String())
	e.text(in.AssignmentID.String())
	e.text(in.TagID.String())
	e.u8(byte(in.Kind))
	e.u64(in.ExpectedRevision)
	e.u64(in.ExpectedTagRevision)
	e.u64(in.ExpectedCatalogRevision)
	e.u64(in.PolicyRevision)
	e.text(in.Actor)
	e.text(in.Reason)
	encodeDecisionTime(e, in.EffectiveAt)
	if in.Kind == Assign {
		if err := e.nested(in.Target); err != nil {
			return err
		}
	} else {
		e.u32(0)
	}
	e.u8(0)
	e.u32(0)
	return nil
}

// DecisionReferences is a core-supplied checked snapshot. For Assign, Target
// asserts exact Content/version ownership, not a suggested or latest target.
// A1c compares values; only the later scoped port can prove database existence,
// ownership and isolation. Replay deliberately does not recheck live references.
type DecisionReferences struct {
	TagID                        identity.TagID
	TagRevision, CatalogRevision uint64
	Target                       OriginalTarget
}

type DecisionResult struct {
	Command         DecisionCommand
	Revision        uint64
	PreviousCommand CommandID
}

func (r DecisionResult) CanonicalBytes() ([]byte, error)   { return canonicalBytes(r) }
func (r DecisionResult) CanonicalByteLen() (uint64, error) { return canonicalByteLen(r) }
func (r DecisionResult) appendCanonical(e *canonicalEncoder) error {
	if r.Revision == 0 || r.Revision > MaxDecisionRevision || r.Command.input.ExpectedRevision != r.Revision-1 {
		return ErrInvalidDecision
	}
	if r.Revision == 1 {
		if !r.PreviousCommand.IsZero() {
			return ErrInvalidDecision
		}
	} else if _, err := ParseCommandID(r.PreviousCommand.String()); err != nil {
		return err
	}
	decisionHeader(e, 2)
	if err := e.nested(r.Command); err != nil {
		return err
	}
	e.u64(r.Revision)
	e.text(r.PreviousCommand.String())
	return nil
}

type DecisionEvent struct {
	Result     DecisionResult
	RecordedAt time.Time
}

func (v DecisionEvent) CanonicalBytes() ([]byte, error)   { return canonicalBytes(v) }
func (v DecisionEvent) CanonicalByteLen() (uint64, error) { return canonicalByteLen(v) }
func (v DecisionEvent) appendCanonical(e *canonicalEncoder) error {
	at, err := decisionTime(v.RecordedAt)
	if err != nil {
		return err
	}
	decisionHeader(e, 3)
	if err := e.nested(v.Result); err != nil {
		return err
	}
	encodeDecisionTime(e, at)
	return nil
}

// DecisionLedger is an immutable append-only value, separate from History.
// It is not a concurrent writer: a future serialized owner must install the
// returned snapshot atomically with CAS and fence global command identity.
type DecisionLedger struct {
	id       ID
	tag      identity.TagID
	revision uint64
	retired  bool
	usage    EvidenceUsage
	events   []DecisionEvent
}

func NewDecisionLedger(id ID, tag identity.TagID) (DecisionLedger, error) {
	if _, err := ParseID(id.String()); err != nil {
		return DecisionLedger{}, err
	}
	if _, err := identity.ParseTagID(tag.String()); err != nil {
		return DecisionLedger{}, ErrInvalidReference
	}
	return DecisionLedger{id: id, tag: tag}, nil
}
func (s DecisionLedger) ID() ID                { return s.id }
func (s DecisionLedger) TagID() identity.TagID { return s.tag }
func (s DecisionLedger) Revision() uint64      { return s.revision }
func (s DecisionLedger) Sealed() bool          { return s.retired }

// Usage charges only this slice's full canonical event (including input/result)
// bytes and event count. It is NOT complete aggregate command/result/index,
// review or future persistence accounting; that owner must add its own charges.
func (s DecisionLedger) Usage() EvidenceUsage    { return s.usage }
func (s DecisionLedger) Events() []DecisionEvent { return append([]DecisionEvent(nil), s.events...) }

// LookupCommand compares complete canonical input, never a digest alone.
// The future application must authenticate before calling this read as well.
func (s DecisionLedger) LookupCommand(c DecisionCommand) (DecisionResult, bool, error) {
	b, err := c.CanonicalBytes()
	if err != nil {
		return DecisionResult{}, false, err
	}
	for _, event := range s.events {
		prior := event.Result.Command
		if prior.input.CommandID != c.input.CommandID {
			continue
		}
		original, err := prior.CanonicalBytes()
		if err != nil {
			return DecisionResult{}, false, err
		}
		if !bytes.Equal(b, original) {
			return DecisionResult{}, false, ErrCommandConflict
		}
		return event.Result, true, nil
	}
	return DecisionResult{}, false, nil
}

func (s DecisionLedger) Admit(c DecisionCommand, refs DecisionReferences, recordedAt time.Time) (DecisionLedger, DecisionResult, error) {
	if r, found, err := s.LookupCommand(c); err != nil || found {
		return s, r, err
	}
	in := c.input
	if s.id.IsZero() || s.tag.IsZero() {
		return s, DecisionResult{}, ErrInvalidReference
	}
	if in.AssignmentID != s.id || in.TagID != s.tag {
		return s, DecisionResult{}, ErrCommandConflict
	}
	if in.ExpectedRevision != s.revision {
		return s, DecisionResult{}, ErrRevisionConflict
	}
	if s.revision >= MaxDecisionRevision {
		return s, DecisionResult{}, ErrRevisionOverflow
	}
	if s.retired {
		return s, DecisionResult{}, ErrDecisionRetired
	}
	if refs.TagID != s.tag || refs.TagRevision != in.ExpectedTagRevision {
		return s, DecisionResult{}, ErrDependencyConflict
	}
	if in.Kind == Assign && (refs.CatalogRevision != in.ExpectedCatalogRevision || refs.Target != in.Target) {
		return s, DecisionResult{}, ErrDependencyConflict
	}
	// D3 requires strictly increasing times across Assign, Unassign and Retire.
	// Equal or earlier instants cannot insert or repair the retained timeline.
	// Every event in this A1c slice is one of those three transition kinds.
	if len(s.events) > 0 && !in.EffectiveAt.After(s.events[len(s.events)-1].Result.Command.input.EffectiveAt) {
		return s, DecisionResult{}, ErrInvalidInterval
	}
	at, err := decisionTime(recordedAt)
	if err != nil {
		return s, DecisionResult{}, err
	}
	r := DecisionResult{Command: c, Revision: s.revision + 1}
	if len(s.events) > 0 {
		r.PreviousCommand = s.events[len(s.events)-1].Result.Command.input.CommandID
	}
	event := DecisionEvent{Result: r, RecordedAt: at}
	n, err := event.CanonicalByteLen()
	if err != nil {
		return s, DecisionResult{}, err
	}
	u, err := s.usage.Prospective(EvidenceDelta{Events: 1, RetainedBytes: n})
	if err != nil {
		return s, DecisionResult{}, err
	}
	// All admission checks precede allocation; no older snapshot is mutated.
	events := make([]DecisionEvent, len(s.events)+1)
	copy(events, s.events)
	events[len(s.events)] = event
	s.events, s.usage, s.revision, s.retired = events, u, r.Revision, in.Kind == Retire
	return s, r, nil
}
