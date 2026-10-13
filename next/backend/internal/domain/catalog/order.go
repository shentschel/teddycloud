package catalog

import (
	"encoding/hex"
	"sort"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// CatalogOrderNamespace is a trusted-core contract identity, not imported text.
// Equal labels, even on the same source, do not merge independently granted scopes.
type CatalogOrderNamespace struct{ binding *catalogOrderNamespace }

type catalogOrderNamespace struct {
	id       [16]byte
	source   SourceRegistration
	contract string
}

func NewCatalogOrderNamespace(source SourceRegistration, contract string) (CatalogOrderNamespace, error) {
	if !source.Active() {
		return CatalogOrderNamespace{}, ErrCatalogUnauthorized
	}
	if err := checkCatalogText(contract, ReferenceBytes, false); err != nil {
		return CatalogOrderNamespace{}, err
	}
	id, err := newCatalogOpaqueID()
	if err != nil {
		return CatalogOrderNamespace{}, err
	}
	return CatalogOrderNamespace{binding: &catalogOrderNamespace{id: id, source: source, contract: contract}}, nil
}

func (n CatalogOrderNamespace) Source() SourceRegistration {
	if n.binding == nil {
		return SourceRegistration{}
	}
	return n.binding.source
}

// This separate trusted-core grant authorizes sequence review in exactly one
// namespace. Editorial claim permissions do not implicitly grant order authority.
type CatalogOrderCapability struct {
	namespace CatalogOrderNamespace
	review    ReviewCapability
}

func NewCatalogOrderCapability(review ReviewCapability, namespace CatalogOrderNamespace) (CatalogOrderCapability, error) {
	capability := CatalogOrderCapability{namespace: namespace, review: review}
	if err := capability.authorize(namespace); err != nil {
		return CatalogOrderCapability{}, err
	}
	return capability, nil
}

func (c CatalogOrderCapability) authorize(namespace CatalogOrderNamespace) error {
	if namespace.binding == nil || c.namespace.binding == nil || c.review.reviewer.IsZero() || c.review.binding == nil {
		return ErrCatalogUnauthorized
	}
	if c.namespace != namespace || c.review.binding != namespace.Source().binding {
		return ErrCatalogSourceMismatch
	}
	if !namespace.Source().Active() {
		return ErrCatalogUnauthorized
	}
	return nil
}

// Sequence input is explicit position evidence. No timestamp, AudioID, source
// revision ordering or legacy ContentVersion order is inferred by this domain.
type CatalogSequenceInput struct {
	ID         evidence.ID
	Content    identity.ContentID
	Version    identity.ContentVersionID
	Position   uint64
	Coordinate RecordCoordinate
}

type CatalogSequenceEvidence struct {
	namespace CatalogOrderNamespace
	input     CatalogSequenceInput
}

func NewCatalogSequenceEvidence(ingest IngestCapability, namespace CatalogOrderNamespace, input CatalogSequenceInput) (CatalogSequenceEvidence, error) {
	if namespace.binding == nil || ingest.binding == nil || !namespace.Source().Active() {
		return CatalogSequenceEvidence{}, ErrCatalogUnauthorized
	}
	if ingest.binding != namespace.Source().binding || input.Coordinate.artifact.source != namespace.Source() {
		return CatalogSequenceEvidence{}, ErrCatalogSourceMismatch
	}
	if input.ID.IsZero() || input.Content.IsZero() || input.Version.IsZero() || input.Coordinate.snapshot.raw == "" {
		return CatalogSequenceEvidence{}, ErrInvalidCatalog
	}
	return CatalogSequenceEvidence{namespace: namespace, input: input}, nil
}

func (e CatalogSequenceEvidence) Input() CatalogSequenceInput { return e.input }

type CatalogOrderEntry struct {
	Version  identity.ContentVersionID
	Position uint64
	Support  []evidence.ID
}

type CatalogOrderCommand struct {
	ID         CatalogCommandID
	Content    identity.ContentID
	Namespace  CatalogOrderNamespace
	Expected   CatalogRevision
	Entries    []CatalogOrderEntry
	Evidence   []CatalogSequenceEvidence
	Supersedes []CatalogCommandID
	Reason     string
}

type CatalogEffectivePosition struct {
	Namespace CatalogOrderNamespace
	Entry     CatalogOrderEntry
}

type CatalogEffectiveOrder struct {
	Revision  CatalogRevision
	Status    CatalogFactStatus
	Positions []CatalogEffectivePosition
}

type CatalogOrderResult struct {
	Command CatalogCommandID
	View    CatalogEffectiveOrder
}

type catalogOrderDecision struct {
	command   CatalogOrderCommand
	reviewer  IssuerPrincipal
	canonical string
	result    CatalogOrderResult
}

// This immutable view has its own revision and never writes ContentVersion.
// A1.3/A2 must atomically fence this successor with the owning Content aggregate.
type CatalogOrderState struct {
	content   identity.ContentID
	revision  CatalogRevision
	versions  []ContentVersion
	evidence  []CatalogSequenceEvidence
	decisions []catalogOrderDecision
	links     uint64
}

func NewCatalogOrderState(content identity.ContentID, versions []ContentVersion) (CatalogOrderState, error) {
	if content.IsZero() {
		return CatalogOrderState{}, ErrInvalidCatalog
	}
	if err := CheckLimit(RetainedVersions, 0, uint64(len(versions))); err != nil {
		return CatalogOrderState{}, err
	}
	for _, version := range versions {
		if version.ContentID() != content {
			return CatalogOrderState{}, ErrCatalogSubjectMismatch
		}
		if _, err := NewContentVersion(version.ID(), content, version.Fingerprint(), version.OrderEvidence()); err != nil {
			return CatalogOrderState{}, err
		}
	}
	canonical := append([]ContentVersion(nil), versions...)
	sortVersions(canonical)
	unique := canonical[:0]
	for _, version := range canonical {
		if len(unique) > 0 && unique[len(unique)-1].ID() == version.ID() {
			if unique[len(unique)-1] != version {
				return CatalogOrderState{}, ErrVersionIdentityConflict
			}
			continue
		}
		unique = append(unique, version)
	}
	return CatalogOrderState{content: content, revision: CatalogRevision{value: 1}, versions: unique}, nil
}

func (s CatalogOrderState) Revision() CatalogRevision { return s.revision }

func (s CatalogOrderState) History() []CatalogCommandID {
	ids := make([]CatalogCommandID, 0, len(s.decisions))
	for _, decision := range s.decisions {
		ids = append(ids, decision.command.ID)
	}
	return ids
}

func (s CatalogOrderState) active(next *catalogOrderDecision) []catalogOrderDecision {
	all := s.decisions
	if next != nil {
		all = append(append([]catalogOrderDecision(nil), all...), *next)
	}
	superseded := make(map[CatalogCommandID]bool)
	for _, decision := range all {
		for _, id := range decision.command.Supersedes {
			superseded[id] = true
		}
	}
	var active []catalogOrderDecision
	for _, decision := range all {
		if !superseded[decision.command.ID] {
			active = append(active, decision)
		}
	}
	return active
}

// EffectiveOrder returns all active alternatives, with no latest/default value.
func (s CatalogOrderState) EffectiveOrder() CatalogEffectiveOrder {
	view, _ := s.effective(nil)
	return view
}

func (s CatalogOrderState) effective(next *catalogOrderDecision) (CatalogEffectiveOrder, error) {
	view := CatalogEffectiveOrder{Revision: s.revision}
	if next != nil {
		view.Revision = next.result.View.Revision
	}
	type positionKey struct {
		namespace CatalogOrderNamespace
		version   identity.ContentVersionID
		position  uint64
	}
	positions := make(map[positionKey]map[evidence.ID]bool)
	for _, decision := range s.active(next) {
		for _, entry := range decision.command.Entries {
			key := positionKey{decision.command.Namespace, entry.Version, entry.Position}
			if positions[key] == nil {
				if err := CheckLimit(RetainedVersions, uint64(len(positions)), 1); err != nil {
					return CatalogEffectiveOrder{}, err
				}
				positions[key] = make(map[evidence.ID]bool)
			}
			for _, id := range entry.Support {
				positions[key][id] = true
			}
		}
	}
	seenVersions := make(map[identity.ContentVersionID]bool)
	seenPositions := make(map[uint64]identity.ContentVersionID)
	var namespace CatalogOrderNamespace
	conflict := false
	for key, ids := range positions {
		if namespace.binding != nil && namespace != key.namespace || seenVersions[key.version] {
			conflict = true
		}
		if old, ok := seenPositions[key.position]; ok && old != key.version {
			conflict = true
		}
		namespace = key.namespace
		seenVersions[key.version] = true
		seenPositions[key.position] = key.version
		entry := CatalogOrderEntry{Version: key.version, Position: key.position}
		for id := range ids {
			entry.Support = append(entry.Support, id)
		}
		sort.Slice(entry.Support, func(i, j int) bool { return entry.Support[i].String() < entry.Support[j].String() })
		view.Positions = append(view.Positions, CatalogEffectivePosition{Namespace: key.namespace, Entry: entry})
	}
	sort.Slice(view.Positions, func(i, j int) bool {
		a, b := view.Positions[i], view.Positions[j]
		if a.Entry.Version != b.Entry.Version {
			return a.Entry.Version.String() < b.Entry.Version.String()
		}
		if a.Namespace != b.Namespace {
			return hex.EncodeToString(a.Namespace.binding.id[:]) < hex.EncodeToString(b.Namespace.binding.id[:])
		}
		return a.Entry.Position < b.Entry.Position
	})
	if conflict {
		view.Status = CatalogFactConflict
	} else if len(seenVersions) > 0 && len(seenVersions) == len(s.versions) {
		view.Status = CatalogFactAccepted
	}
	return view, nil
}

func (s CatalogOrderState) Compare(left, right ContentVersion) VersionOrdering {
	knownLeft, knownRight := false, false
	for _, version := range s.versions {
		if version.ID() == left.ID() {
			if version != left {
				return VersionOrderingConflict
			}
			knownLeft = true
		}
		if version.ID() == right.ID() {
			if version != right {
				return VersionOrderingConflict
			}
			knownRight = true
		}
	}
	if !knownLeft || !knownRight {
		return VersionOrderingUnknown
	}
	if left.ID() == right.ID() {
		return VersionOrderingSame
	}
	view := s.EffectiveOrder()
	if view.Status == CatalogFactConflict {
		return VersionOrderingConflict
	}
	var a, b *CatalogEffectivePosition
	for i := range view.Positions {
		position := &view.Positions[i]
		if position.Entry.Version == left.ID() {
			a = position
		}
		if position.Entry.Version == right.ID() {
			b = position
		}
	}
	if a == nil || b == nil {
		return VersionOrderingUnknown
	}
	if a.Entry.Position < b.Entry.Position {
		return VersionOrderingBefore
	}
	return VersionOrderingAfter
}

func normalizeOrderCommand(capability CatalogOrderCapability, command CatalogOrderCommand) (CatalogOrderCommand, error) {
	if err := capability.authorize(command.Namespace); err != nil {
		return CatalogOrderCommand{}, err
	}
	if command.ID.IsZero() || command.Content.IsZero() {
		return CatalogOrderCommand{}, ErrInvalidCatalog
	}
	if _, err := NewCatalogRevision(command.Expected.value); err != nil {
		return CatalogOrderCommand{}, err
	}
	if err := checkCatalogText(command.Reason, TextBytes, false); err != nil {
		return CatalogOrderCommand{}, err
	}
	if len(command.Entries) == 0 {
		return CatalogOrderCommand{}, ErrCatalogUnsupported
	}
	for resource, count := range map[Resource]int{RetainedVersions: len(command.Entries), MutationObservations: len(command.Evidence), SelectedSupport: len(command.Supersedes)} {
		if err := CheckLimit(resource, 0, uint64(count)); err != nil {
			return CatalogOrderCommand{}, err
		}
	}
	links := uint64(len(command.Supersedes))
	for _, entry := range command.Entries {
		if entry.Version.IsZero() || len(entry.Support) == 0 {
			return CatalogOrderCommand{}, ErrCatalogUnsupported
		}
		if err := CheckLimit(SelectedSupport, links, uint64(len(entry.Support))); err != nil {
			return CatalogOrderCommand{}, err
		}
		links += uint64(len(entry.Support))
		for _, id := range entry.Support {
			if id.IsZero() {
				return CatalogOrderCommand{}, ErrInvalidCatalog
			}
		}
	}
	for _, observation := range command.Evidence {
		if observation.namespace != command.Namespace {
			return CatalogOrderCommand{}, ErrCatalogSourceMismatch
		}
		if observation.input.Content != command.Content {
			return CatalogOrderCommand{}, ErrCatalogSubjectMismatch
		}
		if _, err := NewCatalogSequenceEvidence(IngestCapability{binding: command.Namespace.Source().binding}, command.Namespace, observation.input); err != nil {
			return CatalogOrderCommand{}, err
		}
	}
	entries := append([]CatalogOrderEntry(nil), command.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Version.String() < entries[j].Version.String() })
	for i := range entries {
		if i > 0 && entries[i-1].Version == entries[i].Version {
			return CatalogOrderCommand{}, ErrInvalidCatalog
		}
		ids := append([]evidence.ID(nil), entries[i].Support...)
		sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
		entries[i].Support = ids[:0]
		for _, id := range ids {
			if len(entries[i].Support) == 0 || entries[i].Support[len(entries[i].Support)-1] != id {
				entries[i].Support = append(entries[i].Support, id)
			}
		}
	}
	observations := append([]CatalogSequenceEvidence(nil), command.Evidence...)
	sort.Slice(observations, func(i, j int) bool { return observations[i].input.ID.String() < observations[j].input.ID.String() })
	unique := observations[:0]
	for _, observation := range observations {
		if len(unique) > 0 && unique[len(unique)-1].input.ID == observation.input.ID {
			if unique[len(unique)-1] != observation {
				return CatalogOrderCommand{}, ErrCatalogObservationConflict
			}
			continue
		}
		unique = append(unique, observation)
	}
	supersedes := append([]CatalogCommandID(nil), command.Supersedes...)
	sort.Slice(supersedes, func(i, j int) bool { return supersedes[i].String() < supersedes[j].String() })
	for i, id := range supersedes {
		if id.IsZero() || i > 0 && supersedes[i-1] == id {
			return CatalogOrderCommand{}, ErrInvalidCatalog
		}
	}
	command.Entries, command.Evidence, command.Supersedes = entries, unique, supersedes
	return command, nil
}

