//go:build linux

package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/adapters/contentfs"
	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

// Build an adapter-private test helper rather than exposing syscall injection
// in production ports or violating the application-to-adapter boundary.
func recoveryExecutable(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate helper source")
	}
	executable := filepath.Join(t.TempDir(), "content-recovery-helper")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-o", executable, "../contentfs")
	cmd.Dir = filepath.Dir(file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build recovery helper: %v\n%s", err, output)
	}
	return executable
}

func killRecoveryProcess(t *testing.T, executable, root, database, phase string) {
	t.Helper()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	parkRead, parkWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer parkRead.Close()
	defer parkWrite.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestContentRecoveryProcessHelper$", "-test.timeout=25s")
	cmd.Env = append(os.Environ(), "TC_RECOVERY_ROOT="+root, "TC_RECOVERY_DB="+database, "TC_RECOVERY_PHASE="+phase)
	cmd.ExtraFiles = []*os.File{readyWrite, parkRead}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = readyWrite.Close()
	_ = parkRead.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	type notification struct {
		point string
		err   error
	}
	ready := make(chan notification, 1)
	go func() {
		point, err := bufio.NewReader(readyRead).ReadString('\n')
		ready <- notification{strings.TrimSpace(point), err}
	}()
	select {
	case notice := <-ready:
		if notice.err != nil || notice.point != phase {
			_ = cmd.Process.Kill()
			err := <-done
			reaped = true
			t.Fatalf("helper did not reach %s: %+v; %v\n%s", phase, notice, err, output.String())
		}
	case err := <-done:
		reaped = true
		t.Fatalf("helper exited before %s: %v\n%s", phase, err, output.String())
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		reaped = true
		t.Fatalf("helper failed to reach %s: %v\n%s", phase, ctx.Err(), output.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = <-done
	reaped = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("helper was not killed: %v", err)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("expected reaped SIGKILL at %s, got %v\n%s", phase, status, output.String())
	}
	t.Logf("reaped real helper SIGKILL at %s", phase)
}

func recoveryFixtureRoot(t *testing.T) (string, string, taffixture.Fixture) {
	t.Helper()
	parent := os.Getenv("TEDDYCLOUD_CONTENTFS_TEST_ROOT")
	if parent == "" {
		parent = "/var/tmp"
	}
	root, err := os.MkdirTemp(parent, "tc-process-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	fixture, err := taffixture.New(8193, 123, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(t.TempDir(), "recovery.sqlite"), fixture
}

func reopenRecovery(t *testing.T, root, path string) (*LifecycleOwner, *contentfs.Store, *contentstore.Service) {
	t.Helper()
	store, err := contentfs.OpenForInventory(t.Context(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatalf("real filesystem reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatalf("real database reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := owner.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	service, err := contentstore.NewService(t.Context(), owner, store)
	if err != nil {
		t.Fatal(err)
	}
	return owner, store, service
}

func boundedRecoveryInventory(t *testing.T, service *contentstore.Service) []contentstore.InventoryEntry {
	t.Helper()
	var entries []contentstore.InventoryEntry
	cursor := ""
	for pages := 0; pages < 100; pages++ {
		ctx, cancel := context.WithTimeout(t.Context(), contentfs.InventoryDuration)
		page, err := service.Inventory(ctx, cursor, 1)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > 1 || page.Inspected > contentfs.MaxInventoryInspected || len(page.Cursor) > contentfs.MaxInventoryCursor {
			t.Fatalf("inventory exceeded contract: %+v", page)
		}
		entries = append(entries, page.Entries...)
		if page.Complete {
			if page.Cursor != "" {
				t.Fatal("complete inventory retained cursor")
			}
			return entries
		}
		if page.Cursor == "" {
			t.Fatal("incomplete inventory has no continuation")
		}
		cursor = page.Cursor
	}
	t.Fatal("inventory exceeded test page bound")
	return nil
}

func recoveryPath(root string, c content.ImportCommand) string {
	digest := c.BlobID().String()[7:]
	return filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf")
}

func recoveryBytes(t *testing.T, f taffixture.Fixture) []byte {
	t.Helper()
	source := f.Open()
	defer source.Close()
	var output bytes.Buffer
	buffer := make([]byte, 8192)
	for {
		n, err := source.Read(t.Context(), buffer)
		output.Write(buffer[:n])
		if err == io.EOF {
			return output.Bytes()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func assertRecoveryPlacement(t *testing.T, root string, c content.ImportCommand, canonical, quarantined []byte) {
	t.Helper()
	actual, err := os.ReadFile(recoveryPath(root, c))
	if canonical == nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canonical unexpectedly present: %v", err)
		}
	} else if err != nil || !bytes.Equal(actual, canonical) {
		t.Fatalf("canonical bytes changed: %v", err)
	}
	files, err := os.ReadDir(filepath.Join(root, "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	if quarantined != nil {
		want = 1
	}
	if len(files) != want {
		t.Fatalf("quarantine count %d, want %d", len(files), want)
	}
	if want == 1 {
		name := files[0].Name()
		if !strings.HasPrefix(name, "q-") || len(name) != 38 || !strings.HasSuffix(name, ".taf") {
			t.Fatalf("invalid quarantine name %q", name)
		}
		actual, err := os.ReadFile(filepath.Join(root, "quarantine", name))
		if err != nil || !bytes.Equal(actual, quarantined) {
			t.Fatalf("quarantined bytes changed: %v", err)
		}
	}
}

func TestImportCrashAfterPublish(t *testing.T) {
	executable := recoveryExecutable(t)
	root, path, fixture := recoveryFixtureRoot(t)
	owner, store, service := reopenRecovery(t, root, path)
	if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		return r.Save(t.Context(), testContent(t, "Synthetic recovery", false))
	}); err != nil {
		t.Fatal(err)
	}
	boundedRecoveryInventory(t, service)
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	c, key := blobCommand(t, 1, false), blobKey(t, 1)
	killRecoveryProcess(t, executable, root, path, "publish-before-commit")
	owner, store, service = reopenRecovery(t, root, path)
	entries := boundedRecoveryInventory(t, service)
	if len(entries) != 1 || entries[0].Kind != contentstore.UnreferencedCanonical || entries[0].BlobID != c.BlobID() || entries[0].Bytes != c.CompleteBytes() {
		t.Fatalf("orphan placement: %+v", entries)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
	if _, found, err := service.LookupImport(t.Context(), key, c); err != nil || found {
		t.Fatalf("uncommitted receipt: %v %v", found, err)
	}
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	assertRecoveryPlacement(t, root, c, recoveryBytes(t, fixture), nil)
	before, err := os.Stat(recoveryPath(root, c))
	if err != nil {
		t.Fatal(err)
	}
	assertExactRecoveryRetry(t, owner, service, root, c, key, fixture, 2)
	after, err := os.Stat(recoveryPath(root, c))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("retry replaced durable orphan: %v", err)
	}
	assertRecoveryPersisted(t, owner, store, root, path, c, key)
}

// A second real reopen also checks that repaired bindings/receipts and retained
// files survive closing the retry owner, rather than merely existing in memory.
func assertRecoveryPersisted(t *testing.T, owner *LifecycleOwner, store *contentfs.Store, root, path string, c content.ImportCommand, key content.ImportKey) {
	t.Helper()
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	owner, _, service := reopenRecovery(t, root, path)
	boundedRecoveryInventory(t, service)
	assertBlobCounts(t, owner, 1, 1, 1, 1)
	got, found, err := service.LookupImport(t.Context(), key, c)
	if err != nil || !found || got != importResult(c) {
		t.Fatalf("retry receipt after second reopen: %+v %v %v", got, found, err)
	}
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
}

func assertExactRecoveryRetry(t *testing.T, owner *LifecycleOwner, service *contentstore.Service, root string, c content.ImportCommand, key content.ImportKey, f taffixture.Fixture, stages int) {
	t.Helper()
	want := importResult(c)
	for attempt := 0; attempt < 2; attempt++ {
		got, err := service.Import(t.Context(), key, c, f.Open(), content.FiniteTAFSource)
		if err != nil || got != want {
			t.Fatalf("exact retry %d: %+v %v", attempt, got, err)
		}
		assertBlobCounts(t, owner, 1, 1, 1, 1)
		got, found, err := service.LookupImport(t.Context(), key, c)
		if err != nil || !found || got != want {
			t.Fatalf("exact receipt %d: %+v %v %v", attempt, got, found, err)
		}
	}
	if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	entries := boundedRecoveryInventory(t, service)
	var referenced, retainedStages int
	for _, entry := range entries {
		switch entry.Kind {
		case contentstore.ReferencedCanonical:
			if entry.BlobID != c.BlobID() || entry.Bytes != c.CompleteBytes() {
				t.Fatal("wrong binding inventory")
			}
			referenced++
		case contentstore.StagingEntry:
			retainedStages++
		case contentstore.QuarantineEntry:
		default:
			t.Fatalf("unexpected retained placement: %+v", entry)
		}
	}
	if referenced != 1 || retainedStages != stages {
		t.Fatalf("retry/reuse placement: %+v", entries)
	}
	if actual, err := os.ReadFile(recoveryPath(root, c)); err != nil || !bytes.Equal(actual, recoveryBytes(t, f)) {
		t.Fatalf("retry bytes: %v", err)
	}
}

func TestBlobQuarantineRecovery(t *testing.T) {
	executable := recoveryExecutable(t)
	for _, phase := range []string{"before-rename", "after-rename", "before-source-sync", "after-source-sync", "before-destination-sync", "after-destination-sync"} {
		t.Run(phase, func(t *testing.T) {
			root, path, fixture := recoveryFixtureRoot(t)
			owner, store, service := reopenRecovery(t, root, path)
			if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
				return r.Save(t.Context(), testContent(t, "Synthetic recovery", false))
			}); err != nil {
				t.Fatal(err)
			}
			boundedRecoveryInventory(t, service)
			c, key := blobCommand(t, 1, false), blobKey(t, 1)
			want, err := service.Import(t.Context(), key, c, fixture.Open(), content.FiniteTAFSource)
			if err != nil {
				t.Fatal(err)
			}
			assertBlobCounts(t, owner, 1, 1, 1, 1)
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			raw := recoveryBytes(t, fixture)
			raw[len(raw)-1] ^= 1
			file, err := os.OpenFile(recoveryPath(root, c), os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := file.WriteAt(raw, 0)
			syncErr, closeErr := file.Sync(), file.Close()
			if writeErr != nil || syncErr != nil || closeErr != nil {
				t.Fatal(writeErr, syncErr, closeErr)
			}
			killRecoveryProcess(t, executable, root, path, phase)
			owner, store, service = reopenRecovery(t, root, path)
			entries := boundedRecoveryInventory(t, service)
			assertBlobCounts(t, owner, 1, 1, 1, 1)
			got, found, err := service.LookupImport(t.Context(), key, c)
			if err != nil || !found || got != want {
				t.Fatalf("kill changed DB facts: %+v %v %v", got, found, err)
			}
			moved := phase != "before-rename"
			wantKind, availability := contentstore.ReferencedCanonical, contentstore.ErrCorrupt
			if moved {
				wantKind, availability = contentstore.QuarantineEntry, contentstore.ErrMissing
			}
			if len(entries) != 1 || entries[0].Kind != wantKind || entries[0].Bytes != c.CompleteBytes() {
				t.Fatalf("restart placement: %+v", entries)
			}
			if err := service.Availability(t.Context(), c.BlobID(), c.CompleteBytes()); !errors.Is(err, availability) {
				t.Fatalf("typed availability: %v, want %v", err, availability)
			}
			stages := 1
			if !moved {
				assertRecoveryPlacement(t, root, c, raw, nil)
				if _, err := service.Import(t.Context(), key, c, fixture.Open(), content.FiniteTAFSource); !errors.Is(err, contentstore.ErrCorrupt) {
					t.Fatalf("corrupt retry overwrote bytes: %v", err)
				}
				assertBlobCounts(t, owner, 1, 1, 1, 1)
				assertRecoveryPlacement(t, root, c, raw, nil)
				if err := service.Quarantine(t.Context(), c.BlobID(), c.CompleteBytes()); err != nil {
					t.Fatal(err)
				}
				stages++
			}
			assertRecoveryPlacement(t, root, c, nil, raw)
			assertExactRecoveryRetry(t, owner, service, root, c, key, fixture, stages)
			assertRecoveryPlacement(t, root, c, recoveryBytes(t, fixture), raw)
			assertRecoveryPersisted(t, owner, store, root, path, c, key)
		})
	}
}
