package catalog

import (
	"bytes"
	"math"
	"sort"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// CatalogSupersessionCapability is a separate trusted-core grant. Decoded
// provenance, ordinary review handles and relation text never grant it.
type CatalogSupersessionCapability struct{ review ReviewCapability }

func NewCatalogSupersessionCapability(review ReviewCapability) (CatalogSupersessionCapability, error) {
	c := CatalogSupersessionCapability{review: review}
	if err := c.Authorize(SourceRegistration{binding: review.binding}); err != nil {
		return CatalogSupersessionCapability{}, err
	}
	return c, nil
}

func (c CatalogSupersessionCapability) Reviewer() IssuerPrincipal { return c.review.Reviewer() }
func (c CatalogSupersessionCapability) Source() SourceRegistration {
	return SourceRegistration{binding: c.review.binding}
}
func (c CatalogSupersessionCapability) Authorize(source SourceRegistration) error {
	if !c.Source().Active() || c.Source().Spec().Acquisition != CoreOperator {
		return ErrCatalogUnauthorized
	}
	return c.review.Authorize(source, OwnershipClaimKey)
}

type CatalogSupersessionEvidenceInput struct {
	ID                     evidence.ID
	Content                identity.ContentID
	OldVersion, NewVersion identity.ContentVersionID
	Coordinate             RecordCoordinate
	AssertedOrigin         string
	ObservedAt             time.Time
	Confidence             evidence.Confidence
}

type CatalogSupersessionEvidence struct {
	input CatalogSupersessionEvidenceInput
}

func NewCatalogSupersessionEvidence(ingest IngestCapability, source SourceRegistration, input CatalogSupersessionEvidenceInput) (CatalogSupersessionEvidence, error) {
	if source.Spec().Acquisition != CoreOperator {
		return CatalogSupersessionEvidence{}, ErrCatalogUnauthorized
	}
	if err := ingest.Authorize(source, OwnershipClaimKey); err != nil {
		return CatalogSupersessionEvidence{}, err
	}
	if input.Coordinate.artifact.source != source {
		return CatalogSupersessionEvidence{}, ErrCatalogSourceMismatch
	}
	if input.ID.IsZero() || input.Content.IsZero() || input.OldVersion.IsZero() || input.NewVersion.IsZero() || input.OldVersion == input.NewVersion || input.ObservedAt.IsZero() || input.Confidence > evidence.Corroborated {
		return CatalogSupersessionEvidence{}, ErrInvalidCatalog
	}
	if err := checkCatalogText(input.AssertedOrigin, ReferenceBytes, true); err != nil {
		return CatalogSupersessionEvidence{}, err
	}
	if _, err := NewArtifact(ingest, source, input.Coordinate.artifact.facts); err != nil {
		return CatalogSupersessionEvidence{}, err
	}
	if _, err := NewRecordCoordinate(input.Coordinate.artifact, input.Coordinate.index, input.Coordinate.locator, input.Coordinate.snapshot); err != nil {
		return CatalogSupersessionEvidence{}, err
	}
	input.ObservedAt = input.ObservedAt.Round(0).UTC()
	if _, err := time.Parse(time.RFC3339Nano, input.ObservedAt.Format(time.RFC3339Nano)); err != nil {
		return CatalogSupersessionEvidence{}, ErrInvalidCatalog
	}
	return CatalogSupersessionEvidence{input: input}, nil
}

func (e CatalogSupersessionEvidence) Input() CatalogSupersessionEvidenceInput { return e.input }
func (e CatalogSupersessionEvidence) ID() evidence.ID                         { return e.input.ID }

type CatalogSupersessionCommand struct {
	ID                                      CatalogCommandID
	Content                                 identity.ContentID
	Old, New                                ContentVersion
	ExpectedAggregate, ExpectedSupersession CatalogRevision
	ExpectedOldReview, ExpectedNewReview    CatalogRevision
	Source                                  SourceRegistration
	Reason                                  string
	Evidence                                []CatalogSupersessionEvidence
	Support                                 []evidence.ID
}

type CatalogSupersessionResult struct {
	Command                                             CatalogCommandID
	Content                                             identity.ContentID
	Old, New                                            ContentVersion
	AggregateRevision, SupersessionRevision             CatalogRevision
	OldReviewRevision, NewReviewRevision, OrderRevision CatalogRevision
	ProtectedCustom                                     bool
	Support                                             []evidence.ID
}

// Context consists solely of owner-validated current snapshots, not caller
// assertions. Combined persistence accounting and cross-kind keys belong to the
// owner transaction; this state enforces its own retained bounds independently.
type CatalogSupersessionContext struct {
	AggregateRevision    CatalogRevision
	OldReview, NewReview CatalogReviewState
	OrderRevision        CatalogRevision
	ProtectedCustom      bool
}

type CatalogSupersessionEntry struct {
	Command  CatalogSupersessionCommand
	Reviewer IssuerPrincipal
	Result   CatalogSupersessionResult
}

// State owns all slices and retains original results. Manifest extension is
// explicit and cannot remove or replace an existing immutable version tuple.
type CatalogSupersessionState struct {
	content  identity.ContentID
	versions []ContentVersion
	edges    []CatalogSupersessionEntry
}

func NewCatalogSupersessionState(content identity.ContentID, versions []ContentVersion) (CatalogSupersessionState, error) {
	if content.IsZero() {
		return CatalogSupersessionState{}, ErrInvalidCatalog
	}
	if err := CheckLimit(RetainedVersions, 0, uint64(len(versions))); err != nil {
		return CatalogSupersessionState{}, err
	}
	seen := make(map[identity.ContentVersionID]bool)
	for _, v := range versions {
		if err := validateSupersessionVersion(v); err != nil {
			return CatalogSupersessionState{}, err
		}
		if v.contentID != content {
			return CatalogSupersessionState{}, ErrCatalogSubjectMismatch
		}
		if seen[v.id] {
			return CatalogSupersessionState{}, ErrCatalogCommandConflict
		}
		seen[v.id] = true
	}
	copy := append([]ContentVersion(nil), versions...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].id.String() < copy[j].id.String() })
	return CatalogSupersessionState{content: content, versions: copy}, nil
}

