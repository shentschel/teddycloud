package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
)

type eventState struct {
	appends, appendFailures, prunedRows, prunedBytes atomic.Uint64
	tenant, instance                                 string
	key                                              []byte
	generation                                       uint64
	authority                                        *ev.Authority
	hub                                              *ev.Service
	fenced                                           atomic.Bool
}

func eventRandomID() string {
	var b [16]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic("event entropy unavailable")
		}
		s := hex.EncodeToString(b[:])
		if ev.ValidOpaqueID(s) {
			return s
		}
	}
}
func (db *Database) loadEvents(ctx context.Context) error {
	v, err := db.CurrentVersion(ctx)
	if err != nil {
		return err
	}
	if v < 11 {
		return nil
	}
	s := &eventState{}
	err = db.db.QueryRowContext(ctx, "SELECT CASE WHEN length(tenant)=32 THEN tenant END,CASE WHEN length(instance)=32 THEN instance END,CASE WHEN length(signing_key)=32 THEN signing_key END,authorization_generation FROM tc_event_meta WHERE singleton=1").Scan(&s.tenant, &s.instance, &s.key, &s.generation)
	if err == sql.ErrNoRows {
		var partial bool
		if err := db.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tc_event_streams) OR EXISTS(SELECT 1 FROM tc_event_sources) OR EXISTS(SELECT 1 FROM tc_event_journal) OR EXISTS(SELECT 1 FROM tc_event_observations)").Scan(&partial); err != nil || partial {
			return ev.ErrUnavailable
		}
		return nil
	}
	if err != nil || !ev.ValidOpaqueID(s.tenant) || !ev.ValidOpaqueID(s.instance) || len(s.key) != 32 || s.generation == 0 {
		return ev.ErrUnavailable
	}
	s.authority, err = ev.NewAuthority(s.tenant, s.instance)
	if err != nil {
		return err
	}
	db.events = s
	err = db.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
		if err := db.validateEventJournal(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT owner,epoch,sequence FROM tc_event_sources")
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			var owner, epoch string
			var seq uint64
			if err = rows.Scan(&owner, &epoch, &seq); err != nil {
				break
			}
			if !ev.ValidOpaqueID(epoch) || seq > 1<<63-1 {
				err = ev.ErrUnavailable
				break
			}
			n++
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil || n != 6 {
			return ev.ErrUnavailable
		}
		if err := db.validateEventObservations(ctx, tx); err != nil {
			return err
		}
		return db.validateEventReportedSlots(ctx, tx)
	})
	if err != nil {
		s.fenced.Store(true)
		return ev.ErrUnavailable
	}
	return db.restartEvents(ctx)
}

