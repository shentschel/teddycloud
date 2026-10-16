package catalog

import (
	"context"

	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// These are internal typed DTOs, not public authentication or import codecs.
// Source, namespace, reviewer and capabilities come only from Service.
type ReviewCommand struct {
	ID                domaincatalog.CatalogCommandID
	Subject           domaincatalog.CatalogSubject
	ExpectedAggregate domaincatalog.CatalogRevision
	ExpectedReview    domaincatalog.CatalogRevision
	Key               domaincatalog.ClaimKey
	Observations      []domaincatalog.CatalogObservation
	Support           []evidence.ID
	Reason            string
}

type OrderCommand struct {
	ID                domaincatalog.CatalogCommandID
	Content           identity.ContentID
	ExpectedAggregate domaincatalog.CatalogRevision
	ExpectedOrder     domaincatalog.CatalogRevision
	Entries           []domaincatalog.CatalogOrderEntry
	Evidence          []domaincatalog.CatalogSequenceEvidence
	Supersedes        []domaincatalog.CatalogCommandID
	Reason            string
}

type ReviewResult struct {
	AggregateRevision domaincatalog.CatalogRevision
	Decision          domaincatalog.CatalogDecisionResult
}

type OrderResult struct {
	AggregateRevision domaincatalog.CatalogRevision
	Decision          domaincatalog.CatalogOrderResult
}

func (s Service) reviewOperation(kind domaincatalog.CatalogDecisionKind, command ReviewCommand) (ReviewOperation, error) {
	if command.ID.IsZero() || command.Subject.ContentID().IsZero() || command.ExpectedAggregate.Uint64() == 0 || command.ExpectedReview.Uint64() == 0 {
		return ReviewOperation{}, ErrInvalidInput
	}
	if kind != domaincatalog.RecordObservations && kind != domaincatalog.AcceptSupport && kind != domaincatalog.ResolveConflict {
		return ReviewOperation{}, ErrInvalidInput
	}
	if kind == domaincatalog.RecordObservations {
		if command.Key != 0 || len(command.Support) != 0 || len(command.Observations) == 0 {
			return ReviewOperation{}, ErrInvalidInput
		}
		if err := domaincatalog.CheckLimit(domaincatalog.MutationObservations, 0, uint64(len(command.Observations))); err != nil {
			return ReviewOperation{}, err
		}
		for _, observation := range command.Observations {
			if err := s.review.Authorize(s.source, observation.Input().Claim.Key()); err != nil {
				return ReviewOperation{}, err
			}
			if err := domaincatalog.ValidateObservationBinding(observation, command.Subject, s.source); err != nil {
				return ReviewOperation{}, err
			}
		}
	} else {
		if len(command.Observations) != 0 || len(command.Support) == 0 {
			return ReviewOperation{}, ErrInvalidInput
		}
		if err := domaincatalog.CheckLimit(domaincatalog.SelectedSupport, 0, uint64(len(command.Support))); err != nil {
			return ReviewOperation{}, err
		}
		if err := s.review.Authorize(s.source, command.Key); err != nil {
			return ReviewOperation{}, err
		}
	}
	return ReviewOperation{
		ExpectedAggregate: command.ExpectedAggregate,
		Capability:        s.review,
		Command: domaincatalog.CatalogReviewCommand{
			ID: command.ID, Kind: kind, Subject: command.Subject, Source: s.source,
			Expected: command.ExpectedReview, Key: command.Key, Observations: command.Observations,
			Support: command.Support, Reason: command.Reason,
		},
	}, nil
}

func (s Service) RecordObservations(ctx context.Context, command ReviewCommand) (ReviewResult, error) {
	return s.mutateReview(ctx, domaincatalog.RecordObservations, command)
}

func (s Service) AcceptSupport(ctx context.Context, command ReviewCommand) (ReviewResult, error) {
	return s.mutateReview(ctx, domaincatalog.AcceptSupport, command)
}

func (s Service) ResolveConflict(ctx context.Context, command ReviewCommand) (ReviewResult, error) {
	return s.mutateReview(ctx, domaincatalog.ResolveConflict, command)
}

func (s Service) mutateReview(ctx context.Context, kind domaincatalog.CatalogDecisionKind, command ReviewCommand) (ReviewResult, error) {
	operation, err := s.reviewOperation(kind, command)
	if err != nil {
		return ReviewResult{}, sanitizeError(ctx, err)
	}
	var result ReviewResult
	err = s.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		replayed, found, err := repository.LookupReview(ctx, operation)
		if err != nil {
			return err
		}
		if found {
			if replayed.AggregateRevision.Uint64() == 0 || replayed.Decision.Command != command.ID || replayed.Decision.Revision.Uint64() == 0 {
				return ErrRepositoryUnavailable
			}
			result = replayed
			return nil
		}
		snapshot, found, err := repository.LoadReview(ctx, command.Subject)
		if err != nil {
			return err
		}
		if !found {
			return ErrSubjectNotFound
		}
		if snapshot.State.Subject() != command.Subject {
			return ErrRepositoryUnavailable
		}
		if snapshot.AggregateRevision != command.ExpectedAggregate {
			return ErrRevisionConflict
		}
		// Storage reads return detached provenance. Bind only the exact current
		// trusted source/reviewer before applying a new command; bytes grant no
		// authority and mismatched source metadata still fails closed.
		raw, err := snapshot.State.PersistenceEncoding()
		if err != nil {
			return err
		}
		snapshot.State, err = domaincatalog.RehydrateCatalogReviewState(raw, domaincatalog.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domaincatalog.SourceRegistration{s.source}, Reviewers: []domaincatalog.IssuerPrincipal{s.review.Reviewer()}})
		if err != nil {
			return err
		}
		next, decision, err := snapshot.State.Apply(s.review, operation.Command)
		if err != nil {
			return err
		}
		// An existing domain command without its aggregate envelope is corrupt;
		// it cannot be recommitted under a different aggregate revision.
		if next.Revision() == snapshot.State.Revision() {
			return ErrRepositoryUnavailable
		}
		revision, err := nextAggregate(snapshot.AggregateRevision)
		if err != nil {
			return err
		}
		result = ReviewResult{AggregateRevision: revision, Decision: decision}
		if err := ctx.Err(); err != nil {
			return err
		}
		return repository.CommitReview(ctx, operation, next, result)
	})
	if err != nil {
		return ReviewResult{}, err
	}
	return result, nil
}

