package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// This fence observes an authored prospective transaction and can roll it back
// for measurement. It never changes rows or bypasses a writer's admission checks.
type combinedAccountingFence struct {
	owner   *LifecycleOwner
	after   func(*catalogRepository) error
	failure error
}

func (f *combinedAccountingFence) WithinCatalogTransaction(ctx context.Context, operation func(app.CatalogRepository) error) error {
	f.failure = nil
	return f.owner.WithinCatalogTransaction(ctx, func(repository app.CatalogRepository) error {
		if err := operation(repository); err != nil {
			f.failure = err
			return err
		}
		if f.after != nil {
			return f.after(repository.(*catalogRepository))
		}
		return nil
	})
}

type combinedAccountingWriter struct {
	name string
	// CommitRegistration covers both registration APIs; CommitReview covers
	// RecordObservations and the fact-decision APIs (AcceptSupport below).
	kind string
	// These are the only tables this particular command may change.
	tables string
	// Per-content retained dimensions this writer can grow to a shared bound.
	// "from-absence" means the API can grow the dimension but cannot reach the
	// shared maximum because it creates a new aggregate. "none" means that the
	// writer does not grow the dimension at all.
	retained string
}

var combinedAccountingWriters = []combinedAccountingWriter{
	// Initial registration has one event, <=64 observations and <=5 selections;
	// it cannot approach the shared event/observation/link maxima from absence.
	{"RegisterContent", "content", "tc_catalog_content tc_catalog_managed tc_catalog_subjects tc_catalog_observations tc_catalog_registrations tc_catalog_registration_sources tc_catalog_registration_support tc_catalog_review_states_v2 tc_catalog_order_states_v2", "bytes; events/observations/support from-absence"},
	{"RegisterCatalogVersion", "version", "tc_content_versions tc_catalog_managed tc_catalog_subjects tc_catalog_observations tc_catalog_registrations tc_catalog_registration_sources tc_catalog_registration_support tc_catalog_review_states_v2 tc_catalog_order_states_v2 tc_catalog_order_state_versions_v2", "bytes events observations support"},
	{"RecordObservations", "review", "tc_catalog_managed tc_catalog_subjects tc_catalog_observations tc_catalog_decisions tc_catalog_review_states_v2", "bytes events observations; support none"},
	// Fact decisions require their retained source/observations: no global growth.
	{"AcceptSupport", "decision", "tc_catalog_managed tc_catalog_subjects tc_catalog_decisions tc_catalog_decision_support tc_catalog_review_states_v2", "bytes events support; observations none"},
	{"DecideOrder", "order", "tc_catalog_managed tc_catalog_order_states_v2 tc_catalog_order_state_versions_v2", "bytes events observations support"},
	// Adoption initializes unreviewed states: zero events/observations/support.
	{"AdoptBaseline", "adoption", "tc_catalog_managed tc_catalog_subjects tc_catalog_review_states tc_catalog_order_states", "bytes from-absence; events/observations/support none"},
	// Import has no per-content retained charge; only global provenance grows.
	{"Import", "import", "tc_catalog_imports", "bytes/events/observations/support none"},
	{"SupersedeVersion", "supersession", "tc_catalog_managed tc_catalog_supersessions tc_catalog_supersession_evidence tc_catalog_supersession_support", "bytes events observations support"},
}

func combinedAccountingAssertRows(t *testing.T, writer combinedAccountingWriter, before, after map[string][][]any, observations int) {
	t.Helper()
	want := map[string]int{"tc_catalog_sources": 1, "tc_catalog_source_claims": 5, "tc_catalog_batches": 1, "tc_catalog_records": 1}
	switch writer.kind {
	case "content", "version":
		want["tc_catalog_subjects"], want["tc_catalog_registrations"], want["tc_catalog_registration_sources"], want["tc_catalog_review_states_v2"], want["tc_catalog_order_states_v2"] = 1, 1, 1, 1, 1
		want["tc_catalog_observations"] = observations
		want["tc_catalog_registration_support"] = 1
		if writer.kind == "content" {
			want["tc_catalog_content"], want["tc_catalog_managed"] = 1, 1
		} else {
			want["tc_content_versions"], want["tc_catalog_registration_support"] = 1, 2
			want["tc_catalog_order_state_versions_v2"] = len(after["tc_content_versions"])
		}
	case "review", "decision":
		want["tc_catalog_decisions"], want["tc_catalog_review_states_v2"] = 1, 1
		if writer.kind == "review" {
			want["tc_catalog_observations"] = observations
		} else {
			want["tc_catalog_decision_support"] = 1
			want["tc_catalog_sources"], want["tc_catalog_source_claims"] = 0, 0
			want["tc_catalog_batches"], want["tc_catalog_records"] = 0, 0
		}
	case "order":
		want["tc_catalog_order_states_v2"] = 1
		want["tc_catalog_order_state_versions_v2"] = len(after["tc_content_versions"])
	case "adoption":
		want["tc_catalog_source_claims"], want["tc_catalog_records"] = 4, 2
		want["tc_catalog_managed"], want["tc_catalog_subjects"], want["tc_catalog_review_states"], want["tc_catalog_order_states"] = 1, 1, 1, 1
	case "import":
		want["tc_catalog_imports"] = 1
	case "supersession":
		want["tc_catalog_supersessions"] = 1
		want["tc_catalog_supersession_evidence"], want["tc_catalog_supersession_support"] = observations, observations
	default:
		t.Fatal("unmapped row charges", writer.name)
	}
	for table, rows := range before {
		if delta := len(after[table]) - len(rows); delta != want[table] {
			t.Fatal("unexpected row charge", writer.name, table, delta, want[table])
		}
	}
}

func combinedAccountingFixture(t *testing.T) supersessionFixture {
	t.Helper()
	f := newSupersessionFixture(t)
	if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, 103, 3)); err != nil {
		t.Fatal(err)
	}
	f.command.ExpectedAggregate, _ = domain.NewCatalogRevision(4)
	if _, err := f.sup.SupersedeVersion(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	// Adoption starts from a real legacy Save, never an SQL-seeded baseline.
	baseline, _ := domain.NewContent(catalogQueryContent(t, 200), domain.NewContentFacts("saved baseline", domain.ProductIdentifiers{}))
	if err := f.owner.WithinTransaction(t.Context(), func(r app.ContentRepository) error { return r.Save(t.Context(), baseline) }); err != nil {
		t.Fatal(err)
	}
	return f
}

func combinedAccountingPending(t *testing.T, f supersessionFixture, writer combinedAccountingWriter, fence *combinedAccountingFence, observationCount ...int) func() (any, error) {
	t.Helper()
	acquisition := domain.CoreOperator
	if writer.kind == "adoption" {
		acquisition = domain.BaselineSnapshot
	}
	s := newRegistrationIntegrationSource(t, f.review.Reviewer(), acquisition)
	w := registrationFixture{owner: f.owner, source: s.source, ingest: s.ingest, review: s.review, namespace: s.space, command: f.registrationFixture.command}
	w.capability, _ = domain.NewCatalogRegistrationCapability(s.review, []domain.ReviewCapability{s.review})
	base, err := app.NewService(fence, s.source, s.review, s.space)
	if err != nil {
		t.Fatal(err)
	}
	if writer.kind == "decision" {
		base, err = app.NewService(fence, f.source, f.review, f.namespace)
		if err != nil {
			t.Fatal(err)
		}
	}
	var registration app.RegistrationService
	if writer.kind == "content" || writer.kind == "version" {
		registration, err = app.NewRegistrationService(base, w.capability)
		if err != nil {
			t.Fatal(err)
		}
	}
	id, _ := domain.NewCatalogCommandID()
	var aggregate, reviewRevision, orderRevision uint64
	if err := f.owner.database.db.QueryRowContext(t.Context(), `SELECT aggregate_revision,(SELECT review_revision FROM tc_catalog_subjects WHERE subject_id=?1),(SELECT max(order_revision) FROM tc_catalog_order_states_v2 WHERE content_id=?1) FROM tc_catalog_managed WHERE content_id=?1`, f.command.Content.String()).Scan(&aggregate, &reviewRevision, &orderRevision); err != nil {
		t.Fatal(err)
	}
	expectedAggregate, _ := domain.NewCatalogRevision(aggregate)
	expectedReview, _ := domain.NewCatalogRevision(reviewRevision)
	claim, _ := domain.TitleClaim("matrix evidence")
	o := w.observation(t, w.command.Subject, claim)
	count := 1
	if writer.kind == "version" {
		count = 2
	}
	if len(observationCount) != 0 {
		count = observationCount[0]
	}
	copyObservation := func(original domain.CatalogObservation) domain.CatalogObservation {
		input := original.Input()
		input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
		copied, err := domain.NewCatalogObservation(w.ingest, w.source, input)
		if err != nil {
			t.Fatal(err)
		}
		return copied
	}
	switch writer.kind {
	case "content":
		subject, _ := domain.ContentSubject(catalogQueryContent(t, 201))
		o = w.observation(t, subject, claim)
		c := app.RegistrationCommand{ID: id, Subject: subject, ExpectedKind: domain.CatalogExpectedAbsent, Observations: []domain.CatalogObservation{o}, Selections: []domain.CatalogRegistrationSelection{{Key: domain.TitleClaimKey, Observation: o.ID()}}, Reason: "matrix content"}
		return func() (any, error) { return registration.RegisterContent(t.Context(), c) }
	case "version":
		c := w.version(t, 999, aggregate)
		// Share expectedReview real coordinate: the writer adds expectedReview batch and expectedReview record.
		input := c.Observations[1].Input()
		input.Coordinate = c.Observations[0].Input().Coordinate
		c.Observations[1], err = domain.NewCatalogObservation(w.ingest, w.source, input)
		if err != nil {
			t.Fatal(err)
		}
		for len(c.Observations) < count {
			c.Observations = append(c.Observations, copyObservation(c.Observations[1]))
		}
		return func() (any, error) { return registration.RegisterCatalogVersion(t.Context(), c) }
	case "review", "decision":
		c := app.ReviewCommand{ID: id, Subject: w.command.Subject, ExpectedAggregate: expectedAggregate, ExpectedReview: expectedReview, Observations: []domain.CatalogObservation{o}, Reason: "matrix review"}
		for len(c.Observations) < count {
			c.Observations = append(c.Observations, copyObservation(o))
		}
		if writer.kind == "decision" {
			c.Observations = nil
			c.Key, c.Support = domain.TitleClaimKey, []evidence.ID{f.registrationFixture.command.Observations[0].ID()}
			return func() (any, error) { return base.AcceptSupport(t.Context(), c) }
		}
		return func() (any, error) { return base.RecordObservations(t.Context(), c) }
	case "order":
		expectedOrder, _ := domain.NewCatalogRevision(orderRevision)
		sequence, e := domain.NewCatalogSequenceEvidence(w.ingest, w.namespace, domain.CatalogSequenceInput{ID: o.ID(), Content: f.command.Content, Version: f.command.New.ID(), Position: 1, Coordinate: o.Input().Coordinate})
		if e != nil {
			t.Fatal(e)
		}
		c := app.OrderCommand{ID: id, Content: f.command.Content, ExpectedAggregate: expectedAggregate, ExpectedOrder: expectedOrder, Evidence: []domain.CatalogSequenceEvidence{sequence}, Entries: []domain.CatalogOrderEntry{{Version: f.command.New.ID(), Position: 1, Support: []evidence.ID{o.ID()}}}, Reason: "matrix order"}
		for len(c.Evidence) < count {
			input := sequence.Input()
			input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
			item, err := domain.NewCatalogSequenceEvidence(w.ingest, w.namespace, input)
			if err != nil {
				t.Fatal(err)
			}
			c.Evidence = append(c.Evidence, item)
			c.Entries[0].Support = append(c.Entries[0].Support, item.Input().ID)
		}
		return func() (any, error) { return base.DecideOrder(t.Context(), c) }
	case "adoption":
		baseline, revision, e := base.ReadBaseline(t.Context(), catalogQueryContent(t, 200))
		if e != nil {
			t.Fatal(e)
		}
		c := app.AdoptionCommand{ID: id, Content: baseline.Content().ID(), ExpectedAggregate: revision, BaselineSHA256: baseline.SHA256(), Reason: "matrix adoption"}
		return func() (any, error) { return base.AdoptBaseline(t.Context(), c) }
	case "import":
		service, e := app.NewImportService(fence, w.source, w.ingest)
		if e != nil {
			t.Fatal(e)
		}
		batch, _ := domain.NewBatchID()
		report := importReport(t, `{"format":"catalog-offline-v1","records":[{"record":"matrix","kind":"public","title":"safe"}]}`)
		return func() (any, error) { return service.Import(t.Context(), batch, report) }
	case "supersession":
		capability, e := domain.NewCatalogSupersessionCapability(w.review)
		if e != nil {
			t.Fatal(e)
		}
		service, e := app.NewSupersessionService(base, capability)
		if e != nil {
			t.Fatal(e)
		}
		third, _ := domain.NewContentVersion(catalogQueryVersion(t, 103), f.command.Content, f.command.New.Fingerprint(), domain.UnknownVersionOrderEvidence())
		var revision uint64
		if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*)+1 FROM tc_catalog_supersessions WHERE content_id=?", f.command.Content.String()).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		c := f.link(t, f.command.New, third, aggregate, revision)
		input := c.Evidence[0].Input()
		input.Coordinate = o.Input().Coordinate
		c.Evidence[0], e = domain.NewCatalogSupersessionEvidence(w.ingest, w.source, input)
		if e != nil {
			t.Fatal(e)
		}
		c.Support = []evidence.ID{c.Evidence[0].ID()}
		for len(c.Evidence) < count {
			input := c.Evidence[0].Input()
			input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
			item, err := domain.NewCatalogSupersessionEvidence(w.ingest, w.source, input)
			if err != nil {
				t.Fatal(err)
			}
			c.Evidence = append(c.Evidence, item)
			c.Support = append(c.Support, item.ID())
		}
		return func() (any, error) { return service.SupersedeVersion(t.Context(), c) }
	default:
		t.Fatal("unmapped writer", writer.name)
		return nil
	}
}

