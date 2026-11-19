package sqlite

import (
	"database/sql"
	"fmt"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"strconv"
	"strings"
	"testing"
	"time"
)

const eventFixtureBase uint64 = 1000000000000000000

// These are trusted canonical storage fixtures, not external producer commands.
// Large counters keep encoded widths fixed for exact byte-boundary arithmetic.
func eventFixtureEnvelope(state *eventState, scope, epoch, source string, n uint64) ev.Envelope {
	box := scope
	num := strconv.FormatUint(n, 10)
	rev := strconv.FormatUint(eventFixtureBase, 10)
	label := time.Now().UTC().Format(ev.TimestampLayout)
	return ev.Envelope{Format: ev.Format, Type: ev.PlaybackObserved, PayloadVersion: 1, EventID: "ev1:" + epoch + ":" + num, Sequence: num, Stream: ev.Stream{TenantID: state.tenant, Kind: "box", BoxID: &box, Epoch: epoch}, Source: ev.Source{Owner: ev.GatewayObservation, InstanceID: state.instance, Epoch: source, Sequence: num}, Aggregate: ev.Aggregate{Kind: "box", ID: box, Revision: &rev}, OccurredAt: &label, CommittedAt: label, Payload: ev.PlaybackPayload{State: "starting", ContentID: eventText(catalogSchemaID("cnt", 1)), VersionID: eventText(catalogSchemaID("ver", 1))}}
}
func eventText(s string) *string { return &s }
func eventReferences(e *ev.Envelope, extra int) {
	if extra <= 126 {
		if extra > 0 {
			e.CausationID = eventText(strings.Repeat("a", extra+2))
		}
		return
	}
	e.CausationID = eventText(strings.Repeat("a", 128))
	e.CorrelationID = eventText(strings.Repeat("b", extra-126+2))
}
func eventSeed(t *testing.T, o *LifecycleOwner, scope string, count, target int, label *string) {
	t.Helper()
	err := o.database.withinSQLTransaction(t.Context(), func(tx *sql.Tx) error {
		epoch, _, _, err := o.database.eventStream(t.Context(), tx, scope)
		if err != nil {
			return err
		}
		var source string
		if err = tx.QueryRowContext(t.Context(), "SELECT epoch FROM tc_event_sources WHERE owner='gateway-observation'").Scan(&source); err != nil {
			return err
		}
		sample := eventFixtureEnvelope(o.database.events, scope, epoch, source, eventFixtureBase+1)
		baseRaw, err := ev.Encode(sample)
		if err != nil {
			return err
		}
		base := len(baseRaw)
		if count == 0 {
			count = (target + base + 251) / (base + 252)
		}
		if target == 0 {
			target = count * base
		}
		if count > 4096 || target < count*base || target > count*(base+252) {
			t.Fatalf("unreachable fixture count=%d target=%d base=%d", count, target, base)
		}
		statement, err := tx.PrepareContext(t.Context(), "INSERT INTO tc_event_journal VALUES(?,?,?,?,?)")
		if err != nil {
			return err
		}
		defer statement.Close()
		remaining := target - count*base
		for i := 1; i <= count; i++ {
			e := eventFixtureEnvelope(o.database.events, scope, epoch, source, eventFixtureBase+uint64(i))
			if label != nil {
				e.CommittedAt = *label
			}
			extra := min(252, remaining)
			remaining -= extra
			eventReferences(&e, extra)
			raw, err := ev.Encode(e)
			if err != nil {
				return err
			}
			if _, err = statement.ExecContext(t.Context(), scope, epoch, eventFixtureBase+uint64(i), e.CommittedAt, raw); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(t.Context(), "UPDATE tc_event_streams SET high_water=?,floor=?,retained_rows=?,retained_bytes=? WHERE scope=?", eventFixtureBase+uint64(count), eventFixtureBase, count, target, scope); err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), "UPDATE tc_event_sources SET sequence=max(sequence,?) WHERE owner='gateway-observation'", eventFixtureBase+uint64(count))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func eventAppendBox(t *testing.T, o *LifecycleOwner, box string) error {
	t.Helper()
	return o.database.withinSQLTransaction(t.Context(), func(tx *sql.Tx) error {
		return recordEvent(tx, eventFact{owner: ev.Lifecycle, typ: ev.BoxInvalidated, kind: "box", id: box, box: box, revision: eventRevision(1), payload: ev.InvalidationPayload{Reason: "restart"}})
	})
}
func eventAppendSize(t *testing.T, o *LifecycleOwner, box string) int {
	t.Helper()
	var source string
	var seq uint64
	if err := o.database.db.QueryRowContext(t.Context(), "SELECT epoch,sequence FROM tc_event_sources WHERE owner='lifecycle'").Scan(&source, &seq); err != nil {
		t.Fatal(err)
	}
	e := ev.Envelope{Format: ev.Format, Type: ev.BoxInvalidated, PayloadVersion: 1, EventID: "ev1:" + strings.Repeat("a", 32) + ":" + strconv.FormatUint(eventFixtureBase+4000, 10), Sequence: strconv.FormatUint(eventFixtureBase+4000, 10), Stream: ev.Stream{TenantID: eventTenant, Kind: "box", BoxID: &box, Epoch: strings.Repeat("a", 32)}, Source: ev.Source{Owner: ev.Lifecycle, InstanceID: o.database.events.instance, Epoch: source, Sequence: strconv.FormatUint(seq+1, 10)}, Aggregate: ev.Aggregate{Kind: "box", ID: box, Revision: eventRevision(1)}, CommittedAt: time.Now().UTC().Format(ev.TimestampLayout), Payload: ev.InvalidationPayload{Reason: "restart"}}
	raw, err := ev.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw)
}
func eventFloor(t *testing.T, o *LifecycleOwner, scope string) uint64 {
	t.Helper()
	var floor uint64
	if err := o.database.db.QueryRowContext(t.Context(), "SELECT floor FROM tc_event_streams WHERE scope=?", scope).Scan(&floor); err != nil {
		t.Fatal(err)
	}
	return floor
}
func TestEventRetention(t *testing.T) {
	t.Run("rows-exact-and-plus-one", func(t *testing.T) {
		o, _ := eventOwner(t)
		box := fmt.Sprintf("%032x", 2)
		eventSeed(t, o, box, 4096, 0, nil)
		cap := eventScope(t, o, "box", box)
		var epoch string
		o.database.db.QueryRowContext(t.Context(), "SELECT epoch FROM tc_event_streams WHERE scope=?", box).Scan(&epoch)
		cursor := eventCursor(o.database.events, box, epoch, eventFixtureBase)
		if _, err := o.Replay(t.Context(), cap, cursor, 0); err != nil {
			t.Fatal("floor F rejected", err)
		}
		if err := eventAppendBox(t, o, box); err != nil {
			t.Fatal(err)
		}
		if eventFloor(t, o, box) != eventFixtureBase+1 {
			t.Fatal("row overflow did not evict one prefix")
		}
		if _, err := o.Replay(t.Context(), cap, cursor, 0); err != ev.ErrReconcileRequired {
			t.Fatal("F-1 replayed", err)
		}
		if _, err := o.Replay(t.Context(), cap, eventCursor(o.database.events, box, epoch, eventFixtureBase+1), 0); err != nil {
			t.Fatal("new F rejected", err)
		}
	})
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("stream-bytes-plus-%d", extra), func(t *testing.T) {
			o, _ := eventOwner(t)
			box := fmt.Sprintf("%032x", 2)
			size := eventAppendSize(t, o, box)
			eventSeed(t, o, box, 0, ev.MaxStreamBytes-size+extra, nil)
			if err := eventAppendBox(t, o, box); err != nil {
				t.Fatal(err)
			}
			want := eventFixtureBase
			if extra == 1 {
				want++
			}
			if eventFloor(t, o, box) != want {
				t.Fatal("byte boundary", extra, eventFloor(t, o, box), want)
			}
		})
	}
	t.Run("box-allocation", func(t *testing.T) {
		o, _ := eventOwner(t)
		for i := 1; i <= 64; i++ {
			if _, err := o.AcquireEventObservation(t.Context(), fmt.Sprintf("%032x", i)); err != nil {
				t.Fatal(err)
			}
		}
		before := eventRows(t, o)
		if _, err := o.AcquireEventObservation(t.Context(), fmt.Sprintf("%032x", 65)); err != ev.ErrCapacityExceeded {
			t.Fatal("65th box", err)
		}
		if eventRows(t, o) != before {
			t.Fatal("allocation failure appended")
		}
	})
	t.Run("age-pruning-and-hole", func(t *testing.T) {
		o, _ := eventOwner(t)
		box := fmt.Sprintf("%032x", 2)
		old := time.Now().UTC().Add(-25 * time.Hour).Format(ev.TimestampLayout)
		eventSeed(t, o, box, 2, 0, &old)
		cap := eventScope(t, o, "box", box)
		if _, err := o.Replay(t.Context(), cap, "", 0); err != nil {
			t.Fatal(err)
		}
		if eventFloor(t, o, box) != eventFixtureBase+2 {
			t.Fatal("maintenance did not prune age")
		}
	})
}
func TestEventRetentionMixedPressure(t *testing.T) {
	for _, dimension := range []string{"rows", "bytes"} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s-plus-%d", dimension, extra), func(t *testing.T) {
				o, _ := eventOwner(t)
				newBox := fmt.Sprintf("%032x", 9)
				var existingRows, existingBytes int
				o.database.db.QueryRowContext(t.Context(), "SELECT COALESCE(sum(retained_rows),0),COALESCE(sum(retained_bytes),0) FROM tc_event_streams").Scan(&existingRows, &existingBytes)
				remaining := ev.MaxOwnerRows - existingRows - 1 + extra
				if dimension == "bytes" {
					size := eventAppendSize(t, o, newBox) - 36
					remaining = ev.MaxOwnerBytes - existingBytes - size + extra
				}
				for i := 1; i <= 8; i++ {
					part := remaining / (9 - i)
					remaining -= part
					if dimension == "rows" {
						eventSeed(t, o, fmt.Sprintf("%032x", i), part, 0, nil)
					} else {
						eventSeed(t, o, fmt.Sprintf("%032x", i), 0, part, nil)
					}
				}
				var largest string
				if err := o.database.db.QueryRowContext(t.Context(), "SELECT scope FROM tc_event_streams WHERE retained_rows>0 ORDER BY retained_bytes DESC,epoch LIMIT 1").Scan(&largest); err != nil {
					t.Fatal(err)
				}
				if err := eventAppendBox(t, o, newBox); err != nil {
					t.Fatal(err)
				}
				want := eventFixtureBase
				if extra == 1 {
					want++
				}
				if got := eventFloor(t, o, largest); got != want {
					t.Fatalf("largest charged stream: floor %d want %d", got, want)
				}
				var rows, bytes int
				o.database.db.QueryRowContext(t.Context(), "SELECT sum(retained_rows),sum(retained_bytes) FROM tc_event_streams").Scan(&rows, &bytes)
				if rows > ev.MaxOwnerRows || bytes > ev.MaxOwnerBytes {
					t.Fatal("owner caps relaxed")
				}
			})
		}
	}
}
