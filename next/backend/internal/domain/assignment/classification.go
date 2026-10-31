package assignment

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// PhysicalClassification describes only reviewed physical Tag evidence.
// Content provenance, protection and descriptive/device metadata are not inputs.
type PhysicalClassification uint8

const (
	Unknown PhysicalClassification = iota
	Original
	CustomCard
	Conflict
)

type ContentProtection uint8

const (
	ProtectionUnknown ContentProtection = iota
	Unprotected
	ProtectedCustom
)

// ContentDimensions carries independent catalog facts, not accepted authorship
// inferred from an unreviewed claim. Public support and custom origin can coexist
// with protection. This value cannot be supplied to a physical review operation.
type ContentDimensions struct {
	publicSupport, customOrigin bool
	protection                  ContentProtection
}

func NewContentDimensions(publicSupport, customOrigin bool, protection ContentProtection) (ContentDimensions, error) {
	if protection > ProtectedCustom {
		return ContentDimensions{}, ErrInvalidEvidence
	}
	return ContentDimensions{publicSupport, customOrigin, protection}, nil
}

func (d ContentDimensions) PublicSupport() bool           { return d.publicSupport }
func (d ContentDimensions) CustomOrigin() bool            { return d.customOrigin }
func (d ContentDimensions) Protection() ContentProtection { return d.protection }

var (
	ErrInvalidEvidence   = errors.New("invalid assignment evidence")
	ErrReviewAuthority   = errors.New("assignment review authority required")
	ErrEvidenceSubject   = errors.New("assignment evidence belongs to another Tag")
	ErrDuplicateEvidence = errors.New("assignment evidence identity already retained")
	ErrDuplicateReview   = errors.New("assignment review command already retained")
	ErrInvalidResolution = errors.New("assignment resolution requires agreeing accepted support")
)

type EvidenceID struct{ value string }
type ReviewID struct{ value string }

func parseReviewIdentity(text, prefix string) (string, error) {
	if len(text) != 30 || !strings.HasPrefix(text, prefix) {
		return "", ErrInvalidID
	}
	if _, err := identity.ParseContentID("cnt_" + text[4:]); err != nil {
		return "", ErrInvalidID
	}
	return text, nil
}

func ParseEvidenceID(text string) (EvidenceID, error) {
	v, err := parseReviewIdentity(text, "evd_")
	return EvidenceID{v}, err
}
func ParseReviewID(text string) (ReviewID, error) {
	v, err := parseReviewIdentity(text, "rev_")
	return ReviewID{v}, err
}
func (id EvidenceID) String() string { return id.value }
func (id EvidenceID) IsZero() bool   { return id.value == "" }
func (id ReviewID) String() string   { return id.value }
func (id ReviewID) IsZero() bool     { return id.value == "" }

// These assertions are descriptive origins, never authenticated manufacturer
// evidence. Review acceptance does not change their kind or source label.
type EvidenceKind uint8

const (
	OperatorAssertion EvidenceKind = iota + 1
	ImportedAssertion
)

type EvidenceDisposition uint8

const (
	PendingEvidence EvidenceDisposition = iota + 1
	DisputedEvidence
	RejectedEvidence
	AcceptedEvidence
)

type Evidence struct {
	id          EvidenceID
	subject     identity.TagID
	kind        EvidenceKind
	source      string
	disposition EvidenceDisposition
}

