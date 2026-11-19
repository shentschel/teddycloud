package sqlite

import (
	"context"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const eventTenant = "11111111111111111111111111111111"

func eventOwner(t *testing.T) (*LifecycleOwner, *ev.Service) {
	t.Helper()
	o, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "events.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	h, err := o.NewEventHub(t.Context(), eventTenant)
	if err != nil {
		t.Fatal(err)
	}
	return o, h
}
func eventScope(t *testing.T, o *LifecycleOwner, kind, box string) ev.ReadCapability {
	t.Helper()
	c, err := o.EventReadCapability(t.Context(), kind, box, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func eventBarrier(t *testing.T, o *LifecycleOwner, c ev.ReadCapability) string {
	t.Helper()
	p, err := o.Replay(t.Context(), c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return p.Barrier
}
func eventRows(t *testing.T, o *LifecycleOwner) int {
	t.Helper()
	var n int
	if err := o.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_event_journal").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestEventSchema(t *testing.T) {
	m := SchemaMigrations()
	if len(m) != 11 || m[10].ID != "0011-event-journal" {
		t.Fatal("migration not appended")
	}
	// Old migration identities/checksums are checked against the base in the
	// upgrade tests; a fresh schema introduces no fabricated historical facts.
	o, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "fresh.sqlite")}, m)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close(context.Background())
	if eventRows(t, o) != 0 {
		t.Fatal("fabricated history before explicit composition")
	}
	if _, err := o.NewEventHub(t.Context(), strings.Repeat("0", 32)); err == nil {
		t.Fatal("zero tenant")
	}
	if _, err := o.NewEventHub(t.Context(), eventTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := o.NewEventHub(t.Context(), strings.Repeat("2", 32)); err == nil {
		t.Fatal("second tenant")
	}
	if eventRows(t, o) != 1 {
		t.Fatal("initial storage restart invalidation")
	}
}
