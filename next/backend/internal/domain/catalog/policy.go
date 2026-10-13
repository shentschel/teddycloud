package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// All authority constructors are for trusted core composition only. No text
// parser, JSON codec or imported Source/Confidence can mint these handles.
// Trusted in-process code is assumed; this is not remote authentication.
type SourceID struct{ value [16]byte }
type IssuerPrincipal struct{ value [16]byte }
type RegistrationRevision struct{ value [16]byte }

func NewSourceID() (SourceID, error) {
	value, err := newCatalogOpaqueID()
	return SourceID{value: value}, err
}

func NewIssuerPrincipal() (IssuerPrincipal, error) {
	value, err := newCatalogOpaqueID()
	return IssuerPrincipal{value: value}, err
}

func newCatalogOpaqueID() ([16]byte, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return [16]byte{}, ErrInvalidCatalog
	}
	if value == [16]byte{} {
		return [16]byte{}, ErrInvalidCatalog
	}
	return value, nil
}

func (id SourceID) String() string {
	return hex.EncodeToString(id.value[:])
}

func (id IssuerPrincipal) String() string {
	return hex.EncodeToString(id.value[:])
}

func (id RegistrationRevision) String() string {
	return hex.EncodeToString(id.value[:])
}

func (id SourceID) IsZero() bool {
	return id.value == [16]byte{}
}

func (id IssuerPrincipal) IsZero() bool {
	return id.value == [16]byte{}
}

func (id RegistrationRevision) IsZero() bool {
	return id.value == [16]byte{}
}

type AcquisitionClass uint8

const (
	OfflinePublic AcquisitionClass = iota + 1
	OfflineCustom
	CoreOperator
	LocalVerification
	BaselineSnapshot
)

type ClaimKey uint8

const (
	TitleClaimKey ClaimKey = iota + 1
	ModelClaimKey
	ArticleClaimKey
	FingerprintClaimKey
	OwnershipClaimKey
)

// SourceSpec is trusted configuration. Scope and adapter references are bounded
// opaque text, never paths, accounts to merge, or network addresses to resolve.
type SourceSpec struct {
	Adapter		string
	AdapterVersion	string
	Scope		string
	Acquisition	AcquisitionClass
	AllowedClaims	[]ClaimKey
}

type sourceBinding struct {
	id		SourceID
	revision	RegistrationRevision
	issuer		IssuerPrincipal
	adapter		string
	adapterVersion	string
	scope		string
	acquisition	AcquisitionClass
	claims		uint8
}

type SourceRegistration struct{ binding *sourceBinding }
type IngestCapability struct{ binding *sourceBinding }

type ReviewCapability struct {
	binding		*sourceBinding
	reviewer	IssuerPrincipal
}

var ErrCatalogUnauthorized = errors.New("catalog capability required")
var ErrCatalogSourceMismatch = errors.New("catalog source binding mismatch")

// NewSourceRegistration creates a fresh immutable revision and its ingest
// handle. Reusing a core-created SourceID never rebinds an earlier revision.
// Local verification has no editorial claim keys in this foundation: its
// PI-07 receipt/envelope vocabulary belongs to a later integration checkpoint.
func NewSourceRegistration(id SourceID, issuer IssuerPrincipal, spec SourceSpec) (SourceRegistration, IngestCapability, error) {
	if id.IsZero() || issuer.IsZero() || spec.Acquisition < OfflinePublic || spec.Acquisition > BaselineSnapshot {
		return SourceRegistration{}, IngestCapability{}, ErrInvalidCatalog
	}
	for _, text := range []string{spec.Adapter, spec.AdapterVersion, spec.Scope} {
		if err := checkCatalogText(text, ReferenceBytes, false); err != nil {
			return SourceRegistration{}, IngestCapability{}, err
		}
	}
	if len(spec.AllowedClaims) > int(OwnershipClaimKey) || len(spec.AllowedClaims) == 0 && spec.Acquisition != LocalVerification {
		return SourceRegistration{}, IngestCapability{}, ErrInvalidCatalog
	}
	var mask uint8
	for _, key := range spec.AllowedClaims {
		if key < TitleClaimKey || key > OwnershipClaimKey || spec.Acquisition == LocalVerification {
			return SourceRegistration{}, IngestCapability{}, ErrInvalidCatalog
		}
		if (spec.Acquisition == OfflinePublic || spec.Acquisition == OfflineCustom) && key == OwnershipClaimKey {
			return SourceRegistration{}, IngestCapability{}, ErrCatalogUnauthorized
		}
		bit := uint8(1 << key)
		if mask&bit != 0 {
			return SourceRegistration{}, IngestCapability{}, ErrInvalidCatalog
		}
		mask |= bit
	}
	revision, err := newCatalogOpaqueID()
	if err != nil {
		return SourceRegistration{}, IngestCapability{}, err
	}
	binding := &sourceBinding{
		id:		id,
		revision:	RegistrationRevision{value: revision},
		issuer:		issuer,
		adapter:	spec.Adapter,
		adapterVersion:	spec.AdapterVersion,
		scope:		spec.Scope,
		acquisition:	spec.Acquisition,
		claims:		mask,
	}
	return SourceRegistration{binding: binding}, IngestCapability{binding: binding}, nil
}