func (s Service) orderOperation(command OrderCommand) (OrderOperation, error) {
	if command.ID.IsZero() || command.Content.IsZero() || command.ExpectedAggregate.Uint64() == 0 || command.ExpectedOrder.Uint64() == 0 {
		return OrderOperation{}, ErrInvalidInput
	}
	if err := domaincatalog.CheckLimit(domaincatalog.RetainedVersions, 0, uint64(len(command.Entries))); err != nil {
		return OrderOperation{}, err
	}
	if err := domaincatalog.CheckLimit(domaincatalog.MutationObservations, 0, uint64(len(command.Evidence))); err != nil {
		return OrderOperation{}, err
	}
	links := uint64(len(command.Supersedes))
	if err := domaincatalog.CheckLimit(domaincatalog.SelectedSupport, 0, links); err != nil {
		return OrderOperation{}, err
	}
	for _, entry := range command.Entries {
		if err := domaincatalog.CheckLimit(domaincatalog.SelectedSupport, links, uint64(len(entry.Support))); err != nil {
			return OrderOperation{}, err
		}
		links += uint64(len(entry.Support))
	}
	capability, err := domaincatalog.NewCatalogOrderCapability(s.review, s.namespace)
	if err != nil {
		return OrderOperation{}, err
	}
	return OrderOperation{
		ExpectedAggregate: command.ExpectedAggregate, Capability: capability,
		Command: domaincatalog.CatalogOrderCommand{
			ID: command.ID, Content: command.Content, Namespace: s.namespace,
			Expected: command.ExpectedOrder, Entries: command.Entries, Evidence: command.Evidence,
			Supersedes: command.Supersedes, Reason: command.Reason,
		},
	}, nil
}

