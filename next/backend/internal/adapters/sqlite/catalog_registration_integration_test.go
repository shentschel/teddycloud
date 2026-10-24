package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	parser "github.com/shentschel/teddycloud/next/backend/internal/adapters/catalogimport"
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

// F01-D uses real import/registration/review/order commands throughout. SQL in
// this file only reads evidence; it never seeds or repairs an identity/state.
type registrationIntegrationSource struct {
	source  domain.SourceRegistration
	ingest  domain.IngestCapability
	review  domain.ReviewCapability
	space   domain.CatalogOrderNamespace
	base    app.Service
	records []domain.RecordCoordinate
}

type registrationIntegrationCreation struct {
	kind    domain.CatalogRegistrationKind
	command app.RegistrationCommand
	result  domain.CatalogRegistrationResult
}

type registrationIntegrationFixture struct {
	owner     *LifecycleOwner
	operator  registrationIntegrationSource
	public    registrationIntegrationSource
	custom    registrationIntegrationSource
	service   app.RegistrationService
	contents  [2]domain.CatalogSubject
	dissent   [2]domain.CatalogObservation
	versions  []domain.ContentVersion
	creations []registrationIntegrationCreation
	generated taffixture.Fixture
	pair      domain.AudioFingerprint
	staged    map[string][][]any
}

func registrationIntegrationID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := domain.NewBatchID()
	if err != nil {
		t.Fatal(err)
	}
	return prefix + "_" + id.String()[:26]
}

