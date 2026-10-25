package sqlite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"reflect"
	"testing"
	"time"
)

func adoptionFixture(t *testing.T) (catalogStateFixture, app.Service, app.AdoptionCommand) {
	t.Helper()
	f := catalogStateFixture{owner: catalogQueryOwner(t)}
	catalogSeedVersions(t, f.owner, 2)
	id, _ := domain.NewSourceID()
	issuer, _ := domain.NewIssuerPrincipal()
	var err error
	f.source, f.ingest, err = domain.NewSourceRegistration(id, issuer, domain.SourceSpec{Adapter: "baseline", AdapterVersion: "1", Scope: "stored-facts", Acquisition: domain.BaselineSnapshot, AllowedClaims: []domain.ClaimKey{domain.TitleClaimKey, domain.FingerprintClaimKey}})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _ := domain.NewIssuerPrincipal()
	f.review, err = domain.NewReviewCapability(reviewer, f.source)
	if err != nil {
		t.Fatal(err)
	}
	f.namespace, err = domain.NewCatalogOrderNamespace(f.source, "explicit-order/1")
	if err != nil {
		t.Fatal(err)
	}
	f.subject, _ = domain.ContentSubject(catalogQueryContent(t, 1))
	service, err := app.NewService(f.owner, f.source, f.review, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	baseline, revision, err := service.ReadBaseline(t.Context(), f.subject.ContentID())
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := domain.NewCatalogCommandID()
	return f, service, app.AdoptionCommand{ID: commandID, Content: f.subject.ContentID(), ExpectedAggregate: revision, BaselineSHA256: baseline.SHA256(), Reason: "explicit mechanical baseline adoption"}
}

func TestCatalogBaselineAdoption(t *testing.T) {
	f, service, command := adoptionFixture(t)
	catalogRecoverySeedLegacy(t, f.owner, 81)
	before := catalogRecoveryRows(t, f.owner.database.db)
	result, err := service.AdoptBaseline(t.Context(), command)
	if err != nil || result.Command != command.ID || result.AggregateRevision.Uint64() != 1 {
		t.Fatal(result, err)
	}
	after := catalogRecoveryRows(t, f.owner.database.db)
	for table, rows := range before {
		if table == "tc_catalog_sources" || table == "tc_catalog_source_claims" || table == "tc_catalog_batches" || table == "tc_catalog_records" || table == "tc_catalog_managed" || table == "tc_catalog_subjects" || table == "tc_catalog_review_states" || table == "tc_catalog_order_states" || table == "tc_catalog_order_state_versions" {
			continue
		}
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("adoption mutated %s", table)
		}
	}
	page, err := catalogRead(t, f.owner, app.Query{Kind: app.GetContent, Content: command.Content, Page: app.PageRequest{Limit: 1}})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Review != app.CatalogManagedUnreviewed || page.Entries[0].Title.Status != domain.CatalogFactUnknown || page.Entries[0].ProtectedCustom {
		t.Fatal(page, err)
	}
	baseline, _, err := service.ReadBaseline(t.Context(), command.Content)
	if err != nil || baseline.SHA256() != command.BaselineSHA256 {
		t.Fatal("changed baseline", err)
	}
	for _, version := range baseline.Versions() {
		subject, _ := domain.VersionSubject(command.Content, version.ID())
		if err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
			state, found, e := r.LoadReview(t.Context(), subject)
			if e != nil {
				return e
			}
			if !found || len(state.State.Observations()) != 0 || state.State.Field(domain.FingerprintClaimKey).Status != domain.CatalogFactUnknown {
				t.Fatal("version implicitly accepted")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if replay, e := service.AdoptBaseline(t.Context(), command); e != nil || replay != result {
		t.Fatal(replay, e)
	}
	if !reflect.DeepEqual(after, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("replay mutated rows")
	}
	content, _ := domain.NewContent(command.Content, domain.NewContentFacts("Save bypass", domain.ProductIdentifiers{}))
	if err := f.owner.database.WithinTransaction(t.Context(), func(r app.ContentRepository) error { return r.Save(t.Context(), content) }); !errors.Is(err, app.ErrUnauthorized) {
		t.Fatal("Save bypass", err)
	}
	// Subsequent production Review commands use the adopted revision-1 state.
	w, op, _, _ := reviewWriteFixture(t, false)
	w.owner = f.owner
	writer, err := app.NewService(f.owner, w.source, w.review, w.namespace)
	if err != nil {
		t.Fatal(err)
	}
	reviewCommand := app.ReviewCommand{ID: op.Command.ID, Subject: op.Command.Subject, ExpectedAggregate: op.ExpectedAggregate, ExpectedReview: op.Command.Expected, Observations: op.Command.Observations, Reason: op.Command.Reason}
	if _, err = writer.RecordObservations(t.Context(), reviewCommand); err != nil {
		t.Fatal("post-adoption review", err)
	}
	accept := reviewCommand
	accept.ID, _ = domain.NewCatalogCommandID()
	accept.ExpectedAggregate, _ = domain.NewCatalogRevision(2)
	accept.ExpectedReview = accept.ExpectedAggregate
	accept.Observations = nil
	accept.Key = domain.TitleClaimKey
	accept.Support = []evidence.ID{op.Command.Observations[0].ID()}
	if _, err = writer.AcceptSupport(t.Context(), accept); err != nil {
		t.Fatal("post-adoption acceptance", err)
	}
	page, err = catalogRead(t, f.owner, app.Query{Kind: app.GetContent, Content: command.Content, Page: app.PageRequest{Limit: 1}})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Title.Status != domain.CatalogFactAccepted {
		t.Fatal("explicit acceptance missing", page, err)
	}
	orderFixture, _, orderOp, _, _ := orderWriteFixture(t)
	orderWriter, e := app.NewService(f.owner, orderFixture.source, orderFixture.review, orderFixture.namespace)
	if e != nil {
		t.Fatal(e)
	}
	aggregate, _ := domain.NewCatalogRevision(3)
	if _, err = orderWriter.DecideOrder(t.Context(), app.OrderCommand{ID: orderOp.Command.ID, Content: command.Content, ExpectedAggregate: aggregate, ExpectedOrder: orderOp.Command.Expected, Entries: orderOp.Command.Entries, Evidence: orderOp.Command.Evidence, Reason: orderOp.Command.Reason}); err != nil {
		t.Fatal("post-adoption order", err)
	}
	// The original result remains exact after aggregate revision advances.
	if replay, e := service.AdoptBaseline(t.Context(), command); e != nil || replay != result {
		t.Fatal("old replay", replay, e)
	}
	config := f.owner.config
	retained := catalogRecoveryRows(t, f.owner.database.db)
	if err = f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	service, err = app.NewService(reopened, f.source, f.review, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	if replay, e := service.AdoptBaseline(t.Context(), command); e != nil || replay != result {
		t.Fatal("restart replay", replay, e)
	}
	if !reflect.DeepEqual(retained, catalogRecoveryRows(t, reopened.database.db)) {
		t.Fatal("restart changed rows")
	}
	var bad bool
	if err = reopened.database.db.QueryRowContext(t.Context(), "SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check)").Scan(&bad); err != nil || bad {
		t.Fatal("foreign keys", err)
	}
}

func TestCatalogBaselineAdoptionRejections(t *testing.T) {
	for _, name := range []string{"stale-revision", "stale-facts", "stale-versions", "missing", "foreign-version", "wrong-capability", "wrong-source-class", "revoked", "cancel", "rollback-revocation", "changed-replay", "foreign-replay", "persisted-revocation"} {
		t.Run(name, func(t *testing.T) {
			f, service, command := adoptionFixture(t)
			if name == "changed-replay" || name == "foreign-replay" || name == "persisted-revocation" {
				if _, err := service.AdoptBaseline(t.Context(), command); err != nil {
					t.Fatal(err)
				}
			}
			want := app.ErrRevisionConflict
			switch name {
			case "stale-revision":
				command.ExpectedAggregate, _ = domain.NewCatalogRevision(2)
			case "stale-facts":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_content SET title='changed'")
			case "stale-versions":
				catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)", catalogQueryVersion(t, 3).String(), command.Content.String(), make([]byte, 20))
			case "missing":
				command.Content = catalogQueryContent(t, 99)
				want = app.ErrSubjectNotFound
			case "foreign-version":
				want = domain.ErrInvalidCatalog
			case "wrong-capability":
				want = domain.ErrCatalogUnauthorized
			case "wrong-source-class":
				w, _, _, _ := reviewWriteFixture(t, false)
				service, _ = app.NewService(f.owner, w.source, w.review, w.namespace)
				want = app.ErrUnauthorized
			case "revoked":
				f.source.Revoke()
				want = app.ErrUnauthorized
			case "cancel":
				want = context.Canceled
			case "rollback-revocation":
				want = domain.ErrCatalogUnauthorized
			case "changed-replay":
				command.Reason = "changed"
				want = app.ErrCommandConflict
			case "foreign-replay":
				command.Content = catalogQueryContent(t, 99)
				want = app.ErrCommandConflict
			case "persisted-revocation":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET revoked=1")
				want = app.ErrUnauthorized
			}
			before := catalogRecoveryRows(t, f.owner.database.db)
			var err error
			if name == "foreign-version" || name == "wrong-capability" || name == "rollback-revocation" {
				op := app.AdoptionOperation{Capability: f.review, Command: domain.CatalogAdoptionCommand{ID: command.ID, Subject: f.subject, Source: f.source, Expected: command.ExpectedAggregate, BaselineSHA256: command.BaselineSHA256, Reason: command.Reason}}
				if name == "foreign-version" {
					op.Command.Subject, _ = domain.VersionSubject(command.Content, catalogQueryVersion(t, 99))
				}
				if name == "wrong-capability" {
					op.Capability = domain.ReviewCapability{}
				}
				err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
					_, e := r.(app.BaselineRepository).AdoptBaseline(t.Context(), op)
					if e == nil && name == "rollback-revocation" {
						f.source.Revoke()
					}
					return e
				})
			} else if name == "cancel" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err = service.AdoptBaseline(ctx, command)
			} else {
				_, err = service.AdoptBaseline(t.Context(), command)
			}
			if !errors.Is(err, want) {
				t.Fatalf("want %v, got %v", want, err)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
				t.Fatal("rejected adoption mutated rows")
			}
		})
	}
}