func (s CatalogOrderState) Apply(capability CatalogOrderCapability, command CatalogOrderCommand) (CatalogOrderState, CatalogOrderResult, error) {
	fail := func(err error) (CatalogOrderState, CatalogOrderResult, error) { return s, CatalogOrderResult{}, err }
	if s.revision.value == 0 {
		return fail(ErrInvalidCatalog)
	}
	if command.Content != s.content {
		return fail(ErrCatalogSubjectMismatch)
	}
	command, err := normalizeOrderCommand(capability, command)
	if err != nil {
		return fail(err)
	}
	canonical, err := canonicalOrderCommand(capability.review.Reviewer(), command)
	if err != nil {
		return fail(err)
	}
	for _, old := range s.decisions {
		if old.command.ID == command.ID {
			if old.canonical != canonical {
				return fail(ErrCatalogCommandConflict)
			}
			return s, cloneOrderResult(old.result), nil
		}
	}
	if command.Expected != s.revision {
		return fail(ErrCatalogRevisionConflict)
	}
	revision, err := s.revision.next()
	if err != nil {
		return fail(err)
	}
	if err := CheckLimit(RetainedDecisions, uint64(len(s.decisions)), 1); err != nil {
		return fail(err)
	}
	for _, id := range command.Supersedes {
		found := false
		for _, old := range s.active(nil) {
			if old.command.ID == id {
				if old.command.Namespace != command.Namespace {
					return fail(ErrCatalogSourceMismatch)
				}
				found = true
			}
		}
		if !found {
			return fail(ErrCatalogUnsupported)
		}
	}
	added := make([]CatalogSequenceEvidence, 0, len(command.Evidence))
	for _, observation := range command.Evidence {
		exists := false
		for _, old := range s.evidence {
			if err := CheckArtifactIdentity(old.input.Coordinate.artifact, observation.input.Coordinate.artifact); err != nil {
				return fail(err)
			}
			if old.input.ID == observation.input.ID {
				if old != observation {
					return fail(ErrCatalogObservationConflict)
				}
				exists = true
			}
		}
		for _, other := range added {
			if err := CheckArtifactIdentity(other.input.Coordinate.artifact, observation.input.Coordinate.artifact); err != nil {
				return fail(err)
			}
		}
		if !exists {
			added = append(added, observation)
		}
	}
	if err := CheckLimit(RetainedObservations, uint64(len(s.evidence)), uint64(len(added))); err != nil {
		return fail(err)
	}
	used := make(map[evidence.ID]bool)
	links := uint64(len(command.Supersedes))
	for _, entry := range command.Entries {
		known := false
		for _, version := range s.versions {
			known = known || version.ID() == entry.Version
		}
		if !known {
			return fail(ErrCatalogSubjectMismatch)
		}
		for _, id := range entry.Support {
			found := false
			for _, observations := range [][]CatalogSequenceEvidence{s.evidence, added} {
				for _, observation := range observations {
					if observation.input.ID == id {
						if observation.namespace != command.Namespace {
							return fail(ErrCatalogSourceMismatch)
						}
						if observation.input.Content != command.Content || observation.input.Version != entry.Version || observation.input.Position != entry.Position {
							return fail(ErrCatalogUnsupported)
						}
						found = true
					}
				}
			}
			if !found {
				return fail(ErrCatalogUnsupported)
			}
			used[id] = true
		}
		links += uint64(len(entry.Support))
	}
	for _, observation := range command.Evidence {
		if !used[observation.input.ID] {
			return fail(ErrCatalogUnsupported)
		}
	}
	if err := CheckLimit(RetainedSupportLinks, s.links, links); err != nil {
		return fail(err)
	}
	namespaces := map[CatalogOrderNamespace]bool{command.Namespace: true}
	for _, old := range s.decisions {
		namespaces[old.command.Namespace] = true
	}
	if err := CheckLimit(SourceRegistrations, 0, uint64(len(namespaces))); err != nil {
		return fail(err)
	}
	decision := catalogOrderDecision{command: command, reviewer: capability.review.Reviewer(), canonical: canonical, result: CatalogOrderResult{Command: command.ID, View: CatalogEffectiveOrder{Revision: revision}}}
	decision.result.View, err = s.effective(&decision)
	if err != nil {
		return fail(err)
	}
	counter := catalogEncoder{}
	s.encodeOrderState(&counter, added, &decision)
	if counter.err != nil {
		return fail(counter.err)
	}
	next := s
	next.revision, next.links = revision, s.links+links
	next.evidence = append(append([]CatalogSequenceEvidence(nil), s.evidence...), added...)
	next.decisions = append(append([]catalogOrderDecision(nil), s.decisions...), decision)
	return next, cloneOrderResult(decision.result), nil
}

