package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

var _ app.ImportRepository = (*catalogRepository)(nil)

// CommitImport shares the owner gate, cancellation fence and single transaction.
// Batch keys inhabit the existing global namespace, including adoption batches.
func (r *catalogRepository) CommitImport(ctx context.Context, op app.ImportOperation) (result app.ImportResult, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	defer func() {
		if err != nil {
			r.failure = err
			result = app.ImportResult{}
		}
	}()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, err
	}
	defer done(&err)
	if r.schemaVersion < 7 {
		return result, app.ErrUnsupported
	}
	if err = op.Authorize(); err != nil {
		return result, err
	}
	return r.commitImport(call, op)
}

func (r *catalogRepository) commitImport(ctx context.Context, op app.ImportOperation) (result app.ImportResult, err error) {
	source := op.Artifact.Source()
	facts := op.Artifact.Facts()
	// BatchID intentionally has no textual parser. Artifact encoding supplies its
	// opaque bytes; it is never derived from payload identifiers or fingerprints.
	key := importBatchKey(facts.ID)
	if err = r.stateSourceActive(ctx, source); err != nil {
		return result, err
	}
	sourceRaw, err := source.PersistenceEncoding()
	if err != nil {
		return result, err
	}
	artifact, report, records := op.Payload.ArtifactBytes(), op.Payload.ReportBytes(), op.Payload.Records()
	var sid, revision, parser, sourceRevision string
	var digest []byte
	var length, count uint64
	err = r.transaction.QueryRowContext(ctx, "SELECT source_id,registration_revision,artifact_sha256,byte_length,record_count,parser_revision,source_revision FROM tc_catalog_batches WHERE batch_id=?", key).Scan(&sid, &revision, &digest, &length, &count, &parser, &sourceRevision)
	if err == nil {
		if sid != source.ID().String() || revision != source.Revision().String() || !bytes.Equal(digest, facts.SHA256[:]) || length != facts.ByteLength || count != facts.RecordCount || parser != facts.ParserRevision || sourceRevision != facts.SourceRevision {
			return result, domain.ErrCatalogArtifactConflict
		}
		reader := catalogStateReader{repository: r, ctx: ctx, raw: sourceRaw}
		reader.source()
		if reader.err != nil {
			return result, reader.err
		}
		var a, b []byte
		var retained uint64
		if e := r.transaction.QueryRowContext(ctx, "SELECT artifact,report,retained_bytes FROM tc_catalog_imports WHERE batch_id=? AND length(artifact)<=1048576 AND length(report)<=8388608", key).Scan(&a, &b, &retained); e != nil {
			return result, app.ErrRepositoryUnavailable
		}
		if !bytes.Equal(a, artifact) || !bytes.Equal(b, report) || retained != op.Payload.RetainedBytes() {
			return result, domain.ErrCatalogArtifactConflict
		}
		var n int
		if e := r.transaction.QueryRowContext(ctx, "SELECT count(*) FROM tc_catalog_records WHERE batch_id=?", key).Scan(&n); e != nil || n != len(records) {
			return result, app.ErrRepositoryUnavailable
		}
		for i, record := range records {
			var raw []byte
			var locator string
			if e := r.transaction.QueryRowContext(ctx, "SELECT locator,CASE WHEN length(snapshot)<=65536 THEN snapshot END FROM tc_catalog_records WHERE batch_id=? AND record_index=?", key, i).Scan(&locator, &raw); e != nil || locator != record.Locator || !bytes.Equal(raw, record.Snapshot.Bytes()) {
				return result, app.ErrRepositoryUnavailable
			}
		}
		return app.ImportResult{Batch: facts.ID, Digest: facts.SHA256, Records: facts.RecordCount}, op.Authorize()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, repositoryError(ctx, err)
	}
	// Admission before writes counts exact retained blob bytes, including record
	// copies and reports; other immutable catalog batches remain charged as well.
	var batches, retained uint64
	if err = r.transaction.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM tc_catalog_batches),
 (SELECT COALESCE(sum(length(snapshot)),0) FROM tc_catalog_records)+
 (SELECT COALESCE(sum(length(artifact)+length(report)),0) FROM tc_catalog_imports)`).Scan(&batches, &retained); err != nil {
		return result, repositoryError(ctx, err)
	}
	if err = domain.CheckLimit(domain.StagingBatches, batches, 1); err != nil {
		return result, err
	}
	if err = domain.CheckLimit(domain.StagingBytes, retained, op.Payload.RetainedBytes()); err != nil {
		return result, err
	}
	var sourceCount uint64
	if err = r.transaction.QueryRowContext(ctx, "SELECT count(*) FROM tc_catalog_sources").Scan(&sourceCount); err != nil {
		return result, repositoryError(ctx, err)
	}
	var exists bool
	if err = r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?)", source.ID().String(), source.Revision().String()).Scan(&exists); err != nil {
		return result, repositoryError(ctx, err)
	}
	if !exists {
		if err = domain.CheckLimit(domain.SourceRegistrations, sourceCount, 1); err != nil {
			return result, err
		}
	}
	if _, err = r.transaction.ExecContext(ctx, "PRAGMA query_only=OFF"); err != nil {
		return result, repositoryError(ctx, err)
	}
	defer func() {
		if _, e := r.transaction.ExecContext(r.ctx, "PRAGMA query_only=ON"); err == nil && e != nil {
			err = repositoryError(r.ctx, e)
		}
	}()
	reader := catalogStateReader{repository: r, ctx: ctx, raw: sourceRaw, write: true}
	reader.source()
	if reader.err != nil {
		return result, reader.err
	}
	exec := func(q string, args ...any) error {
		_, e := r.transaction.ExecContext(ctx, q, args...)
		return repositoryError(ctx, e)
	}
	if err = exec("INSERT INTO tc_catalog_batches VALUES (?,?,?,?,?,?,?,?)", key, source.ID().String(), source.Revision().String(), facts.SHA256[:], facts.ByteLength, facts.RecordCount, facts.ParserRevision, facts.SourceRevision); err != nil {
		return result, err
	}
	for i, record := range records {
		if err = exec("INSERT INTO tc_catalog_records VALUES (?,?,?,?)", key, i, record.Locator, record.Snapshot.Bytes()); err != nil {
			return result, err
		}
	}
	if err = exec("INSERT INTO tc_catalog_imports VALUES (?,?,?,?)", key, artifact, report, op.Payload.RetainedBytes()); err != nil {
		return result, err
	}
	r.wrote = true
	tx := r.transaction
	r.authorize = append(r.authorize, func() error {
		if e := op.Authorize(); e != nil {
			return e
		}
		var revoked int
		if e := tx.QueryRowContext(r.ctx, "SELECT revoked FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?", source.ID().String(), source.Revision().String()).Scan(&revoked); e != nil {
			return repositoryError(r.ctx, e)
		}
		if revoked != 0 {
			return domain.ErrCatalogUnauthorized
		}
		return nil
	})
	return app.ImportResult{Batch: facts.ID, Digest: facts.SHA256, Records: facts.RecordCount}, ctx.Err()
}

func importBatchKey(id domain.BatchID) string { return id.String() }
