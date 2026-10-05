//go:build linux

package contentfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

func TestBlobRetainedVerificationAfterLowerImportLimit(t *testing.T) {
	s, root := newStore(t)
	f := fixture(t, 8193)
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	options, err := content.NewTAFOptions(content.MinTAFBytes, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	id := content.NewBlobID(f.BlobDigest())
	t.Run("availability", func(t *testing.T) {
		if err := s.Verify(context.Background(), id, f.CompleteBytes()); err != nil {
			t.Fatalf("healthy retained blob became unavailable after lowering import limit: %v", err)
		}
	})
	t.Run("range", func(t *testing.T) {
		sink := &rangeSink{}
		if err := s.ReadRange(context.Background(), id, f.CompleteBytes(), requested(t, 4096, 32), sink); err != nil {
			t.Fatalf("healthy retained range rejected: %v", err)
		}
		if !bytes.Equal(sink.Bytes(), allBytes(t, f)[4096:4128]) {
			t.Fatal("range bytes changed")
		}
	})
	t.Run("import-admission", func(t *testing.T) {
		source := f.Open()
		defer source.Close()
		if _, err := s.Publish(context.Background(), id, f.CompleteBytes(), source, content.FiniteTAFSource); err != ErrInvalid {
			t.Fatalf("lower import admission limit was bypassed: %v", err)
		}
		if countStages(t, root) != 0 {
			t.Fatal("rejected import allocated a stage")
		}
	})
	t.Run("healthy-quarantine-refusal", func(t *testing.T) {
		if err := s.Quarantine(context.Background(), id, f.CompleteBytes()); err != ErrInvalid {
			t.Fatalf("healthy retained blob quarantine was not refused: %v", err)
		}
		got, err := os.ReadFile(blobPath(root, f))
		if err != nil || !bytes.Equal(got, allBytes(t, f)) {
			t.Fatalf("healthy retained bytes not preserved: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(root, "quarantine"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("healthy blob moved to quarantine: entries=%d err=%v", len(entries), err)
		}
	})
	t.Run("actual-corruption", func(t *testing.T) {
		raw := allBytes(t, f)
		raw[len(raw)-1] ^= 1
		if err := os.WriteFile(blobPath(root, f), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := s.Verify(context.Background(), id, f.CompleteBytes()); err != ErrCorrupt {
			t.Fatalf("actual corruption was not detected: %v", err)
		}
		sink := &rangeSink{}
		if err := s.ReadRange(context.Background(), id, f.CompleteBytes(), requested(t, 4096, 32), sink); err != ErrCorrupt || sink.Len() != 0 {
			t.Fatalf("corrupt range emitted bytes: bytes=%d err=%v", sink.Len(), err)
		}
		if err := s.Quarantine(context.Background(), id, f.CompleteBytes()); err != nil {
			t.Fatalf("actual corruption could not be quarantined: %v", err)
		}
		if err := s.Verify(context.Background(), id, f.CompleteBytes()); err != ErrMissing {
			t.Fatalf("quarantined blob did not become missing: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(root, "quarantine"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("quarantine retention: entries=%d err=%v", len(entries), err)
		}
		got, err := os.ReadFile(filepath.Join(root, "quarantine", entries[0].Name()))
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("corrupt bytes not retained: %v", err)
		}
	})
}
