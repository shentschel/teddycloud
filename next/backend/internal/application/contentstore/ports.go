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
	References(context.Context, content.BlobID) (bool, error)
}

// OperationOwner holds the common restore/close fence for the callback lifetime.
// Use the supplied context for nested calls; recursive gate admission is denied.
type OperationOwner interface {
	WithinContentOperation(context.Context, func(context.Context, Session) error) error
}

// InventoryInvalidator is called while the lifecycle gate is held, before a
// reference mutation or database switch. It must not re-enter that gate.
type InventoryInvalidator interface{ InvalidateInventory() error }

type MediaOwner interface {
	OperationOwner
	AttachContentInventory(context.Context, InventoryInvalidator) error
}

type InventoryKind string

const (
	ReferencedCanonical   InventoryKind = "referenced"
	UnreferencedCanonical InventoryKind = "unreferenced"
	StagingEntry          InventoryKind = "staging"
	QuarantineEntry       InventoryKind = "quarantine"
	UnexpectedEntry       InventoryKind = "unexpected"
)

type InventoryEntry struct {
	Kind   InventoryKind
	BlobID content.BlobID
	Bytes  uint64
}
type InventoryPage struct {
	Entries   []InventoryEntry
	Inspected int
	Cursor    string
	Complete  bool
}
type ReferenceLookup func(context.Context, content.BlobID) (bool, error)

// MediaStore owns the digest lease and descriptors within each synchronous
// call. The service acquires the lifecycle gate first; no SQL transaction spans
// filesystem I/O. Implementations return only the stable application errors.
type MediaStore interface {
	InventoryInvalidator
	Publish(context.Context, content.BlobID, uint64, content.TAFSource, content.TAFSourceMode) (content.TAFEnvelope, error)
	Inventory(context.Context, string, int, ReferenceLookup) (InventoryPage, error)
	Quarantine(context.Context, content.BlobID, uint64) error
	Verify(context.Context, content.BlobID, uint64) error
}
