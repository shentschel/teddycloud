package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var _ app.RegistrationRepository = (*catalogRepository)(nil)

// Registration uses the leased owner transaction. No method can outlive the
// callback, change a media binding, or independently commit its writes.
func (r *catalogRepository) LookupRegistration(ctx context.Context, op app.RegistrationOperation) (result domain.CatalogRegistrationResult, found bool, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, false, err
	}
	defer func() {
		done(&err)
		if err != nil {
			result = domain.CatalogRegistrationResult{}
			found = false
		}
	}()
	canonical, sources, err := r.registrationInput(call, op)
	if err != nil {
		return result, false, err
	}
	return r.lookupRegistration(call, op, canonical, sources)
}

func (r *catalogRepository) registrationInput(ctx context.Context, op app.RegistrationOperation) ([]byte, []domain.SourceRegistration, error) {
	// Live and persisted revocation both precede canonical comparison.
	sources, err := op.Capability.UsedSources(op.Command)
	if err != nil {
		return nil, nil, err
	}
	for _, source := range sources {
		if err := r.stateSourceActive(ctx, source); err != nil {
			return nil, nil, err
		}
	}
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		return nil, nil, err
	}
	if r.schemaVersion < 8 || r.schemaVersion > 9 {
		return nil, nil, app.ErrUnsupported
	}
	rows, err := r.transaction.QueryContext(ctx, "SELECT version,migration_id,checksum,dirty FROM tc_schema_migrations ORDER BY version")
	if err != nil {
		return nil, nil, repositoryError(ctx, err)
	}
	i := 0
	migrations := SchemaMigrations()
	for rows.Next() {
		var version, dirty int
		var id, sum string
		if err := rows.Scan(&version, &id, &sum, &dirty); err != nil {
			rows.Close()
			return nil, nil, repositoryError(ctx, err)
		}
		if i >= len(migrations) || version != migrations[i].Version || id != migrations[i].ID || sum != migrationChecksum(migrations[i]) || dirty != 0 {
			rows.Close()
			return nil, nil, app.ErrUnsupported
		}
		i++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, repositoryError(ctx, err)
	}
	if i != r.schemaVersion {
		return nil, nil, app.ErrUnsupported
	}
	return raw, sources, nil
}

func (r *catalogRepository) lookupRegistration(ctx context.Context, op app.RegistrationOperation, canonical []byte, sources []domain.SourceRegistration) (domain.CatalogRegistrationResult, bool, error) {
	var raw, resultRaw []byte
	var subject, content string
	err := r.transaction.QueryRowContext(ctx, `SELECT
 CASE WHEN length(canonical_command)<=8388608 THEN canonical_command END,
 CASE WHEN length(canonical_result)<=8388608 THEN canonical_result END,subject_id,content_id
 FROM tc_catalog_registrations WHERE command_id=?`, op.Command.ID.String()).Scan(&raw, &resultRaw, &subject, &content)
	if errors.Is(err, sql.ErrNoRows) {
		if err := r.registrationKeyGuard(ctx, op.Command.ID.String(), true); err != nil {
			return domain.CatalogRegistrationResult{}, false, err
		}
		return domain.CatalogRegistrationResult{}, false, nil
	}
	if err != nil || len(raw) == 0 || len(resultRaw) == 0 {
		return domain.CatalogRegistrationResult{}, false, app.ErrRepositoryUnavailable
	}
	if !bytes.Equal(raw, canonical) {
		return domain.CatalogRegistrationResult{}, false, domain.ErrCatalogCommandConflict
	}
	if subject != catalogSubjectKey(op.Command.Subject) || content != op.Command.Subject.ContentID().String() {
		return domain.CatalogRegistrationResult{}, false, app.ErrRepositoryUnavailable
	}
	bindings := domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: sources, Reviewers: []domain.IssuerPrincipal{op.Capability.Reviewer()}}
	states, _, err := r.readStates(ctx, op.Command.Subject.ContentID(), op.Command.Subject, false, bindings)
	if err != nil {
		return domain.CatalogRegistrationResult{}, false, err
	}
	if len(states) == 0 {
		return domain.CatalogRegistrationResult{}, false, app.ErrRepositoryUnavailable
	}
	result, found, err := states[len(states)-1].review.LookupRegistration(op.Capability, op.Command)
	if err != nil {
		return domain.CatalogRegistrationResult{}, false, err
	}
	encoded, err := result.CanonicalEncoding()
	if err != nil || !found || !bytes.Equal(encoded, resultRaw) {
		return domain.CatalogRegistrationResult{}, false, app.ErrRepositoryUnavailable
	}
	// The review read immediately above reconstructed the complete graph from
	// this unchanged transaction snapshot, including raw order envelopes.
	// Revalidate the semantic order state without reconstructing that graph.
	if _, _, err := r.readStatesWithGraph(ctx, op.Command.Subject.ContentID(), domain.CatalogSubject{}, true, bindings, false); err != nil {
		return domain.CatalogRegistrationResult{}, false, err
	}
	if err := r.registrationAccounting(ctx, op.Command.Subject.ContentID()); err != nil {
		return domain.CatalogRegistrationResult{}, false, err
	}
	return result, true, nil
}

