//go:build linux

package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

// Delay the real selected session either before SQL admission or after all
// inserts and immediately before commit. The watchdog makes regressions bounded.
type importDeadlineOwner struct {
	*LifecycleOwner
	t        *testing.T
	phase    string
	deadline time.Time
}

func (o *importDeadlineOwner) WithinContentOperation(ctx context.Context, f func(context.Context, contentstore.Session) error) error {
	o.deadline, _ = ctx.Deadline()
	return o.LifecycleOwner.WithinContentOperation(ctx, func(ctx context.Context, session contentstore.Session) error {
		return f(ctx, &importDeadlineSession{Session: session, owner: o})
	})
}

type importDeadlineSession struct {
	contentstore.Session
	owner        *importDeadlineOwner
	transactions int
}

func (s *importDeadlineSession) WithinTransaction(ctx context.Context, f func(contentstore.BlobRepository) error) error {
	deadline, ok := ctx.Deadline()
	if s.owner.phase != "" && (!ok || !deadline.Equal(s.owner.deadline)) {
		s.owner.t.Fatal("DB phase did not inherit the import deadline")
	}
	// Receipt preflight is SQL phase one; keep these failure seams after
	// durable publication, in phase two.
	s.transactions++
	if s.transactions == 2 && s.owner.phase == "during-db" {
		s.Session.(*contentSession).beforeCommit = func() error {
			return awaitImportDeadline(s.owner.t, ctx)
		}
	}
	if s.transactions == 2 && s.owner.phase == "before-db" {
		if err := awaitImportDeadline(s.owner.t, ctx); err != nil {
			return err
		}
	}
	return s.Session.WithinTransaction(ctx, f)
}

func awaitImportDeadline(t *testing.T, ctx context.Context) error {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		// Return nil to exercise the real transaction's cancellation/rollback
		// rather than manufacturing a failure at the test seam.
		return nil
	case <-timer.C:
		t.Error("configured import deadline did not reach DB phase")
		return contentstore.ErrUnavailable
	}
}

type importDeadlineMedia struct {
	contentstore.MediaStore
	t         *testing.T
	owner     *importDeadlineOwner
	published bool
}

func (m *importDeadlineMedia) Publish(ctx context.Context, id content.BlobID, size uint64, source content.TAFSource, mode content.TAFSourceMode) (content.TAFEnvelope, error) {
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(m.owner.deadline) {
		m.t.Fatal("publication did not inherit the import deadline")
	}
	envelope, err := m.MediaStore.Publish(ctx, id, size, source, mode)
	m.published = err == nil
	return envelope, err
}

func TestImportConfiguredDeadlineThroughCommit(t *testing.T) {
	for _, phase := range []string{"before-db", "during-db"} {
		for _, earlierCaller := range []bool{false, true} {
			name := phase + "/configured"
			if earlierCaller {
				name = phase + "/earlier-caller"
			}
			t.Run(name, func(t *testing.T) {
				const duration = time.Second
				options, err := content.NewTAFOptions(content.DefaultTAFBytes, duration)
				if err != nil {
					t.Fatal(err)
				}
				owner, store, _, root, fixture := mediaServiceWithOptions(t, options)
				point := &importDeadlineOwner{LifecycleOwner: owner, t: t, phase: phase}
				media := &importDeadlineMedia{MediaStore: store, t: t, owner: point}
				service, err := contentstore.NewService(t.Context(), &attachedOwner{point}, media)
				if err != nil {
					t.Fatal(err)
				}
				command, key := blobCommand(t, 1, false), blobKey(t, 1)
				ctx := t.Context()
				var callerDeadline time.Time
				if earlierCaller {
					callerDeadline = time.Now().Add(500 * time.Millisecond)
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, callerDeadline)
					defer cancel()
				}
				start := time.Now()
				result, err := service.Import(ctx, key, command, fixture.Open(), content.FiniteTAFSource)
				if !errors.Is(err, context.DeadlineExceeded) || result != (contentstore.ImportResult{}) {
					t.Fatalf("expired import acknowledged: %+v %v", result, err)
				}
				if !media.published {
					t.Fatal("test did not reach DB phase after durable publication")
				}
				if earlierCaller {
					if !point.deadline.Equal(callerDeadline) {
						t.Fatal("earlier caller deadline was extended")
					}
				} else if point.deadline.Before(start) || point.deadline.After(start.Add(duration+100*time.Millisecond)) {
					t.Fatal("configured duration did not bound owner admission")
				}
				assertBlobCounts(t, owner, 0, 0, 0, 0)
				point.phase = ""
				if _, found, err := service.LookupImport(t.Context(), key, command); err != nil || found {
					t.Fatalf("expired import left receipt: %v %v", found, err)
				}
				entries := mediaInventory(t, service)
				if len(entries) != 1 || entries[0].Kind != contentstore.UnreferencedCanonical {
					t.Fatalf("expected recoverable orphan: %+v", entries)
				}
				digest := command.BlobID().String()[7:]
				path := filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf")
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				// Retry gets a fresh import deadline and reuses the real orphan.
				point.phase = ""
				result, err = service.Import(t.Context(), key, command, fixture.Open(), content.FiniteTAFSource)
				if err != nil || result != importResult(command) {
					t.Fatalf("exact retry: %+v %v", result, err)
				}
				after, err := os.Stat(path)
				if err != nil || !os.SameFile(before, after) {
					t.Fatal("retry replaced orphan", err)
				}
				assertBlobCounts(t, owner, 1, 1, 1, 1)
				if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
