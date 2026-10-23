package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var _ app.BaselineRepository = (*catalogRepository)(nil)

const adoptionEncoding = "pi08-baseline-adoption/1"

func (r *catalogRepository) loadBaseline(ctx context.Context, id identity.ContentID) (domain.CatalogBaseline, domain.CatalogRevision, bool, error) {
	if id.IsZero() {
		return domain.CatalogBaseline{}, domain.CatalogRevision{}, false, app.ErrInvalidInput
	}
	var corrupt bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check LIMIT 1)").Scan(&corrupt); err != nil || corrupt {
		return domain.CatalogBaseline{}, domain.CatalogRevision{}, false, app.ErrRepositoryUnavailable
	}
	record, revision, found, err := r.catalogContent(ctx, id)
	if err != nil || !found {
		return domain.CatalogBaseline{}, revision, false, err
	}
	rows, err := r.catalogVersions(ctx, id)
	if err != nil {
		return domain.CatalogBaseline{}, revision, false, err
	}
	versions := make([]domain.ContentVersion, len(rows))
	for i, row := range rows {
		versions[i] = row.Version
	}
	content, _ := domain.NewContent(id, record.Baseline)
	baseline, err := domain.NewCatalogBaseline(content, versions)
	return baseline, revision, true, err
}

func (r *catalogRepository) LoadBaseline(ctx context.Context, id identity.ContentID) (baseline domain.CatalogBaseline, revision domain.CatalogRevision, found bool, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return baseline, revision, false, err
	}
	defer done(&err)
	baseline, revision, found, err = r.loadBaseline(call, id)
	if err != nil {
		return domain.CatalogBaseline{}, domain.CatalogRevision{}, false, err
	}
	return
}

