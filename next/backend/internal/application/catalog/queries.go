package catalog

import (
	"context"

	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type QueryKind uint8

const (
	GetContent QueryKind = iota + 1
	GetVersion
	FindVersionsByPair
	FindVersionsByAudioID
	ListVersions
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

type CatalogRecord struct {
	Content         identity.ContentID
	Version         domaincatalog.ContentVersion
	ProtectedCustom bool
	Title           domaincatalog.CatalogField
	Model           domaincatalog.CatalogField
	Article         domaincatalog.CatalogField
	Media           MediaState
}

// QueryResult is a bounded page, never an implicit scalar winner. Repositories
// count the real encoded response before materializing it, including escaping,
// and return EncodedBytes. Entries sort by opaque ContentID then VersionID.
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

func validateQuery(query Query) error {
	if query.Kind < GetContent || query.Kind > ListVersions || query.Page.Limit == 0 {
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
	case GetContent, ListVersions:
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
	if result.Kind == Found && query.Kind != ListVersions && len(result.Entries) != 1 {
		return ErrRepositoryUnavailable
	}
	conflict := false
	for i, entry := range result.Entries {
		if entry.Content.IsZero() || entry.Media > MediaEnvelopeVerified || !query.Content.IsZero() && entry.Content != query.Content {
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
