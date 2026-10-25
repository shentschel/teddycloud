package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	contentdomain "github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func (r *catalogRepository) Query(ctx context.Context, query app.Query) (result app.QueryResult, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if ctx == nil {
		return result, app.ErrInvalidInput
	}
	if r.transaction == nil || r.owner.generation.Load() != r.generation {
		return result, app.ErrRepositoryUnavailable
	}
	call, cancel := context.WithCancel(r.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	defer func() {
		if err != nil {
			r.failure = err
		}
	}()
	if query.Kind == app.ListCurrentVersions {
		return result, app.ErrUnsupported
	}
	if err := catalogQueryInput(query); err != nil {
		return result, err
	}
	if query.Kind == app.FindVersionsByPair || query.Kind == app.FindVersionsByAudioID {
		return r.catalogFind(call, query)
	}
	record, revision, found, err := r.catalogContent(call, query.Content)
	if err != nil {
		return result, err
	}
	result.Revision = revision
	if query.Page.ExpectedRevision.Uint64() != 0 && query.Page.ExpectedRevision != revision {
		return app.QueryResult{}, app.ErrStaleCursor
	}
	if !found {
		if query.Page.Cursor != "" {
			return app.QueryResult{}, app.ErrStaleCursor
		}
		result.Kind, result.Complete = app.NoMatch, true
		return catalogPageSize(result)
	}
	if query.Kind == app.GetContent {
		result.Entries = []app.CatalogRecord{record}
		result.Inspected = 1
		result.Complete = true
		result.Kind = catalogResultKind(result.Entries)
		return catalogPageSize(result)
	}
	versions, err := r.catalogVersions(call, record.Content)
	if err != nil {
		return app.QueryResult{}, err
	}
	result.Inspected = uint64(len(versions))
	// Hash the entire bounded relevant snapshot, including baseline/projections
	// and media relations. Raw legacy Save and PI-07 attachment do not advance
	// the managed revision, so revision alone is insufficient for continuation.
	snapshot := sha256.New()
	base, err := catalogRecordBytes(record)
	if err != nil {
		return app.QueryResult{}, err
	}
	snapshot.Write(base)
	if err := r.registrationSnapshot(call, record.Content, snapshot); err != nil {
		return app.QueryResult{}, err
	}
	// The single pooled connection has a stable total_changes counter; SQLite's
	// data_version catches other connections. Together they also invalidate an
	// ABA write whose final values happen to equal the old snapshot.
	var changes, dataVersion uint64
	if err := r.transaction.QueryRowContext(call, "SELECT total_changes()").Scan(&changes); err != nil {
		return app.QueryResult{}, repositoryError(call, err)
	}
	if err := r.transaction.QueryRowContext(call, "PRAGMA data_version").Scan(&dataVersion); err != nil {
		return app.QueryResult{}, repositoryError(call, err)
	}
	var generations [16]byte
	binary.BigEndian.PutUint64(generations[:8], changes)
	binary.BigEndian.PutUint64(generations[8:], dataVersion)
	snapshot.Write(generations[:])
	for _, version := range versions {
		encoded, err := catalogVersionBytes(version)
		if err != nil {
			return app.QueryResult{}, err
		}
		snapshot.Write(encoded)
		snapshot.Write(version.Digest[:])
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], version.Size)
		snapshot.Write(size[:])
	}
	var digest [32]byte
	copy(digest[:], snapshot.Sum(nil))
	after := ""
	if query.Page.Cursor != "" {
		after, err = r.catalogCursorAfter(query, revision, digest)
		if err != nil {
			return app.QueryResult{}, err
		}
	}
	start := 0
	if after != "" {
		start = sort.Search(len(versions), func(i int) bool { return versions[i].Version.ID().String() >= after })
		if start == len(versions) || versions[start].Version.ID().String() != after {
			return app.QueryResult{}, app.ErrStaleCursor
		}
		start++
	}
	charged := uint64(512 + len(base))
	for i := start; i < len(versions); i++ {
		if query.Kind == app.GetVersion && versions[i].Version.ID() != query.Version {
			continue
		}
		versionBytes, err := catalogVersionBytes(versions[i])
		if err != nil {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		added := uint64(len(base) + len(versionBytes) + 32)
		if uint64(len(result.Entries)) == query.Page.Limit || charged+added > domain.QueryBytes.Maximum() {
			if len(result.Entries) == 0 {
				return app.QueryResult{}, app.ErrLimitExceeded
			}
			result.Next, err = r.catalogCursor(query, revision, digest, result.Entries[len(result.Entries)-1].Version.ID().String())
			if err != nil {
				return app.QueryResult{}, err
			}
			result.Kind = app.Incomplete
			return catalogPageSize(result)
		}
		charged += added
		entry := record
		entry.Version, entry.Media = versions[i].Version, versions[i].Media
		result.Entries = append(result.Entries, entry)
	}
	result.Complete = true
	result.Kind = catalogResultKind(result.Entries)
	return catalogPageSize(result)
}

