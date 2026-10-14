package catalog

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync/atomic"
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
	Adapter        string
	AdapterVersion string
	Scope          string
	Acquisition    AcquisitionClass
	AllowedClaims  []ClaimKey
}

type sourceBinding struct {
	persistenceOnly bool
	revoked         atomic.Bool
	id              SourceID
	revision        RegistrationRevision
	issuer          IssuerPrincipal
	adapter         string
	adapterVersion  string
	scope           string
	acquisition     AcquisitionClass
	claims          uint8
}

type SourceRegistration struct{ binding *sourceBinding }
type IngestCapability struct{ binding *sourceBinding }

type ReviewCapability struct {
	binding  *sourceBinding
	reviewer IssuerPrincipal
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
		id:             id,
		revision:       RegistrationRevision{value: revision},
		issuer:         issuer,
		adapter:        spec.Adapter,
		adapterVersion: spec.AdapterVersion,
		scope:          spec.Scope,
		acquisition:    spec.Acquisition,
		claims:         mask,
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
	if !source.Active() || !source.Allows(key) {
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

// Active reports the current core registration status; retained provenance is
// unchanged by revocation. Revocation is monotonic and safe across capability copies.
func (s SourceRegistration) Active() bool {
	return s.binding != nil && !s.binding.persistenceOnly && !s.binding.revoked.Load()
}

// Revoke is a trusted composition-root operation, never an imported command.
func (s SourceRegistration) Revoke() {
	if s.binding != nil {
		s.binding.revoked.Store(true)
	}
}

// CatalogPersistenceBindings are trusted composition-root handles, never decoded
// from a snapshot. Persistence may reference existing identities but cannot
// grant authority or deserialize any capability. AllowUnbound restores detached
// provenance; trusted handles are required to retain live authority after restart.
type CatalogPersistenceBindings struct {
	// AllowUnbound permits historical provenance reconstruction without trusted
	// live bindings. Such sources are permanently inactive and cannot authorize
	// any capability. A later read may supply exact trusted bindings instead.
	AllowUnbound bool
	Sources      []SourceRegistration
	Namespaces   []CatalogOrderNamespace
	Reviewers    []IssuerPrincipal
}

type catalogPersistenceContext struct {
	allowUnbound bool
	sources      map[string]SourceRegistration
	namespaces   map[[16]byte]CatalogOrderNamespace
	reviewers    map[string]IssuerPrincipal
	liveSource   map[SourceRegistration]SourceRegistration
	liveSpace    map[CatalogOrderNamespace]CatalogOrderNamespace
}

// Historical replay uses isolated, unrevoked copies solely for invariant
// validation. No copy or capability escapes; returned states use the supplied
// live handles and preserve their current revocation and pointer identity.
func newCatalogPersistenceContext(bindings CatalogPersistenceBindings) (*catalogPersistenceContext, error) {
	for _, count := range []int{len(bindings.Sources), len(bindings.Namespaces)} {
		if err := CheckLimit(SourceRegistrations, 0, uint64(count)); err != nil {
			return nil, err
		}
	}
	if err := CheckLimit(RetainedDecisions, 0, uint64(len(bindings.Reviewers))); err != nil {
		return nil, err
	}
	c := &catalogPersistenceContext{
		allowUnbound: bindings.AllowUnbound,
		sources:      make(map[string]SourceRegistration),
		namespaces:   make(map[[16]byte]CatalogOrderNamespace),
		reviewers:    make(map[string]IssuerPrincipal),
		liveSource:   make(map[SourceRegistration]SourceRegistration),
		liveSpace:    make(map[CatalogOrderNamespace]CatalogOrderNamespace),
	}
	for _, source := range bindings.Sources {
		if source.binding == nil || source.ID().IsZero() || source.Revision().IsZero() || source.Issuer().IsZero() {
			return nil, ErrInvalidCatalog
		}
		key := source.ID().String() + source.Revision().String()
		if _, exists := c.sources[key]; exists {
			return nil, ErrInvalidCatalog
		}
		b := source.binding
		copy := SourceRegistration{binding: &sourceBinding{
			id:      b.id, revision: b.revision, issuer: b.issuer,
			adapter: b.adapter, adapterVersion: b.adapterVersion,
			scope:   b.scope, acquisition: b.acquisition, claims: b.claims,
		}}
		c.sources[key] = copy
		c.liveSource[copy] = source
	}
	for _, namespace := range bindings.Namespaces {
		if namespace.binding == nil || namespace.binding.id == [16]byte{} {
			return nil, ErrInvalidCatalog
		}
		if _, exists := c.namespaces[namespace.binding.id]; exists {
			return nil, ErrInvalidCatalog
		}
		source := namespace.Source()
		copySource := c.sources[source.ID().String()+source.Revision().String()]
		if copySource.binding == nil || c.liveSource[copySource] != source {
			return nil, ErrCatalogSourceMismatch
		}
		if err := checkCatalogText(namespace.binding.contract, ReferenceBytes, false); err != nil {
			return nil, err
		}
		copy := CatalogOrderNamespace{binding: &catalogOrderNamespace{
			id: namespace.binding.id, source: copySource, contract: namespace.binding.contract,
		}}
		c.namespaces[copy.binding.id] = copy
		c.liveSpace[copy] = namespace
	}
	for _, reviewer := range bindings.Reviewers {
		if reviewer.IsZero() {
			return nil, ErrInvalidCatalog
		}
		if _, exists := c.reviewers[reviewer.String()]; exists {
			return nil, ErrInvalidCatalog
		}
		c.reviewers[reviewer.String()] = reviewer
	}
	return c, nil
}

type catalogPersistenceDecoder struct {
	raw     []byte
	offset  int
	err     error
	context *catalogPersistenceContext
}

func newCatalogPersistenceDecoder(raw []byte, bindings CatalogPersistenceBindings) (*catalogPersistenceDecoder, error) {
	if err := CheckLimit(RetainedBytes, 0, uint64(len(raw))); err != nil {
		return nil, err
	}
	context, err := newCatalogPersistenceContext(bindings)
	if err != nil {
		return nil, err
	}
	return &catalogPersistenceDecoder{raw: raw, context: context}, nil
}

func (d *catalogPersistenceDecoder) reject(err error) {
	if d.err == nil {
		d.err = err
	}
}

func (d *catalogPersistenceDecoder) take(size uint64) []byte {
	if d.err != nil {
		return nil
	}
	if size > uint64(len(d.raw)-d.offset) {
		d.reject(ErrInvalidCatalog)
		return nil
	}
	value := d.raw[d.offset : d.offset+int(size)]
	d.offset += int(size)
	return value
}

func (d *catalogPersistenceDecoder) number() uint64 {
	raw := d.take(8)
	if len(raw) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(raw)
}

func (d *catalogPersistenceDecoder) text(maximum uint64) string {
	size := d.number()
	if size > maximum {
		d.reject(ErrInvalidCatalog)
		return ""
	}
	return string(d.take(size))
}

func (d *catalogPersistenceDecoder) count(resource Resource) int {
	value := d.number()
	if d.err != nil {
		return 0
	}
	if err := CheckLimit(resource, 0, value); err != nil {
		d.reject(err)
		return 0
	}
	// Every element in these contracts contains at least one uint64 frame.
	if value > uint64(len(d.raw)-d.offset)/8 {
		d.reject(ErrInvalidCatalog)
		return 0
	}
	return int(value)
}

func (d *catalogPersistenceDecoder) boolean() bool {
	value := d.number()
	if value > 1 {
		d.reject(ErrInvalidCatalog)
	}
	return value == 1
}

func persistenceOpaque(text string) ([16]byte, error) {
	var id [16]byte
	if len(text) != 32 {
		return id, ErrInvalidCatalog
	}
	raw, err := hex.DecodeString(text)
	if err != nil || hex.EncodeToString(raw) != text {
		return id, ErrInvalidCatalog
	}
	copy(id[:], raw)
	if id == [16]byte{} {
		return id, ErrInvalidCatalog
	}
	return id, nil
}

// RehydrateCatalogCommandID restores only non-authoritative opaque identity.
func RehydrateCatalogCommandID(text string) (CatalogCommandID, error) {
	id, err := persistenceOpaque(text)
	return CatalogCommandID{value: id}, err
}

func (d *catalogPersistenceDecoder) commandID() CatalogCommandID {
	id, err := RehydrateCatalogCommandID(d.text(32))
	if err != nil {
		d.reject(err)
	}
	return id
}

func (d *catalogPersistenceDecoder) revision() CatalogRevision {
	revision, err := NewCatalogRevision(d.number())
	if err != nil {
		d.reject(err)
	}
	return revision
}

func (d *catalogPersistenceDecoder) source() SourceRegistration {
	start := d.offset
	id, idErr := persistenceOpaque(d.text(32))
	revision, revisionErr := persistenceOpaque(d.text(32))
	issuer, issuerErr := persistenceOpaque(d.text(32))
	if idErr != nil || revisionErr != nil || issuerErr != nil {
		d.reject(ErrInvalidCatalog)
	}
	adapter, adapterVersion, scope := d.text(ReferenceBytes.Maximum()), d.text(ReferenceBytes.Maximum()), d.text(ReferenceBytes.Maximum())
	for _, value := range []string{adapter, adapterVersion, scope} {
		if err := checkCatalogText(value, ReferenceBytes, false); err != nil {
			d.reject(err)
		}
	}
	acquisition, count := d.number(), d.number()
	if acquisition < uint64(OfflinePublic) || acquisition > uint64(BaselineSnapshot) || count > uint64(OwnershipClaimKey) || count == 0 && acquisition != uint64(LocalVerification) {
		d.reject(ErrInvalidCatalog)
	}
	var claims uint8
	for i := uint64(0); i < count && d.err == nil; i++ {
		key := d.number()
		if key < uint64(TitleClaimKey) || key > uint64(OwnershipClaimKey) || acquisition == uint64(LocalVerification) {
			d.reject(ErrInvalidCatalog)
			break
		}
		if (acquisition == uint64(OfflinePublic) || acquisition == uint64(OfflineCustom)) && key == uint64(OwnershipClaimKey) {
			d.reject(ErrCatalogUnauthorized)
			break
		}
		bit := uint8(1 << key)
		if claims&bit != 0 {
			d.reject(ErrInvalidCatalog)
		}
		claims |= bit
	}
	if d.err != nil {
		return SourceRegistration{}
	}
	key := hex.EncodeToString(id[:]) + hex.EncodeToString(revision[:])
	source := d.context.sources[key]
	if source.binding == nil {
		if !d.context.allowUnbound {
			d.reject(ErrCatalogUnauthorized)
			return SourceRegistration{}
		}
		if err := CheckLimit(SourceRegistrations, uint64(len(d.context.sources)), 1); err != nil {
			d.reject(err)
			return SourceRegistration{}
		}
		b := &sourceBinding{
			id:          SourceID{value: id}, revision: RegistrationRevision{value: revision}, issuer: IssuerPrincipal{value: issuer},
			adapter:     adapter, adapterVersion: adapterVersion, scope: scope,
			acquisition: AcquisitionClass(acquisition), claims: claims,
		}
		source = SourceRegistration{binding: b}
		// The detached provenance handle is distinct from the private active copy.
		detached := SourceRegistration{binding: &sourceBinding{
			persistenceOnly: true, id: b.id, revision: b.revision, issuer: b.issuer,
			adapter:         adapter, adapterVersion: adapterVersion, scope: scope,
			acquisition:     b.acquisition, claims: claims,
		}}
		d.context.sources[key] = source
		d.context.liveSource[source] = detached
	}
	e := catalogEncoder{data: make([]byte, 0)}
	e.source(source)
	if string(d.raw[start:d.offset]) != string(e.data) {
		d.reject(ErrCatalogSourceMismatch)
	}
	return source
}

func (d *catalogPersistenceDecoder) reviewer() IssuerPrincipal {
	value := d.text(32)
	reviewer, exists := d.context.reviewers[value]
	if !exists {
		if !d.context.allowUnbound {
			d.reject(ErrCatalogUnauthorized)
		} else {
			id, err := persistenceOpaque(value)
			if err != nil {
				d.reject(err)
			}
			reviewer = IssuerPrincipal{value: id}
		}
	}
	return reviewer
}

func (d *catalogPersistenceDecoder) namespace() CatalogOrderNamespace {
	var id [16]byte
	copy(id[:], d.take(16))
	source := d.source()
	contract := d.text(ReferenceBytes.Maximum())
	if id == [16]byte{} || checkCatalogText(contract, ReferenceBytes, false) != nil {
		d.reject(ErrInvalidCatalog)
	}
	if d.err != nil {
		return CatalogOrderNamespace{}
	}
	namespace := d.context.namespaces[id]
	if namespace.binding == nil {
		if !d.context.allowUnbound {
			d.reject(ErrCatalogUnauthorized)
			return CatalogOrderNamespace{}
		}
		if err := CheckLimit(SourceRegistrations, uint64(len(d.context.namespaces)), 1); err != nil {
			d.reject(err)
			return CatalogOrderNamespace{}
		}
		namespace = CatalogOrderNamespace{binding: &catalogOrderNamespace{id: id, source: source, contract: contract}}
		detached := CatalogOrderNamespace{binding: &catalogOrderNamespace{persistenceOnly: true, id: id, source: d.context.liveSource[source], contract: contract}}
		d.context.namespaces[id] = namespace
		d.context.liveSpace[namespace] = detached
	} else if namespace.Source() != source || namespace.binding.contract != contract {
		d.reject(ErrCatalogSourceMismatch)
	}
	return namespace
}

// PersistenceEncoding is an internal, bounded provenance encoding. It is not an
// import format and contains no capability or live revocation state.
func (s SourceRegistration) PersistenceEncoding() ([]byte, error) {
	if s.binding == nil {
		return nil, ErrInvalidCatalog
	}
	e := catalogEncoder{data: make([]byte, 0)}
	e.source(s)
	return e.data, e.err
}

// RehydrateSourceRegistration only resolves an exact core-provided binding.
// Without a trusted binding, AllowUnbound restores permanently inactive
// provenance only. Text never restores authority.
func RehydrateSourceRegistration(raw []byte, bindings CatalogPersistenceBindings) (SourceRegistration, error) {
	d, err := newCatalogPersistenceDecoder(raw, bindings)
	if err != nil {
		return SourceRegistration{}, err
	}
	source := d.source()
	if d.err != nil {
		return SourceRegistration{}, d.err
	}
	if d.offset != len(raw) {
		return SourceRegistration{}, ErrInvalidCatalog
	}
	return d.context.liveSource[source], nil
}

func (n CatalogOrderNamespace) PersistenceEncoding() ([]byte, error) {
	if n.binding == nil {
		return nil, ErrInvalidCatalog
	}
	e := catalogEncoder{data: make([]byte, 0)}
	e.orderNamespace(n)
	return e.data, e.err
}

// RehydrateCatalogOrderNamespace resolves identity without granting order scope.
func RehydrateCatalogOrderNamespace(raw []byte, bindings CatalogPersistenceBindings) (CatalogOrderNamespace, error) {
	d, err := newCatalogPersistenceDecoder(raw, bindings)
	if err != nil {
		return CatalogOrderNamespace{}, err
	}
	namespace := d.namespace()
	if d.err != nil {
		return CatalogOrderNamespace{}, d.err
	}
	if d.offset != len(raw) {
		return CatalogOrderNamespace{}, ErrInvalidCatalog
	}
	return d.context.liveSpace[namespace], nil
}
