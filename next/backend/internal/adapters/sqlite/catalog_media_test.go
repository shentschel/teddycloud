//go:build linux

package sqlite

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/adapters/contentfs"
	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

func catalogMediaFixture(t *testing.T) (catalogStateFixture, *contentfs.Store, *contentstore.Service, string, content.ImportCommand, taffixture.Fixture) {
	t.Helper()
	f := newCatalogStateFixture(t, false)
	generated, err := taffixture.New(8193, 123, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	base := blobCommand(t, 1, false)
	version, err := domain.NewContentVersion(f.versions[0].Version.ID(), f.subject.ContentID(), base.Version().Fingerprint(), base.Version().OrderEvidence())
	if err != nil {
		t.Fatal(err)
	}
	f.versions[0].Version = version
	hash, err := hex.DecodeString(version.Fingerprint().Hash().String())
	if err != nil {
		t.Fatal(err)
	}
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_content_versions SET audio_sha1=? WHERE version_id=?", hash, version.ID().String())
	command, err := content.NewImportCommand(version, base.BlobID(), base.CompleteBytes(), base.Profile())
	if err != nil {
		t.Fatal(err)
	}
	parent := os.Getenv("TEDDYCLOUD_CONTENTFS_TEST_ROOT")
	if parent == "" {
		parent = "/var/tmp"
	}
	root, err := os.MkdirTemp(parent, "tc-catalog-media-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	store, err := contentfs.Open(t.Context(), root, content.DefaultTAFOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	service, err := contentstore.NewService(t.Context(), f.owner, store)
	if err != nil {
		t.Fatal(err)
	}
	return f, store, service, root, command, generated
}

func catalogMediaRead(t *testing.T, f catalogStateFixture, command content.ImportCommand, state app.MediaState) app.CatalogRecord {
	t.Helper()
	got, err := catalogRead(t, f.owner, app.Query{Kind: app.GetVersion, Content: command.Version().ContentID(), Version: command.Version().ID(), Page: app.PageRequest{Limit: 1}})
	if err != nil || got.Kind != app.Found || !got.Complete || len(got.Entries) != 1 {
		t.Fatalf("catalog version: %+v %v", got, err)
	}
	entry := got.Entries[0]
	if entry.Version != command.Version() || entry.Media != state {
		t.Fatalf("immutable version/media: %+v, want state %v", entry, state)
	}
	return entry
}

func TestCatalogMediaAttachment(t *testing.T) {
	f, _, service, root, command, generated := catalogMediaFixture(t)
	before := catalogSchemaRows(t, f.owner.database.db, "SELECT * FROM tc_content_versions ORDER BY version_id")
	catalogMediaRead(t, f, command, app.CatalogOnly)
	assertBlobCounts(t, f.owner, 0, 2, 0, 0)
	key := blobKey(t, 71)
	want, err := service.Import(t.Context(), key, command, generated.Open(), content.FiniteTAFSource)
	if err != nil || want != importResult(command) {
		t.Fatalf("attach verified media to existing stable ID: %+v %v", want, err)
	}
	if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
		t.Fatal("fresh envelope verification", err)
	}
	assertBlobCounts(t, f.owner, 1, 2, 1, 1)
	catalogMediaRead(t, f, command, app.MediaBoundUnchecked)
	if !reflect.DeepEqual(before, catalogSchemaRows(t, f.owner.database.db, "SELECT * FROM tc_content_versions ORDER BY version_id")) {
		t.Fatal("attachment rewrote catalog-only version facts")
	}

	// SQL binding state is intentionally independent from fresh byte availability.
	path := fixtureBlobPath(root, command.BlobID())
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteAt([]byte{0}, 4096)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrCorrupt) || errors.Is(err, contentstore.ErrMissing) || errors.Is(err, contentstore.ErrUnavailable) {
		t.Fatalf("corrupt bytes taxonomy: %v", err)
	}
	catalogMediaRead(t, f, command, app.MediaBoundUnchecked)
	if err := service.Quarantine(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
		t.Fatal(err)
	}
	if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrMissing) || errors.Is(err, contentstore.ErrCorrupt) || errors.Is(err, contentstore.ErrUnavailable) {
		t.Fatalf("missing bytes taxonomy: %v", err)
	}
	catalogMediaRead(t, f, command, app.MediaBoundUnchecked)
	// A closed operation owner supplies a deterministic unavailable boundary,
	// independently of the missing bytes. Catalog-only was asserted before import.
	if err := f.owner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrUnavailable) || errors.Is(err, contentstore.ErrMissing) || errors.Is(err, contentstore.ErrCorrupt) {
		t.Fatalf("unavailable owner taxonomy: %v", err)
	}
}

