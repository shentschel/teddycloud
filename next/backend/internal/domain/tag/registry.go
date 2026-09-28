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

// Tag is one immutable registry aggregate. Its retained value is the single
// authority for identity, metadata, revision and history.
type Tag struct {
	retained retainedValue
}

func NewTag(id identity.TagID, uid UID) (Tag, error) {
	if id.IsZero() {
		return Tag{}, ErrZeroTagID
	}
	return newTagFromRetainedView(retainedTagView{
		id:       id,
		uid:      uid,
		revision: InitialRevision,
	})
}

// newTagFromRetainedView is the private reconstitution boundary. The retained
// constructor validates the borrowed view before taking canonical owned copies.
func newTagFromRetainedView(view retainedTagView) (Tag, error) {
	retained, err := newRetainedValue(view)
	if err != nil {
		return Tag{}, err
	}
	return Tag{retained: retained}, nil
}

func (t Tag) ID() identity.TagID { return t.retained.view.id }
func (t Tag) UID() UID           { return t.retained.view.uid }
func (t Tag) RUID() RUID         { return t.UID().RUID() }
func (t Tag) Revision() Revision { return t.retained.view.revision }

func (t Tag) Metadata() Metadata {
	facts := t.retained.view.facts
	return Metadata{
		protocolValid: facts[retainedProtocolValid].state,
		claimed:       facts[retainedClaimed].state,
		cloudAuth:     facts[retainedCloudAuth].state,
		owned:         facts[retainedOwned].state,
	}
}

// Equal compares the complete canonical retained aggregate without encoding it.
func (t Tag) Equal(other Tag) bool {
	return t.retained.equal(other.retained)
}