func validateSupersessionVersion(v ContentVersion) error {
	if _, err := NewContentVersion(v.id, v.contentID, v.fingerprint, v.order); err != nil {
		return ErrInvalidCatalog
	}
	if v.order.known {
		if _, err := NewVersionOrderEvidence(v.order.namespace, v.order.position); err != nil {
			return ErrInvalidCatalog
		}
	} else if v.order != (VersionOrderEvidence{}) {
		return ErrInvalidCatalog
	}
	return nil
}

func (s CatalogSupersessionState) Content() identity.ContentID { return s.content }
func (s CatalogSupersessionState) Revision() CatalogRevision {
	return CatalogRevision{value: uint64(len(s.edges)) + 1}
}
func (s CatalogSupersessionState) Versions() []ContentVersion {
	return append([]ContentVersion(nil), s.versions...)
}
func (s CatalogSupersessionState) History() []CatalogSupersessionEntry {
	out := make([]CatalogSupersessionEntry, len(s.edges))
	for i, e := range s.edges {
		out[i] = cloneSupersessionEntry(e)
	}
	return out
}

func (s CatalogSupersessionState) WithVersions(versions []ContentVersion) (CatalogSupersessionState, error) {
	if err := s.validate(); err != nil {
		return s, err
	}
	next, err := NewCatalogSupersessionState(s.content, versions)
	if err != nil {
		return s, err
	}
	for _, v := range s.versions {
		if !next.contains(v) {
			return s, ErrVersionIdentityConflict
		}
	}
	next.edges = s.History()
	if _, err := next.RetainedEncodingSize(); err != nil {
		return s, err
	}
	return next, nil
}

func (s CatalogSupersessionState) contains(v ContentVersion) bool {
	for _, stored := range s.versions {
		if stored == v {
			return true
		}
	}
	return false
}

