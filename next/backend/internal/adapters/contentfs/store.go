// Package contentfs owns immutable media descriptors. Database/reference and
// lifecycle integration belong to the subsequent content-operation owner.
package contentfs

import (
	"context"
	"errors"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

// Errors contain no paths, digests, source diagnostics or syscall causes.
var (
	ErrUnsupported = errors.New("blob filesystem capability unavailable")
	ErrInvalid     = errors.New("invalid blob store request")
	ErrUnavailable = errors.New("blob store unavailable")
	ErrCorrupt     = errors.New("blob destination corrupt")
	ErrMismatch    = errors.New("blob input does not match declaration")
	ErrCanceled    = errors.New("blob operation canceled")
	ErrBusy        = errors.New("blob operation busy")
	ErrCapacity    = errors.New("blob staging capacity exceeded")
	ErrRetained    = errors.New("blob retained entries require reconciliation")
	ErrUncertain   = errors.New("blob publication durability uncertain")
)

const (
	maxStageBytes   uint64 = 2 * 1024 * 1024 * 1024
	maxStageEntries        = 32
)

// Store is platform-specific and holds its process-level owner lock until
// Close drains publication. No operation returns a descriptor or accepts a
// media path. Open requires an existing absolute deployment-controlled root.
type Store struct{ platformStore }

func Open(ctx context.Context, root string, options content.TAFOptions) (*Store, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if ctx.Err() != nil {
		return nil, ErrCanceled
	}
	if _, err := content.NewTAFOptions(options.MaxBytes(), options.Duration()); err != nil {
		return nil, ErrInvalid
	}
	return openStore(ctx, root, options)
}

// Publish takes ownership of source after valid admission, closes it before
// publication, and synchronously validates the exact original bytes. Source
// must obey the A1 context-aware finite-source contract. completeBytes is the
// declared reservation bound; actual envelope/digest/size must match it.
func (s *Store) Publish(ctx context.Context, id content.BlobID, completeBytes uint64, source content.TAFSource, mode content.TAFSourceMode) (content.TAFEnvelope, error) {
	if s == nil || ctx == nil || id.IsZero() || source == nil {
		return content.TAFEnvelope{}, ErrInvalid
	}
	return s.publish(ctx, id, completeBytes, source, mode)
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.close()
}
