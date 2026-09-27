package catalog

import "errors"

// ErrRepositoryContention means a repository operation could not complete
// because concurrent access exhausted its bounded wait.
var ErrRepositoryContention = errors.New("catalog repository contention")

// ErrRepositoryUnavailable means storage could not complete a repository or
// transaction operation. Storage-specific causes stay behind the application
// boundary.
var ErrRepositoryUnavailable = errors.New("catalog repository unavailable")