func normalizeSupersession(c CatalogSupersessionCapability, command CatalogSupersessionCommand, historical bool) (CatalogSupersessionCommand, error) {
	if !historical {
		if err := c.Authorize(command.Source); err != nil {
			return CatalogSupersessionCommand{}, err
		}
	}
	if command.Source.binding == nil || command.Source.Spec().Acquisition != CoreOperator || !command.Source.Allows(OwnershipClaimKey) || c.Reviewer().IsZero() {
		return CatalogSupersessionCommand{}, ErrCatalogUnauthorized
	}
	if command.ID.IsZero() || command.Content.IsZero() || command.Old.id == command.New.id {
		return CatalogSupersessionCommand{}, ErrInvalidCatalog
	}
	for _, v := range []ContentVersion{command.Old, command.New} {
		if err := validateSupersessionVersion(v); err != nil {
			return CatalogSupersessionCommand{}, err
		}
		if v.contentID != command.Content {
			return CatalogSupersessionCommand{}, ErrCatalogSubjectMismatch
		}
	}
	for _, r := range []CatalogRevision{command.ExpectedAggregate, command.ExpectedSupersession, command.ExpectedOldReview, command.ExpectedNewReview} {
		if r.value == 0 || r.value > math.MaxInt64 {
			return CatalogSupersessionCommand{}, ErrInvalidCatalog
		}
	}
	if err := checkCatalogText(command.Reason, TextBytes, false); err != nil {
		return CatalogSupersessionCommand{}, err
	}
	for _, n := range []int{len(command.Evidence), len(command.Support)} {
		if err := CheckLimit(MutationObservations, 0, uint64(n)); err != nil {
			return CatalogSupersessionCommand{}, err
		}
	}
	if len(command.Evidence) == 0 || len(command.Support) != len(command.Evidence) {
		return CatalogSupersessionCommand{}, ErrCatalogUnsupported
	}
	for i, e := range command.Evidence {
		input := e.input
		source := input.Coordinate.artifact.source
		if source != command.Source {
			return CatalogSupersessionCommand{}, ErrCatalogSourceMismatch
		}
		if input.Content != command.Content || input.OldVersion != command.Old.id || input.NewVersion != command.New.id {
			return CatalogSupersessionCommand{}, ErrCatalogSubjectMismatch
		}
		if historical {
			// Validate using private unrevoked historical copies, never regranting
			// authority on the retained handle.
			b := source.binding
			active := SourceRegistration{binding: &sourceBinding{id: b.id, revision: b.revision, issuer: b.issuer, adapter: b.adapter, adapterVersion: b.adapterVersion, scope: b.scope, acquisition: b.acquisition, claims: b.claims}}
			historicalInput := input
			historicalInput.Coordinate.artifact.source = active
			if _, err := NewCatalogSupersessionEvidence(IngestCapability{binding: active.binding}, active, historicalInput); err != nil {
				return CatalogSupersessionCommand{}, err
			}
		} else if _, err := NewCatalogSupersessionEvidence(IngestCapability{binding: source.binding}, source, input); err != nil {
			return CatalogSupersessionCommand{}, err
		}
		if input.Coordinate.artifact.facts.ID.value == command.ID.value {
			return CatalogSupersessionCommand{}, ErrCatalogCommandConflict
		}
		for _, before := range command.Evidence[:i] {
			if e.ID() == before.ID() {
				return CatalogSupersessionCommand{}, ErrInvalidCatalog
			}
			if err := checkSupersessionCoordinate(before.input.Coordinate, input.Coordinate); err != nil {
				return CatalogSupersessionCommand{}, err
			}
		}
	}
	command = cloneSupersessionCommand(command)
	sort.Slice(command.Evidence, func(i, j int) bool { return command.Evidence[i].ID().String() < command.Evidence[j].ID().String() })
	sort.Slice(command.Support, func(i, j int) bool { return command.Support[i].String() < command.Support[j].String() })
	for i, id := range command.Support {
		if id.IsZero() || i > 0 && command.Support[i-1] == id {
			return CatalogSupersessionCommand{}, ErrInvalidCatalog
		}
		if id != command.Evidence[i].ID() {
			return CatalogSupersessionCommand{}, ErrCatalogUnsupported
		}
	}
	return command, nil
}

func checkSupersessionCoordinate(a, b RecordCoordinate) error {
	if a.artifact.facts.ID == b.artifact.facts.ID {
		if a.artifact != b.artifact || a.index == b.index && a != b {
			return ErrCatalogObservationConflict
		}
	}
	return nil
}

func cloneSupersessionCommand(c CatalogSupersessionCommand) CatalogSupersessionCommand {
	c.Evidence = append([]CatalogSupersessionEvidence(nil), c.Evidence...)
	c.Support = append([]evidence.ID(nil), c.Support...)
	return c
}
func cloneSupersessionResult(r CatalogSupersessionResult) CatalogSupersessionResult {
	r.Support = append([]evidence.ID(nil), r.Support...)
	return r
}
func cloneSupersessionEntry(e CatalogSupersessionEntry) CatalogSupersessionEntry {
	e.Command = cloneSupersessionCommand(e.Command)
	e.Result = cloneSupersessionResult(e.Result)
	return e
}