type combinedGlobalUsage struct{ sources, batches, bytes uint64 }

func combinedAccountingGlobal(t *testing.T, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) combinedGlobalUsage {
	t.Helper()
	var u combinedGlobalUsage
	if err := db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM tc_catalog_sources),(SELECT count(*) FROM tc_catalog_batches),(SELECT COALESCE(sum(length(snapshot)),0) FROM tc_catalog_records)+(SELECT COALESCE(sum(length(artifact)+length(report)),0) FROM tc_catalog_imports)`).Scan(&u.sources, &u.batches, &u.bytes); err != nil {
		t.Fatal(err)
	}
	return u
}

// Every writer can add a source; fact decisions only cite retained records.
// All other rows exercise a new staging batch/record. Import cannot grow
// per-content bytes/events/observations/support; adoption initializes empty
// review/order history. RegisterContent has no preexisting per-content history.
// The global rows below are combined F01 + F02 + import provenance, all authored.
func TestSupersessionSQLiteCombinedWriterAccounting(t *testing.T) {
	for _, writer := range combinedAccountingWriters {
		resources := []domain.Resource{domain.SourceRegistrations, domain.StagingBatches, domain.StagingBytes}
		if writer.kind == "decision" {
			resources = nil
		}
		for _, resource := range resources {
			name := map[domain.Resource]string{domain.SourceRegistrations: "Sources", domain.StagingBatches: "StagingBatches", domain.StagingBytes: "StagingBytes"}[resource]
			for _, extra := range []uint64{0, 1} {
				t.Run(writer.name+"/"+name+fmt.Sprintf("/over_%d", extra), func(t *testing.T) {
					f := combinedAccountingFixture(t)
					fence := &combinedAccountingFence{owner: f.owner}
					write := combinedAccountingPending(t, f, writer, fence)
					before := catalogRecoveryRows(t, f.owner.database.db)
					start := combinedAccountingGlobal(t, f.owner.database.db)
					rollback := errors.New("rollback authored measurement")
					var prospective combinedGlobalUsage
					fence.after = func(r *catalogRepository) error {
						prospective = combinedAccountingGlobal(t, r.transaction)
						return rollback
					}
					if _, err := write(); !errors.Is(err, app.ErrRepositoryUnavailable) || prospective == (combinedGlobalUsage{}) {
						t.Fatal("measure writer", err)
					}
					fence.after = nil
					if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
						t.Fatal("measurement leaked rows")
					}
					batches := uint64(1)
					if writer.kind == "decision" {
						batches = 0
					}
					if prospective.sources-start.sources != 1 || prospective.batches-start.batches != batches || (writer.kind != "decision" && prospective.bytes <= start.bytes) {
						t.Fatal("writer did not exercise new provenance", start, prospective)
					}
					delta := prospective.bytes - start.bytes
					fillSource := f.source
					fillIngest := f.ingest
					fill := func(padding int, freshSource bool) {
						if freshSource {
							s := newRegistrationIntegrationSource(t, f.review.Reviewer(), domain.CoreOperator)
							base, err := app.NewService(f.owner, s.source, s.review, s.space)
							if err != nil {
								t.Fatal(err)
							}
							capability, err := domain.NewCatalogRegistrationCapability(s.review, []domain.ReviewCapability{s.review, f.review})
							if err != nil {
								t.Fatal(err)
							}
							service, err := app.NewRegistrationService(base, capability)
							if err != nil {
								t.Fatal(err)
							}
							content, _ := identity.ParseContentID(registrationIntegrationID(t, "cnt"))
							subject, _ := domain.ContentSubject(content)
							input := f.registrationFixture.command.Observations[0].Input()
							input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
							input.Subject = subject
							observation, err := domain.NewCatalogObservation(f.ingest, f.source, input)
							if err != nil {
								t.Fatal(err)
							}
							id, _ := domain.NewCatalogCommandID()
							if _, err := service.RegisterContent(t.Context(), app.RegistrationCommand{ID: id, Subject: subject, ExpectedKind: domain.CatalogExpectedAbsent, Observations: []domain.CatalogObservation{observation}, Selections: []domain.CatalogRegistrationSelection{{Key: domain.TitleClaimKey, Observation: observation.ID()}}, Reason: "author source without another batch"}); err != nil {
								t.Fatal(err)
							}
							return
						}
						service, err := app.NewImportService(f.owner, fillSource, fillIngest)
						if err != nil {
							t.Fatal(err)
						}
						report := importReport(t, `{"format":"catalog-offline-v1","records":[{"record":"filler","kind":"public","title":"safe"}]}`+strings.Repeat(" ", padding))
						batch, _ := domain.NewBatchID()
						if _, err := service.Import(t.Context(), batch, report); err != nil {
							t.Fatal("author filler", err)
						}
					}
					switch resource {
					case domain.SourceRegistrations, domain.StagingBatches:
						for {
							u := combinedAccountingGlobal(t, f.owner.database.db)
							current := u.batches
							if resource == domain.SourceRegistrations {
								current = u.sources
							}
							if current == resource.Maximum()-1+extra {
								break
							}
							fill(0, resource == domain.SourceRegistrations)
						}
					case domain.StagingBytes:
						minimal := importReport(t, `{"format":"catalog-offline-v1","records":[{"record":"filler","kind":"public","title":"safe"}]}`)
						payload, err := minimal.CatalogImportPayload(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						base := payload.RetainedBytes()
						for {
							current := combinedAccountingGlobal(t, f.owner.database.db).bytes
							remaining := resource.Maximum() - delta + extra - current
							if remaining == 0 {
								break
							}
							if remaining < base {
								t.Fatal("unfillable staging remainder", remaining, base)
							}
							padding := min(remaining-base, domain.BatchBytes.Maximum()-uint64(len(minimal.ArtifactBytes()))-base)
							if remaining-base-padding > 0 && remaining-base-padding < base {
								padding -= base
							}
							fill(int(padding), false)
						}
					}
					before = catalogRecoveryRows(t, f.owner.database.db)
					u := combinedAccountingGlobal(t, f.owner.database.db)
					prospectiveTotal := u.sources + 1
					if resource == domain.StagingBatches {
						prospectiveTotal = u.batches + 1
					}
					if resource == domain.StagingBytes {
						prospectiveTotal = u.bytes + delta
					}
					if prospectiveTotal != resource.Maximum()+extra {
						t.Fatal("fixture is not exactly at requested global boundary", resource, prospectiveTotal)
					}
					result, err := write()
					if extra != 0 {
						var limit domain.LimitError
						if !errors.Is(err, app.ErrLimitExceeded) || !errors.As(fence.failure, &limit) || limit.Resource != resource {
							t.Fatal("one unit over", resource, err, fence.failure)
						}
						if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
							t.Fatal("rejection leaked rows", resource)
						}
						return
					}
					if err != nil {
						t.Fatal("exact combined boundary", resource, u, delta, err)
					}
					after := catalogRecoveryRows(t, f.owner.database.db)
					n := 1
					if writer.kind == "version" {
						n = 2
					}
					combinedAccountingAssertRows(t, writer, before, after, n)
					got := combinedAccountingGlobal(t, f.owner.database.db)
					if got != (combinedGlobalUsage{u.sources + 1, u.batches + batches, u.bytes + delta}) {
						t.Fatal("writer charged provenance more than once", u, got, delta)
					}
					for table, rows := range before {
						allowed := strings.Fields(writer.tables + " tc_catalog_sources tc_catalog_source_claims tc_catalog_batches tc_catalog_records")
						if !slices.Contains(allowed, table) && !reflect.DeepEqual(rows, after[table]) {
							t.Fatal("changed unrelated table", table)
						}
					}
					replay, err := write()
					if err != nil || !reflect.DeepEqual(result, replay) || !reflect.DeepEqual(after, catalogRecoveryRows(t, f.owner.database.db)) {
						t.Fatal("replay changed result/rows/accounting", err)
					}
				})
			}
		}
	}
}

type supersessionFixture struct {
	registrationFixture
	cap     domain.CatalogSupersessionCapability
	sup     app.SupersessionService
	command app.SupersedeVersionCommand
}

func newSupersessionFixture(t *testing.T) supersessionFixture {
	t.Helper()
	f := supersessionFixture{registrationFixture: newRegistrationFixture(t)}
	if _, err := f.service.RegisterContent(t.Context(), f.registrationFixture.command); err != nil {
		t.Fatal(err)
	}
	for n := 101; n <= 102; n++ {
		if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, n, uint64(n-100))); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.cap, err = domain.NewCatalogSupersessionCapability(f.review)
	if err != nil {
		t.Fatal(err)
	}
	f.sup, err = app.NewSupersessionService(f.base, f.cap)
	if err != nil {
		t.Fatal(err)
	}
	page, err := catalogRead(t, f.owner, app.Query{Kind: app.ListVersions, Content: f.registrationFixture.command.Subject.ContentID(), Page: app.PageRequest{Limit: 128}})
	if err != nil || len(page.Entries) != 2 {
		t.Fatal(page, err)
	}
	f.command = f.link(t, page.Entries[0].Version, page.Entries[1].Version, 3, 1)
	return f
}

func (f supersessionFixture) link(t *testing.T, old, newer domain.ContentVersion, aggregate, revision uint64) app.SupersedeVersionCommand {
	t.Helper()
	id, _ := domain.NewCatalogCommandID()
	a, _ := domain.NewCatalogRevision(aggregate)
	s, _ := domain.NewCatalogRevision(revision)
	one, _ := domain.NewCatalogRevision(1)
	claim, _ := domain.OwnershipClaim(old.ContentID())
	subject, _ := domain.VersionSubject(old.ContentID(), newer.ID())
	o := f.observation(t, subject, claim)
	e, err := domain.NewCatalogSupersessionEvidence(f.ingest, f.source, domain.CatalogSupersessionEvidenceInput{ID: o.ID(), Content: old.ContentID(), OldVersion: old.ID(), NewVersion: newer.ID(), Coordinate: o.Input().Coordinate, ObservedAt: o.Input().ObservedAt})
	if err != nil {
		t.Fatal(err)
	}
	return app.SupersedeVersionCommand{ID: id, Content: old.ContentID(), Old: old, New: newer, ExpectedAggregate: a, ExpectedSupersession: s, ExpectedOldReview: one, ExpectedNewReview: one, Evidence: []domain.CatalogSupersessionEvidence{e}, Support: []evidence.ID{e.ID()}, Reason: "explicit replacement"}
}

func (f supersessionFixture) op(c app.SupersedeVersionCommand) app.SupersessionOperation {
	return app.SupersessionOperation{Capability: f.cap, Command: domain.CatalogSupersessionCommand{ID: c.ID, Content: c.Content, Old: c.Old, New: c.New, ExpectedAggregate: c.ExpectedAggregate, ExpectedSupersession: c.ExpectedSupersession, ExpectedOldReview: c.ExpectedOldReview, ExpectedNewReview: c.ExpectedNewReview, Source: f.source, Evidence: c.Evidence, Support: c.Support, Reason: c.Reason}}
}

func TestSupersessionSQLiteReplay(t *testing.T) {
	f := newSupersessionFixture(t)
	before := registrationSchemaRows(t, f.owner.database.db)
	result, err := f.sup.SupersedeVersion(t.Context(), f.command)
	if err != nil {
		t.Fatal(err)
	}
	if result.AggregateRevision.Uint64() != 4 || result.SupersessionRevision.Uint64() != 2 || result.OrderRevision.Uint64() != 3 {
		t.Fatal(result)
	}
	after := registrationSchemaRows(t, f.owner.database.db)
	for table, rows := range before {
		switch table {
		case "tc_catalog_managed", "tc_catalog_sources", "tc_catalog_source_claims", "tc_catalog_batches", "tc_catalog_records", "tc_catalog_supersessions", "tc_catalog_supersession_evidence", "tc_catalog_supersession_support":
			continue
		}
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatal("changed unrelated table", table)
		}
	}
	// A subsequent registration extends the manifest without rewriting the edge.
	if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, 103, 4)); err != nil {
		t.Fatal("growth after edge", err)
	}
	page, err := catalogRead(t, f.owner, app.Query{Kind: app.ListVersions, Content: f.command.Content, Page: app.PageRequest{Limit: 128}})
	if err != nil || len(page.Entries) != 3 {
		t.Fatal(page, err)
	}
	third, _ := domain.NewContentVersion(catalogQueryVersion(t, 103), f.command.Content, f.command.New.Fingerprint(), domain.UnknownVersionOrderEvidence())
	if _, err := f.sup.SupersedeVersion(t.Context(), f.link(t, f.command.New, third, 5, 2)); err != nil {
		t.Fatal("chain extension", err)
	}
	before = registrationSchemaRows(t, f.owner.database.db)
	for _, readOnly := range []bool{false, true} {
		var got domain.CatalogSupersessionResult
		if readOnly {
			var found bool
			got, found, err = f.sup.ReadSupersession(t.Context(), f.command)
			if !found {
				t.Fatal("missing replay", err)
			}
		} else {
			got, err = f.sup.SupersedeVersion(t.Context(), f.command)
		}
		if err != nil || !reflect.DeepEqual(got, result) {
			t.Fatal("original result changed", got, err)
		}
	}
	if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
		t.Fatal("replay wrote or charged rows")
	}
	if page, err := f.sup.QuerySupersession(t.Context(), app.SupersessionQuery{Content: f.command.Content, Page: app.PageRequest{Limit: 128}}); err != nil || len(page.Entries) != 2 {
		t.Fatal("complete history", page, err)
	}
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), f.owner.config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	f.owner = owner
	f.bind(t)
	f.sup, err = app.NewSupersessionService(f.base, f.cap)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := f.sup.ReadSupersession(t.Context(), f.command)
	if err != nil || !found || !reflect.DeepEqual(got, result) {
		t.Fatal("reopen", got, found, err)
	}
}

func TestSupersessionSQLiteFences(t *testing.T) {
	for _, name := range []string{"aggregate", "supersession", "old review", "new review", "changed tuple", "wide audio", "missing version", "cross content", "missing support", "same key changed reason", "branch", "cycle", "target chain"} {
		t.Run(name, func(t *testing.T) {
			f := newSupersessionFixture(t)
			c := f.command
			want := app.ErrRevisionConflict
			stale, _ := domain.NewCatalogRevision(9)
			switch name {
			case "aggregate":
				c.ExpectedAggregate = stale
			case "supersession":
				c.ExpectedSupersession = stale
			case "old review":
				c.ExpectedOldReview = stale
			case "new review":
				c.ExpectedNewReview = stale
			case "changed tuple":
				order, _ := domain.NewVersionOrderEvidence("changed", 3)
				c.Old, _ = domain.NewContentVersion(c.Old.ID(), c.Content, c.Old.Fingerprint(), order)
				want = app.ErrCommandConflict
			case "wide audio":
				audio, _ := domain.NewAudioID(1 << 32)
				pair, _ := domain.NewAudioFingerprint(audio, c.New.Fingerprint().Hash())
				c.New, _ = domain.NewContentVersion(c.New.ID(), c.Content, pair, domain.UnknownVersionOrderEvidence())
				want = app.ErrUnsupported
			case "missing version":
				c.New, _ = domain.NewContentVersion(catalogQueryVersion(t, 999), c.Content, c.New.Fingerprint(), domain.UnknownVersionOrderEvidence())
				c = f.link(t, c.Old, c.New, 3, 1)
				want = app.ErrSubjectNotFound
			case "cross content":
				c.New, _ = domain.NewContentVersion(c.New.ID(), catalogQueryContent(t, 999), c.New.Fingerprint(), domain.UnknownVersionOrderEvidence())
				want = app.ErrSubjectMismatch
			case "missing support":
				c.Support = nil
				want = app.ErrUnsupported
			default:
				if _, err := f.sup.SupersedeVersion(t.Context(), c); err != nil {
					t.Fatal(err)
				}
				want = app.ErrCommandConflict
				if name == "same key changed reason" {
					c.Reason = "changed"
				} else {
					if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, 103, 4)); err != nil {
						t.Fatal(err)
					}
					third, _ := domain.NewContentVersion(catalogQueryVersion(t, 103), c.Content, c.Old.Fingerprint(), domain.UnknownVersionOrderEvidence())
					switch name {
					case "branch":
						c = f.link(t, c.Old, third, 5, 2)
					case "cycle":
						c = f.link(t, c.New, c.Old, 5, 2)
					case "target chain":
						c = f.link(t, third, c.New, 5, 2)
					}
				}
			}
			before := registrationSchemaRows(t, f.owner.database.db)
			if _, err := f.sup.SupersedeVersion(t.Context(), c); !errors.Is(err, want) {
				t.Fatal("unexpected failure", err, "want", want)
			}
			if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
				t.Fatal("rejected transition wrote rows")
			}
		})
	}
}

func TestSupersessionSQLiteAuthorityAtomicity(t *testing.T) {
	for _, failure := range []string{"before lookup live", "before lookup SQL", "before commit live", "before commit SQL", "cancel before", "cancel after", "callback", "swallowed", "leaked"} {
		t.Run(failure, func(t *testing.T) {
			f := newSupersessionFixture(t)
			before := registrationSchemaRows(t, f.owner.database.db)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := domain.ErrCatalogUnauthorized
			switch failure {
			case "before lookup live":
				f.source.Revoke()
			case "before lookup SQL":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET revoked=1")
				before = registrationSchemaRows(t, f.owner.database.db)
			case "cancel before":
				cancel()
				want = context.Canceled
			case "cancel after":
				want = context.Canceled
			case "callback", "swallowed":
				want = app.ErrRepositoryUnavailable
			}
			var leaked app.SupersessionRepository
			err := f.owner.WithinCatalogTransaction(ctx, func(repository app.CatalogRepository) error {
				leaked = repository.(app.SupersessionRepository)
				if failure == "before lookup live" || failure == "before lookup SQL" {
					_, _, e := leaked.LookupSupersession(ctx, f.op(f.command))
					return e
				}
				_, e := leaked.CommitSupersession(ctx, f.op(f.command))
				if e != nil {
					return e
				}
				switch failure {
				case "before commit live":
					f.source.Revoke()
				case "before commit SQL":
					_, e = repository.(*catalogRepository).transaction.ExecContext(ctx, "PRAGMA query_only=OFF")
					if e == nil {
						_, e = repository.(*catalogRepository).transaction.ExecContext(ctx, "UPDATE tc_catalog_sources SET revoked=1")
					}
					return e
				case "cancel after":
					cancel()
				case "callback":
					return want
				case "swallowed":
					_, _, _ = leaked.LookupSupersession(ctx, app.SupersessionOperation{})
				}
				return nil
			})
			if failure == "leaked" {
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := leaked.LookupSupersession(t.Context(), f.op(f.command)); !errors.Is(err, app.ErrRepositoryUnavailable) {
					t.Fatal("leaked port", err)
				}
				return
			}
			if failure == "swallowed" {
				want = domain.ErrCatalogUnauthorized
			}
			if !errors.Is(err, want) {
				t.Fatal(err, "want", want)
			}
			if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
				t.Fatal("failed operation changed rows")
			}
		})
	}
}

func TestSupersessionSQLiteRollbackStages(t *testing.T) {
	for _, stage := range []struct{ table, operation string }{{"tc_catalog_managed", "UPDATE"}, {"tc_catalog_batches", "INSERT"}, {"tc_catalog_records", "INSERT"}, {"tc_catalog_supersessions", "INSERT"}, {"tc_catalog_supersession_evidence", "INSERT"}, {"tc_catalog_supersession_support", "INSERT"}} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", stage.table, after), func(t *testing.T) {
				f := newSupersessionFixture(t)
				when := "BEFORE"
				if after {
					when = "AFTER"
				}
				catalogSchemaExec(t, f.owner.database.db, "CREATE TRIGGER supersession_failure "+when+" "+stage.operation+" ON "+stage.table+" BEGIN SELECT RAISE(ABORT,'injected'); END")
				before := registrationSchemaRows(t, f.owner.database.db)
				if _, err := f.sup.SupersedeVersion(t.Context(), f.command); !errors.Is(err, app.ErrRepositoryUnavailable) {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
					t.Fatal("partial mutation", stage.table)
				}
			})
		}
	}
}

func TestSupersessionSQLiteKeyCollisions(t *testing.T) {
	f := newSupersessionFixture(t)
	result, err := f.sup.SupersedeVersion(t.Context(), f.command)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := domain.NewCatalogRevision(1)
	four := result.AggregateRevision
	claim, _ := domain.TitleClaim("new title")
	observation := f.observation(t, f.registrationFixture.command.Subject, claim)
	review := app.ReviewCommand{ID: f.command.ID, Subject: f.registrationFixture.command.Subject, ExpectedAggregate: four, ExpectedReview: one, Observations: []domain.CatalogObservation{observation}, Reason: "review"}
	before := registrationSchemaRows(t, f.owner.database.db)
	if _, err := f.base.RecordObservations(t.Context(), review); !errors.Is(err, app.ErrCommandConflict) {
		t.Fatal("F02 key as review", err)
	}
	registration := f.version(t, 103, 4)
	registration.ID = f.command.ID
	if _, err := f.service.RegisterCatalogVersion(t.Context(), registration); !errors.Is(err, app.ErrCommandConflict) {
		t.Fatal("F02 key as registration", err)
	}
	sequence, err := domain.NewCatalogSequenceEvidence(f.ingest, f.namespace, domain.CatalogSequenceInput{ID: observation.ID(), Content: f.command.Content, Version: f.command.Old.ID(), Position: 1, Coordinate: observation.Input().Coordinate})
	if err != nil {
		t.Fatal(err)
	}
	three, _ := domain.NewCatalogRevision(3)
	order := app.OrderCommand{ID: f.command.ID, Content: f.command.Content, ExpectedAggregate: four, ExpectedOrder: three, Evidence: []domain.CatalogSequenceEvidence{sequence}, Entries: []domain.CatalogOrderEntry{{Version: f.command.Old.ID(), Position: 1, Support: []evidence.ID{sequence.Input().ID}}}, Reason: "order"}
	if _, err := f.base.DecideOrder(t.Context(), order); !errors.Is(err, app.ErrCommandConflict) {
		t.Fatal("F02 key as order", err)
	}
	af, _, _ := adoptionFixture(t)
	adopter, err := app.NewService(f.owner, af.source, af.review, af.namespace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adopter.AdoptBaseline(t.Context(), app.AdoptionCommand{ID: f.command.ID, Content: f.command.Content, ExpectedAggregate: one, BaselineSHA256: sha256.Sum256([]byte("baseline")), Reason: "adopt"}); !errors.Is(err, app.ErrCommandConflict) {
		t.Fatal("F02 key as adoption", err)
	}
	if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
		t.Fatal("collision changed rows")
	}
	for _, key := range []domain.CatalogCommandID{f.registrationFixture.command.ID, f.command.ID} {
		c := f.command
		c.ID = key
		c.Reason = "different operation"
		if _, err := f.sup.SupersedeVersion(t.Context(), c); !errors.Is(err, app.ErrCommandConflict) {
			t.Fatal("occupied key", err)
		}
	}
}

func TestSupersessionSQLiteCorruption(t *testing.T) {
	for _, kind := range []string{"command hash", "result hash", "column", "support", "evidence", "historical revision", "missing table"} {
		t.Run(kind, func(t *testing.T) {
			f := newSupersessionFixture(t)
			if _, err := f.sup.SupersedeVersion(t.Context(), f.command); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "command hash", "result hash", "column", "historical revision":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_supersessions_no_update")
				q := "UPDATE tc_catalog_supersessions SET command_sha256=randomblob(32)"
				if kind == "result hash" {
					q = "UPDATE tc_catalog_supersessions SET result_sha256=randomblob(32)"
				}
				if kind == "column" {
					q = "UPDATE tc_catalog_supersessions SET reviewer_principal='11111111111111111111111111111111'"
				}
				if kind == "historical revision" {
					q = "UPDATE tc_catalog_supersessions SET new_review_revision=2"
				}
				catalogSchemaExec(t, f.owner.database.db, q)
			case "support":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_supersession_support_no_delete")
				catalogSchemaExec(t, f.owner.database.db, "DELETE FROM tc_catalog_supersession_support")
			case "evidence":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_supersession_evidence_no_update")
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_supersession_evidence SET asserted_origin='changed'")
			case "missing table":
				catalogSchemaExec(t, f.owner.database.db, "DROP TABLE tc_catalog_supersession_support")
			}
			if _, _, err := f.sup.ReadSupersession(t.Context(), f.command); !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal("accepted corruption", err)
			}
			if _, err := catalogRead(t, f.owner, app.Query{Kind: app.GetContent, Content: f.command.Content, Page: app.PageRequest{Limit: 128}}); !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal("old query hid corruption", err)
			}
		})
	}
}

// Corruption only: SQLite's endpoint UNIQUE constraints are table constraints,
// so rebuild just this table, omitting the one guard needed for a branch/merge.
// Keep all rows, other constraints, explicit indexes and triggers intact.
func supersessionRemoveEndpointGuard(t *testing.T, f supersessionFixture, column string) {
	t.Helper()
	db := f.owner.database.db
	var ddl string
	if err := db.QueryRowContext(t.Context(), "SELECT sql FROM sqlite_schema WHERE name='tc_catalog_supersessions'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	guard := "UNIQUE(" + column + "),"
	if strings.Count(ddl, guard) != 1 {
		t.Fatal("missing endpoint guard", column)
	}
	objects := catalogSchemaRows(t, db, "SELECT sql FROM sqlite_schema WHERE tbl_name='tc_catalog_supersessions' AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type,name")
	catalogSchemaExec(t, db, "CREATE TEMP TABLE supersession_corruption_rows AS SELECT * FROM tc_catalog_supersessions")
	catalogSchemaExec(t, db, "DROP TABLE tc_catalog_supersessions")
	catalogSchemaExec(t, db, strings.Replace(ddl, guard, "", 1))
	catalogSchemaExec(t, db, "INSERT INTO tc_catalog_supersessions SELECT * FROM supersession_corruption_rows")
	for _, object := range objects {
		catalogSchemaExec(t, db, object[0].(string))
	}
	catalogSchemaExec(t, db, "DROP TABLE supersession_corruption_rows")
}

func TestSupersessionSQLitePersistedGraphShapes(t *testing.T) {
	for _, shape := range []string{"cycle", "branch", "merge"} {
		t.Run(shape, func(t *testing.T) {
			f, versions, commands, results := supersessionQueryFixture(t)
			db := f.owner.database.db
			before := catalogRecoveryRows(t, db)
			// Start with accepted A->B->C and D->E. Rewrite only the last
			// edge to C->A (cycle), A->E (branch) or D->C (merge).
			c, result := commands[2], results[2]
			switch shape {
			case "cycle":
				c.Old, c.New = versions[2], versions[0]
			case "branch":
				c.Old = versions[0]
			case "merge":
				c.New = versions[2]
			}
			c.Evidence = append([]domain.CatalogSupersessionEvidence(nil), c.Evidence...)
			for i, e := range c.Evidence {
				input := e.Input()
				input.OldVersion, input.NewVersion = c.Old.ID(), c.New.ID()
				var err error
				c.Evidence[i], err = domain.NewCatalogSupersessionEvidence(f.ingest, f.source, input)
				if err != nil {
					t.Fatal(err)
				}
			}
			result.Old, result.New = c.Old, c.New
			commandRaw, err := f.op(c).Command.CanonicalEncoding(f.cap)
			if err != nil {
				t.Fatal(err)
			}
			resultRaw, err := result.CanonicalEncoding()
			if err != nil {
				t.Fatal(err)
			}
			commandHash, resultHash := sha256.Sum256(commandRaw), sha256.Sum256(resultRaw)
			// Disable FKs only across the parent/evidence pair rewrite; every
			// relation is valid again before any repository read is attempted.
			catalogSchemaExec(t, db, "PRAGMA foreign_keys=OFF")
			if shape == "branch" {
				supersessionRemoveEndpointGuard(t, f, "old_version_id")
			} else if shape == "merge" {
				supersessionRemoveEndpointGuard(t, f, "new_version_id")
			}
			guards := catalogSchemaRows(t, db, "SELECT name,sql FROM sqlite_schema WHERE name IN ('tc_catalog_supersessions_no_update','tc_catalog_supersession_evidence_no_update') ORDER BY name")
			if len(guards) != 2 {
				t.Fatal("missing immutable guards")
			}
			for _, guard := range guards {
				catalogSchemaExec(t, db, "DROP TRIGGER "+guard[0].(string))
			}
			catalogSchemaExec(t, db, "UPDATE tc_catalog_supersessions SET old_version_id=?,new_version_id=?,canonical_command=?,canonical_result=?,command_sha256=?,result_sha256=? WHERE command_id=?", c.Old.ID().String(), c.New.ID().String(), commandRaw, resultRaw, commandHash[:], resultHash[:], c.ID.String())
			catalogSchemaExec(t, db, "UPDATE tc_catalog_supersession_evidence SET old_version_id=?,new_version_id=? WHERE command_id=?", c.Old.ID().String(), c.New.ID().String(), c.ID.String())
			for _, guard := range guards {
				catalogSchemaExec(t, db, guard[1].(string))
			}
			catalogSchemaExec(t, db, "PRAGMA foreign_keys=ON")
			if err := verifyForeignKeys(t.Context(), db); err != nil {
				t.Fatal("fixture has unrelated FK corruption", err)
			}
			wantEdges := [][]any{
				{versions[0].ID().String(), versions[1].ID().String()},
				{versions[1].ID().String(), versions[2].ID().String()},
				{c.Old.ID().String(), c.New.ID().String()},
			}
			if edges := catalogSchemaRows(t, db, "SELECT old_version_id,new_version_id FROM tc_catalog_supersessions ORDER BY result_supersession"); !reflect.DeepEqual(edges, wantEdges) {
				t.Fatal("fixture did not persist the requested graph shape", edges)
			}
			corrupt := catalogRecoveryRows(t, db)
			for table, rows := range before {
				switch table {
				case "sqlite_schema":
					continue
				case "tc_catalog_supersessions", "tc_catalog_supersession_evidence":
					// The first two canonical edges and their evidence stay intact.
					commandColumn := 0
					if table == "tc_catalog_supersession_evidence" {
						commandColumn = 1
					}
					if len(rows) != len(corrupt[table]) {
						t.Fatal("fixture changed row counts", table)
					}
					for _, row := range rows {
						if row[commandColumn] != c.ID.String() && !catalogSchemaContains(corrupt[table], row) {
							t.Fatal("fixture changed unrelated edge/evidence", table)
						}
					}
				default:
					if !reflect.DeepEqual(rows, corrupt[table]) {
						t.Fatal("fixture changed unrelated rows/accounting", table)
					}
				}
			}
			err = f.owner.WithinCatalogTransaction(t.Context(), func(repository app.CatalogRepository) error {
				r := repository.(*catalogRepository)
				if err := r.registrationAccounting(t.Context(), c.Content); err != nil {
					t.Fatal("fixture has unrelated accounting corruption", err)
				}
				if _, err := r.supersessionProject(t.Context(), commandRaw, false); err != nil {
					t.Fatal("fixture has inconsistent canonical evidence/support", err)
				}
				state, err := r.readSupersessionUncached(t.Context(), c.Content, domain.CatalogPersistenceBindings{AllowUnbound: true})
				if !errors.Is(err, app.ErrRepositoryUnavailable) || !reflect.DeepEqual(state, domain.CatalogSupersessionState{}) {
					t.Fatal("reconstruction returned a partial graph", state, err)
				}
				return err
			})
			if !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal("reconstruction error lost", err)
			}
			// Read an unchanged command: a later corrupt edge must invalidate
			// the complete graph, even when the requested row is canonical.
			if got, found, err := f.sup.ReadSupersession(t.Context(), commands[0]); !errors.Is(err, app.ErrRepositoryUnavailable) || found || !reflect.DeepEqual(got, domain.CatalogSupersessionResult{}) {
				t.Fatal("readback leaked a result", got, found, err)
			}
			for _, version := range []identity.ContentVersionID{{}, versions[0].ID(), versions[5].ID()} {
				if page, err := f.sup.QuerySupersession(t.Context(), app.SupersessionQuery{Content: c.Content, Version: version, Page: app.PageRequest{Limit: 1}}); !errors.Is(err, app.ErrRepositoryUnavailable) || !reflect.DeepEqual(page, app.HistoryPage{}) {
					t.Fatal("history leaked entries/accounting", version, page, err)
				}
			}
			for _, kind := range []app.QueryKind{app.GetContent, app.GetVersion, app.ListVersions, app.ListCurrentVersions, app.FindVersionsByPair, app.FindVersionsByAudioID} {
				q := app.Query{Kind: kind, Content: c.Content, Page: app.PageRequest{Limit: 1}}
				switch kind {
				case app.GetVersion:
					q.Version = versions[5].ID() // An isolated row cannot hide corruption.
				case app.FindVersionsByPair:
					q.Content, q.Fingerprint = identity.ContentID{}, c.Old.Fingerprint()
				case app.FindVersionsByAudioID:
					q.Content, q.AudioID = identity.ContentID{}, c.Old.Fingerprint().AudioID()
				}
				if page, err := catalogRead(t, f.owner, q); !errors.Is(err, app.ErrRepositoryUnavailable) || !reflect.DeepEqual(page, app.QueryResult{}) {
					t.Fatal("catalog query leaked entries/accounting", kind, page, err)
				}
			}
			if !reflect.DeepEqual(corrupt, catalogRecoveryRows(t, db)) {
				t.Fatal("rejected reads repaired history or mutated rows/accounting")
			}
		})
	}
}

func TestSupersessionSQLiteConcurrency(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			f := newSupersessionFixture(t)
			other, err := OpenLifecycleOwner(t.Context(), f.owner.config, SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close(context.Background())
			base, err := app.NewService(other, f.source, f.review, f.namespace)
			if err != nil {
				t.Fatal(err)
			}
			second, err := app.NewSupersessionService(base, f.cap)
			if err != nil {
				t.Fatal(err)
			}
			c := f.command
			if !same {
				c.ID, _ = domain.NewCatalogCommandID()
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() { <-start; _, err := f.sup.SupersedeVersion(t.Context(), f.command); results <- err }()
			go func() { <-start; _, err := second.SupersedeVersion(t.Context(), c); results <- err }()
			close(start)
			success := 0
			for range 2 {
				err := <-results
				if err == nil {
					success++
				} else if !errors.Is(err, app.ErrRepositoryContention) && !errors.Is(err, app.ErrRevisionConflict) && !errors.Is(err, app.ErrCommandConflict) {
					t.Fatal(err)
				}
			}
			if success == 0 || !same && success != 1 {
				t.Fatal("commits", success)
			}
			var count, aggregate int
			if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*),(SELECT aggregate_revision FROM tc_catalog_managed) FROM tc_catalog_supersessions").Scan(&count, &aggregate); err != nil || count != 1 || aggregate != 4 {
				t.Fatal(count, aggregate, err)
			}
		})
	}
}

func TestSupersessionSQLiteAccounting(t *testing.T) {
	f := newSupersessionFixture(t)
	input := f.command.Evidence[0].Input()
	facts := input.Coordinate.Artifact().Facts()
	facts.ParserRevision = "révision"
	facts.SourceRevision = "ä"
	artifact, e := domain.NewArtifact(f.ingest, f.source, facts)
	if e != nil {
		t.Fatal(e)
	}
	input.Coordinate, e = domain.NewRecordCoordinate(artifact, 0, "récord", input.Coordinate.Snapshot())
	if e != nil {
		t.Fatal(e)
	}
	input.AssertedOrigin = "Quelle é"
	f.command.Evidence[0], e = domain.NewCatalogSupersessionEvidence(f.ingest, f.source, input)
	if e != nil {
		t.Fatal(e)
	}
	op := f.op(f.command)
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.sup.SupersedeVersion(t.Context(), f.command)
	if err != nil {
		t.Fatal(err)
	}
	resultRaw, _ := result.CanonicalEncoding()
	want := supersessionAdded(op.Command, result, raw, resultRaw)
	err = f.owner.WithinCatalogTransaction(t.Context(), func(repository app.CatalogRepository) error {
		r := repository.(*catalogRepository)
		got, err := r.supersessionUsage(t.Context(), f.command.Content)
		if err != nil {
			return err
		}
		if got != want {
			t.Fatalf("accounting got %+v want %+v", got, want)
		}
		for _, check := range []struct {
			resource                           domain.Resource
			bytes, observations, links, events uint64
		}{{domain.RetainedBytes, domain.RetainedBytes.Maximum(), 0, 0, 0}, {domain.RetainedObservations, 0, domain.RetainedObservations.Maximum(), 0, 0}, {domain.RetainedSupportLinks, 0, 0, domain.RetainedSupportLinks.Maximum(), 0}, {domain.RetainedDecisions, 0, 0, 0, domain.RetainedDecisions.Maximum()}} {
			err := r.registrationAccountingWith(t.Context(), f.command.Content, supersessionUsage{bytes: check.bytes, observations: check.observations, links: check.links, events: check.events})
			var limit domain.LimitError
			if !errors.As(err, &limit) || limit.Resource != check.resource {
				t.Fatal(check.resource, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := f.owner.database.db.QueryRowContext(t.Context(), "SELECT canonical_result FROM tc_catalog_supersessions").Scan(&stored); err != nil || !bytes.Equal(stored, resultRaw) {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	var digest []byte
	_ = f.owner.database.db.QueryRowContext(t.Context(), "SELECT command_sha256 FROM tc_catalog_supersessions").Scan(&digest)
	if !bytes.Equal(sum[:], digest) {
		t.Fatal("digest")
	}
}

// Both opaque namespaces intentionally expose no textual constructor. Exercise
// canonical persistence decoding with trusted bindings, never unsafe conversion.
func supersessionKey(t *testing.T, f supersessionFixture, c app.SupersedeVersionCommand, key string) app.SupersedeVersionCommand {
	t.Helper()
	op := f.op(c)
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		t.Fatal(err)
	}
	d := catalogStateReader{raw: raw}
	d.text()
	d.text()
	d.number()
	copy(raw[d.offset:d.offset+32], key)
	command, _, err := domain.RehydrateCatalogSupersessionCommand(raw, domain.CatalogPersistenceBindings{Sources: []domain.SourceRegistration{f.source}, Reviewers: []domain.IssuerPrincipal{f.cap.Reviewer()}, AllowUnbound: true})
	if err != nil {
		t.Fatal(err)
	}
	c.ID = command.ID
	return c
}

func TestSupersessionSQLiteBatchKeys(t *testing.T) {
	t.Run("F02 key as import and evidence batch", func(t *testing.T) {
		f := newSupersessionFixture(t)
		batch, _ := domain.NewBatchID()
		f.command = supersessionKey(t, f, f.command, batch.String())
		if _, err := f.sup.SupersedeVersion(t.Context(), f.command); err != nil {
			t.Fatal(err)
		}
		before := registrationSchemaRows(t, f.owner.database.db)
		_, source, ingest, _ := importFixture(t)
		service, err := app.NewImportService(f.owner, source, ingest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Import(t.Context(), batch, generatedImport(t)); !errors.Is(err, app.ErrCommandConflict) {
			t.Fatal("F02 key as import", err)
		}
		c := f.version(t, 103, 4)
		input := c.Observations[0].Input()
		facts := input.Coordinate.Artifact().Facts()
		facts.ID = batch
		artifact, err := domain.NewArtifact(f.ingest, f.source, facts)
		if err != nil {
			t.Fatal(err)
		}
		input.Coordinate, err = domain.NewRecordCoordinate(artifact, 0, "0", input.Coordinate.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		c.Observations[0], err = domain.NewCatalogObservation(f.ingest, f.source, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.RegisterCatalogVersion(t.Context(), c); !errors.Is(err, app.ErrCommandConflict) {
			t.Fatal("F02 key as evidence batch", err)
		}
		if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
			t.Fatal("key collision wrote")
		}
	})
	t.Run("artifact and order keys as F02", func(t *testing.T) {
		f := newSupersessionFixture(t)
		batch := f.registrationFixture.command.Observations[0].Input().Coordinate.Artifact().Facts().ID
		if _, err := f.sup.SupersedeVersion(t.Context(), supersessionKey(t, f, f.command, batch.String())); !errors.Is(err, app.ErrCommandConflict) {
			t.Fatal("artifact key", err)
		}
		id, _ := domain.NewCatalogCommandID()
		three, _ := domain.NewCatalogRevision(3)
		observation := f.observation(t, f.registrationFixture.command.Subject, f.registrationFixture.command.Observations[0].Input().Claim)
		seq, err := domain.NewCatalogSequenceEvidence(f.ingest, f.namespace, domain.CatalogSequenceInput{ID: observation.ID(), Content: f.command.Content, Version: f.command.Old.ID(), Position: 1, Coordinate: observation.Input().Coordinate})
		if err != nil {
			t.Fatal(err)
		}
		order := app.OrderCommand{ID: id, Content: f.command.Content, ExpectedAggregate: three, ExpectedOrder: three, Evidence: []domain.CatalogSequenceEvidence{seq}, Entries: []domain.CatalogOrderEntry{{Version: f.command.Old.ID(), Position: 1, Support: []evidence.ID{seq.Input().ID}}}, Reason: "order"}
		if _, err := f.base.DecideOrder(t.Context(), order); err != nil {
			t.Fatal(err)
		}
		c := f.command
		c.ID = id
		if _, err := f.sup.SupersedeVersion(t.Context(), c); !errors.Is(err, app.ErrCommandConflict) {
			t.Fatal("embedded order key", err)
		}
	})
}

func TestSupersessionSQLiteLaterReviewAndAuthority(t *testing.T) {
	f := newSupersessionFixture(t)
	result, err := f.sup.SupersedeVersion(t.Context(), f.command)
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := domain.VersionSubject(f.command.Content, f.command.New.ID())
	claim, _ := domain.OwnershipClaim(f.command.Content)
	o := f.observation(t, subject, claim)
	id, _ := domain.NewCatalogCommandID()
	one, _ := domain.NewCatalogRevision(1)
	if _, err := f.base.RecordObservations(t.Context(), app.ReviewCommand{ID: id, Subject: subject, ExpectedAggregate: result.AggregateRevision, ExpectedReview: one, Observations: []domain.CatalogObservation{o}, Reason: "later review evidence"}); err != nil {
		t.Fatal(err)
	}
	got, found, err := f.sup.ReadSupersession(t.Context(), f.command)
	if err != nil || !found || !reflect.DeepEqual(got, result) {
		t.Fatal("historical review fence", got, found, err)
	}
	// Current authority is checked before any command-key existence disclosure.
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET revoked=1 WHERE source_id=?", f.source.ID().String())
	for _, exists := range []bool{false, true} {
		c := f.command
		if !exists {
			c.ID, _ = domain.NewCatalogCommandID()
		}
		if _, found, err := f.sup.ReadSupersession(t.Context(), c); !errors.Is(err, app.ErrUnauthorized) || found {
			t.Fatal("revocation disclosed key", found, err)
		}
	}
}

func TestSupersessionSQLitePartialRowsAndProvenance(t *testing.T) {
	for _, table := range []string{"tc_catalog_sources", "tc_catalog_source_claims", "tc_catalog_supersession_evidence", "tc_catalog_supersession_support"} {
		t.Run(table, func(t *testing.T) {
			f := newSupersessionFixture(t)
			s := newRegistrationIntegrationSource(t, f.cap.Reviewer(), domain.CoreOperator)
			f.source, f.ingest, f.review, f.namespace = s.source, s.ingest, s.review, s.space
			var err error
			f.base, err = app.NewService(f.owner, f.source, f.review, f.namespace)
			if err != nil {
				t.Fatal(err)
			}
			f.cap, err = domain.NewCatalogSupersessionCapability(f.review)
			if err != nil {
				t.Fatal(err)
			}
			f.sup, err = app.NewSupersessionService(f.base, f.cap)
			if err != nil {
				t.Fatal(err)
			}
			f.command = f.link(t, f.command.Old, f.command.New, 3, 1)
			second := f.link(t, f.command.Old, f.command.New, 3, 1)
			f.command.Evidence = append(f.command.Evidence, second.Evidence...)
			f.command.Support = append(f.command.Support, second.Support...)
			when := "AFTER INSERT"
			predicate := ""
			if table == "tc_catalog_supersession_evidence" || table == "tc_catalog_supersession_support" {
				predicate = " WHEN (SELECT count(*) FROM " + table + ")=2"
			}
			catalogSchemaExec(t, f.owner.database.db, "CREATE TRIGGER fail_partial "+when+" ON "+table+predicate+" BEGIN SELECT RAISE(ABORT,'injected'); END")
			before := registrationSchemaRows(t, f.owner.database.db)
			if _, err := f.sup.SupersedeVersion(t.Context(), f.command); !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
				t.Fatal("partial rows retained")
			}
		})
	}
}

func TestSupersessionSQLiteSchemaAndMalformedState(t *testing.T) {
	for _, kind := range []string{"v8", "checksum", "oversize", "dangling", "forged result"} {
		t.Run(kind, func(t *testing.T) {
			f := newSupersessionFixture(t)
			want := app.ErrRepositoryUnavailable
			switch kind {
			case "v8":
				owner, err := OpenLifecycleOwner(t.Context(), Config{Path: t.TempDir() + "/v8.sqlite"}, SchemaMigrations()[:8])
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close(context.Background())
				f.owner = owner
				f.bind(t)
				f.sup, err = app.NewSupersessionService(f.base, f.cap)
				if err != nil {
					t.Fatal(err)
				}
				want = app.ErrUnsupported
			case "checksum":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_schema_migrations SET checksum=? WHERE version=9", fmt.Sprintf("%064d", 0))
				want = app.ErrUnsupported
			default:
				if _, err := f.sup.SupersedeVersion(t.Context(), f.command); err != nil {
					t.Fatal(err)
				}
				if kind == "dangling" {
					catalogSchemaExec(t, f.owner.database.db, "PRAGMA foreign_keys=OFF")
					catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_supersession_evidence_no_delete")
					catalogSchemaExec(t, f.owner.database.db, "DELETE FROM tc_catalog_supersession_evidence")
					catalogSchemaExec(t, f.owner.database.db, "PRAGMA foreign_keys=ON")
				} else {
					catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_supersessions_no_update")
					if kind == "oversize" {
						catalogSchemaExec(t, f.owner.database.db, "PRAGMA ignore_check_constraints=ON")
						catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_supersessions SET canonical_result=zeroblob(8388609)")
					} else {
						var raw []byte
						_ = f.owner.database.db.QueryRowContext(t.Context(), "SELECT canonical_result FROM tc_catalog_supersessions").Scan(&raw)
						// Correct hash, but an invalid canonical result must still fail.
						raw[len(raw)-1] ^= 1
						sum := sha256.Sum256(raw)
						catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_supersessions SET canonical_result=?,result_sha256=?", raw, sum[:])
					}
				}
			}
			if _, _, err := f.sup.ReadSupersession(t.Context(), f.command); !errors.Is(err, want) {
				t.Fatal(kind, err)
			}
		})
	}
}

func TestSupersessionSQLiteCrossWriterConcurrency(t *testing.T) {
	for _, kind := range []string{"registration", "review", "order"} {
		t.Run(kind, func(t *testing.T) {
			f := newSupersessionFixture(t)
			owner, err := OpenLifecycleOwner(t.Context(), f.owner.config, SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close(context.Background())
			other := f.registrationFixture
			other.owner = owner
			other.bind(t)
			id, _ := domain.NewCatalogCommandID()
			three, _ := domain.NewCatalogRevision(3)
			one, _ := domain.NewCatalogRevision(1)
			observation := f.observation(t, f.registrationFixture.command.Subject, f.registrationFixture.command.Observations[0].Input().Claim)
			sequence, err := domain.NewCatalogSequenceEvidence(f.ingest, f.namespace, domain.CatalogSequenceInput{ID: observation.ID(), Content: f.command.Content, Version: f.command.Old.ID(), Position: 1, Coordinate: observation.Input().Coordinate})
			if err != nil {
				t.Fatal(err)
			}
			reg := other.version(t, 103, 3)
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() { <-start; _, err := f.sup.SupersedeVersion(t.Context(), f.command); results <- err }()
			go func() {
				<-start
				var err error
				switch kind {
				case "registration":
					_, err = other.service.RegisterCatalogVersion(t.Context(), reg)
				case "review":
					_, err = other.base.RecordObservations(t.Context(), app.ReviewCommand{ID: id, Subject: f.registrationFixture.command.Subject, ExpectedAggregate: three, ExpectedReview: one, Observations: []domain.CatalogObservation{observation}, Reason: "concurrent review"})
				case "order":
					_, err = other.base.DecideOrder(t.Context(), app.OrderCommand{ID: id, Content: f.command.Content, ExpectedAggregate: three, ExpectedOrder: three, Evidence: []domain.CatalogSequenceEvidence{sequence}, Entries: []domain.CatalogOrderEntry{{Version: f.command.Old.ID(), Position: 1, Support: []evidence.ID{sequence.Input().ID}}}, Reason: "concurrent order"})
				}
				results <- err
			}()
			close(start)
			success, losers := 0, 0
			for range 2 {
				err := <-results
				if err == nil {
					success++
				} else if errors.Is(err, app.ErrRevisionConflict) || errors.Is(err, app.ErrRepositoryContention) {
					losers++
				} else {
					t.Fatal(kind, err)
				}
			}
			if success != 1 || losers != 1 {
				t.Fatal(success, losers)
			}
			if err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
				state, found, err := r.LoadOrder(t.Context(), f.command.Content)
				if err == nil && (!found || state.AggregateRevision.Uint64() != 4) {
					t.Fatal(state, found)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSupersessionSQLiteEvidenceAndReviewer(t *testing.T) {
	for _, kind := range []string{"record", "artifact", "reviewer", "review key", "source mismatch"} {
		t.Run(kind, func(t *testing.T) {
			f := newSupersessionFixture(t)
			c := f.command
			want := app.ErrEvidenceConflict
			switch kind {
			case "record", "artifact":
				o := f.registrationFixture.command.Observations[0]
				input := c.Evidence[0].Input()
				coordinate := o.Input().Coordinate
				facts := coordinate.Artifact().Facts()
				snapshot := coordinate.Snapshot()
				if kind == "record" {
					var err error
					snapshot, err = domain.NewRecordSnapshot([]byte(`{"sanitized":null}`))
					if err != nil {
						t.Fatal(err)
					}
				} else {
					facts.SHA256 = sha256.Sum256([]byte("different artifact"))
				}
				artifact, err := domain.NewArtifact(f.ingest, f.source, facts)
				if err != nil {
					t.Fatal(err)
				}
				input.Coordinate, err = domain.NewRecordCoordinate(artifact, 0, "0", snapshot)
				if err != nil {
					t.Fatal(err)
				}
				c.Evidence = []domain.CatalogSupersessionEvidence{}
				e, err := domain.NewCatalogSupersessionEvidence(f.ingest, f.source, input)
				if err != nil {
					t.Fatal(err)
				}
				c.Evidence = append(c.Evidence, e)
			case "reviewer":
				if _, err := f.sup.SupersedeVersion(t.Context(), c); err != nil {
					t.Fatal(err)
				}
				principal, _ := domain.NewIssuerPrincipal()
				review, err := domain.NewReviewCapability(principal, f.source)
				if err != nil {
					t.Fatal(err)
				}
				cap, err := domain.NewCatalogSupersessionCapability(review)
				if err != nil {
					t.Fatal(err)
				}
				base, err := app.NewService(f.owner, f.source, review, f.namespace)
				if err != nil {
					t.Fatal(err)
				}
				f.sup, err = app.NewSupersessionService(base, cap)
				if err != nil {
					t.Fatal(err)
				}
				want = app.ErrCommandConflict
			case "review key":
				id, _ := domain.NewCatalogCommandID()
				three, _ := domain.NewCatalogRevision(3)
				one, _ := domain.NewCatalogRevision(1)
				o := f.observation(t, f.registrationFixture.command.Subject, f.registrationFixture.command.Observations[0].Input().Claim)
				if _, err := f.base.RecordObservations(t.Context(), app.ReviewCommand{ID: id, Subject: f.registrationFixture.command.Subject, ExpectedAggregate: three, ExpectedReview: one, Observations: []domain.CatalogObservation{o}, Reason: "record"}); err != nil {
					t.Fatal(err)
				}
				c.ID = id
				want = app.ErrCommandConflict
			case "source mismatch":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_source_binding_immutable")
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET adapter='changed' WHERE source_id=?", f.source.ID().String())
				want = app.ErrSourceMismatch
			}
			before := registrationSchemaRows(t, f.owner.database.db)
			if _, err := f.sup.SupersedeVersion(t.Context(), c); !errors.Is(err, want) {
				t.Fatal(kind, err, "want", want)
			}
			if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
				t.Fatal("rejection wrote")
			}
		})
	}
}

func supersessionHistoryReader(t *testing.T, f supersessionFixture, owner *LifecycleOwner) app.SupersessionService {
	t.Helper()
	source := newRegistrationIntegrationSource(t, f.cap.Reviewer(), domain.CoreOperator)
	base, err := app.NewService(owner, source.source, source.review, source.space)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := domain.NewCatalogSupersessionCapability(source.review)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSupersessionService(base, cap)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSupersessionHistoryQuery(t *testing.T) {
	f, versions, commands, results := supersessionQueryFixture(t)
	before := registrationSchemaRows(t, f.owner.database.db)
	q := app.SupersessionQuery{Content: f.command.Content, Page: app.PageRequest{Limit: 128}}
	page, err := f.sup.QuerySupersession(t.Context(), q)
	if err != nil || !page.Complete || page.Inspected != 3 || len(page.Entries) != 3 || page.AggregateRevision.Uint64() != 10 || page.SupersessionRevision.Uint64() != 4 {
		t.Fatal(page, err)
	}
	size := uint64(8 + len("pi08-version-supersession-page/1") + 48)
	for i, e := range page.Entries {
		command, err := f.op(commands[i]).Command.CanonicalEncoding(f.cap)
		if err != nil {
			t.Fatal(err)
		}
		result, err := results[i].CanonicalEncoding()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(e.Command, command) || !bytes.Equal(e.Result, result) || e.SourceRevoked {
			t.Fatal("original bytes", i)
		}
		size += 24 + uint64(len(command)+len(result))
	}
	if size != page.EncodedBytes {
		t.Fatal("page byte accounting", size, page.EncodedBytes)
	}
	for _, c := range []struct{ index, want int }{{0, 2}, {1, 2}, {2, 2}, {3, 1}, {4, 1}, {5, 0}, {6, 0}} {
		filter := q
		if c.index == 6 {
			filter.Version = catalogQueryVersion(t, 999)
		} else {
			filter.Version = versions[c.index].ID()
		}
		p, err := f.sup.QuerySupersession(t.Context(), filter)
		if err != nil || !p.Complete || p.Inspected != 3 || len(p.Entries) != c.want {
			t.Fatal("whole chain", c, p, err)
		}
	}
	for _, filtered := range []bool{false, true} {
		query := q
		query.Page.Limit = 1
		if filtered {
			query.Version = versions[1].ID()
		}
		var seen [][]byte
		for range 4 {
			p, err := f.sup.QuerySupersession(t.Context(), query)
			if err != nil || len(p.Entries) != 1 {
				t.Fatal(p, err)
			}
			seen = append(seen, p.Entries[0].Result)
			if p.Complete {
				if p.Next != "" {
					t.Fatal(p)
				}
				break
			}
			if p.Next == "" || len(p.Next) > 256 || p.EncodedBytes != supersessionPageBytes(p) {
				t.Fatal(p)
			}
			query.Page.Cursor, query.Page.ExpectedRevision = p.Next, p.AggregateRevision
		}
		want := 3
		if filtered {
			want = 2
		}
		if len(seen) != want {
			t.Fatal("pagination", len(seen), want)
		}
		for i := range seen {
			if !bytes.Equal(seen[i], page.Entries[i].Result) {
				t.Fatal("history order")
			}
		}
	}
	if !reflect.DeepEqual(before, registrationSchemaRows(t, f.owner.database.db)) {
		t.Fatal("history query wrote rows")
	}
}

func TestSupersessionHistoryCursor(t *testing.T) {
	for _, change := range []string{"filter", "content", "limit", "tamper", "revision", "revocation", "generation", "owner", "edge", "registration", "corruption"} {
		t.Run(change, func(t *testing.T) {
			f, versions, _, _ := supersessionQueryFixture(t)
			reader := supersessionHistoryReader(t, f, f.owner)
			q := app.SupersessionQuery{Content: f.command.Content, Page: app.PageRequest{Limit: 1}}
			first, err := reader.QuerySupersession(t.Context(), q)
			if err != nil || first.Next == "" {
				t.Fatal(first, err)
			}
			q.Page.Cursor, q.Page.ExpectedRevision = first.Next, first.AggregateRevision
			want := app.ErrStaleCursor
			switch change {
			case "filter":
				q.Version = versions[0].ID()
			case "content":
				q.Content = catalogQueryContent(t, 999)
			case "limit":
				q.Page.Limit = 2
			case "tamper":
				q.Page.Cursor = first.Next + "="
			case "revision":
				q.Page.ExpectedRevision, _ = domain.NewCatalogRevision(99)
			case "revocation":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET revoked=1 WHERE source_id=?", f.source.ID().String())
				fresh := q
				fresh.Page.Cursor = ""
				fresh.Page.ExpectedRevision = domain.CatalogRevision{}
				p, err := reader.QuerySupersession(t.Context(), fresh)
				if err != nil || len(p.Entries) != 1 || !p.Entries[0].SourceRevoked {
					t.Fatal("revocation erased acceptance", p, err)
				}
			case "generation":
				f.owner.generation.Add(1)
			case "owner":
				config := f.owner.config
				if err := f.owner.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				owner, err := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close(context.Background())
				reader = supersessionHistoryReader(t, f, owner)
			case "edge":
				if _, err := f.sup.SupersedeVersion(t.Context(), f.link(t, versions[2], versions[5], 10, 4)); err != nil {
					t.Fatal(err)
				}
			case "registration":
				if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, 107, 10)); err != nil {
					t.Fatal(err)
				}
			case "corruption":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_supersession_support_no_delete")
				catalogSchemaExec(t, f.owner.database.db, "DELETE FROM tc_catalog_supersession_support")
				want = app.ErrRepositoryUnavailable
			}
			if p, err := reader.QuerySupersession(t.Context(), q); !errors.Is(err, want) || len(p.Entries) != 0 {
				t.Fatal(change, p, err, "want", want)
			}
		})
	}
}

func TestSupersessionHistoryBoundsAndAbsent(t *testing.T) {
	f := newSupersessionFixture(t)
	q := app.SupersessionQuery{Content: f.command.Content, Page: app.PageRequest{Limit: 128}}
	unmanaged := catalogQueryOwner(t)
	catalogSeedVersions(t, unmanaged, 2)
	if _, err := supersessionHistoryReader(t, f, unmanaged).QuerySupersession(t.Context(), app.SupersessionQuery{Content: catalogQueryContent(t, 1), Page: q.Page}); !errors.Is(err, app.ErrUnsupported) {
		t.Fatal("unmanaged history", err)
	}
	old := newRegistrationFixtureMigrations(t, SchemaMigrations()[:8])
	if _, err := supersessionHistoryReader(t, f, old.owner).QuerySupersession(t.Context(), q); !errors.Is(err, app.ErrUnsupported) {
		t.Fatal("old schema history", err)
	}
	for _, missing := range []bool{false, true} {
		query := q
		if missing {
			query.Content = catalogQueryContent(t, 999)
		}
		page, err := f.sup.QuerySupersession(t.Context(), query)
		if err != nil || !page.Complete || len(page.Entries) != 0 || page.Inspected != 0 || page.SupersessionRevision.Uint64() != 1 {
			t.Fatal(page, err)
		}
		if missing && page.AggregateRevision.Uint64() != 1 {
			t.Fatal(page)
		}
	}
	for _, bound := range []string{"zero limit", "limit", "cursor", "missing content", "cursor without revision", "cancel", "schema"} {
		t.Run(bound, func(t *testing.T) {
			query := q
			ctx := t.Context()
			want := app.ErrLimitExceeded
			switch bound {
			case "zero limit":
				query.Page.Limit = 0
				want = app.ErrInvalidInput
			case "limit":
				query.Page.Limit = 129
			case "cursor":
				query.Page.Cursor = strings.Repeat("x", 257)
			case "missing content":
				query.Content = f.command.New.ContentID()
				query.Content = identity.ContentID{}
				query.Version = f.command.New.ID()
				want = app.ErrInvalidInput
			case "cursor without revision":
				query.Page.Cursor = "x"
				want = app.ErrInvalidInput
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "schema":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_schema_migrations SET checksum=? WHERE version=9", strings.Repeat("0", 64))
				want = app.ErrUnsupported
			}
			if _, err := f.sup.QuerySupersession(ctx, query); !errors.Is(err, want) {
				t.Fatal(bound, err, "want", want)
			}
		})
	}
}

func TestSupersessionHistoryOversizeEntry(t *testing.T) {
	f := newSupersessionFixture(t)
	c := f.command
	raw := []byte(`{"safe":` + strings.Repeat(" ", 60<<10) + `true}`)
	claim, _ := domain.OwnershipClaim(c.Content)
	subject, _ := domain.VersionSubject(c.Content, c.New.ID())
	o := f.observationSnapshot(t, subject, claim, raw)
	c.Evidence = nil
	c.Support = nil
	for range 20 {
		id, _ := domain.NewBatchID()
		eid, err := evidence.ParseID("obs_" + id.String()[:26])
		if err != nil {
			t.Fatal(err)
		}
		e, err := domain.NewCatalogSupersessionEvidence(f.ingest, f.source, domain.CatalogSupersessionEvidenceInput{ID: eid, Content: c.Content, OldVersion: c.Old.ID(), NewVersion: c.New.ID(), Coordinate: o.Input().Coordinate, ObservedAt: o.Input().ObservedAt})
		if err != nil {
			t.Fatal(err)
		}
		c.Evidence = append(c.Evidence, e)
		c.Support = append(c.Support, e.ID())
	}
	result, err := f.sup.SupersedeVersion(t.Context(), c)
	if err != nil {
		t.Fatal("admitted large canonical command", err)
	}
	if _, err := f.sup.QuerySupersession(t.Context(), app.SupersessionQuery{Content: c.Content, Page: app.PageRequest{Limit: 128}}); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatal("truncated oversized entry", err)
	}
	got, found, err := f.sup.ReadSupersession(t.Context(), c)
	if err != nil || !found || !reflect.DeepEqual(got, result) {
		t.Fatal("large command readback", got, found, err)
	}
}

func TestSupersessionHistoryMissingCorruption(t *testing.T) {
	f := newSupersessionFixture(t)
	catalogSchemaExec(t, f.owner.database.db, "PRAGMA foreign_keys=OFF")
	catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_supersession_support VALUES (?,?)", f.command.ID.String(), f.command.Support[0].String())
	catalogSchemaExec(t, f.owner.database.db, "PRAGMA foreign_keys=ON")
	if page, err := f.sup.QuerySupersession(t.Context(), app.SupersessionQuery{Content: catalogQueryContent(t, 999), Page: app.PageRequest{Limit: 128}}); !errors.Is(err, app.ErrRepositoryUnavailable) || len(page.Entries) != 0 {
		t.Fatal("orphan became absence", page, err)
	}
}

// Read complete persisted charges, including F01 immutable envelopes and F02
// non-BLOB/provenance columns. No usage counters or fixture row mutations.
func combinedAccountingUsage(t *testing.T, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, content identity.ContentID) supersessionUsage {
	t.Helper()
	var u, f02 supersessionUsage
	if err := db.QueryRowContext(t.Context(), `SELECT
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)),0) FROM tc_catalog_decisions WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_review_states WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_order_states WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_review_states_v2 WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(state)),0) FROM tc_catalog_order_states_v2 WHERE content_id=?1)+
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)),0) FROM tc_catalog_registrations WHERE content_id=?1)+
 (SELECT COALESCE(sum(COALESCE(length(o.text_value),0)+COALESCE(length(b.snapshot),0)+512),0) FROM tc_catalog_observations o LEFT JOIN tc_catalog_records b ON b.batch_id=o.batch_id AND b.record_index=o.record_index WHERE o.content_id=?1)+
 (SELECT COALESCE(sum(8+length(s.command_id)+8+length(s.source_id)+8+length(s.registration_revision)+8+length(s.reviewer_principal)+8),0) FROM tc_catalog_registration_sources s JOIN tc_catalog_registrations r USING(command_id) WHERE r.content_id=?1)+
 (SELECT COALESCE(sum(8+length(s.command_id)+8+length(s.observation_id)+8+length(s.subject_id)+8+length(s.source_id)+8+length(s.registration_revision)+8),0) FROM tc_catalog_registration_support s JOIN tc_catalog_registrations r USING(command_id) WHERE r.content_id=?1)+
 (SELECT COALESCE(sum(8+length(content_id)+8+8+length(version_id)),0) FROM tc_catalog_order_state_versions_v2 WHERE content_id=?1),
 (SELECT count(*) FROM tc_catalog_observations WHERE content_id=?1),
 (SELECT count(*) FROM tc_catalog_decision_support s JOIN tc_catalog_subjects u USING(subject_id) WHERE u.content_id=?1)+(SELECT count(*) FROM tc_catalog_registration_support s JOIN tc_catalog_registrations r USING(command_id) WHERE r.content_id=?1),
 (SELECT count(*) FROM tc_catalog_decisions WHERE content_id=?1)+(SELECT count(*) FROM tc_catalog_registrations WHERE content_id=?1)+(SELECT count(*) FROM tc_catalog_order_states WHERE content_id=?1 AND order_revision>1)+(SELECT count(*) FROM tc_catalog_order_states_v2 o WHERE content_id=?1 AND order_revision>1 AND NOT EXISTS(SELECT 1 FROM tc_catalog_registrations r WHERE r.content_id=o.content_id AND r.result_revision=o.aggregate_revision))`, content.String()).Scan(&u.bytes, &u.observations, &u.links, &u.events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)+
 8+length(command_id)+8+length(content_id)+8+length(old_version_id)+8+length(new_version_id)+8+length(source_id)+8+length(registration_revision)+8+length(reviewer_principal)+9*8+64),0) FROM tc_catalog_supersessions WHERE content_id=?1)+
 (SELECT COALESCE(sum(8+length(e.evidence_id)+8+length(e.command_id)+8+length(e.content_id)+8+length(e.old_version_id)+8+length(e.new_version_id)+8+length(e.source_id)+8+length(e.registration_revision)+8+length(e.batch_id)+8+8+length(CAST(e.asserted_origin AS BLOB))+8+length(e.observed_at)+8+
 8+length(e.batch_id)+32+8+8+8+length(CAST(b.parser_revision AS BLOB))+8+length(CAST(b.source_revision AS BLOB))+8+8+length(CAST(r.locator AS BLOB))+8+length(r.snapshot)),0) FROM tc_catalog_supersession_evidence e JOIN tc_catalog_batches b USING(batch_id) JOIN tc_catalog_records r ON r.batch_id=e.batch_id AND r.record_index=e.record_index WHERE e.content_id=?1)+
 (SELECT COALESCE(sum(8+length(s.command_id)+8+length(s.evidence_id)),0) FROM tc_catalog_supersession_support s JOIN tc_catalog_supersessions e USING(command_id) WHERE e.content_id=?1),
 (SELECT count(*) FROM tc_catalog_supersession_evidence WHERE content_id=?1),
 (SELECT count(*) FROM tc_catalog_supersession_support s JOIN tc_catalog_supersessions e USING(command_id) WHERE e.content_id=?1),
 (SELECT count(*) FROM tc_catalog_supersessions WHERE content_id=?1)`, content.String()).Scan(&f02.bytes, &f02.observations, &f02.links, &f02.events); err != nil {
		t.Fatal(err)
	}
	u.bytes += f02.bytes
	u.observations += f02.observations
	u.links += f02.links
	u.events += f02.events
	var raw []byte
	err := db.QueryRowContext(t.Context(), `SELECT state FROM (SELECT order_revision,state FROM tc_catalog_order_states WHERE content_id=?1 UNION ALL SELECT order_revision,state FROM tc_catalog_order_states_v2 WHERE content_id=?1) ORDER BY order_revision DESC LIMIT 1`, content.String()).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err == nil {
		links, observations, err := registrationOrderUsage(raw)
		if err != nil {
			t.Fatal(err)
		}
		u.links += links
		u.observations += observations
	}
	return u
}