func catalogQueryInput(q app.Query) error {
	if q.Kind < app.GetContent || q.Kind > app.ListVersions || q.Page.Limit == 0 {
		return app.ErrInvalidInput
	}
	if q.Page.Limit > domain.QueryEntries.Maximum() || uint64(len(q.Page.Cursor)) > domain.CursorBytes.Maximum() {
		return app.ErrLimitExceeded
	}
	global := q.Kind == app.FindVersionsByPair || q.Kind == app.FindVersionsByAudioID
	if q.Page.Cursor != "" && ((!global && q.Kind != app.ListVersions) || q.Page.ExpectedRevision.Uint64() == 0) {
		return app.ErrInvalidInput
	}
	switch q.Kind {
	case app.FindVersionsByPair:
		if q.Fingerprint.IsZero() || !q.AudioID.IsZero() || !q.Version.IsZero() {
			return app.ErrInvalidInput
		}
	case app.FindVersionsByAudioID:
		if q.AudioID.IsZero() || !q.Fingerprint.IsZero() || !q.Version.IsZero() {
			return app.ErrInvalidInput
		}
	default:
		if q.Content.IsZero() || !q.Fingerprint.IsZero() || !q.AudioID.IsZero() || q.Kind == app.GetVersion && q.Version.IsZero() || q.Kind != app.GetVersion && !q.Version.IsZero() {
			return app.ErrInvalidInput
		}
	}
	return nil
}