func (s CatalogSupersessionState) LookupCommand(c CatalogSupersessionCapability, command CatalogSupersessionCommand) (CatalogSupersessionResult, bool, error) {
	command, err := normalizeSupersession(c, command, false)
	if err != nil {
		return CatalogSupersessionResult{}, false, err
	}
	if err := s.validate(); err != nil {
		return CatalogSupersessionResult{}, false, err
	}
	encoded, err := encodeSupersessionCommand(c.Reviewer(), command)
	if err != nil {
		return CatalogSupersessionResult{}, false, err
	}
	for _, edge := range s.edges {
		if edge.Command.ID == command.ID {
			original, err := encodeSupersessionCommand(edge.Reviewer, edge.Command)
			if err != nil {
				return CatalogSupersessionResult{}, false, err
			}
			if !bytes.Equal(original, encoded) {
				return CatalogSupersessionResult{}, false, ErrCatalogCommandConflict
			}
			return cloneSupersessionResult(edge.Result), true, nil
		}
	}
	return CatalogSupersessionResult{}, false, nil
}

func (s CatalogSupersessionState) Apply(c CatalogSupersessionCapability, command CatalogSupersessionCommand, context CatalogSupersessionContext) (CatalogSupersessionState, CatalogSupersessionResult, error) {
	command, err := normalizeSupersession(c, command, false)
	if err != nil {
		return s, CatalogSupersessionResult{}, err
	}
	result, found, err := s.LookupCommand(c, command)
	if err != nil || found {
		return s, result, err
	}
	if command.Content != s.content {
		return s, CatalogSupersessionResult{}, ErrCatalogSubjectMismatch
	}
	for _, v := range []ContentVersion{command.Old, command.New} {
		if !s.contains(v) {
			return s, CatalogSupersessionResult{}, ErrVersionIdentityConflict
		}
	}
	oldSubject, _ := VersionSubject(s.content, command.Old.id)
	newSubject, _ := VersionSubject(s.content, command.New.id)
	if context.OldReview.subject != oldSubject || context.NewReview.subject != newSubject {
		return s, CatalogSupersessionResult{}, ErrCatalogSubjectMismatch
	}
	if command.ExpectedAggregate != context.AggregateRevision || command.ExpectedSupersession != s.Revision() || command.ExpectedOldReview != context.OldReview.revision || command.ExpectedNewReview != context.NewReview.revision {
		return s, CatalogSupersessionResult{}, ErrCatalogRevisionConflict
	}
	if context.OrderRevision.value == 0 || context.OrderRevision.value > math.MaxInt64 {
		return s, CatalogSupersessionResult{}, ErrInvalidCatalog
	}
	if len(s.edges) > 0 && context.AggregateRevision.value < s.edges[len(s.edges)-1].Result.AggregateRevision.value {
		return s, CatalogSupersessionResult{}, ErrCatalogRevisionConflict
	}
	nextAggregate, err := context.AggregateRevision.next()
	if err != nil {
		return s, CatalogSupersessionResult{}, err
	}
	for key, want := range map[ClaimKey]CatalogClaim{FingerprintClaimKey: {key: FingerprintClaimKey, fingerprint: command.New.fingerprint}, OwnershipClaimKey: {key: OwnershipClaimKey, owner: s.content}} {
		value, ok := context.NewReview.Field(key).Value()
		if !ok || value != want {
			return s, CatalogSupersessionResult{}, ErrCatalogUnsupported
		}
	}
	if err := s.transition(command); err != nil {
		return s, CatalogSupersessionResult{}, err
	}
	result = CatalogSupersessionResult{Command: command.ID, Content: s.content, Old: command.Old, New: command.New, AggregateRevision: nextAggregate, SupersessionRevision: CatalogRevision{value: s.Revision().value + 1}, OldReviewRevision: command.ExpectedOldReview, NewReviewRevision: command.ExpectedNewReview, OrderRevision: context.OrderRevision, ProtectedCustom: context.ProtectedCustom, Support: append([]evidence.ID(nil), command.Support...)}
	next := s
	next.edges = append(s.History(), CatalogSupersessionEntry{Command: command, Reviewer: c.Reviewer(), Result: cloneSupersessionResult(result)})
	if err := next.validate(); err != nil {
		return s, CatalogSupersessionResult{}, err
	}
	if _, err := next.RetainedEncodingSize(); err != nil {
		return s, CatalogSupersessionResult{}, err
	}
	if err := c.Authorize(command.Source); err != nil {
		return s, CatalogSupersessionResult{}, err
	}
	return next, cloneSupersessionResult(result), nil
}

