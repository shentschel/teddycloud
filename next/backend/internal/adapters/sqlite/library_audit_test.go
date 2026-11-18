package sqlite

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	assignment "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
	tags "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

func TestLibraryNoNPlusOne(t *testing.T) {
	f := newSupersessionFixture(t)
	for i := 0; i < 96; i++ {
		libraryUnmanaged(t, f.owner, 2000+i, 10000+i*4, 4, "independent")
	}
	p := libraryProfileFromOwner(t, f.owner)
	counts := map[bool]int{}
	aggregate, review := uint64(3), uint64(1)
	for _, annotations := range []int{1, 16, 64} {
		p.source.reset(false)
		if p.index != nil {
			p.index.reset(false)
		}
		var observations []domain.CatalogObservation
		for i := 0; i < annotations; i++ {
			observations = append(observations, f.observation(t, f.registrationFixture.command.Subject, f.registrationFixture.command.Observations[0].Input().Claim))
		}
		a, _ := domain.NewCatalogRevision(aggregate)
		r, _ := domain.NewCatalogRevision(review)
		if _, e := f.base.RecordObservations(t.Context(), catalog.ReviewCommand{ID: registrationIntegrationCommandID(t), Subject: f.registrationFixture.command.Subject, ExpectedAggregate: a, ExpectedReview: r, Observations: observations, Reason: "N+1 annotation scaling"}); e != nil {
			t.Fatal(e)
		}
		aggregate++
		review++
		libraryProfileBuild(t, p, fmt.Sprintf("annotations-%d", annotations))
		for _, q := range libraryProfileShapes() {
			for _, limit := range []uint64{1, 16, 128} {
				page := lib.Page{Limit: limit}
				result, c := libraryProfileRequest(t, p, q, page)
				if counts[false] != 0 && counts[false] != c.statements {
					t.Fatal("N+1 first", annotations, c)
				}
				counts[false] = c.statements
				if result.Next != "" {
					page.Cursor = result.Next
					page.ExpectedSnapshot = result.Snapshot
					_, c = libraryProfileRequest(t, p, q, page)
					if counts[true] != 0 && counts[true] != c.statements {
						t.Fatal("N+1 continuation", annotations, c)
					}
					counts[true] = c.statements
				}
			}
		}
	}
	t.Logf("No N+1: independent Contents=97 versions=386 annotations added=1/16/64 Limits=1/16/128 SQL=%v; real VM counters include correlated work", counts)
}

func libraryProfileFromOwner(t *testing.T, o *LifecycleOwner) *libraryProfileFixture {
	t.Helper()
	f := &libraryProfileFixture{owner: o, service: lib.NewService(o), scope: o.LibraryReadScope()}
	rows, e := o.database.db.Query("SELECT content_id,version_id FROM tc_content_versions")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var truth libraryTruth
		if e = rows.Scan(&truth.item.ContentID, &truth.item.VersionID); e != nil {
			t.Fatal(e)
		}
		f.truth = append(f.truth, truth)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	f.source = libraryProfileSource(t, o)
	return f
}

