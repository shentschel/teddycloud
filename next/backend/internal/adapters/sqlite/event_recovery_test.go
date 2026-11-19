package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"path/filepath"
	"testing"
)

func TestEventRecoveryCommitUncertainty(t *testing.T) {
	o := blobOwner(t)
	if _, err := o.NewEventHub(t.Context(), eventTenant); err != nil {
		t.Fatal(err)
	}
	command := blobCommand(t, 1, false)
	key := blobKey(t, 1)
	before := eventRows(t, o)
	err := o.WithinContentOperation(t.Context(), func(ctx context.Context, session contentstore.Session) error {
		session.(*contentSession).commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("lost private commit response")
		}
		return session.WithinTransaction(ctx, func(r contentstore.BlobRepository) error { _, err := r.RecordImport(ctx, key, command); return err })
	})
	if !errors.Is(err, contentstore.ErrCommitUncertain) {
		t.Fatal("owner uncertainty lost", err)
	}
	if eventRows(t, o) != before+1 {
		t.Fatal("confirmed owner write and journal separated")
	}
	result, found, err := blobLookup(t, o, key, command)
	if err != nil || !found || result != importResult(command) {
		t.Fatal("retained owner readback", err)
	}
	if _, err = blobRecord(t, o, key, command); err != nil || eventRows(t, o) != before+1 {
		t.Fatal("uncertain command replay duplicated event", err)
	}
}
func TestEventRecoveryUpgradeBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v10.sqlite")
	db, err := Open(t.Context(), Config{Path: path}, SchemaMigrations()[:10])
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if rejected, err := Open(t.Context(), Config{Path: path}, SchemaMigrations()); !errors.Is(err, ErrUpgradeBackupRequired) {
		if rejected != nil {
			rejected.Close()
		}
		t.Fatal("unbacked upgrade admitted", err)
	}
	o, err := OpenLifecycleOwner(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(dir, "pre-event.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close(context.Background())
	snapshot, found, err := o.UpgradeBackup(t.Context())
	if err != nil || !found || snapshot.SchemaVersion != 10 {
		t.Fatal("verified pre-event backup missing", err)
	}
	if err = VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	if eventRows(t, o) != 0 {
		t.Fatal("migration fabricated historical events")
	}
	if _, err = o.NewEventHub(t.Context(), eventTenant); err != nil {
		t.Fatal(err)
	}
}
func TestEventRecoveryRejectsCorruption(t *testing.T) {
	for _, damage := range []string{"framing", "counter", "observation"} {
		t.Run(damage, func(t *testing.T) {
			o, _ := eventOwner(t)
			path := o.config.Path
			switch damage {
			case "framing":
				catalogSchemaExec(t, o.database.db, "UPDATE tc_event_journal SET envelope=x'7b7d'")
			case "counter":
				catalogSchemaExec(t, o.database.db, "UPDATE tc_event_streams SET high_water=high_water+1")
			case "observation":
				box := "22222222222222222222222222222222"
				lease, err := o.AcquireEventObservation(t.Context(), box)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = o.ObserveEvent(t.Context(), lease, 1, ev.ObservationInput{Type: ev.RemovalObserved, Payload: ev.RemovalPayload{}}); err != nil {
					t.Fatal(err)
				}
				catalogSchemaExec(t, o.database.db, "UPDATE tc_event_observations SET last_input=x'7b7d'")
			}
			if err := o.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
			if err == nil {
				reopened.Close(context.Background())
				t.Fatal("corrupt journal/owner state admitted")
			}
		})
	}
}

type eventMaintenanceMedia struct {
	contentstore.MediaStore
	work func() error
}

func (*eventMaintenanceMedia) InvalidateInventory() error { return nil }
func (m *eventMaintenanceMedia) Quarantine(context.Context, content.BlobID, uint64) error {
	return m.work()
}
func TestEventStorageInvalidation(t *testing.T) {
	for _, phase := range []string{"intent-failure", "completion-success", "completion-failure"} {
		t.Run(phase, func(t *testing.T) {
			o, _ := eventOwner(t)
			calls := 0
			media := &eventMaintenanceMedia{}
			media.work = func() error {
				calls++
				var n int
				if err := o.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_event_journal WHERE json_extract(CAST(envelope AS TEXT),'$.payload.reason')='quarantine'").Scan(&n); err != nil || n != 1 {
					t.Fatal("intent missing before filesystem operation", err, n)
				}
				if phase == "completion-failure" {
					catalogSchemaExec(t, o.database.db, "CREATE TRIGGER event_completion_failure BEFORE INSERT ON tc_event_journal BEGIN SELECT RAISE(ABORT,'private completion failure'); END")
				}
				return nil
			}
			service, err := contentstore.NewService(t.Context(), o, media)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "intent-failure" {
				catalogSchemaExec(t, o.database.db, "CREATE TRIGGER event_intent_failure BEFORE INSERT ON tc_event_journal BEGIN SELECT RAISE(ABORT,'private intent failure'); END")
			}
			err = service.Quarantine(t.Context(), content.NewBlobID([32]byte{1}), 4097)
			if phase == "intent-failure" {
				if err == nil || calls != 0 {
					t.Fatal("mutation admitted before durable intent", err, calls)
				}
				return
			}
			if err != nil || calls != 1 {
				t.Fatal("completion rewrote confirmed PI-07 outcome", err, calls)
			}
			if phase == "completion-failure" {
				if !o.database.events.fenced.Load() {
					t.Fatal("failed completion did not fence freshness")
				}
			} else {
				var n int
				o.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_event_journal WHERE json_extract(CAST(envelope AS TEXT),'$.payload.reason')='quarantine'").Scan(&n)
				if n != 2 {
					t.Fatal("completion invalidation missing", n)
				}
			}
		})
	}
}
