package sqlite

import (
	"database/sql"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEventJournalTransactionBytes(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(strconv.Itoa(extra), func(t *testing.T) {
			o, _ := eventOwner(t)
			before := registrationSchemaRows(t, o.database.db)
			err := o.database.withinSQLTransaction(t.Context(), func(tx *sql.Tx) error {
				var epoch, source string
				var high, sourceHigh uint64
				if err := tx.QueryRowContext(t.Context(), "SELECT epoch,high_water FROM tc_event_streams WHERE scope='control'").Scan(&epoch, &high); err != nil {
					return err
				}
				if err := tx.QueryRowContext(t.Context(), "SELECT epoch,sequence FROM tc_event_sources WHERE owner='assignment'").Scan(&source, &sourceHigh); err != nil {
					return err
				}
				facts := make([]eventFact, 64)
				envelopes := make([]ev.Envelope, 64)
				size := 0
				label := time.Now().UTC().Format(ev.TimestampLayout)
				for i := range facts {
					id := catalogSchemaID("asn", i+1)
					revision := strconv.FormatUint(eventFixtureBase, 10)
					payload := ev.AssignmentPayload{AssignmentID: id, TagID: catalogSchemaID("tag", i+1), Revision: revision, Change: "original-target"}
					facts[i] = eventFact{owner: ev.Assignment, typ: ev.AssignmentChanged, kind: "assignment", id: id, revision: &revision, payload: payload, occurred: &label}
					n := strconv.FormatUint(high+uint64(i)+1, 10)
					envelopes[i] = ev.Envelope{Format: ev.Format, Type: ev.AssignmentChanged, PayloadVersion: 1, EventID: "ev1:" + epoch + ":" + n, Sequence: n, Stream: ev.Stream{TenantID: eventTenant, Kind: "control", Epoch: epoch}, Source: ev.Source{Owner: ev.Assignment, InstanceID: o.database.events.instance, Epoch: source, Sequence: strconv.FormatUint(sourceHigh+uint64(i)+1, 10)}, Aggregate: ev.Aggregate{Kind: "assignment", ID: id, Revision: &revision}, OccurredAt: &label, CommittedAt: label, Payload: payload}
					raw, e := ev.Encode(envelopes[i])
					if e != nil {
						return e
					}
					size += len(raw)
				}
				remaining := ev.MaxTransactionBytes + extra - size
				if remaining < 0 || remaining > 252*len(facts) {
					t.Fatal("byte boundary is unreachable", size)
				}
				for i := range facts {
					delta := min(252, remaining)
					remaining -= delta
					eventReferences(&envelopes[i], delta)
					facts[i].causation = envelopes[i].CausationID
					facts[i].correlation = envelopes[i].CorrelationID
					if err := recordEvent(tx, facts[i]); err != nil {
						return err
					}
				}
				return nil
			})
			if extra == 0 {
				if err != nil {
					t.Fatal("exact transaction bytes", err)
				}
			} else {
				if err == nil {
					t.Fatal("+1 transaction byte committed")
				}
				if !reflect.DeepEqual(before, registrationSchemaRows(t, o.database.db)) {
					t.Fatal("byte overflow consumed source or stream counter")
				}
			}
		})
	}
}
func TestEventJournalCorruption(t *testing.T) {
	for _, damage := range []string{"hole", "framing", "counter", "wrong-box"} {
		t.Run(damage, func(t *testing.T) {
			o, _ := eventOwner(t)
			box := strings.Repeat("2", 32)
			eventSeed(t, o, box, 3, 0, nil)
			cap := eventScope(t, o, "box", box)
			cursor := eventBarrier(t, o, cap)
			switch damage {
			case "hole":
				catalogSchemaExec(t, o.database.db, "DELETE FROM tc_event_journal WHERE scope=? AND sequence=?", box, eventFixtureBase+2)
			case "framing":
				catalogSchemaExec(t, o.database.db, "UPDATE tc_event_journal SET envelope=x'7b7d' WHERE scope=? AND sequence=?", box, eventFixtureBase+2)
			case "counter":
				catalogSchemaExec(t, o.database.db, "UPDATE tc_event_streams SET high_water=high_water+1 WHERE scope=?", box)
			case "wrong-box":
				var raw []byte
				o.database.db.QueryRowContext(t.Context(), "SELECT envelope FROM tc_event_journal WHERE scope=? AND sequence=?", box, eventFixtureBase+2).Scan(&raw)
				e, _ := ev.Decode(raw)
				other := strings.Repeat("3", 32)
				e.Stream.BoxID = &other
				e.Aggregate.ID = other
				raw, _ = ev.Encode(e)
				catalogSchemaExec(t, o.database.db, "UPDATE tc_event_journal SET envelope=? WHERE scope=? AND sequence=?", raw, box, eventFixtureBase+2)
			}
			// Cursor at the retained floor forces the malformed middle row into a batch.
			var epoch string
			o.database.db.QueryRowContext(t.Context(), "SELECT epoch FROM tc_event_streams WHERE scope=?", box).Scan(&epoch)
			cursor = eventCursor(o.database.events, box, epoch, eventFixtureBase)
			if _, err := o.Replay(t.Context(), cap, cursor, 16); err != ev.ErrUnavailable {
				t.Fatal("corruption not fenced", damage, err)
			}
		})
	}
}