func cloneOrderResult(result CatalogOrderResult) CatalogOrderResult {
	result.View.Positions = append([]CatalogEffectivePosition(nil), result.View.Positions...)
	for i := range result.View.Positions {
		result.View.Positions[i].Entry.Support = append([]evidence.ID(nil), result.View.Positions[i].Entry.Support...)
	}
	return result
}

func (s CatalogOrderState) LookupCommand(capability CatalogOrderCapability, command CatalogOrderCommand) (CatalogOrderResult, bool, error) {
	command, err := normalizeOrderCommand(capability, command)
	if err != nil {
		return CatalogOrderResult{}, false, err
	}
	if command.Content != s.content {
		return CatalogOrderResult{}, false, ErrCatalogSubjectMismatch
	}
	canonical, err := canonicalOrderCommand(capability.review.Reviewer(), command)
	if err != nil {
		return CatalogOrderResult{}, false, err
	}
	for _, old := range s.decisions {
		if old.command.ID == command.ID {
			if old.canonical != canonical {
				return CatalogOrderResult{}, false, ErrCatalogCommandConflict
			}
			return cloneOrderResult(old.result), true, nil
		}
	}
	return CatalogOrderResult{}, false, nil
}

func (e *catalogEncoder) orderNamespace(namespace CatalogOrderNamespace) {
	e.raw(string(namespace.binding.id[:]))
	e.source(namespace.Source())
	e.text(namespace.binding.contract)
}

