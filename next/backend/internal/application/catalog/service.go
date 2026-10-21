package catalog

import (
	"context"
	"errors"
	"math"
	"sync/atomic"

	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

// Service is installed by trusted composition with opaque core capabilities.
// It is not authentication and exposes no source/reviewer selection by text.
type Service struct {
	transactions CatalogTransactor
	source       domaincatalog.SourceRegistration
	review       domaincatalog.ReviewCapability
	namespace    domaincatalog.CatalogOrderNamespace
}

func NewService(transactions CatalogTransactor, source domaincatalog.SourceRegistration, review domaincatalog.ReviewCapability, namespace domaincatalog.CatalogOrderNamespace) (Service, error) {
	service := Service{transactions: transactions, source: source, review: review, namespace: namespace}
	if transactions == nil {
		return Service{}, ErrInvalidInput
	}
	if err := service.authorize(); err != nil {
		return Service{}, sanitizeError(context.Background(), err)
	}
	return service, nil
}

func (s Service) authorize() error {
	if s.namespace.Source() != s.source {
		return ErrSourceMismatch
	}
	_, err := domaincatalog.NewCatalogOrderCapability(s.review, s.namespace)
	return err
}

// run admits one bounded operation and a single callback. The storage owner
// must also perform the final cancellation check immediately before commit.
func (s Service) run(ctx context.Context, operation func(context.Context, CatalogRepository) error) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	if s.transactions == nil {
		return ErrRepositoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, domaincatalog.CatalogOperationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.authorize(); err != nil {
		return sanitizeError(ctx, err)
	}
	transaction, stop := context.WithTimeout(ctx, domaincatalog.CatalogTransactionTimeout)
	defer stop()
	var called atomic.Bool
	var repeated atomic.Bool
	var callbackErr error
	err := s.transactions.WithinCatalogTransaction(transaction, func(repository CatalogRepository) error {
		if called.Swap(true) {
			repeated.Store(true)
			return ErrRepositoryUnavailable
		}
		if repository == nil {
			callbackErr = ErrRepositoryUnavailable
		} else if callbackErr = transaction.Err(); callbackErr == nil {
			callbackErr = operation(transaction, repository)
			if callbackErr == nil {
				callbackErr = transaction.Err()
			}
			if callbackErr == nil {
				callbackErr = s.authorize()
			}
		}
		return callbackErr
	})
	if repeated.Load() || err == nil && !called.Load() {
		return ErrRepositoryUnavailable
	}
	if err == nil {
		err = callbackErr
	}
	if err == nil {
		err = transaction.Err()
	}
	return sanitizeError(transaction, err)
}

func nextAggregate(revision domaincatalog.CatalogRevision) (domaincatalog.CatalogRevision, error) {
	if revision.Uint64() == 0 {
		return domaincatalog.CatalogRevision{}, ErrInvalidInput
	}
	if revision.Uint64() >= math.MaxInt64 {
		return domaincatalog.CatalogRevision{}, ErrRevisionOverflow
	}
	return domaincatalog.NewCatalogRevision(revision.Uint64() + 1)
}

// Only stable errors cross the service boundary. Wrapped storage causes,
// snapshots, locators and private identifiers are never echoed or unwrapped.
func sanitizeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// A caller cancellation after commit cannot prove rollback. Preserve the
	// owner's uncertain outcome so recovery uses authorized command readback.
	if errors.Is(err, ErrIndeterminate) {
		return ErrIndeterminate
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
		ErrInvalidInput, ErrUnauthorized, ErrSourceMismatch, ErrSubjectMismatch,
		ErrSubjectNotFound, ErrRevisionConflict, ErrRevisionOverflow,
		ErrCommandConflict, ErrEvidenceConflict, ErrLimitExceeded,
		ErrUnsupported, ErrStaleCursor, ErrIndeterminate,
		ErrRepositoryContention, ErrRepositoryUnavailable,
	} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	for _, mapping := range []struct{ domain, application error }{
		{domaincatalog.ErrInvalidCatalog, ErrInvalidInput},
		{domaincatalog.ErrCatalogUnauthorized, ErrUnauthorized},
		{domaincatalog.ErrCatalogSourceMismatch, ErrSourceMismatch},
		{domaincatalog.ErrCatalogSubjectMismatch, ErrSubjectMismatch},
		{domaincatalog.ErrCatalogRevisionConflict, ErrRevisionConflict},
		{domaincatalog.ErrCatalogRevisionOverflow, ErrRevisionOverflow},
		{domaincatalog.ErrCatalogCommandConflict, ErrCommandConflict},
		{domaincatalog.ErrCatalogObservationConflict, ErrEvidenceConflict},
		{domaincatalog.ErrCatalogArtifactConflict, ErrEvidenceConflict},
		{domaincatalog.ErrCatalogUnsupported, ErrUnsupported},
	} {
		if errors.Is(err, mapping.domain) {
			return mapping.application
		}
	}
	var limit domaincatalog.LimitError
	if errors.As(err, &limit) {
		return ErrLimitExceeded
	}
	return ErrRepositoryUnavailable
}
