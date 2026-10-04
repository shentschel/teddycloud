// Package contentfs owns immutable media descriptors. Database/reference and
// lifecycle integration belong to the subsequent content-operation owner.
package contentfs

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

// Errors contain no paths, digests, source diagnostics or syscall causes.
var (
	ErrUnsupported = errors.New("blob filesystem capability unavailable")
	ErrInvalid     = errors.New("invalid blob store request")
	ErrUnavailable = errors.New("blob store unavailable")
	ErrMissing     = errors.New("blob content missing")
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
