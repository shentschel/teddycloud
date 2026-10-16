package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var _ app.CatalogTransactor = (*LifecycleOwner)(nil)
var _ app.CatalogRepository = (*catalogRepository)(nil)

// WithinCatalogTransaction exposes the read-only PI-08 query checkpoint through
// the existing restore fence. Even successful callbacks roll back their SQL
// snapshot; query_only also prevents accidental writes on the leased connection.
func (owner *LifecycleOwner) WithinCatalogTransaction(ctx context.Context, operation func(app.CatalogRepository) error) error {
	if ctx == nil || operation == nil {
		return app.ErrInvalidInput
	}
	if owner == nil || owner.gate == nil {
		return app.ErrRepositoryUnavailable
	}
	if ctx.Value(contentOperationContextKey{}) != nil {
		return app.ErrRepositoryContention
	}
	ctx, cancel := context.WithTimeout(ctx, domain.CatalogTransactionTimeout)
	defer cancel()
	if err := owner.gate.enterOperation(ctx); err != nil {
		return repositoryError(ctx, err)
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return app.ErrRepositoryUnavailable
	}
	return owner.readCatalogTransaction(ctx, operation)
}

func (owner *LifecycleOwner) readCatalogTransaction(ctx context.Context, operation func(app.CatalogRepository) error) (result error) {
	connection, err := owner.database.db.Conn(ctx)
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer connection.Close()
	var previous int
	if err := connection.QueryRowContext(ctx, "PRAGMA query_only").Scan(&previous); err != nil {
		return repositoryError(ctx, err)
	}
	if _, err := connection.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return repositoryError(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		setting := "PRAGMA query_only=OFF"
		if previous != 0 {
			setting = "PRAGMA query_only=ON"
		}
		if _, err := connection.ExecContext(cleanup, setting); err != nil {
			result = errors.Join(result, app.ErrRepositoryUnavailable)
		}
	}()
	tx, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer tx.Rollback()
	var version, dirty int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0),COALESCE(MAX(dirty),0) FROM tc_schema_migrations").Scan(&version, &dirty); err != nil {
		return repositoryError(ctx, err)
	}
	// v6 adds state envelopes without changing the accepted v5 query tables.
	if (version != 5 && version != 6) || dirty != 0 {
		return app.ErrUnsupported
	}
	repository := &catalogRepository{transaction: tx, ctx: ctx, owner: owner, database: owner.database, generation: owner.generation.Load(), schemaVersion: version}
	defer repository.revoke()
	result = operation(repository)
	repository.revoke()
	if result == nil {
		result = ctx.Err()
	}
	if result == nil {
		result = repository.failure
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(result, repositoryError(ctx, err))
	}
	return result
}

type catalogRepository struct {
	mutex         sync.Mutex
	transaction   *sql.Tx
	ctx           context.Context
	owner         *LifecycleOwner
	database      *Database
	generation    uint64
	failure       error
	schemaVersion int
}

func (r *catalogRepository) revoke() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.transaction = nil
}

func (r *catalogRepository) LoadReview(ctx context.Context, subject domain.CatalogSubject) (result app.ReviewSnapshot, found bool, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, false, err
	}
	defer func() {
		done(&err)
		if err != nil {
			result = app.ReviewSnapshot{}
			found = false
		}
	}()
	if subject.ContentID().IsZero() {
		return result, false, app.ErrInvalidInput
	}
	states, aggregate, err := r.readStates(call, subject.ContentID(), subject, false, domain.CatalogPersistenceBindings{AllowUnbound: true})
	if err != nil || len(states) == 0 {
		return result, false, err
	}
	return app.ReviewSnapshot{AggregateRevision: aggregate, State: states[len(states)-1].review}, true, nil
}

