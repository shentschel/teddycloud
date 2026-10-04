package contentstore

import "errors"

var (
	ErrInvalidInput      = errors.New("invalid content store input")
	ErrBusy              = errors.New("content store busy")
	ErrUnavailable       = errors.New("content store unavailable")
	ErrSchemaUnavailable = errors.New("content store schema unavailable")
	ErrCorrupt           = errors.New("content store records corrupt or unavailable")
	ErrRevoked           = errors.New("content store session revoked")
	ErrConflict          = errors.New("content import conflict")
	ErrContentNotFound   = errors.New("content not found")
	ErrCommitUncertain   = errors.New("content import commit uncertain; readback required")
)