func (r *catalogRepository) CommitRegistration(ctx context.Context, op app.RegistrationOperation) (result domain.CatalogRegistrationResult, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		done(&err)
		if err != nil {
			r.failure = err
			result = domain.CatalogRegistrationResult{}
		}
	}()
	canonical, sources, err := r.registrationInput(call, op)
	if err != nil {
		return result, err
	}
	if replay, found, e := r.lookupRegistration(call, op, canonical, sources); e != nil || found {
		return replay, e
	}
	command := op.Command
	id, subject := command.Subject.ContentID(), catalogSubjectKey(command.Subject)
	var occupied bool
	if err = r.transaction.QueryRowContext(call, `SELECT EXISTS(SELECT 1 FROM tc_catalog_content WHERE content_id=?) OR EXISTS(SELECT 1 FROM tc_content_versions WHERE version_id=?)`, subject, subject).Scan(&occupied); err != nil {
		return result, repositoryError(call, err)
	}
	if occupied {
		return result, domain.ErrCatalogCommandConflict
	}
	var aggregate uint64
	var protected bool
	var order domain.CatalogOrderState
	if command.Kind == domain.RegisterCatalogVersion {
		err = r.transaction.QueryRowContext(call, "SELECT aggregate_revision,protected_custom FROM tc_catalog_managed WHERE content_id=?", id.String()).Scan(&aggregate, &protected)
		if errors.Is(err, sql.ErrNoRows) {
			return result, app.ErrSubjectNotFound
		}
		if err != nil {
			return result, repositoryError(call, err)
		}
		if aggregate != command.Expected.Uint64() {
			return result, domain.ErrCatalogRevisionConflict
		}
		states, _, e := r.readStates(call, id, domain.CatalogSubject{}, true, domain.CatalogPersistenceBindings{AllowUnbound: true})
		if e != nil {
			return result, e
		}
		if len(states) == 0 {
			return result, app.ErrRepositoryUnavailable
		}
		order = states[len(states)-1].order
	} else {
		order, err = domain.NewCatalogOrderState(id, nil)
		if err != nil {
			return result, err
		}
	}
	orderRevision := order.Revision()
	if command.Kind == domain.RegisterCatalogVersion {
		orderRevision, err = domain.NewCatalogRevision(orderRevision.Uint64() + 1)
		if err != nil {
			return result, err
		}
	}
	review, result, err := domain.NewRegisteredCatalogReviewState(op.Capability, command, domain.CatalogRegistrationContext{OrderRevision: orderRevision, ProtectedCustom: protected})
	if err != nil {
		return result, err
	}
	if err = r.registrationEvidence(call, command, sources); err != nil {
		return result, err
	}
	var version domain.ContentVersion
	if command.Kind == domain.RegisterCatalogVersion {
		var pair domain.AudioFingerprint
		for _, field := range result.Fields {
			if field.Key == domain.FingerprintClaimKey {
				pair = field.Field.Alternatives[0].Claim.Fingerprint()
			}
		}
		version, err = domain.NewContentVersion(command.Subject.VersionID(), id, pair, domain.UnknownVersionOrderEvidence())
		if err != nil {
			return result, err
		}
		order, err = order.AppendRegisteredVersion(command.ID, result.AggregateRevision, version)
		if err != nil {
			return result, err
		}
	}
	reviewRaw, err := review.PersistenceEncoding()
	if err != nil {
		return result, err
	}
	orderRaw, err := order.PersistenceEncoding()
	if err != nil {
		return result, err
	}
	if command.Kind == domain.RegisterContent {
		orderRaw = registrationOrderBootstrap(orderRaw)
	}
	resultRaw, err := result.CanonicalEncoding()
	if err != nil {
		return result, err
	}
	if _, err = r.transaction.ExecContext(call, "PRAGMA query_only=OFF"); err != nil {
		return result, repositoryError(call, err)
	}
	defer func() {
		if _, e := r.transaction.ExecContext(r.ctx, "PRAGMA query_only=ON"); err == nil && e != nil {
			err = repositoryError(r.ctx, e)
		}
	}()
	exec := func(q string, args ...any) error {
		_, e := r.transaction.ExecContext(call, q, args...)
		return repositoryError(call, e)
	}
	if command.Kind == domain.RegisterContent {
		values := make([]any, 3)
		for _, field := range result.Fields {
			if len(field.Field.Alternatives) > 0 {
				values[int(field.Key)-1] = field.Field.Alternatives[0].Claim.Text()
			}
		}
		if err = exec("INSERT INTO tc_catalog_content VALUES (?,?,?,?)", id.String(), values[0], values[1], values[2]); err != nil {
			return result, err
		}
		if err = exec("INSERT INTO tc_catalog_managed VALUES (?,1,?)", id.String(), result.ProtectedCustom); err != nil {
			return result, err
		}
	} else {
		updated, e := r.transaction.ExecContext(call, "UPDATE tc_catalog_managed SET aggregate_revision=?,protected_custom=? WHERE content_id=? AND aggregate_revision=?", result.AggregateRevision.Uint64(), result.ProtectedCustom, id.String(), aggregate)
		if e != nil {
			return result, repositoryError(call, e)
		}
		if n, e := updated.RowsAffected(); e != nil || n != 1 {
			return result, domain.ErrCatalogRevisionConflict
		}
		hash, _ := hex.DecodeString(version.Fingerprint().Hash().String())
		if err = exec("INSERT INTO tc_content_versions VALUES (?,?,?,?,0,NULL,NULL)", version.ID().String(), id.String(), version.Fingerprint().AudioID().Uint64(), hash); err != nil {
			return result, err
		}
	}
	var vid any
	if command.Kind == domain.RegisterCatalogVersion {
		vid = subject
	}
	if err = exec("INSERT INTO tc_catalog_subjects VALUES (?,?,?,1)", subject, id.String(), vid); err != nil {
		return result, err
	}
	projection := catalogStateReader{raw: canonical, repository: r, ctx: call, content: id.String(), subject: subject, write: true}
	projection.registrationCommand()
	if projection.err != nil {
		return result, projection.err
	}
	if err = exec("INSERT INTO tc_catalog_registrations VALUES (?,?,?,?,?,?,?,1,?,?,?,?)", command.ID.String(), command.Kind, subject, id.String(), command.ExpectedKind, command.Expected.Uint64(), result.AggregateRevision.Uint64(), result.OrderRevision.Uint64(), result.ProtectedCustom, canonical, resultRaw); err != nil {
		return result, err
	}
	for _, source := range sources {
		if err = exec("INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,?)", command.ID.String(), source.ID().String(), source.Revision().String(), op.Capability.Reviewer().String(), source == command.Source); err != nil {
			return result, err
		}
	}
	for _, selection := range command.Selections {
		if err = exec(`INSERT INTO tc_catalog_registration_support SELECT ?,observation_id,subject_id,source_id,registration_revision,claim_key FROM tc_catalog_observations WHERE observation_id=?`, command.ID.String(), selection.Observation.String()); err != nil {
			return result, err
		}
	}
	sum := sha256.Sum256(reviewRaw)
	if err = exec("INSERT INTO tc_catalog_review_states_v2 VALUES (?,?,?,?,2,?,?,?,?)", subject, id.String(), result.AggregateRevision.Uint64(), 1, reviewRaw, len(reviewRaw), sum[:], command.ID.String()); err != nil {
		return result, err
	}
	sum = sha256.Sum256(orderRaw)
	if err = exec("INSERT INTO tc_catalog_order_states_v2 VALUES (?,?,?,2,?,?,?)", id.String(), result.AggregateRevision.Uint64(), result.OrderRevision.Uint64(), orderRaw, len(orderRaw), sum[:]); err != nil {
		return result, err
	}
	versions, e := r.catalogVersions(call, id)
	if e != nil {
		return result, e
	}
	for _, v := range versions {
		if err = exec("INSERT INTO tc_catalog_order_state_versions_v2 VALUES (?,?,?)", id.String(), result.OrderRevision.Uint64(), v.Version.ID().String()); err != nil {
			return result, err
		}
	}
	if _, found, e := r.lookupRegistration(call, op, canonical, sources); e != nil || !found {
		if e != nil {
			return result, e
		}
		return result, app.ErrRepositoryUnavailable
	}
	r.wrote = true
	tx := r.transaction
	r.authorize = append(r.authorize, func() error {
		if e := op.Capability.Authorize(command); e != nil {
			return e
		}
		for _, source := range sources {
			var revoked int
			if e := tx.QueryRowContext(r.ctx, "SELECT revoked FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?", source.ID().String(), source.Revision().String()).Scan(&revoked); e != nil {
				return repositoryError(r.ctx, e)
			}
			if revoked != 0 {
				return domain.ErrCatalogUnauthorized
			}
		}
		return ctx.Err()
	})
	return result, call.Err()
}