func (r *catalogRepository) LoadOrder(ctx context.Context, content identity.ContentID) (result app.OrderSnapshot, found bool, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, false, err
	}
	defer func() {
		done(&err)
		if err != nil {
			result = app.OrderSnapshot{}
			found = false
		}
	}()
	if content.IsZero() {
		return result, false, app.ErrInvalidInput
	}
	states, aggregate, err := r.readStates(call, content, domain.CatalogSubject{}, true, domain.CatalogPersistenceBindings{AllowUnbound: true})
	if err != nil || len(states) == 0 {
		return result, false, err
	}
	return app.OrderSnapshot{AggregateRevision: aggregate, State: states[len(states)-1].order}, true, nil
}

func (r *catalogRepository) LookupReview(ctx context.Context, operation app.ReviewOperation) (result app.ReviewResult, found bool, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, false, err
	}
	defer func() {
		done(&err)
		if err != nil {
			result = app.ReviewResult{}
			found = false
		}
	}()
	command := operation.Command
	if command.Subject.ContentID().IsZero() || command.ID.IsZero() || operation.ExpectedAggregate.Uint64() == 0 {
		return result, false, app.ErrInvalidInput
	}
	bindings := domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domain.SourceRegistration{command.Source}, Reviewers: []domain.IssuerPrincipal{operation.Capability.Reviewer()}}
	states, _, err := r.readStates(call, command.Subject.ContentID(), command.Subject, false, bindings)
	if err != nil {
		return result, false, err
	}
	// Validate current authority even for a clean miss. The domain owns command
	// normalization and canonical replay; the adapter never invents an outcome.
	state, _ := domain.NewCatalogReviewState(command.Subject, false)
	if len(states) > 0 {
		state = states[len(states)-1].review
	}
	if err := r.stateSourceActive(call, command.Source); err != nil {
		return result, false, err
	}
	decision, found, err := state.LookupCommand(operation.Capability, command)
	if err != nil || !found {
		return result, false, err
	}
	for _, s := range states {
		for _, d := range s.decisions {
			if d.id == command.ID.String() {
				if d.aggregate != operation.ExpectedAggregate.Uint64()+1 {
					return result, false, domain.ErrCatalogCommandConflict
				}
				revision, _ := domain.NewCatalogRevision(d.aggregate)
				return app.ReviewResult{AggregateRevision: revision, Decision: decision}, true, nil
			}
		}
	}
	return result, false, app.ErrRepositoryUnavailable
}

func (r *catalogRepository) LookupOrder(ctx context.Context, operation app.OrderOperation) (result app.OrderResult, found bool, err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return result, false, err
	}
	defer func() {
		done(&err)
		if err != nil {
			result = app.OrderResult{}
			found = false
		}
	}()
	command := operation.Command
	if command.Content.IsZero() || command.ID.IsZero() || operation.ExpectedAggregate.Uint64() == 0 {
		return result, false, app.ErrInvalidInput
	}
	bindings := domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domain.SourceRegistration{command.Namespace.Source()}, Namespaces: []domain.CatalogOrderNamespace{command.Namespace}}
	states, _, err := r.readStates(call, command.Content, domain.CatalogSubject{}, true, bindings)
	if err != nil {
		return result, false, err
	}
	state, _ := domain.NewCatalogOrderState(command.Content, nil)
	if len(states) > 0 {
		state = states[len(states)-1].order
	}
	if err := r.stateSourceActive(call, command.Namespace.Source()); err != nil {
		return result, false, err
	}
	decision, found, err := state.LookupCommand(operation.Capability, command)
	if err != nil || !found {
		return result, false, err
	}
	for _, s := range states {
		if s.revision == decision.View.Revision.Uint64() {
			if s.aggregate != operation.ExpectedAggregate.Uint64()+1 {
				return result, false, domain.ErrCatalogCommandConflict
			}
			revision, _ := domain.NewCatalogRevision(s.aggregate)
			return app.OrderResult{AggregateRevision: revision, Decision: decision}, true, nil
		}
	}
	return result, false, app.ErrRepositoryUnavailable
}

