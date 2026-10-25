package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

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
