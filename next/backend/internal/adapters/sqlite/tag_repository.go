package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"

	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var _ applicationtag.TagRepository = (*tagRepository)(nil)
var _ applicationtag.MetadataRepository = (*tagRepository)(nil)

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
	if !idText.Valid || len(uidBytes) != 8 || !revision.Valid || revision.Int64 < 1 {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	id, err := identity.ParseTagID(idText.String)
	if err != nil {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	var bytes [8]byte
	copy(bytes[:], uidBytes)
	value, err := repository.loadMetadata(ctx, id, domaintag.UIDFromBytes(bytes), domaintag.Revision(revision.Int64))
	if err != nil {
		return domaintag.Tag{}, false, applicationtag.ErrRepositoryUnavailable
	}
	return value, true, nil
}

const tagTimestampLayout = "2006-01-02T15:04:05.000000000Z"

// loadMetadata follows count -> raw scalar -> streamed exact byte -> bounded
// materialization, all on the same callback transaction. No joined payloads.
func (r *tagRepository) loadMetadata(ctx context.Context, id identity.TagID, uid domaintag.UID, revision domaintag.Revision) (domaintag.Tag, error) {
	if err := r.metadataCounts(ctx, id); err != nil {
		return domaintag.Tag{}, err
	}
	if err := r.metadataScalars(ctx, id); err != nil {
		return domaintag.Tag{}, err
	}
	if err := r.validateSupportIDs(ctx, id); err != nil {
		return domaintag.Tag{}, err
	}
	size, err := domaintag.NewHistorySize(id, uid, revision)
	if err != nil {
		return domaintag.Tag{}, applicationtag.ErrRepositoryUnavailable
	}
	err = r.eachObservation(ctx, id, func(o domaintag.StoredObservation) error {
		return size.AddObservation(id, revision, o.Observation, o.IntroducedRevision)
	})
	if err != nil {
		return domaintag.Tag{}, tagBoundaryError(ctx, err)
	}
	err = r.eachDecision(ctx, id, func(d domaintag.StoredDecision, n int) error {
		return size.AddDecision(d.ID, d.Key, d.ExpectedRevision, d.ResultRevision, revision, n)
	})
	if err != nil {
		return domaintag.Tag{}, tagBoundaryError(ctx, err)
	}
	for key := 0; key < 4; key++ {
		var count int
		err = r.transaction.QueryRowContext(ctx, `SELECT count(*) FROM (
    SELECT observation_id FROM tc_tag_observations o WHERE o.tag_id=? AND o.fact_key=? AND o.review=1
    AND (o.introduced_revision > COALESCE((SELECT max(result_revision) FROM tc_tag_decisions WHERE tag_id=? AND fact_key=?),0)
    OR EXISTS(SELECT 1 FROM tc_tag_decision_support s WHERE s.tag_id=o.tag_id AND s.observation_id=o.observation_id AND s.decision_id=(SELECT decision_id FROM tc_tag_decisions WHERE tag_id=? AND fact_key=? ORDER BY result_revision DESC LIMIT 1)))
    LIMIT 4097)`, id.String(), key, id.String(), key, id.String(), key).Scan(&count)
		if err != nil {
			return domaintag.Tag{}, tagStorageError(ctx, err)
		}
		if err := size.AddActive(count); err != nil {
			return domaintag.Tag{}, applicationtag.ErrRepositoryUnavailable
		}
	}
	expectedSize, err := size.Finish()
	if err != nil {
		return domaintag.Tag{}, applicationtag.ErrRepositoryUnavailable
	}
	var observations []domaintag.StoredObservation
	var decisions []domaintag.StoredDecision
	if err := r.eachObservation(ctx, id, func(o domaintag.StoredObservation) error { observations = append(observations, o); return nil }); err != nil {
		return domaintag.Tag{}, err
	}
	// Close decision cursors before loading support on the single connection.
	if err := r.eachDecision(ctx, id, func(d domaintag.StoredDecision, _ int) error { decisions = append(decisions, d); return nil }); err != nil {
		return domaintag.Tag{}, err
	}
	for i := range decisions {
		d := &decisions[i]
		rows, err := r.transaction.QueryContext(ctx, `SELECT observation_id FROM tc_tag_decision_support WHERE tag_id=? AND decision_id=? LIMIT 4097`, id.String(), d.ID.String())
		if err != nil {
			return domaintag.Tag{}, tagStorageError(ctx, err)
		}
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				rows.Close()
				return domaintag.Tag{}, tagStorageError(ctx, err)
			}
			support, err := evidence.ParseID(text)
			if err != nil || len(d.Support) >= domaintag.MaxDecisionSupportLinks {
				rows.Close()
				return domaintag.Tag{}, applicationtag.ErrRepositoryUnavailable
			}
			d.Support = append(d.Support, support)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return domaintag.Tag{}, tagStorageError(ctx, err)
		}
	}
	value, err := domaintag.RestoreMetadata(id, uid, revision, observations, decisions)
	if err != nil {
		return domaintag.Tag{}, applicationtag.ErrRepositoryUnavailable
	}
	// Second-pass parity uses the complete encoder rather than a SQL size sum.
	got, err := value.EncodedSize()
	if err != nil || got != expectedSize {
		return domaintag.Tag{}, applicationtag.ErrRepositoryUnavailable
	}
	return value, nil
}