func safeReviewText(text string, limit int) bool {
	if len(text) == 0 || len(text) > limit || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func NewEvidence(id EvidenceID, subject identity.TagID, kind EvidenceKind, source string, disposition EvidenceDisposition) (Evidence, error) {
	if id.IsZero() || subject.IsZero() || (kind != OperatorAssertion && kind != ImportedAssertion) ||
		disposition < PendingEvidence || disposition > AcceptedEvidence || !safeReviewText(source, MaxReviewReferenceBytes) {
		return Evidence{}, ErrInvalidEvidence
	}
	return Evidence{id, subject, kind, source, disposition}, nil
}

func (e Evidence) ID() EvidenceID                   { return e.id }
func (e Evidence) Subject() identity.TagID          { return e.subject }
func (e Evidence) Kind() EvidenceKind               { return e.kind }
func (e Evidence) Source() string                   { return e.source }
func (e Evidence) Disposition() EvidenceDisposition { return e.disposition }

// OriginalTarget is an explicit tuple, never a filename, catalog order or a
// fallback. Construction checks identities only; existence/version ownership
// must be verified by the future scoped cross-domain service.
type OriginalTarget struct {
	content identity.ContentID
	version identity.ContentVersionID
}

func NewOriginalTarget(content identity.ContentID, version identity.ContentVersionID) (OriginalTarget, error) {
	if content.IsZero() || version.IsZero() {
		return OriginalTarget{}, ErrInvalidReference
	}
	return OriginalTarget{content, version}, nil
}
func (t OriginalTarget) ContentID() identity.ContentID        { return t.content }
func (t OriginalTarget) VersionID() identity.ContentVersionID { return t.version }

type OriginalTargetState uint8

const (
	OriginalTargetUnknown OriginalTargetState = iota
	OriginalTargetKnown
	OriginalTargetConflict
)

type reviewPermission uint8

const (
	reviewClassification reviewPermission = 1 << iota
	reviewOriginalTarget
	reviewResolution
)

// ReviewAuthority is an opaque, process-local core capability. There is no
// exported issuer, string parser or wire reconstruction. A1a intentionally
// cannot activate a public writer. Future A1 core binding owns trusted issuance;
// it must not expose newReviewAuthority to request-controlled actor/permissions.
type ReviewAuthority struct {
	actor       string
	permissions reviewPermission
}

func newReviewAuthority(actor string, permissions reviewPermission) (ReviewAuthority, error) {
	if !safeReviewText(actor, MaxReviewReferenceBytes) || permissions == 0 ||
		permissions & ^(reviewClassification|reviewOriginalTarget|reviewResolution) != 0 {
		return ReviewAuthority{}, ErrReviewAuthority
	}
	return ReviewAuthority{actor, permissions}, nil
}

// ReviewRequest has no actor/capability/source-of-authority field.
type ReviewRequest struct {
	command                          ReviewID
	expectedRevision, policyRevision uint64
	reason                           string
}

func NewReviewRequest(command ReviewID, expectedRevision, policyRevision uint64, reason string) (ReviewRequest, error) {
	if command.IsZero() || policyRevision == 0 || !safeReviewText(reason, MaxReviewReasonBytes) {
		return ReviewRequest{}, ErrInvalidEvidence
	}
	return ReviewRequest{command, expectedRevision, policyRevision, reason}, nil
}

// ReviewProvenance is output evidence. Actor is copied from the capability,
// never from request text. Revision is the newly admitted knowledge revision.
type ReviewProvenance struct {
	Command                                    ReviewID
	Subject                                    identity.TagID
	Actor, Reason                              string
	PolicyRevision, ExpectedRevision, Revision uint64
}

type ReviewKind uint8

const (
	ClassificationClaim ReviewKind = iota + 1
	OriginalTargetClaim
	ClassificationResolution
	OriginalTargetResolution
)

// ReviewRecord is a value snapshot. Selected support is copied on input/output.
// Claims contain one new observation; resolutions retain references and dissent.
type ReviewRecord struct {
	Kind           ReviewKind
	Provenance     ReviewProvenance
	Evidence       Evidence
	Classification PhysicalClassification
	Target         OriginalTarget
	Support        []EvidenceID
}

// TagEvidence is an immutable, single-Tag review aggregate, not an assignment
// decision/projection. Only accepted claims affect its two independent states.
type TagEvidence struct {
	tag            identity.TagID
	revision       uint64
	classification PhysicalClassification
	originalState  OriginalTargetState
	originalTarget OriginalTarget
	usage          EvidenceUsage
	records        []ReviewRecord
}

func NewTagEvidence(tag identity.TagID) (TagEvidence, error) {
	if tag.IsZero() {
		return TagEvidence{}, ErrInvalidReference
	}
	return TagEvidence{tag: tag}, nil
}
func (s TagEvidence) Subject() identity.TagID                { return s.tag }
func (s TagEvidence) Revision() uint64                       { return s.revision }
func (s TagEvidence) Classification() PhysicalClassification { return s.classification }
func (s TagEvidence) OriginalState() OriginalTargetState     { return s.originalState }
func (s TagEvidence) Usage() EvidenceUsage                   { return s.usage }

// OriginalBaseline returns no tuple when unknown or conflicted. It does not
// select an assignment target, even when the review relation is known.
func (s TagEvidence) OriginalBaseline() (OriginalTarget, bool) {
	if s.originalState != OriginalTargetKnown {
		return OriginalTarget{}, false
	}
	return s.originalTarget, true
}

func copyReview(r ReviewRecord) ReviewRecord {
	r.Support = append([]EvidenceID(nil), r.Support...)
	return r
}

func (s TagEvidence) Records() []ReviewRecord {
	result := make([]ReviewRecord, len(s.records))
	for i, r := range s.records {
		result[i] = copyReview(r)
	}
	return result
}

func (s TagEvidence) validateReview(a ReviewAuthority, q ReviewRequest, permission reviewPermission) (ReviewProvenance, error) {
	if !safeReviewText(a.actor, MaxReviewReferenceBytes) || a.permissions&permission != permission {
		return ReviewProvenance{}, ErrReviewAuthority
	}
	if s.tag.IsZero() {
		return ReviewProvenance{}, ErrInvalidReference
	}
	if _, err := NewReviewRequest(q.command, q.expectedRevision, q.policyRevision, q.reason); err != nil {
		return ReviewProvenance{}, err
	}
	if q.expectedRevision != s.revision || s.revision == ^uint64(0) {
		return ReviewProvenance{}, ErrRevisionConflict
	}
	for _, r := range s.records {
		if r.Provenance.Command == q.command {
			return ReviewProvenance{}, ErrDuplicateReview
		}
	}
	return ReviewProvenance{q.command, s.tag, a.actor, q.reason, q.policyRevision, s.revision, s.revision + 1}, nil
}

func (s TagEvidence) validateEvidence(e Evidence) error {
	if _, err := NewEvidence(e.id, e.subject, e.kind, e.source, e.disposition); err != nil {
		return err
	}
	if e.subject != s.tag {
		return ErrEvidenceSubject
	}
	for _, r := range s.records {
		if r.Evidence.id == e.id {
			return ErrDuplicateEvidence
		}
	}
	return nil
}

// reviewRetention reserves a conservative envelope for every primitive record:
// 1024 bytes for fixed IDs, scalar fields, record/index/result framing; six bytes
// per text byte (including worst-case JSON escaping); 256 per support reference.
// No caller supplies a smaller charge. This is NOT A1's canonical wire format.
// Future canonical encoding must check the envelope and charge any additional
// command/result/snapshot material before using the aggregate-wide byte budget.
func reviewRetention(r ReviewRecord) uint64 {
	return 1024 + 6*uint64(len(r.Provenance.Actor)+len(r.Provenance.Reason)+len(r.Evidence.source)) + 256*uint64(len(r.Support))
}

func (s TagEvidence) appendReview(r ReviewRecord, observations uint64) (TagEvidence, error) {
	u, err := s.usage.Prospective(EvidenceDelta{1, observations, uint64(len(r.Support)), reviewRetention(r)})
	if err != nil {
		return s, err
	}
	// Admission precedes both slice copying and state mutation. Existing records
	// and support slices are immutable; no older snapshot aliases a new append.
	records := make([]ReviewRecord, len(s.records)+1)
	copy(records, s.records)
	records[len(s.records)] = copyReview(r)
	selected := records[len(s.records)].Support
	sort.Slice(selected, func(i, j int) bool { return selected[i].value < selected[j].value })
	s.records, s.usage, s.revision = records, u, r.Provenance.Revision
	return s, nil
}

func (s TagEvidence) AdmitClassification(a ReviewAuthority, q ReviewRequest, e Evidence, claim PhysicalClassification) (TagEvidence, error) {
	p, err := s.validateReview(a, q, reviewClassification)
	if err != nil {
		return s, err
	}
	if claim != Original && claim != CustomCard {
		return s, ErrInvalidEvidence
	}
	if err := s.validateEvidence(e); err != nil {
		return s, err
	}
	next, err := s.appendReview(ReviewRecord{Kind: ClassificationClaim, Provenance: p, Evidence: e, Classification: claim, Support: []EvidenceID{e.id}}, 1)
	if err != nil {
		return s, err
	}
	if e.disposition == AcceptedEvidence {
		switch s.classification {
		case Unknown:
			next.classification = claim
		case Original, CustomCard:
			if s.classification != claim {
				next.classification = Conflict
			}
		}
	}
	return next, nil
}

func (s TagEvidence) AdmitOriginalTarget(a ReviewAuthority, q ReviewRequest, e Evidence, target OriginalTarget) (TagEvidence, error) {
	p, err := s.validateReview(a, q, reviewOriginalTarget)
	if err != nil {
		return s, err
	}
	if _, err := NewOriginalTarget(target.content, target.version); err != nil {
		return s, err
	}
	if err := s.validateEvidence(e); err != nil {
		return s, err
	}
	next, err := s.appendReview(ReviewRecord{Kind: OriginalTargetClaim, Provenance: p, Evidence: e, Target: target, Support: []EvidenceID{e.id}}, 1)
	if err != nil {
		return s, err
	}
	if e.disposition == AcceptedEvidence {
		switch s.originalState {
		case OriginalTargetUnknown:
			next.originalState, next.originalTarget = OriginalTargetKnown, target
		case OriginalTargetKnown:
			if s.originalTarget != target {
				next.originalState, next.originalTarget = OriginalTargetConflict, OriginalTarget{}
			}
		}
	}
	return next, nil
}

// agreeingSupport validates a bounded selection before any copy/sort. Evidence
// kind, disposition and claim dimension must agree; request order is irrelevant.
func (s TagEvidence) agreeingSupport(ids []EvidenceID, kind ReviewKind, class PhysicalClassification, target OriginalTarget) ([]EvidenceID, error) {
	if len(ids) == 0 {
		return nil, ErrInvalidResolution
	}
	if uint64(len(ids)) > MaxSelectedSupport {
		return nil, ErrCommandEvidenceLimit
	}
	for i, id := range ids {
		if id.IsZero() {
			return nil, ErrInvalidResolution
		}
		for _, earlier := range ids[:i] {
			if earlier == id {
				return nil, ErrInvalidResolution
			}
		}
		found := false
		for _, r := range s.records {
			if r.Evidence.id == id && r.Kind == kind && r.Evidence.subject == s.tag && r.Evidence.disposition == AcceptedEvidence && r.Classification == class && r.Target == target {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrInvalidResolution
		}
	}
	return ids, nil
}

func (s TagEvidence) ResolveClassification(a ReviewAuthority, q ReviewRequest, claim PhysicalClassification, support []EvidenceID) (TagEvidence, error) {
	p, err := s.validateReview(a, q, reviewClassification|reviewResolution)
	if err != nil {
		return s, err
	}
	if s.classification != Conflict || (claim != Original && claim != CustomCard) {
		return s, ErrInvalidResolution
	}
	ids, err := s.agreeingSupport(support, ClassificationClaim, claim, OriginalTarget{})
	if err != nil {
		return s, err
	}
	next, err := s.appendReview(ReviewRecord{Kind: ClassificationResolution, Provenance: p, Classification: claim, Support: ids}, 0)
	if err != nil {
		return s, err
	}
	next.classification = claim
	return next, nil
}

func (s TagEvidence) ResolveOriginalTarget(a ReviewAuthority, q ReviewRequest, target OriginalTarget, support []EvidenceID) (TagEvidence, error) {
	p, err := s.validateReview(a, q, reviewOriginalTarget|reviewResolution)
	if err != nil {
		return s, err
	}
	if s.originalState != OriginalTargetConflict {
		return s, ErrInvalidResolution
	}
	if _, err := NewOriginalTarget(target.content, target.version); err != nil {
		return s, err
	}
	ids, err := s.agreeingSupport(support, OriginalTargetClaim, Unknown, target)
	if err != nil {
		return s, err
	}
	next, err := s.appendReview(ReviewRecord{Kind: OriginalTargetResolution, Provenance: p, Target: target, Support: ids}, 0)
	if err != nil {
		return s, err
	}
	next.originalState, next.originalTarget = OriginalTargetKnown, target
	return next, nil
}
