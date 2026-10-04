package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	sqlite3 "modernc.org/sqlite/lib"
)

// contentSession holds no transaction while media is received or published.
// Its mutex drains an admitted DB call before revocation releases the gate.
type contentSession struct {
	mutex      sync.Mutex
	owner      *LifecycleOwner
	database   *Database
	generation uint64
	ctx        context.Context
	active     bool
	// Adapter-private failure seams exercise ambiguous commit and readback.
	beforeCommit func() error
	commit       func(*sql.Tx) error
}

func (s *contentSession) revoke() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.active = false
	s.database = nil
}

func (s *contentSession) WithinTransaction(ctx context.Context, operation func(contentstore.BlobRepository) error) error {
	if operation == nil {
		return contentstore.ErrInvalidInput
	}
	if !s.mutex.TryLock() {
		return contentstore.ErrBusy
	}
	defer s.mutex.Unlock()
	if !s.active || s.owner.generation.Load() != s.generation || s.database == nil {
		return contentstore.ErrRevoked
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	txContext, cancel := context.WithTimeout(s.ctx, contentstore.TransactionTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.transact(txContext, func(repository contentstore.BlobRepository) error {
		callbackErr := operation(repository)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return callbackErr
	})
	if ctx.Err() != nil {
		if err == nil {
			return errors.Join(contentstore.ErrCommitUncertain, ctx.Err())
		}
		return errors.Join(contentCommitMarker(err), ctx.Err())
	}
	return contentBoundaryError(txContext, err)
}

func (s *contentSession) transact(ctx context.Context, operation func(contentstore.BlobRepository) error) (result error) {
	connection, err := s.database.db.Conn(ctx)
	if err != nil {
		return contentBoundaryError(ctx, err)
	}
	defer connection.Close()
	committed := false
	var timeout int
	if err := connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		return contentBoundaryError(ctx, err)
	}
	defer func() {
		restoreContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := connection.ExecContext(restoreContext, busyTimeoutPragma(timeout)); err != nil {
			if committed {
				result = errors.Join(result, contentstore.ErrCommitUncertain)
			}
			result = errors.Join(result, contentBoundaryError(restoreContext, err))
		}
	}()
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return contentBoundaryError(ctx, err)
	}
	defer transaction.Rollback()
	repository := &blobRepository{transaction: transaction, ctx: ctx}
	defer repository.revoke()
	callbackErr := operation(repository)
	repository.revoke()
	if callbackErr != nil {
		return contentBoundaryError(ctx, callbackErr)
	}
	if repository.failure != nil {
		return repository.failure
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			return contentBoundaryError(ctx, err)
		}
	}
	commit := transaction.Commit
	if s.commit != nil {
		commit = func() error { return s.commit(transaction) }
	}
	if err := commit(); err != nil {
		return errors.Join(contentstore.ErrCommitUncertain, contentBoundaryError(ctx, err))
	}
	committed = true
	return nil
}

func contentCommitMarker(err error) error {
	if errors.Is(err, contentstore.ErrCommitUncertain) {
		return contentstore.ErrCommitUncertain
	}
	return nil
}

func contentBoundaryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	marker := contentCommitMarker(err)
	if ctx.Err() != nil {
		return errors.Join(marker, ctx.Err())
	}
	for _, category := range []error{context.Canceled, context.DeadlineExceeded, contentstore.ErrInvalidInput, contentstore.ErrBusy, contentstore.ErrSchemaUnavailable, contentstore.ErrCorrupt, contentstore.ErrRevoked, contentstore.ErrConflict, contentstore.ErrContentNotFound, contentstore.ErrUnavailable} {
		if errors.Is(err, category) {
			return errors.Join(marker, category)
		}
	}
	if code, ok := sqlitePrimaryCode(err); ok && (code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED) {
		return errors.Join(marker, contentstore.ErrBusy)
	}
	return errors.Join(marker, contentstore.ErrUnavailable)
}

