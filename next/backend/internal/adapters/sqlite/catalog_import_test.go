package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	parser "github.com/shentschel/teddycloud/next/backend/internal/adapters/catalogimport"
	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

func importFixture(t *testing.T) (*LifecycleOwner, domain.SourceRegistration, domain.IngestCapability, app.ImportService) {
	t.Helper()
	owner := catalogQueryOwner(t)
	id, _ := domain.NewSourceID()
	issuer, _ := domain.NewIssuerPrincipal()
	source, capability, err := domain.NewSourceRegistration(id, issuer, domain.SourceSpec{Adapter: "offline", AdapterVersion: "1", Scope: "generated", Acquisition: domain.OfflinePublic, AllowedClaims: []domain.ClaimKey{domain.TitleClaimKey, domain.FingerprintClaimKey}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewImportService(owner, source, capability)
	if err != nil {
		t.Fatal(err)
	}
	return owner, source, capability, service
}
func importReport(t *testing.T, raw string) parser.Report {
	t.Helper()
	r, e := parser.Parse(t.Context(), []byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func generatedImport(t *testing.T) parser.Report {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join("..", "catalogimport", "testdata", "dispositions.json"))
	if e != nil {
		t.Fatal(e)
	}
	return importReport(t, string(raw))
}
func assertImportRows(t *testing.T, owner *LifecycleOwner, batch domain.BatchID, report parser.Report) {
	t.Helper()
	payload, e := report.CatalogImportPayload(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var artifact, storedReport []byte
	var retained uint64
	if e = owner.database.db.QueryRowContext(t.Context(), "SELECT artifact,report,retained_bytes FROM tc_catalog_imports WHERE batch_id=?", batch.String()).Scan(&artifact, &storedReport, &retained); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(artifact, report.ArtifactBytes()) || !bytes.Equal(storedReport, payload.ReportBytes()) || retained != payload.RetainedBytes() {
		t.Fatal("changed artifact, report or exact staging charge")
	}
	var dispositions []parser.Record
	if e = json.Unmarshal(storedReport, &dispositions); e != nil || len(dispositions) != len(report.Records) {
		t.Fatal("lost dispositions", e)
	}
	for i, r := range dispositions {
		if r.Disposition != report.Records[i].Disposition || !reflect.DeepEqual(r.Issues, report.Records[i].Issues) {
			t.Fatal("changed disposition", i)
		}
	}
	for i, r := range payload.Records() {
		var raw []byte
		if e = owner.database.db.QueryRowContext(t.Context(), "SELECT snapshot FROM tc_catalog_records WHERE batch_id=? AND record_index=?", batch.String(), i).Scan(&raw); e != nil || !bytes.Equal(raw, r.Snapshot.Bytes()) {
			t.Fatal("record changed", i, e)
		}
	}
	for _, table := range []string{"tc_catalog_content", "tc_content_versions", "tc_catalog_managed", "tc_catalog_subjects", "tc_catalog_observations", "tc_catalog_decisions"} {
		var n int
		if e = owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 0 {
			t.Fatal("import projected facts/identities", table, n, e)
		}
	}
}
func TestCatalogImportDispositions(t *testing.T) {
	owner, source, capability, service := importFixture(t)
	report := generatedImport(t)
	batch, _ := domain.NewBatchID()
	result, e := service.Import(t.Context(), batch, report)
	if e != nil || result.Records != 4 || result.Digest != report.Digest {
		t.Fatal(result, e)
	}
	assertImportRows(t, owner, batch, report)
	before := catalogRecoveryRows(t, owner.database.db)
	replay, e := service.Import(t.Context(), batch, report)
	if e != nil || replay != result || !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("nonexact replay", e)
	}
	path := owner.config.Path
	if e = owner.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close(context.Background())
	assertImportRows(t, reopened, batch, report)
	// Original in-memory core handles remain required after restart.
	service, e = app.NewImportService(reopened, source, capability)
	if e != nil {
		t.Fatal(e)
	}
	replay, e = service.Import(t.Context(), batch, report)
	if e != nil || replay != result {
		t.Fatal("restart replay", e)
	}
}

func TestCatalogImportReportEncodingBounds(t *testing.T) {
	// Exercise actual JSON escaping and every report field shape, including
	// unsupported-field issue paths, nil/non-nil slices and uint64 candidates.
	text := strings.Repeat("<>&\u2028\u2029\"\\é", 128)
	records := make([]map[string]any, domain.BatchRecords.Maximum())
	for i := range records {
		records[i] = map[string]any{
			"record": fmt.Sprintf("record-%d", i), "kind": "public",
			"publisher": text, "title": text, "model": text, "article": text,
			"pairs":            []map[string]any{{"audio_id": uint64(1<<64 - 1), "sha1": strings.Repeat("a", 40)}},
			"legacy_audio_ids": []uint64{7}, "legacy_sha1s": []string{strings.Repeat("b", 40)},
			"future-field": "safe",
		}
	}
	raw, err := json.Marshal(map[string]any{"format": parser.EnvelopeFormat, "records": records})
	if err != nil || uint64(len(raw)) > domain.BatchBytes.Maximum() {
		t.Fatal("invalid boundary fixture", len(raw), err)
	}
	report := importReport(t, string(raw))
	if len(report.Records) != len(records) || report.Records[0].Title != text || len(report.Records[0].Pairs) != 1 {
		t.Fatal("fixture lost the fields exercising JSON expansion")
	}
	payload, err := report.CatalogImportPayload(t.Context())
	if err != nil {
		t.Fatal("encoding preflight disagrees with encoder", err)
	}
	want, err := json.Marshal(report.Records)
	if err != nil || !bytes.Equal(payload.ReportBytes(), want) || uint64(len(want)) > domain.RetainedBytes.Maximum() {
		t.Fatal("report encoding changed or exceeded bound", err)
	}
	owner, _, _, service := importFixture(t)
	batch, _ := domain.NewBatchID()
	if _, err = service.Import(t.Context(), batch, report); err != nil {
		t.Fatal(err)
	}
	assertImportRows(t, owner, batch, report)
	before := catalogRecoveryRows(t, owner.database.db)
	// Public report fields are untrusted even when the original bytes were safe.
	report.Records[0].Title = strings.Repeat("<", int(domain.RetainedBytes.Maximum()/6+1))
	other, _ := domain.NewBatchID()
	if _, err = service.Import(t.Context(), other, report); !errors.Is(err, app.ErrInvalidInput) || !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("fabricated encoding reached persistence", err)
	}
}
func TestCatalogImportArrayAmbiguity(t *testing.T) {
	owner, _, _, service := importFixture(t)
	for _, r := range []string{
		`{"record":"equal","kind":"public","legacy_audio_ids":[7,9],"legacy_sha1s":["1111111111111111111111111111111111111111","2222222222222222222222222222222222222222"]}`,
		`{"record":"unequal","kind":"public","legacy_audio_ids":[7],"legacy_sha1s":[]}`,
		`{"record":"duplicate","kind":"public","title":"a","title":"b"}`,
	} {
		report := importReport(t, `{"format":"catalog-offline-v1","records":[`+r+`]}`)
		batch, _ := domain.NewBatchID()
		if _, e := service.Import(t.Context(), batch, report); e != nil {
			t.Fatal(e)
		}
		assertImportRows(t, owner, batch, report)
	}
}
func TestCatalogImportNoAuthority(t *testing.T) {
	owner, source, capability, service := importFixture(t)
	report := generatedImport(t)
	batch, _ := domain.NewBatchID()
	_, other, otherCap, _ := importFixture(t)
	if _, e := app.NewImportService(owner, other, capability); !errors.Is(e, app.ErrSourceMismatch) {
		t.Fatal("spoofed source", e)
	}
	if _, e := service.Import(t.Context(), batch, report); e != nil {
		t.Fatal(e)
	}
	before := catalogRecoveryRows(t, owner.database.db)
	changed := importReport(t, strings.Replace(string(report.ArtifactBytes()), "Generated title", "Other title", 1))
	if _, e := service.Import(t.Context(), batch, changed); !errors.Is(e, app.ErrEvidenceConflict) {
		t.Fatal("batch key collision", e)
	}
	// Use another current core registration against the same owner and key.
	otherService, e := app.NewImportService(owner, other, otherCap)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = otherService.Import(t.Context(), batch, report); !errors.Is(e, app.ErrEvidenceConflict) {
		t.Fatal("changed source", e)
	}
	tampered := generatedImport(t)
	tampered.Records[0].Disposition = parser.Rejected
	if _, e := service.Import(t.Context(), batch, tampered); !errors.Is(e, app.ErrInvalidInput) {
		t.Fatal("changed report", e)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("rejection changed rows")
	}
	catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_sources SET revoked=1")
	if _, e := service.Import(t.Context(), batch, report); !errors.Is(e, app.ErrUnauthorized) {
		t.Fatal("revoked replay", e)
	}
	source.Revoke()
	if _, e := service.Import(t.Context(), batch, report); !errors.Is(e, app.ErrUnauthorized) {
		t.Fatal("core revoked", e)
	}
}
func TestCatalogImportUnsupportedFields(t *testing.T) {
	owner, _, _, service := importFixture(t)
	report := generatedImport(t)
	batch, _ := domain.NewBatchID()
	if _, e := service.Import(t.Context(), batch, report); e != nil {
		t.Fatal(e)
	}
	assertImportRows(t, owner, batch, report)
	before := catalogRecoveryRows(t, owner.database.db)
	sensitive, e := parser.Parse(t.Context(), []byte(`{"format":"catalog-offline-v1","records":[{"record":"sensitive","kind":"public","token":"never retain"}]}`))
	if !errors.Is(e, parser.ErrSensitive) || sensitive.Digest != [32]byte{} || len(sensitive.ArtifactBytes()) != 0 {
		t.Fatal("sensitive retention", e)
	}
	batch, _ = domain.NewBatchID()
	if _, e = service.Import(t.Context(), batch, sensitive); e == nil {
		t.Fatal("failed report admitted")
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("sensitive rejection persisted data")
	}
}
func TestCatalogImportRollback(t *testing.T) {
	for _, table := range []string{"tc_catalog_sources", "tc_catalog_source_claims", "tc_catalog_batches", "tc_catalog_records", "tc_catalog_imports"} {
		t.Run(table, func(t *testing.T) {
			owner, _, _, service := importFixture(t)
			report := generatedImport(t)
			batch, _ := domain.NewBatchID()
			catalogSchemaExec(t, owner.database.db, fmt.Sprintf("CREATE TRIGGER reject_import BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT,'injected'); END", table))
			before := catalogRecoveryRows(t, owner.database.db)
			if _, e := service.Import(t.Context(), batch, report); e == nil {
				t.Fatal("injected failure ignored")
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
				t.Fatal("partial persistence", table)
			}
		})
	}
}
func TestCatalogImportBounds(t *testing.T) {
	owner, source, _, service := importFixture(t)
	report := generatedImport(t)
	first, _ := domain.NewBatchID()
	if _, e := service.Import(t.Context(), first, report); e != nil {
		t.Fatal(e)
	}
	catalogSchemaExec(t, owner.database.db, `WITH RECURSIVE seq(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<254) INSERT INTO tc_catalog_batches SELECT printf('%032x',n),?,?,CAST(zeroblob(31)||x'01' AS BLOB),1,1,'fixture','' FROM seq`, source.ID().String(), source.Revision().String())
	last, _ := domain.NewBatchID()
	if _, e := service.Import(t.Context(), last, report); e != nil {
		t.Fatal("exact staging count", e)
	}
	before := catalogRecoveryRows(t, owner.database.db)
	excess, _ := domain.NewBatchID()
	if _, e := service.Import(t.Context(), excess, report); !errors.Is(e, app.ErrLimitExceeded) {
		t.Fatal("one over", e)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("budget failure persisted")
	}
	if _, e := service.Import(t.Context(), first, report); e != nil {
		t.Fatal("exact replay charged twice", e)
	}
}

func TestCatalogImportByteBudget(t *testing.T) {
	owner, source, _, service := importFixture(t)
	report := generatedImport(t)
	first, _ := domain.NewBatchID()
	if _, e := service.Import(t.Context(), first, report); e != nil {
		t.Fatal(e)
	}
	// Seven maximal artifact/report fixtures consume 63 MiB without allocating
	// their blobs in the caller. A maximal valid envelope must then be rejected.
	for i := 1; i <= 7; i++ {
		key := fmt.Sprintf("%032x", i)
		catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_catalog_batches VALUES (?,?,?,randomblob(32),1048576,1,'budget-fixture','')", key, source.ID().String(), source.Revision().String())
		catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_catalog_imports VALUES (?,zeroblob(1048576),zeroblob(8388608),9437184)", key)
	}
	raw := string(report.ArtifactBytes())
	raw += strings.Repeat(" ", int(domain.BatchBytes.Maximum())-len(raw))
	maximum := importReport(t, raw)
	next, _ := domain.NewBatchID()
	if _, e := service.Import(t.Context(), next, maximum); !errors.Is(e, app.ErrLimitExceeded) {
		t.Fatal("staging bytes exceeded", e)
	}
	var count int
	if e := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tc_catalog_batches").Scan(&count); e != nil || count != 8 {
		t.Fatal("partial byte admission", count, e)
	}
}

type importFence struct {
	owner *LifecycleOwner
	after func(*catalogRepository) error
}

func (f importFence) WithinCatalogTransaction(ctx context.Context, operation func(app.CatalogRepository) error) error {
	return f.owner.WithinCatalogTransaction(ctx, func(r app.CatalogRepository) error {
		if e := operation(r); e != nil {
			return e
		}
		return f.after(r.(*catalogRepository))
	})
}
func TestCatalogImportFinalAuthorizationAndCancellation(t *testing.T) {
	for _, mode := range []string{"core-revoke", "stored-revoke", "cancel", "callback-failure"} {
		t.Run(mode, func(t *testing.T) {
			owner, source, capability, _ := importFixture(t)
			before := catalogRecoveryRows(t, owner.database.db)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fence := importFence{owner, func(r *catalogRepository) error {
				switch mode {
				case "core-revoke":
					source.Revoke()
				case "stored-revoke":
					if _, e := r.transaction.ExecContext(ctx, "PRAGMA query_only=OFF"); e != nil {
						return e
					}
					if _, e := r.transaction.ExecContext(ctx, "UPDATE tc_catalog_sources SET revoked=1"); e != nil {
						return e
					}
				case "cancel":
					cancel()
				case "callback-failure":
					return app.ErrRepositoryUnavailable
				}
				return nil
			}}
			service, e := app.NewImportService(fence, source, capability)
			if e != nil {
				t.Fatal(e)
			}
			batch, _ := domain.NewBatchID()
			if _, e = service.Import(ctx, batch, generatedImport(t)); e == nil {
				t.Fatal("post-write fence ignored")
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
				t.Fatal("fence failure retained partial batch")
			}
		})
	}
}
