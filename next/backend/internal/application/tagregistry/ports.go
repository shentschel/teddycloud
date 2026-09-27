// Package tagregistry registers and reads immutable Tag identities.
package tagregistry

import (
	"context"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

// TagRepository is callback-scoped storage for currently supported identity
// operations. Metadata compare-and-swap is added only with that behavior.
type TagRepository interface {
	FindByID(context.Context, identity.TagID) (domaintag.Tag, bool, error)
	FindByUID(context.Context, domaintag.UID) (domaintag.Tag, bool, error)
	Insert(context.Context, domaintag.Tag) error
}

// Transactor executes one bounded Tag operation atomically. The repository is
// usable only while the callback is running.
type Transactor interface {
	WithinTagTransaction(context.Context, func(TagRepository) error) error
}
