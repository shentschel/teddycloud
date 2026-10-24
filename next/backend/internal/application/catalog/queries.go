package catalog

import (
	"context"

	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type QueryKind uint8

const (
	GetContent QueryKind = iota + 1
	GetVersion
	FindVersionsByPair
	FindVersionsByAudioID
	ListVersions
	ListCurrentVersions
)

type Query struct {
	Kind        QueryKind
	Content     identity.ContentID
	Version     identity.ContentVersionID
	Fingerprint domaincatalog.AudioFingerprint
	AudioID     domaincatalog.AudioID
	Page        PageRequest
}

type PageRequest struct {
	Limit            uint64
	Cursor           string
	ExpectedRevision domaincatalog.CatalogRevision
}

type ResultKind uint8

const (
	NoMatch ResultKind = iota + 1
	Found
	Ambiguous
	Conflicted
	Incomplete
)

// MediaState projects stored relations or separately supplied verification
// outcomes. This service never probes bytes or upgrades an availability claim.
type MediaState uint8

const (
	MediaUnknown MediaState = iota
	CatalogOnly
	MediaBoundUnchecked
	MediaMissing
	MediaCorrupt
	MediaUnavailable
	MediaEnvelopeVerified
)

// CatalogReviewStatus describes the Content editorial projections below, not
// version-registration or order decisions. Reviewed means at least one active
// editorial projection, including a conflict; other fields may remain unknown.
// These internal states do not establish public V3 vocabulary or source authority.
type CatalogReviewStatus uint8

const (
	// The zero value is conservatively unmanaged, never implicitly reviewed.
	CatalogUnmanaged CatalogReviewStatus = iota
	CatalogManagedUnreviewed
	CatalogReviewed
)

// CatalogRecord keeps stored facts separate from reviewed editorial projections.
// Baseline is stored Content context, even when a later reviewed value differs.
// Its zero value preserves an empty stored title and absent product identifiers.
// Version is an immutable stored tuple, including legacy order evidence; neither
// its presence nor Review grants catalog acceptance to ownership, pair or order.
// ProtectedCustom is a separate protection flag, never a review/physical class.
type CatalogRecord struct {
	Content         identity.ContentID
	Baseline        domaincatalog.ContentFacts
	Review          CatalogReviewStatus
	Version         domaincatalog.ContentVersion
	ProtectedCustom bool
	Title           domaincatalog.CatalogField
	Model           domaincatalog.CatalogField
	Article         domaincatalog.CatalogField
	Media           MediaState
	Supersession    Supersession
}

// QueryResult is a bounded page, never an implicit scalar winner. Repositories
// count the real encoded response before materializing it, including escaping,
// and return EncodedBytes, charging Baseline and every projection/support link.
// Entries sort by opaque ContentID then VersionID.
// Incomplete results never assert uniqueness; Next is bound by CursorCodec.
type QueryResult struct {
	Kind         ResultKind
	Entries      []CatalogRecord
	Revision     domaincatalog.CatalogRevision
	Complete     bool
	Next         string
	Inspected    uint64
	EncodedBytes uint64
}

type SupersessionStatus uint8

const (
	Unavailable SupersessionStatus = iota
	Current
	Superseded
)

// Supersession annotates an immutable version. Terminal is descriptive only;
// consumers must never use it to retarget an assignment or infer latest order.
type Supersession struct {
	Status                           SupersessionStatus
	SupersessionRevision             domaincatalog.CatalogRevision
	Predecessor, Successor, Terminal identity.ContentVersionID
	IncomingCommand, OutgoingCommand domaincatalog.CatalogCommandID
}

type SupersessionQuery struct {
	Content identity.ContentID
	Version identity.ContentVersionID
	Page    PageRequest
}

type SupersessionHistoryEntry struct {
	Command, Result []byte
	SourceRevoked   bool
}

// The owner validates the whole graph before filtering by the optional Version
// (the entire containing chain) or paginating. Inspected counts all graph edges.
// Cursors bind this filter, last edge revision, graph and revocation digest,
// catalog snapshot and owner generation. Missing Content reports revisions 1/1.
type HistoryPage struct {
	Entries                                 []SupersessionHistoryEntry
	AggregateRevision, SupersessionRevision domaincatalog.CatalogRevision
	Complete                                bool
	Next                                    string
	Inspected, EncodedBytes                 uint64
}

func validateSupersessionAnnotation(entry CatalogRecord) error {
	s := entry.Supersession
	if s.Status == Unavailable {
		if s != (Supersession{}) {
			return ErrRepositoryUnavailable
		}
		return nil
	}
	v := entry.Version.ID()
	if v.IsZero() || entry.Review == CatalogUnmanaged || s.Status > Superseded ||
		s.SupersessionRevision.Uint64() == 0 || s.SupersessionRevision.Uint64() > domaincatalog.RetainedVersions.Maximum() ||
		s.Terminal.IsZero() || s.Predecessor == v || s.Successor == v ||
		s.Predecessor.IsZero() != s.IncomingCommand.IsZero() || s.Successor.IsZero() != s.OutgoingCommand.IsZero() {
		return ErrRepositoryUnavailable
	}
	if s.SupersessionRevision.Uint64() == 1 && (!s.Predecessor.IsZero() || !s.Successor.IsZero()) {
		return ErrRepositoryUnavailable
	}
	if !s.IncomingCommand.IsZero() && s.IncomingCommand == s.OutgoingCommand {
		return ErrRepositoryUnavailable
	}
	if !s.Predecessor.IsZero() && (s.Predecessor == s.Successor || s.Predecessor == s.Terminal) {
		return ErrRepositoryUnavailable
	}
	if s.Status == Current && (!s.Successor.IsZero() || s.Terminal != v) ||
		s.Status == Superseded && (s.Successor.IsZero() || s.Terminal == v) {
		return ErrRepositoryUnavailable
	}
	return nil
}

func validateSupersessionQuery(query SupersessionQuery) error {
	// Reuse exactly the Content-scoped pagination contract. Version is an
	// additional optional history filter, never an alternative to Content.
	return validateQuery(Query{Kind: ListVersions, Content: query.Content, Page: query.Page})
}

func validateHistoryPage(query SupersessionQuery, page HistoryPage) error {
	if page.AggregateRevision.Uint64() == 0 || page.SupersessionRevision.Uint64() == 0 ||
		page.SupersessionRevision.Uint64() > page.AggregateRevision.Uint64() ||
		page.SupersessionRevision.Uint64() > domaincatalog.RetainedVersions.Maximum() ||
		page.Inspected != page.SupersessionRevision.Uint64()-1 || page.Inspected < uint64(len(page.Entries)) ||
		page.Complete && page.Next != "" || !page.Complete && (page.Next == "" || len(page.Entries) == 0) {
		return ErrRepositoryUnavailable
	}
	for _, bound := range []struct {
		resource domaincatalog.Resource
		count    uint64
	}{
		{domaincatalog.QueryEntries, uint64(len(page.Entries))}, {domaincatalog.QueryBytes, page.EncodedBytes},
		{domaincatalog.ResolutionCandidates, page.Inspected}, {domaincatalog.CursorBytes, uint64(len(page.Next))},
	} {
		if err := domaincatalog.CheckLimit(bound.resource, 0, bound.count); err != nil {
			return err
		}
	}
	if uint64(len(page.Entries)) > query.Page.Limit {
		return ErrLimitExceeded
	}
	if query.Page.ExpectedRevision.Uint64() != 0 && query.Page.ExpectedRevision != page.AggregateRevision {
		return ErrStaleCursor
	}
	size := uint64(8 + len("pi08-version-supersession-page/1") + 6*8 + len(page.Next))
	var previousSupersession, previousAggregate uint64
	seenCommands := map[domaincatalog.CatalogCommandID]bool{}
	predecessors := map[identity.ContentVersionID]identity.ContentVersionID{}
	successors := map[identity.ContentVersionID]identity.ContentVersionID{}
	for _, entry := range page.Entries {
		added := uint64(24) + uint64(len(entry.Command)) + uint64(len(entry.Result))
		if err := domaincatalog.CheckLimit(domaincatalog.QueryBytes, size, added); err != nil {
			return err
		}
		size += added
		command, _, err := domaincatalog.RehydrateCatalogSupersessionCommand(entry.Command, domaincatalog.CatalogPersistenceBindings{AllowUnbound: true})
		if err != nil {
			return ErrRepositoryUnavailable
		}
		result, err := domaincatalog.RehydrateCatalogSupersessionResult(entry.Result)
		if err != nil {
			return ErrRepositoryUnavailable
		}
		if _, err := validateSupersessionResponse(command, result); err != nil {
			return err
		}
		if command.Content != query.Content || result.AggregateRevision.Uint64() > page.AggregateRevision.Uint64() ||
			result.SupersessionRevision.Uint64() > page.SupersessionRevision.Uint64() ||
			result.SupersessionRevision.Uint64() <= previousSupersession || result.AggregateRevision.Uint64() <= previousAggregate || seenCommands[command.ID] {
			return ErrRepositoryUnavailable
		}
		seenCommands[command.ID] = true
		if !successors[command.Old.ID()].IsZero() || !predecessors[command.New.ID()].IsZero() {
			return ErrRepositoryUnavailable
		}
		successors[command.Old.ID()] = command.New.ID()
		predecessors[command.New.ID()] = command.Old.ID()
		previousSupersession, previousAggregate = result.SupersessionRevision.Uint64(), result.AggregateRevision.Uint64()
	}
	if size != page.EncodedBytes {
		return ErrRepositoryUnavailable
	}
	// Unfiltered first-page complete history must contain every inspected edge.
	if query.Version.IsZero() && query.Page.Cursor == "" && page.Complete && uint64(len(page.Entries)) != page.Inspected {
		return ErrRepositoryUnavailable
	}
	if !query.Version.IsZero() && query.Page.Cursor == "" && page.Complete && len(page.Entries) != 0 {
		// A complete filtered page must describe precisely one chain containing
		// the requested version. Bounded traversal also rejects cyclic pages.
		seen := map[identity.ContentVersionID]bool{query.Version: true}
		for _, links := range []map[identity.ContentVersionID]identity.ContentVersionID{predecessors, successors} {
			for v := links[query.Version]; !v.IsZero(); v = links[v] {
				if seen[v] {
					return ErrRepositoryUnavailable
				}
				seen[v] = true
			}
		}
		if len(seen) != len(page.Entries)+1 {
			return ErrRepositoryUnavailable
		}
	}
	return nil
}

func validateQuery(query Query) error {
	if query.Kind < GetContent || query.Kind > ListCurrentVersions || query.Page.Limit == 0 {
		return ErrInvalidInput
	}
	if err := domaincatalog.CheckLimit(domaincatalog.QueryEntries, 0, query.Page.Limit); err != nil {
		return err
	}
	if err := domaincatalog.CheckLimit(domaincatalog.CursorBytes, 0, uint64(len(query.Page.Cursor))); err != nil {
		return err
	}
	if query.Page.Cursor != "" && query.Page.ExpectedRevision.Uint64() == 0 {
		return ErrInvalidInput
	}
	switch query.Kind {
	case GetContent, ListVersions, ListCurrentVersions:
		if query.Content.IsZero() || !query.Version.IsZero() || !query.Fingerprint.IsZero() || !query.AudioID.IsZero() {
			return ErrInvalidInput
		}
	case GetVersion:
		if query.Content.IsZero() || query.Version.IsZero() || !query.Fingerprint.IsZero() || !query.AudioID.IsZero() {
			return ErrInvalidInput
		}
	case FindVersionsByPair:
		if query.Fingerprint.IsZero() || !query.Version.IsZero() || !query.AudioID.IsZero() {
			return ErrInvalidInput
		}
	case FindVersionsByAudioID:
		if query.AudioID.IsZero() || !query.Version.IsZero() || !query.Fingerprint.IsZero() {
			return ErrInvalidInput
		}
	}
	return nil
}

func validateQueryResult(query Query, result QueryResult) error {
	if result.Kind < NoMatch || result.Kind > Incomplete || result.Revision.Uint64() == 0 || result.EncodedBytes == 0 {
		return ErrRepositoryUnavailable
	}
	for _, bound := range []struct {
		resource domaincatalog.Resource
		count    uint64
	}{
		{domaincatalog.QueryEntries, uint64(len(result.Entries))},
		{domaincatalog.QueryBytes, result.EncodedBytes},
		{domaincatalog.ResolutionCandidates, result.Inspected},
		{domaincatalog.CursorBytes, uint64(len(result.Next))},
	} {
		if err := domaincatalog.CheckLimit(bound.resource, 0, bound.count); err != nil {
			return err
		}
	}
	if uint64(len(result.Entries)) > query.Page.Limit {
		return ErrLimitExceeded
	}
	if query.Page.ExpectedRevision.Uint64() != 0 && query.Page.ExpectedRevision != result.Revision {
		return ErrStaleCursor
	}
	if result.Complete == (result.Kind == Incomplete) || result.Complete && result.Next != "" {
		return ErrRepositoryUnavailable
	}
	if result.Kind == NoMatch && len(result.Entries) != 0 || result.Kind != NoMatch && result.Kind != Incomplete && len(result.Entries) == 0 {
		return ErrRepositoryUnavailable
	}
	if result.Kind == Ambiguous && len(result.Entries) < 2 || (query.Kind == GetContent || query.Kind == GetVersion) && len(result.Entries) > 1 {
		return ErrRepositoryUnavailable
	}
	if result.Kind == Found && query.Kind != ListVersions && query.Kind != ListCurrentVersions && len(result.Entries) != 1 {
		return ErrRepositoryUnavailable
	}
	conflict := false
	for i, entry := range result.Entries {
		if entry.Content.IsZero() || entry.Media > MediaEnvelopeVerified || !query.Content.IsZero() && entry.Content != query.Content {
			return ErrRepositoryUnavailable
		}
		if err := validateCatalogRecord(entry); err != nil {
			return err
		}
		if err := validateSupersessionAnnotation(entry); err != nil {
			return err
		}
		if query.Kind == ListCurrentVersions && entry.Supersession.Status != Current {
			return ErrRepositoryUnavailable
		}
		if query.Kind == GetContent && (entry.Version != (domaincatalog.ContentVersion{}) || entry.Media != MediaUnknown) {
			return ErrRepositoryUnavailable
		}
		if query.Kind != GetContent {
			if entry.Version.ID().IsZero() || entry.Version.ContentID() != entry.Content || entry.Version.Fingerprint().IsZero() {
				return ErrRepositoryUnavailable
			}
			if query.Kind == GetVersion && entry.Version.ID() != query.Version || query.Kind == FindVersionsByPair && entry.Version.Fingerprint() != query.Fingerprint || query.Kind == FindVersionsByAudioID && entry.Version.Fingerprint().AudioID() != query.AudioID {
				return ErrRepositoryUnavailable
			}
		}
		for _, field := range []domaincatalog.CatalogField{entry.Title, entry.Model, entry.Article} {
			conflict = conflict || field.Status == domaincatalog.CatalogFactConflict
		}
		if i > 0 {
			previous := result.Entries[i-1]
			if previous.Content.String() > entry.Content.String() || previous.Content == entry.Content && previous.Version.ID().String() >= entry.Version.ID().String() {
				return ErrRepositoryUnavailable
			}
		}
	}
	if conflict && result.Kind != Conflicted && result.Kind != Incomplete {
		return ErrRepositoryUnavailable
	}
	return nil
}

// validateCatalogRecord checks representation, not the existence/authority of
// persisted decisions. Repositories must verify same-subject support relations
// before constructing fields; this boundary never manufactures review evidence.
func validateCatalogRecord(entry CatalogRecord) error {
	if entry.Review > CatalogReviewed {
		return ErrRepositoryUnavailable
	}
	title := entry.Baseline.Title()
	if err := domaincatalog.CheckLimit(domaincatalog.TextBytes, 0, uint64(len(title))); err != nil {
		return err
	}
	if title != "" && !evidence.ValidText(title, int(domaincatalog.TextBytes.Maximum())) {
		return ErrRepositoryUnavailable
	}
	product := entry.Baseline.ProductIdentifiers()
	if model, present := product.Model(); present {
		if _, err := domaincatalog.ParseModelNumber(model.String()); err != nil {
			return ErrRepositoryUnavailable
		}
	}
	if article, present := product.Article(); present {
		if _, err := domaincatalog.ParseArticleNumber(article.String()); err != nil {
			return ErrRepositoryUnavailable
		}
	}
	reviewed := false
	links := uint64(0)
	seen := make(map[evidence.ID]struct{})
	for _, projection := range []struct {
		key   domaincatalog.ClaimKey
		field domaincatalog.CatalogField
	}{
		{domaincatalog.TitleClaimKey, entry.Title},
		{domaincatalog.ModelClaimKey, entry.Model},
		{domaincatalog.ArticleClaimKey, entry.Article},
	} {
		field := projection.field
		count := len(field.Alternatives)
		if err := domaincatalog.CheckLimit(domaincatalog.RetainedObservations, 0, uint64(count)); err != nil {
			return err
		}
		switch field.Status {
		case domaincatalog.CatalogFactUnknown:
			if count != 0 {
				return ErrRepositoryUnavailable
			}
		case domaincatalog.CatalogFactAccepted:
			if count != 1 || entry.Review != CatalogReviewed {
				return ErrRepositoryUnavailable
			}
			reviewed = true
		case domaincatalog.CatalogFactConflict:
			if count < 2 || entry.Review != CatalogReviewed {
				return ErrRepositoryUnavailable
			}
			reviewed = true
		default:
			return ErrRepositoryUnavailable
		}
		for i, alternative := range field.Alternatives {
			claim := alternative.Claim
			if claim.Key() != projection.key || len(alternative.Support) == 0 {
				return ErrRepositoryUnavailable
			}
			// CatalogClaim is immutable and constructible only through its
			// checked domain constructors. Same-key text alternatives use
			// the domain's canonical length-prefixed text order.
			if i > 0 {
				previous := field.Alternatives[i-1].Claim.Text()
				value := claim.Text()
				if len(previous) > len(value) || len(previous) == len(value) && previous >= value {
					return ErrRepositoryUnavailable
				}
			}
			if err := domaincatalog.CheckLimit(domaincatalog.RetainedSupportLinks, links, uint64(len(alternative.Support))); err != nil {
				return err
			}
			links += uint64(len(alternative.Support))
			for j, id := range alternative.Support {
				if id.IsZero() || j > 0 && alternative.Support[j-1].String() >= id.String() {
					return ErrRepositoryUnavailable
				}
				if _, duplicate := seen[id]; duplicate {
					return ErrRepositoryUnavailable
				}
				seen[id] = struct{}{}
			}
		}
	}
	if (entry.Review == CatalogReviewed) != reviewed {
		return ErrRepositoryUnavailable
	}
	return nil
}

func (s Service) Query(ctx context.Context, query Query) (QueryResult, error) {
	if err := validateQuery(query); err != nil {
		return QueryResult{}, sanitizeError(ctx, err)
	}
	var result QueryResult
	err := s.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		page, err := repository.Query(ctx, query)
		if err != nil {
			return err
		}
		if err := validateQueryResult(query, page); err != nil {
			return err
		}
		result = page
		return nil
	})
	if err != nil {
		return QueryResult{}, err
	}
	return result, nil
}
