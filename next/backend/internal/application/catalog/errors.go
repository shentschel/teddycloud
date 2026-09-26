package catalog

import "errors"

// ErrRepositoryContention means a repository operation could not complete
// because concurrent access exhausted its bounded wait.
var ErrRepositoryContention = errors.New("catalog repository contention")
