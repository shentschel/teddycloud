package tagregistry

import "errors"

var (
	ErrInvalidInput          = errors.New("invalid tag registry input")
	ErrTagNotFound           = errors.New("tag not found")
	ErrIdentityConflict      = errors.New("tag identity conflict")
	ErrRepositoryContention  = errors.New("tag repository contention")
	ErrRepositoryUnavailable = errors.New("tag repository unavailable")
)
