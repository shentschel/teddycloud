// Package contentfs owns immutable media descriptors. Database/reference and
// lifecycle integration belong to the subsequent content-operation owner.
package contentfs

import (
	"context"
	"math"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

// Errors contain no paths, digests, source diagnostics or syscall causes.
var (
	ErrUnsupported = contentstore.ErrUnsupported
	ErrInvalid     = contentstore.ErrInvalidInput
	ErrUnavailable = contentstore.ErrUnavailable
	ErrMissing     = contentstore.ErrMissing
	ErrCorrupt     = contentstore.ErrCorrupt
	ErrMismatch    = contentstore.ErrMismatch
	ErrCanceled    = contentstore.ErrCanceled
	ErrBusy        = contentstore.ErrBusy
	ErrCapacity    = contentstore.ErrCapacity
	ErrRetained    = contentstore.ErrRetained
	ErrUncertain   = contentstore.ErrUncertain
	ErrCursor      = contentstore.ErrCursor
)

const (
	DefaultInventoryPage  = 128
	MaxInventoryInspected = 1024
	MaxInventoryCursor    = 256
	InventoryDuration     = 2 * time.Second
	InventoryIdleExpiry   = 60 * time.Second
)

type InventoryKind = contentstore.InventoryKind

const (
	ReferencedCanonical   = contentstore.ReferencedCanonical
	UnreferencedCanonical = contentstore.UnreferencedCanonical
	StagingEntry          = contentstore.StagingEntry
	QuarantineEntry       = contentstore.QuarantineEntry
	UnexpectedEntry       = contentstore.UnexpectedEntry
)

// Inventory is observational. Entries remain unchecked until explicit envelope
// verification; BlobID is populated only for canonical candidates. No entry
// grants deletion authority, and an orphan label is scoped to the gated instant.
type InventoryEntry = contentstore.InventoryEntry
type InventoryPage = contentstore.InventoryPage

// ReferenceLookup runs inside the caller's common content-operation gate against
// its selected DB/session, honors ctx, and does not re-enter Store. Reference
// mutations and restore must call InvalidateInventory under the same gate.
type ReferenceLookup = contentstore.ReferenceLookup

// OpenForInventory permits retained-file observation after restart. Mutations
// remain disabled until a complete clean scan has reconciled retained capacity.
// Ordinary Open preserves its fail-closed ErrRetained behavior.
func OpenForInventory(ctx context.Context, root string, options content.TAFOptions) (*Store, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if ctx.Err() != nil {
		return nil, ErrCanceled
	}
	if _, err := content.NewTAFOptions(options.MaxBytes(), options.Duration()); err != nil {
		return nil, ErrInvalid
	}
	return openForInventory(ctx, root, options)
}

const (
	maxStageBytes   uint64 = 2 * 1024 * 1024 * 1024
	maxStageEntries        = 32
)

const (
	DefaultRangeBytes    uint64 = 8 * 1048576
	HardRangeBytes       uint64 = 64 * 1048576
	DefaultRangeDuration        = 60 * time.Second
	HardRangeDuration           = 120 * time.Second
)

// RangeOptions is immutable for the lifetime of an owner; zero is invalid.
type RangeOptions struct {
	maxBytes uint64
	duration time.Duration
}

func NewRangeOptions(maxBytes uint64, duration time.Duration) (RangeOptions, error) {
	if maxBytes == 0 || maxBytes > HardRangeBytes || duration <= 0 || duration > HardRangeDuration {
		return RangeOptions{}, ErrInvalid
	}
	return RangeOptions{maxBytes, duration}, nil
}
func DefaultRangeOptions() RangeOptions { return RangeOptions{DefaultRangeBytes, DefaultRangeDuration} }

// ByteRange denotes complete-file bytes [offset, offset+length).
type ByteRange struct{ offset, length uint64 }

func NewByteRange(offset, length int64) (ByteRange, error) {
	if offset < 0 || length < 0 || offset > math.MaxInt64-length {
		return ByteRange{}, ErrInvalid
	}
	return ByteRange{uint64(offset), uint64(length)}, nil
}

// RangeSink must honor context and Close must unblock pending Write. Ownership
// transfers only after admission and ends before ReadRange returns. Write must
// consume its slice synchronously; no descriptor or retained buffer is exposed.
type RangeSink interface {
	Write(context.Context, []byte) (int, error)
	Close() error
}

// ReadRange verifies all original bytes before the first sink call, including
// empty ranges. Success certifies delivery and unchanged descriptor observations,
// never playback or protection against a hostile same-owner writer.
func (s *Store) ReadRange(ctx context.Context, id content.BlobID, completeBytes uint64, r ByteRange, sink RangeSink) error {
	if s == nil || ctx == nil || id.IsZero() || sink == nil || completeBytes < content.MinTAFBytes || completeBytes > content.HardTAFBytes || r.offset > completeBytes || r.length > completeBytes-r.offset {
		return ErrInvalid
	}
	return s.readRange(ctx, id, completeBytes, r, sink)
}

// Store is platform-specific and holds its process-level owner lock until
// Close drains publication. No operation returns a descriptor or accepts a
// media path. Open requires an existing absolute deployment-controlled root.
type Store struct{ platformStore }

var _ contentstore.MediaStore = (*Store)(nil)

// Verify uses the verified range protocol without delivering content bytes.
// The digest lease and descriptor remain scoped until verification returns.
func (s *Store) Verify(ctx context.Context, id content.BlobID, size uint64) error {
	return s.ReadRange(ctx, id, size, ByteRange{}, verificationSink{})
}

type verificationSink struct{}

func (verificationSink) Write(_ context.Context, bytes []byte) (int, error) { return len(bytes), nil }
func (verificationSink) Close() error                                       { return nil }

func Open(ctx context.Context, root string, options content.TAFOptions) (*Store, error) {
	return OpenWithRangeOptions(ctx, root, options, DefaultRangeOptions())
}

func OpenWithRangeOptions(ctx context.Context, root string, options content.TAFOptions, ranges RangeOptions) (*Store, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if ctx.Err() != nil {
		return nil, ErrCanceled
	}
	if _, err := content.NewTAFOptions(options.MaxBytes(), options.Duration()); err != nil {
		return nil, ErrInvalid
	}
	if _, err := NewRangeOptions(ranges.maxBytes, ranges.duration); err != nil {
		return nil, ErrInvalid
	}
	s, err := openStore(ctx, root, options)
	if err == nil {
		s.setRangeOptions(ranges)
	}
	return s, err
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