func combinedAccountingByteFixture(t *testing.T, padding, reason int) supersessionFixture {
	t.Helper()
	f := newSupersessionFixture(t)
	if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, 103, 3)); err != nil {
		t.Fatal(err)
	}
	f.command.ExpectedAggregate, _ = domain.NewCatalogRevision(4)
	input := f.command.Evidence[0].Input()
	claim, _ := domain.OwnershipClaim(f.command.Content)
	subject, _ := domain.VersionSubject(f.command.Content, f.command.New.ID())
	o := f.observationSnapshot(t, subject, claim, []byte(`{"safe":true}`+strings.Repeat(" ", padding)))
	input.Coordinate = o.Input().Coordinate
	f.command.Evidence, f.command.Support = nil, nil
	for range 64 {
		input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
		e, err := domain.NewCatalogSupersessionEvidence(f.ingest, f.source, input)
		if err != nil {
			t.Fatal(err)
		}
		f.command.Evidence = append(f.command.Evidence, e)
		f.command.Support = append(f.command.Support, e.ID())
	}
	f.command.Reason = "a" + strings.Repeat("x", reason)
	if _, err := f.sup.SupersedeVersion(t.Context(), f.command); err != nil {
		t.Fatal("author byte filler", err)
	}
	return f
}

// The padding is a retained JSON whitespace byte per evidence coordinate, both
// in the canonical command and in the F02 coordinate charge: 64 * 2 bytes.
// The reason supplies the remainder one byte at a time. Both are authoring inputs.
func TestSupersessionSQLiteCombinedWriterBytes(t *testing.T) {
	for _, writer := range combinedAccountingWriters {
		if writer.kind == "content" || writer.kind == "adoption" || writer.kind == "import" {
			continue
		}
		t.Run(writer.name, func(t *testing.T) {
			calibration := combinedAccountingByteFixture(t, 0, 0)
			fence := &combinedAccountingFence{owner: calibration.owner}
			write := combinedAccountingPending(t, calibration, writer, fence)
			beforeUsage := combinedAccountingUsage(t, calibration.owner.database.db, calibration.command.Content)
			var prospective supersessionUsage
			fence.after = func(r *catalogRepository) error {
				prospective = combinedAccountingUsage(t, r.transaction, calibration.command.Content)
				return errors.New("rollback byte calibration")
			}
			before := catalogRecoveryRows(t, calibration.owner.database.db)
			if _, err := write(); !errors.Is(err, app.ErrRepositoryUnavailable) || prospective.bytes == 0 {
				t.Fatal("calibrate bytes", err)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, calibration.owner.database.db)) {
				t.Fatal("calibration leaked rows")
			}
			delta := prospective.bytes - beforeUsage.bytes
			remaining := domain.RetainedBytes.Maximum() - prospective.bytes
			for _, extra := range []uint64{0, 1} {
				t.Run(fmt.Sprintf("over_%d", extra), func(t *testing.T) {
					f := combinedAccountingByteFixture(t, int(remaining/128), int(remaining%128+extra))
					fence := &combinedAccountingFence{owner: f.owner}
					write := combinedAccountingPending(t, f, writer, fence)
					usage := combinedAccountingUsage(t, f.owner.database.db, f.command.Content)
					if usage.bytes+delta != domain.RetainedBytes.Maximum()+extra {
						t.Fatal("fixture is not exact", usage, delta, extra)
					}
					before := catalogRecoveryRows(t, f.owner.database.db)
					result, err := write()
					if extra == 1 {
						var limit domain.LimitError
						if !errors.Is(err, app.ErrLimitExceeded) || !errors.As(fence.failure, &limit) || limit.Resource != domain.RetainedBytes {
							t.Fatal("byte rejection", err, fence.failure)
						}
						if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
							t.Fatal("byte rejection leaked rows")
						}
						return
					}
					if err != nil {
						t.Fatal("exact combined byte bound", err, fence.failure)
					}
					after := catalogRecoveryRows(t, f.owner.database.db)
					n := 1
					if writer.kind == "version" {
						n = 2
					}
					combinedAccountingAssertRows(t, writer, before, after, n)
					got := combinedAccountingUsage(t, f.owner.database.db, f.command.Content)
					if got.bytes != domain.RetainedBytes.Maximum() || got.events != usage.events+1 {
						t.Fatal("actual combined charges", usage, got)
					}
					for table, rows := range before {
						if !slices.Contains(strings.Fields(writer.tables+" tc_catalog_sources tc_catalog_source_claims tc_catalog_batches tc_catalog_records"), table) && !reflect.DeepEqual(rows, after[table]) {
							t.Fatal("changed unrelated rows", table)
						}
					}
					replay, err := write()
					if err != nil || !reflect.DeepEqual(result, replay) || !reflect.DeepEqual(after, catalogRecoveryRows(t, f.owner.database.db)) {
						t.Fatal("byte-bound replay changed result or rows", err)
					}
				})
			}
		})
	}
}

