package tagregistry

import (
	"context"
	"errors"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

type MetadataCommand struct {
	TagID  identity.TagID
	Change domaintag.MetadataChange
}

// MetadataRepository extends the identity port only for admitted mutations.
type MetadataRepository interface {
	TagRepository
	CompareAndSwap(context.Context, domaintag.Revision, domaintag.Tag) error
}

func (s Service) UpdateMetadata(ctx context.Context, c MetadataCommand) (domaintag.Tag, error) {
	if c.TagID.IsZero() {
		return domaintag.Tag{}, ErrInvalidInput
	}
	if len(c.Change.Observations) > domaintag.MaxNewObservationsPerCommand || (c.Change.Resolution != nil && len(c.Change.Resolution.Support) > domaintag.MaxDecisionSupportLinks) {
		return domaintag.Tag{}, ErrLimitExceeded
	}
	if s.transactions == nil {
		return domaintag.Tag{}, ErrRepositoryUnavailable
	}
	var result domaintag.Tag
	err := s.transactions.WithinTagTransaction(ctx, func(r TagRepository) error {
		repository, ok := r.(MetadataRepository)
		if !ok {
			return ErrRepositoryUnavailable
		}
		current, found, err := repository.FindByID(ctx, c.TagID)
		if err != nil {
			return err
		}
		if !found {
			return ErrTagNotFound
		}
		if !validStoredTag(current) || current.ID() != c.TagID {
			return ErrRepositoryUnavailable
		}
		updated, changed, err := current.ApplyMetadata(c.Change)
		if err != nil {
			return metadataError(err)
		}
		if changed {
			if err := repository.CompareAndSwap(ctx, current.Revision(), updated); err != nil {
				return err
			}
		}
		result = updated
		return nil
	})
	if err != nil {
		return domaintag.Tag{}, sanitizeError(ctx, err)
	}
	return result, nil
}
func metadataError(err error) error {
	switch {
	case errors.Is(err, domaintag.ErrHistoryLimit), errors.Is(err, domaintag.ErrEncodedTagLimit):
		return ErrLimitExceeded
	case errors.Is(err, domaintag.ErrRevisionConflict):
		return ErrRevisionConflict
	case errors.Is(err, domaintag.ErrEvidenceConflict):
		return ErrEvidenceConflict
	default:
		return ErrInvalidInput
	}
}