// The zero-growth order envelope is the registration bootstrap. The domain
// growth replay starts at the first version event; validate its base separately.
func registrationFrame(dst []byte, value []byte) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(value)))
	dst = append(dst, n[:]...)
	return append(dst, value...)
}
func registrationOrderBootstrap(base []byte) []byte {
	raw := registrationFrame(nil, []byte("pi08-order-state/2"))
	raw = registrationFrame(raw, base)
	return append(raw, make([]byte, 8)...)
}
func rehydrateRegistrationOrder(raw []byte, bindings domain.CatalogPersistenceBindings) (domain.CatalogOrderState, error) {
	d := catalogStateReader{raw: raw}
	if d.text() == "pi08-order-state/2" {
		base := []byte(d.text())
		count := d.number()
		if d.err == nil && count == 0 && d.offset == len(raw) {
			return domain.RehydrateCatalogOrderState(base, bindings)
		}
	}
	return domain.RehydrateCatalogOrderState(raw, bindings)
}

func registrationOrderTransition(previous, current catalogStoredState) error {
	if current.encoding != 2 {
		return nil
	}
	c := catalogStateReader{raw: current.raw}
	c.text()
	base := []byte(c.text())
	count := c.number()
	events := c.raw[c.offset:]
	if previous.encoding == 1 {
		if count != 1 || !bytes.Equal(base, previous.raw) {
			return app.ErrRepositoryUnavailable
		}
		return nil
	}
	p := catalogStateReader{raw: previous.raw}
	p.text()
	oldBase := []byte(p.text())
	oldCount := p.number()
	oldEvents := p.raw[p.offset:]
	if oldCount > 0 && (!bytes.Equal(base, oldBase) || count != oldCount+1 || !bytes.HasPrefix(events, oldEvents)) || oldCount == 0 && count > 0 && (count != 1 || !bytes.Equal(base, oldBase)) {
		return app.ErrRepositoryUnavailable
	}
	return nil
}

