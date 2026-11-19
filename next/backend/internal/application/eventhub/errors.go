package eventhub

import "errors"

var (
	ErrInvalidEvent         = errors.New("invalid event")
	ErrUnsupportedVersion   = errors.New("unsupported event version")
	ErrPayloadTooLarge      = errors.New("event payload too large")
	ErrUnauthorizedProducer = errors.New("unauthorized event producer")
	ErrCapacityExceeded     = errors.New("event capacity exceeded")
	ErrInvalidCursor        = errors.New("invalid event cursor")
	ErrReconcileRequired    = errors.New("event reconciliation required")
	ErrUnavailable          = errors.New("events unavailable")
	ErrStaleObservation     = errors.New("stale observation")
	ErrObservationConflict  = errors.New("observation conflict")
	ErrSourceGap            = errors.New("observation source gap")
	ErrRevoked              = errors.New("event capability revoked")
	ErrUnsupported          = errors.New("events unsupported")
	ErrShutdownIncomplete   = errors.New("event shutdown incomplete")
)