type blobRepository struct {
	mutex       sync.Mutex
	transaction *sql.Tx
	ctx         context.Context
	failure     error
}

var _ contentstore.Session = (*contentSession)(nil)
var _ contentstore.BlobRepository = (*blobRepository)(nil)

func (r *blobRepository) revoke() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.transaction = nil
}

// Each call is bounded by both callback and caller context. Invalid/failed
// writes poison the transaction even if a callback ignores their result.
func (r *blobRepository) RecordImport(ctx context.Context, key content.ImportKey, command content.ImportCommand) (result contentstore.ImportResult, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.transaction == nil {
		return result, contentstore.ErrRevoked
	}
	callContext, cancel := context.WithCancel(r.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if ctx.Err() != nil {
		cancel()
	}
	defer func() {
		if err != nil {
			r.failure = err
		}
	}()
	if key.IsZero() || command.IsZero() {
		return result, contentstore.ErrInvalidInput
	}
	if r.failure != nil {
		return result, r.failure
	}
	if old, found, e := r.lookup(callContext, key, command); e != nil || found {
		return old, e
	}
	version, versionFound, e := r.loadVersion(callContext, command.Version().ID())
	if e != nil {
		return result, e
	}
	existing, found, e := (contentRepository{transaction: r.transaction}).FindByID(callContext, command.Version().ContentID())
	if e != nil {
		return result, contentBoundaryError(callContext, e)
	}
	if !found || existing.ID() != command.Version().ContentID() {
		return result, contentstore.ErrContentNotFound
	}
	// Validate existing references before inserting anything: re-import repairs
	// media in B2, never silently repairs corrupt relational rows here.
	if versionFound {
		if version.Version != command.Version() || version.BlobID != command.BlobID() {
			return result, contentstore.ErrConflict
		}
		if version.CompleteBytes != command.CompleteBytes() || version.Profile != command.Profile() {
			return result, contentstore.ErrCorrupt
		}
	}
	digest := command.BlobID().Digest()
	var size, algorithm, profile sql.NullInt64
	e = r.transaction.QueryRowContext(callContext, `SELECT CASE WHEN typeof(size)='integer' THEN size END, CASE WHEN typeof(algorithm)='integer' THEN algorithm END, CASE WHEN typeof(profile)='integer' THEN profile END FROM tc_blobs WHERE digest=?`, digest[:]).Scan(&size, &algorithm, &profile)
	if errors.Is(e, sql.ErrNoRows) {
		if e = r.exec(callContext, `INSERT INTO tc_blobs(digest,algorithm,size,profile) VALUES(?,1,?,1)`, digest[:], int64(command.CompleteBytes())); e != nil {
			return result, e
		}
	} else if e != nil {
		return result, contentBoundaryError(callContext, e)
	} else if !size.Valid || !algorithm.Valid || !profile.Valid || algorithm.Int64 != 1 || profile.Int64 != 1 || size.Int64 != int64(command.CompleteBytes()) {
		return result, contentstore.ErrCorrupt
	}
	if !versionFound {
		v := command.Version()
		hash, _ := hex.DecodeString(v.Fingerprint().Hash().String())
		known := 0
		var namespace, position any
		if v.OrderEvidence().Known() {
			known = 1
			namespace = v.OrderEvidence().Namespace()
			position = binary.BigEndian.AppendUint64(nil, v.OrderEvidence().Position())
		}
		if e = r.exec(callContext, `INSERT INTO tc_content_versions(version_id,content_id,audio_id,audio_sha1,order_known,order_namespace,order_position) VALUES(?,?,?,?,?,?,?)`, v.ID().String(), v.ContentID().String(), int64(v.Fingerprint().AudioID().Uint64()), hash, known, namespace, position); e != nil {
			return result, e
		}
		if e = r.exec(callContext, `INSERT INTO tc_version_blobs(version_id,digest) VALUES(?,?)`, v.ID().String(), digest[:]); e != nil {
			return result, e
		}
	}
	fingerprint := command.Fingerprint()
	if e = r.exec(callContext, `INSERT INTO tc_blob_imports(import_key,encoding,command,fingerprint,version_id) VALUES(?,1,?,?,?)`, key.String(), command.CanonicalBytes(), fingerprint[:], command.Version().ID().String()); e != nil {
		return result, e
	}
	return importResult(command), nil
}

func importResult(c content.ImportCommand) contentstore.ImportResult {
	return contentstore.ImportResult{Version: c.Version(), BlobID: c.BlobID(), CompleteBytes: c.CompleteBytes(), Profile: c.Profile()}
}

func (r *blobRepository) exec(ctx context.Context, query string, args ...any) error {
	return execWithContention(ctx, r.transaction, contentBoundaryError, query, args...)
}

func (r *blobRepository) LookupImport(ctx context.Context, key content.ImportKey, command content.ImportCommand) (contentstore.ImportResult, bool, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.transaction == nil {
		return contentstore.ImportResult{}, false, contentstore.ErrRevoked
	}
	if key.IsZero() || command.IsZero() {
		return contentstore.ImportResult{}, false, contentstore.ErrInvalidInput
	}
	callContext, cancel := context.WithCancel(r.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if ctx.Err() != nil {
		cancel()
	}
	return r.lookup(callContext, key, command)
}

func (r *blobRepository) lookup(ctx context.Context, key content.ImportKey, command content.ImportCommand) (contentstore.ImportResult, bool, error) {
	var encoding sql.NullInt64
	var data, hash []byte
	var versionText sql.NullString
	err := r.transaction.QueryRowContext(ctx, `SELECT
 CASE WHEN typeof(encoding)='integer' THEN encoding END,
 CASE WHEN typeof(command)='blob' AND length(command) BETWEEN 137 AND 275 THEN command END,
 CASE WHEN typeof(fingerprint)='blob' AND length(fingerprint)=32 THEN fingerprint END,
 CASE WHEN typeof(version_id)='text' AND length(CAST(version_id AS BLOB))=30 THEN version_id END
 FROM tc_blob_imports WHERE import_key=?`, key.String()).Scan(&encoding, &data, &hash, &versionText)
	if errors.Is(err, sql.ErrNoRows) {
		return contentstore.ImportResult{}, false, nil
	}
	if err != nil {
		return contentstore.ImportResult{}, false, contentBoundaryError(ctx, err)
	}
	if !encoding.Valid || encoding.Int64 != 1 || len(hash) != 32 || !versionText.Valid {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	stored, err := content.ParseImportCommand(data)
	if err != nil {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	fingerprint := stored.Fingerprint()
	if !bytes.Equal(hash, fingerprint[:]) || stored.Version().ID().String() != versionText.String {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	result, found, err := r.loadVersion(ctx, stored.Version().ID())
	if err != nil {
		return contentstore.ImportResult{}, false, err
	}
	if !found || result != importResult(stored) {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	if !stored.SameCommand(command) {
		return contentstore.ImportResult{}, false, contentstore.ErrConflict
	}
	return result, true, nil
}

// LEFT JOIN distinguishes missing referenced rows from an absent version.
// CASE bounds all variable materialization even with bypassed SQL constraints.
func (r *blobRepository) loadVersion(ctx context.Context, id identity.ContentVersionID) (contentstore.ImportResult, bool, error) {
	var contentText, namespace sql.NullString
	var audio, known, size, algorithm, profile sql.NullInt64
	var hash, position, digest []byte
	var contentExists, namespaceNull, positionNull bool
	err := r.transaction.QueryRowContext(ctx, `SELECT
 CASE WHEN typeof(v.content_id)='text' AND length(CAST(v.content_id AS BLOB))=30 THEN v.content_id END,
 CASE WHEN typeof(v.audio_id)='integer' THEN v.audio_id END,
 CASE WHEN typeof(v.audio_sha1)='blob' AND length(v.audio_sha1)=20 THEN v.audio_sha1 END,
 CASE WHEN typeof(v.order_known)='integer' THEN v.order_known END,
 CASE WHEN v.order_namespace IS NULL OR (typeof(v.order_namespace)='text' AND length(CAST(v.order_namespace AS BLOB)) BETWEEN 1 AND 128) THEN v.order_namespace END,
 CASE WHEN typeof(v.order_position)='blob' AND length(v.order_position)=8 THEN v.order_position END,
 CASE WHEN typeof(b.digest)='blob' AND length(b.digest)=32 THEN b.digest END,
 CASE WHEN typeof(b.size)='integer' THEN b.size END,
 CASE WHEN typeof(b.algorithm)='integer' THEN b.algorithm END,
 CASE WHEN typeof(b.profile)='integer' THEN b.profile END,
 EXISTS(SELECT 1 FROM tc_catalog_content c WHERE c.content_id=v.content_id),
 (v.order_namespace IS NULL), (v.order_position IS NULL)
 FROM tc_content_versions v LEFT JOIN tc_version_blobs vb ON vb.version_id=v.version_id LEFT JOIN tc_blobs b ON b.digest=vb.digest WHERE v.version_id=?`, id.String()).Scan(&contentText, &audio, &hash, &known, &namespace, &position, &digest, &size, &algorithm, &profile, &contentExists, &namespaceNull, &positionNull)
	if errors.Is(err, sql.ErrNoRows) {
		var dangling bool
		if err := r.transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tc_version_blobs WHERE version_id=?) OR EXISTS(SELECT 1 FROM tc_blob_imports WHERE version_id=?)`, id.String(), id.String()).Scan(&dangling); err != nil {
			return contentstore.ImportResult{}, false, contentBoundaryError(ctx, err)
		}
		if dangling {
			return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
		}
		return contentstore.ImportResult{}, false, nil
	}
	if err != nil {
		return contentstore.ImportResult{}, false, contentBoundaryError(ctx, err)
	}
	if !contentText.Valid || !audio.Valid || audio.Int64 < 1 || audio.Int64 > 4294967295 || len(hash) != 20 || !known.Valid || known.Int64 < 0 || known.Int64 > 1 || !size.Valid || size.Int64 < int64(content.MinTAFBytes) || size.Int64 > int64(content.HardTAFBytes) || !algorithm.Valid || algorithm.Int64 != 1 || !profile.Valid || profile.Int64 != 1 || len(digest) != 32 || !contentExists {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	contentID, err := identity.ParseContentID(contentText.String)
	if err != nil {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	order := catalog.UnknownVersionOrderEvidence()
	if known.Int64 == 0 {
		if !namespaceNull || !positionNull {
			return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
		}
	} else {
		if !namespace.Valid || len(position) != 8 {
			return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
		}
		order, err = catalog.NewVersionOrderEvidence(namespace.String, binary.BigEndian.Uint64(position))
		if err != nil {
			return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
		}
	}
	audioID, err := catalog.NewAudioID(uint64(audio.Int64))
	if err != nil {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	audioHash, err := catalog.ParseAudioHash(hex.EncodeToString(hash))
	if err != nil {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	fingerprint, err := catalog.NewAudioFingerprint(audioID, audioHash)
	if err != nil {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	version, err := catalog.NewContentVersion(id, contentID, fingerprint, order)
	if err != nil {
		return contentstore.ImportResult{}, false, contentstore.ErrCorrupt
	}
	var blobDigest [32]byte
	copy(blobDigest[:], digest)
	return contentstore.ImportResult{Version: version, BlobID: content.NewBlobID(blobDigest), CompleteBytes: uint64(size.Int64), Profile: uint16(profile.Int64)}, true, nil
}