func TestLibraryMilestone(t *testing.T) {
	if path := os.Getenv("TC_LIBRARY_T_RESTART_PATH"); path != "" {
		o, e := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
		if e != nil {
			t.Fatal(e)
		}
		defer o.Close(context.Background())
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		page := lib.Page{Limit: 1, Cursor: os.Getenv("TC_LIBRARY_T_CURSOR"), ExpectedSnapshot: os.Getenv("TC_LIBRARY_T_SNAPSHOT")}
		if _, e = s.ListLibrary(t.Context(), lib.Query{}, page, scope); e != lib.ErrStaleCursor {
			t.Fatal("process restart revived token", e)
		}
		t.Log("T restart cursor rejected")
		return
	}
	f := libraryProfileNew(t, 100000)
	if !t.Run("profile", func(t *testing.T) { libraryProfileMatrix(t, f) }) {
		return
	}
	t.Run("lifecycle", func(t *testing.T) {
		// A lifecycle-only rerun needs a real ready index but does not repeat the
		// large page profiling matrix. The normal full test shares the built index.
		if !f.owner.library.progress.Ready {
			libraryProfileBuild(t, f, "lifecycle-only")
		}
		f.source.reset(false)
		f.index.reset(false)
		first, e := f.service.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, f.scope)
		if e != nil || first.Next == "" {
			t.Fatal(e)
		}
		page := lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}
		backup, e := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "t-backup.sqlite"), SchemaMigrations())
		if e != nil {
			t.Fatal(e)
		}
		if e = f.owner.Restore(t.Context(), backup, filepath.Join(t.TempDir(), "t-restored.sqlite")); e != nil {
			t.Fatal(e)
		}
		if _, e = f.service.ListLibrary(t.Context(), lib.Query{}, page, f.scope); e != lib.ErrUnauthorized {
			t.Fatal("restore authority", e)
		}
		f.scope = f.owner.LibraryReadScope()
		if _, e = f.service.ListLibrary(t.Context(), lib.Query{}, page, f.scope); e != lib.ErrStaleCursor {
			t.Fatal("restore token", e)
		}
		path := f.owner.config.Path
		if e = f.owner.Close(t.Context()); e != nil {
			t.Fatal(e)
		}
		executable, e := os.Executable()
		if e != nil {
			t.Fatal(e)
		}
		command := exec.CommandContext(t.Context(), executable, "-test.run=^TestLibraryMilestone$", "-test.v")
		command.Env = append(os.Environ(), "TC_LIBRARY_T_RESTART_PATH="+path, "TC_LIBRARY_T_CURSOR="+page.Cursor, "TC_LIBRARY_T_SNAPSHOT="+page.ExpectedSnapshot)
		output, e := command.CombinedOutput()
		if e != nil {
			t.Fatal("restart", e, string(output))
		}
		if !strings.Contains(string(output), "T restart cursor rejected") {
			t.Fatal("restart helper did not execute", string(output))
		}
		t.Logf("real process restart: %s", output)
		// Fresh process/rebuilt option semantics and post-restore rejection are also
		// exercised uncached by the inherited B lifecycle tests; no second 100k matrix.
	})
	t.Run("byte-short", func(t *testing.T) {
		o := catalogQueryOwner(t)
		libraryUnmanaged(t, o, 1, 1, 128, strings.Repeat("\"", 4096))
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryReady(t, o, s, scope)
		got, pages := libraryCollect(t, s, scope, lib.Query{}, 128)
		if len(got) != 128 || len(pages) < 2 || len(pages[0].Items) >= 128 || pages[0].Next == "" {
			t.Fatal("milestone lacked byte-short continuation", len(got), len(pages))
		}
		for i, key := range got {
			if key != catalogSchemaID("cnt", 1)+catalogSchemaID("ver", i+1) {
				t.Fatal("byte-short oracle", i, key)
			}
		}
	})
}

func TestLibraryCompatibilityAudit(t *testing.T) {
	t.Run("PI06-08-09-composed", func(t *testing.T) {
		f, o, sup := assignmentAuditFixture(t)
		in := f.command(t, 1, assignment.Assign, 0)
		result, e := f.service.Execute(t.Context(), in)
		if e != nil {
			t.Fatal(e)
		}
		// Author metadata through its owner; Library cannot reinterpret it as
		// catalog ownership, original/custom physical classification or authority.
		tagService := tags.New(o)
		tag, e := tagService.FindByID(t.Context(), f.tag.String())
		if e != nil {
			t.Fatal(e)
		}
		observation := tagMetadataObservation(t, tag, 800001, domaintag.FactKey(0), "true")
		tag, e = tagService.UpdateMetadata(t.Context(), tags.MetadataCommand{TagID: tag.ID(), Change: domaintag.MetadataChange{ExpectedRevision: tag.Revision(), Key: domaintag.FactKey(0), Observations: []evidence.Observation{observation}}})
		if e != nil {
			t.Fatal(e)
		}
		// Supersession changes only its explicit live annotation, never the
		// retained exact assignment target or classification.
		if _, e = sup.sup.SupersedeVersion(t.Context(), sup.command); e != nil {
			t.Fatal(e)
		}
		view, e := f.service.GetAssignment(t.Context(), assignment.EffectiveQuery{TagID: f.tag, At: in.EffectiveAt})
		if e != nil {
			t.Fatal(e)
		}
		if view.Desired.Target != f.target || !view.Superseded {
			t.Fatal("exact old target lost", view)
		}
		query := catalog.Query{Kind: catalog.FindVersionsByPair, Fingerprint: sup.command.Old.Fingerprint(), Page: catalog.PageRequest{Limit: 128}}
		lookup, e := catalogRead(t, o, query)
		if e != nil || lookup.Kind != catalog.Ambiguous || len(lookup.Entries) != 3 {
			t.Fatal(lookup, e)
		}
		before := catalogRecoveryRows(t, o.database.db)
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryReady(t, o, s, scope)
		for _, q := range []lib.Query{{Versions: lib.All, Sort: lib.TitleAsc}, {Versions: lib.Current, Sort: lib.TitleDesc}, {OwnerContentID: ptrContent(sup.command.Content), Versions: lib.Current, Sort: lib.TitleAsc}} {
			if _, e = s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 129}, scope); e != lib.ErrLimitExceeded {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
			t.Fatal("Library mutated PI06/08/09 facts, history or receipts")
		}
		again, e := catalogRead(t, o, query)
		if e != nil || !reflect.DeepEqual(lookup, again) {
			t.Fatal("PI08 lookup changed", e)
		}
		viewAgain, e := f.service.GetAssignment(t.Context(), assignment.EffectiveQuery{TagID: f.tag, At: in.EffectiveAt})
		if e != nil || !reflect.DeepEqual(view, viewAgain) {
			t.Fatal("PI09 exact target/classification changed", e)
		}
		if replay, e := f.service.Execute(t.Context(), in); e != nil || replay != result {
			t.Fatal("PI09 receipt changed", e)
		}
		tagAgain, e := tagService.FindByID(t.Context(), tag.ID().String())
		if e != nil || !tag.Equal(tagAgain) {
			t.Fatal("PI06 metadata changed", e)
		}
	})
	t.Run("PI07-receipt", func(t *testing.T) {
		o := blobOwner(t)
		command := blobCommand(t, 1, false)
		key := blobKey(t, 1)
		if _, e := blobRecord(t, o, key, command); e != nil {
			t.Fatal(e)
		}
		before := catalogRecoveryRows(t, o.database.db)
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryReady(t, o, s, scope)
		r, e := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 128}, scope)
		if e != nil || len(r.Items) != 1 || r.Items[0].Media != catalog.MediaBoundUnchecked {
			t.Fatal(r, e)
		}
		if got, found, e := blobLookup(t, o, key, command); e != nil || !found || got != importResult(command) {
			t.Fatal("PI07 receipt", got, found, e)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
			t.Fatal("PI07 durable mutation")
		}
	})
}