// Adoption is a revision-1 initialization, not an accepted fact decision.
// Its immutable two-record batch retains the snapshot and canonical authorized
// command. Existing review/order envelopes carry all subsequent revisions.
func (r *catalogRepository) AdoptBaseline(ctx context.Context, op app.AdoptionOperation) (result app.AdoptionResult, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	defer func() {
		if err != nil {
			r.failure = err
			result = app.AdoptionResult{}
		}
	}()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, err
	}
	defer done(&err)
	command := op.Command
	canonical, err := command.Canonical(op.Capability)
	if err != nil {
		return result, err
	}
	if err = r.stateSourceActive(call, command.Source); err != nil {
		return result, err
	}
	if err = r.registrationKeyOnly(call, command.ID.String()); err != nil {
		return result, err
	}
	id := command.Subject.ContentID()
	// A retained key is compared before revision rejection, even after later edits.
	var parser, source, registration string
	var digest, snapshot, stored []byte
	var length, count uint64
	err = r.transaction.QueryRowContext(call, `SELECT parser_revision,source_id,registration_revision,artifact_sha256,byte_length,record_count,
 (SELECT CASE WHEN length(snapshot)<=65536 THEN snapshot END FROM tc_catalog_records WHERE batch_id=b.batch_id AND record_index=0),
 (SELECT CASE WHEN length(snapshot)<=65536 THEN snapshot END FROM tc_catalog_records WHERE batch_id=b.batch_id AND record_index=1)
 FROM tc_catalog_batches b WHERE batch_id=?`, command.ID.String()).Scan(&parser, &source, &registration, &digest, &length, &count, &snapshot, &stored)
	if err == nil {
		if parser != adoptionEncoding {
			return result, domain.ErrCatalogCommandConflict
		}
		if len(snapshot) == 0 || len(stored) == 0 {
			return result, app.ErrRepositoryUnavailable
		}
		if !bytes.Equal(stored, canonical) {
			return result, domain.ErrCatalogCommandConflict
		}
		sum := sha256.Sum256(snapshot)
		artifact := adoptionDigest(snapshot, stored)
		if source != command.Source.ID().String() || registration != command.Source.Revision().String() || count != 2 || length != uint64(len(snapshot)+len(stored)) || sum != command.BaselineSHA256 || !bytes.Equal(digest, artifact[:]) {
			return result, app.ErrRepositoryUnavailable
		}
		rawSource, e := command.Source.PersistenceEncoding()
		if e != nil {
			return result, e
		}
		projection := catalogStateReader{raw: rawSource, repository: r, ctx: call}
		projection.source()
		if projection.err != nil {
			return result, projection.err
		}
		var records int
		if e = r.transaction.QueryRowContext(call, "SELECT count(*) FROM tc_catalog_records WHERE batch_id=?", command.ID.String()).Scan(&records); e != nil || records != 2 {
			return result, app.ErrRepositoryUnavailable
		}
		for _, order := range []bool{false, true} {
			states, _, e := r.readStates(call, id, command.Subject, order, domain.CatalogPersistenceBindings{AllowUnbound: true})
			if e != nil {
				return result, e
			}
			if len(states) == 0 {
				return result, app.ErrRepositoryUnavailable
			}
		}
		rows, e := r.transaction.QueryContext(call, "SELECT version_id FROM tc_catalog_order_state_versions WHERE content_id=? AND order_revision=1 ORDER BY version_id", id.String())
		if e != nil {
			return result, repositoryError(call, e)
		}
		var versions []identity.ContentVersionID
		for rows.Next() {
			var text string
			if e = rows.Scan(&text); e != nil {
				break
			}
			var version identity.ContentVersionID
			version, e = identity.ParseContentVersionID(text)
			if e != nil {
				break
			}
			versions = append(versions, version)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return result, app.ErrRepositoryUnavailable
		}
		for _, version := range versions {
			subject, _ := domain.VersionSubject(id, version)
			states, _, e := r.readStates(call, id, subject, false, domain.CatalogPersistenceBindings{AllowUnbound: true})
			if e != nil {
				return result, e
			}
			if len(states) == 0 || states[0].revision != 1 {
				return result, app.ErrRepositoryUnavailable
			}
		}
		return adoptionResult(command.ID), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, repositoryError(call, err)
	}
	baseline, revision, found, err := r.loadBaseline(call, id)
	if err != nil {
		return result, err
	}
	if !found {
		return result, app.ErrSubjectNotFound
	}
	var managed bool
	if err = r.transaction.QueryRowContext(call, "SELECT EXISTS(SELECT 1 FROM tc_catalog_managed WHERE content_id=?)", id.String()).Scan(&managed); err != nil {
		return result, repositoryError(call, err)
	}
	if managed || revision != command.Expected || command.Expected.Uint64() != 1 || baseline.SHA256() != command.BaselineSHA256 {
		return result, domain.ErrCatalogRevisionConflict
	}
	var keyUsed bool
	if err = r.transaction.QueryRowContext(call, "SELECT EXISTS(SELECT 1 FROM tc_catalog_decisions WHERE command_id=?)", command.ID.String()).Scan(&keyUsed); err != nil {
		return result, repositoryError(call, err)
	}
	if keyUsed {
		return result, domain.ErrCatalogCommandConflict
	}
	if _, err = r.transaction.ExecContext(call, "PRAGMA query_only=OFF"); err != nil {
		return result, repositoryError(call, err)
	}
	defer func() {
		if _, e := r.transaction.ExecContext(r.ctx, "PRAGMA query_only=ON"); e != nil && err == nil {
			err = repositoryError(r.ctx, e)
		}
	}()
	// Reuse normalized source validation; core handles remain the sole authority.
	rawSource, err := command.Source.PersistenceEncoding()
	if err != nil {
		return result, err
	}
	projection := catalogStateReader{raw: rawSource, repository: r, ctx: call, write: true}
	projection.source()
	if projection.err != nil {
		return result, projection.err
	}
	exec := func(query string, args ...any) error {
		_, e := r.transaction.ExecContext(call, query, args...)
		return repositoryError(call, e)
	}
	if err = exec("INSERT INTO tc_catalog_managed VALUES (?,1,0)", id.String()); err != nil {
		return result, err
	}
	subjects := []domain.CatalogSubject{command.Subject}
	for _, version := range baseline.Versions() {
		subject, _ := domain.VersionSubject(id, version.ID())
		subjects = append(subjects, subject)
	}
	for _, subject := range subjects {
		var version any
		if !subject.VersionID().IsZero() {
			version = subject.VersionID().String()
		}
		if err = exec("INSERT INTO tc_catalog_subjects VALUES (?,?,?,1)", catalogSubjectKey(subject), id.String(), version); err != nil {
			return result, err
		}
		state, _ := domain.NewCatalogReviewState(subject, false)
		raw, e := state.PersistenceEncoding()
		if e != nil {
			return result, e
		}
		sum := sha256.Sum256(raw)
		if err = exec("INSERT INTO tc_catalog_review_states VALUES (?,?,1,1,1,?,?,?)", catalogSubjectKey(subject), id.String(), raw, len(raw), sum[:]); err != nil {
			return result, err
		}
	}
	order, err := domain.NewCatalogOrderState(id, baseline.Versions())
	if err != nil {
		return result, err
	}
	raw, err := order.PersistenceEncoding()
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(raw)
	if err = exec("INSERT INTO tc_catalog_order_states VALUES (?,1,1,1,?,?,?)", id.String(), raw, len(raw), sum[:]); err != nil {
		return result, err
	}
	for _, version := range baseline.Versions() {
		if err = exec("INSERT INTO tc_catalog_order_state_versions VALUES (?,1,?)", id.String(), version.ID().String()); err != nil {
			return result, err
		}
	}
	snapshot = baseline.Bytes()
	sum = adoptionDigest(snapshot, canonical)
	if err = exec("INSERT INTO tc_catalog_batches VALUES (?,?,?,?,?,2,?,'')", command.ID.String(), command.Source.ID().String(), command.Source.Revision().String(), sum[:], len(snapshot)+len(canonical), adoptionEncoding); err != nil {
		return result, err
	}
	for i, record := range [][]byte{snapshot, canonical} {
		if err = exec("INSERT INTO tc_catalog_records VALUES (?,?,?,?)", command.ID.String(), i, []string{"stored-baseline", "authorized-adoption"}[i], record); err != nil {
			return result, err
		}
	}
	for _, subject := range subjects {
		if _, _, err = r.readStates(call, id, subject, false, domain.CatalogPersistenceBindings{AllowUnbound: true}); err != nil {
			return result, err
		}
	}
	if _, _, err = r.readStates(call, id, command.Subject, true, domain.CatalogPersistenceBindings{AllowUnbound: true}); err != nil {
		return result, err
	}
	r.wrote = true
	r.authorize = append(r.authorize, func() error {
		_, e := command.Canonical(op.Capability)
		if e != nil {
			return e
		}
		return ctx.Err()
	})
	return adoptionResult(command.ID), nil
}

func adoptionResult(id domain.CatalogCommandID) app.AdoptionResult {
	revision, _ := domain.NewCatalogRevision(1)
	return app.AdoptionResult{Command: id, AggregateRevision: revision}
}
func adoptionDigest(snapshot, command []byte) [32]byte {
	h := sha256.New()
	h.Write(snapshot)
	h.Write(command)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
