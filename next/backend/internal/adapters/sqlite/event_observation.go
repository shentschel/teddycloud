package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"strconv"
	"time"
)

// EventObservationLease is trusted synthetic A/T ingress, not device auth.
type EventObservationLease struct {
	state      *eventState
	generation uint64
	box, epoch string
}

func (o *LifecycleOwner) AcquireEventObservation(ctx context.Context, box string) (lease EventObservationLease, err error) {
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return lease, boundErr
	}
	defer finish()
	ctx = bounded

	if o == nil || o.gate == nil || !ev.ValidOpaqueID(box) {
		return lease, ev.ErrInvalidEvent
	}
	if err = o.gate.enterOperation(ctx); err != nil {
		return lease, ev.ErrUnavailable
	}
	defer o.gate.leave()
	db := o.database
	if db == nil || db.events == nil {
		return lease, ev.ErrUnsupported
	}
	s := db.events
	if s.fenced.Load() {
		return lease, ev.ErrUnavailable
	}
	epoch := eventRandomID()
	err = db.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
		if _, _, _, err := db.eventStream(ctx, tx, box); err != nil {
			return err
		}
		var revision uint64
		err := tx.QueryRowContext(ctx, "SELECT revision FROM tc_event_observations WHERE box=?", box).Scan(&revision)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if revision == 1<<63-1 {
			return ev.ErrCapacityExceeded
		}
		revision++
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(ctx, "INSERT INTO tc_event_observations(box,lease,sequence,revision,fresh) VALUES(?,?,0,?,0)", box, epoch, revision)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE tc_event_observations SET lease=?,sequence=0,revision=?,fresh=0,last_input=NULL,last_result=NULL WHERE box=?", epoch, revision, box)
		}
		if err != nil {
			return err
		}
		return recordEvent(tx, eventFact{owner: ev.Lifecycle, typ: ev.BoxInvalidated, kind: "box", id: box, box: box, revision: eventRevision(revision), payload: ev.InvalidationPayload{Reason: "source-replaced"}})
	})
	if err != nil {
		return lease, eventBoundaryError(ctx, err)
	}
	return EventObservationLease{s, o.generation.Load(), box, epoch}, nil
}
func (o *LifecycleOwner) ObserveEvent(ctx context.Context, lease EventObservationLease, sequence uint64, input ev.ObservationInput) (raw []byte, err error) {
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return nil, boundErr
	}
	defer finish()
	ctx = bounded

	encoded, err := input.Canonical()
	if err != nil {
		return nil, err
	}
	if o == nil || o.gate == nil {
		return nil, ev.ErrUnavailable
	}
	if err = o.gate.enterOperation(ctx); err != nil {
		return nil, ev.ErrUnavailable
	}
	defer o.gate.leave()
	db := o.database
	if db == nil || db.events == nil {
		return nil, ev.ErrUnsupported
	}
	s := db.events
	if s != lease.state || o.generation.Load() != lease.generation || s.fenced.Load() {
		return nil, ev.ErrRevoked
	}
	gap := false
	err = db.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
		var epoch string
		var last, revision uint64
		var previous, result []byte
		if err := tx.QueryRowContext(ctx, "SELECT lease,sequence,revision,last_input,last_result FROM tc_event_observations WHERE box=?", lease.box).Scan(&epoch, &last, &revision, &previous, &result); err != nil {
			return ev.ErrUnavailable
		}
		if epoch != lease.epoch {
			return ev.ErrRevoked
		}
		verdict := ev.CompareObservation(last, sequence, previous, encoded)
		if verdict != nil && verdict != ev.ErrSourceGap {
			return verdict
		}
		if verdict == nil && sequence == last {
			raw = bytes.Clone(result)
			return nil
		}
		if revision == 1<<63-1 {
			return ev.ErrCapacityExceeded
		}
		revision++
		if verdict == ev.ErrSourceGap {
			gap = true
			if _, err := tx.ExecContext(ctx, "UPDATE tc_event_observations SET lease=?,sequence=0,revision=?,fresh=0,last_input=NULL,last_result=NULL WHERE box=?", eventRandomID(), revision, lease.box); err != nil {
				return err
			}
			return recordEvent(tx, eventFact{owner: ev.Lifecycle, typ: ev.BoxInvalidated, kind: "box", id: lease.box, box: lease.box, revision: eventRevision(revision), payload: ev.InvalidationPayload{Reason: "source-gap"}})
		}
		f := eventFact{owner: ev.GatewayObservation, typ: input.Type, kind: "box", id: lease.box, box: lease.box, revision: eventRevision(revision), payload: input.Payload, occurred: input.OccurredAt, sourceEpoch: lease.epoch, sourceSequence: sequence}
		if err := db.appendEvents(ctx, tx, []eventFact{f}); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT envelope FROM tc_event_journal WHERE scope=? ORDER BY sequence DESC LIMIT 1", lease.box).Scan(&raw); err != nil {
			return err
		}
		column := "placement"
		if input.Type == ev.PlaybackObserved {
			column = "playback"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE tc_event_observations SET "+column+"=?,"+column+"_lease=? WHERE box=?", encoded, lease.epoch, lease.box); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE tc_event_observations SET sequence=?,revision=?,fresh=1,last_input=?,last_result=? WHERE box=?", sequence, revision, encoded, raw, lease.box)
		return err
	})
	if gap && err == nil {
		return nil, ev.ErrSourceGap
	}
	if err != nil {
		read, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		var epoch string
		var n uint64
		var previous, result []byte
		e := db.db.QueryRowContext(read, "SELECT lease,sequence,last_input,last_result FROM tc_event_observations WHERE box=?", lease.box).Scan(&epoch, &n, &previous, &result)
		if e == nil && epoch == lease.epoch && n == sequence && bytes.Equal(previous, encoded) {
			db.eventHint()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return result, nil
		}
		return nil, eventBoundaryError(ctx, err)
	}
	return raw, nil
}
func (db *Database) validateEventObservations(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT box,lease,sequence,revision,fresh,CASE WHEN length(last_input)<=4096 THEN last_input END,CASE WHEN length(last_result)<=8192 THEN last_result END FROM tc_event_observations LIMIT 65")
	if err != nil {
		return ev.ErrUnavailable
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var box, lease string
		var seq, revision uint64
		var fresh bool
		var input, result []byte
		if err = rows.Scan(&box, &lease, &seq, &revision, &fresh, &input, &result); err != nil {
			return ev.ErrUnavailable
		}
		if count > 64 || !ev.ValidOpaqueID(box) || !ev.ValidOpaqueID(lease) || revision == 0 || seq > 1<<63-1 {
			return ev.ErrUnavailable
		}
		if seq == 0 {
			if input != nil || result != nil || fresh {
				return ev.ErrUnavailable
			}
			continue
		}
		in, de := ev.DecodeObservation(input)
		if de != nil {
			return ev.ErrUnavailable
		}
		event, de := ev.Decode(result)
		reported := ev.ObservationInput{Type: event.Type, OccurredAt: event.OccurredAt, Payload: event.Payload}
		actual, _ := reported.Canonical()
		want, _ := in.Canonical()
		if de != nil || !fresh || event.Source.InstanceID != db.events.instance || event.Stream.TenantID != db.events.tenant || event.Source.Epoch != lease || event.Source.Sequence != strconv.FormatUint(seq, 10) || event.Aggregate.Revision == nil || *event.Aggregate.Revision != strconv.FormatUint(revision, 10) || event.Stream.BoxID == nil || *event.Stream.BoxID != box || !bytes.Equal(want, actual) {
			return ev.ErrUnavailable
		}
	}
	if rows.Err() != nil {
		return ev.ErrUnavailable
	}
	return nil
}