// Read the independently bounded registration grammar while projecting exact
// sources, artifacts and observations. Semantic replay remains domain-owned.
func (d *catalogStateReader) registrationCommand() (key string, kind uint64, reviewer string, operator catalogStoredSource, selections map[string]uint64, expectedKind, expected uint64) {
	if d.text() != "pi08-registration/1" || d.text() != domain.CatalogDecisionPolicy {
		d.err = app.ErrRepositoryUnavailable
	}
	kind = d.number()
	key = d.text()
	content, version := d.text(), d.text()
	if content != d.content || (version != "" && version != d.subject) || (version == "" && content != d.subject) {
		d.err = app.ErrRepositoryUnavailable
	}
	expectedKind = d.number()
	expected = d.number()
	operator = d.source()
	reviewer = d.text()
	for n := d.count(domain.SourceRegistrations.Maximum()); n > 0; n-- {
		d.source()
	}
	d.text()
	for n := d.count(domain.MutationObservations.Maximum()); n > 0; n-- {
		d.observation()
	}
	selections = make(map[string]uint64)
	for n := d.count(domain.SelectedSupport.Maximum()); n > 0; n-- {
		claim, id := d.number(), d.text()
		if _, exists := selections[id]; exists {
			d.err = app.ErrRepositoryUnavailable
		}
		selections[id] = claim
	}
	if d.offset != len(d.raw) {
		d.err = app.ErrRepositoryUnavailable
	}
	return
}

func (d *catalogStateReader) checkEnvelope(s *catalogStoredState, versions []catalogVersionRow, latest bool) error {
	probe := catalogStateReader{raw: d.raw}
	tag := probe.text()
	if s.encoding == 0 || s.encoding == 1 {
		want := "pi08-review-state/1"
		if d.order {
			want = "pi08-order-state/1"
		}
		if tag != want {
			return app.ErrRepositoryUnavailable
		}
		return d.check(s, versions, latest)
	}
	if d.order {
		return d.checkGrowthOrder(s, versions, latest)
	}
	if tag != "pi08-review-state/2" {
		return app.ErrRepositoryUnavailable
	}
	commandRaw, resultRaw, tail := []byte(probe.text()), []byte(probe.text()), []byte(probe.text())
	if probe.err != nil || probe.offset != len(probe.raw) {
		return app.ErrRepositoryUnavailable
	}
	command := *d
	command.raw = commandRaw
	command.offset = 0
	if command.sources == nil {
		command.sources = make(map[string]domain.SourceRegistration)
	}
	key, kind, reviewer, operator, selections, expectedKind, expected := command.registrationCommand()
	if command.err != nil {
		return command.err
	}
	result := catalogStateReader{raw: resultRaw}
	if result.text() != "pi08-registration-result/1" || result.number() != kind || result.text() != key || result.text() != d.content {
		return app.ErrRepositoryUnavailable
	}
	version := result.text()
	aggregate, reviewRevision, orderRevision, protected := result.number(), result.number(), result.number(), result.number()
	if version != "" && version != d.subject || reviewRevision != 1 {
		return app.ErrRepositoryUnavailable
	}
	d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE command_id=? AND kind=? AND subject_id=? AND content_id=? AND expected_kind=? AND expected_revision=? AND result_revision=? AND review_revision=1 AND order_revision=? AND protected_custom=? AND canonical_command=? AND canonical_result=?)`, key, kind, d.subject, d.content, expectedKind, expected, aggregate, orderRevision, protected, commandRaw, resultRaw)
	if !d.write {
		d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_review_states_v2 WHERE subject_id=? AND review_revision=? AND bootstrap_command=?)`, d.subject, s.revision, key)
	}
	d.match("SELECT count(*)=? FROM tc_catalog_registration_sources WHERE command_id=?", len(command.sources), key)
	for _, source := range command.sources {
		d.match("SELECT EXISTS(SELECT 1 FROM tc_catalog_registration_sources WHERE command_id=? AND source_id=? AND registration_revision=? AND reviewer_principal=? AND is_operator=?)", key, source.ID().String(), source.Revision().String(), reviewer, source.ID().String() == operator.id && source.Revision().String() == operator.revision)
	}
	d.match("SELECT count(*)=? FROM tc_catalog_registration_support WHERE command_id=?", len(selections), key)
	for id, claim := range selections {
		d.match("SELECT EXISTS(SELECT 1 FROM tc_catalog_registration_support WHERE command_id=? AND observation_id=? AND subject_id=? AND claim_key=?)", key, id, d.subject, claim)
	}
	if d.err != nil {
		return d.err
	}
	t := *d
	t.raw = tail
	t.offset = 0
	p := catalogStateReader{raw: tail}
	if p.text() != "pi08-review-tail/2" {
		return app.ErrRepositoryUnavailable
	}
	return t.check(s, versions, latest)
}

