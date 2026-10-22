package catalog

import (
	"bytes"
	"context"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

// RegistrationRepository extends the callback-scoped catalog port. Lookup
// compares the complete canonical command before existence/revision checks and
// returns the original committed result. An absent key returns zero,false,nil.
// Commit computes and validates all state under the same owner transaction;
// it never calls Save or starts another transaction. Both methods must check
// persisted revocation for every used source. The owner also checks those live
// source authorizations and cancellation immediately before commit, revokes the
// port on callback exit and preserves ErrIndeterminate after an uncertain commit.
// Identity/key collisions return ErrCatalogCommandConflict; subject existence
// and revision errors precede evidence validation. Exact replay writes nothing.
type RegistrationRepository interface {
	LookupRegistration(context.Context, RegistrationOperation) (domain.CatalogRegistrationResult, bool, error)
	CommitRegistration(context.Context, RegistrationOperation) (domain.CatalogRegistrationResult, error)
}

type RegistrationOperation struct {
	Capability domain.CatalogRegistrationCapability
	Command    domain.CatalogRegistrationCommand
}

// RegistrationCommand contains caller-supplied facts only. Source, reviewer and
// capabilities are bound by trusted composition, never accepted from a caller.
// ReadRegistration takes the original kind and this complete original command.
type RegistrationCommand struct {
	ID            domain.CatalogCommandID
	Subject       domain.CatalogSubject
	ExpectedKind  domain.CatalogExpectedKind
	Expected      domain.CatalogRevision
	Observations  []domain.CatalogObservation
	Selections    []domain.CatalogRegistrationSelection
	Reason        string
	OriginalOrder domain.VersionOrderEvidence
}

type RegistrationService struct {
	service    Service
	capability domain.CatalogRegistrationCapability
}

func NewRegistrationService(service Service, capability domain.CatalogRegistrationCapability) (RegistrationService, error) {
	s := RegistrationService{service: service, capability: capability}
	if service.transactions == nil {
		return RegistrationService{}, ErrInvalidInput
	}
	// A title-only or ownership-only operator may construct the wrapper; each
	// command still needs its own claim authorization at invocation time.
	kind := domain.RegisterContent
	if !service.source.Allows(domain.TitleClaimKey) {
		kind = domain.RegisterCatalogVersion
	}
	if err := s.authorize(domain.CatalogRegistrationCommand{Kind: kind, Source: service.source}); err != nil {
		return RegistrationService{}, sanitizeError(context.Background(), err)
	}
	return s, nil
}

func (s RegistrationService) authorize(command domain.CatalogRegistrationCommand) error {
	if err := s.service.authorize(); err != nil {
		return err
	}
	if s.capability.Reviewer() != s.service.review.Reviewer() {
		return ErrUnauthorized
	}
	return s.capability.Authorize(command)
}

func (s RegistrationService) RegisterContent(ctx context.Context, command RegistrationCommand) (domain.CatalogRegistrationResult, error) {
	result, _, err := s.run(ctx, domain.RegisterContent, command, false)
	return result, err
}

func (s RegistrationService) RegisterCatalogVersion(ctx context.Context, command RegistrationCommand) (domain.CatalogRegistrationResult, error) {
	result, _, err := s.run(ctx, domain.RegisterCatalogVersion, command, false)
	return result, err
}

func (s RegistrationService) ReadRegistration(ctx context.Context, kind domain.CatalogRegistrationKind, command RegistrationCommand) (domain.CatalogRegistrationResult, bool, error) {
	return s.run(ctx, kind, command, true)
}

func (s RegistrationService) run(ctx context.Context, kind domain.CatalogRegistrationKind, command RegistrationCommand, readOnly bool) (domain.CatalogRegistrationResult, bool, error) {
	op := RegistrationOperation{Capability: s.capability, Command: domain.CatalogRegistrationCommand{
		ID: command.ID, Kind: kind, Subject: command.Subject,
		ExpectedKind: command.ExpectedKind, Expected: command.Expected,
		Source: s.service.source, Observations: command.Observations,
		Selections: command.Selections, Reason: command.Reason, OriginalOrder: command.OriginalOrder,
	}}
	var result domain.CatalogRegistrationResult
	var found bool
	err := s.service.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		// Domain canonicalization checks live authority first, then syntax and
		// bounds. Evidence semantics follow canonical lookup inside the owner.
		if err := s.authorize(op.Command); err != nil {
			return err
		}
		if _, err := op.Command.CanonicalEncoding(op.Capability); err != nil {
			return err
		}
		// Own the slice headers/backing arrays before exposing the operation to
		// storage; the retained domain values themselves have immutable fields.
		op.Command.Observations = append([]domain.CatalogObservation(nil), command.Observations...)
		op.Command.Selections = append([]domain.CatalogRegistrationSelection(nil), command.Selections...)
		port, ok := repository.(RegistrationRepository)
		if !ok {
			return ErrUnsupported
		}
		var err error
		result, found, err = port.LookupRegistration(ctx, op)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.authorize(op.Command); err != nil {
			return err
		}
		if !found && !readOnly {
			result, err = port.CommitRegistration(ctx, op)
			if err != nil {
				return err
			}
			found = true
		}
		if err := s.authorize(op.Command); err != nil {
			return err
		}
		if !found {
			result = domain.CatalogRegistrationResult{}
			return nil
		}
		result, err = validateRegistrationResponse(op, result)
		return err
	})
	if err != nil {
		return domain.CatalogRegistrationResult{}, false, err
	}
	return result, found, nil
}

// Reject a malformed or mismatched envelope while still inside the callback,
// so a provisional write cannot be committed as success. Owner-derived order
// revision and inherited protection are allowed only for a Version. Replay is
// checked against its original command, never today's aggregate revision.
func validateRegistrationResponse(op RegistrationOperation, result domain.CatalogRegistrationResult) (domain.CatalogRegistrationResult, error) {
	raw, err := result.CanonicalEncoding()
	if err != nil {
		return domain.CatalogRegistrationResult{}, ErrRepositoryUnavailable
	}
	context := domain.CatalogRegistrationContext{OrderRevision: result.OrderRevision}
	if op.Command.Kind == domain.RegisterCatalogVersion {
		context.ProtectedCustom = result.ProtectedCustom
	}
	_, expected, err := domain.NewRegisteredCatalogReviewState(op.Capability, op.Command, context)
	if err != nil {
		return domain.CatalogRegistrationResult{}, ErrRepositoryUnavailable
	}
	want, err := expected.CanonicalEncoding()
	if err != nil || !bytes.Equal(raw, want) {
		return domain.CatalogRegistrationResult{}, ErrRepositoryUnavailable
	}
	return expected, nil
}
