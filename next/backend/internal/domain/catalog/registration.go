package catalog

import (
	"bytes"
	"math"
	"sort"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

// Registration is an internal, explicitly reviewed authoring operation. Its
// expected condition is a closed union; absence is never a CatalogRevision.
type CatalogRegistrationKind uint8

const (
	RegisterContent CatalogRegistrationKind = iota + 1
	RegisterCatalogVersion
)

type CatalogExpectedKind uint8

const (
	CatalogExpectedAbsent CatalogExpectedKind = iota
	CatalogExpectedRevision
)

type CatalogRegistrationSelection struct {
	Key         ClaimKey
	Observation evidence.ID
}

type CatalogRegistrationCommand struct {
	ID           CatalogCommandID
	Kind         CatalogRegistrationKind
	Subject      CatalogSubject
	ExpectedKind CatalogExpectedKind
	Expected     CatalogRevision
	Source       SourceRegistration
	Observations []CatalogObservation
	Selections   []CatalogRegistrationSelection
	Reason       string
	// Original order cannot be supplied by registration. Kept explicit so a
	// caller cannot silently discard an unsupported original-order assertion.
	OriginalOrder VersionOrderEvidence
}

type CatalogRegistrationResult struct {
	Kind              CatalogRegistrationKind
	Command           CatalogCommandID
	Subject           CatalogSubject
	AggregateRevision CatalogRevision
	ReviewRevision    CatalogRevision
	OrderRevision     CatalogRevision
	ProtectedCustom   bool
	Fields            []CatalogRegistrationField
}

type CatalogRegistrationField struct {
	Key   ClaimKey
	Field CatalogField
}

// Context contains the owner-derived resulting order revision and inherited
// protection. The persistence owner supplies it under its aggregate fence.
type CatalogRegistrationContext struct {
	OrderRevision   CatalogRevision
	ProtectedCustom bool
}

type CatalogRegistrationCapability struct {
	operator ReviewCapability
	evidence []ReviewCapability
}

func NewCatalogRegistrationCapability(operator ReviewCapability, reviews []ReviewCapability) (CatalogRegistrationCapability, error) {
	if operator.binding == nil || operator.reviewer.IsZero() || operator.binding.acquisition != CoreOperator || !(SourceRegistration{binding: operator.binding}).Active() {
		return CatalogRegistrationCapability{}, ErrCatalogUnauthorized
	}
	if err := CheckLimit(SourceRegistrations, 0, uint64(len(reviews))); err != nil {
		return CatalogRegistrationCapability{}, err
	}
	seen := make(map[string]bool)
	included := false
	for _, review := range reviews {
		source := SourceRegistration{binding: review.binding}
		if review.binding == nil || review.reviewer != operator.reviewer || !source.Active() {
			return CatalogRegistrationCapability{}, ErrCatalogUnauthorized
		}
		key := source.ID().String() + source.Revision().String()
		if seen[key] {
			return CatalogRegistrationCapability{}, ErrInvalidCatalog
		}
		seen[key] = true
		included = included || review == operator
	}
	if !included {
		return CatalogRegistrationCapability{}, ErrCatalogUnauthorized
	}
	return CatalogRegistrationCapability{operator: operator, evidence: append([]ReviewCapability(nil), reviews...)}, nil
}

func (c CatalogRegistrationCapability) Reviewer() IssuerPrincipal { return c.operator.Reviewer() }

// UsedSources returns only complete bindings actually used by the command.
// Unused grants are deliberately absent from canonical command identity.
func (c CatalogRegistrationCapability) UsedSources(command CatalogRegistrationCommand) ([]SourceRegistration, error) {
	if err := c.Authorize(command); err != nil {
		return nil, err
	}
	used := map[SourceRegistration]bool{command.Source: true}
	for _, observation := range command.Observations {
		used[observation.input.Coordinate.artifact.source] = true
	}
	sources := make([]SourceRegistration, 0, len(used))
	for source := range used {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].ID().String()+sources[i].Revision().String() < sources[j].ID().String()+sources[j].Revision().String()
	})
	return sources, nil
}