func (e *catalogEncoder) sequence(observation CatalogSequenceEvidence) {
	e.orderNamespace(observation.namespace)
	input := observation.input
	e.text(input.ID.String())
	e.text(input.Content.String())
	e.text(input.Version.String())
	e.number(input.Position)
	coordinate := input.Coordinate
	facts := coordinate.artifact.facts
	e.raw(string(facts.ID.value[:]))
	e.raw(string(facts.SHA256[:]))
	e.number(facts.ByteLength)
	e.number(facts.RecordCount)
	e.text(facts.ParserRevision)
	e.text(facts.SourceRevision)
	e.number(coordinate.index)
	e.text(coordinate.locator)
	e.text(coordinate.snapshot.raw)
}

func (e *catalogEncoder) orderEntry(entry CatalogOrderEntry) {
	e.text(entry.Version.String())
	e.number(entry.Position)
	e.number(uint64(len(entry.Support)))
	for _, id := range entry.Support {
		e.text(id.String())
	}
}

func (e *catalogEncoder) orderCommand(reviewer IssuerPrincipal, command CatalogOrderCommand) {
	e.text("pi08-order-command/1")
	e.text(CatalogDecisionPolicy)
	e.text(command.ID.String())
	e.text(command.Content.String())
	e.orderNamespace(command.Namespace)
	e.text(reviewer.String())
	e.number(command.Expected.value)
	e.text(command.Reason)
	e.number(uint64(len(command.Entries)))
	for _, entry := range command.Entries {
		e.orderEntry(entry)
	}
	e.number(uint64(len(command.Evidence)))
	for _, observation := range command.Evidence {
		e.sequence(observation)
	}
	e.number(uint64(len(command.Supersedes)))
	for _, id := range command.Supersedes {
		e.text(id.String())
	}
}