func (s Service) DecideOrder(ctx context.Context, command OrderCommand) (OrderResult, error) {
	operation, err := s.orderOperation(command)
	if err != nil {
		return OrderResult{}, sanitizeError(ctx, err)
	}
	var result OrderResult
	err = s.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		replayed, found, err := repository.LookupOrder(ctx, operation)
		if err != nil {
			return err
		}
		if found {
			if replayed.AggregateRevision.Uint64() == 0 || replayed.Decision.Command != command.ID || replayed.Decision.View.Revision.Uint64() == 0 {
				return ErrRepositoryUnavailable
			}
			result = replayed
			return nil
		}
		snapshot, found, err := repository.LoadOrder(ctx, command.Content)
		if err != nil {
			return err
		}
		if !found {
			return ErrSubjectNotFound
		}
		if snapshot.AggregateRevision != command.ExpectedAggregate {
			return ErrRevisionConflict
		}
		raw, err := snapshot.State.PersistenceEncoding()
		if err != nil {
			return err
		}
		snapshot.State, err = domaincatalog.RehydrateCatalogOrderState(raw, domaincatalog.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domaincatalog.SourceRegistration{s.source}, Namespaces: []domaincatalog.CatalogOrderNamespace{s.namespace}, Reviewers: []domaincatalog.IssuerPrincipal{s.review.Reviewer()}})
		if err != nil {
			return err
		}
		next, decision, err := snapshot.State.Apply(operation.Capability, operation.Command)
		if err != nil {
			return err
		}
		if next.Revision() == snapshot.State.Revision() {
			return ErrRepositoryUnavailable
		}
		revision, err := nextAggregate(snapshot.AggregateRevision)
		if err != nil {
			return err
		}
		result = OrderResult{AggregateRevision: revision, Decision: decision}
		if err := ctx.Err(); err != nil {
			return err
		}
		return repository.CommitOrder(ctx, operation, next, result)
	})
	if err != nil {
		return OrderResult{}, err
	}
	return result, nil
}

func (s Service) LookupReview(ctx context.Context, kind domaincatalog.CatalogDecisionKind, command ReviewCommand) (ReviewResult, bool, error) {
	operation, err := s.reviewOperation(kind, command)
	if err != nil {
		return ReviewResult{}, false, sanitizeError(ctx, err)
	}
	var result ReviewResult
	var found bool
	err = s.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		var err error
		result, found, err = repository.LookupReview(ctx, operation)
		if err == nil && found && (result.AggregateRevision.Uint64() == 0 || result.Decision.Command != command.ID || result.Decision.Revision.Uint64() == 0) {
			return ErrRepositoryUnavailable
		}
		return err
	})
	if err != nil || !found {
		return ReviewResult{}, false, err
	}
	return result, true, nil
}

func (s Service) LookupOrder(ctx context.Context, command OrderCommand) (OrderResult, bool, error) {
	operation, err := s.orderOperation(command)
	if err != nil {
		return OrderResult{}, false, sanitizeError(ctx, err)
	}
	var result OrderResult
	var found bool
	err = s.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		var err error
		result, found, err = repository.LookupOrder(ctx, operation)
		if err == nil && found && (result.AggregateRevision.Uint64() == 0 || result.Decision.Command != command.ID || result.Decision.View.Revision.Uint64() == 0) {
			return ErrRepositoryUnavailable
		}
		return err
	})
	if err != nil || !found {
		return OrderResult{}, false, err
	}
	return result, true, nil
}