// EventObservationSnapshot distinguishes retained reports from lease freshness.
// Values are synthetic producer reports, never independently verified device state.
type EventObservationSnapshot struct {
	Revision                      uint64
	Placement, Playback           *ev.ObservationInput
	PlacementFresh, PlaybackFresh bool
}

func (o *LifecycleOwner) EventObservationSnapshot(ctx context.Context, cap ev.ReadCapability) (snapshot EventObservationSnapshot, err error) {
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return snapshot, boundErr
	}
	defer finish()
	ctx = bounded

	tenant, kind, box, err := cap.Scope()
	if err != nil {
		return snapshot, err
	}
	if kind != "box" {
		return snapshot, ev.ErrRevoked
	}
	if o == nil || o.gate == nil {
		return snapshot, ev.ErrUnavailable
	}
	if err = o.gate.enterOperation(ctx); err != nil {
		return snapshot, ev.ErrUnavailable
	}
	defer o.gate.leave()
	if o.database == nil || o.database.events == nil {
		return snapshot, ev.ErrUnsupported
	}
	s := o.database.events
	if tenant != s.tenant || !s.authority.OwnsRead(cap) {
		return snapshot, ev.ErrRevoked
	}
	if s.fenced.Load() {
		return snapshot, ev.ErrUnavailable
	}
	var lease string
	var placement, playback []byte
	var pl, bl sql.NullString
	err = o.database.db.QueryRowContext(ctx, "SELECT revision,lease,CASE WHEN length(placement)<=4096 THEN placement END,CASE WHEN length(playback)<=4096 THEN playback END,placement_lease,playback_lease FROM tc_event_observations WHERE box=?", box).Scan(&snapshot.Revision, &lease, &placement, &playback, &pl, &bl)
	if err == sql.ErrNoRows {
		return snapshot, ev.ErrReconcileRequired
	}
	if err != nil {
		return EventObservationSnapshot{}, ev.ErrUnavailable
	}
	if placement != nil {
		v, e := ev.DecodeObservation(placement)
		if e != nil {
			return EventObservationSnapshot{}, ev.ErrUnavailable
		}
		snapshot.Placement = &v
		snapshot.PlacementFresh = pl.Valid && pl.String == lease
	}
	if playback != nil {
		v, e := ev.DecodeObservation(playback)
		if e != nil {
			return EventObservationSnapshot{}, ev.ErrUnavailable
		}
		snapshot.Playback = &v
		snapshot.PlaybackFresh = bl.Valid && bl.String == lease
	}
	if err = cap.Authorize(); err != nil {
		return EventObservationSnapshot{}, err
	}
	return snapshot, nil
}