// catalogFind scans a bounded, validated snapshot before asserting uniqueness.
// Fingerprints are search attributes; the page key remains the independent
// opaque ContentID/VersionID tuple. Even nonmatching rows count toward admission.
func (r *catalogRepository) catalogFind(ctx context.Context, query app.Query) (app.QueryResult, error) {
	var count uint64
	if err := r.transaction.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT 1 FROM tc_content_versions LIMIT 1025)").Scan(&count); err != nil {
		return app.QueryResult{}, repositoryError(ctx, err)
	}
	if count > domain.ResolutionCandidates.Maximum() {
		return app.QueryResult{}, app.ErrLimitExceeded
	}
	var corrupt bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check LIMIT 1)").Scan(&corrupt); err != nil {
		return app.QueryResult{}, repositoryError(ctx, err)
	}
	if corrupt {
		return app.QueryResult{}, app.ErrRepositoryUnavailable
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT DISTINCT
 CASE WHEN typeof(content_id)='text' AND length(CAST(content_id AS BLOB))=30 THEN content_id END
 FROM tc_content_versions ORDER BY 1 COLLATE BINARY LIMIT 1025`)
	if err != nil {
		return app.QueryResult{}, repositoryError(ctx, err)
	}
	var contents []identity.ContentID
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return app.QueryResult{}, repositoryError(ctx, err)
		}
		id, err := identity.ParseContentID(value.String)
		if !value.Valid || err != nil || uint64(len(contents)) >= domain.ResolutionCandidates.Maximum() {
			rows.Close()
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		contents = append(contents, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return app.QueryResult{}, repositoryError(ctx, err)
	}
	revision, _ := domain.NewCatalogRevision(1)
	result := app.QueryResult{Inspected: count}
	snapshot := sha256.New()
	var changes, dataVersion uint64
	if err := r.transaction.QueryRowContext(ctx, "SELECT total_changes()").Scan(&changes); err != nil {
		return app.QueryResult{}, repositoryError(ctx, err)
	}
	if err := r.transaction.QueryRowContext(ctx, "PRAGMA data_version").Scan(&dataVersion); err != nil {
		return app.QueryResult{}, repositoryError(ctx, err)
	}
	var counters [16]byte
	binary.BigEndian.PutUint64(counters[:8], changes)
	binary.BigEndian.PutUint64(counters[8:], dataVersion)
	snapshot.Write(counters[:])
	type candidate struct {
		record app.CatalogRecord
		key    string
	}
	var matches []candidate
	var scanned, retained uint64
	for _, id := range contents {
		record, current, found, err := r.catalogContent(ctx, id)
		if err != nil {
			return app.QueryResult{}, err
		}
		if err := r.registrationSnapshot(ctx, id, snapshot); err != nil {
			return app.QueryResult{}, err
		}
		if !found {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		if current.Uint64() > revision.Uint64() {
			revision = current
		}
		base, err := catalogRecordBytes(record)
		if err != nil {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		if err := domain.CheckLimit(domain.RetainedBytes, retained, uint64(len(base))); err != nil {
			return app.QueryResult{}, app.ErrLimitExceeded
		}
		retained += uint64(len(base))
		snapshot.Write(base)
		versions, err := r.catalogVersions(ctx, id)
		if err != nil {
			return app.QueryResult{}, err
		}
		scanned += uint64(len(versions))
		for _, row := range versions {
			encoded, err := catalogVersionBytes(row)
			if err != nil {
				return app.QueryResult{}, app.ErrRepositoryUnavailable
			}
			if err := domain.CheckLimit(domain.RetainedBytes, retained, uint64(len(encoded)+40)); err != nil {
				return app.QueryResult{}, app.ErrLimitExceeded
			}
			retained += uint64(len(encoded) + 40)
			snapshot.Write(encoded)
			snapshot.Write(row.Digest[:])
			var size [8]byte
			binary.BigEndian.PutUint64(size[:], row.Size)
			snapshot.Write(size[:])
			if !query.Content.IsZero() && id != query.Content {
				continue
			}
			pair := row.Version.Fingerprint()
			if query.Kind == app.FindVersionsByPair && pair != query.Fingerprint || query.Kind == app.FindVersionsByAudioID && pair.AudioID() != query.AudioID {
				continue
			}
			entry := record
			entry.Version, entry.Media = row.Version, row.Media
			matches = append(matches, candidate{record: entry, key: id.String() + row.Version.ID().String()})
		}
	}
	if scanned != count {
		return app.QueryResult{}, app.ErrRepositoryUnavailable
	}
	result.Revision = revision
	if query.Page.ExpectedRevision.Uint64() != 0 && query.Page.ExpectedRevision != revision {
		return app.QueryResult{}, app.ErrStaleCursor
	}
	var digest [32]byte
	copy(digest[:], snapshot.Sum(nil))
	start := 0
	if query.Page.Cursor != "" {
		after, err := r.catalogCursorAfter(query, revision, digest)
		if err != nil {
			return app.QueryResult{}, err
		}
		start = sort.Search(len(matches), func(i int) bool { return matches[i].key >= after })
		if start == len(matches) || matches[start].key != after {
			return app.QueryResult{}, app.ErrStaleCursor
		}
		start++
	}
	// Reserve the maximum cursor plus envelope while charging actual escaped
	// record/version bytes. The final encoder checks the exact complete envelope.
	charged := uint64(512)
	end := start
	for end < len(matches) && uint64(len(result.Entries)) < query.Page.Limit {
		entry := matches[end].record
		base, err := catalogRecordBytes(entry)
		if err != nil {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		version, err := catalogVersionBytes(catalogVersionRow{Version: entry.Version, Media: entry.Media})
		if err != nil {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		added := uint64(len(base) + len(version) + 32)
		if charged+added > domain.QueryBytes.Maximum() {
			if len(result.Entries) == 0 {
				return app.QueryResult{}, app.ErrLimitExceeded
			}
			break
		}
		charged += added
		result.Entries = append(result.Entries, entry)
		end++
	}
	result.Complete = start == 0 && end == len(matches)
	if !result.Complete {
		result.Kind = app.Incomplete
		if end < len(matches) {
			result.Next, err = r.catalogCursor(query, revision, digest, matches[end-1].key)
			if err != nil {
				return app.QueryResult{}, err
			}
		}
	} else {
		result.Kind = catalogResultKind(result.Entries)
		if result.Kind == app.Found && len(result.Entries) > 1 {
			result.Kind = app.Ambiguous
		}
	}
	return catalogPageSize(result)
}

func (r *catalogRepository) catalogContent(ctx context.Context, id identity.ContentID) (app.CatalogRecord, domain.CatalogRevision, bool, error) {
	revision, _ := domain.NewCatalogRevision(1)
	record := app.CatalogRecord{Content: id}
	var title, model, article sql.NullString
	var aggregate, protected sql.NullInt64
	var modelPresent, articlePresent bool
	err := r.transaction.QueryRowContext(ctx, `SELECT
 CASE WHEN typeof(c.title)='text' AND length(CAST(c.title AS BLOB))<=4096 THEN c.title END,
 CASE WHEN c.model_number IS NULL OR (typeof(c.model_number)='text' AND length(CAST(c.model_number AS BLOB))<=64) THEN c.model_number END,
 CASE WHEN c.article_number IS NULL OR (typeof(c.article_number)='text' AND length(CAST(c.article_number AS BLOB))<=64) THEN c.article_number END,
 m.aggregate_revision,m.protected_custom,
 c.model_number IS NOT NULL,c.article_number IS NOT NULL
 FROM tc_catalog_content c LEFT JOIN tc_catalog_managed m ON m.content_id=c.content_id WHERE c.content_id=?`, id.String()).Scan(&title, &model, &article, &aggregate, &protected, &modelPresent, &articlePresent)
	if errors.Is(err, sql.ErrNoRows) {
		var dangling bool
		err = r.transaction.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM tc_content_versions WHERE content_id=?) OR
 EXISTS(SELECT 1 FROM tc_catalog_managed WHERE content_id=?) OR
 EXISTS(SELECT 1 FROM tc_catalog_subjects WHERE content_id=?)`, id.String(), id.String(), id.String()).Scan(&dangling)
		if err != nil || dangling {
			return record, revision, false, app.ErrRepositoryUnavailable
		}
		return record, revision, false, nil
	}
	if err != nil {
		return record, revision, false, repositoryError(ctx, err)
	}
	// Check NULL-versus-invalid explicitly; CASE never allocates an oversized
	// scalar even when a damaged file bypassed the original SQL constraints.
	if !title.Valid || title.String != "" && !evidence.ValidText(title.String, 4096) || modelPresent != model.Valid || articlePresent != article.Valid {
		return record, revision, false, app.ErrRepositoryUnavailable
	}
	product, err := productIdentifiers(model, article)
	if err != nil {
		return record, revision, false, app.ErrRepositoryUnavailable
	}
	record.Baseline = domain.NewContentFacts(title.String, product)
	if aggregate.Valid != protected.Valid {
		return record, revision, false, app.ErrRepositoryUnavailable
	}
	if aggregate.Valid {
		revision, err = domain.NewCatalogRevision(uint64(aggregate.Int64))
		if err != nil || aggregate.Int64 < 1 || protected.Int64 < 0 || protected.Int64 > 1 {
			return record, revision, false, app.ErrRepositoryUnavailable
		}
		record.Review = app.CatalogManagedUnreviewed
		record.ProtectedCustom = protected.Int64 == 1
	}
	if err := r.catalogFields(ctx, &record, revision, aggregate.Valid); err != nil {
		return record, revision, false, err
	}
	if aggregate.Valid && r.schemaVersion == 9 {
		// C3 validates the graph but does not expose unannotated records as a
		// successful F02 query. C4 owns current/history annotations and cursors.
		var graph bool
		if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_supersessions WHERE content_id=?)", id.String()).Scan(&graph); err != nil {
			return record, revision, false, repositoryError(ctx, err)
		}
		if graph {
			return record, revision, false, app.ErrUnsupported
		}
	}
	return record, revision, true, nil
}

