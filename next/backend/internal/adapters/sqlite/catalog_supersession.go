package sqlite

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var _ app.SupersessionRepository = (*catalogRepository)(nil)

// History uses the callback lease, complete C3 validation and current service
// authority. Historical source revocation changes provenance, not acceptance.
func (r *catalogRepository) QuerySupersession(ctx context.Context, query app.SupersessionQuery) (page app.HistoryPage, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return page, err
	}
	defer func() {
		done(&err)
		if err != nil {
			page = app.HistoryPage{}
		}
	}()
	call = context.WithValue(call, catalogReadCacheKey{}, &catalogReadCache{repository: r})
	if err := catalogQueryInput(app.Query{Kind: app.ListVersions, Content: query.Content, Page: query.Page}); err != nil {
		return page, err
	}
	if err := r.supersessionReadSchema(call); err != nil {
		return page, err
	}
	// Even a missing Content cannot turn dangling retained relations into a
	// successful empty history. Found Contents get this guard in catalogFields.
	var corrupt bool
	if err := r.transaction.QueryRowContext(call, "SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check LIMIT 1)").Scan(&corrupt); err != nil {
		return page, repositoryError(call, err)
	}
	if corrupt {
		return page, app.ErrRepositoryUnavailable
	}
	record, aggregate, found, err := r.catalogContent(call, query.Content)
	if err != nil {
		return page, err
	}
	page.AggregateRevision = aggregate
	page.SupersessionRevision, _ = domain.NewCatalogRevision(1)
	if query.Page.ExpectedRevision.Uint64() != 0 && query.Page.ExpectedRevision != aggregate {
		return page, app.ErrStaleCursor
	}
	if !found {
		if query.Page.Cursor != "" {
			return page, app.ErrStaleCursor
		}
		page.Complete = true
		page.EncodedBytes = supersessionPageBytes(page)
		return page, nil
	}
	if record.Review == app.CatalogUnmanaged {
		return page, app.ErrUnsupported
	}
	state, err := r.readSupersession(call, query.Content, domain.CatalogPersistenceBindings{AllowUnbound: true})
	if err != nil {
		return page, err
	}
	page.SupersessionRevision = state.Revision()
	page.Inspected = state.Revision().Uint64() - 1
	versions, err := r.catalogVersions(call, query.Content)
	if err != nil {
		return page, err
	}
	digest, err := r.catalogScopedSnapshot(call, record, versions)
	if err != nil {
		return page, err
	}
	// Select a whole containing chain after validating the entire graph. An
	// isolated or absent version intentionally has an empty history.
	chain := map[identity.ContentVersionID]bool{}
	history := state.History()
	if !query.Version.IsZero() {
		chain[query.Version] = true
		for range len(history) {
			changed := false
			for _, e := range history {
				if chain[e.Command.Old.ID()] || chain[e.Command.New.ID()] {
					if !chain[e.Command.Old.ID()] || !chain[e.Command.New.ID()] {
						changed = true
					}
					chain[e.Command.Old.ID()] = true
					chain[e.Command.New.ID()] = true
				}
			}
			if !changed {
				break
			}
		}
	}
	var after uint64
	if query.Page.Cursor != "" {
		after, err = r.supersessionCursorAfter(query, aggregate, state.Revision(), digest)
		if err != nil {
			return page, err
		}
	}
	type candidate struct {
		key            string
		revision, size uint64
		revoked        bool
	}
	rows, err := r.transaction.QueryContext(call, `SELECT s.command_id,s.result_supersession,length(s.canonical_command)+length(s.canonical_result),p.revoked FROM tc_catalog_supersessions s JOIN tc_catalog_sources p USING(source_id,registration_revision) WHERE s.content_id=? ORDER BY s.result_supersession LIMIT 1024`, query.Content.String())
	if err != nil {
		return page, repositoryError(call, err)
	}
	var candidates []candidate
	var count uint64
	afterFound := after == 0
	for rows.Next() {
		var c candidate
		var revoked int
		if e := rows.Scan(&c.key, &c.revision, &c.size, &revoked); e != nil {
			rows.Close()
			return page, repositoryError(call, e)
		}
		if count >= uint64(len(history)) || c.revision != count+2 || c.key != history[count].Command.ID.String() || revoked < 0 || revoked > 1 || c.size == 0 || c.size > 2*domain.RetainedBytes.Maximum() {
			rows.Close()
			return page, app.ErrRepositoryUnavailable
		}
		e := history[count]
		count++
		c.revoked = revoked == 1
		if !query.Version.IsZero() && !chain[e.Command.Old.ID()] {
			continue
		}
		if c.revision == after {
			afterFound = true
		}
		if c.revision > after {
			candidates = append(candidates, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, repositoryError(call, err)
	}
	if count != page.Inspected {
		return page, app.ErrRepositoryUnavailable
	}
	if !afterFound {
		return page, app.ErrStaleCursor
	}
	page.Complete = true
	size := supersessionPageBytes(page)
	chosen := 0
	for i, c := range candidates {
		added := 24 + c.size
		more := i+1 < len(candidates)
		var next string
		if more {
			next, err = r.supersessionCursor(query, aggregate, state.Revision(), digest, c.revision)
			if err != nil {
				return page, err
			}
		}
		if uint64(chosen) == query.Page.Limit || size+added+uint64(len(next)) > domain.QueryBytes.Maximum() {
			if chosen == 0 {
				return page, app.ErrLimitExceeded
			}
			page.Complete = false
			page.Next, err = r.supersessionCursor(query, aggregate, state.Revision(), digest, candidates[chosen-1].revision)
			if err != nil {
				return page, err
			}
			break
		}
		size += added
		chosen++
	}
	// No response BLOB is selected until the exact page, including its cursor,
	// has passed byte/count admission.
	for _, c := range candidates[:chosen] {
		var e app.SupersessionHistoryEntry
		if err := r.transaction.QueryRowContext(call, `SELECT canonical_command,canonical_result FROM tc_catalog_supersessions WHERE command_id=? AND content_id=? AND length(canonical_command)+length(canonical_result)=?`, c.key, query.Content.String(), c.size).Scan(&e.Command, &e.Result); err != nil {
			return page, repositoryError(call, err)
		}
		e.SourceRevoked = c.revoked
		page.Entries = append(page.Entries, e)
	}
	page.EncodedBytes = supersessionPageBytes(page)
	if page.EncodedBytes > domain.QueryBytes.Maximum() {
		return page, app.ErrLimitExceeded
	}
	return page, call.Err()
}

func supersessionPageBytes(page app.HistoryPage) uint64 {
	size := uint64(8 + len("pi08-version-supersession-page/1") + 6*8 + len(page.Next))
	for _, e := range page.Entries {
		size += 24 + uint64(len(e.Command)+len(e.Result))
	}
	return size
}

func (r *catalogRepository) supersessionCursor(query app.SupersessionQuery, aggregate, revision domain.CatalogRevision, snapshot [32]byte, after uint64) (string, error) {
	payload := []byte{2}
	payload = binary.BigEndian.AppendUint64(payload, after)
	payload = binary.BigEndian.AppendUint64(payload, revision.Uint64())
	payload = append(payload, snapshot[:]...)
	mac, err := r.catalogCursorMAC(app.Query{Content: query.Content, Version: query.Version, Page: query.Page}, aggregate, payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(payload, mac...)), nil
}

func (r *catalogRepository) supersessionCursorAfter(query app.SupersessionQuery, aggregate, revision domain.CatalogRevision, snapshot [32]byte) (uint64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(query.Page.Cursor)
	if err != nil || len(raw) != 81 || raw[0] != 2 || base64.RawURLEncoding.EncodeToString(raw) != query.Page.Cursor {
		return 0, app.ErrStaleCursor
	}
	mac, err := r.catalogCursorMAC(app.Query{Content: query.Content, Version: query.Version, Page: query.Page}, aggregate, raw[:49])
	if err != nil {
		return 0, err
	}
	after := binary.BigEndian.Uint64(raw[1:9])
	if after < 2 || after > revision.Uint64() || binary.BigEndian.Uint64(raw[9:17]) != revision.Uint64() || !hmac.Equal(raw[17:49], snapshot[:]) || !hmac.Equal(raw[49:], mac) {
		return 0, app.ErrStaleCursor
	}
	return after, nil
}

// Revocation is independent of the immutable command/result graph digest
// already included by registrationSnapshot. Frames prevent source-key aliasing.
func (r *catalogRepository) supersessionRevocations(ctx context.Context, content identity.ContentID, target io.Writer) error {
	if r.schemaVersion != 9 {
		return nil
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT DISTINCT s.source_id,s.registration_revision,p.revoked FROM tc_catalog_supersessions s JOIN tc_catalog_sources p USING(source_id,registration_revision) WHERE s.content_id=? ORDER BY s.source_id,s.registration_revision`, content.String())
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var source, revision string
		var revoked uint64
		if err := rows.Scan(&source, &revision, &revoked); err != nil {
			return repositoryError(ctx, err)
		}
		count++
		if count > int(domain.SourceRegistrations.Maximum()) || !catalogHexID(source) || !catalogHexID(revision) || revoked > 1 {
			return app.ErrRepositoryUnavailable
		}
		raw := registrationFrame(nil, []byte(source))
		raw = registrationFrame(raw, []byte(revision))
		raw = binary.BigEndian.AppendUint64(raw, revoked)
		if _, err := target.Write(raw); err != nil {
			return app.ErrRepositoryUnavailable
		}
	}
	return repositoryError(ctx, rows.Err())
}

func (r *catalogRepository) catalogAnnotations(ctx context.Context, record app.CatalogRecord) (map[identity.ContentVersionID]app.Supersession, error) {
	annotations := map[identity.ContentVersionID]app.Supersession{}
	if r.schemaVersion != 9 || record.Review == app.CatalogUnmanaged {
		return annotations, nil
	}
	if err := r.supersessionReadSchema(ctx); err != nil {
		return nil, err
	}
	state, err := r.readSupersession(ctx, record.Content, domain.CatalogPersistenceBindings{AllowUnbound: true})
	if err != nil {
		return nil, err
	}
	for _, v := range state.Versions() {
		annotations[v.ID()] = app.Supersession{Status: app.Current, SupersessionRevision: state.Revision(), Terminal: v.ID()}
	}
	for _, edge := range state.History() {
		old, newer := annotations[edge.Command.Old.ID()], annotations[edge.Command.New.ID()]
		old.Status = app.Superseded
		old.Successor = edge.Command.New.ID()
		old.OutgoingCommand = edge.Command.ID
		newer.Predecessor = edge.Command.Old.ID()
		newer.IncomingCommand = edge.Command.ID
		annotations[edge.Command.Old.ID()], annotations[edge.Command.New.ID()] = old, newer
	}
	for id, a := range annotations {
		terminal := id
		for steps := 0; !annotations[terminal].Successor.IsZero(); steps++ {
			if steps >= len(annotations) {
				return nil, app.ErrRepositoryUnavailable
			}
			terminal = annotations[terminal].Successor
		}
		a.Terminal = terminal
		annotations[id] = a
	}
	return annotations, nil
}

func (r *catalogRepository) supersessionReadSchema(ctx context.Context) error {
	if r.schemaVersion != 9 {
		return app.ErrUnsupported
	}
	rows, err := r.transaction.QueryContext(ctx, "SELECT version,migration_id,checksum,dirty FROM tc_schema_migrations ORDER BY version")
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer rows.Close()
	migrations := SchemaMigrations()
	i := 0
	for rows.Next() {
		var version, dirty int
		var id, sum string
		if err := rows.Scan(&version, &id, &sum, &dirty); err != nil {
			return repositoryError(ctx, err)
		}
		if i >= 9 || version != migrations[i].Version || id != migrations[i].ID || sum != migrationChecksum(migrations[i]) || dirty != 0 {
			return app.ErrUnsupported
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return repositoryError(ctx, err)
	}
	if i != 9 {
		return app.ErrUnsupported
	}
	return nil
}

func (r *catalogRepository) supersessionInput(ctx context.Context, op app.SupersessionOperation) ([]byte, error) {
	if err := op.Capability.Authorize(op.Command.Source); err != nil {
		return nil, err
	}
	if err := r.stateSourceActive(ctx, op.Command.Source); err != nil {
		return nil, err
	}
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		return nil, err
	}
	for _, v := range []domain.ContentVersion{op.Command.Old, op.Command.New} {
		if v.Fingerprint().AudioID().Uint64() > math.MaxUint32 {
			return nil, app.ErrUnsupported
		}
	}
	if r.schemaVersion != 9 {
		return nil, app.ErrUnsupported
	}
	var persisted bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?)", op.Command.Source.ID().String(), op.Command.Source.Revision().String()).Scan(&persisted); err != nil {
		return nil, repositoryError(ctx, err)
	}
	if persisted {
		sourceRaw, err := op.Command.Source.PersistenceEncoding()
		if err != nil {
			return nil, err
		}
		d := catalogStateReader{raw: sourceRaw, repository: r, ctx: ctx}
		d.source()
		if d.err != nil {
			return nil, domain.ErrCatalogSourceMismatch
		}
	}
	// Check the entire exact migration prefix, not just MAX(version).
	rows, err := r.transaction.QueryContext(ctx, "SELECT version,migration_id,checksum,dirty FROM tc_schema_migrations ORDER BY version")
	if err != nil {
		return nil, repositoryError(ctx, err)
	}
	i, migrations := 0, SchemaMigrations()
	for rows.Next() {
		var version, dirty int
		var id, sum string
		if err := rows.Scan(&version, &id, &sum, &dirty); err != nil {
			rows.Close()
			return nil, repositoryError(ctx, err)
		}
		if i >= 9 || version != migrations[i].Version || id != migrations[i].ID || sum != migrationChecksum(migrations[i]) || dirty != 0 {
			rows.Close()
			return nil, app.ErrUnsupported
		}
		i++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, repositoryError(ctx, err)
	}
	if i != 9 {
		return nil, app.ErrUnsupported
	}
	return raw, nil
}

func (r *catalogRepository) LookupSupersession(ctx context.Context, op app.SupersessionOperation) (result domain.CatalogSupersessionResult, found bool, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, false, err
	}
	defer func() {
		done(&err)
		if err != nil {
			result, found = domain.CatalogSupersessionResult{}, false
		}
	}()
	raw, err := r.supersessionInput(call, op)
	if err != nil {
		return result, false, err
	}
	return r.lookupSupersession(call, op, raw)
}

func (r *catalogRepository) lookupSupersession(ctx context.Context, op app.SupersessionOperation, raw []byte) (domain.CatalogSupersessionResult, bool, error) {
	var stored []byte
	var content string
	err := r.transaction.QueryRowContext(ctx, "SELECT CASE WHEN length(canonical_command) BETWEEN 1 AND 8388608 THEN canonical_command END,content_id FROM tc_catalog_supersessions WHERE command_id=?", op.Command.ID.String()).Scan(&stored, &content)
	if errors.Is(err, sql.ErrNoRows) {
		if err := r.registrationKeyGuard(ctx, op.Command.ID.String(), true); err != nil {
			return domain.CatalogSupersessionResult{}, false, err
		}
		return domain.CatalogSupersessionResult{}, false, nil
	}
	if err != nil || len(stored) == 0 {
		return domain.CatalogSupersessionResult{}, false, app.ErrRepositoryUnavailable
	}
	if !bytes.Equal(stored, raw) {
		return domain.CatalogSupersessionResult{}, false, domain.ErrCatalogCommandConflict
	}
	if content != op.Command.Content.String() {
		return domain.CatalogSupersessionResult{}, false, app.ErrRepositoryUnavailable
	}
	state, err := r.readSupersession(ctx, op.Command.Content, domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domain.SourceRegistration{op.Command.Source}, Reviewers: []domain.IssuerPrincipal{op.Capability.Reviewer()}})
	if err != nil {
		return domain.CatalogSupersessionResult{}, false, err
	}
	if err := r.registrationAccounting(ctx, op.Command.Content); err != nil {
		return domain.CatalogSupersessionResult{}, false, app.ErrRepositoryUnavailable
	}
	return state.LookupCommand(op.Capability, op.Command)
}

func (r *catalogRepository) CommitSupersession(ctx context.Context, op app.SupersessionOperation) (result domain.CatalogSupersessionResult, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		done(&err)
		if err != nil {
			result = domain.CatalogSupersessionResult{}
		}
	}()
	raw, err := r.supersessionInput(call, op)
	if err != nil {
		return result, err
	}
	if replay, found, e := r.lookupSupersession(call, op, raw); e != nil || found {
		return replay, e
	}
	c := op.Command
	var aggregate uint64
	var protected bool
	err = r.transaction.QueryRowContext(call, "SELECT aggregate_revision,protected_custom FROM tc_catalog_managed WHERE content_id=?", c.Content.String()).Scan(&aggregate, &protected)
	if errors.Is(err, sql.ErrNoRows) {
		return result, app.ErrSubjectNotFound
	}
	if err != nil {
		return result, repositoryError(call, err)
	}
	bindings := domain.CatalogPersistenceBindings{AllowUnbound: true}
	state, err := r.readSupersession(call, c.Content, bindings)
	if err != nil {
		return result, err
	}
	for _, v := range []domain.ContentVersion{c.Old, c.New} {
		found := false
		for _, stored := range state.Versions() {
			if stored.ID() == v.ID() {
				if stored != v {
					return result, domain.ErrCatalogCommandConflict
				}
				found = true
			}
		}
		if !found {
			return result, app.ErrSubjectNotFound
		}
	}
	oldSubject, _ := domain.VersionSubject(c.Content, c.Old.ID())
	newSubject, _ := domain.VersionSubject(c.Content, c.New.ID())
	old, _, err := r.readStatesRaw(call, c.Content, oldSubject, false, bindings)
	if err != nil {
		return result, err
	}
	newer, _, err := r.readStatesRaw(call, c.Content, newSubject, false, bindings)
	if err != nil {
		return result, err
	}
	order, revision, err := r.readStatesRaw(call, c.Content, domain.CatalogSubject{}, true, bindings)
	if err != nil {
		return result, err
	}
	if len(old) == 0 || len(newer) == 0 || len(order) == 0 {
		return result, app.ErrSubjectNotFound
	}
	_, result, err = state.Apply(op.Capability, c, domain.CatalogSupersessionContext{AggregateRevision: revision, OldReview: old[len(old)-1].review, NewReview: newer[len(newer)-1].review, OrderRevision: order[len(order)-1].order.Revision(), ProtectedCustom: protected})
	if errors.Is(err, domain.ErrVersionIdentityConflict) {
		err = domain.ErrCatalogCommandConflict
	}
	if err != nil {
		return result, err
	}
	resultRaw, err := result.CanonicalEncoding()
	if err != nil {
		return result, err
	}
	if err = r.supersessionProvenance(call, c); err != nil {
		return result, err
	}
	if err = r.registrationAccountingWith(call, c.Content, supersessionAdded(c, result, raw, resultRaw)); err != nil {
		return result, err
	}
	if _, err = r.transaction.ExecContext(call, "PRAGMA query_only=OFF"); err != nil {
		return result, repositoryError(call, err)
	}
	defer func() {
		if _, e := r.transaction.ExecContext(r.ctx, "PRAGMA query_only=ON"); e != nil && err == nil {
			err = repositoryError(r.ctx, e)
		}
	}()
	updated, err := r.transaction.ExecContext(call, "UPDATE tc_catalog_managed SET aggregate_revision=? WHERE content_id=? AND aggregate_revision=?", result.AggregateRevision.Uint64(), c.Content.String(), aggregate)
	if err != nil {
		return result, repositoryError(call, err)
	}
	if n, e := updated.RowsAffected(); e != nil || n != 1 {
		return result, domain.ErrCatalogRevisionConflict
	}
	// Reuse the accepted source/batch/record projector, before inserting the
	// command whose key would deliberately collide with its own provenance.
	if _, err = r.supersessionProject(call, raw, true); err != nil {
		return result, err
	}
	a, b := sha256.Sum256(raw), sha256.Sum256(resultRaw)
	_, err = r.transaction.ExecContext(call, `INSERT INTO tc_catalog_supersessions VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?)`, c.ID.String(), c.Content.String(), c.Old.ID().String(), c.New.ID().String(), c.Source.ID().String(), c.Source.Revision().String(), op.Capability.Reviewer().String(), c.ExpectedAggregate.Uint64(), result.AggregateRevision.Uint64(), c.ExpectedSupersession.Uint64(), result.SupersessionRevision.Uint64(), c.ExpectedOldReview.Uint64(), c.ExpectedNewReview.Uint64(), result.OrderRevision.Uint64(), protected, raw, resultRaw, a[:], b[:])
	if err != nil {
		return result, repositoryError(call, err)
	}
	for _, evidence := range c.Evidence {
		i := evidence.Input()
		_, err = r.transaction.ExecContext(call, "INSERT INTO tc_catalog_supersession_evidence VALUES (?,?,?,?,?,?,?,?,?,?,?,?)", evidence.ID().String(), c.ID.String(), c.Content.String(), c.Old.ID().String(), c.New.ID().String(), c.Source.ID().String(), c.Source.Revision().String(), i.Coordinate.Artifact().Facts().ID.String(), i.Coordinate.Index(), i.AssertedOrigin, i.ObservedAt.UTC().Format(time.RFC3339Nano), i.Confidence)
		if err != nil {
			return result, repositoryError(call, err)
		}
	}
	for _, id := range result.Support {
		if _, err = r.transaction.ExecContext(call, "INSERT INTO tc_catalog_supersession_support VALUES (?,?)", c.ID.String(), id.String()); err != nil {
			return result, repositoryError(call, err)
		}
	}
	if _, found, e := r.lookupSupersession(call, op, raw); e != nil || !found {
		if e != nil {
			return result, e
		}
		return result, app.ErrRepositoryUnavailable
	}
	r.wrote = true
	// The callback lease is revoked before these owner checks, so capture the
	// transaction rather than consulting the now unusable repository port.
	tx := r.transaction
	r.authorize = append(r.authorize, func() error {
		if e := op.Capability.Authorize(c.Source); e != nil {
			return e
		}
		var revoked int
		if e := tx.QueryRowContext(r.ctx, "SELECT revoked FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?", c.Source.ID().String(), c.Source.Revision().String()).Scan(&revoked); e != nil {
			return repositoryError(r.ctx, e)
		}
		if revoked != 0 {
			return domain.ErrCatalogUnauthorized
		}
		return ctx.Err()
	})
	return result, call.Err()
}

type supersessionUsage struct{ bytes, observations, links, events uint64 }

// SQL charges exactly the additional retained columns and one full C per
// evidence reference, even when several references share a record.
func (r *catalogRepository) supersessionUsage(ctx context.Context, content identity.ContentID) (u supersessionUsage, err error) {
	err = r.transaction.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)+
 8+length(command_id)+8+length(content_id)+8+length(old_version_id)+8+length(new_version_id)+8+length(source_id)+8+length(registration_revision)+8+length(reviewer_principal)+9*8+64),0) FROM tc_catalog_supersessions WHERE content_id=?1)+
 (SELECT COALESCE(sum(8+length(e.evidence_id)+8+length(e.command_id)+8+length(e.content_id)+8+length(e.old_version_id)+8+length(e.new_version_id)+8+length(e.source_id)+8+length(e.registration_revision)+8+length(e.batch_id)+8+8+length(CAST(e.asserted_origin AS BLOB))+8+length(e.observed_at)+8+
 8+length(e.batch_id)+32+8+8+8+length(CAST(b.parser_revision AS BLOB))+8+length(CAST(b.source_revision AS BLOB))+8+8+length(CAST(r.locator AS BLOB))+8+length(r.snapshot)),0) FROM tc_catalog_supersession_evidence e JOIN tc_catalog_batches b USING(batch_id) JOIN tc_catalog_records r ON r.batch_id=e.batch_id AND r.record_index=e.record_index WHERE e.content_id=?1)+
 (SELECT COALESCE(sum(8+length(s.command_id)+8+length(s.evidence_id)),0) FROM tc_catalog_supersession_support s JOIN tc_catalog_supersessions e USING(command_id) WHERE e.content_id=?1),
 (SELECT count(*) FROM tc_catalog_supersession_evidence WHERE content_id=?1),
 (SELECT count(*) FROM tc_catalog_supersession_support s JOIN tc_catalog_supersessions e USING(command_id) WHERE e.content_id=?1),
 (SELECT count(*) FROM tc_catalog_supersessions WHERE content_id=?1)`, content.String()).Scan(&u.bytes, &u.observations, &u.links, &u.events)
	return u, repositoryError(ctx, err)
}

