// Package catalog defines application ports for transactional catalog access.
package catalog

import (
	"context"

	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// ContentRepository persists and retrieves Content aggregates.
// Implementations must keep storage-specific values behind this boundary.
type ContentRepository interface {
	Save(context.Context, domaincatalog.Content) error
	FindByID(context.Context, identity.ContentID) (domaincatalog.Content, bool, error)
}

// Transactor executes one operation atomically. The repository is valid only
// for the duration of operation.
type Transactor interface {
	WithinTransaction(context.Context, func(ContentRepository) error) error
}

// CatalogTransactor is distinct from the unmanaged ContentRepository.Save path.
// It invokes the callback exactly once, never retries it, revokes the repository
// on exit and checks ctx.Err immediately before commit. Every method participates
// in the owner's restore fence; no media or network work occurs here.
type CatalogTransactor interface {
	WithinCatalogTransaction(context.Context, func(CatalogRepository) error) error
}

type ReviewOperation struct {
	ExpectedAggregate domaincatalog.CatalogRevision
	Capability        domaincatalog.ReviewCapability
	Command           domaincatalog.CatalogReviewCommand
}

type OrderOperation struct {
	ExpectedAggregate domaincatalog.CatalogRevision
	Capability        domaincatalog.CatalogOrderCapability
	Command           domaincatalog.CatalogOrderCommand
}

type ReviewSnapshot struct {
	AggregateRevision domaincatalog.CatalogRevision
	State             domaincatalog.CatalogReviewState
}

type OrderSnapshot struct {
	AggregateRevision domaincatalog.CatalogRevision
	State             domaincatalog.CatalogOrderState
}

// CatalogRepository is callback-scoped. Lookup compares the complete domain
// canonical command, current capability/principal and ExpectedAggregate, before
// revision rejection. Changed identity conflicts; an exact lookup returns the
// originally committed envelope. Commit atomically compares ExpectedAggregate,
// advances it once and stores state, support and that envelope. The separate
// domain decision revision must not be substituted for the Content revision.
// Missing or corrupt relations fail closed; no unmanaged Save is called.
type CatalogRepository interface {
	LoadReview(context.Context, domaincatalog.CatalogSubject) (ReviewSnapshot, bool, error)
	LoadOrder(context.Context, identity.ContentID) (OrderSnapshot, bool, error)
	LookupReview(context.Context, ReviewOperation) (ReviewResult, bool, error)
	LookupOrder(context.Context, OrderOperation) (OrderResult, bool, error)
	CommitReview(context.Context, ReviewOperation, domaincatalog.CatalogReviewState, ReviewResult) error
	CommitOrder(context.Context, OrderOperation, domaincatalog.CatalogOrderState, OrderResult) error
	Query(context.Context, Query) (QueryResult, error)
}

// CursorCodec is a contract only. An implementation must bind the complete
// typed query, stable continuation ID, aggregate/catalog and owner generations.
// Restore, restart or relevant writes invalidate cursors. No storage or cursor
// encoding implementation is introduced by A1.3a.
type CursorBinding struct {
	Query             Query
	AggregateRevision domaincatalog.CatalogRevision
	CatalogGeneration uint64
	OwnerGeneration   uint64
	AfterContent      identity.ContentID
	AfterVersion      identity.ContentVersionID
}

type CursorCodec interface {
	Encode(CursorBinding) (string, error)
	Validate(string, CursorBinding) error
}