func (d *catalogStateReader) checkGrowthOrder(s *catalogStoredState, versions []catalogVersionRow, latest bool) error {
	p := catalogStateReader{raw: d.raw}
	if p.text() != "pi08-order-state/2" {
		return app.ErrRepositoryUnavailable
	}
	base := []byte(p.text())
	count := p.count(domain.RetainedDecisions.Maximum())
	bd := catalogStateReader{raw: base}
	if bd.text() != "pi08-order-state/1" || bd.text() != d.content {
		return app.ErrRepositoryUnavailable
	}
	baseRevision := bd.number()
	bs := catalogStoredState{raw: base, revision: baseRevision, aggregate: s.aggregate}
	b := *d
	b.raw = base
	b.offset = 0
	b.skipManifest = true
	if err := b.check(&bs, versions, false); err != nil {
		return err
	}
	s.decisions = append(s.decisions, bs.decisions...)
	// The base manifest is historical. Every growth adds exactly one tuple.
	manifest := catalogStateReader{raw: base}
	manifest.text()
	manifest.text()
	manifest.number()
	manifest.number()
	ids := make(map[string]bool)
	for n := manifest.count(domain.RetainedVersions.Maximum()); n > 0; n-- {
		id := manifest.text()
		ids[id] = true
		manifest.text()
		manifest.number()
		manifest.text()
		manifest.text()
		manifest.number()
		manifest.number()
	}
	for i := 0; i < count; i++ {
		switch p.number() {
		case 1:
			key, aggregate, id, owner, audio, hash, namespace, position, known := p.text(), p.number(), p.text(), p.text(), p.number(), p.text(), p.text(), p.number(), p.number()
			if ids[id] || owner != d.content || namespace != "" || position != 0 || known != 0 {
				return app.ErrRepositoryUnavailable
			}
			ids[id] = true
			d.match("SELECT EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE command_id=? AND kind=2 AND subject_id=? AND content_id=? AND result_revision=? AND order_revision=?)", key, id, d.content, aggregate, baseRevision+uint64(i)+1)
			found := false
			for _, v := range versions {
				if v.Version.ID().String() == id && v.Version.Fingerprint().AudioID().Uint64() == audio && v.Version.Fingerprint().Hash().String() == hash {
					found = true
				}
			}
			if !found {
				return app.ErrRepositoryUnavailable
			}
		case 2:
			cmd, res := []byte(p.text()), []byte(p.text())
			// Reuse the exact v1 command/result projection, with the framed
			// command and result restored to the v1 decision representation.
			raw := append([]byte(nil), cmd...)
			raw = registrationFrame(raw, cmd)
			raw = append(raw, res...)
			e := *d
			e.raw = raw
			e.offset = 0
			decision := e.orderDecision()
			if e.err != nil || e.offset != len(raw) {
				return app.ErrRepositoryUnavailable
			}
			s.decisions = append(s.decisions, decision)
		default:
			return app.ErrRepositoryUnavailable
		}
	}
	if p.err != nil || p.offset != len(p.raw) || s.revision != baseRevision+uint64(count) || latest && len(ids) != len(versions) {
		return app.ErrRepositoryUnavailable
	}
	for id := range ids {
		d.insert("INSERT INTO tc_catalog_order_state_versions_v2 VALUES (?,?,?)", d.content, s.revision, id)
		d.match("SELECT EXISTS(SELECT 1 FROM tc_catalog_order_state_versions_v2 WHERE content_id=? AND order_revision=? AND version_id=?)", d.content, s.revision, id)
	}
	d.match("SELECT count(*)=? FROM tc_catalog_order_state_versions_v2 WHERE content_id=? AND order_revision=?", len(ids), d.content, s.revision)
	return d.err
}

func (r *catalogRepository) registrationKeyOnly(ctx context.Context, key string) error {
	if r.schemaVersion < 8 {
		return nil
	}
	var used bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE command_id=?)", key).Scan(&used); err != nil {
		return repositoryError(ctx, err)
	}
	if !used && r.schemaVersion == 9 {
		if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_supersessions WHERE command_id=?)", key).Scan(&used); err != nil {
			return repositoryError(ctx, err)
		}
	}
	if used {
		return domain.ErrCatalogCommandConflict
	}
	return nil
}

// Existing exact provenance remains readable, including legacy overlaps. A
// newly created v9 batch cannot occupy any catalog command namespace.
func (r *catalogRepository) catalogBatchKeyGuard(ctx context.Context, key string) error {
	if err := r.registrationKeyOnly(ctx, key); err != nil {
		return err
	}
	if r.schemaVersion != 9 {
		return nil
	}
	var exists bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_batches WHERE batch_id=?)", key).Scan(&exists); err != nil {
		return repositoryError(ctx, err)
	}
	if exists {
		return nil
	}
	return r.registrationKeyGuard(ctx, key, false)
}

