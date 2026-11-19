package sqlite

import (
	"context"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEventRestoreEpoch(t *testing.T) {
	o, h := eventOwner(t)
	cap := eventScope(t, o, "control", "")
	oldCursor := eventBarrier(t, o, cap)
	subscription, err := h.Subscribe(t.Context(), cap, oldCursor)
	if err != nil {
		t.Fatal(err)
	}
	if err = o.WithinTransaction(t.Context(), func(r catalogapp.ContentRepository) error {
		return r.Save(t.Context(), testContent(t, "Restore domain witness", false))
	}); err != nil {
		t.Fatal(err)
	}
	before := catalogSchemaRows(t, o.database.db, "SELECT * FROM tc_catalog_content ORDER BY content_id")
	snapshot, err := o.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "backup.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err = o.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err = subscription.Next(t.Context()); err != ev.ErrRevoked {
		t.Fatal("restore did not revoke old subscription", err)
	}
	if !reflect.DeepEqual(before, catalogSchemaRows(t, o.database.db, "SELECT * FROM tc_catalog_content ORDER BY content_id")) {
		t.Fatal("restore changed domain data")
	}
	nextCap := eventScope(t, o, "control", "")
	if _, err = o.Replay(t.Context(), nextCap, oldCursor, 16); err != ev.ErrInvalidCursor {
		t.Fatal("restore resurrected cursor MAC", err)
	}
	if oldCursor == eventBarrier(t, o, nextCap) {
		t.Fatal("restore reused replay identity")
	}
	t.Run("pre-event-restore", func(t *testing.T) {
		source, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v10.sqlite")}, SchemaMigrations()[:10])
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close(context.Background())
		old, err := source.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "v10-backup.sqlite"), SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		if err = o.Restore(t.Context(), old, filepath.Join(t.TempDir(), "restored-v10.sqlite")); err != nil {
			t.Fatal(err)
		}
		if _, err = o.NewEventHub(t.Context(), eventTenant); err != ev.ErrUnsupported {
			t.Fatal("pre-event restore autoactivated", err)
		}
		v, err := o.CurrentVersion(t.Context())
		if err != nil || v != 10 {
			t.Fatal("restore auto-upgraded", v, err)
		}
	})
}
func TestEventRecoveryNormalReopen(t *testing.T) {
	o, _ := eventOwner(t)
	cap := eventScope(t, o, "control", "")
	cursor := eventBarrier(t, o, cap)
	box := strings.Repeat("2", 32)
	lease, err := o.AcquireEventObservation(t.Context(), box)
	if err != nil {
		t.Fatal(err)
	}
	input := ev.ObservationInput{Type: ev.PlacementObserved, Payload: ev.PlacementPayload{ProtocolValid: "unknown"}}
	if _, err = o.ObserveEvent(t.Context(), lease, 1, input); err != nil {
		t.Fatal(err)
	}
	path := o.config.Path
	if err = o.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if _, err = reopened.NewEventHub(t.Context(), eventTenant); err != nil {
		t.Fatal(err)
	}
	fresh := eventScope(t, reopened, "control", "")
	page, err := reopened.Replay(t.Context(), fresh, cursor, 16)
	if err != nil || len(page.Records) == 0 {
		t.Fatal("ordinary reopen lost cursor", err)
	}
	report, err := reopened.EventObservationSnapshot(t.Context(), eventScope(t, reopened, "box", box))
	if err != nil || report.Placement == nil || report.PlacementFresh {
		t.Fatal("restart invented removal or fresh report", err)
	}
	if _, err = reopened.ObserveEvent(t.Context(), lease, 2, input); err != ev.ErrRevoked {
		t.Fatal("restart preserved ingress lease", err)
	}
}

func TestEventRevocation(t *testing.T) {
	o, h := eventOwner(t)
	box := strings.Repeat("2", 32)
	lease, err := o.AcquireEventObservation(t.Context(), box)
	if err != nil {
		t.Fatal(err)
	}
	cap := eventScope(t, o, "box", box)
	cursor := eventBarrier(t, o, cap)
	sub, err := h.Subscribe(t.Context(), cap, cursor)
	if err != nil {
		t.Fatal(err)
	}
	before := eventRows(t, o)
	if err = o.RevokeEventReadCapabilities(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = sub.Next(t.Context()); err != ev.ErrRevoked {
		t.Fatal("authorization change retained subscription", err)
	}
	next := eventScope(t, o, "box", box)
	if _, err = o.Replay(t.Context(), next, cursor, 16); err != ev.ErrReconcileRequired {
		t.Fatal("old authorization cursor remained valid", err)
	}
	if eventRows(t, o) != before {
		t.Fatal("read revocation invented an owner fact")
	}
	// Read grants and producer leases are independent authorities.
	if _, err = o.ObserveEvent(t.Context(), lease, 1, ev.ObservationInput{Type: ev.PlacementObserved, Payload: ev.PlacementPayload{ProtocolValid: "unknown"}}); err != nil {
		t.Fatal("read revocation replaced the producer", err)
	}
	report, err := o.EventObservationSnapshot(t.Context(), next)
	if err != nil || !report.PlacementFresh {
		t.Fatal("producer continuity changed", err)
	}
}

func TestEventLifecycleContextBounds(t *testing.T) {
	o, _ := eventOwner(t)
	if _, err := o.EventReadCapability(nil, "control", "", time.Time{}); err != ev.ErrInvalidEvent {
		t.Fatal("nil context", err)
	}
	err := o.WithinContentOperation(t.Context(), func(ctx context.Context, _ contentstore.Session) error {
		_, err := o.EventReadCapability(ctx, "control", "", time.Time{})
		if err != ev.ErrUnavailable {
			t.Fatal("recursive event admission was not denied", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