// Kept separate from the large cardinality matrix so repeated state checks can
// select NoNPlusOne/CompatibilityAudit without rebuilding expensive maxima.
func libraryProfileMaxEvidence(t *testing.T) {
	t.Helper()
	for _, kind := range []string{"observations", "events", "bytes"} {
		t.Run("max-"+kind, func(t *testing.T) {
			start := time.Now()
			var f supersessionFixture
			switch kind {
			case "observations":
				f = combinedAccountingObservationFixture(t)
				writer := combinedAccountingWriter{kind: "review"}
				write := combinedAccountingPending(t, f, writer, &combinedAccountingFence{owner: f.owner}, 2)
				if _, e := write(); e != nil {
					t.Fatal(e)
				}
				if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.observations != domain.RetainedObservations.Maximum() {
					t.Fatal("not max observations", u)
				}
			case "events":
				f = combinedAccountingEventFixture(t)
				a, _ := domain.NewCatalogRevision(1024)
				r, _ := domain.NewCatalogRevision(2)
				if _, e := f.base.AcceptSupport(t.Context(), catalog.ReviewCommand{ID: registrationIntegrationCommandID(t), Subject: f.registrationFixture.command.Subject, ExpectedAggregate: a, ExpectedReview: r, Key: domain.TitleClaimKey, Support: []evidence.ID{f.registrationFixture.command.Observations[0].ID()}, Reason: "e"}); e != nil {
					t.Fatal(e)
				}
				if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.events != domain.RetainedDecisions.Maximum() {
					t.Fatal("not max events", u)
				}
			case "bytes":
				calibration := combinedAccountingByteFixture(t, 0, 0)
				remaining := domain.RetainedBytes.Maximum() - combinedAccountingUsage(t, calibration.owner.database.db, calibration.command.Content).bytes
				f = combinedAccountingByteFixture(t, int(remaining/128), int(remaining%128))
				if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.bytes != domain.RetainedBytes.Maximum() {
					t.Fatal("not max bytes", u)
				}
			}
			t.Logf("max-valid-%s fixture construction=%s usage=%+v; support links capped by retained bytes, no unreachable simultaneous-max claim", kind, time.Since(start), combinedAccountingUsage(t, f.owner.database.db, f.command.Content))
			p := libraryProfileFromOwner(t, f.owner)
			libraryProfileBuild(t, p, "max-"+kind)
			q := lib.Query{Versions: lib.All, Sort: lib.IdentityAsc}
			want := libraryProfileOracle(p.truth, q)
			for _, limit := range []uint64{1, 16, 128} {
				for _, position := range libraryProfilePositions(t, p, q, limit, want) {
					libraryProfileSamples(t, p, q, position.page, "max-"+kind+"-"+position.name, want, position.offset)
				}
			}
		})
	}
}