func registrationIntegrationCommandID(t *testing.T) domain.CatalogCommandID {
	t.Helper()
	id, err := domain.NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func newRegistrationIntegrationSource(t *testing.T, reviewer domain.IssuerPrincipal, acquisition domain.AcquisitionClass) registrationIntegrationSource {
	t.Helper()
	id, err := domain.NewSourceID()
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := domain.NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	keys := []domain.ClaimKey{domain.TitleClaimKey, domain.ModelClaimKey, domain.ArticleClaimKey, domain.FingerprintClaimKey}
	if acquisition == domain.CoreOperator {
		keys = append(keys, domain.OwnershipClaimKey)
	}
	source, ingest, err := domain.NewSourceRegistration(id, issuer, domain.SourceSpec{Adapter: "integration-sanitized", AdapterVersion: "1", Scope: "f01-d", Acquisition: acquisition, AllowedClaims: keys})
	if err != nil {
		t.Fatal(err)
	}
	review, err := domain.NewReviewCapability(reviewer, source)
	if err != nil {
		t.Fatal(err)
	}
	space, err := domain.NewCatalogOrderNamespace(source, "integration-order/1")
	if err != nil {
		t.Fatal(err)
	}
	return registrationIntegrationSource{source: source, ingest: ingest, review: review, space: space}
}

func (s *registrationIntegrationSource) stage(t *testing.T, owner *LifecycleOwner, records string) {
	t.Helper()
	report, err := parser.Parse(t.Context(), []byte(`{"format":"catalog-offline-v1","records":[`+records+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := report.CatalogImportPayload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewImportService(owner, s.source, s.ingest)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := domain.NewBatchID()
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Import(t.Context(), batch, report)
	if err != nil || result.Batch != batch || result.Digest != report.Digest || result.Records != payload.RecordCount() {
		t.Fatal("stage sanitized records", result, err)
	}
	assertImportRows(t, owner, batch, report)
	for _, record := range report.Records {
		for _, pair := range record.Pairs {
			if pair.AudioID > uint64(^uint32(0)) && record.Disposition != parser.Unsupported {
				t.Fatal("unsupported durable width lost its import disposition")
			}
		}
	}
	artifact, err := domain.NewArtifact(s.ingest, s.source, domain.ArtifactFacts{ID: batch, SHA256: sha256.Sum256(payload.ArtifactBytes()), ByteLength: payload.ByteLength(), RecordCount: payload.RecordCount(), ParserRevision: app.OfflineImportRevision})
	if err != nil {
		t.Fatal(err)
	}
	for i, record := range payload.Records() {
		coordinate, err := domain.NewRecordCoordinate(artifact, uint64(i), record.Locator, record.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		s.records = append(s.records, coordinate)
	}
}

func (s registrationIntegrationSource) observation(t *testing.T, subject domain.CatalogSubject, claim domain.CatalogClaim, record int) domain.CatalogObservation {
	t.Helper()
	id, err := evidence.ParseID(registrationIntegrationID(t, "obs"))
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.NewCatalogObservation(s.ingest, s.source, domain.ObservationInput{ID: id, Subject: subject, Claim: claim, Coordinate: s.records[record], ObservedAt: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), Confidence: evidence.Confidence(1)})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func (f *registrationIntegrationFixture) bind(t *testing.T) {
	t.Helper()
	sources := []domain.SourceRegistration{f.operator.source, f.public.source, f.custom.source}
	bindings := domain.CatalogPersistenceBindings{Sources: sources, Reviewers: []domain.IssuerPrincipal{f.operator.review.Reviewer()}, Namespaces: []domain.CatalogOrderNamespace{f.operator.space, f.public.space, f.custom.space}}
	for _, s := range []*registrationIntegrationSource{&f.operator, &f.public, &f.custom} {
		raw, err := s.source.PersistenceEncoding()
		if err != nil {
			t.Fatal(err)
		}
		rebound, err := domain.RehydrateSourceRegistration(raw, bindings)
		if err != nil || rebound != s.source || rebound.Revision() != s.source.Revision() || !reflect.DeepEqual(rebound.Spec(), s.source.Spec()) {
			t.Fatal("exact trusted source rebind", err)
		}
		s.review, err = domain.NewReviewCapability(f.operator.review.Reviewer(), rebound)
		if err != nil {
			t.Fatal(err)
		}
		s.base, err = app.NewService(f.owner, rebound, s.review, s.space)
		if err != nil {
			t.Fatal(err)
		}
	}
	capability, err := domain.NewCatalogRegistrationCapability(f.operator.review, []domain.ReviewCapability{f.operator.review, f.public.review, f.custom.review})
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = app.NewRegistrationService(f.operator.base, capability)
	if err != nil {
		t.Fatal(err)
	}
}

func newRegistrationIntegrationFixture(t *testing.T) *registrationIntegrationFixture {
	t.Helper()
	f := &registrationIntegrationFixture{owner: catalogQueryOwner(t)}
	t.Cleanup(func() { _ = f.owner.Close(context.Background()) })
	if version, err := f.owner.CurrentVersion(t.Context()); err != nil || version != 8 {
		t.Fatal("newly migrated v8", version, err)
	}
	for table, rows := range registrationSchemaRows(t, f.owner.database.db) {
		if table != "tc_schema_migrations" && len(rows) != 0 {
			t.Fatal("nonempty initial database", table)
		}
	}
	reviewer, err := domain.NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	f.operator = newRegistrationIntegrationSource(t, reviewer, domain.CoreOperator)
	f.public = newRegistrationIntegrationSource(t, reviewer, domain.OfflinePublic)
	f.custom = newRegistrationIntegrationSource(t, reviewer, domain.OfflineCustom)
	f.generated, err = taffixture.New(8193, 123, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	digest := f.generated.PayloadSHA1()
	hash, err := domain.ParseAudioHash(hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	audio, err := domain.NewAudioID(123)
	if err != nil {
		t.Fatal(err)
	}
	f.pair, err = domain.NewAudioFingerprint(audio, hash)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.contents {
		id, err := identity.ParseContentID(registrationIntegrationID(t, "cnt"))
		if err != nil {
			t.Fatal(err)
		}
		f.contents[i], err = domain.ContentSubject(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.public.stage(t, f.owner, fmt.Sprintf(`{"record":"public-a","kind":"public","title":"Public A","pairs":[{"audio_id":123,"sha1":%q}]},{"record":"wide","kind":"public","pairs":[{"audio_id":4294967296,"sha1":%q}]}`, hash.String(), hash.String()))
	f.custom.stage(t, f.owner, fmt.Sprintf(`{"record":"custom-b","kind":"custom","title":"Protected B","pairs":[{"audio_id":123,"sha1":%q}]}`, hash.String()))
	f.operator.stage(t, f.owner, fmt.Sprintf(`{"record":"operator-a","kind":"public","title":"Corrected A","content_ref":%q,"order_position":1},{"record":"operator-b","kind":"public","title":"Alternative B","content_ref":%q}`, f.contents[0].ContentID().String(), f.contents[1].ContentID().String()))
	f.staged = make(map[string][][]any)
	staged := registrationSchemaRows(t, f.owner.database.db)
	for _, table := range []string{"tc_catalog_imports", "tc_catalog_batches", "tc_catalog_records"} {
		f.staged[table] = staged[table]
	}
	for _, table := range []string{"tc_catalog_content", "tc_content_versions", "tc_catalog_managed", "tc_catalog_subjects", "tc_catalog_observations"} {
		if len(staged[table]) != 0 {
			t.Fatal("staging authored catalog facts", table)
		}
	}
	f.bind(t)
	for i, source := range []registrationIntegrationSource{f.public, f.custom} {
		title := []string{"Public A", "Protected B"}[i]
		claim, err := domain.TitleClaim(title)
		if err != nil {
			t.Fatal(err)
		}
		selected := source.observation(t, f.contents[i], claim, 0)
		alternative, err := domain.TitleClaim([]string{"Corrected A", "Alternative B"}[i])
		if err != nil {
			t.Fatal(err)
		}
		f.dissent[i] = f.operator.observation(t, f.contents[i], alternative, i)
		command := app.RegistrationCommand{ID: registrationIntegrationCommandID(t), Subject: f.contents[i], ExpectedKind: domain.CatalogExpectedAbsent, Observations: []domain.CatalogObservation{f.dissent[i], selected}, Selections: []domain.CatalogRegistrationSelection{{Key: domain.TitleClaimKey, Observation: selected.ID()}}, Reason: "explicit content registration from staged safe evidence"}
		result, err := f.service.RegisterContent(t.Context(), command)
		if err != nil || result.AggregateRevision.Uint64() != 1 || result.ReviewRevision.Uint64() != 1 || result.OrderRevision.Uint64() != 1 || result.ProtectedCustom != (i == 1) {
			t.Fatal("register independent content", result, err)
		}
		f.creations = append(f.creations, registrationIntegrationCreation{domain.RegisterContent, command, result})
	}
	f.addVersion(t, 0)
	f.addVersion(t, 1)
	f.assertStaged(t)
	return f
}

func (f *registrationIntegrationFixture) order(t *testing.T, index int) app.OrderSnapshot {
	t.Helper()
	var snapshot app.OrderSnapshot
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		var found bool
		var err error
		snapshot, found, err = r.LoadOrder(t.Context(), f.contents[index].ContentID())
		if err == nil && !found {
			t.Fatal("missing registered order")
		}
		return err
	})
	if err != nil {
		t.Fatal("load order", err)
	}
	return snapshot
}

func (f *registrationIntegrationFixture) addVersion(t *testing.T, index int) domain.ContentVersion {
	t.Helper()
	id, err := identity.ParseContentVersionID(registrationIntegrationID(t, "ver"))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := domain.VersionSubject(f.contents[index].ContentID(), id)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := domain.FingerprintClaim(f.pair)
	if err != nil {
		t.Fatal(err)
	}
	source := f.public
	if index == 1 {
		source = f.custom
	}
	pair := source.observation(t, subject, claim, 0)
	claim, err = domain.OwnershipClaim(subject.ContentID())
	if err != nil {
		t.Fatal(err)
	}
	owner := f.operator.observation(t, subject, claim, index)
	before := f.order(t, index)
	command := app.RegistrationCommand{ID: registrationIntegrationCommandID(t), Subject: subject, ExpectedKind: domain.CatalogExpectedRevision, Expected: before.AggregateRevision, Observations: []domain.CatalogObservation{pair, owner}, Selections: []domain.CatalogRegistrationSelection{{Key: domain.FingerprintClaimKey, Observation: pair.ID()}, {Key: domain.OwnershipClaimKey, Observation: owner.ID()}}, Reason: "explicit fingerprint and independent operator ownership"}
	result, err := f.service.RegisterCatalogVersion(t.Context(), command)
	if err != nil || result.AggregateRevision.Uint64() != before.AggregateRevision.Uint64()+1 || result.ReviewRevision.Uint64() != 1 || result.OrderRevision.Uint64() != before.State.Revision().Uint64()+1 || result.ProtectedCustom != (index == 1) {
		t.Fatal("register unbound version", result, err)
	}
	version, err := domain.NewContentVersion(id, subject.ContentID(), f.pair, domain.UnknownVersionOrderEvidence())
	if err != nil {
		t.Fatal(err)
	}
	f.versions = append(f.versions, version)
	f.creations = append(f.creations, registrationIntegrationCreation{domain.RegisterCatalogVersion, command, result})
	return version
}

func (f *registrationIntegrationFixture) assertStaged(t *testing.T) {
	t.Helper()
	rows := registrationSchemaRows(t, f.owner.database.db)
	for table, want := range f.staged {
		if !reflect.DeepEqual(want, rows[table]) {
			t.Fatal("staged artifacts, dispositions or exact safe records changed", table)
		}
	}
}

func (f *registrationIntegrationFixture) readContent(t *testing.T, index int) app.CatalogRecord {
	t.Helper()
	result, err := catalogRead(t, f.owner, app.Query{Kind: app.GetContent, Content: f.contents[index].ContentID(), Page: app.PageRequest{Limit: 1}})
	if err != nil || len(result.Entries) != 1 || !result.Complete {
		t.Fatal("content query", result, err)
	}
	return result.Entries[0]
}

func (f *registrationIntegrationFixture) correct(t *testing.T, index int) {
	t.Helper()
	var review app.ReviewSnapshot
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		var found bool
		var err error
		review, found, err = r.LoadReview(t.Context(), f.contents[index])
		if err == nil && !found {
			t.Fatal("missing registered review")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.State.Observations()) != 2 || review.State.Field(domain.TitleClaimKey).Status != domain.CatalogFactAccepted {
		t.Fatal("pending competing provenance not retained")
	}
	for _, resolve := range []bool{false, true} {
		command := app.ReviewCommand{ID: registrationIntegrationCommandID(t), Subject: f.contents[index], ExpectedAggregate: review.AggregateRevision, ExpectedReview: review.State.Revision(), Key: domain.TitleClaimKey, Support: []evidence.ID{f.dissent[index].ID()}, Reason: "explicitly review retained competing title"}
		var result app.ReviewResult
		if resolve {
			result, err = f.operator.base.ResolveConflict(t.Context(), command)
		} else {
			result, err = f.operator.base.AcceptSupport(t.Context(), command)
		}
		if err != nil {
			t.Fatal("explicit review/correction", resolve, err)
		}
		wantStatus := domain.CatalogFactConflict
		if resolve {
			wantStatus = domain.CatalogFactAccepted
		}
		entry := f.readContent(t, index)
		if entry.Title.Status != wantStatus || entry.ProtectedCustom != (index == 1) {
			t.Fatal("review projection or monotonic protection", entry)
		}
		if resolve {
			value, ok := entry.Title.Value()
			if !ok || value != f.dissent[index].Input().Claim || !reflect.DeepEqual(entry.Title.Alternatives[0].Support, []evidence.ID{f.dissent[index].ID()}) {
				t.Fatal("correction selected wrong provenance", entry)
			}
		}
		err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
			var found bool
			var err error
			review, found, err = r.LoadReview(t.Context(), f.contents[index])
			if err == nil && (!found || review.AggregateRevision != result.AggregateRevision || review.State.Revision() != result.Decision.Revision || len(review.State.Observations()) != 2) {
				t.Fatal("review history/evidence changed")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (f *registrationIntegrationFixture) decidePosition(t *testing.T, version domain.ContentVersion, position uint64) app.OrderResult {
	t.Helper()
	index := 0
	if version.ContentID() == f.contents[1].ContentID() {
		index = 1
	}
	snapshot := f.order(t, index)
	id, err := evidence.ParseID(registrationIntegrationID(t, "obs"))
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := domain.NewCatalogSequenceEvidence(f.operator.ingest, f.operator.space, domain.CatalogSequenceInput{ID: id, Content: version.ContentID(), Version: version.ID(), Position: position, Coordinate: f.operator.records[index]})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.operator.base.DecideOrder(t.Context(), app.OrderCommand{ID: registrationIntegrationCommandID(t), Content: version.ContentID(), ExpectedAggregate: snapshot.AggregateRevision, ExpectedOrder: snapshot.State.Revision(), Entries: []domain.CatalogOrderEntry{{Version: version.ID(), Position: position, Support: []evidence.ID{id}}}, Evidence: []domain.CatalogSequenceEvidence{sequence}, Reason: "explicit supported position"})
	if err != nil {
		t.Fatal("order decision", err)
	}
	return result
}

func (f *registrationIntegrationFixture) replays(t *testing.T) {
	t.Helper()
	before := catalogRecoveryRows(t, f.owner.database.db)
	for _, creation := range f.creations {
		got, found, err := f.service.ReadRegistration(t.Context(), creation.kind, creation.command)
		if err != nil || !found || !reflect.DeepEqual(got, creation.result) {
			t.Fatal("exact authorized registration readback", found, err)
		}
		if creation.kind == domain.RegisterContent {
			got, err = f.service.RegisterContent(t.Context(), creation.command)
		} else {
			got, err = f.service.RegisterCatalogVersion(t.Context(), creation.command)
		}
		if err != nil || !reflect.DeepEqual(got, creation.result) {
			t.Fatal("original registration result after later revisions", err)
		}
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("replay charged/wrote retained state")
	}
}

func registrationIntegrationMilestone(t *testing.T) {
	f := newRegistrationIntegrationFixture(t)
	for i := range f.contents {
		entry := f.readContent(t, i)
		if entry.ProtectedCustom != (i == 1) || entry.Title.Status != domain.CatalogFactAccepted || entry.Model.Status != domain.CatalogFactUnknown || entry.Article.Status != domain.CatalogFactUnknown {
			t.Fatal("independent bootstrap projections", entry)
		}
		f.correct(t, i)
	}
	first := f.versions[0]
	if f.order(t, 0).State.EffectiveOrder().Status != domain.CatalogFactUnknown {
		t.Fatal("inferred original order")
	}
	accepted := f.decidePosition(t, first, 1)
	if accepted.Decision.View.Status != domain.CatalogFactAccepted {
		t.Fatal("explicit position not accepted")
	}
	added := f.addVersion(t, 0)
	state := f.order(t, 0).State
	if state.Compare(first, added) != domain.VersionOrderingUnknown || state.EffectiveOrder().Status != domain.CatalogFactUnknown || len(state.EffectiveOrder().Positions) != 1 || state.EffectiveOrder().Positions[0].Entry.Version != first.ID() {
		t.Fatal("growth lost prior position or inferred latest")
	}
	f.decidePosition(t, added, 1)
	state = f.order(t, 0).State
	if state.EffectiveOrder().Status != domain.CatalogFactConflict || state.Compare(first, added) != domain.VersionOrderingConflict {
		t.Fatal("contradictory sequence silently chose a winner")
	}
	query := app.Query{Kind: app.FindVersionsByPair, Fingerprint: f.pair, Page: app.PageRequest{Limit: 1}}
	page, err := catalogRead(t, f.owner, query)
	if err != nil || page.Kind != app.Incomplete || page.Complete || page.Next == "" {
		t.Fatal("partial ambiguity", page, err)
	}
	query.Page.Limit = 128
	want, err := catalogRead(t, f.owner, query)
	if err != nil || want.Kind != app.Ambiguous || !want.Complete || len(want.Entries) != 3 {
		t.Fatal("shared fingerprints merged", want, err)
	}
	seen := make(map[identity.ContentVersionID]bool)
	for _, entry := range want.Entries {
		if entry.Media != app.CatalogOnly || entry.Version.Fingerprint() != f.pair || entry.Version.OrderEvidence() != domain.UnknownVersionOrderEvidence() || seen[entry.Version.ID()] {
			t.Fatal("unbound immutable tuple changed", entry)
		}
		seen[entry.Version.ID()] = true
	}
	for _, version := range f.versions {
		if !seen[version.ID()] {
			t.Fatal("missing distinct identity", version)
		}
	}
	for _, table := range []string{"tc_blobs", "tc_version_blobs", "tc_blob_imports"} {
		if len(registrationSchemaRows(t, f.owner.database.db)[table]) != 0 {
			t.Fatal("registration performed media I/O", table)
		}
	}
	f.replays(t)
	f.assertStaged(t)
	before := catalogRecoveryRows(t, f.owner.database.db)
	snapshot, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "registered.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal("verify source snapshot", err)
	}
	config := f.owner.config
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.owner, err = OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	f.bind(t)
	for _, phase := range []string{"restart", "verified restore"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "verified restore" {
				f.addVersion(t, 1)
				absent := f.creations[len(f.creations)-1]
				f.creations = f.creations[:len(f.creations)-1]
				if reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
					t.Fatal("post-backup command did not commit")
				}
				if err := f.owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "selected.sqlite")); err != nil {
					t.Fatal(err)
				}
				f.bind(t)
				got, found, err := f.service.ReadRegistration(t.Context(), absent.kind, absent.command)
				if err != nil || found || !reflect.DeepEqual(got, domain.CatalogRegistrationResult{}) {
					t.Fatal("restored absent command fabricated result", got, found, err)
				}
			}
			result, err := catalogRead(t, f.owner, query)
			if err != nil || result.Kind != want.Kind || !reflect.DeepEqual(result.Entries, want.Entries) {
				t.Fatal("query changed across lifecycle", err)
			}
			stale := query
			stale.Page = app.PageRequest{Limit: 1, Cursor: page.Next, ExpectedRevision: page.Revision}
			if _, err := catalogRead(t, f.owner, stale); !errors.Is(err, app.ErrStaleCursor) {
				t.Fatal("old owner cursor remained valid", err)
			}
			f.replays(t)
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
				t.Fatal("lifecycle changed exact rows, migration checksums or retained evidence")
			}
			if err := verifyForeignKeys(t.Context(), f.owner.database.db); err != nil {
				t.Fatal(err)
			}
			if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
				t.Fatal("original backup altered", err)
			}
		})
	}
	t.Log("F01-D command-authored query/review/order/restart/restore evidence; F02 and final PI-08/T acceptance remain separate")
}

func TestRegistrationManifestAndMedia(t *testing.T) {
	t.Run("supplied known original order", func(t *testing.T) {
		f := newRegistrationIntegrationFixture(t)
		command := f.creations[2].command
		command.ID = registrationIntegrationCommandID(t)
		command.Expected = f.order(t, 0).AggregateRevision
		var err error
		command.OriginalOrder, err = domain.NewVersionOrderEvidence("unsupported-original", 1)
		if err != nil {
			t.Fatal(err)
		}
		before := catalogRecoveryRows(t, f.owner.database.db)
		if _, err := f.service.RegisterCatalogVersion(t.Context(), command); !errors.Is(err, app.ErrUnsupported) {
			t.Fatal("known original order admitted", err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
			t.Fatal("unsupported order wrote rows")
		}
	})
	t.Run("immutable tuple reuse", func(t *testing.T) {
		f := newRegistrationIntegrationFixture(t)
		command := f.creations[2].command
		command.ID = registrationIntegrationCommandID(t)
		var err error
		command.Subject, err = domain.VersionSubject(f.contents[1].ContentID(), command.Subject.VersionID())
		if err != nil {
			t.Fatal(err)
		}
		command.Expected = f.order(t, 1).AggregateRevision
		before := catalogRecoveryRows(t, f.owner.database.db)
		if _, err := f.service.RegisterCatalogVersion(t.Context(), command); !errors.Is(err, app.ErrCommandConflict) {
			t.Fatal("version reassigned to another content", err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
			t.Fatal("identity collision wrote rows")
		}
	})
	t.Run("exact PI-07 attachment/replay and verified restore", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("descriptor-backed contentfs requires Linux")
		}
		f := newRegistrationIntegrationFixture(t)
		parent := os.Getenv("TEDDYCLOUD_CONTENTFS_TEST_ROOT")
		if parent == "" {
			parent = "/var/tmp"
		}
		root, err := os.MkdirTemp(parent, "tc-registration-media-")
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
		command, err := content.NewImportCommand(f.versions[0], content.NewBlobID(f.generated.BlobDigest()), f.generated.CompleteBytes(), content.TAFProfileV1)
		if err != nil {
			t.Fatal(err)
		}
		key, err := content.ParseImportKey(registrationIntegrationID(t, "imp"))
		if err != nil {
			t.Fatal(err)
		}
		readMedia := func(version domain.ContentVersion, state app.MediaState) {
			t.Helper()
			result, err := catalogRead(t, f.owner, app.Query{Kind: app.GetVersion, Content: version.ContentID(), Version: version.ID(), Page: app.PageRequest{Limit: 1}})
			if err != nil || result.Kind != app.Found || len(result.Entries) != 1 || result.Entries[0].Version != version || result.Entries[0].Media != state {
				t.Fatal("exact tuple/media projection", result, state, err)
			}
		}
		readMedia(command.Version(), app.CatalogOnly)
		readMedia(f.versions[1], app.CatalogOnly)
		want, err := media.Import(t.Context(), key, command, f.generated.Open(), content.FiniteTAFSource)
		if err != nil || want != importResult(command) {
			t.Fatal("attach exact registered version", want, err)
		}
		if err := media.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
			t.Fatal("fresh full envelope verification", err)
		}
		readMedia(command.Version(), app.MediaBoundUnchecked)
		// Sharing a fingerprint does not attach the independently registered version.
		readMedia(f.versions[1], app.CatalogOnly)
		canonical := command.CanonicalBytes()
		retained := registrationSchemaRows(t, f.owner.database.db)
		assignmentID, err := assignment.ParseID(registrationIntegrationID(t, "asn"))
		if err != nil {
			t.Fatal(err)
		}
		tag, err := identity.ParseTagID(registrationIntegrationID(t, "tag"))
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
		interval, err := assignment.NewInterval(at, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		assigned, err := assignment.New(assignmentID, 1, tag, command.Version().ID(), interval)
		if err != nil {
			t.Fatal(err)
		}
		history, err := assignment.NewHistory([]assignment.Assignment{assigned})
		if err != nil {
			t.Fatal(err)
		}
		assignmentBefore := history.Entries()
		f.correct(t, 0)
		f.correct(t, 1)
		f.decidePosition(t, command.Version(), 1)
		for _, field := range []string{"content", "audio", "hash", "order", "rebinding"} {
			t.Run("changed tuple/"+field, func(t *testing.T) {
				version := command.Version()
				owner, pair, order, blob := version.ContentID(), version.Fingerprint(), version.OrderEvidence(), command.BlobID()
				var err error
				switch field {
				case "content":
					owner = f.contents[1].ContentID()
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
					blob = content.NewBlobID(sha256.Sum256([]byte("different generated envelope")))
				}
				if err != nil {
					t.Fatal(err)
				}
				version, err = domain.NewContentVersion(version.ID(), owner, pair, order)
				if err != nil {
					t.Fatal(err)
				}
				changed, err := content.NewImportCommand(version, blob, command.CompleteBytes(), command.Profile())
				if err != nil {
					t.Fatal(err)
				}
				before := catalogRecoveryRows(t, f.owner.database.db)
				// Exercise a fresh PI-07 key as well as the original receipt key.
				fresh, err := content.ParseImportKey(registrationIntegrationID(t, "imp"))
				if err != nil {
					t.Fatal(err)
				}
				for _, candidate := range []content.ImportKey{key, fresh} {
					if _, err := blobRecord(t, f.owner, candidate, changed); !errors.Is(err, contentstore.ErrConflict) {
						t.Fatal("changed tuple/rebinding admitted", err)
					}
					if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
						t.Fatal("rejected PI-07 attachment wrote rows")
					}
				}
			})
		}
		assertOriginal := func() {
			t.Helper()
			before := catalogRecoveryRows(t, f.owner.database.db)
			got, found, err := media.LookupImport(t.Context(), key, command)
			if err != nil || !found || got != want {
				t.Fatal("original receipt readback", got, found, err)
			}
			got, err = media.Import(t.Context(), key, command, f.generated.Open(), content.FiniteTAFSource)
			if err != nil || got != want || !bytes.Equal(canonical, command.CanonicalBytes()) {
				t.Fatal("original PI-07 canonical replay", got, err)
			}
			if err := media.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
				t.Fatal("retained media verification", err)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
				t.Fatal("exact media replay rewrote catalog or receipts")
			}
			rows := registrationSchemaRows(t, f.owner.database.db)
			for _, table := range []string{"tc_blob_imports", "tc_version_blobs", "tc_content_versions", "tc_blobs"} {
				if !reflect.DeepEqual(retained[table], rows[table]) {
					t.Fatal("correction/lifecycle changed immutable media facts", table)
				}
			}
			current, ok := history.CurrentAt(at)
			if !reflect.DeepEqual(assignmentBefore, history.Entries()) || !ok || current != assigned || current.ContentVersionID() != command.Version().ID() {
				t.Fatal("stable domain assignment changed")
			}
			readMedia(command.Version(), app.MediaBoundUnchecked)
			readMedia(f.versions[1], app.CatalogOnly)
			f.replays(t)
			f.assertStaged(t)
		}
		assertOriginal()
		before := catalogRecoveryRows(t, f.owner.database.db)
		snapshot, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "attached.sqlite"), SchemaMigrations())
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
		f.bind(t)
		media, err = contentstore.NewService(t.Context(), f.owner, store)
		if err != nil {
			t.Fatal(err)
		}
		assertOriginal()
		if err := f.owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "attached-restored.sqlite")); err != nil {
			t.Fatal(err)
		}
		f.bind(t)
		assertOriginal()
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
			t.Fatal("verified restore changed exact attached rows")
		}
		if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
			t.Fatal("original backup changed", err)
		}
		before = catalogRecoveryRows(t, f.owner.database.db)
		// Deliberate corruption of this generated test blob, then quarantine it.
		digest := command.BlobID().String()[7:]
		path := filepath.Join(root, "blobs", "sha256", digest[:2], digest[2:4], digest+".taf")
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteAt([]byte{0}, 4096)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(writeErr, closeErr)
		}
		if err := media.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrCorrupt) || errors.Is(err, contentstore.ErrMissing) || errors.Is(err, contentstore.ErrUnavailable) {
			t.Fatal("corrupt media taxonomy", err)
		}
		readMedia(command.Version(), app.MediaBoundUnchecked)
		if err := media.Quarantine(t.Context(), command.BlobID(), command.CompleteBytes()); err != nil {
			t.Fatal(err)
		}
		if err := media.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrMissing) || errors.Is(err, contentstore.ErrCorrupt) || errors.Is(err, contentstore.ErrUnavailable) {
			t.Fatal("missing media taxonomy", err)
		}
		readMedia(command.Version(), app.MediaBoundUnchecked)
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
			t.Fatal("media verification/quarantine rewrote catalog or receipts")
		}
		if err := f.owner.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := media.Availability(t.Context(), command.BlobID(), command.CompleteBytes()); !errors.Is(err, contentstore.ErrUnavailable) || errors.Is(err, contentstore.ErrMissing) || errors.Is(err, contentstore.ErrCorrupt) {
			t.Fatal("unavailable owner taxonomy", err)
		}
	})
}
