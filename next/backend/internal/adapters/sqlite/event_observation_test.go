package sqlite

import (
	"bytes"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"strings"
	"testing"
)

func TestEventObservationContinuity(t *testing.T) {
	o, _ := eventOwner(t)
	box := strings.Repeat("2", 32)
	lease, err := o.AcquireEventObservation(t.Context(), box)
	if err != nil {
		t.Fatal(err)
	}
	cap := eventScope(t, o, "box", box)
	cursor := eventBarrier(t, o, cap)
	input := ev.ObservationInput{Type: ev.PlacementObserved, Payload: ev.PlacementPayload{ProtocolValid: "unknown"}}
	raw, err := o.ObserveEvent(t.Context(), lease, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	n := eventRows(t, o)
	replay, err := o.ObserveEvent(t.Context(), lease, 1, input)
	if err != nil || !bytes.Equal(raw, replay) || eventRows(t, o) != n {
		t.Fatal("last input replay", err)
	}
	changed := ev.ObservationInput{Type: ev.RemovalObserved, Payload: ev.RemovalPayload{}}
	if _, err := o.ObserveEvent(t.Context(), lease, 1, changed); err != ev.ErrObservationConflict {
		t.Fatal("changed duplicate", err)
	}
	if _, err := o.ObserveEvent(t.Context(), lease, 2, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ObserveEvent(t.Context(), lease, 1, input); err != ev.ErrStaleObservation {
		t.Fatal("older observation", err)
	}
	page, err := o.Replay(t.Context(), cap, cursor, 16)
	if err != nil || len(page.Records) != 2 {
		t.Fatal("report stream", err, len(page.Records))
	}
	if _, err := o.ObserveEvent(t.Context(), lease, 4, input); err != ev.ErrSourceGap {
		t.Fatal("gap", err)
	}
	snapshot, err := o.EventObservationSnapshot(t.Context(), cap)
	if err != nil || snapshot.Placement == nil || snapshot.PlacementFresh {
		t.Fatal("gap invented removal or fresh state", err)
	}
	if _, err := o.ObserveEvent(t.Context(), lease, 3, input); err != ev.ErrRevoked {
		t.Fatal("late report after gap", err)
	}
	next, err := o.AcquireEventObservation(t.Context(), box)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.ObserveEvent(t.Context(), next, 1, input); err != nil {
		t.Fatal(err)
	}
	snapshot, err = o.EventObservationSnapshot(t.Context(), cap)
	if err != nil || !snapshot.PlacementFresh {
		t.Fatal("new lease freshness", err)
	}
	other, err := o.AcquireEventObservation(t.Context(), strings.Repeat("3", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.ObserveEvent(t.Context(), other, 1, input); err != nil {
		t.Fatal(err)
	}
	var tags int
	if err = o.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_tags").Scan(&tags); err != nil || tags != 0 {
		t.Fatal("placement allocated an identity")
	}
	if _, err = o.Replay(t.Context(), eventScope(t, o, "box", strings.Repeat("3", 32)), page.Records[0].Cursor, 16); err != ev.ErrInvalidCursor {
		t.Fatal("cross-box cursor", err)
	}
}