// Caller and transaction deadlines share one cancellation fence. A swallowed
// error still fails the enclosing transaction, as it does for Query.
func (r *catalogRepository) stateCall(ctx context.Context) (context.Context, func(*error), error) {
	if ctx == nil {
		return nil, nil, app.ErrInvalidInput
	}
	if r.transaction == nil || r.owner.generation.Load() != r.generation {
		return nil, nil, app.ErrRepositoryUnavailable
	}
	if err := r.ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if r.schemaVersion != 6 {
		return nil, nil, app.ErrUnsupported
	}
	call, cancel := context.WithCancel(r.ctx)
	stop := context.AfterFunc(ctx, cancel)
	return call, func(err *error) {
		if *err == nil {
			*err = call.Err()
		}
		if *err != nil {
			r.failure = *err
		}
		stop()
		cancel()
	}, nil
}

func (r *catalogRepository) stateSourceActive(ctx context.Context, source domain.SourceRegistration) error {
	var revoked int
	err := r.transaction.QueryRowContext(ctx, "SELECT revoked FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?", source.ID().String(), source.Revision().String()).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // A core-authorized clean miss need not have imported evidence yet.
	if err != nil {
		return repositoryError(ctx, err)
	}
	if revoked != 0 || !source.Active() {
		return domain.ErrCatalogUnauthorized
	}
	return nil
}

type catalogStoredDecision struct {
	id              string
	command, result []byte
	aggregate       uint64
}
type catalogStoredState struct {
	aggregate, revision uint64
	raw                 []byte
	review              domain.CatalogReviewState
	order               domain.CatalogOrderState
	decisions           []catalogStoredDecision
}

func catalogSubjectKey(subject domain.CatalogSubject) string {
	if !subject.VersionID().IsZero() {
		return subject.VersionID().String()
	}
	return subject.ContentID().String()
}

