package sqlite

import (
	"context"
	"errors"
	"github.com/shentschel/teddycloud/next/backend/internal/adapters/contentfs"
	assignmentapp "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	tagapp "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	assignmentdomain "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	catalogdomain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	tag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestEventOwnerCoverage(t *testing.T) {
	t.Run("tag", func(t *testing.T) {
		o, _ := eventOwner(t)
		cap := eventScope(t, o, "control", "")
		cursor := eventBarrier(t, o, cap)
		v := testRegistryTag(t, '1', tag.UID{})
		if err := o.WithinTagTransaction(t.Context(), func(r tagapp.TagRepository) error { return r.Insert(t.Context(), v) }); err != nil {
			t.Fatal(err)
		}
		page, err := o.Replay(t.Context(), cap, cursor, 16)
		if err != nil || len(page.Records) != 1 {
			t.Fatal(err, len(page.Records))
		}
		e, err := ev.Decode(page.Records[0].Bytes)
		if err != nil || e.Type != ev.TagChanged || e.Source.Owner != ev.TagRegistry || e.Aggregate.ID != v.ID().String() {
			t.Fatal("wrong authoritative fact", err)
		}
	})
	t.Run("unmanaged", func(t *testing.T) {
		o, _ := eventOwner(t)
		cap := eventScope(t, o, "control", "")
		cursor := eventBarrier(t, o, cap)
		v := testContent(t, "Synthetic event fixture", false)
		if err := o.WithinTransaction(t.Context(), func(r catalogapp.ContentRepository) error { return r.Save(t.Context(), v) }); err != nil {
			t.Fatal(err)
		}
		n := eventRows(t, o)
		if err := o.WithinTransaction(t.Context(), func(r catalogapp.ContentRepository) error { return r.Save(t.Context(), v) }); err != nil {
			t.Fatal(err)
		}
		if eventRows(t, o) != n {
			t.Fatal("exact Save replay appended")
		}
		page, err := o.Replay(t.Context(), cap, cursor, 16)
		if err != nil || len(page.Records) != 1 {
			t.Fatal(err, len(page.Records))
		}
	})
}

