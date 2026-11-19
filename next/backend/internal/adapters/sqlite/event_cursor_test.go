package sqlite

import (
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"strings"
	"testing"
	"time"
)

func TestEventCursor(t *testing.T) {
	o, _ := eventOwner(t)
	cap := eventScope(t, o, "control", "")
	token := eventBarrier(t, o, cap)
	if len(token) != 136 {
		t.Fatal("cursor wire length", len(token))
	}
	for _, bad := range []string{token + "=", token[:len(token)-1] + "?", strings.Repeat("x", 513), "ec2." + token[4:]} {
		if _, err := o.Replay(t.Context(), cap, bad, 16); err != ev.ErrInvalidCursor {
			t.Fatal("malformed cursor", err)
		}
	}
	var epoch string
	var high uint64
	if err := o.database.db.QueryRowContext(t.Context(), "SELECT epoch,high_water FROM tc_event_streams WHERE scope='control'").Scan(&epoch, &high); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Replay(t.Context(), cap, eventCursor(o.database.events, "control", epoch, high+1), 16); err != ev.ErrReconcileRequired {
		t.Fatal("future cursor", err)
	}
	if _, err := o.Replay(t.Context(), cap, eventCursor(o.database.events, "control", strings.Repeat("f", 32), high), 16); err != ev.ErrReconcileRequired {
		t.Fatal("retired epoch", err)
	}
	foreign, _ := ev.NewAuthority(eventTenant, strings.Repeat("f", 32))
	denied, _ := foreign.IssueRead("control", "", time.Time{})
	if _, err := o.Replay(t.Context(), denied, token, 16); err != ev.ErrRevoked {
		t.Fatal("foreign authority", err)
	}
	for _, generation := range []uint64{0, 1 << 63, o.database.events.generation + 1} {
		state := eventState{tenant: o.database.events.tenant, key: o.database.events.key}
		state.generation = generation
		_, err := o.Replay(t.Context(), cap, eventCursor(&state, "control", epoch, high), 16)
		want := ev.ErrInvalidCursor
		if generation == o.database.events.generation+1 {
			want = ev.ErrReconcileRequired
		}
		if err != want {
			t.Fatal("generation", generation, err)
		}
	}
}