func combinedAccountingClone(t *testing.T, f supersessionFixture) supersessionFixture {
	t.Helper()
	path, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "authored.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	f.owner, err = OpenLifecycleOwner(t.Context(), Config{Path: path.Path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.owner.Close(context.Background()) })
	f.bind(t)
	f.sup, err = app.NewSupersessionService(f.base, f.cap)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func combinedAccountingObservationFixture(t *testing.T) supersessionFixture {
	t.Helper()
	f := combinedAccountingFixture(t)
	coordinate := f.registrationFixture.command.Observations[0].Input().Coordinate
	subjects := []domain.CatalogSubject{f.registrationFixture.command.Subject}
	for _, n := range []int{101, 102, 103} {
		subject, _ := domain.VersionSubject(f.command.Content, catalogQueryVersion(t, n))
		subjects = append(subjects, subject)
	}
	revisions := make([]uint64, len(subjects))
	for i := range revisions {
		revisions[i] = 1
	}
	aggregate := uint64(5)
	for step := 0; step < 62; step++ {
		i := step % len(subjects)
		subject := subjects[i]
		count := 64
		if step == 61 {
			count = 60
		}
		claim, _ := domain.FingerprintClaim(f.command.Old.Fingerprint())
		if i == 0 {
			claim, _ = domain.TitleClaim("observation filler")
		}
		input := f.observation(t, subject, claim).Input()
		input.Coordinate = coordinate
		id, _ := domain.NewCatalogCommandID()
		a, _ := domain.NewCatalogRevision(aggregate)
		r, _ := domain.NewCatalogRevision(revisions[i])
		c := app.ReviewCommand{ID: id, Subject: subject, ExpectedAggregate: a, ExpectedReview: r, Reason: "observation filler"}
		for len(c.Observations) < count {
			input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
			item, err := domain.NewCatalogObservation(f.ingest, f.source, input)
			if err != nil {
				t.Fatal(err)
			}
			c.Observations = append(c.Observations, item)
		}
		if _, err := f.base.RecordObservations(t.Context(), c); err != nil {
			t.Fatal("author observation evidence", step, err)
		}
		aggregate++
		revisions[i]++
	}
	if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.observations != 4094 {
		t.Fatal("observation fixture", u)
	}
	return f
}

func combinedAccountingSupportFixture(t *testing.T, target uint64) supersessionFixture {
	t.Helper()
	f := combinedAccountingFixture(t)
	u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content)
	if u.links >= target {
		t.Fatal("support target below authored fixture", u.links, target)
	}
	subject, _ := domain.VersionSubject(f.command.Content, catalogQueryVersion(t, 101))
	claim, _ := domain.FingerprintClaim(f.command.Old.Fingerprint())
	input := f.observation(t, subject, claim).Input()
	input.Coordinate = f.registrationFixture.command.Observations[0].Input().Coordinate
	var observations []domain.CatalogObservation
	for range 64 {
		input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
		item, err := domain.NewCatalogObservation(f.ingest, f.source, input)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, item)
	}
	aggregate, review := uint64(5), uint64(1)
	id, _ := domain.NewCatalogCommandID()
	a, _ := domain.NewCatalogRevision(aggregate)
	r, _ := domain.NewCatalogRevision(review)
	if _, err := f.base.RecordObservations(t.Context(), app.ReviewCommand{ID: id, Subject: subject, ExpectedAggregate: a, ExpectedReview: r, Observations: observations, Reason: "support evidence"}); err != nil {
		t.Fatal("author support observations", err)
	}
	aggregate++
	review++
	ids := make([]evidence.ID, len(observations))
	for i := range observations {
		ids[i] = observations[i].ID()
	}
	for u = combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.links < target; u = combinedAccountingUsage(t, f.owner.database.db, f.command.Content) {
		count := min(uint64(len(ids)), target-u.links)
		id, _ = domain.NewCatalogCommandID()
		a, _ = domain.NewCatalogRevision(aggregate)
		r, _ = domain.NewCatalogRevision(review)
		command := app.ReviewCommand{ID: id, Subject: subject, ExpectedAggregate: a, ExpectedReview: r, Key: domain.FingerprintClaimKey, Support: ids[:count], Reason: "support filler"}
		if _, err := f.base.AcceptSupport(t.Context(), command); err != nil {
			t.Fatal("author support links", u.links, err)
		}
		aggregate++
		review++
	}
	if u.links != target {
		t.Fatal("support fixture", u, target)
	}
	return f
}