func supersessionAdded(c domain.CatalogSupersessionCommand, result domain.CatalogSupersessionResult, raw, resultRaw []byte) supersessionUsage {
	t := func(v string) uint64 { return 8 + uint64(len(v)) }
	u := supersessionUsage{bytes: uint64(len(raw)+len(resultRaw)) + t(c.ID.String()) + t(c.Content.String()) + t(c.Old.ID().String()) + t(c.New.ID().String()) + t(c.Source.ID().String()) + t(c.Source.Revision().String()) + 40 + 9*8 + 64, events: 1, observations: uint64(len(c.Evidence)), links: uint64(len(c.Support))}
	for _, e := range c.Evidence {
		i := e.Input()
		coordinate, facts := i.Coordinate, i.Coordinate.Artifact().Facts()
		u.bytes += t(e.ID().String()) + t(c.ID.String()) + t(c.Content.String()) + t(c.Old.ID().String()) + t(c.New.ID().String()) + t(c.Source.ID().String()) + t(c.Source.Revision().String()) + t(facts.ID.String()) + 8 + t(i.AssertedOrigin) + t(i.ObservedAt.UTC().Format(time.RFC3339Nano)) + 8
		u.bytes += t(facts.ID.String()) + 32 + 8 + 8 + t(facts.ParserRevision) + t(facts.SourceRevision) + 8 + t(coordinate.Locator()) + 8 + uint64(len(coordinate.Snapshot().Bytes()))
		u.bytes += t(c.ID.String()) + t(e.ID().String())
	}
	return u
}

