package sqlite

import (
	"context"
	"database/sql"
	"errors"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
)

// withinTagTransaction keeps SQL and the owned handle inside the adapter.
// The shared transaction machinery still owns rollback and wait-budget cleanup.
func (database *Database) withinTagTransaction(ctx context.Context, operation func(applicationtag.TagRepository) error) error {
	var callbackFailure *lifecycleCallbackFailure
	err := database.withinSQLTransaction(ctx, func(transaction *sql.Tx) error {
		repository := &tagRepository{transaction: transaction}
		defer repository.invalidate()
		if err := operation(repository); err != nil {
			callbackFailure = &lifecycleCallbackFailure{err: err}
			return callbackFailure
		}
		return nil
	})
	if err == nil {
		return nil
	}
	if callbackFailure != nil && err == callbackFailure {
		return callbackFailure.err
	}
	if callbackFailure != nil && errors.Is(err, callbackFailure) {
		return errors.Join(callbackFailure.err, tagBoundaryError(ctx, err))
	}
	return tagBoundaryError(ctx, err)
}

// Shared plumbing uses catalog sentinels internally; the Tag port returns only
// its own taxonomy and context errors, never a storage diagnostic or SQL type.
func tagBoundaryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, allowed := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	var mapped []error
	for _, allowed := range []error{
		applicationtag.ErrInvalidInput, applicationtag.ErrIdentityConflict,
	} {
		if errors.Is(err, allowed) {
			mapped = append(mapped, allowed)
		}
	}
	if errors.Is(err, applicationtag.ErrRepositoryContention) || errors.Is(err, applicationcatalog.ErrRepositoryContention) {
		mapped = append(mapped, applicationtag.ErrRepositoryContention)
	}
	if errors.Is(err, applicationtag.ErrRepositoryUnavailable) || errors.Is(err, applicationcatalog.ErrRepositoryUnavailable) {
		mapped = append(mapped, applicationtag.ErrRepositoryUnavailable)
	}
	if len(mapped) == 1 {
		return mapped[0]
	}
	if len(mapped) > 1 {
		return errors.Join(mapped...)
	}
	return applicationtag.ErrRepositoryUnavailable
}
