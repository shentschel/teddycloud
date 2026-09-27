package tag

import (
	"errors"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const InitialRevision Revision = 1

var ErrZeroTagID = errors.New("tag identity is required")

// Revision is the positive, signed-storage-compatible aggregate revision.
type Revision int64

// MetadataState is a closed projection state. Registration never infers one
// metadata fact from another.
type MetadataState uint8

const (
	MetadataUnknown MetadataState = iota
	MetadataObservedTrue
	MetadataObservedFalse
	MetadataConflict
)

// Metadata keeps the four registry facts independent. Its fields are private
// so callers cannot mutate a Tag returned by the registry.
type Metadata struct {
	protocolValid MetadataState
	claimed       MetadataState
	cloudAuth     MetadataState
	owned         MetadataState
}

func (m Metadata) ProtocolValid() MetadataState { return m.protocolValid }
func (m Metadata) Claimed() MetadataState       { return m.claimed }
func (m Metadata) CloudAuth() MetadataState     { return m.cloudAuth }
func (m Metadata) Owned() MetadataState         { return m.owned }

// Tag is one immutable registry identity. Metadata mutation and later
// revisions are intentionally outside this identity checkpoint.
type Tag struct {
	id       identity.TagID
	uid      UID
	revision Revision
	metadata Metadata
}

func NewTag(id identity.TagID, uid UID) (Tag, error) {
	if id.IsZero() {
		return Tag{}, ErrZeroTagID
	}
	return Tag{
		id:       id,
		uid:      uid,
		revision: InitialRevision,
		metadata: Metadata{},
	}, nil
}

func (t Tag) ID() identity.TagID { return t.id }
func (t Tag) UID() UID           { return t.uid }
func (t Tag) RUID() RUID         { return t.uid.RUID() }
func (t Tag) Revision() Revision { return t.revision }
func (t Tag) Metadata() Metadata { return t.metadata }