func (r *catalogRepository) registrationKeyGuard(ctx context.Context, key string, registrations bool) error {
	if registrations {
		if err := r.registrationKeyOnly(ctx, key); err != nil {
			return err
		}
	}
	var used bool
	if err := r.transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tc_catalog_decisions WHERE command_id=?) OR EXISTS(SELECT 1 FROM tc_catalog_batches WHERE batch_id=?)`, key, key).Scan(&used); err != nil {
		return repositoryError(ctx, err)
	}
	if used {
		return domain.ErrCatalogCommandConflict
	}
	q := `SELECT CASE WHEN length(state)<=8388608 THEN state END FROM tc_catalog_order_states WHERE instr(state,?)>0`
	args := []any{key}
	if r.schemaVersion >= 8 {
		q += ` UNION ALL SELECT CASE WHEN length(state)<=8388608 THEN state END FROM tc_catalog_order_states_v2 WHERE instr(state,?)>0`
		args = append(args, key)
	}
	rows, err := r.transaction.QueryContext(ctx, q, args...)
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return repositoryError(ctx, err)
		}
		keys, err := registrationOrderKeys(raw)
		if err != nil {
			return err
		}
		if keys[key] {
			return domain.ErrCatalogCommandConflict
		}
	}
	return repositoryError(ctx, rows.Err())
}

func registrationOrderKeys(raw []byte) (map[string]bool, error) {
	d := catalogStateReader{raw: raw}
	keys := make(map[string]bool)
	switch d.text() {
	case "pi08-order-state/1":
		d.content = d.text()
		d.number()
		d.number()
		for n := d.count(domain.RetainedVersions.Maximum()); n > 0; n-- {
			d.text()
			d.text()
			d.number()
			d.text()
			d.text()
			d.number()
			d.number()
		}
		for n := d.count(domain.RetainedObservations.Maximum()); n > 0; n-- {
			d.sequence()
		}
		for n := d.count(domain.RetainedDecisions.Maximum()); n > 0; n-- {
			command := d.orderDecision()
			keys[command.id] = true
		}
	case "pi08-order-state/2":
		var err error
		base := []byte(d.text())
		probe := catalogStateReader{raw: base}
		if probe.text() != "pi08-order-state/1" {
			return nil, app.ErrRepositoryUnavailable
		}
		keys, err = registrationOrderKeys(base)
		if err != nil {
			return nil, err
		}
		for n := d.count(domain.RetainedDecisions.Maximum()); n > 0; n-- {
			switch d.number() {
			case 1:
				keys[d.text()] = true
				d.number()
				d.text()
				d.text()
				d.number()
				d.text()
				d.text()
				d.number()
				d.number()
			case 2:
				c := catalogStateReader{raw: []byte(d.text())}
				c.text()
				c.text()
				keys[c.text()] = true
				d.text()
				if c.err != nil {
					return nil, app.ErrRepositoryUnavailable
				}
			default:
				return nil, app.ErrRepositoryUnavailable
			}
		}
	default:
		return nil, app.ErrRepositoryUnavailable
	}
	if d.err != nil || d.offset != len(raw) {
		return nil, app.ErrRepositoryUnavailable
	}
	return keys, nil
}

// Identity comparisons precede inserts; a changed retained batch, record or
// observation reports the evidence error rather than a raw constraint failure.
func (r *catalogRepository) registrationEvidence(ctx context.Context, command domain.CatalogRegistrationCommand, sources []domain.SourceRegistration) error {
	for _, source := range sources {
		var exists bool
		if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?)", source.ID().String(), source.Revision().String()).Scan(&exists); err != nil {
			return repositoryError(ctx, err)
		}
		if exists {
			raw, _ := source.PersistenceEncoding()
			d := catalogStateReader{raw: raw, repository: r, ctx: ctx}
			d.source()
			if d.err != nil {
				return domain.ErrCatalogSourceMismatch
			}
		}
	}
	for _, observation := range command.Observations {
		input := observation.Input()
		c := input.Coordinate
		artifact := c.Artifact()
		facts := artifact.Facts()
		source := artifact.Source()
		if facts.ID.String() == command.ID.String() {
			return domain.ErrCatalogCommandConflict
		}
		if err := r.catalogBatchKeyGuard(ctx, facts.ID.String()); err != nil {
			return err
		}
		var count int
		var matched bool
		if err := r.transaction.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(source_id=? AND registration_revision=? AND artifact_sha256=? AND byte_length=? AND record_count=? AND parser_revision=? AND source_revision=?),0) FROM tc_catalog_batches WHERE batch_id=?`, source.ID().String(), source.Revision().String(), facts.SHA256[:], facts.ByteLength, facts.RecordCount, facts.ParserRevision, facts.SourceRevision, facts.ID.String()).Scan(&count, &matched); err != nil {
			return repositoryError(ctx, err)
		}
		if count != 0 && !matched {
			return domain.ErrCatalogArtifactConflict
		}
		if err := r.transaction.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(locator=? AND snapshot=?),0) FROM tc_catalog_records WHERE batch_id=? AND record_index=?`, c.Locator(), c.Snapshot().Bytes(), facts.ID.String(), c.Index()).Scan(&count, &matched); err != nil {
			return repositoryError(ctx, err)
		}
		if count != 0 && !matched {
			return domain.ErrCatalogObservationConflict
		}
		var text, audio, hash, owner any
		switch input.Claim.Key() {
		case domain.TitleClaimKey, domain.ModelClaimKey, domain.ArticleClaimKey:
			text = input.Claim.Text()
		case domain.FingerprintClaimKey:
			var n [8]byte
			binary.BigEndian.PutUint64(n[:], input.Claim.Fingerprint().AudioID().Uint64())
			audio = n[:]
			hash, _ = hex.DecodeString(input.Claim.Fingerprint().Hash().String())
		case domain.OwnershipClaimKey:
			owner = input.Claim.Owner().String()
		}
		if err := r.transaction.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(subject_id=? AND content_id=? AND claim_key=? AND text_value IS ? AND audio_id IS ? AND audio_sha1 IS ? AND owner_content_id IS ? AND batch_id=? AND record_index=? AND source_id=? AND registration_revision=? AND asserted_origin=? AND observed_at=? AND confidence=?),0) FROM tc_catalog_observations WHERE observation_id=?`, catalogSubjectKey(command.Subject), command.Subject.ContentID().String(), input.Claim.Key(), text, audio, hash, owner, facts.ID.String(), c.Index(), source.ID().String(), source.Revision().String(), input.AssertedOrigin, input.ObservedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), input.Confidence, observation.ID().String()).Scan(&count, &matched); err != nil {
			return repositoryError(ctx, err)
		}
		if count != 0 && !matched {
			return domain.ErrCatalogObservationConflict
		}
	}
	return nil
}

// Charge retained physical copies, not SQLite page sizes. Each index column
// uses its normative N(integer) or T(bytes) encoding, including framing bytes.
func (r *catalogRepository) registrationAccounting(ctx context.Context, content identity.ContentID) error {
	return r.registrationAccountingWith(ctx, content, supersessionUsage{})
}