func TestSupersessionSQLiteCombinedWriterSupport(t *testing.T) {
	for _, writer := range combinedAccountingWriters {
		if writer.kind != "version" && writer.kind != "decision" && writer.kind != "order" && writer.kind != "supersession" {
			continue
		}
		delta := uint64(1)
		if writer.kind == "version" {
			delta = 2
		}
		for _, extra := range []uint64{0, 1} {
			t.Run(fmt.Sprintf("%s/over_%d", writer.name, extra), func(t *testing.T) {
				f := combinedAccountingSupportFixture(t, domain.RetainedSupportLinks.Maximum()-delta+extra)
				fence := &combinedAccountingFence{owner: f.owner}
				write := combinedAccountingPending(t, f, writer, fence)
				before := catalogRecoveryRows(t, f.owner.database.db)
				result, err := write()
				if extra == 1 {
					var limit domain.LimitError
					if !errors.Is(err, app.ErrLimitExceeded) || !errors.As(fence.failure, &limit) || limit.Resource != domain.RetainedSupportLinks {
						t.Fatal("combined support rejection", err, fence.failure)
					}
					if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
						t.Fatal("support rejection leaked rows")
					}
					return
				}
				if err != nil {
					t.Fatal("exact combined support", err, fence.failure)
				}
				after := catalogRecoveryRows(t, f.owner.database.db)
				n := 1
				if writer.kind == "version" {
					n = 2
				}
				combinedAccountingAssertRows(t, writer, before, after, n)
				if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.links != domain.RetainedSupportLinks.Maximum() {
					t.Fatal("support charge duplicated or lost", u)
				}
				for table, rows := range before {
					if !slices.Contains(strings.Fields(writer.tables+" tc_catalog_sources tc_catalog_source_claims tc_catalog_batches tc_catalog_records"), table) && !reflect.DeepEqual(rows, after[table]) {
						t.Fatal("support writer changed unrelated rows", table)
					}
				}
				replay, err := write()
				if err != nil || !reflect.DeepEqual(result, replay) || !reflect.DeepEqual(after, catalogRecoveryRows(t, f.owner.database.db)) {
					t.Fatal("support replay changed result or rows", err)
				}
			})
		}
	}
}