// Every fixture authors owner commands through its actual application/adapter
// boundary. Deliberate SQL fixtures only arrange pre-existing domain state.
func eventCheckProducer(t *testing.T, o *LifecycleOwner, typ ev.Type, change string, run func() error) {
	t.Helper()
	if _, err := o.NewEventHub(t.Context(), eventTenant); err != nil {
		t.Fatal(err)
	}
	cap := eventScope(t, o, "control", "")
	cursor := eventBarrier(t, o, cap)
	if err := run(); err != nil {
		t.Fatal("owner mutation", err)
	}
	page, err := o.Replay(t.Context(), cap, cursor, 16)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, record := range page.Records {
		e, err := ev.Decode(record.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if e.Type != typ {
			continue
		}
		actual := ""
		switch p := e.Payload.(type) {
		case ev.TagPayload:
			actual = p.Change
		case ev.ContentPayload:
			actual = p.Change
		case ev.CatalogPayload:
			actual = p.Change
		case ev.AssignmentPayload:
			actual = p.Change
		case ev.InvalidationPayload:
			actual = p.Reason
		}
		if actual == change {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("wanted one %s/%s, got %d in %d events", typ, change, found, len(page.Records))
	}
	before := eventRows(t, o)
	if err := run(); err != nil {
		t.Fatal("exact owner replay", err)
	}
	if eventRows(t, o) != before {
		t.Fatal("exact replay emitted a new fact")
	}
}
func TestEventIdempotency(t *testing.T) {
	t.Run("tag-metadata", func(t *testing.T) {
		o, _ := eventOwner(t)
		s := tagapp.New(o)
		value := registerMetadataTag(t, s)
		observation := tagMetadataObservation(t, value, 1, tag.FactKey(0), "true")
		command := tagapp.MetadataCommand{TagID: value.ID(), Change: tag.MetadataChange{ExpectedRevision: value.Revision(), Key: 0, Observations: []evidence.Observation{observation}}}
		eventCheckProducer(t, o, ev.TagChanged, "metadata", func() error { _, err := s.UpdateMetadata(t.Context(), command); return err })
	})
	t.Run("content-reference", func(t *testing.T) {
		o := blobOwner(t)
		command := blobCommand(t, 1, false)
		eventCheckProducer(t, o, ev.ContentChanged, "bound", func() error { _, err := blobRecord(t, o, blobKey(t, 1), command); return err })
	})
	t.Run("adoption", func(t *testing.T) {
		f, s, command := adoptionFixture(t)
		eventCheckProducer(t, f.owner, ev.CatalogChanged, "adoption", func() error { _, err := s.AdoptBaseline(t.Context(), command); return err })
	})
	t.Run("review", func(t *testing.T) {
		f, op, next, result := reviewWriteFixture(t, false)
		eventCheckProducer(t, f.owner, ev.CatalogChanged, "review", func() error { return commitReviewFixture(t, f, op, next, result) })
	})
	t.Run("order", func(t *testing.T) {
		f, _, op, next, result := orderWriteFixture(t)
		eventCheckProducer(t, f.owner, ev.CatalogChanged, "order", func() error { return commitOrderFixture(t, f, op, next, result) })
	})
	t.Run("registration", func(t *testing.T) {
		f := newRegistrationFixture(t)
		eventCheckProducer(t, f.owner, ev.CatalogChanged, "registration", func() error { _, err := f.service.RegisterContent(t.Context(), f.command); return err })
	})
	t.Run("supersession", func(t *testing.T) {
		f := newSupersessionFixture(t)
		eventCheckProducer(t, f.owner, ev.CatalogChanged, "supersession", func() error { _, err := f.sup.SupersedeVersion(t.Context(), f.command); return err })
	})
	t.Run("import", func(t *testing.T) {
		o, _, _, s := importFixture(t)
		report := generatedImport(t)
		batch, _ := catalogdomain.NewBatchID()
		eventCheckProducer(t, o, ev.CatalogInvalidated, "import", func() error { _, err := s.Import(t.Context(), batch, report); return err })
	})
	t.Run("assignment", func(t *testing.T) {
		f := newAssignmentRepositoryFixture(t)
		o, s, _ := assignmentActivateOwner(t, f)
		command := f.command(t, 1, assignmentapp.Assign, 0)
		command.EffectiveAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
		eventCheckProducer(t, o, ev.AssignmentChanged, "assign", func() error { _, err := s.Execute(t.Context(), command); return err })
	})
}

func TestEventOwnerCoverageRegistrationVersion(t *testing.T) {
	f := newRegistrationFixture(t)
	if _, err := f.service.RegisterContent(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	version := f.version(t, 101, 1)
	eventCheckProducer(t, f.owner, ev.CatalogChanged, "registration", func() error { _, err := f.service.RegisterCatalogVersion(t.Context(), version); return err })
}
func TestEventOwnerCoverageSourceRegistration(t *testing.T) {
	f := newRegistrationFixture(t)
	eventCheckProducer(t, f.owner, ev.CatalogInvalidated, "source-registration", func() error { _, err := f.service.RegisterContent(t.Context(), f.command); return err })
}
func TestEventOwnerCoverageLiveDelivery(t *testing.T) {
	o, h := eventOwner(t)
	cap := eventScope(t, o, "control", "")
	s, err := h.Subscribe(t.Context(), cap, eventBarrier(t, o, cap))
	if err != nil {
		t.Fatal(err)
	}
	v := eventTag(t, 1)
	if err = o.WithinTagTransaction(t.Context(), func(r tagapp.TagRepository) error { return r.Insert(t.Context(), v) }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	r, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e, err := ev.Decode(r.Bytes)
	if err != nil || e.Type != ev.TagChanged || e.Aggregate.ID != v.ID().String() {
		t.Fatal("uncommitted or wrong delivery", err)
	}
	d, err := o.EventDiagnostics(t.Context())
	if err != nil || d.Appends < 2 {
		t.Fatal("append diagnostics", d, err)
	}
}

func TestEventOwnerCoverageAssignmentKinds(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	o, s, _ := assignmentActivateOwner(t, f)
	first := f.command(t, 1, assignmentapp.RecordClassification, 0)
	second := f.command(t, 2, assignmentapp.RecordClassification, 1)
	second.Classification = assignmentdomain.CustomCard
	resolution := f.command(t, 3, assignmentapp.ResolveClassification, 2)
	resolution.Support = []assignmentdomain.EvidenceID{first.Evidence.ID()}
	baseline := f.command(t, 4, assignmentapp.RecordOriginalTarget, 3)
	contrary := f.command(t, 5, assignmentapp.RecordOriginalTarget, 4)
	contrary.Target = f.other
	resolve := f.command(t, 6, assignmentapp.ResolveOriginalTarget, 5)
	resolve.Support = []assignmentdomain.EvidenceID{baseline.Evidence.ID()}
	commands := []assignmentapp.Command{first, second, resolution, baseline, contrary, resolve, f.command(t, 7, assignmentapp.Assign, 6), f.command(t, 8, assignmentapp.Unassign, 7), f.command(t, 9, assignmentapp.RetireAssignment, 8)}
	for i, command := range commands {
		change := "classification"
		if i >= 3 && i <= 5 {
			change = "original-target"
		}
		if i == 6 {
			change = "assign"
		}
		if i == 7 {
			change = "unassign"
		}
		if i == 8 {
			change = "retire"
		}
		eventCheckProducer(t, o, ev.AssignmentChanged, change, func() error { _, err := s.Execute(t.Context(), command); return err })
	}
	var playback int
	if err := o.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_event_journal WHERE json_extract(CAST(envelope AS TEXT),'$.type')='playback.observed'").Scan(&playback); err != nil || playback != 0 {
		t.Fatal("desired assignment asserted applied playback", err)
	}
}
func TestEventOwnerCoverageCatalogMedia(t *testing.T) {
	f := newCatalogStateFixture(t, false)
	base := blobCommand(t, 1, false)
	command, err := content.NewImportCommand(f.versions[0].Version, base.BlobID(), base.CompleteBytes(), base.Profile())
	if err != nil {
		t.Fatal(err)
	}
	// The metadata port assumes prior verification; actual descriptor publication
	// and repair are independently exercised by TestEventOwnerCoverageMediaRepair.
	eventCheckProducer(t, f.owner, ev.CatalogChanged, "media", func() error { _, err := blobRecord(t, f.owner, blobKey(t, 51), command); return err })
}
func TestEventOwnerCoverageMediaRepair(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native ext4/xfs media profile is Linux-only")
	}
	o := blobOwner(t)
	if _, err := o.NewEventHub(t.Context(), eventTenant); err != nil {
		t.Fatal(err)
	}
	parent := os.Getenv("TEDDYCLOUD_CONTENTFS_TEST_ROOT")
	if parent == "" {
		parent = "/var/tmp"
	}
	root, err := os.MkdirTemp(parent, "pi11-event-media-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	store, err := contentfs.Open(t.Context(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, err := contentstore.NewService(t.Context(), o, store)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := taffixture.New(8193, 123, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	command := blobCommand(t, 1, false)
	key := blobKey(t, 1)
	cap := eventScope(t, o, "control", "")
	cursor := eventBarrier(t, o, cap)
	result, err := service.Import(t.Context(), key, command, generated.Open(), content.FiniteTAFSource)
	if err != nil || result != importResult(command) {
		t.Fatal("real publication", err)
	}
	page, err := o.Replay(t.Context(), cap, cursor, 16)
	if err != nil || len(page.Records) != 3 {
		t.Fatal("intent/binding/completion", err, len(page.Records))
	}
	for i, want := range []ev.Type{ev.StorageInvalidated, ev.ContentChanged, ev.StorageInvalidated} {
		e, err := ev.Decode(page.Records[i].Bytes)
		if err != nil || e.Type != want {
			t.Fatal("filesystem invalidation order", i, err)
		}
	}
	before := eventRows(t, o)
	if err = service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil || eventRows(t, o) != before {
		t.Fatal("availability read appended", err)
	}
	// The existing owner permits quarantine only for freshly verified corrupt
	// bytes. Damage this test's generated file before authorizing that operation.
	digest := command.BlobID().String()[7:]
	file, err := os.OpenFile(filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sample [1]byte
	if _, err = file.ReadAt(sample[:], 4096); err != nil {
		file.Close()
		t.Fatal(err)
	}
	sample[0] ^= 0xff
	if _, err = file.WriteAt(sample[:], 4096); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Sync(); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = service.Quarantine(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	if err = service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrMissing) {
		t.Fatal("quarantine result", err)
	}
	before = eventRows(t, o)
	repaired, err := service.Import(t.Context(), key, command, generated.Open(), content.FiniteTAFSource)
	if err != nil || repaired != result || eventRows(t, o) != before+2 {
		t.Fatal("retained receipt repair or explicit invalidations", err)
	}
	if err = service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
		t.Fatal("repaired canonical bytes", err)
	}
}