// NewEventHub is an internal composition point. It admits one explicitly bound
// tenant; no listener, external writer or source administration is introduced.
func (o *LifecycleOwner) NewEventHub(ctx context.Context, tenant string) (*ev.Service, error) {
	lifetime := ctx
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return nil, boundErr
	}
	defer finish()
	ctx = bounded

	if o == nil || o.gate == nil || !ev.ValidOpaqueID(tenant) {
		return nil, ev.ErrInvalidEvent
	}
	if err := o.gate.enterOperation(ctx); err != nil {
		return nil, ev.ErrUnavailable
	}
	defer o.gate.leave()
	db := o.database
	if db == nil {
		return nil, ev.ErrUnavailable
	}
	v, err := db.CurrentVersion(ctx)
	if err != nil {
		return nil, ev.ErrUnavailable
	}
	if v != 11 {
		return nil, ev.ErrUnsupported
	}
	if db.events == nil {
		state := &eventState{tenant: tenant, instance: eventRandomID(), key: make([]byte, 32), generation: 1}
		if _, err = rand.Read(state.key); err != nil {
			return nil, ev.ErrUnavailable
		}
		state.authority, err = ev.NewAuthority(tenant, state.instance)
		if err != nil {
			return nil, err
		}
		// Install only after initialization commits; no domain operation can enter
		// while this lifecycle gate is held.
		err = db.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "INSERT INTO tc_event_meta VALUES(1,?,?,?,1)", tenant, state.instance, state.key); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO tc_event_streams VALUES('control',?,0,0,0,0)", eventRandomID()); err != nil {
				return err
			}
			for _, owner := range []ev.Owner{ev.TagRegistry, ev.ContentStore, ev.Catalog, ev.Assignment, ev.GatewayObservation, ev.Lifecycle} {
				if _, err := tx.ExecContext(ctx, "INSERT INTO tc_event_sources VALUES(?,?,0)", owner, eventRandomID()); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, ev.ErrUnavailable
		}
		db.events = state
		if err = db.restartEvents(ctx); err != nil {
			state.fenced.Store(true)
			return nil, ev.ErrUnavailable
		}
	}
	if db.events.tenant != tenant || db.events.fenced.Load() {
		return nil, ev.ErrUnavailable
	}
	if db.events.hub != nil {
		return db.events.hub, nil
	}
	hub, err := ev.NewService(lifetime, o)
	if err != nil {
		return nil, err
	}
	db.events.hub = hub
	return hub, nil
}
func (o *LifecycleOwner) EventReadCapability(ctx context.Context, kind, box string, expires time.Time) (ev.ReadCapability, error) {
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return ev.ReadCapability{}, boundErr
	}
	defer finish()
	ctx = bounded

	if o == nil || o.gate == nil {
		return ev.ReadCapability{}, ev.ErrUnavailable
	}
	if err := o.gate.enterOperation(ctx); err != nil {
		return ev.ReadCapability{}, ev.ErrUnavailable
	}
	defer o.gate.leave()
	if o.database == nil || o.database.events == nil {
		return ev.ReadCapability{}, ev.ErrUnsupported
	}
	s := o.database.events
	if s.fenced.Load() {
		return ev.ReadCapability{}, ev.ErrUnavailable
	}
	return s.authority.IssueRead(kind, box, expires)
}
func (db *Database) restartEvents(ctx context.Context) error {
	if db.events == nil {
		return nil
	}
	var boxes []string
	rows, err := db.db.QueryContext(ctx, "SELECT box FROM tc_event_observations ORDER BY box")
	if err != nil {
		return ev.ErrUnavailable
	}
	for rows.Next() {
		var box string
		if err = rows.Scan(&box); err != nil {
			break
		}
		boxes = append(boxes, box)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return ev.ErrUnavailable
	}
	// Admission is still closed during these bounded lifecycle batches.
	for first := true; first || len(boxes) > 0; first = false {
		limit := 63
		if len(boxes) < limit {
			limit = len(boxes)
		}
		batch := boxes[:limit]
		boxes = boxes[limit:]
		err = db.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
			if first {
				if err := recordEvent(tx, eventFact{owner: ev.Lifecycle, typ: ev.StorageInvalidated, kind: "storage-scope", id: db.events.tenant, payload: ev.InvalidationPayload{Reason: "restart"}}); err != nil {
					return err
				}
			}
			for _, box := range batch {
				var revision uint64
				if err := tx.QueryRowContext(ctx, "SELECT revision FROM tc_event_observations WHERE box=?", box).Scan(&revision); err != nil {
					return err
				}
				if revision == 1<<63-1 {
					return ev.ErrCapacityExceeded
				}
				revision++
				if _, err := tx.ExecContext(ctx, "UPDATE tc_event_observations SET lease=?,sequence=0,revision=?,fresh=0,last_input=NULL,last_result=NULL WHERE box=?", eventRandomID(), revision, box); err != nil {
					return err
				}
				if err := recordEvent(tx, eventFact{owner: ev.Lifecycle, typ: ev.BoxInvalidated, kind: "box", id: box, box: box, revision: eventRevision(revision), payload: ev.InvalidationPayload{Reason: "restart"}}); err != nil {
					return err
				}
			}
			return eventPruneAge(ctx, tx, time.Now().UTC().Format(ev.TimestampLayout))
		})
		if err != nil {
			db.events.fenced.Store(true)
			return ev.ErrUnavailable
		}
	}
	return nil
}
func (db *Database) restoreEvents(ctx context.Context) error {
	s := db.events
	if s == nil {
		return nil
	}
	s.authority.Close()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return ev.ErrUnavailable
	}
	if s.generation == 1<<63-1 {
		return ev.ErrCapacityExceeded
	}
	err := db.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM tc_event_journal"); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT scope FROM tc_event_streams")
		if err != nil {
			return err
		}
		var scopes []string
		for rows.Next() {
			var scope string
			if err = rows.Scan(&scope); err != nil {
				break
			}
			scopes = append(scopes, scope)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		for _, scope := range scopes {
			if _, err = tx.ExecContext(ctx, "UPDATE tc_event_streams SET epoch=?,high_water=0,floor=0,retained_rows=0,retained_bytes=0 WHERE scope=?", eventRandomID(), scope); err != nil {
				return err
			}
		}
		for _, owner := range []ev.Owner{ev.TagRegistry, ev.ContentStore, ev.Catalog, ev.Assignment, ev.GatewayObservation, ev.Lifecycle} {
			if _, err = tx.ExecContext(ctx, "UPDATE tc_event_sources SET epoch=?,sequence=0 WHERE owner=?", eventRandomID(), owner); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE tc_event_meta SET signing_key=?,authorization_generation=authorization_generation+1", key)
		return err
	})
	if err != nil {
		s.fenced.Store(true)
		return ev.ErrUnavailable
	}
	s.key = key
	s.generation++
	s.authority, _ = ev.NewAuthority(s.tenant, s.instance)
	return db.restartEvents(ctx)
}
func (db *Database) eventHint() {
	if db.events != nil && db.events.hub != nil {
		db.events.hub.Hint()
	}
}
func (db *Database) closeEvents() error {
	if db.events == nil {
		return nil
	}
	s := db.events
	s.authority.Close()
	if s.hub == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.hub.Close(ctx)
}