type catalogVersionRow struct {
	Version domain.ContentVersion
	Media   app.MediaState
	Digest  [32]byte
	Size    uint64
}

func (r *catalogRepository) catalogVersions(ctx context.Context, content identity.ContentID) ([]catalogVersionRow, error) {
	var count uint64
	if err := r.transaction.QueryRowContext(ctx, "SELECT count(*) FROM tc_content_versions WHERE content_id=?", content.String()).Scan(&count); err != nil {
		return nil, repositoryError(ctx, err)
	}
	if count > domain.ResolutionCandidates.Maximum() || count > domain.RetainedVersions.Maximum() {
		return nil, app.ErrLimitExceeded
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT
 CASE WHEN typeof(v.version_id)='text' AND length(CAST(v.version_id AS BLOB))=30 THEN v.version_id END,
 v.audio_id,
 CASE WHEN typeof(v.audio_sha1)='blob' AND length(v.audio_sha1)=20 THEN v.audio_sha1 END,
 v.order_known,
 CASE WHEN typeof(v.order_namespace)='text' AND length(CAST(v.order_namespace AS BLOB))<=128 THEN v.order_namespace END,
 CASE WHEN typeof(v.order_position)='blob' AND length(v.order_position)=8 THEN v.order_position END,
 v.order_namespace IS NULL,v.order_position IS NULL,
 vb.version_id IS NOT NULL,
 CASE WHEN typeof(vb.digest)='blob' AND length(vb.digest)=32 THEN vb.digest END,
 CASE WHEN typeof(b.digest)='blob' AND length(b.digest)=32 THEN b.digest END,
 b.size,b.algorithm,b.profile,
 EXISTS(SELECT 1 FROM tc_blob_imports i WHERE i.version_id=v.version_id)
 FROM tc_content_versions v
 LEFT JOIN tc_version_blobs vb ON vb.version_id=v.version_id
 LEFT JOIN tc_blobs b ON b.digest=vb.digest
 WHERE v.content_id=? ORDER BY v.version_id COLLATE BINARY`, content.String())
	if err != nil {
		return nil, repositoryError(ctx, err)
	}
	defer rows.Close()
	result := make([]catalogVersionRow, 0, int(count))
	for rows.Next() {
		var text, namespace sql.NullString
		var audio, known int64
		var size, algorithm, profile sql.NullInt64
		var hash, position, binding, blob []byte
		var namespaceNull, positionNull, bound, receipt bool
		if err := rows.Scan(&text, &audio, &hash, &known, &namespace, &position, &namespaceNull, &positionNull, &bound, &binding, &blob, &size, &algorithm, &profile, &receipt); err != nil {
			return nil, repositoryError(ctx, err)
		}
		if !text.Valid || audio < 1 || audio > 4294967295 || len(hash) != 20 || known < 0 || known > 1 {
			return nil, app.ErrRepositoryUnavailable
		}
		id, err := identity.ParseContentVersionID(text.String)
		if err != nil {
			return nil, app.ErrRepositoryUnavailable
		}
		order := domain.UnknownVersionOrderEvidence()
		if known == 0 {
			if !namespaceNull || !positionNull {
				return nil, app.ErrRepositoryUnavailable
			}
		} else {
			if !namespace.Valid || len(position) != 8 {
				return nil, app.ErrRepositoryUnavailable
			}
			order, err = domain.NewVersionOrderEvidence(namespace.String, binary.BigEndian.Uint64(position))
			if err != nil {
				return nil, app.ErrRepositoryUnavailable
			}
		}
		audioID, err := domain.NewAudioID(uint64(audio))
		if err != nil {
			return nil, app.ErrRepositoryUnavailable
		}
		audioHash, err := domain.ParseAudioHash(hex.EncodeToString(hash))
		if err != nil {
			return nil, app.ErrRepositoryUnavailable
		}
		fingerprint, err := domain.NewAudioFingerprint(audioID, audioHash)
		if err != nil {
			return nil, app.ErrRepositoryUnavailable
		}
		version, err := domain.NewContentVersion(id, content, fingerprint, order)
		if err != nil {
			return nil, app.ErrRepositoryUnavailable
		}
		media := app.CatalogOnly
		if bound {
			if len(binding) != 32 || !hmac.Equal(binding, blob) || !size.Valid || size.Int64 < int64(contentdomain.MinTAFBytes) || size.Int64 > int64(contentdomain.HardTAFBytes) || !algorithm.Valid || algorithm.Int64 != 1 || !profile.Valid || profile.Int64 != 1 {
				return nil, app.ErrRepositoryUnavailable
			}
			media = app.MediaBoundUnchecked
		} else if receipt {
			return nil, app.ErrRepositoryUnavailable
		}
		row := catalogVersionRow{Version: version, Media: media}
		copy(row.Digest[:], blob)
		if size.Valid {
			row.Size = uint64(size.Int64)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, repositoryError(ctx, err)
	}
	return result, nil
}

// catalogFields reads normalized history in revision order, never source/arrival
// preference. FK checks and explicit chain/support checks cover missing relations
// even when a corrupt fixture disabled constraint enforcement.
func (r *catalogRepository) catalogFields(ctx context.Context, record *app.CatalogRecord, revision domain.CatalogRevision, managed bool) error {
	id := record.Content.String()
	var corrupt bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check LIMIT 1)").Scan(&corrupt); err != nil {
		return repositoryError(ctx, err)
	}
	if corrupt {
		return app.ErrRepositoryUnavailable
	}
	var decisions, observations, links, retained uint64
	if err := r.transaction.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM tc_catalog_decisions WHERE content_id=?),
 (SELECT count(*) FROM tc_catalog_observations WHERE content_id=?),
 (SELECT count(*) FROM tc_catalog_decision_support s JOIN tc_catalog_subjects u USING(subject_id) WHERE u.content_id=?),
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)),0) FROM tc_catalog_decisions WHERE content_id=?) +
 (SELECT COALESCE(sum(COALESCE(length(o.text_value),0)+COALESCE(length(b.snapshot),0)+512),0) FROM tc_catalog_observations o LEFT JOIN tc_catalog_records b ON b.batch_id=o.batch_id AND b.record_index=o.record_index WHERE o.content_id=?)`, id, id, id, id, id).Scan(&decisions, &observations, &links, &retained); err != nil {
		return repositoryError(ctx, err)
	}
	if decisions > domain.RetainedDecisions.Maximum() || observations > domain.RetainedObservations.Maximum() || links > domain.RetainedSupportLinks.Maximum() || retained > domain.RetainedBytes.Maximum() {
		return app.ErrLimitExceeded
	}
	var invalidRevision bool
	if err := r.transaction.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM tc_catalog_subjects u WHERE u.content_id=? AND
 u.review_revision<>1+(SELECT count(*) FROM tc_catalog_decisions d WHERE d.subject_id=u.subject_id))`, id).Scan(&invalidRevision); err != nil {
		return repositoryError(ctx, err)
	}
	if invalidRevision {
		return app.ErrRepositoryUnavailable
	}
	if !managed && (decisions != 0 || observations != 0 || links != 0) {
		return app.ErrRepositoryUnavailable
	}
	if managed && r.schemaVersion >= 6 {
		// Order commits share the aggregate revision with review decisions. Validate
		// their combined contiguous history and bounded order envelopes before
		// allowing gaps between the review decisions projected below.
		if _, _, err := r.readStates(ctx, record.Content, domain.CatalogSubject{}, true, domain.CatalogPersistenceBindings{AllowUnbound: true}); err != nil {
			return err
		}
	}
	if managed && r.schemaVersion >= 8 {
		if err := r.registrationAccounting(ctx, record.Content); err != nil {
			return err
		}
		var registered bool
		if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE content_id=?)", id).Scan(&registered); err != nil {
			return repositoryError(ctx, err)
		}
		if registered {
			return r.registeredFields(ctx, record)
		}
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT d.command_id,d.subject_id,d.kind,d.claim_key,
 d.expected_aggregate_revision,d.result_aggregate_revision,d.expected_review_revision,d.result_review_revision,
 u.review_revision,d.policy_revision,
 CASE WHEN typeof(d.reason)='text' AND length(CAST(d.reason AS BLOB))<=4096 THEN d.reason END
 FROM tc_catalog_decisions d LEFT JOIN tc_catalog_subjects u ON u.subject_id=d.subject_id AND u.content_id=d.content_id
 WHERE d.content_id=? ORDER BY d.result_aggregate_revision`, id)
	if err != nil {
		return repositoryError(ctx, err)
	}
	type decision struct {
		command, subject string
		kind, key        int
		expected, result uint64
	}
	var history []decision
	reviews := make(map[string]uint64)
	aggregate := uint64(1)
	for rows.Next() {
		var d decision
		var before, after uint64
		var current sql.NullInt64
		var policy string
		var reason sql.NullString
		if err := rows.Scan(&d.command, &d.subject, &d.kind, &d.key, &d.expected, &d.result, &before, &after, &current, &policy, &reason); err != nil {
			rows.Close()
			return repositoryError(ctx, err)
		}
		previous := reviews[d.subject]
		if previous == 0 {
			previous = 1
		}
		if !catalogHexID(d.command) || !current.Valid || policy != domain.CatalogDecisionPolicy || !reason.Valid || !evidence.ValidText(reason.String, 4096) || d.expected < aggregate || r.schemaVersion < 6 && d.expected != aggregate || d.result != d.expected+1 || before != previous || after != before+1 || after > uint64(current.Int64) || d.kind < 1 || d.kind > 3 || d.kind == 1 && d.key != 0 || d.kind != 1 && (d.key < 1 || d.key > 5) || d.kind == 3 && d.key > 3 {
			rows.Close()
			return app.ErrRepositoryUnavailable
		}
		aggregate, reviews[d.subject] = d.result, after
		history = append(history, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return repositoryError(ctx, err)
	}
	rows.Close()
	if managed && r.schemaVersion < 6 && aggregate != revision.Uint64() {
		return app.ErrRepositoryUnavailable
	}
	for subject, last := range reviews {
		var current uint64
		if err := r.transaction.QueryRowContext(ctx, "SELECT review_revision FROM tc_catalog_subjects WHERE subject_id=?", subject).Scan(&current); err != nil || current != last {
			return app.ErrRepositoryUnavailable
		}
	}
	selected := make(map[int]map[evidence.ID]domain.CatalogClaim)
	for _, d := range history {
		support, err := r.catalogSupport(ctx, d.command, d.subject, id, d.kind, d.key)
		if err != nil {
			return err
		}
		if d.subject != id || d.key < 1 || d.key > 3 {
			continue
		}
		if d.kind == int(domain.ResolveConflict) || selected[d.key] == nil {
			selected[d.key] = make(map[evidence.ID]domain.CatalogClaim)
		}
		for observation, claim := range support {
			selected[d.key][observation] = claim
		}
	}
	fields := []*domain.CatalogField{&record.Title, &record.Model, &record.Article}
	for key, target := range fields {
		byClaim := make(map[domain.CatalogClaim][]evidence.ID)
		for observation, claim := range selected[key+1] {
			byClaim[claim] = append(byClaim[claim], observation)
		}
		for claim, support := range byClaim {
			sort.Slice(support, func(i, j int) bool { return support[i].String() < support[j].String() })
			target.Alternatives = append(target.Alternatives, domain.CatalogAlternative{Claim: claim, Support: support})
		}
		sort.Slice(target.Alternatives, func(i, j int) bool {
			left, right := target.Alternatives[i].Claim.Text(), target.Alternatives[j].Claim.Text()
			return len(left) < len(right) || len(left) == len(right) && left < right
		})
		if len(target.Alternatives) > 0 {
			target.Status = domain.CatalogFactAccepted
			record.Review = app.CatalogReviewed
		}
		if len(target.Alternatives) > 1 {
			target.Status = domain.CatalogFactConflict
		}
	}
	return nil
}