// NewReviewCapability is a separate trusted-core grant. Ingest handles,
// imported fields and confidence cannot be converted to review capabilities.
func NewReviewCapability(reviewer IssuerPrincipal, source SourceRegistration) (ReviewCapability, error) {
	if reviewer.IsZero() || source.binding == nil {
		return ReviewCapability{}, ErrInvalidCatalog
	}
	return ReviewCapability{binding: source.binding, reviewer: reviewer}, nil
}

func (c IngestCapability) Authorize(source SourceRegistration, key ClaimKey) error {
	if c.binding == nil || source.binding == nil {
		return ErrCatalogUnauthorized
	}
	if c.binding != source.binding {
		return ErrCatalogSourceMismatch
	}
	if !source.Allows(key) {
		return ErrCatalogUnauthorized
	}
	return nil
}

func (c ReviewCapability) Authorize(source SourceRegistration, key ClaimKey) error {
	if c.reviewer.IsZero() {
		return ErrCatalogUnauthorized
	}
	return (IngestCapability{binding: c.binding}).Authorize(source, key)
}

func (c ReviewCapability) Reviewer() IssuerPrincipal {
	return c.reviewer
}

func (s SourceRegistration) Allows(key ClaimKey) bool {
	return s.binding != nil && key >= TitleClaimKey && key <= OwnershipClaimKey && s.binding.claims&(1<<key) != 0
}

func (s SourceRegistration) ID() SourceID {
	if s.binding == nil {
		return SourceID{}
	}
	return s.binding.id
}

func (s SourceRegistration) Revision() RegistrationRevision {
	if s.binding == nil {
		return RegistrationRevision{}
	}
	return s.binding.revision
}

func (s SourceRegistration) Issuer() IssuerPrincipal {
	if s.binding == nil {
		return IssuerPrincipal{}
	}
	return s.binding.issuer
}

func (s SourceRegistration) Spec() SourceSpec {
	if s.binding == nil {
		return SourceSpec{}
	}
	b := s.binding
	spec := SourceSpec{Adapter: b.adapter, AdapterVersion: b.adapterVersion, Scope: b.scope, Acquisition: b.acquisition}
	for key := TitleClaimKey; key <= OwnershipClaimKey; key++ {
		if s.Allows(key) {
			spec.AllowedClaims = append(spec.AllowedClaims, key)
		}
	}
	return spec
}

// BoundSources validates a whole composition-root allowlist before copying.
// Each retained revision consumes one slot. It grants no ingestion or review.
func BoundSources(sources []SourceRegistration) ([]SourceRegistration, error) {
	if err := CheckLimit(SourceRegistrations, 0, uint64(len(sources))); err != nil {
		return nil, err
	}
	for i, source := range sources {
		if source.binding == nil {
			return nil, ErrInvalidCatalog
		}
		for _, earlier := range sources[:i] {
			if earlier.ID() == source.ID() && earlier.Revision() == source.Revision() {
				return nil, ErrInvalidCatalog
			}
		}
	}
	return append([]SourceRegistration(nil), sources...), nil
}