func canonicalOrderCommand(reviewer IssuerPrincipal, command CatalogOrderCommand) (string, error) {
	counter := catalogEncoder{}
	counter.orderCommand(reviewer, command)
	if counter.err != nil {
		return "", counter.err
	}
	e := catalogEncoder{data: make([]byte, 0, int(counter.size))}
	e.orderCommand(reviewer, command)
	return string(e.data), e.err
}

func (e *catalogEncoder) orderResult(result CatalogOrderResult) {
	e.text(result.Command.String())
	e.number(result.View.Revision.value)
	e.number(uint64(result.View.Status))
	e.number(uint64(len(result.View.Positions)))
	for _, position := range result.View.Positions {
		e.orderNamespace(position.Namespace)
		e.orderEntry(position.Entry)
	}
}

func (s CatalogOrderState) encodeOrderState(e *catalogEncoder, added []CatalogSequenceEvidence, next *catalogOrderDecision) {
	e.text("pi08-order-state/1")
	e.text(s.content.String())
	revision := s.revision
	if next != nil {
		revision = next.result.View.Revision
	}
	e.number(revision.value)
	e.number(s.links)
	e.number(uint64(len(s.versions)))
	for _, version := range s.versions {
		e.text(version.ID().String())
		e.text(version.ContentID().String())
		e.number(version.Fingerprint().AudioID().Uint64())
		e.text(version.Fingerprint().Hash().String())
		// Original immutable order is retained as history, never consulted.
		order := version.OrderEvidence()
		e.text(order.Namespace())
		e.number(order.Position())
		if order.Known() {
			e.number(1)
		} else {
			e.number(0)
		}
	}
	e.number(uint64(len(s.evidence) + len(added)))
	for _, observations := range [][]CatalogSequenceEvidence{s.evidence, added} {
		for _, observation := range observations {
			e.sequence(observation)
		}
	}
	count := len(s.decisions)
	if next != nil {
		count++
	}
	e.number(uint64(count))
	encode := func(decision catalogOrderDecision) {
		e.orderCommand(decision.reviewer, decision.command)
		e.text(decision.canonical)
		e.orderResult(decision.result)
	}
	for _, decision := range s.decisions {
		encode(decision)
	}
	if next != nil {
		encode(*next)
	}
}

func (s CatalogOrderState) RetainedEncodingSize() (uint64, error) {
	e := catalogEncoder{}
	s.encodeOrderState(&e, nil, nil)
	return e.size, e.err
}