func catalogHexID(value string) bool {
	if len(value) != 32 || value == strings.Repeat("0", 32) {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(raw) == value
}

func (r *catalogRepository) catalogSupport(ctx context.Context, command, subject, content string, kind, key int) (map[evidence.ID]domain.CatalogClaim, error) {
	rows, err := r.transaction.QueryContext(ctx, `SELECT
 s.observation_id,
 s.subject_id=d.subject_id AND s.source_id=d.source_id AND s.registration_revision=d.registration_revision AND s.claim_key=d.claim_key
 AND o.subject_id=s.subject_id AND o.content_id=d.content_id AND o.source_id=s.source_id AND o.registration_revision=s.registration_revision AND o.claim_key=s.claim_key,
 CASE WHEN typeof(o.text_value)='text' AND length(CAST(o.text_value AS BLOB))<=4096 THEN o.text_value END,
 CASE WHEN typeof(o.audio_id)='blob' AND length(o.audio_id)=8 THEN o.audio_id END,
 CASE WHEN typeof(o.audio_sha1)='blob' AND length(o.audio_sha1)=20 THEN o.audio_sha1 END,
 o.owner_content_id,
 CASE WHEN typeof(o.observed_at)='text' AND length(CAST(o.observed_at AS BLOB))<=128 THEN o.observed_at END,
 o.confidence,
 CASE WHEN typeof(b.snapshot)='blob' AND length(b.snapshot) BETWEEN 1 AND 65536 THEN b.snapshot END
 FROM tc_catalog_decision_support s JOIN tc_catalog_decisions d ON d.command_id=s.command_id
 LEFT JOIN tc_catalog_observations o ON o.observation_id=s.observation_id
 LEFT JOIN tc_catalog_records b ON b.batch_id=o.batch_id AND b.record_index=o.record_index
 WHERE s.command_id=? ORDER BY s.observation_id`, command)
	if err != nil {
		return nil, repositoryError(ctx, err)
	}
	defer rows.Close()
	result := make(map[evidence.ID]domain.CatalogClaim)
	var agreeing domain.CatalogClaim
	for rows.Next() {
		if uint64(len(result)) >= domain.SelectedSupport.Maximum() || kind == int(domain.RecordObservations) {
			return nil, app.ErrRepositoryUnavailable
		}
		var text, owner, observed sql.NullString
		var observation string
		var matches sql.NullBool
		var confidence sql.NullInt64
		var audio, hash, snapshot []byte
		if err := rows.Scan(&observation, &matches, &text, &audio, &hash, &owner, &observed, &confidence, &snapshot); err != nil {
			return nil, repositoryError(ctx, err)
		}
		parsed, err := evidence.ParseID(observation)
		if err != nil || !matches.Valid || !matches.Bool || !observed.Valid || !confidence.Valid || confidence.Int64 < 0 || confidence.Int64 > 2 {
			return nil, app.ErrRepositoryUnavailable
		}
		at, err := time.Parse(time.RFC3339Nano, observed.String)
		if err != nil || at.IsZero() {
			return nil, app.ErrRepositoryUnavailable
		}
		if _, err := domain.NewRecordSnapshot(snapshot); err != nil {
			return nil, app.ErrRepositoryUnavailable
		}
		var claim domain.CatalogClaim
		switch domain.ClaimKey(key) {
		case domain.TitleClaimKey:
			claim, err = domain.TitleClaim(text.String)
		case domain.ModelClaimKey:
			var model domain.ModelNumber
			model, err = domain.ParseModelNumber(text.String)
			if err == nil {
				claim, err = domain.ModelClaim(model)
			}
		case domain.ArticleClaimKey:
			var article domain.ArticleNumber
			article, err = domain.ParseArticleNumber(text.String)
			if err == nil {
				claim, err = domain.ArticleClaim(article)
			}
		case domain.FingerprintClaimKey:
			if len(audio) != 8 || len(hash) != 20 || subject == content || text.Valid || owner.Valid {
				return nil, app.ErrRepositoryUnavailable
			}
			var audioID domain.AudioID
			audioID, err = domain.NewAudioID(binary.BigEndian.Uint64(audio))
			if err == nil {
				var audioHash domain.AudioHash
				audioHash, err = domain.ParseAudioHash(hex.EncodeToString(hash))
				if err == nil {
					var pair domain.AudioFingerprint
					pair, err = domain.NewAudioFingerprint(audioID, audioHash)
					if err == nil {
						claim, err = domain.FingerprintClaim(pair)
					}
				}
			}
		case domain.OwnershipClaimKey:
			if !owner.Valid || owner.String != content || subject == content || text.Valid || len(audio) != 0 || len(hash) != 0 {
				return nil, app.ErrRepositoryUnavailable
			}
			var id identity.ContentID
			id, err = identity.ParseContentID(owner.String)
			if err == nil {
				claim, err = domain.OwnershipClaim(id)
			}
		default:
			return nil, app.ErrRepositoryUnavailable
		}
		if err != nil || key <= 3 && (!text.Valid || subject != content || len(audio) != 0 || len(hash) != 0 || owner.Valid) {
			return nil, app.ErrRepositoryUnavailable
		}
		if kind == int(domain.ResolveConflict) && agreeing.Key() != 0 && agreeing != claim {
			return nil, app.ErrRepositoryUnavailable
		}
		agreeing = claim
		result[parsed] = claim
	}
	if err := rows.Err(); err != nil {
		return nil, repositoryError(ctx, err)
	}
	if kind != int(domain.RecordObservations) && len(result) == 0 {
		return nil, app.ErrRepositoryUnavailable
	}
	return result, nil
}

func catalogResultKind(entries []app.CatalogRecord) app.ResultKind {
	if len(entries) == 0 {
		return app.NoMatch
	}
	for _, entry := range entries {
		if entry.Title.Status == domain.CatalogFactConflict || entry.Model.Status == domain.CatalogFactConflict || entry.Article.Status == domain.CatalogFactConflict {
			return app.Conflicted
		}
	}
	return app.Found
}

// This is the internal bounded query encoding, not a public API. Flatten all
// private domain fields explicitly; json.Marshal(domain values) would lose facts.
func catalogRecordBytes(record app.CatalogRecord) ([]byte, error) {
	model, modelPresent := record.Baseline.ProductIdentifiers().Model()
	article, articlePresent := record.Baseline.ProductIdentifiers().Article()
	type alternative struct {
		Key     domain.ClaimKey
		Text    string
		Support []string
	}
	type field struct {
		Status       domain.CatalogFactStatus
		Alternatives []alternative
	}
	fields := make([]field, 0, 3)
	for _, projection := range []domain.CatalogField{record.Title, record.Model, record.Article} {
		f := field{Status: projection.Status}
		for _, value := range projection.Alternatives {
			a := alternative{Key: value.Claim.Key(), Text: value.Claim.Text()}
			for _, support := range value.Support {
				a.Support = append(a.Support, support.String())
			}
			f.Alternatives = append(f.Alternatives, a)
		}
		fields = append(fields, f)
	}
	return json.Marshal(struct {
		Content, Title, Model, Article string
		ModelPresent, ArticlePresent   bool
		Review                         app.CatalogReviewStatus
		Protected                      bool
		Fields                         []field
	}{record.Content.String(), record.Baseline.Title(), model.String(), article.String(), modelPresent, articlePresent, record.Review, record.ProtectedCustom, fields})
}

func catalogVersionBytes(row catalogVersionRow) ([]byte, error) {
	return json.Marshal(struct {
		ID, Content, Hash, Namespace string
		Audio, Position              uint64
		OrderKnown                   bool
		Media                        app.MediaState
	}{row.Version.ID().String(), row.Version.ContentID().String(), row.Version.Fingerprint().Hash().String(), row.Version.OrderEvidence().Namespace(), row.Version.Fingerprint().AudioID().Uint64(), row.Version.OrderEvidence().Position(), row.Version.OrderEvidence().Known(), row.Media})
}

func catalogPageSize(result app.QueryResult) (app.QueryResult, error) {
	type entry struct {
		Record  json.RawMessage
		Version json.RawMessage
	}
	var entries []entry
	for _, record := range result.Entries {
		base, err := catalogRecordBytes(record)
		if err != nil {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		version, err := catalogVersionBytes(catalogVersionRow{Version: record.Version, Media: record.Media})
		if err != nil {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		entries = append(entries, entry{Record: base, Version: version})
	}
	for {
		encoded, err := json.Marshal(struct {
			Kind                    app.ResultKind
			Entries                 []entry
			Revision                uint64
			Complete                bool
			Next                    string
			Inspected, EncodedBytes uint64
		}{result.Kind, entries, result.Revision.Uint64(), result.Complete, result.Next, result.Inspected, result.EncodedBytes})
		if err != nil {
			return app.QueryResult{}, app.ErrRepositoryUnavailable
		}
		size := uint64(len(encoded))
		if size > domain.QueryBytes.Maximum() {
			return app.QueryResult{}, app.ErrLimitExceeded
		}
		if size == result.EncodedBytes {
			return result, nil
		}
		result.EncodedBytes = size
	}
}

var catalogCursorSecret struct {
	once sync.Once
	key  [32]byte
	err  error
}

func (r *catalogRepository) catalogCursorMAC(query app.Query, revision domain.CatalogRevision, payload []byte) ([]byte, error) {
	catalogCursorSecret.once.Do(func() {
		_, catalogCursorSecret.err = rand.Read(catalogCursorSecret.key[:])
	})
	if catalogCursorSecret.err != nil {
		return nil, app.ErrRepositoryUnavailable
	}
	mac := hmac.New(sha256.New, catalogCursorSecret.key[:])
	fmt.Fprintf(mac, "pi08-sqlite-cursor/1|%p|%p|%d|%d|%s|%s|%d|%d|", r.owner, r.database, r.generation, query.Kind, query.Content.String(), query.Version.String(), query.Page.Limit, revision.Uint64())
	fmt.Fprintf(mac, "%d|%s|", query.AudioID.Uint64(), query.Fingerprint.Hash().String())
	fmt.Fprintf(mac, "%d|", query.Fingerprint.AudioID().Uint64())
	mac.Write(payload)
	return mac.Sum(nil), nil
}

func (r *catalogRepository) catalogCursor(query app.Query, revision domain.CatalogRevision, snapshot [32]byte, after string) (string, error) {
	payload := append([]byte{1}, []byte(after)...)
	payload = append(payload, snapshot[:]...)
	mac, err := r.catalogCursorMAC(query, revision, payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(payload, mac...)), nil
}

func (r *catalogRepository) catalogCursorAfter(query app.Query, revision domain.CatalogRevision, snapshot [32]byte) (string, error) {
	keyBytes := 30
	if query.Kind == app.FindVersionsByPair || query.Kind == app.FindVersionsByAudioID {
		keyBytes = 60
	}
	payloadBytes := 1 + keyBytes + 32
	raw, err := base64.RawURLEncoding.DecodeString(query.Page.Cursor)
	if err != nil || len(raw) != payloadBytes+32 || raw[0] != 1 || base64.RawURLEncoding.EncodeToString(raw) != query.Page.Cursor {
		return "", app.ErrStaleCursor
	}
	mac, err := r.catalogCursorMAC(query, revision, raw[:payloadBytes])
	if err != nil {
		return "", err
	}
	if !hmac.Equal(mac, raw[payloadBytes:]) || !hmac.Equal(snapshot[:], raw[1+keyBytes:payloadBytes]) {
		return "", app.ErrStaleCursor
	}
	after := string(raw[1 : 1+keyBytes])
	version := after
	if keyBytes == 60 {
		if _, err := identity.ParseContentID(after[:30]); err != nil {
			return "", app.ErrStaleCursor
		}
		version = after[30:]
	}
	if _, err := identity.ParseContentVersionID(version); err != nil {
		return "", app.ErrStaleCursor
	}
	return after, nil
}
