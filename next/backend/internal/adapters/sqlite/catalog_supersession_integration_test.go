package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
)

// All milestone facts are authored through F01/import/review/order/F02 commands.
// The inherited fixture starts empty, retains two independent Contents (one
// protected custom) and dissent, and gives their versions a shared fingerprint.
type supersessionIntegrationFixture struct {
	*registrationIntegrationFixture
	cap      domain.CatalogSupersessionCapability
	sup      app.SupersessionService
	chain    []domain.ContentVersion
	commands []app.SupersedeVersionCommand
	results  []domain.CatalogSupersessionResult
}

func newSupersessionIntegrationFixture(t *testing.T) *supersessionIntegrationFixture {
	t.Helper()
	f := &supersessionIntegrationFixture{registrationIntegrationFixture: newRegistrationIntegrationFixture(t)}
	f.chain = []domain.ContentVersion{f.versions[1]}
	f.bindSupersession(t)
	return f
}

func (f *supersessionIntegrationFixture) bindSupersession(t *testing.T) {
	t.Helper()
	f.bind(t) // Full trusted registration/reviewer/namespace equality after reopen.
	var err error
	f.cap, err = domain.NewCatalogSupersessionCapability(f.operator.review)
	if err != nil {
		t.Fatal(err)
	}
	f.sup, err = app.NewSupersessionService(f.operator.base, f.cap)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *supersessionIntegrationFixture) link(t *testing.T, old, newer domain.ContentVersion) {
	t.Helper()
	snapshot := f.order(t, 1)
	revision, err := domain.NewCatalogRevision(uint64(len(f.commands) + 1))
	if err != nil {
		t.Fatal(err)
	}
	one, err := domain.NewCatalogRevision(1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := evidence.ParseID(registrationIntegrationID(t, "obs"))
	if err != nil {
		t.Fatal(err)
	}
	// Each correction has its own sanitized operator relation record.
	claim, err := domain.OwnershipClaim(old.ContentID())
	if err != nil {
		t.Fatal(err)
	}
	subject, err := domain.VersionSubject(newer.ContentID(), newer.ID())
	if err != nil {
		t.Fatal(err)
	}
	author := registrationFixture{source: f.operator.source, ingest: f.operator.ingest}
	observation := author.observationSnapshot(t, subject, claim, []byte(`{"relation":"explicit correction"}`))
	relation, err := domain.NewCatalogSupersessionEvidence(f.operator.ingest, f.operator.source, domain.CatalogSupersessionEvidenceInput{
		ID: id, Content: old.ContentID(), OldVersion: old.ID(), NewVersion: newer.ID(), Coordinate: observation.Input().Coordinate, ObservedAt: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := app.SupersedeVersionCommand{ID: registrationIntegrationCommandID(t), Content: old.ContentID(), Old: old, New: newer, ExpectedAggregate: snapshot.AggregateRevision, ExpectedSupersession: revision, ExpectedOldReview: one, ExpectedNewReview: one, Evidence: []domain.CatalogSupersessionEvidence{relation}, Support: []evidence.ID{id}, Reason: "explicit immutable version correction"}
	before := catalogRecoveryRows(t, f.owner.database.db)
	result, err := f.sup.SupersedeVersion(t.Context(), c)
	if err != nil {
		t.Fatal("link committed versions", len(f.commands)+1, err)
	}
	after := catalogRecoveryRows(t, f.owner.database.db)
	// Only the aggregate fence, F02 rows and new operator provenance may change.
	for table, rows := range before {
		switch table {
		case "tc_catalog_managed", "tc_catalog_batches", "tc_catalog_records", "tc_catalog_supersessions", "tc_catalog_supersession_evidence", "tc_catalog_supersession_support":
			continue
		}
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatal("F02 changed unrelated rows", table)
		}
	}
	current := f.order(t, 1)
	if current.AggregateRevision.Uint64() != snapshot.AggregateRevision.Uint64()+1 || !reflect.DeepEqual(snapshot.State, current.State) || result.OrderRevision != snapshot.State.Revision() || !result.ProtectedCustom || result.Old != old || result.New != newer {
		t.Fatal("edge changed order, protection or immutable tuples", result)
	}
	f.commands = append(f.commands, c)
	f.results = append(f.results, result)
}

func (f *supersessionIntegrationFixture) correctVersions(t *testing.T) {
	t.Helper()
	for i := range f.contents {
		f.correct(t, i)
	}
	old := f.chain[0]
	if f.order(t, 1).State.EffectiveOrder().Status != domain.CatalogFactUnknown {
		t.Fatal("inferred original order")
	}
	f.decidePosition(t, old, 1)
	middle := f.addVersion(t, 1)
	if s := f.order(t, 1).State; s.EffectiveOrder().Status != domain.CatalogFactUnknown || s.Compare(old, middle) != domain.VersionOrderingUnknown {
		t.Fatal("registration inferred latest order")
	}
	f.decidePosition(t, middle, 1)
	if s := f.order(t, 1).State; s.EffectiveOrder().Status != domain.CatalogFactConflict || s.Compare(old, middle) != domain.VersionOrderingConflict {
		t.Fatal("contradictory order lost")
	}
	f.chain = append(f.chain, middle)
	f.link(t, old, middle)
	// The terminal correction deliberately has a different pair: old-ID and old
	// fingerprint queries must not silently resolve to this terminal.
	original := f.pair
	audio, err := domain.NewAudioID(124)
	if err != nil {
		t.Fatal(err)
	}
	f.pair, err = domain.NewAudioFingerprint(audio, original.Hash())
	if err != nil {
		t.Fatal(err)
	}
	terminal := f.addVersion(t, 1)
	f.pair = original
	f.chain = append(f.chain, terminal)
	f.link(t, middle, terminal)
}

func (f *supersessionIntegrationFixture) assertQueries(t *testing.T) {
	t.Helper()
	before := catalogRecoveryRows(t, f.owner.database.db)
	for i, version := range f.chain {
		page, err := catalogRead(t, f.owner, app.Query{Kind: app.GetVersion, Content: version.ContentID(), Version: version.ID(), Page: app.PageRequest{Limit: 1}})
		if err != nil || page.Kind != app.Found || !page.Complete || len(page.Entries) != 1 {
			t.Fatal("explicit version", page, err)
		}
		got := page.Entries[0]
		want := app.Current
		if i < 2 {
			want = app.Superseded
		}
		if got.Version != version || !got.ProtectedCustom || got.Supersession.Status != want || got.Supersession.Terminal != f.chain[2].ID() || got.Supersession.SupersessionRevision.Uint64() != 3 {
			t.Fatal("old/current tuple projection", got)
		}
		if i > 0 && (got.Supersession.Predecessor != f.chain[i-1].ID() || got.Supersession.IncomingCommand != f.commands[i-1].ID) {
			t.Fatal("incoming edge", got)
		}
		if i < 2 && (got.Supersession.Successor != f.chain[i+1].ID() || got.Supersession.OutgoingCommand != f.commands[i].ID) {
			t.Fatal("outgoing edge", got)
		}
	}
	current, err := catalogRead(t, f.owner, app.Query{Kind: app.ListCurrentVersions, Content: f.chain[0].ContentID(), Page: app.PageRequest{Limit: 128}})
	if err != nil || !current.Complete || len(current.Entries) != 1 || current.Entries[0].Version != f.chain[2] {
		t.Fatal("explicit current versions", current, err)
	}
	for _, kind := range []app.QueryKind{app.FindVersionsByPair, app.FindVersionsByAudioID} {
		query := app.Query{Kind: kind, Page: app.PageRequest{Limit: 128}}
		if kind == app.FindVersionsByPair {
			query.Fingerprint = f.pair
		} else {
			query.AudioID = f.pair.AudioID()
		}
		page, err := catalogRead(t, f.owner, query)
		if err != nil || page.Kind != app.Ambiguous || !page.Complete || len(page.Entries) != 3 {
			t.Fatal("shared old pair lost ambiguity", page, err)
		}
		seen := map[identity.ContentVersionID]bool{}
		for _, entry := range page.Entries {
			seen[entry.Version.ID()] = true
			if entry.Version.Fingerprint() != f.pair {
				t.Fatal("old pair redirected", entry)
			}
		}
		for _, version := range []domain.ContentVersion{f.versions[0], f.chain[0], f.chain[1]} {
			if !seen[version.ID()] {
				t.Fatal("distinct match missing", version)
			}
		}
		query.Page.Limit = 1
		partial, err := catalogRead(t, f.owner, query)
		if err != nil || partial.Kind != app.Incomplete || partial.Complete || partial.Next == "" {
			t.Fatal("partial ambiguity became unique", partial, err)
		}
	}
	all, err := catalogRead(t, f.owner, app.Query{Kind: app.ListVersions, Content: f.chain[0].ContentID(), Page: app.PageRequest{Limit: 128}})
	if err != nil || len(all.Entries) != 3 || !all.Complete {
		t.Fatal("old versions filtered", all, err)
	}
	if s := f.order(t, 1).State; s.Compare(f.chain[0], f.chain[1]) != domain.VersionOrderingConflict || s.Compare(f.chain[1], f.chain[2]) != domain.VersionOrderingConflict {
		t.Fatal("supersession inferred order")
	}
	for _, filter := range []identity.ContentVersionID{{}, f.chain[0].ID(), f.chain[1].ID(), f.chain[2].ID()} {
		page, err := f.sup.QuerySupersession(t.Context(), app.SupersessionQuery{Content: f.chain[0].ContentID(), Version: filter, Page: app.PageRequest{Limit: 128}})
		if err != nil || !page.Complete || page.Inspected != 2 || len(page.Entries) != 2 || page.SupersessionRevision.Uint64() != 3 {
			t.Fatal("whole-chain history", page, err)
		}
		for i, entry := range page.Entries {
			raw, err := f.operation(f.commands[i]).Command.CanonicalEncoding(f.cap)
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.results[i].CanonicalEncoding()
			if err != nil || !bytes.Equal(raw, entry.Command) || !bytes.Equal(result, entry.Result) || entry.SourceRevoked {
				t.Fatal("historical canonical evidence changed", i, err)
			}
		}
	}
	for i, subject := range f.contents {
		err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
			state, found, err := r.LoadReview(t.Context(), subject)
			if err != nil {
				return err
			}
			retained := false
			for _, o := range state.State.Observations() {
				if o.ID() == f.dissent[i].ID() {
					retained = true
				}
			}
			if !found || !retained || len(state.State.Observations()) != 2 {
				t.Fatal("retained dissent lost")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.replays(t)
	for i, c := range f.commands {
		got, found, err := f.sup.ReadSupersession(t.Context(), c)
		if err != nil || !found || !reflect.DeepEqual(got, f.results[i]) {
			t.Fatal("original F02 readback", got, found, err)
		}
		got, err = f.sup.SupersedeVersion(t.Context(), c)
		if err != nil || !reflect.DeepEqual(got, f.results[i]) {
			t.Fatal("original F02 replay", got, err)
		}
	}
	staged := catalogRecoveryRows(t, f.owner.database.db)
	for table, rows := range f.staged {
		for _, row := range rows {
			if !catalogSchemaContains(staged[table], row) {
				t.Fatal("original staged evidence changed", table)
			}
		}
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("queries/replays changed rows or accounting")
	}
}

func (f *supersessionIntegrationFixture) operation(c app.SupersedeVersionCommand) app.SupersessionOperation {
	return app.SupersessionOperation{Capability: f.cap, Command: domain.CatalogSupersessionCommand{ID: c.ID, Content: c.Content, Old: c.Old, New: c.New, ExpectedAggregate: c.ExpectedAggregate, ExpectedSupersession: c.ExpectedSupersession, ExpectedOldReview: c.ExpectedOldReview, ExpectedNewReview: c.ExpectedNewReview, Source: f.operator.source, Reason: c.Reason, Evidence: c.Evidence, Support: c.Support}}
}

func supersessionIntegrationMilestone(t *testing.T) {
	t.Helper()
	f := newSupersessionIntegrationFixture(t)
	f.correctVersions(t)
	f.assertQueries(t)
	before := catalogRecoveryRows(t, f.owner.database.db)
	snapshot, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "f02.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	config := f.owner.config
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.owner, err = OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	f.bindSupersession(t)
	f.assertQueries(t)
	// Prove restore actually selects the verified snapshot over later commands.
	f.addVersion(t, 0)
	absent := f.creations[len(f.creations)-1]
	f.creations = f.creations[:len(f.creations)-1]
	if reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("post-backup registration did not commit")
	}
	if err := f.owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	f.bindSupersession(t)
	if _, found, err := f.service.ReadRegistration(t.Context(), absent.kind, absent.command); err != nil || found {
		t.Fatal("restore retained later registration", found, err)
	}
	f.assertQueries(t)
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("reopen/restore changed exact rows")
	}
	if err := verifyForeignKeys(t.Context(), f.owner.database.db); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal("original backup changed", err)
	}
	t.Log("F02-D local command/reopen/restore evidence only; independent audit and F02/PI-08/T acceptance remain open")
}

func TestSupersessionIntegrationMilestone(t *testing.T) { supersessionIntegrationMilestone(t) }

func TestSupersessionHistoryCursorVerifiedRestore(t *testing.T) {
	f := newSupersessionIntegrationFixture(t)
	f.correctVersions(t)
	// Retain this service and owner across Restore: no reopen, synthetic
	// generation change or replacement reader can account for invalidation.
	reader := f.sup
	q := app.SupersessionQuery{Content: f.chain[0].ContentID(), Page: app.PageRequest{Limit: 128}}
	restoredHistory, err := reader.QuerySupersession(t.Context(), q)
	if err != nil || !restoredHistory.Complete || restoredHistory.Next != "" || len(restoredHistory.Entries) != 2 {
		t.Fatal("snapshot history", restoredHistory, err)
	}
	for i, entry := range restoredHistory.Entries {
		command, err := f.operation(f.commands[i]).Command.CanonicalEncoding(f.cap)
		if err != nil {
			t.Fatal(err)
		}
		result, err := f.results[i].CanonicalEncoding()
		if err != nil || !bytes.Equal(entry.Command, command) || !bytes.Equal(entry.Result, result) || entry.SourceRevoked {
			t.Fatal("history differs from accepted canonical command/result", i, err)
		}
	}
	continuation := q
	continuation.Page.Limit = 1
	first, err := reader.QuerySupersession(t.Context(), continuation)
	if err != nil || first.Complete || first.Next == "" || len(first.Entries) != 1 || !reflect.DeepEqual(first.Entries[0], restoredHistory.Entries[0]) {
		t.Fatal("issue history cursor", first, err)
	}
	continuation.Page.Cursor, continuation.Page.ExpectedRevision = first.Next, first.AggregateRevision
	if second, err := reader.QuerySupersession(t.Context(), continuation); err != nil || !second.Complete || second.Next != "" || len(second.Entries) != 1 || !reflect.DeepEqual(second.Entries[0], restoredHistory.Entries[1]) {
		t.Fatal("cursor was not usable before restore", second, err)
	}
	rows := catalogRecoveryRows(t, f.owner.database.db)
	snapshot, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "history.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	// Distinguish live history from the accepted snapshot using commands only.
	later := f.addVersion(t, 1)
	f.link(t, f.chain[2], later)
	live, err := reader.QuerySupersession(t.Context(), q)
	if err != nil || !live.Complete || len(live.Entries) != 3 || live.AggregateRevision.Uint64() <= restoredHistory.AggregateRevision.Uint64() || live.SupersessionRevision.Uint64() != restoredHistory.SupersessionRevision.Uint64()+1 || !reflect.DeepEqual(live.Entries[:2], restoredHistory.Entries) {
		t.Fatal("post-snapshot history did not advance", live, err)
	}
	destination := filepath.Join(t.TempDir(), "restored-history.sqlite")
	if err := f.owner.Restore(t.Context(), snapshot, destination); err != nil {
		t.Fatal(err)
	}
	f.bindSupersession(t)
	if f.owner.config.Path != destination || !reflect.DeepEqual(rows, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("restore did not select the exact snapshot at the new path")
	}
	// The original aggregate, graph and snapshot are back, so rejection cannot
	// be explained merely by the later edge or its aggregate revision.
	if page, err := reader.QuerySupersession(t.Context(), continuation); !errors.Is(err, app.ErrStaleCursor) || !reflect.DeepEqual(page, app.HistoryPage{}) {
		t.Fatal("verified restore accepted the prior owner cursor", page, err)
	}
	if fresh, err := reader.QuerySupersession(t.Context(), q); err != nil || !reflect.DeepEqual(fresh, restoredHistory) {
		t.Fatal("fresh history differs from the restored history", fresh, err)
	}
	if !reflect.DeepEqual(rows, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("stale/fresh queries mutated restored rows/accounting")
	}
}

func TestSupersessionMediaAssignmentCompatibility(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor-backed contentfs requires Linux")
	}
	f := newSupersessionIntegrationFixture(t)
	parent := os.Getenv("TEDDYCLOUD_CONTENTFS_TEST_ROOT")
	if parent == "" {
		parent = "/var/tmp"
	}
	root, err := os.MkdirTemp(parent, "tc-supersession-media-")
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
	media, err := contentstore.NewService(t.Context(), f.owner, store)
	if err != nil {
		t.Fatal(err)
	}
	command, err := content.NewImportCommand(f.chain[0], content.NewBlobID(f.generated.BlobDigest()), f.generated.CompleteBytes(), content.TAFProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	key, err := content.ParseImportKey(registrationIntegrationID(t, "imp"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := media.Import(t.Context(), key, command, f.generated.Open(), content.FiniteTAFSource)
	if err != nil || want != importResult(command) {
		t.Fatal("exact PI-07 attachment", want, err)
	}
	canonical := command.CanonicalBytes()
	retained := catalogRecoveryRows(t, f.owner.database.db)
	id, err := assignment.ParseID(registrationIntegrationID(t, "asn"))
	if err != nil {
		t.Fatal(err)
	}
	tag, err := identity.ParseTagID(registrationIntegrationID(t, "tag"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	bounds := []time.Time{at.Add(-48 * time.Hour), at.Add(-24 * time.Hour), at.Add(24 * time.Hour), {}}
	var entries []assignment.Assignment
	for i := 0; i < 3; i++ {
		interval, err := assignment.NewInterval(bounds[i], bounds[i+1])
		if err != nil {
			t.Fatal(err)
		}
		a, err := assignment.New(id, uint64(i+1), tag, command.Version().ID(), interval)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, a)
	}
	history, err := assignment.NewHistory(entries)
	if err != nil {
		t.Fatal(err)
	}
	f.correctVersions(t)
	assertOriginal := func() {
		t.Helper()
		before := catalogRecoveryRows(t, f.owner.database.db)
		got, found, err := media.LookupImport(t.Context(), key, command)
		if err != nil || !found || got != want {
			t.Fatal("old command receipt", got, found, err)
		}
		got, err = media.Import(t.Context(), key, command, f.generated.Open(), content.FiniteTAFSource)
		if err != nil || got != want || !bytes.Equal(canonical, command.CanonicalBytes()) {
			t.Fatal("PI-07 canonical replay changed", got, err)
		}
		if err := media.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
			t.Fatal("old blob verification", err)
		}
		for _, table := range []string{"tc_blob_imports", "tc_blobs", "tc_version_blobs"} {
			if !reflect.DeepEqual(retained[table], catalogRecoveryRows(t, f.owner.database.db)[table]) {
				t.Fatal("F02 changed media/receipt", table)
			}
		}
		// These are domain snapshots, not a claim of persistent assignment storage.
		if !reflect.DeepEqual(entries, history.Entries()) {
			t.Fatal("assignment values/intervals changed")
		}
		for i, point := range []time.Time{bounds[0], at, bounds[2]} {
			current, ok := history.CurrentAt(point)
			if !ok || current != entries[i] || current.ContentVersionID() != command.Version().ID() {
				t.Fatal("historical/current/future assignment retargeted", current)
			}
		}
		for i, version := range f.chain {
			page, err := catalogRead(t, f.owner, app.Query{Kind: app.GetVersion, Content: version.ContentID(), Version: version.ID(), Page: app.PageRequest{Limit: 1}})
			state := app.CatalogOnly
			if i == 0 {
				state = app.MediaBoundUnchecked
			}
			if err != nil || len(page.Entries) != 1 || page.Entries[0].Version != version || page.Entries[0].Media != state || page.Entries[0].Supersession.Terminal != f.chain[2].ID() {
				t.Fatal("explicit old-ID media was redirected", page, err)
			}
		}
		// Exercise PI-07's actual version-to-media resolver by the explicit old
		// ID inside the real content-operation/transaction fence.
		err = f.owner.WithinContentOperation(t.Context(), func(ctx context.Context, session contentstore.Session) error {
			return session.WithinTransaction(ctx, func(repository contentstore.BlobRepository) error {
				resolver := repository.(*blobRepository)
				old, found, err := resolver.loadVersion(ctx, command.Version().ID())
				if err != nil || !found || old != want {
					t.Fatal("old-ID PI-07 media resolver redirected", old, found, err)
				}
				terminal, found, err := resolver.loadVersion(ctx, f.chain[2].ID())
				if err != nil || !found || terminal.Version != f.chain[2] || !terminal.BlobID.IsZero() {
					t.Fatal("terminal inherited old media", terminal, found, err)
				}
				return nil
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
			t.Fatal("read/replay changed rows")
		}
	}
	assertOriginal()
	for _, field := range []string{"owner", "audio", "hash", "order", "rebinding"} {
		t.Run(field, func(t *testing.T) {
			v := command.Version()
			owner, pair, order, blob := v.ContentID(), v.Fingerprint(), v.OrderEvidence(), command.BlobID()
			var err error
			switch field {
			case "owner":
				owner = f.contents[0].ContentID()
			case "audio":
				audio, e := domain.NewAudioID(456)
				if e != nil {
					t.Fatal(e)
				}
				pair, err = domain.NewAudioFingerprint(audio, pair.Hash())
			case "hash":
				hash, e := domain.ParseAudioHash("1111111111111111111111111111111111111111")
				if e != nil {
					t.Fatal(e)
				}
				pair, err = domain.NewAudioFingerprint(pair.AudioID(), hash)
			case "order":
				order, err = domain.NewVersionOrderEvidence("changed-original", 7)
			case "rebinding":
				blob = content.NewBlobID(sha256.Sum256([]byte("different envelope")))
			}
			if err != nil {
				t.Fatal(err)
			}
			changedVersion, err := domain.NewContentVersion(v.ID(), owner, pair, order)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := content.NewImportCommand(changedVersion, blob, command.CompleteBytes(), command.Profile())
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := content.ParseImportKey(registrationIntegrationID(t, "imp"))
			if err != nil {
				t.Fatal(err)
			}
			before := catalogRecoveryRows(t, f.owner.database.db)
			for _, candidate := range []content.ImportKey{key, fresh} {
				if _, err := blobRecord(t, f.owner, candidate, changed); !errors.Is(err, contentstore.ErrConflict) {
					t.Fatal("changed tuple/rebinding accepted", err)
				}
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
				t.Fatal("rejection leaked rows")
			}
		})
	}
	before := catalogRecoveryRows(t, f.owner.database.db)
	snapshot, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "media.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	config := f.owner.config
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.owner, err = OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	f.bindSupersession(t)
	media, err = contentstore.NewService(t.Context(), f.owner, store)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal()
	if err := f.owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "media-restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	f.bindSupersession(t)
	assertOriginal()
	f.assertQueries(t)
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("restored media rows differ")
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal("original media backup changed", err)
	}
}

func TestSupersessionLegacyQueryCompatibility(t *testing.T) {
	f := newRegistrationFixtureMigrations(t, SchemaMigrations()[:8])
	if _, err := f.service.RegisterContent(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	for n := 101; n <= 102; n++ {
		if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, n, uint64(n-100))); err != nil {
			t.Fatal(err)
		}
	}
	query := app.Query{Kind: app.ListVersions, Content: f.command.Subject.ContentID(), Page: app.PageRequest{Limit: 128}}
	legacy, err := catalogRead(t, f.owner, query)
	if err != nil || !legacy.Complete || len(legacy.Entries) != 2 {
		t.Fatal("v8 F01 query", legacy, err)
	}
	for _, entry := range legacy.Entries {
		if entry.Supersession != (app.Supersession{}) {
			t.Fatal("legacy version inferred current", entry)
		}
	}
	before := catalogRecoveryRows(t, f.owner.database.db)
	snapshot, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "v8.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	config := f.owner.config
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	config.UpgradeBackupPath = filepath.Join(t.TempDir(), "pre-upgrade.sqlite")
	owner, err := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	f.owner = owner
	f.bind(t)
	for _, entry := range legacy.Entries {
		page, err := catalogRead(t, owner, app.Query{Kind: app.GetVersion, Content: entry.Content, Version: entry.Version.ID(), Page: app.PageRequest{Limit: 1}})
		if err != nil || len(page.Entries) != 1 || page.Entries[0].Version != entry.Version || page.Entries[0].Supersession.Status != app.Current {
			t.Fatal("v9 empty graph projection", page, err)
		}
	}
	if err := owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "selected-v8.sqlite")); err != nil {
		t.Fatal(err)
	}
	f.bind(t)
	got, err := catalogRead(t, owner, query)
	if err != nil || got.Kind != legacy.Kind || !reflect.DeepEqual(got.Entries, legacy.Entries) {
		t.Fatal("legacy restore changed old query", got, err)
	}
	cap, err := domain.NewCatalogSupersessionCapability(f.review)
	if err != nil {
		t.Fatal(err)
	}
	sup, err := app.NewSupersessionService(f.base, cap)
	if err != nil {
		t.Fatal(err)
	}
	sf := supersessionFixture{registrationFixture: f, cap: cap, sup: sup}
	command := sf.link(t, legacy.Entries[0].Version, legacy.Entries[1].Version, 3, 1)
	if _, err := sup.SupersedeVersion(t.Context(), command); !errors.Is(err, app.ErrUnsupported) {
		t.Fatal("F02 enabled after v8 restore", err)
	}
	if _, _, err := sup.ReadSupersession(t.Context(), command); !errors.Is(err, app.ErrUnsupported) {
		t.Fatal("F02 readback enabled on v8", err)
	}
	if _, err := sup.QuerySupersession(t.Context(), app.SupersessionQuery{Content: query.Content, Page: query.Page}); !errors.Is(err, app.ErrUnsupported) {
		t.Fatal("F02 history enabled on v8", err)
	}
	current := query
	current.Kind = app.ListCurrentVersions
	if _, err := catalogRead(t, owner, current); !errors.Is(err, app.ErrUnsupported) {
		t.Fatal("current inference enabled on v8", err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("legacy recovery/rejections changed rows")
	}
	if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, 103, 3)); err != nil {
		t.Fatal("F01 disabled by v8 restore", err)
	}
	page, err := catalogRead(t, owner, query)
	if err != nil || len(page.Entries) != 3 {
		t.Fatal("F01 growth on restored v8", page, err)
	}
	for _, entry := range page.Entries {
		if entry.Supersession != (app.Supersession{}) {
			t.Fatal("F01 growth inferred F02 status")
		}
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal("original v8 backup changed", err)
	}
}