// StorageInvalidation runs within an already admitted Content operation. It
// never re-enters the lifecycle gate or holds SQL across filesystem work.
func (s *contentSession) StorageInvalidation(ctx context.Context, reason contentstore.StorageInvalidationReason, phase contentstore.StorageInvalidationPhase) error {
	if !s.mutex.TryLock() {
		return contentstore.ErrBusy
	}
	defer s.mutex.Unlock()
	if !s.active || s.database == nil || s.owner.generation.Load() != s.generation {
		return contentstore.ErrRevoked
	}
	if reason != contentstore.StorageReconcile && reason != contentstore.StorageQuarantine || phase != contentstore.StorageIntent && phase != contentstore.StorageCompletion {
		return contentstore.ErrInvalidInput
	}
	db := s.database
	if db.events == nil {
		return nil
	}
	text := "reconcile"
	if reason == contentstore.StorageQuarantine {
		text = "quarantine"
	}
	call, cancel := context.WithTimeout(ctx, contentstore.TransactionTimeout)
	defer cancel()
	err := db.withinSQLTransaction(call, func(tx *sql.Tx) error {
		return recordEvent(tx, eventFact{owner: ev.ContentStore, typ: ev.StorageInvalidated, kind: "storage-scope", id: db.events.tenant, payload: ev.InvalidationPayload{Reason: text}})
	})
	if err != nil {
		if phase == contentstore.StorageCompletion {
			db.events.fenced.Store(true)
		}
		return contentstore.ErrUnavailable
	}
	return nil
}

func eventBoundaryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, safe := range []error{ev.ErrInvalidEvent, ev.ErrUnsupportedVersion, ev.ErrPayloadTooLarge, ev.ErrUnauthorizedProducer, ev.ErrCapacityExceeded, ev.ErrInvalidCursor, ev.ErrReconcileRequired, ev.ErrUnavailable, ev.ErrStaleObservation, ev.ErrObservationConflict, ev.ErrSourceGap, ev.ErrRevoked, ev.ErrUnsupported} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return ev.ErrUnavailable
}

func (o *LifecycleOwner) EventDiagnostics(ctx context.Context) (ev.Diagnostics, error) {
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return ev.Diagnostics{}, boundErr
	}
	defer finish()
	ctx = bounded

	if o == nil || o.gate == nil {
		return ev.Diagnostics{}, ev.ErrUnavailable
	}
	if err := o.gate.enterOperation(ctx); err != nil {
		return ev.Diagnostics{}, ev.ErrUnavailable
	}
	defer o.gate.leave()
	if o.database == nil || o.database.events == nil {
		return ev.Diagnostics{}, ev.ErrUnsupported
	}
	s := o.database.events
	d := ev.Diagnostics{}
	if s.hub != nil {
		d = s.hub.Diagnostics()
	}
	d.Appends = s.appends.Load()
	d.AppendFailures = s.appendFailures.Load()
	d.PrunedRows = s.prunedRows.Load()
	d.PrunedBytes = s.prunedBytes.Load()
	return d, nil
}

func eventRollback(db *Database, tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) && db.events != nil {
		db.events.fenced.Store(true)
	}
}

// RevokeEventReadCapabilities is a trusted internal lifecycle operation, not an
// administration endpoint. Read authorization generation is independent of an
// observation producer's lease and never changes domain ledgers or epochs.
func (o *LifecycleOwner) RevokeEventReadCapabilities(ctx context.Context) error {
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return boundErr
	}
	defer finish()
	ctx = bounded

	if o == nil || o.gate == nil || ctx == nil {
		return ev.ErrInvalidEvent
	}
	if err := o.gate.enterOperation(ctx); err != nil {
		return ev.ErrUnavailable
	}
	defer o.gate.leave()
	if o.database == nil || o.database.events == nil {
		return ev.ErrUnsupported
	}
	s := o.database.events
	if s.fenced.Load() {
		return ev.ErrUnavailable
	}
	if s.generation == 1<<63-1 {
		return ev.ErrCapacityExceeded
	}
	next := s.generation + 1
	err := o.database.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE tc_event_meta SET authorization_generation=? WHERE singleton=1 AND authorization_generation=?", next, s.generation)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return ev.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		s.fenced.Store(true)
		s.authority.Revoke()
		return eventBoundaryError(ctx, err)
	}
	s.generation = next
	s.authority.Revoke()
	return nil
}

func eventOperationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, ev.ErrInvalidEvent
	}
	if ctx.Value(contentOperationContextKey{}) != nil {
		return nil, nil, ev.ErrUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, ev.OperationTimeout)
	return bounded, cancel, nil
}