func TestCatalogMediaAttachmentRejectChangedTuple(t *testing.T) {
	for _, field := range []string{"content", "audio", "hash", "order"} {
		t.Run(field, func(t *testing.T) {
			f, _, _, _, original, _ := catalogMediaFixture(t)
			version := original.Version()
			contentID, fingerprint, order := version.ContentID(), version.Fingerprint(), version.OrderEvidence()
			var err error
			switch field {
			case "content":
				contentID = catalogQueryContent(t, 2)
				catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_content VALUES (?,'Other',NULL,NULL)", contentID.String())
			case "audio":
				audio, e := domain.NewAudioID(456)
				if e != nil {
					t.Fatal(e)
				}
				fingerprint, err = domain.NewAudioFingerprint(audio, fingerprint.Hash())
			case "hash":
				fingerprint = f.versions[1].Version.Fingerprint()
			case "order":
				order, err = domain.NewVersionOrderEvidence("changed-order", 9)
			}
			if err != nil {
				t.Fatal(err)
			}
			changed, err := domain.NewContentVersion(version.ID(), contentID, fingerprint, order)
			if err != nil {
				t.Fatal(err)
			}
			command, err := content.NewImportCommand(changed, original.BlobID(), original.CompleteBytes(), original.Profile())
			if err != nil {
				t.Fatal(err)
			}
			before := catalogV4Rows(t, f.owner.database.db)
			if _, err := blobRecord(t, f.owner, blobKey(t, 72), command); !errors.Is(err, contentstore.ErrConflict) {
				t.Fatalf("changed %s admitted: %v", field, err)
			}
			if !reflect.DeepEqual(before, catalogV4Rows(t, f.owner.database.db)) {
				t.Fatal("rejected tuple persisted partial data")
			}
			catalogMediaRead(t, f, original, app.CatalogOnly)
		})
	}
}

