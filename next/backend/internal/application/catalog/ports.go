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