// Counts and byte admission precede any blob materialization. History is read
// in full within these fixed bounds: a truncated tail cannot look like a miss.
func (r *catalogRepository) readStates(ctx context.Context, content identity.ContentID, subject domain.CatalogSubject, order bool, bindings domain.CatalogPersistenceBindings) ([]catalogStoredState, domain.CatalogRevision, error) {
	id, key := content.String(), catalogSubjectKey(subject)
	var corrupt bool
	if err := r.transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check LIMIT 1)").Scan(&corrupt); err != nil {
		return nil, domain.CatalogRevision{}, repositoryError(ctx, err)
	}
	if corrupt {
		return nil, domain.CatalogRevision{}, app.ErrRepositoryUnavailable
	}
	var aggregate, protected uint64
	err := r.transaction.QueryRowContext(ctx, "SELECT aggregate_revision,protected_custom FROM tc_catalog_managed WHERE content_id=?", id).Scan(&aggregate, &protected)
	managed := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, domain.CatalogRevision{}, repositoryError(ctx, err)
	}
	if managed && (aggregate == 0 || protected > 1) {
		return nil, domain.CatalogRevision{}, app.ErrRepositoryUnavailable
	}
	if !managed {
		aggregate = 1
	}
	revision, err := domain.NewCatalogRevision(aggregate)
	if err != nil {
		return nil, domain.CatalogRevision{}, app.ErrRepositoryUnavailable
	}
	// Every aggregate advance is backed by exactly one normalized review decision
	// or order envelope. The latter carries its original result and full history.
	var events, distinct, minimum, maximum uint64
	err = r.transaction.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT n),COALESCE(min(n),1),COALESCE(max(n),1) FROM (
 SELECT result_aggregate_revision n FROM tc_catalog_decisions WHERE content_id=?
 UNION ALL SELECT aggregate_revision FROM tc_catalog_order_states WHERE content_id=? AND order_revision>1)`, id, id).Scan(&events, &distinct, &minimum, &maximum)
	if err != nil {
		return nil, revision, repositoryError(ctx, err)
	}
	if events > domain.RetainedDecisions.Maximum() || events != distinct || maximum != aggregate || events != aggregate-1 || events > 0 && minimum != 2 {
		return nil, revision, app.ErrRepositoryUnavailable
	}
	table, column, wanted := "tc_catalog_review_states", "subject_id", key
	if order {
		table, column, wanted = "tc_catalog_order_states", "content_id", id
	}
	var count, size uint64
	err = r.transaction.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(length(state)),0) FROM "+table+" WHERE "+column+"=?", wanted).Scan(&count, &size)
	if err != nil {
		return nil, revision, repositoryError(ctx, err)
	}
	if count > domain.RetainedDecisions.Maximum()+1 || size > domain.RetainedBytes.Maximum() {
		return nil, revision, app.ErrRepositoryUnavailable
	}
	var expected uint64
	if !order {
		var storedContent string
		var version sql.NullString
		err = r.transaction.QueryRowContext(ctx, "SELECT content_id,version_id,review_revision FROM tc_catalog_subjects WHERE subject_id=?", key).Scan(&storedContent, &version, &expected)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, revision, repositoryError(ctx, err)
		}
		if err == nil && (storedContent != id || version.String != subject.VersionID().String() || version.Valid == subject.VersionID().IsZero() || expected == 0) {
			return nil, revision, app.ErrRepositoryUnavailable
		}
		var observations, decisions uint64
		if err := r.transaction.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM tc_catalog_observations WHERE subject_id=?),(SELECT count(*) FROM tc_catalog_decisions WHERE subject_id=?)", key, key).Scan(&observations, &decisions); err != nil {
			return nil, revision, repositoryError(ctx, err)
		}
		if observations > domain.RetainedObservations.Maximum() || decisions > domain.RetainedDecisions.Maximum() || expected != 0 && expected != decisions+1 || count == 0 && (observations != 0 || decisions != 0 || expected > 1) {
			return nil, revision, app.ErrRepositoryUnavailable
		}
	}
	if count == 0 {
		return nil, revision, nil
	}
	if !managed {
		return nil, revision, app.ErrRepositoryUnavailable
	}
	versions, err := r.catalogVersions(ctx, content)
	if err != nil {
		return nil, revision, app.ErrRepositoryUnavailable
	}
	if !order && !subject.VersionID().IsZero() {
		found := false
		for _, v := range versions {
			if v.Version.ID() == subject.VersionID() {
				found = true
			}
		}
		if !found {
			return nil, revision, app.ErrRepositoryUnavailable
		}
	}
	revColumn := "review_revision"
	if order {
		revColumn = "order_revision"
	}
	rows, err := r.transaction.QueryContext(ctx, "SELECT aggregate_revision,"+revColumn+",encoding,state,state_bytes,state_sha256,content_id FROM "+table+" WHERE "+column+"=? ORDER BY "+revColumn+" LIMIT 1026", wanted)
	if err != nil {
		return nil, revision, repositoryError(ctx, err)
	}
	var states []catalogStoredState
	for rows.Next() {
		var s catalogStoredState
		var encoding, declared uint64
		var digest []byte
		var owner string
		if err := rows.Scan(&s.aggregate, &s.revision, &encoding, &s.raw, &declared, &digest, &owner); err != nil {
			rows.Close()
			return nil, revision, repositoryError(ctx, err)
		}
		sum := sha256.Sum256(s.raw)
		if encoding != 1 || declared != uint64(len(s.raw)) || declared > domain.RetainedBytes.Maximum() || !bytes.Equal(digest, sum[:]) || owner != id || s.revision == 0 || s.revision > domain.RetainedDecisions.Maximum()+1 || order && s.revision != uint64(len(states))+1 || s.aggregate == 0 || s.aggregate > aggregate || len(states) > 0 && (s.aggregate <= states[len(states)-1].aggregate || s.revision <= states[len(states)-1].revision) {
			rows.Close()
			return nil, revision, app.ErrRepositoryUnavailable
		}
		states = append(states, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, revision, repositoryError(ctx, err)
	}
	if uint64(len(states)) != count || !order && states[len(states)-1].revision != expected {
		return nil, revision, app.ErrRepositoryUnavailable
	}
	for i := range states {
		if err := ctx.Err(); err != nil {
			return nil, revision, err
		}
		s := &states[i]
		projection := catalogStateReader{raw: s.raw, repository: r, ctx: ctx, content: id, subject: key, order: order, sources: make(map[string]domain.SourceRegistration)}
		if err := projection.check(s, versions, i == len(states)-1); err != nil {
			return nil, revision, err
		}
		// Exact normalized source bindings are reconstructed as permanently
		// inactive provenance. Only explicit caller-supplied core handles retain
		// live authority during Lookup; SQL and state bytes cannot create it.
		resolved := bindings
		resolved.Sources = append([]domain.SourceRegistration(nil), bindings.Sources...)
		for key, source := range projection.sources {
			provided := false
			for _, live := range bindings.Sources {
				if live.ID().String()+live.Revision().String() == key {
					provided = true
				}
			}
			if !provided {
				resolved.Sources = append(resolved.Sources, source)
			}
		}
		if order {
			s.order, err = domain.RehydrateCatalogOrderState(s.raw, resolved)
		} else {
			s.review, err = domain.RehydrateCatalogReviewState(s.raw, resolved)
		}
		if err != nil {
			return nil, revision, app.ErrRepositoryUnavailable
		}
		if !order && (s.review.Subject() != subject || s.review.Revision().Uint64() != s.revision || i == len(states)-1 && s.review.ProtectedCustom() != (protected == 1)) || order && s.order.Revision().Uint64() != s.revision {
			return nil, revision, app.ErrRepositoryUnavailable
		}
		if i > 0 {
			previous := states[i-1].decisions
			if len(s.decisions) <= len(previous) {
				return nil, revision, app.ErrRepositoryUnavailable
			}
			for j, d := range previous {
				if d.id != s.decisions[j].id || !bytes.Equal(d.command, s.decisions[j].command) || !bytes.Equal(d.result, s.decisions[j].result) {
					return nil, revision, app.ErrRepositoryUnavailable
				}
			}
		}
	}
	return states, revision, nil
}

// This reader projects bounded bytes onto SQL relations before the A1.3c
// semantic decoder replays them. It never deserializes authority. Every
// length/count is independently fenced so format drift also fails closed.
type catalogStateReader struct {
	raw              []byte
	offset           int
	err              error
	repository       *catalogRepository
	ctx              context.Context
	content, subject string
	order            bool
	sources          map[string]domain.SourceRegistration
}

func (d *catalogStateReader) take(n uint64) []byte {
	if d.err != nil {
		return nil
	}
	if n > uint64(len(d.raw)-d.offset) {
		d.err = app.ErrRepositoryUnavailable
		return nil
	}
	b := d.raw[d.offset : d.offset+int(n)]
	d.offset += int(n)
	return b
}
func (d *catalogStateReader) number() uint64 {
	b := d.take(8)
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
func (d *catalogStateReader) text() string {
	n := d.number()
	if n > domain.RetainedBytes.Maximum() {
		d.err = app.ErrRepositoryUnavailable
		return ""
	}
	return string(d.take(n))
}
func (d *catalogStateReader) count(max uint64) int {
	n := d.number()
	if n > max {
		d.err = app.ErrRepositoryUnavailable
		return 0
	}
	return int(n)
}
func (d *catalogStateReader) match(query string, args ...any) {
	if d.err != nil || d.repository == nil {
		return
	}
	var valid bool
	if err := d.repository.transaction.QueryRowContext(d.ctx, query, args...).Scan(&valid); err != nil {
		d.err = repositoryError(d.ctx, err)
	} else if !valid {
		d.err = app.ErrRepositoryUnavailable
	}
}

type catalogStoredSource struct{ id, revision string }

func (d *catalogStateReader) source() catalogStoredSource {
	start := d.offset
	s := catalogStoredSource{id: d.text(), revision: d.text()}
	issuer, adapter, version, scope := d.text(), d.text(), d.text(), d.text()
	acquisition := d.number()
	n := d.count(5)
	claims := make([]uint64, n)
	for i := range claims {
		claims[i] = d.number()
	}
	d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?
 AND issuer_principal=? AND adapter=? AND adapter_version=? AND scope=? AND acquisition=? AND revoked IN (0,1))`, s.id, s.revision, issuer, adapter, version, scope, acquisition)
	d.match("SELECT count(*)=? FROM tc_catalog_source_claims WHERE source_id=? AND registration_revision=?", n, s.id, s.revision)
	for _, key := range claims {
		d.match("SELECT EXISTS(SELECT 1 FROM tc_catalog_source_claims WHERE source_id=? AND registration_revision=? AND claim_key=? AND acquisition=?)", s.id, s.revision, key, acquisition)
	}
	if d.err == nil && d.sources != nil {
		key := s.id + s.revision
		if _, exists := d.sources[key]; !exists {
			if uint64(len(d.sources)) >= domain.SourceRegistrations.Maximum() {
				d.err = app.ErrRepositoryUnavailable
				return s
			}
			source, err := domain.RehydrateSourceRegistration(d.raw[start:d.offset], domain.CatalogPersistenceBindings{AllowUnbound: true})
			if err != nil {
				d.err = app.ErrRepositoryUnavailable
			} else {
				d.sources[key] = source
			}
		}
	}
	return s
}
func (d *catalogStateReader) namespace() catalogStoredSource {
	d.take(16)
	s := d.source()
	d.text()
	return s
}
func (d *catalogStateReader) coordinate(source catalogStoredSource, binaryBatch bool) (string, uint64) {
	batch := ""
	if binaryBatch {
		batch = hex.EncodeToString(d.take(16))
	} else {
		batch = d.text()
	}
	digest := d.take(32)
	size, count := d.number(), d.number()
	parser, revision := d.text(), d.text()
	index, locator, snapshot := d.number(), d.text(), []byte(d.text())
	d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_batches b JOIN tc_catalog_records r USING(batch_id)
 WHERE b.batch_id=? AND b.source_id=? AND b.registration_revision=? AND b.artifact_sha256=? AND b.byte_length=? AND b.record_count=?
 AND b.parser_revision=? AND b.source_revision=? AND r.record_index=? AND r.locator=? AND r.snapshot=?)`, batch, source.id, source.revision, digest, size, count, parser, revision, index, locator, snapshot)
	return batch, index
}
func (d *catalogStateReader) claim() (uint64, any, any, any, any) {
	key, text, audio, hash, owner := d.number(), d.text(), d.number(), d.text(), d.text()
	var textValue, audioValue, hashValue, ownerValue any
	switch domain.ClaimKey(key) {
	case domain.TitleClaimKey, domain.ModelClaimKey, domain.ArticleClaimKey:
		textValue = text
	case domain.FingerprintClaimKey:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], audio)
		audioValue = b[:]
		h, err := hex.DecodeString(hash)
		if err != nil {
			d.err = app.ErrRepositoryUnavailable
		}
		hashValue = h
	case domain.OwnershipClaimKey:
		ownerValue = owner
	default:
		d.err = app.ErrRepositoryUnavailable
	}
	return key, textValue, audioValue, hashValue, ownerValue
}
func (d *catalogStateReader) observation() {
	id, content, version := d.text(), d.text(), d.text()
	key, text, audio, hash, owner := d.claim()
	source := d.source()
	batch, index := d.coordinate(source, false)
	origin, observed, confidence := d.text(), d.text(), d.number()
	subject := content
	if version != "" {
		subject = version
	}
	d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_observations WHERE observation_id=? AND subject_id=? AND content_id=?
 AND claim_key=? AND text_value IS ? AND audio_id IS ? AND audio_sha1 IS ? AND owner_content_id IS ?
 AND source_id=? AND registration_revision=? AND batch_id=? AND record_index=? AND asserted_origin=? AND observed_at=? AND confidence=?)`, id, subject, content, key, text, audio, hash, owner, source.id, source.revision, batch, index, origin, observed, confidence)
}
func (d *catalogStateReader) sequence() {
	source := d.namespace()
	d.text()
	content, version := d.text(), d.text()
	d.number()
	if content != d.content {
		d.err = app.ErrRepositoryUnavailable
	}
	d.match("SELECT EXISTS(SELECT 1 FROM tc_content_versions WHERE content_id=? AND version_id=?)", content, version)
	d.coordinate(source, true)
}
func (d *catalogStateReader) entry() {
	d.text()
	d.number()
	for n := d.count(domain.RetainedSupportLinks.Maximum()); n > 0; n-- {
		d.text()
	}
}
func (d *catalogStateReader) reviewResult() {
	d.text()
	d.number()
	d.number()
	d.number()
	for n := d.count(domain.RetainedObservations.Maximum()); n > 0; n-- {
		d.claim()
		for m := d.count(domain.RetainedSupportLinks.Maximum()); m > 0; m-- {
			d.text()
		}
	}
}
func (d *catalogStateReader) orderResult() {
	d.text()
	d.number()
	d.number()
	for n := d.count(domain.RetainedVersions.Maximum()); n > 0; n-- {
		d.namespace()
		d.entry()
	}
}
func (d *catalogStateReader) reviewDecision() catalogStoredDecision {
	start := d.offset
	if d.text() != "pi08-command/1" || d.text() != domain.CatalogDecisionPolicy {
		d.err = app.ErrRepositoryUnavailable
	}
	id, kind, content, version := d.text(), d.number(), d.text(), d.text()
	source := d.source()
	reviewer, expected, key, reason := d.text(), d.number(), d.number(), d.text()
	for n := d.count(domain.MutationObservations.Maximum()); n > 0; n-- {
		d.observation()
	}
	n := d.count(domain.SelectedSupport.Maximum())
	support := make([]string, n)
	for i := range support {
		support[i] = d.text()
	}
	command := d.raw[start:d.offset]
	canonical := []byte(d.text())
	if !bytes.Equal(command, canonical) {
		d.err = app.ErrRepositoryUnavailable
	}
	start = d.offset
	d.reviewResult()
	result := d.raw[start:d.offset]
	subject := content
	if version != "" {
		subject = version
	}
	var aggregate uint64
	if d.err == nil && d.repository != nil {
		if err := d.repository.transaction.QueryRowContext(d.ctx, "SELECT result_aggregate_revision FROM tc_catalog_decisions WHERE command_id=?", id).Scan(&aggregate); err != nil {
			d.err = app.ErrRepositoryUnavailable
		}
	}
	d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_decisions WHERE command_id=? AND subject_id=? AND content_id=? AND source_id=?
 AND registration_revision=? AND reviewer_principal=? AND policy_revision=? AND kind=? AND claim_key=? AND expected_review_revision=?
 AND result_review_revision=? AND reason=? AND canonical_command=? AND canonical_result=? AND result_aggregate_revision=expected_aggregate_revision+1)`, id, subject, content, source.id, source.revision, reviewer, domain.CatalogDecisionPolicy, kind, key, expected, expected+1, reason, canonical, result)
	d.match("SELECT count(*)=? FROM tc_catalog_decision_support WHERE command_id=?", len(support), id)
	for _, observation := range support {
		d.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_decision_support WHERE command_id=? AND observation_id=? AND subject_id=? AND source_id=? AND registration_revision=? AND claim_key=?)`, id, observation, subject, source.id, source.revision, key)
	}
	return catalogStoredDecision{id: id, command: canonical, result: result, aggregate: aggregate}
}
func (d *catalogStateReader) orderDecision() catalogStoredDecision {
	start := d.offset
	if d.text() != "pi08-order-command/1" || d.text() != domain.CatalogDecisionPolicy {
		d.err = app.ErrRepositoryUnavailable
	}
	id, content := d.text(), d.text()
	d.namespace()
	d.text()
	d.number()
	d.text()
	if content != d.content {
		d.err = app.ErrRepositoryUnavailable
	}
	for n := d.count(domain.RetainedVersions.Maximum()); n > 0; n-- {
		d.entry()
	}
	for n := d.count(domain.MutationObservations.Maximum()); n > 0; n-- {
		d.sequence()
	}
	for n := d.count(domain.SelectedSupport.Maximum()); n > 0; n-- {
		d.text()
	}
	command := d.raw[start:d.offset]
	canonical := []byte(d.text())
	if !bytes.Equal(command, canonical) {
		d.err = app.ErrRepositoryUnavailable
	}
	start = d.offset
	d.orderResult()
	return catalogStoredDecision{id: id, command: canonical, result: d.raw[start:d.offset]}
}
func (d *catalogStateReader) check(s *catalogStoredState, versions []catalogVersionRow, latest bool) error {
	d.text()
	content := d.text()
	if content != d.content {
		d.err = app.ErrRepositoryUnavailable
	}
	if !d.order {
		d.text()
	}
	if d.number() != s.revision {
		d.err = app.ErrRepositoryUnavailable
	}
	if !d.order {
		d.number()
	}
	d.number() // Retained link count is validated by deterministic domain replay.
	if d.order {
		n := d.count(domain.RetainedVersions.Maximum())
		if latest && n != len(versions) {
			d.err = app.ErrRepositoryUnavailable
		}
		seen := make(map[string]bool, n)
		for i := 0; i < n; i++ {
			id, owner, audio, hash, namespace, position, known := d.text(), d.text(), d.number(), d.text(), d.text(), d.number(), d.number()
			found := false
			for _, v := range versions {
				if v.Version.ID().String() == id {
					order := v.Version.OrderEvidence()
					found = owner == d.content && audio == v.Version.Fingerprint().AudioID().Uint64() && hash == v.Version.Fingerprint().Hash().String() && namespace == order.Namespace() && position == order.Position() && (known == 1) == order.Known()
				}
			}
			if !found || seen[id] {
				d.err = app.ErrRepositoryUnavailable
			}
			seen[id] = true
			d.match("SELECT EXISTS(SELECT 1 FROM tc_catalog_order_state_versions WHERE content_id=? AND order_revision=? AND version_id=?)", d.content, s.revision, id)
		}
		d.match("SELECT count(*)=? FROM tc_catalog_order_state_versions WHERE content_id=? AND order_revision=?", n, d.content, s.revision)
	}
	n := d.count(domain.RetainedObservations.Maximum())
	for i := 0; i < n; i++ {
		if d.order {
			d.sequence()
		} else {
			d.observation()
		}
	}
	if latest && !d.order {
		d.match("SELECT count(*)=? FROM tc_catalog_observations WHERE subject_id=?", n, d.subject)
	}
	n = d.count(domain.RetainedDecisions.Maximum())
	for i := 0; i < n; i++ {
		if d.order {
			s.decisions = append(s.decisions, d.orderDecision())
		} else {
			s.decisions = append(s.decisions, d.reviewDecision())
		}
	}
	if !d.order {
		if latest {
			d.match("SELECT count(*)=? FROM tc_catalog_decisions WHERE subject_id=?", n, d.subject)
		}
		if n > 0 && s.decisions[n-1].aggregate != s.aggregate {
			d.err = app.ErrRepositoryUnavailable
		}
	}
	if d.offset != len(d.raw) {
		d.err = app.ErrRepositoryUnavailable
	}
	return d.err
}

func (r *catalogRepository) CommitReview(context.Context, app.ReviewOperation, domain.CatalogReviewState, app.ReviewResult) error {
	return r.unsupported()
}

func (r *catalogRepository) CommitOrder(context.Context, app.OrderOperation, domain.CatalogOrderState, app.OrderResult) error {
	return r.unsupported()
}

func (r *catalogRepository) unsupported() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.transaction == nil {
		return app.ErrRepositoryUnavailable
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	return app.ErrUnsupported
}