func (s CatalogSupersessionState) transition(c CatalogSupersessionCommand) error {
	for _, e := range s.edges {
		if e.Command.Old.id == c.Old.id || e.Command.Old.id == c.New.id || e.Command.New.id == c.New.id {
			return ErrCatalogCommandConflict
		}
		if e.Command.ID.value == c.ID.value {
			return ErrCatalogCommandConflict
		}
		for _, old := range e.Command.Evidence {
			if old.input.Coordinate.artifact.facts.ID.value == c.ID.value {
				return ErrCatalogCommandConflict
			}
			for _, added := range c.Evidence {
				if old.ID() == added.ID() {
					return ErrCatalogObservationConflict
				}
				if added.input.Coordinate.artifact.facts.ID.value == e.Command.ID.value {
					return ErrCatalogCommandConflict
				}
				if err := checkSupersessionCoordinate(old.input.Coordinate, added.input.Coordinate); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validate replays the entire bounded graph, including for command replay. The
// persistence owner additionally verifies the pinned historical review/order
// snapshots and the shared aggregate event sequence.
func (s CatalogSupersessionState) validate() error {
	base, err := NewCatalogSupersessionState(s.content, s.versions)
	if err != nil {
		return err
	}
	if len(s.edges) > int(RetainedVersions.Maximum())-1 {
		return LimitError{Resource: RetainedDecisions}
	}
	var aggregate, observations, links uint64
	sources := map[string]bool{}
	batches := map[BatchID]Artifact{}
	var staging uint64
	for _, edge := range s.edges {
		cap := CatalogSupersessionCapability{review: ReviewCapability{binding: edge.Command.Source.binding, reviewer: edge.Reviewer}}
		command, err := normalizeSupersession(cap, edge.Command, true)
		if err != nil {
			return err
		}
		original, err := encodeSupersessionCommand(edge.Reviewer, edge.Command)
		if err != nil {
			return err
		}
		canonical, err := encodeSupersessionCommand(edge.Reviewer, command)
		if err != nil {
			return err
		}
		if !bytes.Equal(original, canonical) {
			return ErrInvalidCatalog
		}
		if command.Content != s.content || !base.contains(command.Old) || !base.contains(command.New) {
			return ErrVersionIdentityConflict
		}
		if command.ExpectedSupersession != base.Revision() || command.ExpectedAggregate.value < aggregate {
			return ErrInvalidCatalog
		}
		if err := base.transition(command); err != nil {
			return err
		}
		r := edge.Result
		if command.ExpectedAggregate.value == math.MaxInt64 || r.Command != command.ID || r.Content != s.content || r.Old != command.Old || r.New != command.New || r.AggregateRevision.value != command.ExpectedAggregate.value+1 || r.SupersessionRevision.value != base.Revision().value+1 || r.OldReviewRevision != command.ExpectedOldReview || r.NewReviewRevision != command.ExpectedNewReview || r.OrderRevision.value == 0 || r.OrderRevision.value > math.MaxInt64 || !equalSupersessionSupport(r.Support, command.Support) {
			return ErrInvalidCatalog
		}
		if len(base.edges) > 0 && base.edges[len(base.edges)-1].Result.ProtectedCustom && !r.ProtectedCustom {
			return ErrInvalidCatalog
		}
		if _, err := r.CanonicalEncoding(); err != nil {
			return err
		}
		if _, err := encodeSupersessionCommand(edge.Reviewer, command); err != nil {
			return err
		}
		for _, e := range command.Evidence {
			observations++
			links++
			a := e.input.Coordinate.artifact
			sources[a.source.ID().String()+a.source.Revision().String()] = true
			if _, ok := batches[a.facts.ID]; !ok {
				if err := CheckLimit(StagingBytes, staging, a.facts.ByteLength); err != nil {
					return err
				}
				staging += a.facts.ByteLength
				batches[a.facts.ID] = a
			}
		}
		aggregate = r.AggregateRevision.value
		base.edges = append(base.edges, edge)
	}
	for resource, count := range map[Resource]uint64{RetainedObservations: observations, RetainedSupportLinks: links, SourceRegistrations: uint64(len(sources)), StagingBatches: uint64(len(batches))} {
		if err := CheckLimit(resource, 0, count); err != nil {
			return err
		}
	}
	return nil
}

func equalSupersessionSupport(a, b []evidence.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *catalogEncoder) supersessionVersion(v ContentVersion) {
	e.text(v.id.String())
	e.text(v.contentID.String())
	e.number(v.fingerprint.AudioID().Uint64())
	e.text(v.fingerprint.Hash().String())
	e.text(v.order.namespace)
	e.number(v.order.position)
	if v.order.known {
		e.number(1)
	} else {
		e.number(0)
	}
}
func (e *catalogEncoder) supersessionCoordinate(c RecordCoordinate) {
	f := c.artifact.facts
	e.text(f.ID.String())
	e.raw(string(f.SHA256[:]))
	e.number(f.ByteLength)
	e.number(f.RecordCount)
	e.text(f.ParserRevision)
	e.text(f.SourceRevision)
	e.number(c.index)
	e.text(c.locator)
	e.text(c.snapshot.raw)
}
func (e *catalogEncoder) supersessionCommand(reviewer IssuerPrincipal, c CatalogSupersessionCommand) {
	e.text("pi08-version-supersession/1")
	e.text(CatalogDecisionPolicy)
	e.text(c.ID.String())
	e.text(c.Content.String())
	e.supersessionVersion(c.Old)
	e.supersessionVersion(c.New)
	for _, r := range []CatalogRevision{c.ExpectedAggregate, c.ExpectedSupersession, c.ExpectedOldReview, c.ExpectedNewReview} {
		e.number(r.value)
	}
	e.source(c.Source)
	e.text(reviewer.String())
	e.text(c.Reason)
	e.number(uint64(len(c.Evidence)))
	for _, v := range c.Evidence {
		i := v.input
		e.text(i.ID.String())
		e.text(i.Content.String())
		e.text(i.OldVersion.String())
		e.text(i.NewVersion.String())
		e.source(i.Coordinate.artifact.source)
		e.supersessionCoordinate(i.Coordinate)
		e.text(i.AssertedOrigin)
		e.text(i.ObservedAt.Format(time.RFC3339Nano))
		e.number(uint64(i.Confidence))
	}
	e.number(uint64(len(c.Support)))
	for _, id := range c.Support {
		e.text(id.String())
	}
}

func encodeSupersessionCommand(reviewer IssuerPrincipal, c CatalogSupersessionCommand) ([]byte, error) {
	e := catalogEncoder{}
	e.supersessionCommand(reviewer, c)
	if e.err != nil {
		return nil, e.err
	}
	e = catalogEncoder{data: make([]byte, 0, e.size)}
	e.supersessionCommand(reviewer, c)
	return e.data, e.err
}
func (c CatalogSupersessionCommand) CanonicalEncoding(cap CatalogSupersessionCapability) ([]byte, error) {
	normalized, err := normalizeSupersession(cap, c, false)
	if err != nil {
		return nil, err
	}
	return encodeSupersessionCommand(cap.Reviewer(), normalized)
}

func (e *catalogEncoder) supersessionResult(r CatalogSupersessionResult) {
	e.text("pi08-version-supersession-result/1")
	e.text(r.Command.String())
	e.text(r.Content.String())
	e.supersessionVersion(r.Old)
	e.supersessionVersion(r.New)
	for _, v := range []CatalogRevision{r.AggregateRevision, r.SupersessionRevision, r.OldReviewRevision, r.NewReviewRevision, r.OrderRevision} {
		e.number(v.value)
	}
	if r.ProtectedCustom {
		e.number(1)
	} else {
		e.number(0)
	}
	e.number(uint64(len(r.Support)))
	for _, id := range r.Support {
		e.text(id.String())
	}
}
func (r CatalogSupersessionResult) CanonicalEncoding() ([]byte, error) {
	if r.Command.IsZero() || r.Content.IsZero() || r.Old.id == r.New.id {
		return nil, ErrInvalidCatalog
	}
	for _, v := range []ContentVersion{r.Old, r.New} {
		if err := validateSupersessionVersion(v); err != nil {
			return nil, err
		}
		if v.contentID != r.Content {
			return nil, ErrCatalogSubjectMismatch
		}
	}
	for _, rev := range []CatalogRevision{r.AggregateRevision, r.SupersessionRevision, r.OldReviewRevision, r.NewReviewRevision, r.OrderRevision} {
		if rev.value == 0 || rev.value > math.MaxInt64 {
			return nil, ErrInvalidCatalog
		}
	}
	if r.AggregateRevision.value < 2 || r.SupersessionRevision.value < 2 || r.SupersessionRevision.value > RetainedVersions.Maximum() {
		return nil, ErrInvalidCatalog
	}
	if err := CheckLimit(MutationObservations, 0, uint64(len(r.Support))); err != nil {
		return nil, err
	}
	if len(r.Support) == 0 {
		return nil, ErrCatalogUnsupported
	}
	for i, id := range r.Support {
		if id.IsZero() || i > 0 && r.Support[i-1].String() >= id.String() {
			return nil, ErrInvalidCatalog
		}
	}
	e := catalogEncoder{}
	e.supersessionResult(r)
	if e.err != nil {
		return nil, e.err
	}
	e = catalogEncoder{data: make([]byte, 0, e.size)}
	e.supersessionResult(r)
	return e.data, e.err
}

func (s CatalogSupersessionState) encode(e *catalogEncoder) {
	e.text("pi08-version-supersession-state/1")
	e.text(s.content.String())
	e.number(s.Revision().value)
	e.number(uint64(len(s.versions)))
	for _, v := range s.versions {
		e.supersessionVersion(v)
	}
	e.number(uint64(len(s.edges)))
	for _, edge := range s.edges {
		command, err := encodeSupersessionCommand(edge.Reviewer, edge.Command)
		if err != nil {
			e.err = err
			return
		}
		result, err := edge.Result.CanonicalEncoding()
		if err != nil {
			e.err = err
			return
		}
		e.text(string(command))
		e.text(string(result))
	}
}
func (s CatalogSupersessionState) RetainedEncodingSize() (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	e := catalogEncoder{}
	s.encode(&e)
	return e.size, e.err
}
func (s CatalogSupersessionState) PersistenceEncoding() ([]byte, error) {
	size, err := s.RetainedEncodingSize()
	if err != nil {
		return nil, err
	}
	e := catalogEncoder{data: make([]byte, 0, size)}
	s.encode(&e)
	return e.data, e.err
}

func (d *catalogPersistenceDecoder) supersessionCommand() (IssuerPrincipal, CatalogSupersessionCommand) {
	if d.text(64) != "pi08-version-supersession/1" || d.text(64) != CatalogDecisionPolicy {
		d.reject(ErrInvalidCatalog)
	}
	c := CatalogSupersessionCommand{ID: d.commandID(), Content: d.contentID(), Old: d.version(), New: d.version(), ExpectedAggregate: d.revision(), ExpectedSupersession: d.revision(), ExpectedOldReview: d.revision(), ExpectedNewReview: d.revision(), Source: d.source()}
	reviewer := d.reviewer()
	c.Reason = d.text(TextBytes.Maximum())
	for n := d.count(MutationObservations); n > 0 && d.err == nil; n-- {
		i := CatalogSupersessionEvidenceInput{ID: d.evidenceID(), Content: d.contentID(), OldVersion: d.versionID(), NewVersion: d.versionID()}
		source := d.source()
		i.Coordinate = d.coordinate(source, false)
		i.AssertedOrigin = d.text(ReferenceBytes.Maximum())
		timestamp := d.text(64)
		var err error
		i.ObservedAt, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil || i.ObservedAt.UTC().Format(time.RFC3339Nano) != timestamp {
			d.reject(ErrInvalidCatalog)
		}
		confidence := d.number()
		if confidence > uint64(evidence.Corroborated) {
			d.reject(ErrInvalidCatalog)
		}
		i.Confidence = evidence.Confidence(confidence)
		v, err := NewCatalogSupersessionEvidence(IngestCapability{binding: source.binding}, source, i)
		if err != nil {
			d.reject(err)
		}
		c.Evidence = append(c.Evidence, v)
	}
	for n := d.count(MutationObservations); n > 0 && d.err == nil; n-- {
		c.Support = append(c.Support, d.evidenceID())
	}
	return reviewer, c
}

func (d *catalogPersistenceDecoder) supersessionResult() CatalogSupersessionResult {
	if d.text(64) != "pi08-version-supersession-result/1" {
		d.reject(ErrInvalidCatalog)
	}
	r := CatalogSupersessionResult{Command: d.commandID(), Content: d.contentID(), Old: d.version(), New: d.version(), AggregateRevision: d.revision(), SupersessionRevision: d.revision(), OldReviewRevision: d.revision(), NewReviewRevision: d.revision(), OrderRevision: d.revision(), ProtectedCustom: d.boolean()}
	for n := d.count(MutationObservations); n > 0 && d.err == nil; n-- {
		r.Support = append(r.Support, d.evidenceID())
	}
	return r
}

// RehydrateCatalogSupersessionCommand binds complete canonical provenance to
// supplied trusted handles, or returns detached provenance with AllowUnbound.
// Decoding never constructs a supersession capability.
func RehydrateCatalogSupersessionCommand(raw []byte, bindings CatalogPersistenceBindings) (CatalogSupersessionCommand, IssuerPrincipal, error) {
	d, err := newCatalogPersistenceDecoder(raw, bindings)
	if err != nil {
		return CatalogSupersessionCommand{}, IssuerPrincipal{}, err
	}
	reviewer, c := d.supersessionCommand()
	cap := CatalogSupersessionCapability{review: ReviewCapability{binding: c.Source.binding, reviewer: reviewer}}
	normalized, err := normalizeSupersession(cap, c, true)
	if d.err != nil {
		return CatalogSupersessionCommand{}, IssuerPrincipal{}, d.err
	}
	if err != nil {
		return CatalogSupersessionCommand{}, IssuerPrincipal{}, err
	}
	encoded, err := encodeSupersessionCommand(reviewer, normalized)
	if err != nil {
		return CatalogSupersessionCommand{}, IssuerPrincipal{}, err
	}
	if d.offset != len(raw) || !bytes.Equal(encoded, raw) {
		return CatalogSupersessionCommand{}, IssuerPrincipal{}, ErrInvalidCatalog
	}
	return d.context.liveSupersessionCommand(normalized), reviewer, nil
}

func RehydrateCatalogSupersessionResult(raw []byte) (CatalogSupersessionResult, error) {
	d, err := newCatalogPersistenceDecoder(raw, CatalogPersistenceBindings{})
	if err != nil {
		return CatalogSupersessionResult{}, err
	}
	r := d.supersessionResult()
	if d.err != nil {
		return CatalogSupersessionResult{}, d.err
	}
	encoded, err := r.CanonicalEncoding()
	if err != nil {
		return CatalogSupersessionResult{}, err
	}
	if d.offset != len(raw) || !bytes.Equal(encoded, raw) {
		return CatalogSupersessionResult{}, ErrInvalidCatalog
	}
	return r, nil
}

func RehydrateCatalogSupersessionState(raw []byte, bindings CatalogPersistenceBindings) (CatalogSupersessionState, error) {
	d, err := newCatalogPersistenceDecoder(raw, bindings)
	if err != nil {
		return CatalogSupersessionState{}, err
	}
	if d.text(64) != "pi08-version-supersession-state/1" {
		return CatalogSupersessionState{}, ErrInvalidCatalog
	}
	content, revision := d.contentID(), d.revision()
	var versions []ContentVersion
	for n := d.count(RetainedVersions); n > 0 && d.err == nil; n-- {
		versions = append(versions, d.version())
	}
	state, err := NewCatalogSupersessionState(content, versions)
	if err != nil {
		return CatalogSupersessionState{}, err
	}
	for n := d.count(RetainedDecisions); n > 0 && d.err == nil; n-- {
		commandBytes := d.take(d.number())
		resultBytes := d.take(d.number())
		if d.err != nil {
			break
		}
		// Subdecoders share the historical source registry, so all equal source
		// identities resolve to the same private validation handle.
		cd := catalogPersistenceDecoder{raw: commandBytes, context: d.context}
		reviewer, c := cd.supersessionCommand()
		rd := catalogPersistenceDecoder{raw: resultBytes, context: d.context}
		r := rd.supersessionResult()
		if cd.err != nil {
			d.reject(cd.err)
		}
		if rd.err != nil {
			d.reject(rd.err)
		}
		if cd.offset != len(commandBytes) || rd.offset != len(resultBytes) {
			d.reject(ErrInvalidCatalog)
		}
		state.edges = append(state.edges, CatalogSupersessionEntry{Command: c, Reviewer: reviewer, Result: r})
	}
	if d.err != nil {
		return CatalogSupersessionState{}, d.err
	}
	if d.offset != len(raw) || state.Revision() != revision {
		return CatalogSupersessionState{}, ErrInvalidCatalog
	}
	encoded, err := state.PersistenceEncoding()
	if err != nil {
		return CatalogSupersessionState{}, err
	}
	if !bytes.Equal(encoded, raw) {
		return CatalogSupersessionState{}, ErrInvalidCatalog
	}
	for i := range state.edges {
		state.edges[i].Command = d.context.liveSupersessionCommand(state.edges[i].Command)
	}
	return state, nil
}

func (c *catalogPersistenceContext) liveSupersessionCommand(command CatalogSupersessionCommand) CatalogSupersessionCommand {
	command.Source = c.liveSource[command.Source]
	for i := range command.Evidence {
		v := &command.Evidence[i]
		v.input.Coordinate.artifact.source = c.liveSource[v.input.Coordinate.artifact.source]
	}
	return command
}
