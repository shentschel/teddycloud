//go:build !windows

package sqlite

import (
	"context"
	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventRecoveryProcess(t *testing.T) {
	for _, phase := range []string{"uncommitted", "durable-response-lost"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.sqlite")
			helper := startRestartHelperFor(t, path, "TestEventRecoveryProcessHelper", "TC_EVENT_RECOVERY_PHASE="+phase)
			if err := helper.waitUntilWriteIsPending(); err != nil {
				_ = helper.terminate()
				t.Fatal(err, helper.output())
			}
			if err := helper.terminate(); err != nil {
				t.Fatal(err, helper.output())
			}
			o, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close(context.Background())
			var contents, events int
			if err = o.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_catalog_content").Scan(&contents); err != nil {
				t.Fatal(err)
			}
			if err = o.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_event_journal WHERE json_extract(CAST(envelope AS TEXT),'$.type')='catalog.changed'").Scan(&events); err != nil {
				t.Fatal(err)
			}
			want := 0
			if phase == "durable-response-lost" {
				want = 1
			}
			if contents != want || events != want {
				t.Fatal("state/journal mismatch after real process kill", contents, events, want)
			}
		})
	}
}
func TestEventRecoveryProcessHelper(t *testing.T) {
	phase := os.Getenv("TC_EVENT_RECOVERY_PHASE")
	if os.Getenv(restartHelperEnvironment) != "1" || phase == "" {
		return
	}
	path := os.Getenv("TEDDYCLOUD_SQLITE_RESTART_PATH")
	o, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = o.NewEventHub(t.Context(), eventTenant); err != nil {
		t.Fatal(err)
	}
	park := func() {
		ready := os.NewFile(3, "event-recovery-ready")
		if ready == nil {
			t.Fatal("missing readiness pipe")
		}
		defer ready.Close()
		if _, err := ready.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
		t.Fatal("helper was not terminated")
	}
	err = o.WithinTransaction(t.Context(), func(repository app.ContentRepository) error {
		if err := repository.Save(t.Context(), testContent(t, "Synthetic crash witness", false)); err != nil {
			return err
		}
		if phase == "uncommitted" {
			r := repository.(contentRepository)
			value, ok := eventTransactions.Load(r.transaction)
			if !ok {
				t.Fatal("missing collector")
			}
			if err := value.(*eventCollector).flush(t.Context(), r.transaction); err != nil {
				return err
			}
			park()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if phase != "durable-response-lost" {
		t.Fatal("unknown recovery phase")
	}
	park()
}
