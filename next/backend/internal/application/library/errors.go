package library

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput     = errors.New("library: invalid input")
	ErrLimitExceeded    = errors.New("library: limit exceeded")
	ErrUnauthorized     = errors.New("library: unauthorized")
	ErrUnsupported      = errors.New("library: unsupported")
	ErrStaleCursor      = errors.New("library: stale cursor")
	ErrIndexNotReady    = errors.New("library: index not ready")
	ErrUnavailable      = errors.New("library: unavailable")
	ErrBusy             = errors.New("library: busy")
	ErrCanceled         = context.Canceled
	ErrDeadlineExceeded = context.DeadlineExceeded
)

// BoundaryError preserves only the public category. No driver or caller text
// crosses the Library boundary, including joined cleanup failures.
func BoundaryError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	for _, category := range []error{ErrCanceled, ErrDeadlineExceeded, ErrUnauthorized, ErrInvalidInput, ErrLimitExceeded, ErrUnsupported, ErrStaleCursor, ErrIndexNotReady, ErrBusy, ErrUnavailable} {
		if errors.Is(err, category) {
			return category
		}
	}
	return ErrUnavailable
}
