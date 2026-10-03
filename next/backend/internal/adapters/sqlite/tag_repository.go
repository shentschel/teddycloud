package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var _ applicationtag.TagRepository = (*tagRepository)(nil)

// The scope lock drains any in-flight call before the callback returns and
// revokes the repository before commit/rollback or a lifecycle handle switch.
type tagRepository struct {
	mutex       sync.Mutex
	transaction *sql.Tx
}

func (repository *tagRepository) invalidate() {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.transaction = nil
}

func (repository *tagRepository) FindByID(ctx context.Context, id identity.TagID) (domaintag.Tag, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.transaction == nil {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	if id.IsZero() {
		return domaintag.Tag{}, false, applicationtag.ErrInvalidInput
	}
	return repository.find(ctx, tagIdentityColumns+` WHERE tag_id = ?`, id.String())
}

func (repository *tagRepository) FindByUID(ctx context.Context, uid domaintag.UID) (domaintag.Tag, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.transaction == nil {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	bytes := uid.Bytes()
	return repository.find(ctx, tagIdentityColumns+` WHERE uid = ?`, bytes[:])
}

// CASE guards bound driver materialization even if constraints were bypassed
// by a corrupt/imported database. Parse the bounded values again in the domain.
const tagIdentityColumns = `SELECT
	CASE WHEN typeof(tag_id) = 'text' AND length(CAST(tag_id AS BLOB)) = 30 THEN tag_id END,
	CASE WHEN typeof(uid) = 'blob' AND length(uid) = 8 THEN uid END,
	CASE WHEN typeof(revision) = 'integer' THEN revision END
	FROM tc_tags`

func (repository *tagRepository) find(ctx context.Context, query string, key any) (domaintag.Tag, bool, error) {
	var idText sql.NullString
	var uidBytes []byte
	var revision sql.NullInt64
	err := repository.transaction.QueryRowContext(ctx, query, key).Scan(&idText, &uidBytes, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return domaintag.Tag{}, false, nil
	}
	if err != nil {
		return domaintag.Tag{}, false, tagStorageError(ctx, err)
	}
	// Migration 2 represents only an initial identity. Later revisions cannot
	// be reconstructed without B1 evidence; never silently discard that state.
	if !idText.Valid || len(uidBytes) != 8 || !revision.Valid || revision.Int64 != int64(domaintag.InitialRevision) {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	id, err := identity.ParseTagID(idText.String)
	if err != nil {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	var bytes [8]byte
	copy(bytes[:], uidBytes)
	value, err := domaintag.NewTag(id, domaintag.UIDFromBytes(bytes))
	if err != nil {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	return value, true, nil
}

func (repository *tagRepository) Insert(ctx context.Context, value domaintag.Tag) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.transaction == nil {
		return applicationtag.ErrRepositoryUnavailable
	}
	initial, err := domaintag.NewTag(value.ID(), value.UID())
	if err != nil || !value.Equal(initial) {
		return applicationtag.ErrInvalidInput
	}
	bytes := value.UID().Bytes()
	return tagBoundaryError(ctx, execWithContention(ctx, repository.transaction, tagStorageError,
		`INSERT INTO tc_tags (tag_id, uid, revision) VALUES (?, ?, ?)`,
		value.ID().String(), bytes[:], int64(value.Revision()),
	))
}

func tagStorageError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return tagBoundaryError(ctx, err)
	}
	var driverError *sqliteDriver.Error
	if errors.As(err, &driverError) {
		switch driverError.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			return applicationtag.ErrIdentityConflict
		}
		if code := driverError.Code() & 0xff; code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED {
			return applicationtag.ErrRepositoryContention
		}
	}
	return applicationtag.ErrRepositoryUnavailable
}