func TestSupersessionSQLiteCombinedWriterObservations(t *testing.T) {
	seed := combinedAccountingObservationFixture(t)
	for _, writer := range combinedAccountingWriters {
		if writer.kind == "content" || writer.kind == "adoption" || writer.kind == "import" || writer.kind == "decision" {
			continue
		}
		for _, count := range []int{2, 3} {
			t.Run(fmt.Sprintf("%s/observations_%d", writer.name, count), func(t *testing.T) {
				f := combinedAccountingClone(t, seed)
				fence := &combinedAccountingFence{owner: f.owner}
				write := combinedAccountingPending(t, f, writer, fence, count)
				before := catalogRecoveryRows(t, f.owner.database.db)
				result, err := write()
				if count == 3 {
					var limit domain.LimitError
					if !errors.Is(err, app.ErrLimitExceeded) || !errors.As(fence.failure, &limit) || limit.Resource != domain.RetainedObservations {
						t.Fatal("combined observation rejection", err, fence.failure)
					}
					if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
						t.Fatal("observation rejection leaked rows")
					}
					return
				}
				if err != nil {
					t.Fatal("exact combined observations", err, fence.failure)
				}
				after := catalogRecoveryRows(t, f.owner.database.db)
				combinedAccountingAssertRows(t, writer, before, after, count)
				if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.observations != 4096 {
					t.Fatal("observation charge duplicated or lost", u)
				}
				for table, rows := range before {
					if !slices.Contains(strings.Fields(writer.tables+" tc_catalog_sources tc_catalog_source_claims tc_catalog_batches tc_catalog_records"), table) && !reflect.DeepEqual(rows, after[table]) {
						t.Fatal("observation writer changed unrelated rows", table)
					}
				}
				replay, err := write()
				if err != nil || !reflect.DeepEqual(result, replay) || !reflect.DeepEqual(after, catalogRecoveryRows(t, f.owner.database.db)) {
					t.Fatal("observation replay changed result or rows", err)
				}
			})
		}
	}
}

