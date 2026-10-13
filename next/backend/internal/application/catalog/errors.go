package catalog

import "errors"

// ErrRepositoryContention means a repository operation could not complete
// because concurrent access exhausted its bounded wait.
var ErrRepositoryContention = errors.New("catalog repository contention")

// ErrRepositoryUnavailable means storage could not complete a repository or
// transaction operation. Storage-specific causes stay behind the application
// boundary.
var ErrRepositoryUnavailable = errors.New("catalog repository unavailable")

var (
	ErrInvalidInput     = errors.New("invalid catalog input")
	ErrUnauthorized     = errors.New("catalog operation unauthorized")
	ErrSourceMismatch   = errors.New("catalog source mismatch")
	ErrSubjectMismatch  = errors.New("catalog subject mismatch")
	ErrSubjectNotFound  = errors.New("catalog subject not found")
	ErrRevisionConflict = errors.New("catalog aggregate or decision revision conflict")
	ErrRevisionOverflow = errors.New("catalog revision overflow")
	ErrCommandConflict  = errors.New("catalog command conflict")
	ErrEvidenceConflict = errors.New("catalog evidence identity conflict")
	ErrLimitExceeded    = errors.New("catalog limit exceeded")
	ErrUnsupported      = errors.New("unsupported catalog operation")
	ErrStaleCursor      = errors.New("stale catalog cursor")
	ErrIndeterminate    = errors.New("catalog commit outcome indeterminate")
)