func (c CatalogRegistrationCapability) Authorize(command CatalogRegistrationCommand) error {
	operator := SourceRegistration{binding: c.operator.binding}
	if !operator.Active() || operator.Spec().Acquisition != CoreOperator || c.operator.reviewer.IsZero() {
		return ErrCatalogUnauthorized
	}
	key := TitleClaimKey
	if command.Kind == RegisterCatalogVersion {
		key = OwnershipClaimKey
	}
	if err := c.operator.Authorize(command.Source, key); err != nil {
		return err
	}
	if err := CheckLimit(MutationObservations, 0, uint64(len(command.Observations))); err != nil {
		return err
	}
	for _, observation := range command.Observations {
		source := observation.input.Coordinate.artifact.source
		found := false
		for _, review := range c.evidence {
			if review.binding == source.binding {
				if review.reviewer != c.operator.reviewer {
					return ErrCatalogUnauthorized
				}
				if err := review.Authorize(source, observation.input.Claim.key); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			if source.binding == nil || !source.Active() {
				return ErrCatalogUnauthorized
			}
			return ErrCatalogSourceMismatch
		}
		if observation.input.Claim.key == OwnershipClaimKey && source.Spec().Acquisition != CoreOperator {
			return ErrCatalogUnauthorized
		}
	}
	return nil
}

func normalizeRegistration(c CatalogRegistrationCapability, command CatalogRegistrationCommand, validateEvidence bool) (CatalogRegistrationCommand, error) {
	if err := c.Authorize(command); err != nil {
		return CatalogRegistrationCommand{}, err
	}
	if command.ID.IsZero() || command.Subject.content.IsZero() {
		return CatalogRegistrationCommand{}, ErrInvalidCatalog
	}
	switch command.Kind {
	case RegisterContent:
		if !command.Subject.version.IsZero() || command.ExpectedKind != CatalogExpectedAbsent || command.Expected.value != 0 {
			return CatalogRegistrationCommand{}, ErrInvalidCatalog
		}
	case RegisterCatalogVersion:
		if command.Subject.version.IsZero() || command.ExpectedKind != CatalogExpectedRevision || command.Expected.value == 0 || command.Expected.value > math.MaxInt64 {
			return CatalogRegistrationCommand{}, ErrInvalidCatalog
		}
		if command.Expected.value == math.MaxInt64 {
			return CatalogRegistrationCommand{}, ErrCatalogRevisionOverflow
		}
	default:
		return CatalogRegistrationCommand{}, ErrInvalidCatalog
	}
	if command.OriginalOrder != UnknownVersionOrderEvidence() {
		return CatalogRegistrationCommand{}, ErrCatalogUnsupported
	}
	if err := checkCatalogText(command.Reason, TextBytes, false); err != nil {
		return CatalogRegistrationCommand{}, err
	}
	for resource, count := range map[Resource]int{MutationObservations: len(command.Observations), SelectedSupport: len(command.Selections)} {
		if err := CheckLimit(resource, 0, uint64(count)); err != nil {
			return CatalogRegistrationCommand{}, err
		}
	}
	if validateEvidence && (len(command.Observations) == 0 || len(command.Selections) == 0) {
		return CatalogRegistrationCommand{}, ErrCatalogUnsupported
	}
	for i, o := range command.Observations {
		if err := validateCatalogObservation(o); err != nil {
			return CatalogRegistrationCommand{}, err
		}
		if validateEvidence && o.input.Subject != command.Subject {
			return CatalogRegistrationCommand{}, ErrCatalogSubjectMismatch
		}
		coord := o.input.Coordinate
		if validateEvidence && coord.artifact.facts.ID.value == command.ID.value {
			return CatalogRegistrationCommand{}, ErrCatalogCommandConflict
		}
		// Recheck retained coordinates, including same record under another ID.
		if _, err := NewRecordCoordinate(coord.artifact, coord.index, coord.locator, coord.snapshot); err != nil {
			return CatalogRegistrationCommand{}, err
		}
		for _, old := range command.Observations[:i] {
			if old.ID() == o.ID() {
				return CatalogRegistrationCommand{}, ErrInvalidCatalog
			}
			if err := CheckArtifactIdentity(old.input.Coordinate.artifact, coord.artifact); err != nil {
				return CatalogRegistrationCommand{}, err
			}
			a := old.input.Coordinate
			if a.artifact.facts.ID == coord.artifact.facts.ID && a.index == coord.index && a != coord {
				return CatalogRegistrationCommand{}, ErrCatalogObservationConflict
			}
		}
	}
	observations := append([]CatalogObservation(nil), command.Observations...)
	sort.Slice(observations, func(i, j int) bool { return observations[i].ID().String() < observations[j].ID().String() })
	selected := make(map[evidence.ID]bool)
	claims := make(map[ClaimKey]CatalogClaim)
	for _, selection := range command.Selections {
		if selection.Observation.IsZero() || selection.Key < TitleClaimKey || selection.Key > OwnershipClaimKey || selected[selection.Observation] {
			return CatalogRegistrationCommand{}, ErrInvalidCatalog
		}
		selected[selection.Observation] = true
		if !validateEvidence {
			continue
		}
		var found CatalogObservation
		for _, o := range observations {
			if o.ID() == selection.Observation {
				found = o
				break
			}
		}
		if found.ID().IsZero() || found.input.Claim.key != selection.Key {
			return CatalogRegistrationCommand{}, ErrCatalogUnsupported
		}
		claim := found.input.Claim
		if old, ok := claims[selection.Key]; ok && old != claim {
			return CatalogRegistrationCommand{}, ErrCatalogUnsupported
		}
		claims[selection.Key] = claim
	}
	if !validateEvidence {
		// Canonical comparison precedes semantic evidence checks in the owner.
	} else if command.Kind == RegisterContent {
		if claims[TitleClaimKey].key == 0 {
			return CatalogRegistrationCommand{}, ErrCatalogUnsupported
		}
	} else {
		if claims[FingerprintClaimKey].key == 0 || claims[OwnershipClaimKey].owner != command.Subject.content {
			return CatalogRegistrationCommand{}, ErrCatalogUnsupported
		}
		if claims[FingerprintClaimKey].fingerprint.AudioID().Uint64() > math.MaxUint32 {
			return CatalogRegistrationCommand{}, ErrCatalogUnsupported
		}
	}
	selections := append([]CatalogRegistrationSelection(nil), command.Selections...)
	sort.Slice(selections, func(i, j int) bool {
		if selections[i].Key != selections[j].Key {
			return selections[i].Key < selections[j].Key
		}
		return selections[i].Observation.String() < selections[j].Observation.String()
	})
	command.Observations, command.Selections = observations, selections
	return command, nil
}

func (e *catalogEncoder) registration(reviewer IssuerPrincipal, sources []SourceRegistration, command CatalogRegistrationCommand) {
	e.text("pi08-registration/1")
	e.text(CatalogDecisionPolicy)
	e.number(uint64(command.Kind))
	e.text(command.ID.String())
	e.subject(command.Subject)
	e.number(uint64(command.ExpectedKind))
	e.number(command.Expected.value)
	e.source(command.Source)
	e.text(reviewer.String())
	e.number(uint64(len(sources)))
	for _, source := range sources {
		e.source(source)
	}
	e.text(command.Reason)
	e.number(uint64(len(command.Observations)))
	for _, o := range command.Observations {
		e.observation(o)
	}
	e.number(uint64(len(command.Selections)))
	for _, s := range command.Selections {
		e.number(uint64(s.Key))
		e.text(s.Observation.String())
	}
}

func encodeRegistration(c CatalogRegistrationCapability, command CatalogRegistrationCommand) (string, error) {
	sources, err := c.UsedSources(command)
	if err != nil {
		return "", err
	}
	count := catalogEncoder{}
	count.registration(c.Reviewer(), sources, command)
	if count.err != nil {
		return "", count.err
	}
	e := catalogEncoder{data: make([]byte, 0, int(count.size))}
	e.registration(c.Reviewer(), sources, command)
	return string(e.data), e.err
}

// CanonicalEncoding authorizes and validates syntax before encoding. Semantic
// support checks belong to NewRegisteredCatalogReviewState, after the owner's
// canonical key lookup, so changed payloads cannot bypass identity conflict.
func (command CatalogRegistrationCommand) CanonicalEncoding(c CatalogRegistrationCapability) ([]byte, error) {
	normalized, err := normalizeRegistration(c, command, false)
	if err != nil {
		return nil, err
	}
	raw, err := encodeRegistration(c, normalized)
	return []byte(raw), err
}

func (e *catalogEncoder) registrationResult(result CatalogRegistrationResult) {
	e.text("pi08-registration-result/1")
	e.number(uint64(result.Kind))
	e.text(result.Command.String())
	e.subject(result.Subject)
	e.number(result.AggregateRevision.value)
	e.number(result.ReviewRevision.value)
	e.number(result.OrderRevision.value)
	if result.ProtectedCustom {
		e.number(1)
	} else {
		e.number(0)
	}
	e.number(uint64(len(result.Fields)))
	for _, field := range result.Fields {
		e.number(uint64(field.Key))
		e.number(uint64(field.Field.Status))
		e.number(uint64(len(field.Field.Alternatives)))
		for _, alternative := range field.Field.Alternatives {
			e.claim(alternative.Claim)
			e.number(uint64(len(alternative.Support)))
			for _, id := range alternative.Support {
				e.text(id.String())
			}
		}
	}
}

// CanonicalEncoding accepts only results that a registration bootstrap could
// produce. It is safe to use for persisted result comparison.
func (result CatalogRegistrationResult) CanonicalEncoding() ([]byte, error) {
	if err := validateRegistrationResult(result); err != nil {
		return nil, err
	}
	count := catalogEncoder{}
	count.registrationResult(result)
	if count.err != nil {
		return nil, count.err
	}
	e := catalogEncoder{data: make([]byte, 0, int(count.size))}
	e.registrationResult(result)
	return e.data, e.err
}

func validateRegistrationResult(r CatalogRegistrationResult) error {
	if r.Command.IsZero() || r.Subject.content.IsZero() || r.AggregateRevision.value == 0 || r.AggregateRevision.value > math.MaxInt64 || r.ReviewRevision.value != 1 || r.OrderRevision.value == 0 || r.OrderRevision.value > 1025 {
		return ErrInvalidCatalog
	}
	first, last := TitleClaimKey, ArticleClaimKey
	if r.Kind == RegisterContent {
		if !r.Subject.version.IsZero() || r.AggregateRevision.value != 1 || r.OrderRevision.value != 1 {
			return ErrInvalidCatalog
		}
	} else if r.Kind == RegisterCatalogVersion {
		first, last = FingerprintClaimKey, OwnershipClaimKey
		if r.Subject.version.IsZero() || r.AggregateRevision.value < 2 || r.OrderRevision.value < 2 {
			return ErrInvalidCatalog
		}
	} else {
		return ErrInvalidCatalog
	}
	if len(r.Fields) != int(last-first)+1 {
		return ErrInvalidCatalog
	}
	seen := make(map[evidence.ID]bool)
	for i, f := range r.Fields {
		if f.Key != first+ClaimKey(i) {
			return ErrInvalidCatalog
		}
		if f.Field.Status == CatalogFactUnknown && len(f.Field.Alternatives) == 0 && f.Key != TitleClaimKey && r.Kind == RegisterContent {
			continue
		}
		if f.Field.Status != CatalogFactAccepted || len(f.Field.Alternatives) != 1 {
			return ErrInvalidCatalog
		}
		a := f.Field.Alternatives[0]
		if a.Claim.key != f.Key || !a.Claim.validFor(r.Subject) || len(a.Support) == 0 {
			return ErrInvalidCatalog
		}
		if err := CheckLimit(SelectedSupport, uint64(len(seen)), uint64(len(a.Support))); err != nil {
			return err
		}
		for j, id := range a.Support {
			if id.IsZero() || seen[id] || j > 0 && a.Support[j-1].String() >= id.String() {
				return ErrInvalidCatalog
			}
			seen[id] = true
		}
		if f.Key == FingerprintClaimKey && a.Claim.fingerprint.AudioID().Uint64() > math.MaxUint32 {
			return ErrCatalogUnsupported
		}
	}
	return nil
}

type catalogRegistrationBootstrap struct {
	command     CatalogRegistrationCommand
	reviewer    IssuerPrincipal
	canonical   string
	result      CatalogRegistrationResult
	resultBytes string
}

func NewRegisteredCatalogReviewState(c CatalogRegistrationCapability, command CatalogRegistrationCommand, contexts ...CatalogRegistrationContext) (CatalogReviewState, CatalogRegistrationResult, error) {
	command, err := normalizeRegistration(c, command, true)
	if err != nil {
		return CatalogReviewState{}, CatalogRegistrationResult{}, err
	}
	if len(contexts) > 1 {
		return CatalogReviewState{}, CatalogRegistrationResult{}, ErrInvalidCatalog
	}
	context := CatalogRegistrationContext{OrderRevision: CatalogRevision{value: 1}}
	if command.Kind == RegisterCatalogVersion {
		context.OrderRevision.value = 2
	}
	if len(contexts) == 1 {
		context = contexts[0]
	}
	state, _ := NewCatalogReviewState(command.Subject, context.ProtectedCustom)
	state.observations = command.Observations
	state.links = uint64(len(command.Selections))
	for _, o := range command.Observations {
		state.protected = state.protected || o.input.Coordinate.artifact.source.Spec().Acquisition == OfflineCustom
	}
	canonical, err := encodeRegistration(c, command)
	if err != nil {
		return CatalogReviewState{}, CatalogRegistrationResult{}, err
	}
	state.bootstrap = &catalogRegistrationBootstrap{command: command, reviewer: c.Reviewer(), canonical: canonical}
	aggregate := CatalogRevision{value: 1}
	if command.Kind == RegisterCatalogVersion {
		aggregate, _ = command.Expected.next()
	}
	result := CatalogRegistrationResult{Kind: command.Kind, Command: command.ID, Subject: command.Subject, AggregateRevision: aggregate, ReviewRevision: state.revision, OrderRevision: context.OrderRevision, ProtectedCustom: state.protected}
	first, last := TitleClaimKey, ArticleClaimKey
	if command.Kind == RegisterCatalogVersion {
		first, last = FingerprintClaimKey, OwnershipClaimKey
	}
	for key := first; key <= last; key++ {
		result.Fields = append(result.Fields, CatalogRegistrationField{Key: key, Field: state.Field(key)})
	}
	raw, err := result.CanonicalEncoding()
	if err != nil {
		return CatalogReviewState{}, CatalogRegistrationResult{}, err
	}
	state.bootstrap.result = result
	state.bootstrap.resultBytes = string(raw)
	if _, err := state.RetainedEncodingSize(); err != nil {
		return CatalogReviewState{}, CatalogRegistrationResult{}, err
	}
	return state, cloneRegistrationResult(result), nil
}

func cloneRegistrationResult(r CatalogRegistrationResult) CatalogRegistrationResult {
	r.Fields = append([]CatalogRegistrationField(nil), r.Fields...)
	for i := range r.Fields {
		r.Fields[i].Field = cloneDecisionResult(CatalogDecisionResult{Field: r.Fields[i].Field}).Field
	}
	return r
}

func (s CatalogReviewState) LookupRegistration(c CatalogRegistrationCapability, command CatalogRegistrationCommand) (CatalogRegistrationResult, bool, error) {
	raw, err := command.CanonicalEncoding(c)
	if err != nil {
		return CatalogRegistrationResult{}, false, err
	}
	if s.bootstrap == nil || s.bootstrap.command.ID != command.ID {
		return CatalogRegistrationResult{}, false, nil
	}
	if s.bootstrap.canonical != string(raw) {
		return CatalogRegistrationResult{}, false, ErrCatalogCommandConflict
	}
	return cloneRegistrationResult(s.bootstrap.result), true, nil
}

func (s CatalogReviewState) encodeRegisteredReview(e *catalogEncoder, added []CatalogObservation, next *catalogDecision) {
	e.text("pi08-review-state/2")
	e.text(s.bootstrap.canonical)
	e.text(s.bootstrap.resultBytes)
	count := catalogEncoder{}
	s.encodeReviewBody(&count, added, next, "pi08-review-tail/2")
	if count.err != nil {
		e.err = count.err
		return
	}
	e.number(count.size)
	s.encodeReviewBody(e, added, next, "pi08-review-tail/2")
}

// Database-wide collision checking (including batches/adoption/import writers)
// belongs to the single owner in F01-C; domain states fence their own history.
func registrationKeyConflict(id CatalogCommandID, bootstrap *catalogRegistrationBootstrap) bool {
	return bootstrap != nil && bootstrap.command.ID == id
}

func registrationEqual(a, b CatalogRegistrationResult) bool {
	x, err := a.CanonicalEncoding()
	if err != nil {
		return false
	}
	y, err := b.CanonicalEncoding()
	return err == nil && bytes.Equal(x, y)
}

func (b *catalogRegistrationBootstrap) live(c *catalogPersistenceContext) *catalogRegistrationBootstrap {
	copy := *b
	copy.command.Source = c.liveSource[b.command.Source]
	copy.command.Observations = append([]CatalogObservation(nil), b.command.Observations...)
	for i := range copy.command.Observations {
		copy.command.Observations[i] = c.liveObservation(copy.command.Observations[i])
	}
	return &copy
}

func (d *catalogPersistenceDecoder) registrationCommand() (CatalogRegistrationCapability, CatalogRegistrationCommand) {
	if d.text(32) != "pi08-registration/1" || d.text(32) != CatalogDecisionPolicy {
		d.reject(ErrInvalidCatalog)
	}
	kind := d.number()
	if kind < 1 || kind > 2 {
		d.reject(ErrInvalidCatalog)
	}
	command := CatalogRegistrationCommand{Kind: CatalogRegistrationKind(kind), ID: d.commandID(), Subject: d.subject()}
	expected := d.number()
	if expected > 1 {
		d.reject(ErrInvalidCatalog)
	}
	command.ExpectedKind = CatalogExpectedKind(expected)
	command.Expected = CatalogRevision{value: d.number()}
	command.Source = d.source()
	reviewer := d.reviewer()
	var reviews []ReviewCapability
	for count := d.count(SourceRegistrations); count > 0; count-- {
		source := d.source()
		reviews = append(reviews, ReviewCapability{binding: source.binding, reviewer: reviewer})
	}
	command.Reason = d.text(TextBytes.Maximum())
	for count := d.count(MutationObservations); count > 0; count-- {
		command.Observations = append(command.Observations, d.observation())
	}
	for count := d.count(SelectedSupport); count > 0; count-- {
		key := d.number()
		if key < 1 || key > 5 {
			d.reject(ErrInvalidCatalog)
		}
		command.Selections = append(command.Selections, CatalogRegistrationSelection{Key: ClaimKey(key), Observation: d.evidenceID()})
	}
	c, err := NewCatalogRegistrationCapability(ReviewCapability{binding: command.Source.binding, reviewer: reviewer}, reviews)
	if err != nil {
		d.reject(err)
	}
	return c, command
}

func (d *catalogPersistenceDecoder) registrationResult() CatalogRegistrationResult {
	if d.text(40) != "pi08-registration-result/1" {
		d.reject(ErrInvalidCatalog)
	}
	kind := d.number()
	if kind < 1 || kind > 2 {
		d.reject(ErrInvalidCatalog)
	}
	r := CatalogRegistrationResult{Kind: CatalogRegistrationKind(kind), Command: d.commandID(), Subject: d.subject(), AggregateRevision: d.revision(), ReviewRevision: d.revision(), OrderRevision: d.revision(), ProtectedCustom: d.boolean()}
	count := d.number()
	if count > 3 {
		d.reject(ErrInvalidCatalog)
	}
	for ; count > 0 && d.err == nil; count-- {
		key, status, alternatives := d.number(), d.number(), d.number()
		if key < 1 || key > 5 || status > 1 || alternatives > 1 {
			d.reject(ErrInvalidCatalog)
			break
		}
		field := CatalogRegistrationField{Key: ClaimKey(key), Field: CatalogField{Status: CatalogFactStatus(status)}}
		for ; alternatives > 0 && d.err == nil; alternatives-- {
			a := CatalogAlternative{Claim: d.claim()}
			for n := d.count(SelectedSupport); n > 0; n-- {
				a.Support = append(a.Support, d.evidenceID())
			}
			field.Field.Alternatives = append(field.Field.Alternatives, a)
		}
		r.Fields = append(r.Fields, field)
	}
	if err := validateRegistrationResult(r); err != nil {
		d.reject(err)
	}
	return r
}

func rehydrateRegisteredReview(d *catalogPersistenceDecoder, raw []byte) (CatalogReviewState, error) {
	commandRaw, resultRaw, tail := d.text(RetainedBytes.Maximum()), d.text(RetainedBytes.Maximum()), d.text(RetainedBytes.Maximum())
	if d.err != nil {
		return CatalogReviewState{}, d.err
	}
	if d.offset != len(raw) {
		return CatalogReviewState{}, ErrInvalidCatalog
	}
	cd := &catalogPersistenceDecoder{raw: []byte(commandRaw), context: d.context}
	c, command := cd.registrationCommand()
	rd := &catalogPersistenceDecoder{raw: []byte(resultRaw), context: d.context}
	result := rd.registrationResult()
	if cd.err != nil {
		return CatalogReviewState{}, cd.err
	}
	if rd.err != nil {
		return CatalogReviewState{}, rd.err
	}
	if cd.offset != len(cd.raw) || rd.offset != len(rd.raw) {
		return CatalogReviewState{}, ErrInvalidCatalog
	}
	seed, reconstructed, err := NewRegisteredCatalogReviewState(c, command, CatalogRegistrationContext{OrderRevision: result.OrderRevision, ProtectedCustom: result.ProtectedCustom})
	if err != nil {
		return CatalogReviewState{}, err
	}
	if seed.bootstrap.canonical != commandRaw || seed.bootstrap.resultBytes != resultRaw || !registrationEqual(result, reconstructed) {
		return CatalogReviewState{}, ErrInvalidCatalog
	}
	td := &catalogPersistenceDecoder{raw: []byte(tail), context: d.context}
	if td.text(32) != "pi08-review-tail/2" {
		td.reject(ErrInvalidCatalog)
	}
	return rehydrateReviewTail(td, raw, seed)
}

type catalogOrderGrowthEvent struct {
	command   CatalogCommandID
	aggregate CatalogRevision
	version   ContentVersion
	decision  *catalogOrderDecision
}

// AppendRegisteredVersion retains the previous snapshot verbatim. The manifest
// grows without inventing an accepted order or altering an immutable tuple.
func (s CatalogOrderState) AppendRegisteredVersion(command CatalogCommandID, aggregate CatalogRevision, version ContentVersion) (CatalogOrderState, error) {
	fail := func(err error) (CatalogOrderState, error) { return s, err }
	if s.revision.value == 0 || command.IsZero() || aggregate.value < 2 || aggregate.value > math.MaxInt64 {
		return fail(ErrInvalidCatalog)
	}
	if version.ContentID() != s.content {
		return fail(ErrCatalogSubjectMismatch)
	}
	if _, err := NewContentVersion(version.ID(), version.ContentID(), version.Fingerprint(), version.OrderEvidence()); err != nil {
		return fail(ErrInvalidCatalog)
	}
	for _, old := range s.versions {
		if old.ID() == version.ID() {
			return fail(ErrCatalogCommandConflict)
		}
	}
	if version.OrderEvidence() != UnknownVersionOrderEvidence() {
		return fail(ErrCatalogUnsupported)
	}
	if version.Fingerprint().AudioID().Uint64() > math.MaxUint32 {
		return fail(ErrCatalogUnsupported)
	}
	for _, old := range s.decisions {
		if old.command.ID == command {
			return fail(ErrCatalogCommandConflict)
		}
	}
	for _, event := range s.growthEvents {
		if event.decision == nil && (event.command == command || event.aggregate.value >= aggregate.value) {
			return fail(ErrCatalogCommandConflict)
		}
	}
	if err := CheckLimit(RetainedVersions, uint64(len(s.versions)), 1); err != nil {
		return fail(err)
	}
	if err := CheckLimit(RetainedDecisions, s.revision.value-1, 1); err != nil {
		return fail(err)
	}
	base := s.growthBase
	if base == "" {
		raw, err := s.PersistenceEncoding()
		if err != nil {
			return fail(err)
		}
		base = string(raw)
	}
	event := catalogOrderGrowthEvent{command: command, aggregate: aggregate, version: version}
	count := catalogEncoder{}
	s.encodeGrowthState(&count, base, &event)
	if count.err != nil {
		return fail(count.err)
	}
	next := s
	next.growthBase = base
	next.revision.value++
	next.versions = append(append([]ContentVersion(nil), s.versions...), version)
	sortVersions(next.versions)
	next.growthEvents = append(append([]catalogOrderGrowthEvent(nil), s.growthEvents...), event)
	return next, nil
}

func (s CatalogOrderState) encodeGrowthState(e *catalogEncoder, base string, next *catalogOrderGrowthEvent) {
	e.text("pi08-order-state/2")
	e.text(base)
	count := len(s.growthEvents)
	if next != nil {
		count++
	}
	e.number(uint64(count))
	encode := func(event catalogOrderGrowthEvent) {
		if event.decision != nil {
			e.number(2)
			e.text(event.decision.canonical)
			count := catalogEncoder{}
			count.orderResult(event.decision.result)
			if count.err != nil {
				e.err = count.err
				return
			}
			e.number(count.size)
			e.orderResult(event.decision.result)
		} else {
			e.number(1)
			e.text(event.command.String())
			e.number(event.aggregate.value)
			v := event.version
			e.text(v.ID().String())
			e.text(v.ContentID().String())
			e.number(v.Fingerprint().AudioID().Uint64())
			e.text(v.Fingerprint().Hash().String())
			e.text("")
			e.number(0)
			e.number(0)
		}
	}
	for _, event := range s.growthEvents {
		encode(event)
	}
	if next != nil {
		encode(*next)
	}
}

func rehydrateGrowthOrder(d *catalogPersistenceDecoder, raw []byte) (CatalogOrderState, error) {
	base := d.text(RetainedBytes.Maximum())
	probe := &catalogPersistenceDecoder{raw: []byte(base)}
	if probe.text(32) != "pi08-order-state/1" || probe.err != nil {
		return CatalogOrderState{}, ErrInvalidCatalog
	}
	// The base decoder discovers detached historical handles if needed. Reuse
	// its context for every later event so equality remains complete binding.
	bd := &catalogPersistenceDecoder{raw: []byte(base), context: d.context}
	bd.text(32)
	state, err := rehydrateOrderTail(bd, []byte(base), false)
	if err != nil {
		return CatalogOrderState{}, err
	}
	for count := d.count(RetainedDecisions); count > 0 && d.err == nil; count-- {
		switch d.number() {
		case 1:
			command, aggregate, version := d.commandID(), d.revision(), d.version()
			if d.err != nil {
				break
			}
			state, err = state.AppendRegisteredVersion(command, aggregate, version)
		case 2:
			if state.growthBase == "" {
				return CatalogOrderState{}, ErrInvalidCatalog
			}
			commandRaw, resultRaw := d.text(RetainedBytes.Maximum()), d.text(RetainedBytes.Maximum())
			cd := &catalogPersistenceDecoder{raw: []byte(commandRaw), context: d.context}
			reviewer, command := cd.orderCommand()
			rd := &catalogPersistenceDecoder{raw: []byte(resultRaw), context: d.context}
			rd.orderResult()
			if cd.err != nil {
				return CatalogOrderState{}, cd.err
			}
			if rd.err != nil {
				return CatalogOrderState{}, rd.err
			}
			if cd.offset != len(cd.raw) || rd.offset != len(rd.raw) || command.Expected != state.revision {
				return CatalogOrderState{}, ErrInvalidCatalog
			}
			capability := CatalogOrderCapability{namespace: command.Namespace, review: ReviewCapability{binding: command.Namespace.Source().binding, reviewer: reviewer}}
			before := len(state.growthEvents)
			state, _, err = state.Apply(capability, command)
			if err == nil {
				if len(state.growthEvents) != before+1 {
					return CatalogOrderState{}, ErrInvalidCatalog
				}
				event := state.growthEvents[len(state.growthEvents)-1]
				enc := catalogEncoder{data: make([]byte, 0)}
				enc.orderResult(event.decision.result)
				if event.decision.canonical != commandRaw || string(enc.data) != resultRaw {
					return CatalogOrderState{}, ErrInvalidCatalog
				}
			}
		default:
			d.reject(ErrInvalidCatalog)
		}
		if err != nil {
			return CatalogOrderState{}, err
		}
	}
	if d.err != nil {
		return CatalogOrderState{}, d.err
	}
	if d.offset != len(raw) || state.growthBase == "" {
		return CatalogOrderState{}, ErrInvalidCatalog
	}
	encoded, err := state.PersistenceEncoding()
	if err != nil {
		return CatalogOrderState{}, err
	}
	if !bytes.Equal(encoded, raw) {
		return CatalogOrderState{}, ErrInvalidCatalog
	}
	return state.liveOrder(d.context), nil
}

func (s CatalogOrderState) liveOrder(c *catalogPersistenceContext) CatalogOrderState {
	for i := range s.evidence {
		s.evidence[i] = c.liveSequence(s.evidence[i])
	}
	for i := range s.decisions {
		d := &s.decisions[i]
		d.command.Namespace = c.liveSpace[d.command.Namespace]
		for j := range d.command.Evidence {
			d.command.Evidence[j] = c.liveSequence(d.command.Evidence[j])
		}
		for j := range d.result.View.Positions {
			p := &d.result.View.Positions[j]
			p.Namespace = c.liveSpace[p.Namespace]
		}
	}
	// Events retain immutable canonical bytes; only their typed decision handle
	// is rebound, without changing any original result or base snapshot.
	for i := range s.growthEvents {
		if s.growthEvents[i].decision != nil {
			for j := range s.decisions {
				if s.decisions[j].command.ID == s.growthEvents[i].decision.command.ID {
					copy := s.decisions[j]
					s.growthEvents[i].decision = &copy
					break
				}
			}
		}
	}
	return s
}
