package catalog

import (
	"context"
	"errors"
	"math"
	"sort"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// SupersessionRepository is valid only inside the existing catalog callback.
// The owner checks persisted authority before lookup, compares complete canonical
// commands, validates the entire graph, and checks authority/cancellation again
// before commit. Lookup never applies today's revision fences to exact replay.
// Commit computes the result in this transaction and preserves indeterminate
// durability outcomes. All graph, historical support and combined accounting
// checks belong to that owner; this extension never starts a transaction.
type SupersessionRepository interface {
	LookupSupersession(context.Context, SupersessionOperation) (domain.CatalogSupersessionResult, bool, error)
	CommitSupersession(context.Context, SupersessionOperation) (domain.CatalogSupersessionResult, error)
	QuerySupersession(context.Context, SupersessionQuery) (HistoryPage, error)
}

type SupersessionOperation struct {
	Capability domain.CatalogSupersessionCapability
	Command    domain.CatalogSupersessionCommand
}

// Source and reviewer are supplied by trusted composition, never caller text.
type SupersedeVersionCommand struct {
	ID                                      domain.CatalogCommandID
	Content                                 identity.ContentID
	Old, New                                domain.ContentVersion
	ExpectedAggregate, ExpectedSupersession domain.CatalogRevision
	ExpectedOldReview, ExpectedNewReview    domain.CatalogRevision
	Evidence                                []domain.CatalogSupersessionEvidence
	Support                                 []evidence.ID
	Reason                                  string
}

type SupersessionService struct {
	service    Service
	capability domain.CatalogSupersessionCapability
}

func NewSupersessionService(service Service, capability domain.CatalogSupersessionCapability) (SupersessionService, error) {
	s := SupersessionService{service: service, capability: capability}
	if service.transactions == nil {
		return SupersessionService{}, ErrInvalidInput
	}
	if err := s.authorize(); err != nil {
		return SupersessionService{}, sanitizeError(context.Background(), err)
	}
	return s, nil
}

func (s SupersessionService) authorize() error {
	if err := s.service.authorize(); err != nil {
		return err
	}
	if s.capability.Reviewer() != s.service.review.Reviewer() {
		return ErrUnauthorized
	}
	return s.capability.Authorize(s.service.source)
}

func (s SupersessionService) SupersedeVersion(ctx context.Context, command SupersedeVersionCommand) (domain.CatalogSupersessionResult, error) {
	result, _, err := s.run(ctx, command, false)
	return result, err
}

func (s SupersessionService) ReadSupersession(ctx context.Context, command SupersedeVersionCommand) (domain.CatalogSupersessionResult, bool, error) {
	return s.run(ctx, command, true)
}

func (s SupersessionService) run(ctx context.Context, command SupersedeVersionCommand, readOnly bool) (domain.CatalogSupersessionResult, bool, error) {
	op := SupersessionOperation{Capability: s.capability, Command: domain.CatalogSupersessionCommand{
		ID: command.ID, Content: command.Content, Old: command.Old, New: command.New,
		ExpectedAggregate: command.ExpectedAggregate, ExpectedSupersession: command.ExpectedSupersession,
		ExpectedOldReview: command.ExpectedOldReview, ExpectedNewReview: command.ExpectedNewReview,
		Source: s.service.source, Evidence: command.Evidence, Support: command.Support, Reason: command.Reason,
	}}
	var result domain.CatalogSupersessionResult
	var found bool
	err := s.service.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		if err := s.authorize(); err != nil {
			return err
		}
		if _, err := op.Command.CanonicalEncoding(op.Capability); err != nil {
			return supersessionError(err)
		}
		// Keep a private original for response checks even if a broken port
		// mutates the backing arrays of its operation argument.
		expected := op.Command
		expected.Evidence = append([]domain.CatalogSupersessionEvidence(nil), command.Evidence...)
		expected.Support = append([]evidence.ID(nil), command.Support...)
		op.Command.Evidence = append([]domain.CatalogSupersessionEvidence(nil), command.Evidence...)
		op.Command.Support = append([]evidence.ID(nil), command.Support...)
		port, ok := repository.(SupersessionRepository)
		if !ok {
			return ErrUnsupported
		}
		var err error
		result, found, err = port.LookupSupersession(ctx, op)
		if err != nil {
			return supersessionError(err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.authorize(); err != nil {
			return err
		}
		if !found && !readOnly {
			result, err = port.CommitSupersession(ctx, op)
			if err != nil {
				return supersessionError(err)
			}
			found = true
		}
		if err := s.authorize(); err != nil {
			return err
		}
		if !found {
			result = domain.CatalogSupersessionResult{}
			return nil
		}
		result, err = validateSupersessionResponse(expected, result)
		return err
	})
	if err != nil {
		return domain.CatalogSupersessionResult{}, false, err
	}
	return result, found, nil
}

func supersessionError(err error) error {
	if errors.Is(err, domain.ErrVersionIdentityConflict) {
		return domain.ErrCatalogCommandConflict
	}
	return err
}

// Owner-derived order revision and protection require persisted validation by
// the owner. All command-derived fields are compared to the original command,
// including on replay, before the transaction callback may succeed.
func validateSupersessionResponse(c domain.CatalogSupersessionCommand, r domain.CatalogSupersessionResult) (domain.CatalogSupersessionResult, error) {
	if _, err := r.CanonicalEncoding(); err != nil {
		return domain.CatalogSupersessionResult{}, ErrRepositoryUnavailable
	}
	if c.ExpectedAggregate.Uint64() == math.MaxInt64 || c.ExpectedSupersession.Uint64() == math.MaxInt64 ||
		r.Command != c.ID || r.Content != c.Content || r.Old != c.Old || r.New != c.New ||
		r.AggregateRevision.Uint64() != c.ExpectedAggregate.Uint64()+1 || r.SupersessionRevision.Uint64() != c.ExpectedSupersession.Uint64()+1 ||
		r.OldReviewRevision != c.ExpectedOldReview || r.NewReviewRevision != c.ExpectedNewReview || len(r.Support) != len(c.Support) {
		return domain.CatalogSupersessionResult{}, ErrRepositoryUnavailable
	}
	support := append([]evidence.ID(nil), c.Support...)
	if r.OrderRevision.Uint64() > c.ExpectedAggregate.Uint64() {
		return domain.CatalogSupersessionResult{}, ErrRepositoryUnavailable
	}
	sort.Slice(support, func(i, j int) bool { return support[i].String() < support[j].String() })
	for i := range support {
		if support[i] != r.Support[i] {
			return domain.CatalogSupersessionResult{}, ErrRepositoryUnavailable
		}
	}
	r.Support = append([]evidence.ID(nil), r.Support...)
	return r, nil
}

func (s SupersessionService) QuerySupersession(ctx context.Context, query SupersessionQuery) (HistoryPage, error) {
	var result HistoryPage
	err := s.service.run(ctx, func(ctx context.Context, repository CatalogRepository) error {
		// Historical sources may be revoked; only this service's live authority
		// is required. Reconstruction of bytes never grants mutation authority.
		if err := s.authorize(); err != nil {
			return err
		}
		if err := validateSupersessionQuery(query); err != nil {
			return err
		}
		port, ok := repository.(SupersessionRepository)
		if !ok {
			return ErrUnsupported
		}
		page, err := port.QuerySupersession(ctx, query)
		if err != nil {
			return supersessionError(err)
		}
		if err := validateHistoryPage(query, page); err != nil {
			return err
		}
		page.Entries = append([]SupersessionHistoryEntry(nil), page.Entries...)
		for i := range page.Entries {
			page.Entries[i].Command = append([]byte(nil), page.Entries[i].Command...)
			page.Entries[i].Result = append([]byte(nil), page.Entries[i].Result...)
		}
		result = page
		return s.authorize()
	})
	if err != nil {
		return HistoryPage{}, err
	}
	return result, nil
}