func (db *Database) validateEventReportedSlots(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT placement IS NOT NULL,playback IS NOT NULL,CASE WHEN length(placement)<=4096 THEN placement END,CASE WHEN length(playback)<=4096 THEN playback END,placement_lease,playback_lease FROM tc_event_observations LIMIT 65")
	if err != nil {
		return ev.ErrUnavailable
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var hasPlacement, hasPlayback bool
		var placement, playback []byte
		var pl, bl sql.NullString
		if err = rows.Scan(&hasPlacement, &hasPlayback, &placement, &playback, &pl, &bl); err != nil || count > 64 {
			return ev.ErrUnavailable
		}
		for i, raw := range [][]byte{placement, playback} {
			present, lease := hasPlacement, pl
			if i == 1 {
				present, lease = hasPlayback, bl
			}
			if !present {
				if lease.Valid {
					return ev.ErrUnavailable
				}
				continue
			}
			if !lease.Valid || !ev.ValidOpaqueID(lease.String) {
				return ev.ErrUnavailable
			}
			v, err := ev.DecodeObservation(raw)
			if err != nil || i == 0 && v.Type != ev.PlacementObserved && v.Type != ev.RemovalObserved || i == 1 && v.Type != ev.PlaybackObserved {
				return ev.ErrUnavailable
			}
		}
	}
	if rows.Err() != nil {
		return ev.ErrUnavailable
	}
	return nil
}
