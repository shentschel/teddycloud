// Package contentstore defines callback-scoped content persistence ports.
package contentstore

import (
	"context"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

const (
	AdmissionTimeout   = 5 * time.Second
	TransactionTimeout = 5 * time.Second
	OperationTimeout   = 10 * time.Minute
)

// ImportResult is committed metadata, never a present-media availability claim.
type ImportResult struct {
	Version       catalog.ContentVersion
	BlobID        content.BlobID
	CompleteBytes uint64
	Profile       uint16
}

// BlobRepository is revoked before commit/rollback. RecordImport requires
// previously verified, durably published media; it performs only DB work.
type BlobRepository interface {
	RecordImport(context.Context, content.ImportKey, content.ImportCommand) (ImportResult, error)
	LookupImport(context.Context, content.ImportKey, content.ImportCommand) (ImportResult, bool, error)
}

// Session uses the owner's selected database without entering its gate again.
// It must not escape its operation callback or start detached work.
type Session interface {
	WithinTransaction(context.Context, func(BlobRepository) error) error
}

// OperationOwner holds the common restore/close fence for the callback lifetime.
// Use the supplied context for nested calls; recursive gate admission is denied.
type OperationOwner interface {
	WithinContentOperation(context.Context, func(context.Context, Session) error) error
}