func (r *tagRepository) metadataCounts(ctx context.Context, id identity.TagID) error {
	for _, query := range []string{
		`SELECT count(*) FROM (SELECT 1 FROM tc_tag_observations WHERE tag_id=? LIMIT 4097)`,
		`SELECT count(*) FROM (SELECT 1 FROM tc_tag_decisions WHERE tag_id=? LIMIT 4097)`,
		`SELECT count(*) FROM (SELECT 1 FROM tc_tag_decision_support WHERE tag_id=? LIMIT 16385)`,
	} {
		var count int
		if err := r.transaction.QueryRowContext(ctx, query, id.String()).Scan(&count); err != nil {
			return tagStorageError(ctx, err)
		}
		limit := 4096
		if query == `SELECT count(*) FROM (SELECT 1 FROM tc_tag_decision_support WHERE tag_id=? LIMIT 16385)` {
			limit = 16384
		}
		if count > limit {
			return applicationtag.ErrRepositoryUnavailable
		}
	}
	var invalid bool
	err := r.transaction.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM tc_tag_decisions d WHERE d.tag_id=? AND
 ((SELECT count(*) FROM (SELECT 1 FROM tc_tag_decision_support WHERE decision_id=d.decision_id LIMIT 4097)) NOT BETWEEN 1 AND 4096)
 ) OR EXISTS(
 SELECT 1 FROM tc_tag_decision_support s WHERE
 (s.tag_id=? OR EXISTS(SELECT 1 FROM tc_tag_decisions d WHERE d.decision_id=s.decision_id AND d.tag_id=?))
 AND (NOT EXISTS(SELECT 1 FROM tc_tag_decisions d WHERE d.decision_id=s.decision_id AND d.tag_id=s.tag_id)
 OR NOT EXISTS(SELECT 1 FROM tc_tag_observations o WHERE o.observation_id=s.observation_id AND o.tag_id=s.tag_id)))`, id.String(), id.String(), id.String()).Scan(&invalid)
	if err != nil {
		return tagStorageError(ctx, err)
	}
	if invalid {
		return applicationtag.ErrRepositoryUnavailable
	}
	return nil
}

func (r *tagRepository) metadataScalars(ctx context.Context, id identity.TagID) error {
	// All variable values are measured in bytes before any payload Scan.
	checks := []string{
		`SELECT EXISTS(SELECT 1 FROM tc_tag_observations WHERE tag_id=? AND (
   typeof(observation_id)!='text' OR length(CAST(observation_id AS BLOB))!=30
   OR typeof(tag_id)!='text' OR length(CAST(tag_id AS BLOB))!=30
   OR typeof(source_name)!='text' OR length(CAST(source_name AS BLOB)) NOT BETWEEN 1 AND 128
   OR typeof(source_revision)!='text' OR length(CAST(source_revision AS BLOB)) NOT BETWEEN 1 AND 128
   OR typeof(source_record)!='text' OR length(CAST(source_record AS BLOB)) NOT BETWEEN 1 AND 128
   OR typeof(observed_at)!='text' OR length(CAST(observed_at AS BLOB))!=30
   OR typeof(introduced_revision)!='integer' OR introduced_revision<2
   OR typeof(fact_key)!='integer' OR fact_key NOT BETWEEN 0 AND 3
   OR typeof(value)!='integer' OR value NOT IN (0,1)
   OR typeof(confidence)!='integer' OR confidence NOT BETWEEN 0 AND 2
   OR typeof(review)!='integer' OR review NOT BETWEEN 0 AND 3))`,
		`SELECT EXISTS(SELECT 1 FROM tc_tag_decisions WHERE tag_id=? AND (
   typeof(decision_id)!='text' OR length(CAST(decision_id AS BLOB))!=30
   OR typeof(tag_id)!='text' OR length(CAST(tag_id AS BLOB))!=30
   OR typeof(fact_key)!='integer' OR fact_key NOT BETWEEN 0 AND 3
   OR typeof(expected_revision)!='integer' OR expected_revision<=0 OR expected_revision>=9223372036854775807
   OR typeof(result_revision)!='integer' OR result_revision!=expected_revision+1))`,
		`SELECT EXISTS(SELECT 1 FROM tc_tag_decision_support WHERE tag_id=? AND (
   typeof(tag_id)!='text' OR length(CAST(tag_id AS BLOB))!=30
   OR typeof(decision_id)!='text' OR length(CAST(decision_id AS BLOB))!=30
   OR typeof(observation_id)!='text' OR length(CAST(observation_id AS BLOB))!=30))`,
	}
	for _, query := range checks {
		var invalid bool
		if err := r.transaction.QueryRowContext(ctx, query, id.String()).Scan(&invalid); err != nil {
			return tagStorageError(ctx, err)
		}
		if invalid {
			return applicationtag.ErrRepositoryUnavailable
		}
	}
	return nil
}

func (r *tagRepository) eachObservation(ctx context.Context, id identity.TagID, visit func(domaintag.StoredObservation) error) error {
	rows, err := r.transaction.QueryContext(ctx, `SELECT observation_id,introduced_revision,fact_key,value,source_name,source_revision,source_record,observed_at,confidence,review FROM tc_tag_observations WHERE tag_id=? LIMIT 4097`, id.String())
	if err != nil {
		return tagStorageError(ctx, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if count >= 4096 {
			return applicationtag.ErrRepositoryUnavailable
		}
		count++
		var oid, name, sourceRevision, record, timestamp string
		var introduced int64
		var key, value, confidence, review int
		if err := rows.Scan(&oid, &introduced, &key, &value, &name, &sourceRevision, &record, &timestamp, &confidence, &review); err != nil {
			return tagStorageError(ctx, err)
		}
		observationID, err := evidence.ParseID(oid)
		if err != nil {
			return applicationtag.ErrRepositoryUnavailable
		}
		source, err := evidence.NewSource(name, sourceRevision, record)
		if err != nil {
			return applicationtag.ErrRepositoryUnavailable
		}
		observedAt, err := time.Parse(tagTimestampLayout, timestamp)
		if err != nil || observedAt.Format(tagTimestampLayout) != timestamp {
			return applicationtag.ErrRepositoryUnavailable
		}
		text := "false"
		if value == 1 {
			text = "true"
		}
		observation, err := evidence.NewObservation(observationID, source, evidence.Claim{Subject: id.String(), Key: domaintag.FactKey(key).String(), Value: text}, observedAt, evidence.Confidence(confidence), evidence.Review(review))
		if err != nil {
			return applicationtag.ErrRepositoryUnavailable
		}
		if err := visit(domaintag.StoredObservation{Observation: observation, IntroducedRevision: domaintag.Revision(introduced)}); err != nil {
			return err
		}
	}
	return tagStorageError(ctx, rows.Err())
}

func (r *tagRepository) validateSupportIDs(ctx context.Context, id identity.TagID) error {
	rows, err := r.transaction.QueryContext(ctx, `SELECT decision_id,observation_id FROM tc_tag_decision_support WHERE tag_id=? LIMIT 16385`, id.String())
	if err != nil {
		return tagStorageError(ctx, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if count >= domaintag.MaxRetainedDecisionSupportLinks {
			return applicationtag.ErrRepositoryUnavailable
		}
		count++
		var decision, observation string
		if err := rows.Scan(&decision, &observation); err != nil {
			return tagStorageError(ctx, err)
		}
		if _, err := domaintag.ParseDecisionID(decision); err != nil {
			return applicationtag.ErrRepositoryUnavailable
		}
		if _, err := evidence.ParseID(observation); err != nil {
			return applicationtag.ErrRepositoryUnavailable
		}
	}
	return tagStorageError(ctx, rows.Err())
}
func (r *tagRepository) eachDecision(ctx context.Context, id identity.TagID, visit func(domaintag.StoredDecision, int) error) error {
	rows, err := r.transaction.QueryContext(ctx, `SELECT decision_id,fact_key,expected_revision,result_revision,
 (SELECT count(*) FROM tc_tag_decision_support s WHERE s.decision_id=d.decision_id) FROM tc_tag_decisions d WHERE tag_id=? LIMIT 4097`, id.String())
	if err != nil {
		return tagStorageError(ctx, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if count >= 4096 {
			return applicationtag.ErrRepositoryUnavailable
		}
		count++
		var text string
		var key, n int
		var expected, result int64
		if err := rows.Scan(&text, &key, &expected, &result, &n); err != nil {
			return tagStorageError(ctx, err)
		}
		did, err := domaintag.ParseDecisionID(text)
		if err != nil {
			return applicationtag.ErrRepositoryUnavailable
		}
		if err := visit(domaintag.StoredDecision{ID: did, Key: domaintag.FactKey(key), ExpectedRevision: domaintag.Revision(expected), ResultRevision: domaintag.Revision(result)}, n); err != nil {
			return err
		}
	}
	return tagStorageError(ctx, rows.Err())
}

func (r *tagRepository) CompareAndSwap(ctx context.Context, expected domaintag.Revision, next domaintag.Tag) (result error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.transaction == nil {
		return applicationtag.ErrRepositoryUnavailable
	}
	if expected <= 0 || next.ID().IsZero() {
		return applicationtag.ErrInvalidInput
	}
	current, found, err := r.find(ctx, tagIdentityColumns+` WHERE tag_id = ?`, next.ID().String())
	if err != nil {
		return err
	}
	if !found {
		return applicationtag.ErrTagNotFound
	}
	if current.Revision() != expected {
		return applicationtag.ErrRevisionConflict
	}
	if !current.IsMetadataSuccessor(next) {
		return applicationtag.ErrInvalidInput
	}
	newObservations := 0
	if err := next.VisitHistory(func(_ evidence.Observation, v domaintag.Revision) error {
		if v > expected {
			newObservations++
			if newObservations > domaintag.MaxNewObservationsPerCommand {
				return applicationtag.ErrLimitExceeded
			}
		}
		return nil
	}, func(domaintag.DecisionID, domaintag.FactKey, domaintag.Revision, domaintag.Revision, int, func(int) evidence.ID) error {
		return nil
	}); err != nil {
		return err
	}
	if _, err := r.transaction.ExecContext(ctx, `SAVEPOINT tag_metadata`); err != nil {
		return tagStorageError(ctx, err)
	}
	defer func() {
		if result != nil {
			_, rollbackErr := r.transaction.ExecContext(ctx, `ROLLBACK TO tag_metadata`)
			if rollbackErr != nil {
				result = errors.Join(result, tagStorageError(ctx, rollbackErr))
			}
		}
		if _, err := r.transaction.ExecContext(ctx, `RELEASE tag_metadata`); err != nil {
			result = errors.Join(result, tagStorageError(ctx, err))
		}
	}()
	err = next.VisitHistory(func(o evidence.Observation, revision domaintag.Revision) error {
		if revision <= expected {
			return nil
		}
		key, err := domaintag.ParseFactKey(o.Claim().Key)
		if err != nil {
			return applicationtag.ErrInvalidInput
		}
		value := 0
		if o.Claim().Value == "true" {
			value = 1
		}
		return r.metadataExec(ctx, `INSERT INTO tc_tag_observations (observation_id,tag_id,introduced_revision,fact_key,value,source_name,source_revision,source_record,observed_at,confidence,review) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, o.ID().String(), next.ID().String(), int64(revision), int(key), value, o.Source().Name(), o.Source().Revision(), o.Source().Record(), o.ObservedAt().Format(tagTimestampLayout), int(o.Confidence()), int(o.Review()))
	}, func(id domaintag.DecisionID, key domaintag.FactKey, e, v domaintag.Revision, n int, support func(int) evidence.ID) error {
		if v <= expected {
			return nil
		}
		if err := r.metadataExec(ctx, `INSERT INTO tc_tag_decisions (decision_id,tag_id,fact_key,expected_revision,result_revision) VALUES (?,?,?,?,?)`, id.String(), next.ID().String(), int(key), int64(e), int64(v)); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if err := r.metadataExec(ctx, `INSERT INTO tc_tag_decision_support (tag_id,decision_id,observation_id) VALUES (?,?,?)`, next.ID().String(), id.String(), support(i).String()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	changed, err := r.transaction.ExecContext(ctx, `UPDATE tc_tags SET revision=? WHERE tag_id=? AND revision=?`, int64(next.Revision()), next.ID().String(), int64(expected))
	if err != nil {
		return tagStorageError(ctx, err)
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return tagStorageError(ctx, err)
	}
	if n != 1 {
		return applicationtag.ErrRevisionConflict
	}
	return nil
}
func (r *tagRepository) metadataExec(ctx context.Context, query string, args ...any) error {
	mapError := func(ctx context.Context, err error) error {
		var driverError *sqliteDriver.Error
		if ctx.Err() == nil && errors.As(err, &driverError) && driverError.Code() == sqlite3.SQLITE_BUSY_SNAPSHOT {
			// Another connection committed after this metadata transaction
			// read its expected revision; its snapshot cannot become a writer.
			return applicationtag.ErrRevisionConflict
		}
		return tagStorageError(ctx, err)
	}
	err := execWithContention(ctx, r.transaction, mapError, query, args...)
	if errors.Is(err, applicationtag.ErrIdentityConflict) {
		return applicationtag.ErrEvidenceConflict
	}
	return tagBoundaryError(ctx, err)
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