func combinedAccountingEventFixture(t *testing.T, events uint64) supersessionFixture {
	t.Helper()
	f := combinedAccountingFixture(t)
	coordinate := f.registrationFixture.command.Observations[0].Input().Coordinate
	aggregate := uint64(5)
	for n := 104; n <= 164; n++ {
		c := f.version(t, n, aggregate)
		for i, o := range c.Observations {
			input := o.Input()
			input.Coordinate = coordinate
			var err error
			c.Observations[i], err = domain.NewCatalogObservation(f.ingest, f.source, input)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.service.RegisterCatalogVersion(t.Context(), c); err != nil {
			t.Fatal("author event manifest", n, err)
		}
		aggregate++
	}
	// Spread review history over independent version subjects so immutable
	// envelope bytes do not hit the byte bound before the shared event bound.
	var support [61]evidence.ID
	var revisions [61]uint64
	for step := uint64(0); aggregate < events; step++ {
		i := int(step % 61)
		subject, _ := domain.VersionSubject(f.command.Content, catalogQueryVersion(t, 104+i))
		id, _ := domain.NewCatalogCommandID()
		a, _ := domain.NewCatalogRevision(aggregate)
		if revisions[i] == 0 {
			revisions[i] = 1
		}
		r, _ := domain.NewCatalogRevision(revisions[i])
		c := app.ReviewCommand{ID: id, Subject: subject, ExpectedAggregate: a, ExpectedReview: r, Reason: "a"}
		var err error
		if support[i].String() == "" {
			claim, _ := domain.FingerprintClaim(f.command.Old.Fingerprint())
			input := f.observation(t, subject, claim).Input()
			input.Coordinate = coordinate
			o, e := domain.NewCatalogObservation(f.ingest, f.source, input)
			if e != nil {
				t.Fatal(e)
			}
			support[i] = o.ID()
			c.Observations = []domain.CatalogObservation{o}
			_, err = f.base.RecordObservations(t.Context(), c)
		} else {
			c.Key = domain.FingerprintClaimKey
			c.Support = []evidence.ID{support[i]}
			_, err = f.base.AcceptSupport(t.Context(), c)
		}
		if err != nil {
			t.Fatal("author shared event", aggregate, err)
		}
		aggregate++
		revisions[i]++
	}
	if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.events != events {
		t.Fatal("event fixture", u, events)
	}
	return f
}

func TestSupersessionSQLiteCombinedWriterEvents(t *testing.T) {
	for _, extra := range []uint64{0, 1} {
		seed := combinedAccountingEventFixture(t, 1023+extra)
		for _, writer := range combinedAccountingWriters {
			if writer.kind == "content" || writer.kind == "adoption" || writer.kind == "import" {
				continue
			}
			t.Run(fmt.Sprintf("%s/over_%d", writer.name, extra), func(t *testing.T) {
				f := combinedAccountingClone(t, seed)
				fence := &combinedAccountingFence{owner: f.owner}
				write := combinedAccountingPending(t, f, writer, fence)
				before := catalogRecoveryRows(t, f.owner.database.db)
				result, err := write()
				if extra == 1 {
					var limit domain.LimitError
					if !errors.Is(err, app.ErrLimitExceeded) || !errors.As(fence.failure, &limit) || limit.Resource != domain.RetainedDecisions {
						t.Fatal("shared event rejection", err, fence.failure)
					}
					if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
						t.Fatal("event rejection leaked rows")
					}
					return
				}
				if err != nil {
					t.Fatal("exact combined events", err, fence.failure)
				}
				after := catalogRecoveryRows(t, f.owner.database.db)
				n := 1
				if writer.kind == "version" {
					n = 2
				}
				combinedAccountingAssertRows(t, writer, before, after, n)
				if u := combinedAccountingUsage(t, f.owner.database.db, f.command.Content); u.events != 1024 {
					t.Fatal("event charge duplicated or lost", u)
				}
				for table, rows := range before {
					if !slices.Contains(strings.Fields(writer.tables+" tc_catalog_sources tc_catalog_source_claims tc_catalog_batches tc_catalog_records"), table) && !reflect.DeepEqual(rows, after[table]) {
						t.Fatal("event writer changed unrelated rows", table)
					}
				}
				replay, err := write()
				if err != nil || !reflect.DeepEqual(result, replay) || !reflect.DeepEqual(after, catalogRecoveryRows(t, f.owner.database.db)) {
					t.Fatal("event replay changed result or rows", err)
				}
			})
		}
	}
}