// The projector validates normalized provenance without constructing authority.
// With write=true it creates only absent exact source/batch/record rows.
func (r *catalogRepository) supersessionProject(ctx context.Context, raw []byte, write bool) (map[string]domain.SourceRegistration, error) {
	var matches map[string]bool
	if !write {
		matches = make(map[string]bool)
	}
	return r.supersessionProjectMatches(ctx, raw, write, matches)
}

func (r *catalogRepository) supersessionProjectMatches(ctx context.Context, raw []byte, write bool, matches map[string]bool) (map[string]domain.SourceRegistration, error) {
	d := catalogStateReader{raw: raw, repository: r, ctx: ctx, write: write, sources: map[string]domain.SourceRegistration{}}
	d.matches = matches
	d.text()
	d.text()
	command, content := d.text(), d.text()
	for range 2 {
		d.text()
		d.text()
		d.number()
		d.text()
		d.text()
		d.number()
		d.number()
	}
	for range 4 {
		d.number()
	}
	d.source()
	d.text()
	d.text()
	n := d.count(domain.MutationObservations.Maximum())
	for range n {
		id, owner, old, newer := d.text(), d.text(), d.text(), d.text()
		source := d.source()
		batch, index := d.coordinate(source, false)
		origin, observed, confidence := d.text(), d.text(), d.number()
		if !write {
			d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_supersession_evidence WHERE evidence_id=? AND command_id=? AND content_id=? AND old_version_id=? AND new_version_id=? AND source_id=? AND registration_revision=? AND batch_id=? AND record_index=? AND asserted_origin=? AND observed_at=? AND confidence=?)`, id, command, owner, old, newer, source.id, source.revision, batch, index, origin, observed, confidence)
		}
	}
	if !write {
		d.match("SELECT count(*)=? FROM tc_catalog_supersession_evidence WHERE command_id=? AND content_id=?", n, command, content)
	}
	n = d.count(domain.MutationObservations.Maximum())
	for range n {
		id := d.text()
		if !write {
			d.match("SELECT EXISTS(SELECT 1 FROM tc_catalog_supersession_support WHERE command_id=? AND evidence_id=?)", command, id)
		}
	}
	if !write {
		d.match("SELECT count(*)=? FROM tc_catalog_supersession_support WHERE command_id=?", n, command)
	}
	if d.err == nil && d.offset != len(raw) {
		d.err = app.ErrRepositoryUnavailable
	}
	return d.sources, d.err
}

func (r *catalogRepository) supersessionProvenance(ctx context.Context, c domain.CatalogSupersessionCommand) error {
	sourceRaw, _ := c.Source.PersistenceEncoding()
	var exists bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?)", c.Source.ID().String(), c.Source.Revision().String()).Scan(&exists); err != nil {
		return repositoryError(ctx, err)
	}
	var addSources uint64
	if exists {
		d := catalogStateReader{raw: sourceRaw, repository: r, ctx: ctx}
		d.source()
		if d.err != nil {
			return domain.ErrCatalogSourceMismatch
		}
	} else {
		addSources = 1
	}
	var addBatches, addBytes uint64
	batches, records := map[string]bool{}, map[string]bool{}
	for _, e := range c.Evidence {
		i := e.Input()
		coordinate := i.Coordinate
		f := coordinate.Artifact().Facts()
		key := f.ID.String()
		if err := r.catalogBatchKeyGuard(ctx, key); err != nil {
			return err
		}
		var count int
		var matched bool
		if err := r.transaction.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(source_id=? AND registration_revision=? AND artifact_sha256=? AND byte_length=? AND record_count=? AND parser_revision=? AND source_revision=?),0) FROM tc_catalog_batches WHERE batch_id=?`, c.Source.ID().String(), c.Source.Revision().String(), f.SHA256[:], f.ByteLength, f.RecordCount, f.ParserRevision, f.SourceRevision, key).Scan(&count, &matched); err != nil {
			return repositoryError(ctx, err)
		}
		if count != 0 && !matched {
			return domain.ErrCatalogArtifactConflict
		}
		if count == 0 && !batches[key] {
			addBatches++
			batches[key] = true
		}
		if err := r.transaction.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(locator=? AND snapshot=?),0) FROM tc_catalog_records WHERE batch_id=? AND record_index=?`, coordinate.Locator(), coordinate.Snapshot().Bytes(), key, coordinate.Index()).Scan(&count, &matched); err != nil {
			return repositoryError(ctx, err)
		}
		if count != 0 && !matched {
			return domain.ErrCatalogObservationConflict
		}
		// index is fixed-width to avoid ambiguities between adjacent batch IDs.
		recordKey := key + string(binary.BigEndian.AppendUint64(nil, coordinate.Index()))
		if count == 0 && !records[recordKey] {
			addBytes += uint64(len(coordinate.Snapshot().Bytes()))
			records[recordKey] = true
		}
		if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_supersession_evidence WHERE evidence_id=?)", e.ID().String()).Scan(&exists); err != nil {
			return repositoryError(ctx, err)
		}
		if exists {
			return domain.ErrCatalogObservationConflict
		}
	}
	var sources, count, staged uint64
	if err := r.transaction.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM tc_catalog_sources),(SELECT count(*) FROM tc_catalog_batches),(SELECT COALESCE(sum(length(snapshot)),0) FROM tc_catalog_records)+(SELECT COALESCE(sum(length(artifact)+length(report)),0) FROM tc_catalog_imports)`).Scan(&sources, &count, &staged); err != nil {
		return repositoryError(ctx, err)
	}
	for _, check := range []struct {
		resource       domain.Resource
		current, added uint64
	}{{domain.SourceRegistrations, sources, addSources}, {domain.StagingBatches, count, addBatches}, {domain.StagingBytes, staged, addBytes}} {
		if err := domain.CheckLimit(check.resource, check.current, check.added); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type catalogReadCacheKey struct{}

// A query holds one immutable SQLite snapshot. Cache only its current Content,
// never a map of all Contents or any state across callbacks. Writer contexts
// never carry this private key, so every writer validation remains uncached.
type catalogReadCache struct {
	repository *catalogRepository
	state      domain.CatalogSupersessionState
	valid      bool
}

func (r *catalogRepository) readSupersession(ctx context.Context, content identity.ContentID, bindings domain.CatalogPersistenceBindings) (domain.CatalogSupersessionState, error) {
	cache, _ := ctx.Value(catalogReadCacheKey{}).(*catalogReadCache)
	use := cache != nil && cache.repository == r && bindings.AllowUnbound && len(bindings.Sources) == 0 && len(bindings.Reviewers) == 0 && len(bindings.Namespaces) == 0
	if use && cache.valid && cache.state.Content() == content {
		return cache.state, ctx.Err()
	}
	state, err := r.readSupersessionUncached(ctx, content, bindings)
	if use && err == nil {
		cache.state = state
		cache.valid = true
	}
	return state, err
}

// Reconstruct the complete graph from immutable rows, bounded before selecting
// any BLOB. Historical endpoint/order contexts are resolved from raw envelopes;
// no reader here calls readStates (which in turn calls this function).
func (r *catalogRepository) readSupersessionUncached(ctx context.Context, content identity.ContentID, bindings domain.CatalogPersistenceBindings) (domain.CatalogSupersessionState, error) {
	var zero domain.CatalogSupersessionState
	if err := r.registrationAccounting(ctx, content); err != nil {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		// While a validated writer has enabled the leased connection, these
		// rows are prospective: preserve its admission error and roll back.
		// The same impossible budget in a read-only snapshot is unavailable.
		var limit domain.LimitError
		if errors.As(err, &limit) {
			var queryOnly int
			if e := r.transaction.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); e != nil {
				return zero, repositoryError(ctx, e)
			}
			if queryOnly == 0 {
				return zero, err
			}
		}
		return zero, app.ErrRepositoryUnavailable
	}
	u, err := r.supersessionUsage(ctx, content)
	if err != nil {
		return zero, err
	}
	if u.events > 1023 || u.observations > domain.RetainedObservations.Maximum() || u.links != u.observations || u.bytes > domain.RetainedBytes.Maximum() {
		return zero, app.ErrRepositoryUnavailable
	}
	var managed bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_managed WHERE content_id=?)", content.String()).Scan(&managed); err != nil {
		return zero, repositoryError(ctx, err)
	}
	if !managed {
		if u.events != 0 {
			return zero, app.ErrRepositoryUnavailable
		}
		return domain.NewCatalogSupersessionState(content, nil)
	}
	orders, _, err := r.readStatesRaw(ctx, content, domain.CatalogSubject{}, true, bindings)
	if err != nil {
		return zero, err
	}
	versions, err := r.catalogVersions(ctx, content)
	if err != nil {
		return zero, err
	}
	manifest := make([]domain.ContentVersion, 0, len(versions))
	for _, v := range versions {
		manifest = append(manifest, v.Version)
	}
	state, err := domain.NewCatalogSupersessionState(content, manifest)
	if err != nil {
		return zero, app.ErrRepositoryUnavailable
	}
	if u.events == 0 {
		return state, nil
	}
	if len(orders) == 0 {
		return zero, app.ErrRepositoryUnavailable
	}
	// The derived state is never stored as a second copy of the history.
	var raw []byte
	raw = registrationFrame(raw, []byte("pi08-version-supersession-state/1"))
	raw = registrationFrame(raw, []byte(content.String()))
	raw = binary.BigEndian.AppendUint64(raw, u.events+1)
	raw = binary.BigEndian.AppendUint64(raw, uint64(len(manifest)))
	for _, v := range manifest {
		raw = supersessionVersionFrame(raw, v)
	}
	raw = binary.BigEndian.AppendUint64(raw, u.events)
	rows, err := r.transaction.QueryContext(ctx, `SELECT CASE WHEN length(canonical_command) BETWEEN 1 AND 8388608 THEN canonical_command END, CASE WHEN length(canonical_result) BETWEEN 1 AND 8388608 THEN canonical_result END,command_sha256,result_sha256 FROM tc_catalog_supersessions WHERE content_id=? ORDER BY result_supersession LIMIT 1024`, content.String())
	if err != nil {
		return zero, repositoryError(ctx, err)
	}
	type pair struct{ command, result []byte }
	var pairs []pair
	for rows.Next() {
		var p pair
		var a, b []byte
		if err := rows.Scan(&p.command, &p.result, &a, &b); err != nil {
			rows.Close()
			return zero, repositoryError(ctx, err)
		}
		x, y := sha256.Sum256(p.command), sha256.Sum256(p.result)
		if len(p.command) == 0 || len(p.result) == 0 || !bytes.Equal(a, x[:]) || !bytes.Equal(b, y[:]) || uint64(len(raw)+16+len(p.command)+len(p.result)) > domain.RetainedBytes.Maximum() {
			rows.Close()
			return zero, app.ErrRepositoryUnavailable
		}
		raw = registrationFrame(raw, p.command)
		raw = registrationFrame(raw, p.result)
		pairs = append(pairs, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return zero, repositoryError(ctx, err)
	}
	if uint64(len(pairs)) != u.events {
		return zero, app.ErrRepositoryUnavailable
	}
	resolved := bindings
	resolved.AllowUnbound = true
	resolved.Sources = append([]domain.SourceRegistration(nil), bindings.Sources...)
	seen := map[string]bool{}
	for _, s := range resolved.Sources {
		seen[s.ID().String()+s.Revision().String()] = true
	}
	matches := make(map[string]bool)
	for _, p := range pairs {
		sources, e := r.supersessionProjectMatches(ctx, p.command, false, matches)
		if e != nil {
			return zero, e
		}
		for key, s := range sources {
			if !seen[key] {
				resolved.Sources = append(resolved.Sources, s)
				seen[key] = true
			}
		}
		if len(resolved.Sources) > int(domain.SourceRegistrations.Maximum()) {
			return zero, app.ErrRepositoryUnavailable
		}
	}
	state, err = domain.RehydrateCatalogSupersessionState(raw, resolved)
	if err != nil {
		return zero, app.ErrRepositoryUnavailable
	}
	// Load all subject histories once: protection can originate on a subject
	// other than either endpoint, and must be evaluated at each historical fence.
	subjects := []domain.CatalogSubject{}
	cs, _ := domain.ContentSubject(content)
	subjects = append(subjects, cs)
	for _, v := range manifest {
		s, _ := domain.VersionSubject(content, v.ID())
		subjects = append(subjects, s)
	}
	reviews := map[domain.CatalogSubject][]catalogStoredState{}
	for _, s := range subjects {
		// The order read above checked all database FKs. Nothing in this
		// reconstruction writes, so repeating that global scan for every
		// subject checks the same snapshot without adding validation.
		states, _, e := r.readStatesRawWithForeignKeys(ctx, content, s, false, bindings, true)
		if e != nil {
			return zero, e
		}
		reviews[s] = states
	}
	for _, edge := range state.History() {
		c, result := edge.Command, edge.Result
		// v9 edges never inherit legacy cross-kind overlaps. Validate the old
		// namespaces as well as the edge table's own immutable key.
		if err := r.registrationKeyGuard(ctx, c.ID.String(), false); err != nil {
			return zero, app.ErrRepositoryUnavailable
		}
		var overlap bool
		if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE command_id=?)", c.ID.String()).Scan(&overlap); err != nil {
			return zero, repositoryError(ctx, err)
		}
		if overlap {
			return zero, app.ErrRepositoryUnavailable
		}
		a, b := sha256.Sum256(pairs[int(result.SupersessionRevision.Uint64())-2].command), sha256.Sum256(pairs[int(result.SupersessionRevision.Uint64())-2].result)
		d := catalogStateReader{repository: r, ctx: ctx}
		d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_supersessions WHERE command_id=? AND content_id=? AND old_version_id=? AND new_version_id=? AND source_id=? AND registration_revision=? AND reviewer_principal=? AND expected_aggregate=? AND result_aggregate=? AND expected_supersession=? AND result_supersession=? AND old_review_revision=? AND new_review_revision=? AND order_revision=? AND protected_custom=? AND encoding=1 AND command_sha256=? AND result_sha256=?)`, c.ID.String(), content.String(), c.Old.ID().String(), c.New.ID().String(), c.Source.ID().String(), c.Source.Revision().String(), edge.Reviewer.String(), c.ExpectedAggregate.Uint64(), result.AggregateRevision.Uint64(), c.ExpectedSupersession.Uint64(), result.SupersessionRevision.Uint64(), result.OldReviewRevision.Uint64(), result.NewReviewRevision.Uint64(), result.OrderRevision.Uint64(), result.ProtectedCustom, a[:], b[:])
		if d.err != nil {
			return zero, d.err
		}
		fence := c.ExpectedAggregate.Uint64()
		oldS, _ := domain.VersionSubject(content, c.Old.ID())
		newS, _ := domain.VersionSubject(content, c.New.ID())
		old, newer, order := supersessionAt(reviews[oldS], fence), supersessionAt(reviews[newS], fence), supersessionAt(orders, fence)
		if old == nil || newer == nil || order == nil || old.revision != c.ExpectedOldReview.Uint64() || newer.revision != c.ExpectedNewReview.Uint64() || order.revision != result.OrderRevision.Uint64() {
			return zero, app.ErrRepositoryUnavailable
		}
		for _, v := range []domain.ContentVersion{c.Old, c.New} {
			table := "tc_catalog_order_state_versions"
			if order.encoding == 2 {
				table += "_v2"
			}
			d.match("SELECT EXISTS(SELECT 1 FROM "+table+" WHERE content_id=? AND order_revision=? AND version_id=?)", content.String(), order.revision, v.ID().String())
		}
		fingerprint, fp := newer.review.Field(domain.FingerprintClaimKey).Value()
		owner, own := newer.review.Field(domain.OwnershipClaimKey).Value()
		if d.err != nil || !fp || !own || fingerprint.Fingerprint() != c.New.Fingerprint() || owner.Owner() != content {
			return zero, app.ErrRepositoryUnavailable
		}
		var protected bool
		if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE content_id=? AND result_revision<=? AND protected_custom=1)", content.String(), fence).Scan(&protected); err != nil {
			return zero, repositoryError(ctx, err)
		}
		for _, history := range reviews {
			for _, s := range history {
				if s.aggregate <= fence && s.review.ProtectedCustom() {
					protected = true
				}
			}
		}
		if protected != result.ProtectedCustom {
			return zero, app.ErrRepositoryUnavailable
		}
	}
	return state, ctx.Err()
}

func supersessionAt(states []catalogStoredState, fence uint64) *catalogStoredState {
	var result *catalogStoredState
	for i := range states {
		if states[i].aggregate <= fence {
			result = &states[i]
		}
	}
	return result
}

func supersessionVersionFrame(raw []byte, v domain.ContentVersion) []byte {
	for _, s := range []string{v.ID().String(), v.ContentID().String()} {
		raw = registrationFrame(raw, []byte(s))
	}
	raw = binary.BigEndian.AppendUint64(raw, v.Fingerprint().AudioID().Uint64())
	raw = registrationFrame(raw, []byte(v.Fingerprint().Hash().String()))
	o := v.OrderEvidence()
	raw = registrationFrame(raw, []byte(o.Namespace()))
	raw = binary.BigEndian.AppendUint64(raw, o.Position())
	var known uint64
	if o.Known() {
		known = 1
	}
	return binary.BigEndian.AppendUint64(raw, known)
}
