// Package assignment owns the unactivated PI-09 application authority.
package assignment

import (
	"context"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	c "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// AuthenticatedActor is supplied by the trusted port, never by a request.
// External authentication/authorization and runtime wiring remain deferred.
type AuthenticatedActor struct {
	Reference            string
	Read, Decide, Review bool
}
type TagSnapshot struct {
	ID       identity.TagID
	Revision uint64
}

// TargetSnapshot retains exact immutable ownership and accepted catalog facts.
// Superseded and Bound are annotations, never selection/admission shortcuts.
type TargetSnapshot struct {
	Content                              c.Content
	Version                              c.ContentVersion
	CatalogRevision                      uint64
	Managed                              bool
	Ownership, Fingerprint               c.CatalogField
	Dimensions                           d.ContentDimensions
	Bound, Superseded, EditorialConflict bool
}

// Transactor calls operation exactly once, without retries or nesting, with all
// methods on one dependency transaction. It revokes the port on exit, rolls back
// on callback/context failure and checks context immediately before commit.
// Restore/close fence Tag, catalog, assignment and the global command index.
type Transactor interface {
	WithinAssignmentTransaction(context.Context, func(Port) error) error
}

// Port is callback-scoped; implementations must not expose an unmanaged Save.
// Commit atomically compares the aggregate head, enforces unique Assignment/Tag
// and GLOBAL CommandID, appends exactly one event and installs all retained
// material/index/usage. A false-negative lookup must still lose that unique
// fence. No adapter or in-memory implementation is part of this application.
type Port interface {
	Authenticate(context.Context) (AuthenticatedActor, error)
	Tag(context.Context, identity.TagID) (TagSnapshot, bool, error)
	Target(context.Context, d.OriginalTarget) (TargetSnapshot, bool, error)
	Load(context.Context, identity.TagID) (Snapshot, bool, error)
	LookupCommand(context.Context, d.CommandID) (Event, bool, error)
	Commit(context.Context, uint64, Snapshot) error
}

// Snapshot is a bounded transfer value, not a writer. Every loaded event and
// usage total is reconstructed and validated by the service before use.
// Events include the domain primitive bytes, whose local revision is separate
// from the authoritative application Result.Revision (including every kind).
type Snapshot struct {
	ID     d.ID
	TagID  identity.TagID
	Events []Event
	Usage  d.EvidenceUsage
}
type Event struct {
	Command    Command
	Actor      string
	Result     Result
	RecordedAt time.Time
	Primitive  []byte
}
type Result struct {
	CommandID       d.CommandID
	AssignmentID    d.ID
	TagID           identity.TagID
	Revision        uint64
	PreviousCommand d.CommandID
}