func combinedAccountingContentBytes(t *testing.T, f supersessionFixture, fence *combinedAccountingFence, padding, locatorPadding, reason int) func() (any, error) {
	t.Helper()
	base, err := app.NewService(fence, f.source, f.review, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewRegistrationService(base, f.capability)
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := domain.ContentSubject(catalogQueryContent(t, 201))
	claim, _ := domain.TitleClaim("title")
	o := f.observationSnapshot(t, subject, claim, []byte(`{"safe":true}`+strings.Repeat(" ", padding)))
	id, _ := domain.NewCatalogCommandID()
	c := app.RegistrationCommand{ID: id, Subject: subject, ExpectedKind: domain.CatalogExpectedAbsent, Reason: "a" + strings.Repeat("x", reason)}
	for i := 0; i < 64; i++ {
		input := o.Input()
		input.ID, _ = evidence.ParseID(registrationIntegrationID(t, "obs"))
		if i == 63 {
			facts := o.Input().Coordinate.Artifact().Facts()
			facts.ID, _ = domain.NewBatchID()
			artifact, err := domain.NewArtifact(f.ingest, f.source, facts)
			if err != nil {
				t.Fatal(err)
			}
			input.Coordinate, err = domain.NewRecordCoordinate(artifact, 0, "0"+strings.Repeat("x", locatorPadding), o.Input().Coordinate.Snapshot())
			if err != nil {
				t.Fatal(err)
			}
		}
		item, err := domain.NewCatalogObservation(f.ingest, f.source, input)
		if err != nil {
			t.Fatal(err)
		}
		c.Observations = append(c.Observations, item)
	}
	c.Selections = []domain.CatalogRegistrationSelection{{Key: domain.TitleClaimKey, Observation: c.Observations[0].ID()}}
	return func() (any, error) { return service.RegisterContent(t.Context(), c) }
}

func TestSupersessionSQLiteCombinedRegisterContentBytes(t *testing.T) {
	f := combinedAccountingFixture(t)
	fence := &combinedAccountingFence{owner: f.owner}
	measure := func(padding, locatorPadding, reason int) uint64 {
		var size uint64
		fence.after = func(r *catalogRepository) error {
			size = combinedAccountingUsage(t, r.transaction, catalogQueryContent(t, 201)).bytes
			return errors.New("rollback content calibration")
		}
		before := catalogRecoveryRows(t, f.owner.database.db)
		if _, err := combinedAccountingContentBytes(t, f, fence, padding, locatorPadding, reason)(); !errors.Is(err, app.ErrRepositoryUnavailable) || size == 0 {
			t.Fatal("content calibration", err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
			t.Fatal("content calibration leaked rows")
		}
		return size
	}
	initial := measure(0, 0, 0)
	snapshotStep, locatorStep, reasonStep := measure(1, 0, 0)-initial, measure(0, 1, 0)-initial, measure(0, 0, 1)-initial
	fence.after = nil
	if snapshotStep != 256 || locatorStep != 3 || reasonStep != 2 {
		t.Fatal("unexpected content charge steps", snapshotStep, locatorStep, reasonStep)
	}
	for _, extra := range []uint64{0, 1} {
		t.Run(fmt.Sprintf("over_%d", extra), func(t *testing.T) {
			remaining := domain.RetainedBytes.Maximum() + extra - initial
			padding := remaining / snapshotStep
			locatorPadding, reason, found := uint64(0), uint64(0), false
			for !found && padding > 0 {
				remainder := remaining - padding*snapshotStep
				for x := uint64(0); x <= min(uint64(512), remainder/locatorStep); x++ {
					if (remainder-x*locatorStep)%reasonStep == 0 {
						locatorPadding, reason, found = x, (remainder-x*locatorStep)/reasonStep, true
						break
					}
				}
				if !found {
					padding--
				}
			}
			if !found {
				t.Fatal("no exact authoring inputs")
			}
			clone := combinedAccountingClone(t, f)
			fence := &combinedAccountingFence{owner: clone.owner}
			write := combinedAccountingContentBytes(t, clone, fence, int(padding), int(locatorPadding), int(reason))
			before := catalogRecoveryRows(t, clone.owner.database.db)
			result, err := write()
			if extra == 1 {
				var limit domain.LimitError
				if !errors.Is(err, app.ErrLimitExceeded) || !errors.As(fence.failure, &limit) || limit.Resource != domain.RetainedBytes {
					t.Fatal("initial content byte rejection", err, fence.failure)
				}
				if !reflect.DeepEqual(before, catalogRecoveryRows(t, clone.owner.database.db)) {
					t.Fatal("initial content rejection leaked rows")
				}
				return
			}
			if err != nil {
				t.Fatal("exact initial content bytes", err, fence.failure)
			}
			if u := combinedAccountingUsage(t, clone.owner.database.db, catalogQueryContent(t, 201)); u.bytes != domain.RetainedBytes.Maximum() || u.observations != 64 || u.events != 1 || u.links != 1 {
				t.Fatal("initial content actual charges", u)
			}
			after := catalogRecoveryRows(t, clone.owner.database.db)
			for table, rows := range before {
				if !slices.Contains(strings.Fields(combinedAccountingWriters[0].tables+" tc_catalog_batches tc_catalog_records"), table) && !reflect.DeepEqual(rows, after[table]) {
					t.Fatal("content changed unrelated rows", table)
				}
			}
			replay, err := write()
			if err != nil || !reflect.DeepEqual(result, replay) || !reflect.DeepEqual(after, catalogRecoveryRows(t, clone.owner.database.db)) {
				t.Fatal("initial content replay changed result or rows", err)
			}
		})
	}
}
