package tagregistry

import (
	"context"
	"errors"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

// OptionalText separates field presence from text content. Present empty text
// is invalid; absent text is not confused with a valid all-zero physical UID.
type OptionalText struct {
	Present bool
	Value   string
}

type PhysicalIdentityInput struct {
	UID  OptionalText
	RUID OptionalText
}

type RegisterCommand struct {
	OpaqueID string
	Physical PhysicalIdentityInput
}

type Service struct {
	transactions Transactor
}

func New(transactions Transactor) Service {
	return Service{transactions: transactions}
}

func (s Service) Register(ctx context.Context, command RegisterCommand) (domaintag.Tag, error) {
	id, err := identity.ParseTagID(command.OpaqueID)
	if err != nil || id.IsZero() {
		return domaintag.Tag{}, ErrInvalidInput
	}
	uid, err := normalizePhysicalIdentity(command.Physical)
	if err != nil {
		return domaintag.Tag{}, err
	}
	created, err := domaintag.NewTag(id, uid)
	if err != nil {
		return domaintag.Tag{}, ErrInvalidInput
	}
	if s.transactions == nil {
		return domaintag.Tag{}, ErrRepositoryUnavailable
	}

	var result domaintag.Tag
	err = s.transactions.WithinTagTransaction(ctx, func(repository TagRepository) error {
		byID, foundByID, findErr := repository.FindByID(ctx, id)
		if findErr != nil {
			return findErr
		}
		if foundByID && (!validStoredTag(byID) || byID.ID() != id) {
			return ErrRepositoryUnavailable
		}

		byUID, foundByUID, findErr := repository.FindByUID(ctx, uid)
		if findErr != nil {
			return findErr
		}
		if foundByUID && (!validStoredTag(byUID) || byUID.UID() != uid) {
			return ErrRepositoryUnavailable
		}

		switch {
		case foundByID && foundByUID:
			if byID.ID() != id || byID.UID() != uid || byUID.ID() != id || byUID.UID() != uid {
				return ErrIdentityConflict
			}
			if !byID.Equal(byUID) {
				return ErrRepositoryUnavailable
			}
			result = byID
			return nil
		case foundByID || foundByUID:
			// A matching value visible through only one unique index is corrupt
			// storage, while any differing identity is a caller collision.
			existing := byID
			if foundByUID {
				existing = byUID
			}
			if existing.ID() == id && existing.UID() == uid {
				return ErrRepositoryUnavailable
			}
			return ErrIdentityConflict
		default:
			if insertErr := repository.Insert(ctx, created); insertErr != nil {
				return insertErr
			}
			result = created
			return nil
		}
	})
	if err != nil {
		return domaintag.Tag{}, sanitizeError(ctx, err)
	}
	return result, nil
}

func (s Service) FindByID(ctx context.Context, opaqueID string) (domaintag.Tag, error) {
	id, err := identity.ParseTagID(opaqueID)
	if err != nil || id.IsZero() {
		return domaintag.Tag{}, ErrInvalidInput
	}
	return s.find(ctx, func(repository TagRepository) (domaintag.Tag, bool, error) {
		return repository.FindByID(ctx, id)
	}, func(found domaintag.Tag) bool {
		return found.ID() == id
	})
}

func (s Service) FindByUID(ctx context.Context, uidText string) (domaintag.Tag, error) {
	return s.FindByPhysicalIdentity(ctx, PhysicalIdentityInput{
		UID: OptionalText{Present: true, Value: uidText},
	})
}

func (s Service) FindByRUID(ctx context.Context, ruidText string) (domaintag.Tag, error) {
	return s.FindByPhysicalIdentity(ctx, PhysicalIdentityInput{
		RUID: OptionalText{Present: true, Value: ruidText},
	})
}

func (s Service) FindByPhysicalIdentity(ctx context.Context, input PhysicalIdentityInput) (domaintag.Tag, error) {
	uid, err := normalizePhysicalIdentity(input)
	if err != nil {
		return domaintag.Tag{}, err
	}
	return s.find(ctx, func(repository TagRepository) (domaintag.Tag, bool, error) {
		return repository.FindByUID(ctx, uid)
	}, func(found domaintag.Tag) bool {
		return found.UID() == uid
	})
}

func (s Service) find(
	ctx context.Context,
	lookup func(TagRepository) (domaintag.Tag, bool, error),
	matchesLookup func(domaintag.Tag) bool,
) (domaintag.Tag, error) {
	if s.transactions == nil {
		return domaintag.Tag{}, ErrRepositoryUnavailable
	}
	var result domaintag.Tag
	err := s.transactions.WithinTagTransaction(ctx, func(repository TagRepository) error {
		found, ok, findErr := lookup(repository)
		if findErr != nil {
			return findErr
		}
		if !ok {
			return ErrTagNotFound
		}
		if !validStoredTag(found) || !matchesLookup(found) {
			return ErrRepositoryUnavailable
		}
		result = found
		return nil
	})
	if err != nil {
		return domaintag.Tag{}, sanitizeError(ctx, err)
	}
	return result, nil
}

func validStoredTag(found domaintag.Tag) bool {
	return !found.ID().IsZero() && found.Revision() > 0
}

func normalizePhysicalIdentity(input PhysicalIdentityInput) (domaintag.UID, error) {
	if !input.UID.Present && !input.RUID.Present {
		return domaintag.UID{}, ErrInvalidInput
	}

	var uid domaintag.UID
	var ruid domaintag.RUID
	var err error
	if input.UID.Present {
		uid, err = domaintag.ParseUID(input.UID.Value)
		if err != nil {
			return domaintag.UID{}, ErrInvalidInput
		}
	}
	if input.RUID.Present {
		ruid, err = domaintag.ParseRUID(input.RUID.Value)
		if err != nil {
			return domaintag.UID{}, ErrInvalidInput
		}
	}

	switch {
	case input.UID.Present && input.RUID.Present:
		pair, pairErr := domaintag.NewUIDPair(uid, ruid)
		if pairErr != nil {
			return domaintag.UID{}, ErrInvalidInput
		}
		return pair.UID(), nil
	case input.UID.Present:
		return uid, nil
	default:
		return ruid.UID(), nil
	}
}

func sanitizeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for _, allowed := range []error{
		ErrInvalidInput,
		ErrTagNotFound,
		ErrIdentityConflict,
		ErrRevisionConflict,
		ErrEvidenceConflict,
		ErrLimitExceeded,
		ErrRepositoryContention,
		ErrRepositoryUnavailable,
	} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	return ErrRepositoryUnavailable
}
