package catalog

import (
	"errors"
	"sort"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type BatchID struct{ value [16]byte }

func NewBatchID() (BatchID, error) {
	value, err := newCatalogOpaqueID()
	return BatchID{value: value}, err
}

func (id BatchID) IsZero() bool {
	return id.value == [16]byte{}
}

// ArtifactFacts must come from the core's sanitized-byte admission, never from
// artifact claims. SHA256 identifies admitted bytes, not an authenticated origin.
// No raw artifact bytes/digest may be retained on sensitive-field rejection.
type ArtifactFacts struct {
	ID		BatchID
	SHA256		[32]byte
	ByteLength	uint64
	RecordCount	uint64
	ParserRevision	string
	SourceRevision	string
}

type Artifact struct {
	source	SourceRegistration
	facts	ArtifactFacts
}

func NewArtifact(capability IngestCapability, source SourceRegistration, facts ArtifactFacts) (Artifact, error) {
	if capability.binding == nil || source.binding == nil {
		return Artifact{}, ErrCatalogUnauthorized
	}
	if capability.binding != source.binding {
		return Artifact{}, ErrCatalogSourceMismatch
	}
	if facts.ID.IsZero() || facts.SHA256 == [32]byte{} || facts.ByteLength == 0 || facts.RecordCount == 0 {
		return Artifact{}, ErrInvalidCatalog
	}
	if err := CheckLimit(BatchBytes, 0, facts.ByteLength); err != nil {
		return Artifact{}, err
	}
	if err := CheckLimit(BatchRecords, 0, facts.RecordCount); err != nil {
		return Artifact{}, err
	}
	if err := checkCatalogText(facts.ParserRevision, ReferenceBytes, false); err != nil {
		return Artifact{}, err
	}
	if err := checkCatalogText(facts.SourceRevision, ReferenceBytes, true); err != nil {
		return Artifact{}, err
	}
	return Artifact{source: source, facts: facts}, nil
}

func (a Artifact) Facts() ArtifactFacts {
	return a.facts
}

func (a Artifact) Source() SourceRegistration {
	return a.source
}

type RecordCoordinate struct {
	artifact	Artifact
	index		uint64
	locator		string
	snapshot	RecordSnapshot
}

func NewRecordCoordinate(artifact Artifact, index uint64, locator string, snapshot RecordSnapshot) (RecordCoordinate, error) {
	if artifact.source.binding == nil || index >= artifact.facts.RecordCount || snapshot.raw == "" || snapshot.ByteLength() > artifact.facts.ByteLength {
		return RecordCoordinate{}, ErrInvalidCatalog
	}
	if err := checkCatalogText(locator, ReferenceBytes, false); err != nil {
		return RecordCoordinate{}, err
	}
	return RecordCoordinate{artifact: artifact, index: index, locator: locator, snapshot: snapshot}, nil
}

func (c RecordCoordinate) Artifact() Artifact {
	return c.artifact
}

func (c RecordCoordinate) Index() uint64 {
	return c.index
}

func (c RecordCoordinate) Locator() string {
	return c.locator
}

func (c RecordCoordinate) Snapshot() RecordSnapshot {
	return c.snapshot
}

type CatalogSubject struct {
	content	identity.ContentID
	version	identity.ContentVersionID
}

func ContentSubject(content identity.ContentID) (CatalogSubject, error) {
	if content.IsZero() {
		return CatalogSubject{}, ErrInvalidCatalog
	}
	return CatalogSubject{content: content}, nil
}

func VersionSubject(content identity.ContentID, version identity.ContentVersionID) (CatalogSubject, error) {
	if content.IsZero() || version.IsZero() {
		return CatalogSubject{}, ErrInvalidCatalog
	}
	return CatalogSubject{content: content, version: version}, nil
}

func (s CatalogSubject) ContentID() identity.ContentID {
	return s.content
}

func (s CatalogSubject) VersionID() identity.ContentVersionID {
	return s.version
}

// CatalogClaim is a closed typed union. Fingerprints are inseparable and wider
// uint64 AudioIDs remain candidates; this type makes no durable-width decision.
type CatalogClaim struct {
	key		ClaimKey
	text	string
	fingerprint	AudioFingerprint
	owner		identity.ContentID
}

func TitleClaim(title string) (CatalogClaim, error) {
	if err := checkCatalogText(title, TextBytes, false); err != nil {
		return CatalogClaim{}, err
	}
	return CatalogClaim{key: TitleClaimKey, text: title}, nil
}

func ModelClaim(model ModelNumber) (CatalogClaim, error) {
	if !validProductIdentifier(model.String()) {
		return CatalogClaim{}, ErrInvalidCatalog
	}
	return CatalogClaim{key: ModelClaimKey, text: model.String()}, nil
}

func ArticleClaim(article ArticleNumber) (CatalogClaim, error) {
	if !validProductIdentifier(article.String()) {
		return CatalogClaim{}, ErrInvalidCatalog
	}
	return CatalogClaim{key: ArticleClaimKey, text: article.String()}, nil
}

func FingerprintClaim(fingerprint AudioFingerprint) (CatalogClaim, error) {
	if fingerprint.IsZero() {
		return CatalogClaim{}, ErrInvalidCatalog
	}
	return CatalogClaim{key: FingerprintClaimKey, fingerprint: fingerprint}, nil
}

func OwnershipClaim(owner identity.ContentID) (CatalogClaim, error) {
	if owner.IsZero() {
		return CatalogClaim{}, ErrInvalidCatalog
	}
	return CatalogClaim{key: OwnershipClaimKey, owner: owner}, nil
}

func (c CatalogClaim) Key() ClaimKey {
	return c.key
}

func (c CatalogClaim) Text() string {
	return c.text
}

func (c CatalogClaim) Fingerprint() AudioFingerprint {
	return c.fingerprint
}

func (c CatalogClaim) Owner() identity.ContentID {
	return c.owner
}

func (c CatalogClaim) validFor(subject CatalogSubject) bool {
	if subject.content.IsZero() {
		return false
	}
	switch c.key {
	case TitleClaimKey:
		return subject.version.IsZero() && checkCatalogText(c.text, TextBytes, false) == nil
	case ModelClaimKey, ArticleClaimKey:
		return subject.version.IsZero() && validProductIdentifier(c.text)
	case FingerprintClaimKey:
		return !subject.version.IsZero() && !c.fingerprint.IsZero()
	case OwnershipClaimKey:
		return !subject.version.IsZero() && !c.owner.IsZero() && c.owner == subject.content
	}
	return false
}

type ObservationInput struct {
	ID		evidence.ID
	Subject		CatalogSubject
	Claim		CatalogClaim
	Coordinate	RecordCoordinate
	AssertedOrigin	string
	ObservedAt	time.Time
	Confidence	evidence.Confidence
}

// CatalogObservation is immutable pending evidence. It has no review state or
// accepted projection, and confidence cannot authorize any command.
type CatalogObservation struct{ input ObservationInput }

var ErrCatalogSubjectMismatch = errors.New("catalog subject mismatch")
var ErrCatalogObservationConflict = errors.New("catalog observation identity conflict")
var ErrCatalogArtifactConflict = errors.New("catalog batch identity conflict")

func NewCatalogObservation(capability IngestCapability, source SourceRegistration, input ObservationInput) (CatalogObservation, error) {
	if err := capability.Authorize(source, input.Claim.key); err != nil {
		return CatalogObservation{}, err
	}
	if input.Coordinate.artifact.source != source {
		return CatalogObservation{}, ErrCatalogSourceMismatch
	}
	if !input.Claim.validFor(input.Subject) {
		return CatalogObservation{}, ErrCatalogSubjectMismatch
	}
	if input.ID.IsZero() || input.Coordinate.snapshot.raw == "" || input.ObservedAt.IsZero() || input.Confidence > evidence.Corroborated {
		return CatalogObservation{}, ErrInvalidCatalog
	}
	if err := checkCatalogText(input.AssertedOrigin, ReferenceBytes, true); err != nil {
		return CatalogObservation{}, err
	}
	input.ObservedAt = input.ObservedAt.Round(0).UTC()
	return CatalogObservation{input: input}, nil
}

// Input returns values only; snapshots and source bindings have no mutation API.
func (o CatalogObservation) Input() ObservationInput {
	return o.input
}

func (o CatalogObservation) ID() evidence.ID {
	return o.input.ID
}

func ValidateObservationBinding(observation CatalogObservation, subject CatalogSubject, source SourceRegistration) error {
	if err := validateCatalogObservation(observation); err != nil {
		return err
	}
	if observation.input.Subject != subject {
		return ErrCatalogSubjectMismatch
	}
	if observation.input.Coordinate.artifact.source != source {
		return ErrCatalogSourceMismatch
	}
	return nil
}

func validateCatalogObservation(observation CatalogObservation) error {
	input := observation.input
	source := input.Coordinate.artifact.source
	_, err := NewCatalogObservation(IngestCapability{binding: source.binding}, source, input)
	return err
}

// CheckObservationIdentity reports changed same-ID payloads without accepting,
// preferring or merging claims. Different IDs preserve independent evidence.
func CheckObservationIdentity(left, right CatalogObservation) error {
	if err := validateCatalogObservation(left); err != nil {
		return err
	}
	if err := validateCatalogObservation(right); err != nil {
		return err
	}
	if left.ID() == right.ID() && left != right {
		return ErrCatalogObservationConflict
	}
	return nil
}

func CheckArtifactIdentity(left, right Artifact) error {
	if left.source.binding == nil || right.source.binding == nil {
		return ErrInvalidCatalog
	}
	if left.facts.ID == right.facts.ID && left != right {
		return ErrCatalogArtifactConflict
	}
	return nil
}

// CanonicalCatalogObservations sorts only opaque IDs for deterministic equality.
// It retains dissent under distinct IDs and coalesces only identical payloads.
// The full input count (including duplicates) is checked before any copy.
func CanonicalCatalogObservations(observations []CatalogObservation) ([]CatalogObservation, error) {
	if err := CheckLimit(RetainedObservations, 0, uint64(len(observations))); err != nil {
		return nil, err
	}
	for _, observation := range observations {
		if err := validateCatalogObservation(observation); err != nil {
			return nil, err
		}
	}
	sorted := append([]CatalogObservation(nil), observations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID().String() < sorted[j].ID().String() })
	result := make([]CatalogObservation, 0, len(sorted))
	for _, observation := range sorted {
		if len(result) > 0 && result[len(result)-1].ID() == observation.ID() {
			if err := CheckObservationIdentity(result[len(result)-1], observation); err != nil {
				return nil, err
			}
			continue
		}
		result = append(result, observation)
	}
	return result, nil
}
