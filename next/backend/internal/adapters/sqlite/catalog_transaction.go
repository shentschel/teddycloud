package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var _ app.CatalogTransactor = (*LifecycleOwner)(nil)
var _ app.CatalogRepository = (*catalogRepository)(nil)

// WithinCatalogTransaction retains the restore fence and starts query-only.
// Only a validated v6 catalog commit enables writes on the leased connection.
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
	return owner.catalogTransaction(ctx, operation)
}

func (owner *LifecycleOwner) catalogTransaction(ctx context.Context, operation func(app.CatalogRepository) error) (result error) {
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
	commitAttempted := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		setting := "PRAGMA query_only=OFF"
		if previous != 0 {
			setting = "PRAGMA query_only=ON"
		}
		if _, err := connection.ExecContext(cleanup, setting); err != nil {
			failure := app.ErrRepositoryUnavailable
			if commitAttempted {
				// A commit may already be durable when connection cleanup fails.
				// Read-only and rolled-back operations have no uncertain commit.
				failure = app.ErrIndeterminate
			}
			result = errors.Join(result, failure)
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
	if (version < 5 || version > 9) || dirty != 0 {
		return app.ErrUnsupported
	}
	repository := &catalogRepository{transaction: tx, ctx: ctx, owner: owner, database: owner.database, generation: owner.generation.Load(), schemaVersion: version, statements: make(map[string]*sql.Stmt)}
	defer repository.closeStatements()
	defer repository.revoke()
	result = operation(repository)
	repository.revoke()
	if result == nil {
		result = ctx.Err()
	}
	if result == nil {
		result = repository.failure
	}
	if result == nil && repository.wrote {
		for _, authorize := range repository.authorize {
			if result = authorize(); result != nil {
				break
			}
		}
		if result == nil {
			result = ctx.Err()
		}
		if result == nil {
			commitAttempted = true
			if err := tx.Commit(); err != nil {
				// The driver may have committed before losing its response. Do not
				// retry the callback or expose the raw cause (including cancellation).
				return app.ErrIndeterminate
			}
			return nil
		}
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
	wrote         bool
	authorize     []func() error
	statements    map[string]*sql.Stmt
}

func (r *catalogRepository) statement(ctx context.Context, query string) (*sql.Stmt, error) {
	if statement := r.statements[query]; statement != nil {
		return statement, nil
	}
	statement, err := r.transaction.PrepareContext(ctx, query)
	if err != nil {
		return nil, repositoryError(ctx, err)
	}
	r.statements[query] = statement
	return statement, nil
}

func (r *catalogRepository) closeStatements() {
	for _, statement := range r.statements {
		_ = statement.Close()
	}
	r.statements = nil
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
	if err == nil && !found {
		err = r.registrationKeyOnly(call, command.ID.String())
	}
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
	if err == nil && !found {
		err = r.registrationKeyOnly(call, command.ID.String())
	}
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
	if r.schemaVersion < 6 {
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
	encoding            uint64
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
	return r.readStatesWithGraph(ctx, content, subject, order, bindings, true)
}

// A caller that already reconstructed the graph from this unchanged SQLite
// snapshot may still validate another complete state envelope without doing
// the identical graph reconstruction twice.
func (r *catalogRepository) readStatesWithGraph(ctx context.Context, content identity.ContentID, subject domain.CatalogSubject, order bool, bindings domain.CatalogPersistenceBindings, graph bool) ([]catalogStoredState, domain.CatalogRevision, error) {
	states, revision, err := r.readStatesRaw(ctx, content, subject, order, bindings)
	if err == nil && graph && r.schemaVersion == 9 {
		_, err = r.readSupersession(ctx, content, bindings)
	}
	return states, revision, err
}

// Raw envelope validation never calls the graph reader. The graph reader uses
// these immutable snapshots to validate historical fences without recursion.
func (r *catalogRepository) readStatesRaw(ctx context.Context, content identity.ContentID, subject domain.CatalogSubject, order bool, bindings domain.CatalogPersistenceBindings) ([]catalogStoredState, domain.CatalogRevision, error) {
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
	registeredAggregate := false
	if managed && r.schemaVersion >= 8 {
		var expectedProtection bool
		err = r.transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE content_id=?1),
 EXISTS(SELECT 1 FROM tc_catalog_registrations WHERE content_id=?1 AND protected_custom=1) OR EXISTS(SELECT 1 FROM tc_catalog_observations o JOIN tc_catalog_sources s USING(source_id,registration_revision) WHERE o.content_id=?1 AND s.acquisition=2)`, id).Scan(&registeredAggregate, &expectedProtection)
		if err != nil {
			return nil, domain.CatalogRevision{}, repositoryError(ctx, err)
		}
		if registeredAggregate && expectedProtection != (protected == 1) {
			return nil, domain.CatalogRevision{}, app.ErrRepositoryUnavailable
		}
	}
	revision, err := domain.NewCatalogRevision(aggregate)
	if err != nil {
		return nil, domain.CatalogRevision{}, app.ErrRepositoryUnavailable
	}
	// Every aggregate advance is backed by exactly one normalized review decision
	// or order envelope. The latter carries its original result and full history.
	var events, distinct, minimum, maximum uint64
	eventSQL := `SELECT count(*),count(DISTINCT n),COALESCE(min(n),1),COALESCE(max(n),1) FROM (
 SELECT result_aggregate_revision n FROM tc_catalog_decisions WHERE content_id=?
 UNION ALL SELECT aggregate_revision FROM tc_catalog_order_states WHERE content_id=? AND order_revision>1`
	args := []any{id, id}
	if r.schemaVersion >= 8 {
		eventSQL += ` UNION ALL SELECT aggregate_revision FROM tc_catalog_order_states_v2 WHERE content_id=? AND order_revision>1`
		args = append(args, id)
	}
	if r.schemaVersion == 9 {
		eventSQL += ` UNION ALL SELECT result_aggregate FROM tc_catalog_supersessions WHERE content_id=?`
		args = append(args, id)
	}
	err = r.transaction.QueryRowContext(ctx, eventSQL+")", args...).Scan(&events, &distinct, &minimum, &maximum)
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
	if r.schemaVersion >= 8 {
		table = "(SELECT * FROM " + table + " UNION ALL SELECT "
		if order {
			table += "* FROM tc_catalog_order_states_v2)"
		} else {
			table += "subject_id,content_id,aggregate_revision,review_revision,encoding,state,state_bytes,state_sha256 FROM tc_catalog_review_states_v2)"
		}
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
		if order && registeredAggregate {
			return nil, revision, app.ErrRepositoryUnavailable
		}
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
		s.encoding = encoding
		if (encoding != 1 && encoding != 2) || declared != uint64(len(s.raw)) || declared > domain.RetainedBytes.Maximum() || !bytes.Equal(digest, sum[:]) || owner != id || s.revision == 0 || s.revision > domain.RetainedDecisions.Maximum()+1 || (order || encoding == 2) && s.revision != uint64(len(states))+1 || s.aggregate == 0 || s.aggregate > aggregate || len(states) > 0 && (s.aggregate <= states[len(states)-1].aggregate || s.revision <= states[len(states)-1].revision || encoding < states[len(states)-1].encoding) {
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
		if err := projection.checkEnvelope(s, versions, i == len(states)-1); err != nil {
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
			s.order, err = rehydrateRegistrationOrder(s.raw, resolved)
		} else {
			s.review, err = domain.RehydrateCatalogReviewState(s.raw, resolved)
		}
		if err != nil {
			return nil, revision, app.ErrRepositoryUnavailable
		}
		if !order && (s.review.Subject() != subject || s.review.Revision().Uint64() != s.revision || i == len(states)-1 && (s.review.ProtectedCustom() && protected != 1 || !registeredAggregate && s.review.ProtectedCustom() != (protected == 1))) || order && s.order.Revision().Uint64() != s.revision {
			return nil, revision, app.ErrRepositoryUnavailable
		}
		if !order && s.encoding == 2 && s.revision == 1 && !subject.VersionID().IsZero() {
			field := s.review.Field(domain.FingerprintClaimKey)
			matched := false
			for _, v := range versions {
				if v.Version.ID() == subject.VersionID() && len(field.Alternatives) == 1 && field.Alternatives[0].Claim.Fingerprint() == v.Version.Fingerprint() {
					matched = true
				}
			}
			if !matched {
				return nil, revision, app.ErrRepositoryUnavailable
			}
		}
		if !order && s.encoding == 2 && s.revision == 1 && subject.VersionID().IsZero() {
			var values [3]sql.NullString
			if err := r.transaction.QueryRowContext(ctx, "SELECT title,model_number,article_number FROM tc_catalog_content WHERE content_id=?", id).Scan(&values[0], &values[1], &values[2]); err != nil {
				return nil, revision, app.ErrRepositoryUnavailable
			}
			for i, value := range values {
				field := s.review.Field(domain.ClaimKey(i + 1))
				if value.Valid != (len(field.Alternatives) == 1) || value.Valid && value.String != field.Alternatives[0].Claim.Text() {
					return nil, revision, app.ErrRepositoryUnavailable
				}
			}
		}
		if i > 0 {
			if order {
				if err := registrationOrderTransition(states[i-1], *s); err != nil {
					return nil, revision, err
				}
			}
			previous := states[i-1].decisions
			if len(s.decisions) < len(previous) || len(s.decisions) == len(previous) && !(order && s.encoding == 2) {
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
	raw               []byte
	offset            int
	err               error
	repository        *catalogRepository
	ctx               context.Context
	content, subject  string
	order             bool
	sources           map[string]domain.SourceRegistration
	write             bool
	writeCommand      string
	expectedAggregate uint64
	skipManifest      bool
	manifestTable     string
	matches           map[string]bool
}

func (d *catalogStateReader) insert(query string, args ...any) {
	if !d.write || d.err != nil {
		return
	}
	if _, err := d.repository.transaction.ExecContext(d.ctx, query, args...); err != nil {
		d.err = repositoryError(d.ctx, err)
	}
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
	key := ""
	if d.matches != nil {
		raw := registrationFrame(nil, []byte(query))
		for _, arg := range args {
			raw = registrationFrame(raw, []byte(fmt.Sprintf("%T:%#v", arg, arg)))
		}
		key = string(raw)
		if d.matches[key] {
			return
		}
	}
	statement, err := d.repository.statement(d.ctx, query)
	if err != nil {
		d.err = err
		return
	}
	var valid bool
	if err := statement.QueryRowContext(d.ctx, args...).Scan(&valid); err != nil {
		d.err = repositoryError(d.ctx, err)
	} else if !valid {
		d.err = app.ErrRepositoryUnavailable
	} else if d.matches != nil {
		d.matches[key] = true
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
	if d.write && d.err == nil {
		var exists bool
		if err := d.repository.transaction.QueryRowContext(d.ctx, "SELECT EXISTS(SELECT 1 FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?)", s.id, s.revision).Scan(&exists); err != nil {
			d.err = repositoryError(d.ctx, err)
		}
		if !exists {
			d.insert("INSERT INTO tc_catalog_sources VALUES (?,?,?,?,?,?,?,0)", s.id, s.revision, issuer, adapter, version, scope, acquisition)
			for _, key := range claims {
				d.insert("INSERT INTO tc_catalog_source_claims VALUES (?,?,?,?)", s.id, s.revision, acquisition, key)
			}
		}
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
	if d.write && d.err == nil {
		if err := d.repository.catalogBatchKeyGuard(d.ctx, batch); err != nil {
			d.err = err
		}
	}
	d.insert("INSERT INTO tc_catalog_batches VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(batch_id) DO NOTHING", batch, source.id, source.revision, digest, size, count, parser, revision)
	d.insert("INSERT INTO tc_catalog_records VALUES (?,?,?,?) ON CONFLICT(batch_id,record_index) DO NOTHING", batch, index, locator, snapshot)
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
	d.insert("INSERT INTO tc_catalog_observations VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(observation_id) DO NOTHING", id, subject, content, key, text, audio, hash, owner, batch, index, source.id, source.revision, origin, observed, confidence)
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
	if id == d.writeCommand {
		d.insert("INSERT INTO tc_catalog_decisions VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", id, subject, content, source.id, source.revision, reviewer, domain.CatalogDecisionPolicy, kind, key, d.expectedAggregate, d.expectedAggregate+1, expected, expected+1, reason, canonical, result)
		for _, observation := range support {
			d.insert("INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)", subject, source.id, source.revision, key, id, observation)
		}
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
			if !d.skipManifest {
				table := d.manifestTable
				if table == "" {
					table = "tc_catalog_order_state_versions"
				}
				d.insert("INSERT INTO "+table+" VALUES (?,?,?)", d.content, s.revision, id)
				d.match("SELECT EXISTS(SELECT 1 FROM "+table+" WHERE content_id=? AND order_revision=? AND version_id=?)", d.content, s.revision, id)
			}
		}
		if !d.skipManifest {
			table := d.manifestTable
			if table == "" {
				table = "tc_catalog_order_state_versions"
			}
			d.match("SELECT count(*)=? FROM "+table+" WHERE content_id=? AND order_revision=?", n, d.content, s.revision)
		}
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

func (r *catalogRepository) CommitReview(ctx context.Context, operation app.ReviewOperation, next domain.CatalogReviewState, result app.ReviewResult) (err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	defer func() {
		if err != nil {
			r.failure = err
		}
	}()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return err
	}
	defer done(&err)
	command := operation.Command
	if command.ID.IsZero() || command.Subject.ContentID().IsZero() || operation.ExpectedAggregate.Uint64() == 0 {
		return app.ErrInvalidInput
	}
	bindings := domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domain.SourceRegistration{command.Source}, Reviewers: []domain.IssuerPrincipal{operation.Capability.Reviewer()}}
	states, aggregate, err := r.readStates(call, command.Subject.ContentID(), command.Subject, false, bindings)
	if err != nil {
		return err
	}
	if err = r.stateSourceActive(call, command.Source); err != nil {
		return err
	}
	if len(states) == 0 {
		return app.ErrSubjectNotFound
	}
	current := states[len(states)-1].review
	decision, replay, err := current.LookupCommand(operation.Capability, command)
	if err != nil {
		return err
	}
	raw, err := next.PersistenceEncoding()
	if err != nil {
		return err
	}
	if replay {
		for _, s := range states {
			if s.revision == decision.Revision.Uint64() {
				if s.aggregate != operation.ExpectedAggregate.Uint64()+1 {
					return domain.ErrCatalogCommandConflict
				}
				if result.AggregateRevision.Uint64() != s.aggregate || !reflect.DeepEqual(result.Decision, decision) || !bytes.Equal(raw, s.raw) {
					return app.ErrInvalidInput
				}
				return nil
			}
		}
		return app.ErrRepositoryUnavailable
	}
	if err = r.registrationKeyOnly(call, command.ID.String()); err != nil {
		return err
	}
	if aggregate != operation.ExpectedAggregate {
		return domain.ErrCatalogRevisionConflict
	}
	expected, decision, err := current.Apply(operation.Capability, command)
	if err != nil {
		return err
	}
	expectedRaw, err := expected.PersistenceEncoding()
	if err != nil {
		return err
	}
	if result.AggregateRevision.Uint64() != aggregate.Uint64()+1 || !reflect.DeepEqual(result.Decision, decision) || !bytes.Equal(raw, expectedRaw) {
		return app.ErrInvalidInput
	}
	// A command key is globally unique, including across review subjects.
	var exists bool
	if err = r.transaction.QueryRowContext(call, "SELECT EXISTS(SELECT 1 FROM tc_catalog_decisions WHERE command_id=?)", command.ID.String()).Scan(&exists); err != nil {
		return repositoryError(call, err)
	}
	if exists {
		return domain.ErrCatalogCommandConflict
	}
	if _, err = r.transaction.ExecContext(call, "PRAGMA query_only=OFF"); err != nil {
		return repositoryError(call, err)
	}
	// Restore query-only before returning to callback code, even on failure.
	defer func() {
		if _, resetErr := r.transaction.ExecContext(r.ctx, "PRAGMA query_only=ON"); resetErr != nil && err == nil {
			err = repositoryError(r.ctx, resetErr)
		}
	}()
	updated, err := r.transaction.ExecContext(call, "UPDATE tc_catalog_managed SET aggregate_revision=?,protected_custom=max(protected_custom,?) WHERE content_id=? AND aggregate_revision=?", result.AggregateRevision.Uint64(), next.ProtectedCustom(), command.Subject.ContentID().String(), aggregate.Uint64())
	if err != nil {
		return repositoryError(call, err)
	}
	if count, e := updated.RowsAffected(); e != nil || count != 1 {
		return app.ErrRepositoryUnavailable
	}
	updated, err = r.transaction.ExecContext(call, "UPDATE tc_catalog_subjects SET review_revision=? WHERE subject_id=? AND review_revision=?", next.Revision().Uint64(), catalogSubjectKey(command.Subject), command.Expected.Uint64())
	if err != nil {
		return repositoryError(call, err)
	}
	if count, e := updated.RowsAffected(); e != nil || count != 1 {
		return domain.ErrCatalogRevisionConflict
	}
	stored := catalogStoredState{raw: raw, aggregate: result.AggregateRevision.Uint64(), revision: next.Revision().Uint64()}
	var bootstrap string
	if r.schemaVersion >= 8 {
		e := r.transaction.QueryRowContext(call, "SELECT command_id FROM tc_catalog_registrations WHERE subject_id=?", catalogSubjectKey(command.Subject)).Scan(&bootstrap)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return repositoryError(call, e)
		}
	}
	if bootstrap != "" {
		stored.encoding = 2
	} else {
		stored.encoding = 1
	}
	projection := catalogStateReader{raw: raw, repository: r, ctx: call, content: command.Subject.ContentID().String(), subject: catalogSubjectKey(command.Subject), write: true, writeCommand: command.ID.String(), expectedAggregate: aggregate.Uint64()}
	if err = projection.checkEnvelope(&stored, nil, true); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if bootstrap != "" {
		_, err = r.transaction.ExecContext(call, "INSERT INTO tc_catalog_review_states_v2 VALUES (?,?,?,?,2,?,?,?,?)", catalogSubjectKey(command.Subject), command.Subject.ContentID().String(), result.AggregateRevision.Uint64(), next.Revision().Uint64(), raw, len(raw), sum[:], bootstrap)
	} else {
		_, err = r.transaction.ExecContext(call, "INSERT INTO tc_catalog_review_states VALUES (?,?,?,?,1,?,?,?)", catalogSubjectKey(command.Subject), command.Subject.ContentID().String(), result.AggregateRevision.Uint64(), next.Revision().Uint64(), raw, len(raw), sum[:])
	}
	if err != nil {
		return repositoryError(call, err)
	}
	// Read back through exactly the existing corruption/binding checks.
	if _, _, err = r.readStates(call, command.Subject.ContentID(), command.Subject, false, bindings); err != nil {
		return err
	}
	if err = r.registrationAccounting(call, command.Subject.ContentID()); err != nil {
		return err
	}
	r.wrote = true
	tx := r.transaction
	r.authorize = append(r.authorize, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, _, err := current.LookupCommand(operation.Capability, command); err != nil {
			return err
		}
		var revoked int
		if err := tx.QueryRowContext(r.ctx, "SELECT revoked FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?", command.Source.ID().String(), command.Source.Revision().String()).Scan(&revoked); err != nil {
			return repositoryError(r.ctx, err)
		}
		if revoked != 0 {
			return domain.ErrCatalogUnauthorized
		}
		return ctx.Err()
	})
	return call.Err()
}

func (r *catalogRepository) CommitOrder(ctx context.Context, operation app.OrderOperation, next domain.CatalogOrderState, result app.OrderResult) (err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	defer func() {
		if err != nil {
			r.failure = err
		}
	}()
	call, done, err := r.stateCall(ctx)
	if err != nil {
		return err
	}
	defer done(&err)
	command := operation.Command
	if command.ID.IsZero() || command.Content.IsZero() || operation.ExpectedAggregate.Uint64() == 0 {
		return app.ErrInvalidInput
	}
	source := command.Namespace.Source()
	bindings := domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domain.SourceRegistration{source}, Namespaces: []domain.CatalogOrderNamespace{command.Namespace}}
	states, aggregate, err := r.readStates(call, command.Content, domain.CatalogSubject{}, true, bindings)
	if err != nil {
		return err
	}
	if err = r.stateSourceActive(call, source); err != nil {
		return err
	}
	if len(states) == 0 {
		return app.ErrSubjectNotFound
	}
	current := states[len(states)-1].order
	decision, replay, err := current.LookupCommand(operation.Capability, command)
	if err != nil {
		return err
	}
	raw, err := next.PersistenceEncoding()
	if err != nil {
		return err
	}
	if states[len(states)-1].encoding == 2 {
		probe := catalogStateReader{raw: raw}
		if probe.text() == "pi08-order-state/1" {
			raw = registrationOrderBootstrap(raw)
		}
	}
	if replay {
		for _, s := range states {
			if s.revision == decision.View.Revision.Uint64() {
				if s.aggregate != operation.ExpectedAggregate.Uint64()+1 {
					return domain.ErrCatalogCommandConflict
				}
				if result.AggregateRevision.Uint64() != s.aggregate || !reflect.DeepEqual(result.Decision, decision) || !bytes.Equal(raw, s.raw) {
					return app.ErrInvalidInput
				}
				return nil
			}
		}
		return app.ErrRepositoryUnavailable
	}
	if err = r.registrationKeyOnly(call, command.ID.String()); err != nil {
		return err
	}
	if aggregate != operation.ExpectedAggregate {
		return domain.ErrCatalogRevisionConflict
	}
	expected, decision, err := current.Apply(operation.Capability, command)
	if err != nil {
		return err
	}
	expectedRaw, err := expected.PersistenceEncoding()
	if err != nil {
		return err
	}
	if states[len(states)-1].encoding == 2 {
		probe := catalogStateReader{raw: expectedRaw}
		if probe.text() == "pi08-order-state/1" {
			expectedRaw = registrationOrderBootstrap(expectedRaw)
		}
	}
	if result.AggregateRevision.Uint64() != aggregate.Uint64()+1 || !reflect.DeepEqual(result.Decision, decision) || !bytes.Equal(raw, expectedRaw) {
		return app.ErrInvalidInput
	}
	versions, err := r.catalogVersions(call, command.Content)
	if err != nil {
		return err
	}
	if _, err = r.transaction.ExecContext(call, "PRAGMA query_only=OFF"); err != nil {
		return repositoryError(call, err)
	}
	defer func() {
		if _, resetErr := r.transaction.ExecContext(r.ctx, "PRAGMA query_only=ON"); resetErr != nil && err == nil {
			err = repositoryError(r.ctx, resetErr)
		}
	}()
	updated, err := r.transaction.ExecContext(call, "UPDATE tc_catalog_managed SET aggregate_revision=? WHERE content_id=? AND aggregate_revision=?", result.AggregateRevision.Uint64(), command.Content.String(), aggregate.Uint64())
	if err != nil {
		return repositoryError(call, err)
	}
	if count, e := updated.RowsAffected(); e != nil || count != 1 {
		return domain.ErrCatalogRevisionConflict
	}
	sum := sha256.Sum256(raw)
	encoding := uint64(1)
	table := "tc_catalog_order_states"
	probe := catalogStateReader{raw: raw}
	if probe.text() == "pi08-order-state/2" {
		encoding = 2
		table = "tc_catalog_order_states_v2"
	}
	if _, err = r.transaction.ExecContext(call, "INSERT INTO "+table+" VALUES (?,?,?,?,?,?,?)", command.Content.String(), result.AggregateRevision.Uint64(), next.Revision().Uint64(), encoding, raw, len(raw), sum[:]); err != nil {
		return repositoryError(call, err)
	}
	stored := catalogStoredState{raw: raw, aggregate: result.AggregateRevision.Uint64(), revision: next.Revision().Uint64(), encoding: encoding}
	projection := catalogStateReader{raw: raw, repository: r, ctx: call, content: command.Content.String(), order: true, write: true}
	if err = projection.checkEnvelope(&stored, versions, true); err != nil {
		return err
	}
	if _, _, err = r.readStates(call, command.Content, domain.CatalogSubject{}, true, bindings); err != nil {
		return err
	}
	if err = r.registrationAccounting(call, command.Content); err != nil {
		return err
	}
	r.wrote = true
	tx := r.transaction
	r.authorize = append(r.authorize, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, _, err := current.LookupCommand(operation.Capability, command); err != nil {
			return err
		}
		var revoked int
		if err := tx.QueryRowContext(r.ctx, "SELECT revoked FROM tc_catalog_sources WHERE source_id=? AND registration_revision=?", source.ID().String(), source.Revision().String()).Scan(&revoked); err != nil {
			return repositoryError(r.ctx, err)
		}
		if revoked != 0 {
			return domain.ErrCatalogUnauthorized
		}
		return ctx.Err()
	})
	return call.Err()
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