func (r *catalogRepository) registrationAccountingWith(ctx context.Context, content identity.ContentID, added supersessionUsage) error {
	if r.schemaVersion < 8 {
		return nil
	}
	var bytesRetained, observations, links, events uint64
	err := r.transaction.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)),0) FROM tc_catalog_decisions WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_review_states WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_order_states WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_review_states_v2 WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_order_states_v2 WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)),0) FROM tc_catalog_registrations WHERE content_id=?1)+
 (SELECT COALESCE(sum(COALESCE(length(o.text_value),0)+COALESCE(length(b.snapshot),0)+512),0) FROM tc_catalog_observations o LEFT JOIN tc_catalog_records b ON b.batch_id=o.batch_id AND b.record_index=o.record_index WHERE o.content_id=?1)+
 (SELECT COALESCE(sum(8+length(s.command_id)+8+length(s.source_id)+8+length(s.registration_revision)+8+length(s.reviewer_principal)+8),0) FROM tc_catalog_registration_sources s JOIN tc_catalog_registrations r USING(command_id) WHERE r.content_id=?1)+
 (SELECT COALESCE(sum(8+length(s.command_id)+8+length(s.observation_id)+8+length(s.subject_id)+8+length(s.source_id)+8+length(s.registration_revision)+8),0) FROM tc_catalog_registration_support s JOIN tc_catalog_registrations r USING(command_id) WHERE r.content_id=?1)+
 (SELECT COALESCE(sum(8+length(content_id)+8+8+length(version_id)),0) FROM tc_catalog_order_state_versions_v2 WHERE content_id=?1),
 (SELECT count(*) FROM tc_catalog_observations WHERE content_id=?1),
 (SELECT count(*) FROM tc_catalog_decision_support s JOIN tc_catalog_subjects u USING(subject_id) WHERE u.content_id=?1)+(SELECT count(*) FROM tc_catalog_registration_support s JOIN tc_catalog_registrations r USING(command_id) WHERE r.content_id=?1),
 (SELECT count(*) FROM tc_catalog_decisions WHERE content_id=?1)+(SELECT count(*) FROM tc_catalog_registrations WHERE content_id=?1)+(SELECT count(*) FROM tc_catalog_order_states WHERE content_id=?1 AND order_revision>1)+(SELECT count(*) FROM tc_catalog_order_states_v2 o WHERE content_id=?1 AND order_revision>1 AND NOT EXISTS(SELECT 1 FROM tc_catalog_registrations r WHERE r.content_id=o.content_id AND r.result_revision=o.aggregate_revision))`, content.String()).Scan(&bytesRetained, &observations, &links, &events)
	if err != nil {
		return repositoryError(ctx, err)
	}
	if r.schemaVersion == 9 {
		usage, err := r.supersessionUsage(ctx, content)
		if err != nil {
			return err
		}
		bytesRetained += usage.bytes + added.bytes
		observations += usage.observations + added.observations
		links += usage.links + added.links
		events += usage.events + added.events
	}
	var orderRaw []byte
	err = r.transaction.QueryRowContext(ctx, `SELECT CASE WHEN length(state)<=8388608 THEN state END FROM (SELECT order_revision,state FROM tc_catalog_order_states WHERE content_id=?1 UNION ALL SELECT order_revision,state FROM tc_catalog_order_states_v2 WHERE content_id=?1) ORDER BY order_revision DESC LIMIT 1`, content.String()).Scan(&orderRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return repositoryError(ctx, err)
	}
	if err == nil {
		orderLinks, orderObservations, e := registrationOrderUsage(orderRaw)
		if e != nil {
			return e
		}
		if e = domain.CheckLimit(domain.RetainedSupportLinks, links, orderLinks); e != nil {
			return e
		}
		if e = domain.CheckLimit(domain.RetainedObservations, observations, orderObservations); e != nil {
			return e
		}
	}
	for _, check := range []struct {
		resource domain.Resource
		count    uint64
	}{{domain.RetainedBytes, bytesRetained}, {domain.RetainedObservations, observations}, {domain.RetainedSupportLinks, links}, {domain.RetainedDecisions, events}} {
		if err := domain.CheckLimit(check.resource, 0, check.count); err != nil {
			return err
		}
	}
	var sources, batches, staged uint64
	if err := r.transaction.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM tc_catalog_sources),(SELECT count(*) FROM tc_catalog_batches),(SELECT COALESCE(sum(length(snapshot)),0) FROM tc_catalog_records)+(SELECT COALESCE(sum(length(artifact)+length(report)),0) FROM tc_catalog_imports)`).Scan(&sources, &batches, &staged); err != nil {
		return repositoryError(ctx, err)
	}
	for _, check := range []struct {
		resource domain.Resource
		count    uint64
	}{{domain.SourceRegistrations, sources}, {domain.StagingBatches, batches}, {domain.StagingBytes, staged}} {
		if err := domain.CheckLimit(check.resource, 0, check.count); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Order evidence has no separate observation/support table. Count the current
// logical history once, in addition to the physically retained state bytes.
func registrationOrderUsage(raw []byte) (links, observations uint64, err error) {
	d := catalogStateReader{raw: raw}
	tag := d.text()
	var growth *catalogStateReader
	var events int
	if tag == "pi08-order-state/2" {
		base := []byte(d.text())
		events = d.count(domain.RetainedDecisions.Maximum())
		outer := d
		growth = &outer
		d = catalogStateReader{raw: base}
		tag = d.text()
	}
	if tag != "pi08-order-state/1" {
		return 0, 0, app.ErrRepositoryUnavailable
	}
	d.content = d.text()
	d.number()
	links = d.number()
	for n := d.count(domain.RetainedVersions.Maximum()); n > 0; n-- {
		d.text()
		d.text()
		d.number()
		d.text()
		d.text()
		d.number()
		d.number()
	}
	seen := make(map[string]bool)
	sequence := func(p *catalogStateReader) {
		source := p.namespace()
		id := p.text()
		p.text()
		p.text()
		p.number()
		p.coordinate(source, true)
		seen[id] = true
	}
	for n := d.count(domain.RetainedObservations.Maximum()); n > 0; n-- {
		sequence(&d)
	}
	if d.err != nil {
		return 0, 0, app.ErrRepositoryUnavailable
	}
	if growth != nil {
		for i := 0; i < events; i++ {
			switch growth.number() {
			case 1:
				growth.text()
				growth.number()
				growth.text()
				growth.text()
				growth.number()
				growth.text()
				growth.text()
				growth.number()
				growth.number()
			case 2:
				c := catalogStateReader{raw: []byte(growth.text())}
				growth.text()
				c.text()
				c.text()
				c.text()
				c.text()
				c.namespace()
				c.text()
				c.number()
				c.text()
				for n := c.count(domain.RetainedVersions.Maximum()); n > 0; n-- {
					c.text()
					c.number()
					count := c.count(domain.SelectedSupport.Maximum())
					links += uint64(count)
					for j := 0; j < count; j++ {
						c.text()
					}
				}
				for n := c.count(domain.MutationObservations.Maximum()); n > 0; n-- {
					sequence(&c)
				}
				n := c.count(domain.SelectedSupport.Maximum())
				links += uint64(n)
				for j := 0; j < n; j++ {
					c.text()
				}
				if c.err != nil || c.offset != len(c.raw) {
					return 0, 0, app.ErrRepositoryUnavailable
				}
			default:
				return 0, 0, app.ErrRepositoryUnavailable
			}
		}
		if growth.err != nil || growth.offset != len(growth.raw) {
			return 0, 0, app.ErrRepositoryUnavailable
		}
	}
	return links, uint64(len(seen)), nil
}

func (r *catalogRepository) registeredFields(ctx context.Context, record *app.CatalogRecord) error {
	versions, err := r.catalogVersions(ctx, record.Content)
	if err != nil {
		return err
	}
	content, _ := domain.ContentSubject(record.Content)
	subjects := []domain.CatalogSubject{content}
	for _, v := range versions {
		subject, _ := domain.VersionSubject(record.Content, v.Version.ID())
		subjects = append(subjects, subject)
	}
	for _, subject := range subjects {
		states, _, err := r.readStates(ctx, record.Content, subject, false, domain.CatalogPersistenceBindings{AllowUnbound: true})
		if err != nil {
			return err
		}
		if len(states) == 0 {
			return app.ErrRepositoryUnavailable
		}
		if subject == content {
			state := states[len(states)-1].review
			record.Title = state.Field(domain.TitleClaimKey)
			record.Model = state.Field(domain.ModelClaimKey)
			record.Article = state.Field(domain.ArticleClaimKey)
			if record.Title.Status != domain.CatalogFactUnknown || record.Model.Status != domain.CatalogFactUnknown || record.Article.Status != domain.CatalogFactUnknown {
				record.Review = app.CatalogReviewed
			}
		}
	}
	return nil
}

func (r *catalogRepository) registrationSnapshot(ctx context.Context, content identity.ContentID, target io.Writer) error {
	if r.schemaVersion < 8 {
		return nil
	}
	if r.schemaVersion == 9 {
		state, err := r.readSupersession(ctx, content, domain.CatalogPersistenceBindings{AllowUnbound: true})
		if err != nil {
			return err
		}
		raw, err := state.PersistenceEncoding()
		if err != nil {
			return app.ErrRepositoryUnavailable
		}
		sum := sha256.Sum256(raw)
		if _, err := target.Write(sum[:]); err != nil {
			return app.ErrRepositoryUnavailable
		}
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT canonical_command,canonical_result FROM tc_catalog_registrations WHERE content_id=?1 ORDER BY result_revision`, content.String())
	if err != nil {
		return repositoryError(ctx, err)
	}
	for rows.Next() {
		var command, result []byte
		if err := rows.Scan(&command, &result); err != nil {
			rows.Close()
			return repositoryError(ctx, err)
		}
		a, b := sha256.Sum256(command), sha256.Sum256(result)
		target.Write(a[:])
		target.Write(b[:])
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return repositoryError(ctx, err)
	}
	rows, err = r.transaction.QueryContext(ctx, `SELECT state_sha256 FROM (SELECT subject_id,review_revision AS revision,state_sha256 FROM tc_catalog_review_states_v2 WHERE content_id=?1 UNION ALL SELECT content_id,order_revision,state_sha256 FROM tc_catalog_order_states_v2 WHERE content_id=?1) ORDER BY subject_id,revision,state_sha256`, content.String())
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer rows.Close()
	for rows.Next() {
		var digest []byte
		if err := rows.Scan(&digest); err != nil {
			return repositoryError(ctx, err)
		}
		if len(digest) != 32 {
			return app.ErrRepositoryUnavailable
		}
		target.Write(digest)
	}
	return repositoryError(ctx, rows.Err())
}