func TestCatalogReceiptAfterCorrection(t *testing.T) {
	f, _, service, _, command, generated := catalogMediaFixture(t)
	versions := []domain.ContentVersion{f.versions[0].Version, f.versions[1].Version}
	order, err := domain.NewCatalogOrderState(f.subject.ContentID(), versions)
	if err != nil {
		t.Fatal(err)
	}
	f.storeOrder(t, order, 1)
	review, err := domain.NewCatalogReviewState(f.subject, false)
	if err != nil {
		t.Fatal(err)
	}
	f.storeReview(t, review, 1)
	key := blobKey(t, 73)
	want, err := service.Import(t.Context(), key, command, generated.Open(), content.FiniteTAFSource)
	if err != nil {
		t.Fatal(err)
	}
	canonical := command.CanonicalBytes()
	receipts := catalogSchemaRows(t, f.owner.database.db, "SELECT * FROM tc_blob_imports ORDER BY import_key")
	bindings := catalogSchemaRows(t, f.owner.database.db, "SELECT * FROM tc_version_blobs ORDER BY version_id")
	facts := catalogSchemaRows(t, f.owner.database.db, "SELECT * FROM tc_content_versions ORDER BY version_id")
	id, err := assignment.ParseID(catalogSchemaID("asn", 1))
	if err != nil {
		t.Fatal(err)
	}
	tagID, err := identity.ParseTagID(catalogSchemaID("tag", 1))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	interval, err := assignment.NewInterval(at, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := assignment.New(id, 1, tagID, command.Version().ID(), interval)
	if err != nil {
		t.Fatal(err)
	}
	history, err := assignment.NewHistory([]assignment.Assignment{assigned})
	if err != nil {
		t.Fatal(err)
	}
	assignmentBefore := history.Entries()
	claim, err := domain.TitleClaim("Explicit corrected title")
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := evidence.ParseID(catalogSchemaID("obs", 81))
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.NewCatalogObservation(f.ingest, f.source, domain.ObservationInput{ID: observationID, Subject: f.subject, Claim: claim, Coordinate: f.coordinate(t), ObservedAt: at, Confidence: evidence.Confidence(1)})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.CatalogDecisionKind{domain.RecordObservations, domain.AcceptSupport} {
		commandID, err := domain.NewCatalogCommandID()
		if err != nil {
			t.Fatal(err)
		}
		cmd := domain.CatalogReviewCommand{ID: commandID, Kind: kind, Subject: f.subject, Source: f.source, Expected: review.Revision(), Reason: "explicit title correction"}
		if kind == domain.RecordObservations {
			cmd.Observations = []domain.CatalogObservation{observation}
		} else {
			cmd.Key, cmd.Support = domain.TitleClaimKey, []evidence.ID{observationID}
		}
		next, decision, err := review.Apply(f.review, cmd)
		if err != nil {
			t.Fatal(err)
		}
		revision, err := domain.NewCatalogRevision(next.Revision().Uint64())
		if err != nil {
			t.Fatal(err)
		}
		if err := commitReviewFixture(t, f, app.ReviewOperation{ExpectedAggregate: review.Revision(), Capability: f.review, Command: cmd}, next, app.ReviewResult{AggregateRevision: revision, Decision: decision}); err != nil {
			t.Fatal(err)
		}
		review = next
	}
	sequenceID, err := evidence.ParseID(catalogSchemaID("obs", 82))
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := domain.NewCatalogSequenceEvidence(f.ingest, f.namespace, domain.CatalogSequenceInput{ID: sequenceID, Content: f.subject.ContentID(), Version: command.Version().ID(), Position: 7, Coordinate: f.coordinate(t)})
	if err != nil {
		t.Fatal(err)
	}
	orderID, err := domain.NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	orderCommand := domain.CatalogOrderCommand{ID: orderID, Content: f.subject.ContentID(), Namespace: f.namespace, Expected: order.Revision(), Reason: "explicit later order correction", Evidence: []domain.CatalogSequenceEvidence{sequence}, Entries: []domain.CatalogOrderEntry{{Version: command.Version().ID(), Position: 7, Support: []evidence.ID{sequenceID}}}}
	nextOrder, decision, err := order.Apply(f.order, orderCommand)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := domain.NewCatalogRevision(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitOrderFixture(t, f, app.OrderOperation{ExpectedAggregate: review.Revision(), Capability: f.order, Command: orderCommand}, nextOrder, app.OrderResult{AggregateRevision: aggregate, Decision: decision}); err != nil {
		t.Fatal(err)
	}
	entry := catalogMediaRead(t, f, command, app.MediaBoundUnchecked)
	if title, ok := entry.Title.Value(); !ok || title.Text() != claim.Text() {
		t.Fatal("correction was not projected", entry.Title)
	}
	if err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		stored, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
		if err != nil || !found || stored.AggregateRevision != aggregate {
			t.Fatalf("corrected order readback: %+v %v %v", stored, found, err)
		}
		gotBytes, err := stored.State.PersistenceEncoding()
		if err != nil {
			return err
		}
		wantBytes, err := nextOrder.PersistenceEncoding()
		if err != nil {
			return err
		}
		view := stored.State.EffectiveOrder()
		if !bytes.Equal(gotBytes, wantBytes) || len(view.Positions) != 1 || view.Positions[0].Entry.Version != command.Version().ID() || view.Positions[0].Entry.Position != 7 {
			t.Fatal("later effective order not retained independently of immutable tuple", view)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := service.LookupImport(t.Context(), key, command)
	if err != nil || !found || got != want {
		t.Fatalf("original receipt after corrections: %+v %v %v", got, found, err)
	}
	got, err = service.Import(t.Context(), key, command, generated.Open(), content.FiniteTAFSource)
	if err != nil || got != want || !bytes.Equal(canonical, command.CanonicalBytes()) {
		t.Fatalf("original command replay: %+v %v", got, err)
	}
	for table, before := range map[string][][]any{"tc_blob_imports": receipts, "tc_version_blobs": bindings, "tc_content_versions": facts} {
		if !reflect.DeepEqual(before, catalogSchemaRows(t, f.owner.database.db, "SELECT * FROM "+table+" ORDER BY 1")) {
			t.Fatalf("correction/replay rewrote %s", table)
		}
	}
	if !reflect.DeepEqual(history.Entries(), assignmentBefore) {
		t.Fatal("domain assignment history changed")
	}
	current, ok := history.CurrentAt(at)
	if !ok || current != assigned || current.ContentVersionID() != entry.Version.ID() {
		t.Fatal("stable domain assignment no longer resolves to original version")
	}
	assertBlobCounts(t, f.owner, 1, 2, 1, 1)
}

func TestCatalogMediaAttachmentAtomicityAndBrokenReferences(t *testing.T) {
	t.Run("receipt failure rolls back attachment", func(t *testing.T) {
		f, _, _, _, command, _ := catalogMediaFixture(t)
		before := catalogV4Rows(t, f.owner.database.db)
		catalogSchemaExec(t, f.owner.database.db, "CREATE TEMP TRIGGER reject_catalog_attachment BEFORE INSERT ON tc_blob_imports BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END")
		if _, err := blobRecord(t, f.owner, blobKey(t, 74), command); !errors.Is(err, contentstore.ErrUnavailable) {
			t.Fatalf("receipt insertion failure: %v", err)
		}
		if !reflect.DeepEqual(before, catalogV4Rows(t, f.owner.database.db)) {
			t.Fatal("failed receipt left a blob, binding or changed version")
		}
		catalogMediaRead(t, f, command, app.CatalogOnly)
	})
	for _, failure := range []string{"receipt without binding", "binding without blob"} {
		t.Run(failure, func(t *testing.T) {
			f, _, _, _, command, _ := catalogMediaFixture(t)
			key := blobKey(t, 75)
			if _, err := blobRecord(t, f.owner, key, command); err != nil {
				t.Fatal(err)
			}
			// Deliberately damaged generated fixture, never a production repair.
			catalogSchemaExec(t, f.owner.database.db, "PRAGMA foreign_keys=OFF")
			if failure == "receipt without binding" {
				catalogSchemaExec(t, f.owner.database.db, "DELETE FROM tc_version_blobs")
			} else {
				catalogSchemaExec(t, f.owner.database.db, "DELETE FROM tc_blobs")
			}
			catalogSchemaExec(t, f.owner.database.db, "PRAGMA foreign_keys=ON")
			before := catalogV4Rows(t, f.owner.database.db)
			for _, candidate := range []content.ImportKey{key, blobKey(t, 76)} {
				if _, err := blobRecord(t, f.owner, candidate, command); !errors.Is(err, contentstore.ErrCorrupt) {
					t.Fatalf("broken reference repaired as catalog-only: %v", err)
				}
			}
			if !reflect.DeepEqual(before, catalogV4Rows(t, f.owner.database.db)) {
				t.Fatal("corrupt references were rewritten")
			}
		})
	}
}