func TestCatalogBaselineAdoptionRace(t *testing.T) {
	f, _, command := adoptionFixture(t)
	other, err := OpenLifecycleOwner(t.Context(), f.owner.config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	for _, owner := range []*LifecycleOwner{f.owner, other} {
		go func() {
			id, _ := domain.NewCatalogCommandID()
			op := app.AdoptionOperation{Capability: f.review, Command: domain.CatalogAdoptionCommand{ID: id, Subject: f.subject, Source: f.source, Expected: command.ExpectedAggregate, BaselineSHA256: command.BaselineSHA256, Reason: command.Reason}}
			results <- owner.WithinCatalogTransaction(ctx, func(r app.CatalogRepository) error {
				_, _, _, e := r.(app.BaselineRepository).LoadBaseline(ctx, command.Content)
				ready <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if e != nil {
					return e
				}
				_, e = r.(app.BaselineRepository).AdoptBaseline(ctx, op)
				return e
			})
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	success := 0
	for range 2 {
		e := <-results
		if e == nil {
			success++
		} else if !errors.Is(e, app.ErrRepositoryContention) && !errors.Is(e, domain.ErrCatalogRevisionConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("%d successful adoptions", success)
	}
	var count int
	if err = f.owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_catalog_batches").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

// Exercise both adoption replay and verified restore: malformed relationships
// must fail closed without repairing, replacing or discarding original data.
func TestCatalogCorruptRelations(t *testing.T) {
	for _, name := range []string{"missing-command", "changed-snapshot", "missing-version-state", "changed-source"} {
		t.Run(name, func(t *testing.T) {
			f, service, command := adoptionFixture(t)
			if _, err := service.AdoptBaseline(t.Context(), command); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing-command":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_records_no_delete")
				catalogSchemaExec(t, f.owner.database.db, "DELETE FROM tc_catalog_records WHERE batch_id=? AND record_index=1", command.ID.String())
			case "changed-snapshot":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_records_no_update")
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_records SET snapshot=CAST('changed' AS BLOB) WHERE batch_id=? AND record_index=0", command.ID.String())
			case "missing-version-state":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_review_states_no_delete")
				catalogSchemaExec(t, f.owner.database.db, "DELETE FROM tc_catalog_review_states WHERE subject_id=?", catalogQueryVersion(t, 1).String())
			case "changed-source":
				catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_source_binding_immutable")
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET adapter='corrupt'")
			}
			before := catalogRecoveryRows(t, f.owner.database.db)
			if _, err := service.AdoptBaseline(t.Context(), command); !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal("corrupt adoption relation", err)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
				t.Fatal("corrupt replay changed rows")
			}
		})
	}
	for _, corruption := range []struct {
		name, child, parent string
	}{
		{"broken-support", "tc_catalog_decision_support", "tc_catalog_observations"},
		{"missing-binding", "tc_blob_imports", "tc_version_blobs"},
		{"dangling-receipt", "tc_blob_imports", "tc_version_blobs"},
	} {
		t.Run("restore/"+corruption.name, func(t *testing.T) {
			f := catalogRecoveryCurrent(t)
			owner := f.owner
			source := owner.config.Path
			original := catalogRecoveryRows(t, owner.database.db)
			for _, table := range []string{"tc_catalog_decision_support", "tc_content_versions", "tc_version_blobs", "tc_blob_imports", "tc_tags"} {
				if len(original[table]) == 0 {
					t.Fatal("empty corruption fixture", table)
				}
			}
			dir := filepath.Dir(owner.config.Path)
			snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(dir, "corrupt-relations.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			raw := openRawDatabase(t, snapshot.Path)
			catalogSchemaExec(t, raw, "PRAGMA foreign_keys=OFF")
			switch corruption.name {
			case "broken-support":
				catalogSchemaExec(t, raw, "DROP TRIGGER tc_catalog_decision_support_no_update")
				catalogSchemaExec(t, raw, "UPDATE tc_catalog_decision_support SET observation_id=?", catalogSchemaID("obs", 99))
			case "missing-binding":
				catalogSchemaExec(t, raw, "DELETE FROM tc_version_blobs WHERE version_id=?", blobCommand(t, 81, true).Version().ID().String())
			case "dangling-receipt":
				catalogSchemaExec(t, raw, "UPDATE tc_blob_imports SET version_id=?", catalogSchemaID("ver", 98))
			}
			corrupt := catalogRecoveryRows(t, raw)
			// A missing binding preserves the receipt; a dangling receipt leaves
			// the binding intact. Neither may be treated as harmless unbound media.
			if corruption.name == "missing-binding" && (len(corrupt["tc_version_blobs"]) != len(original["tc_version_blobs"])-1 || !reflect.DeepEqual(original["tc_blob_imports"], corrupt["tc_blob_imports"])) {
				t.Fatal("missing-binding injection did not preserve original receipts")
			}
			if corruption.name == "dangling-receipt" && (!reflect.DeepEqual(original["tc_version_blobs"], corrupt["tc_version_blobs"]) || reflect.DeepEqual(original["tc_blob_imports"], corrupt["tc_blob_imports"])) {
				t.Fatal("dangling-receipt injection did not preserve original bindings")
			}
			violations := catalogSchemaRows(t, raw, "PRAGMA foreign_key_check")
			if len(violations) == 0 {
				t.Fatal("corruption did not break referential integrity")
			}
			for _, violation := range violations {
				if violation[0] != corruption.child || violation[2] != corruption.parent {
					t.Fatal("unexpected broken relation", violation)
				}
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			// Recompute the digest so rejection proves relation validation rather
			// than merely detecting stale backup metadata.
			snapshot.SHA256, err = fileDigest(snapshot.Path)
			if err != nil {
				t.Fatal(err)
			}
			retainedBytes, err := os.ReadFile(snapshot.Path)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "must-not-publish.sqlite")
			if err := owner.Restore(t.Context(), snapshot, target); !errors.Is(err, ErrBackupInvalid) {
				t.Fatal("relationally corrupt snapshot accepted", err)
			}
			if !reflect.DeepEqual(original, catalogRecoveryRows(t, owner.database.db)) {
				t.Fatal("rejected restore changed original rows or schema")
			}
			if owner.config.Path != source {
				t.Fatal("restore selected another database")
			}
			if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 9 {
				t.Fatal("rejected restore closed original owner", version, err)
			}
			if err := verifyForeignKeys(t.Context(), owner.database.db); err != nil {
				t.Fatal("original integrity changed", err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("corrupt destination published", err)
			}
			if got, err := os.ReadFile(snapshot.Path); err != nil || !bytes.Equal(got, retainedBytes) {
				t.Fatal("corrupt snapshot original was modified or removed", err)
			}
		})
	}
}

func TestCatalogBaselineAdoptionSaveRace(t *testing.T) {
	f, service, command := adoptionFixture(t)
	other, err := OpenLifecycleOwner(t.Context(), f.owner.config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	content, _ := domain.NewContent(command.Content, domain.NewContentFacts("concurrent unmanaged Save", domain.ProductIdentifiers{}))
	op := app.AdoptionOperation{Capability: f.review, Command: domain.CatalogAdoptionCommand{ID: command.ID, Subject: f.subject, Source: f.source, Expected: command.ExpectedAggregate, BaselineSHA256: command.BaselineSHA256, Reason: command.Reason}}
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		if _, _, _, e := r.(app.BaselineRepository).LoadBaseline(t.Context(), command.Content); e != nil {
			return e
		}
		if e := other.database.WithinTransaction(t.Context(), func(r app.ContentRepository) error { return r.Save(t.Context(), content) }); e != nil {
			return e
		}
		_, e := r.(app.BaselineRepository).AdoptBaseline(t.Context(), op)
		return e
	})
	if !errors.Is(err, app.ErrRepositoryContention) {
		t.Fatal("stale snapshot promoted", err)
	}
	before := catalogRecoveryRows(t, f.owner.database.db)
	if _, err = service.AdoptBaseline(t.Context(), command); !errors.Is(err, app.ErrRevisionConflict) {
		t.Fatal("stale baseline accepted", err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("stale retry changed rows")
	}
}
