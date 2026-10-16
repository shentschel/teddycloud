package catalog

import (
	"context"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// BaselineRepository extends the scoped port without granting Save authority.
type BaselineRepository interface {
	LoadBaseline(context.Context, identity.ContentID) (domain.CatalogBaseline, domain.CatalogRevision, bool, error)
	AdoptBaseline(context.Context, AdoptionOperation) (AdoptionResult, error)
}
type AdoptionOperation struct {
	Capability domain.ReviewCapability
	Command    domain.CatalogAdoptionCommand
}
type AdoptionCommand struct {
	ID                domain.CatalogCommandID
	Content           identity.ContentID
	ExpectedAggregate domain.CatalogRevision
	BaselineSHA256    [32]byte
	Reason            string
}
type AdoptionResult struct {
	Command           domain.CatalogCommandID
	AggregateRevision domain.CatalogRevision
}

func (s Service) ReadBaseline(ctx context.Context, content identity.ContentID) (baseline domain.CatalogBaseline, revision domain.CatalogRevision, err error) {
	err = s.run(ctx, func(ctx context.Context, r CatalogRepository) error {
		port, ok := r.(BaselineRepository)
		if !ok {
			return ErrUnsupported
		}
		var found bool
		baseline, revision, found, err = port.LoadBaseline(ctx, content)
		if err == nil && !found {
			return ErrSubjectNotFound
		}
		if err == nil && (baseline.Content().ID() != content || revision.Uint64() == 0 || baseline.SHA256() == [32]byte{}) {
			return ErrRepositoryUnavailable
		}
		return err
	})
	if err != nil {
		return domain.CatalogBaseline{}, domain.CatalogRevision{}, err
	}
	return
}

func (s Service) AdoptBaseline(ctx context.Context, command AdoptionCommand) (result AdoptionResult, err error) {
	subject, e := domain.ContentSubject(command.Content)
	if e != nil {
		return result, ErrInvalidInput
	}
	op := AdoptionOperation{Capability: s.review, Command: domain.CatalogAdoptionCommand{ID: command.ID, Subject: subject, Source: s.source, Expected: command.ExpectedAggregate, BaselineSHA256: command.BaselineSHA256, Reason: command.Reason}}
	if _, e = op.Command.Canonical(op.Capability); e != nil {
		return result, sanitizeError(ctx, e)
	}
	err = s.run(ctx, func(ctx context.Context, r CatalogRepository) error {
		port, ok := r.(BaselineRepository)
		if !ok {
			return ErrUnsupported
		}
		var e error
		result, e = port.AdoptBaseline(ctx, op)
		if e == nil && (result.Command != command.ID || result.AggregateRevision.Uint64() != 1) {
			return ErrRepositoryUnavailable
		}
		return e
	})
	if err != nil {
		return AdoptionResult{}, err
	}
	return
}
