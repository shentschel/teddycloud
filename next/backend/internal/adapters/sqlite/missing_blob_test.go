//go:build linux

package sqlite

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/adapters/contentfs"
	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

type missingBlobSink struct {
	bytes.Buffer
	closed bool
}

func (s *missingBlobSink) Write(_ context.Context, p []byte) (int, error) {
	return s.Buffer.Write(p)
}

func (s *missingBlobSink) Close() error {
	s.closed = true
	return nil
}

func fixtureBytes(t *testing.T, fixture taffixture.Fixture) []byte {
	t.Helper()
	source := fixture.Open()
	defer source.Close()
	var result bytes.Buffer
	buffer := make([]byte, 8192)
	for {
		n, err := source.Read(t.Context(), buffer)
		result.Write(buffer[:n])
		if err == io.EOF {
			return result.Bytes()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func fixtureBlobPath(root string, id content.BlobID) string {
	digest := id.String()[7:]
	return filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf")
}

func loadCatalogContent(t *testing.T, owner *LifecycleOwner, id catalog.Content) catalog.Content {
	t.Helper()
	var got catalog.Content
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		var found bool
		var err error
		got, found, err = repository.FindByID(t.Context(), id.ID())
		if err == nil && !found {
			return errors.New("catalog content missing")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func assertMissingBlobFacts(t *testing.T, owner *LifecycleOwner, service *contentstore.Service, key content.ImportKey, command content.ImportCommand, want contentstore.ImportResult, protected catalog.Content) {
	t.Helper()
	if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrMissing) {
		t.Fatalf("availability error = %v, want typed missing", err)
	}
	got, found, err := service.LookupImport(t.Context(), key, command)
	if err != nil || !found || got != want {
		t.Fatalf("committed import changed: got=%+v found=%v err=%v", got, found, err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
	if gotContent := loadCatalogContent(t, owner, protected); !reflect.DeepEqual(gotContent, protected) {
		t.Fatalf("protected catalog facts changed: got=%+v want=%+v", gotContent, protected)
	}
}

func TestMissingBlobReimport(t *testing.T) {
	owner, store, service, root, fixture := mediaService(t)
	protected := testContent(t, "Protected synthetic fixture", true)
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), protected)
	}); err != nil {
		t.Fatal(err)
	}
	command, key := blobCommand(t, 1, true), blobKey(t, 1)
	want, err := service.Import(t.Context(), key, command, fixture.Open(), content.FiniteTAFSource)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "with-media-reference.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	path := fixtureBlobPath(root, command.BlobID())
	if err := os.Remove(path); err != nil { // Test-only access to this generated fixture.
		t.Fatal(err)
	}
	assertMissingBlobFacts(t, owner, service, key, command, want, protected)
	rangeRequest, err := contentfs.NewByteRange(4096, 32)
	if err != nil {
		t.Fatal(err)
	}
	sink := &missingBlobSink{}
	if err := store.ReadRange(t.Context(), command.BlobID(), command.CompleteBytes(), rangeRequest, sink); !errors.Is(err, contentstore.ErrMissing) || sink.Len() != 0 || !sink.closed {
		t.Fatalf("missing range: bytes=%d closed=%v err=%v", sink.Len(), sink.closed, err)
	}
	if err := owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored-reference.sqlite")); err != nil {
		t.Fatal(err)
	}
	assertMissingBlobFacts(t, owner, service, key, command, want, protected)
	repaired, err := service.Import(t.Context(), key, command, fixture.Open(), content.FiniteTAFSource)
	if err != nil || repaired != want {
		t.Fatalf("exact repair changed identity: got=%+v err=%v", repaired, err)
	}
	if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
		t.Fatalf("repaired availability: %v", err)
	}
	sink = &missingBlobSink{}
	if err := store.ReadRange(t.Context(), command.BlobID(), command.CompleteBytes(), rangeRequest, sink); err != nil {
		t.Fatal(err)
	}
	wantBytes := fixtureBytes(t, fixture)[4096:4128]
	if !bytes.Equal(sink.Bytes(), wantBytes) || !sink.closed {
		t.Fatalf("repaired range changed: bytes=%d closed=%v", sink.Len(), sink.closed)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
	if gotContent := loadCatalogContent(t, owner, protected); !reflect.DeepEqual(gotContent, protected) {
		t.Fatalf("repair changed protected catalog facts: got=%+v want=%+v", gotContent, protected)
	}
}

func TestMissingBlobErrorTaxonomy(t *testing.T) {
	t.Run("invalid-typed-identifier", func(t *testing.T) {
		_, store, _, _, fixture := mediaService(t)
		rangeRequest, err := contentfs.NewByteRange(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		sink := &missingBlobSink{}
		if err := store.ReadRange(t.Context(), content.BlobID{}, fixture.CompleteBytes(), rangeRequest, sink); !errors.Is(err, contentstore.ErrInvalidInput) || sink.Len() != 0 {
			t.Fatalf("invalid identifier: bytes=%d err=%v", sink.Len(), err)
		}
	})
	t.Run("digest-mismatch-corruption", func(t *testing.T) {
		owner, store, service, root, fixture := mediaService(t)
		command, key := blobCommand(t, 1, false), blobKey(t, 1)
		want, err := service.Import(t.Context(), key, command, fixture.Open(), content.FiniteTAFSource)
		if err != nil {
			t.Fatal(err)
		}
		path := fixtureBlobPath(root, command.BlobID())
		raw := fixtureBytes(t, fixture)
		raw[len(raw)-1] ^= 1
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrCorrupt) {
			t.Fatalf("corrupt availability: %v", err)
		}
		rangeRequest, _ := contentfs.NewByteRange(4096, 32)
		sink := &missingBlobSink{}
		if err := store.ReadRange(t.Context(), command.BlobID(), command.CompleteBytes(), rangeRequest, sink); !errors.Is(err, contentstore.ErrCorrupt) || sink.Len() != 0 {
			t.Fatalf("corrupt range: bytes=%d err=%v", sink.Len(), err)
		}
		got, found, err := service.LookupImport(t.Context(), key, command)
		if err != nil || !found || got != want {
			t.Fatalf("corruption changed DB facts: %+v %v %v", got, found, err)
		}
		assertBlobCounts(t, owner, 1, 1, 1, 1)
	})
	t.Run("missing-catalog-record", func(t *testing.T) {
		owner, _, service, _, fixture := mediaService(t)
		if _, err := owner.database.db.ExecContext(t.Context(), `DELETE FROM tc_catalog_content`); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Import(t.Context(), blobKey(t, 1), blobCommand(t, 1, false), fixture.Open(), content.FiniteTAFSource); !errors.Is(err, contentstore.ErrContentNotFound) {
			t.Fatalf("missing catalog record: %v", err)
		}
		assertBlobCounts(t, owner, 0, 0, 0, 0)
	})
	t.Run("broken-database", func(t *testing.T) {
		owner, _, service, _, fixture := mediaService(t)
		command, key := blobCommand(t, 1, false), blobKey(t, 1)
		if _, err := service.Import(t.Context(), key, command, fixture.Open(), content.FiniteTAFSource); err != nil {
			t.Fatal(err)
		}
		if err := owner.database.db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.LookupImport(t.Context(), key, command); !errors.Is(err, contentstore.ErrUnavailable) {
			t.Fatalf("broken database: %v", err)
		}
	})
}
