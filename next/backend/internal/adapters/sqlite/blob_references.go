package sqlite

import (
	"bytes"
	"context"

	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

// Schema 4 has no digest index on version bindings. Never change its accepted
// checksum or materialize the whole table: inspect at most 1024 rows and fail
// unavailable when absence cannot be established within that bound. A future
// indexed migration can remove this conservative catalog-size restriction.
const referenceScanLimit = 1024

func (s *contentSession) References(ctx context.Context, id content.BlobID) (referenced bool, failure error) {
	if ctx == nil || id.IsZero() {
		return false, contentstore.ErrInvalidInput
	}
	if !s.mutex.TryLock() {
		return false, contentstore.ErrBusy
	}
	defer s.mutex.Unlock()
	if !s.active || s.database == nil || s.owner.generation.Load() != s.generation {
		return false, contentstore.ErrRevoked
	}
	if err := s.ctx.Err(); err != nil {
		return false, err
	}
	bounded, cancel := context.WithTimeout(s.ctx, contentstore.TransactionTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	rows, err := s.database.db.QueryContext(bounded, `SELECT
 CASE WHEN typeof(vb.digest)='blob' AND length(vb.digest)=32 THEN vb.digest END,
 EXISTS(SELECT 1 FROM tc_content_versions v JOIN tc_catalog_content c ON c.content_id=v.content_id WHERE v.version_id=vb.version_id),
 EXISTS(SELECT 1 FROM tc_blobs b WHERE b.digest=vb.digest AND b.algorithm=1 AND b.profile=1 AND b.size BETWEEN 4097 AND 1073741824)
 FROM tc_version_blobs vb ORDER BY vb.version_id LIMIT 1025`)
	if err != nil {
		return false, contentBoundaryError(bounded, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			referenced, failure = false, contentstore.ErrUnavailable
		}
		if err := bounded.Err(); err != nil {
			referenced, failure = false, contentBoundaryError(bounded, err)
		}
	}()
	digest := id.Digest()
	for count := 0; rows.Next(); count++ {
		if count == referenceScanLimit {
			return false, contentstore.ErrUnavailable
		}
		var stored []byte
		var versionPresent, blobPresent bool
		if err := rows.Scan(&stored, &versionPresent, &blobPresent); err != nil {
			return false, contentstore.ErrUnavailable
		}
		if len(stored) != 32 || !versionPresent || !blobPresent {
			return false, contentstore.ErrCorrupt
		}
		if bytes.Equal(stored, digest[:]) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, contentBoundaryError(bounded, err)
	}
	return false, nil
}
