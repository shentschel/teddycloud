//go:build linux

package contentfs

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/adapters/sqlite"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

// This entry point is executed only by the SQLite recovery tests, in a separate
// test executable. Hooks park the process without replacing any syscall result.
// The parent must observe the exact boundary and reap a SIGKILL before reopening.
func TestContentRecoveryProcessHelper(t *testing.T) {
	phase := os.Getenv("TC_RECOVERY_PHASE")
	if phase == "" {
		return
	}
	ctx := t.Context()
	store, err := OpenForInventory(ctx, os.Getenv("TC_RECOVERY_ROOT"), content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := sqlite.OpenLifecycleOwner(ctx, sqlite.Config{Path: os.Getenv("TC_RECOVERY_DB")}, sqlite.SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	media := &recoveryMedia{Store: store, t: t, phase: phase}
	service, err := contentstore.NewService(ctx, owner, media)
	if err != nil {
		t.Fatal(err)
	}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages == 100 {
			t.Fatal("helper inventory did not finish within bound")
		}
		page, err := service.Inventory(ctx, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if page.Complete {
			break
		}
		cursor = page.Cursor
	}
	f, err := taffixture.New(8193, 123, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if phase == "publish-before-commit" {
		id, err := identity.ParseContentID("cnt_0123456789abcdefghjkmnpqrs")
		if err != nil {
			t.Fatal(err)
		}
		versionID, err := identity.ParseContentVersionID("ver_00000000000000000000000001")
		if err != nil {
			t.Fatal(err)
		}
		audio, err := catalog.NewAudioID(123)
		if err != nil {
			t.Fatal(err)
		}
		h := f.PayloadSHA1()
		hash, err := catalog.ParseAudioHash(hex.EncodeToString(h[:]))
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := catalog.NewAudioFingerprint(audio, hash)
		if err != nil {
			t.Fatal(err)
		}
		version, err := catalog.NewContentVersion(versionID, id, fingerprint, catalog.UnknownVersionOrderEvidence())
		if err != nil {
			t.Fatal(err)
		}
		command, err := content.NewImportCommand(version, content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), content.TAFProfileV1)
		if err != nil {
			t.Fatal(err)
		}
		key, err := content.ParseImportKey("imp_00000000000000000000000001")
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Import(ctx, key, command, f.Open(), content.FiniteTAFSource)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		// Installed after startup probes and reconciliation. A source-sync entry
		// proves rename returned successfully; a destination-sync entry proves
		// the actual source fsync returned successfully. No fake success is used.
		store.ops.fault = func(point string) error {
			at := phase == "before-rename" && point == "renameat2" ||
				(phase == "after-rename" || phase == "before-source-sync") && point == "quarantine-source-sync" ||
				(phase == "after-source-sync" || phase == "before-destination-sync") && point == "quarantine-dest-sync"
			if at {
				parkRecoveryProcess(t, phase)
			}
			return nil
		}
		if err := service.Quarantine(ctx, content.NewBlobID(f.BlobDigest()), f.CompleteBytes()); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("requested kill boundary was not reached")
}

type recoveryMedia struct {
	*Store
	t     *testing.T
	phase string
}

func (m *recoveryMedia) Publish(ctx context.Context, id content.BlobID, size uint64, source content.TAFSource, mode content.TAFSourceMode) (content.TAFEnvelope, error) {
	envelope, err := m.Store.Publish(ctx, id, size, source, mode)
	if err == nil && m.phase == "publish-before-commit" {
		// Publish has completed the actual file and both directory fsyncs. The
		// service has not yet entered its DB transaction and still holds the gate.
		parkRecoveryProcess(m.t, m.phase)
	}
	return envelope, err
}

func (m *recoveryMedia) Quarantine(ctx context.Context, id content.BlobID, size uint64) error {
	err := m.Store.Quarantine(ctx, id, size)
	if err == nil && m.phase == "after-destination-sync" {
		// Both actual fsyncs succeeded and descriptor identity was rechecked;
		// the application lifecycle gate remains held until this method returns.
		parkRecoveryProcess(m.t, m.phase)
	}
	return err
}

func parkRecoveryProcess(t *testing.T, phase string) {
	t.Helper()
	ready := os.NewFile(3, "recovery-ready")
	park := os.NewFile(4, "recovery-park")
	if ready == nil || park == nil {
		t.Fatal("missing parent pipes")
	}
	if _, err := fmt.Fprintln(ready, phase); err != nil {
		t.Fatal(err)
	}
	var byte [1]byte
	_, err := park.Read(byte[:])
	t.Fatalf("helper resumed instead of receiving SIGKILL: %v", err)
}
