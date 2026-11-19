package sqlite

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
)

func eventRevision(n uint64) *string { s := strconv.FormatUint(n, 10); return &s }
func (db *Database) eventStream(ctx context.Context, tx *sql.Tx, scope string) (epoch string, high, floor uint64, err error) {
	err = tx.QueryRowContext(ctx, "SELECT epoch,high_water,floor FROM tc_event_streams WHERE scope=?", scope).Scan(&epoch, &high, &floor)
	if err != sql.ErrNoRows {
		return
	}
	if scope != "control" {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tc_event_streams WHERE scope<>'control'").Scan(&n); err != nil {
			return
		}
		if n >= ev.MaxBoxStreams {
			err = ev.ErrCapacityExceeded
			return
		}
	}
	epoch = eventRandomID()
	_, err = tx.ExecContext(ctx, "INSERT INTO tc_event_streams VALUES(?,?,0,0,0,0)", scope, epoch)
	return
}
func (db *Database) appendEvents(ctx context.Context, tx *sql.Tx, facts []eventFact) (result error) {
	defer func() {
		if result != nil && db.events != nil {
			db.events.appendFailures.Add(1)
		}
	}()
	if len(facts) == 0 {
		return nil
	}
	state := db.events
	if state == nil || state.fenced.Load() {
		return ev.ErrUnavailable
	}
	label := time.Now().UTC().Format(ev.TimestampLayout)
	total := 0
	for _, f := range facts {
		scope := "control"
		if f.box != "" {
			scope = f.box
		}
		epoch, high, _, err := db.eventStream(ctx, tx, scope)
		if err != nil {
			return err
		}
		if high == 1<<63-1 {
			return ev.ErrCapacityExceeded
		}
		sourceEpoch := f.sourceEpoch
		sequence := f.sourceSequence
		if sourceEpoch == "" {
			if err = tx.QueryRowContext(ctx, "SELECT epoch,sequence FROM tc_event_sources WHERE owner=?", f.owner).Scan(&sourceEpoch, &sequence); err != nil {
				return ev.ErrUnavailable
			}
			if sequence == 1<<63-1 {
				return ev.ErrCapacityExceeded
			}
			sequence++
			if _, err = tx.ExecContext(ctx, "UPDATE tc_event_sources SET sequence=? WHERE owner=?", sequence, f.owner); err != nil {
				return err
			}
		}
		stream := ev.Stream{TenantID: state.tenant, Kind: "control", Epoch: epoch}
		if f.box != "" {
			box := f.box
			stream.Kind = "box"
			stream.BoxID = &box
		}
		text := strconv.FormatUint(high+1, 10)
		e := ev.Envelope{Format: ev.Format, Type: f.typ, PayloadVersion: 1, EventID: "ev1:" + epoch + ":" + text, Sequence: text, Stream: stream, Source: ev.Source{Owner: f.owner, InstanceID: state.instance, Epoch: sourceEpoch, Sequence: strconv.FormatUint(sequence, 10)}, Aggregate: ev.Aggregate{Kind: f.kind, ID: f.id, Revision: f.revision}, OccurredAt: f.occurred, CommittedAt: label, CausationID: f.causation, CorrelationID: f.correlation, Payload: f.payload}
		raw, err := ev.Encode(e)
		if err != nil {
			return err
		}
		total += len(raw)
		if total > ev.MaxTransactionBytes {
			return ev.ErrCapacityExceeded
		}
		// Eviction precedes insertion so table constraints remain true throughout.
		if err = db.eventRoom(ctx, tx, scope, len(raw), label); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO tc_event_journal VALUES(?,?,?,?,?)", scope, epoch, high+1, label, raw); err != nil {
			return err
		}
		if value, ok := eventTransactions.Load(tx); ok {
			value.(*eventCollector).appended++
		}
		if _, err = tx.ExecContext(ctx, "UPDATE tc_event_streams SET high_water=?,retained_rows=retained_rows+1,retained_bytes=retained_bytes+? WHERE scope=?", high+1, len(raw), scope); err != nil {
			return err
		}
	}
	return nil
}
func (db *Database) validateEventJournal(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT scope,epoch,high_water,floor,retained_rows,retained_bytes FROM tc_event_streams ORDER BY scope")
	if err != nil {
		return ev.ErrUnavailable
	}
	type stream struct {
		scope, epoch             string
		high, floor, rows, bytes uint64
	}
	var streams []stream
	controlCount := 0
	for rows.Next() {
		var s stream
		if err = rows.Scan(&s.scope, &s.epoch, &s.high, &s.floor, &s.rows, &s.bytes); err != nil {
			break
		}
		streams = append(streams, s)
		if s.scope == "control" {
			controlCount++
		} else if !ev.ValidOpaqueID(s.scope) {
			err = ev.ErrUnavailable
			break
		}
		if len(streams) > 65 {
			err = ev.ErrUnavailable
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return ev.ErrUnavailable
	}
	if controlCount != 1 {
		return ev.ErrUnavailable
	}
	type sourceCounter struct {
		epoch    string
		sequence uint64
	}
	sources := map[ev.Owner]sourceCounter{}
	sourceRows, err := tx.QueryContext(ctx, "SELECT owner,epoch,sequence FROM tc_event_sources")
	if err != nil {
		return ev.ErrUnavailable
	}
	for sourceRows.Next() {
		var owner ev.Owner
		var c sourceCounter
		if err = sourceRows.Scan(&owner, &c.epoch, &c.sequence); err != nil {
			break
		}
		if !ev.ValidOpaqueID(c.epoch) || c.sequence > 1<<63-1 {
			err = ev.ErrUnavailable
			break
		}
		sources[owner] = c
	}
	if err == nil {
		err = sourceRows.Err()
	}
	sourceRows.Close()
	if err != nil || len(sources) != 6 {
		return ev.ErrUnavailable
	}
	for _, owner := range []ev.Owner{ev.TagRegistry, ev.ContentStore, ev.Catalog, ev.Assignment, ev.GatewayObservation, ev.Lifecycle} {
		if _, ok := sources[owner]; !ok {
			return ev.ErrUnavailable
		}
	}
	var totalRows, totalBytes uint64
	for _, s := range streams {
		if !ev.ValidOpaqueID(s.epoch) || s.floor > s.high || s.high > 1<<63-1 || s.rows != s.high-s.floor {
			return ev.ErrUnavailable
		}
		data, err := tx.QueryContext(ctx, "SELECT sequence,committed_at,CASE WHEN length(envelope)<=8192 THEN envelope END FROM tc_event_journal WHERE scope=? ORDER BY sequence", s.scope)
		if err != nil {
			return ev.ErrUnavailable
		}
		next, bytes := s.floor+1, uint64(0)
		for data.Next() {
			var n uint64
			var label string
			var raw []byte
			if err = data.Scan(&n, &label, &raw); err != nil {
				break
			}
			e, de := ev.Decode(raw)
			scope := "control"
			if e.Stream.BoxID != nil {
				scope = *e.Stream.BoxID
			}
			if de != nil || n != next || e.Sequence != strconv.FormatUint(n, 10) || e.Stream.Epoch != s.epoch || e.Stream.TenantID != db.events.tenant || scope != s.scope || label != e.CommittedAt {
				err = ev.ErrUnavailable
				break
			}
			counter := sources[e.Source.Owner]
			sourceSequence, _ := ev.Decimal(e.Source.Sequence, false)
			if e.Source.InstanceID != db.events.instance || e.Source.Owner != ev.GatewayObservation && (e.Source.Epoch != counter.epoch || sourceSequence > counter.sequence) {
				err = ev.ErrUnavailable
				break
			}
			bytes += uint64(len(raw))
			next++
		}
		if err == nil {
			err = data.Err()
		}
		data.Close()
		if err != nil || next != s.high+1 || bytes != s.bytes || s.rows > ev.MaxStreamRows || bytes > ev.MaxStreamBytes {
			return ev.ErrUnavailable
		}
		totalRows += s.rows
		totalBytes += bytes
	}
	if totalRows > ev.MaxOwnerRows || totalBytes > ev.MaxOwnerBytes {
		return ev.ErrUnavailable
	}
	return nil
}
