package catalog

import (
	"context"
	"errors"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"testing"
)

type reportStub struct {
	payload domain.ImportPayload
	err     error
}

func (r reportStub) CatalogImportPayload(context.Context) (domain.ImportPayload, error) {
	return r.payload, r.err
}

type importContract struct {
	CatalogRepository
	calls        int
	transactions int
	after        func()
	fail         error
}

func (r *importContract) WithinCatalogTransaction(ctx context.Context, fn func(CatalogRepository) error) error {
	r.transactions++
	return fn(r)
}
func (r *importContract) CommitImport(_ context.Context, op ImportOperation) (ImportResult, error) {
	r.calls++
	if r.after != nil {
		r.after()
	}
	f := op.Artifact.Facts()
	return ImportResult{f.ID, f.SHA256, f.RecordCount}, r.fail
}
func TestCatalogImportAdmissionBeforeTransaction(t *testing.T) {
	id, _ := domain.NewSourceID()
	issuer, _ := domain.NewIssuerPrincipal()
	source, capability, _ := domain.NewSourceRegistration(id, issuer, domain.SourceSpec{Adapter: "offline", AdapterVersion: "1", Scope: "test", Acquisition: domain.OfflinePublic, AllowedClaims: []domain.ClaimKey{domain.TitleClaimKey}})
	tx := &importContract{}
	service, e := NewImportService(tx, source, capability)
	if e != nil {
		t.Fatal(e)
	}
	batch, _ := domain.NewBatchID()
	if _, e = service.Import(t.Context(), batch, reportStub{err: errors.New("sensitive private detail")}); !errors.Is(e, ErrInvalidInput) || tx.transactions != 0 {
		t.Fatal("failed report entered transaction", e)
	}
	snapshot, _ := domain.NewRecordSnapshot([]byte(`{"title":"generated"}`))
	payload, e := domain.NewImportPayload([]byte(`{"format":"catalog-offline-v1","records":[{"title":"generated"}]}`), []byte(`[{"disposition":"accepted-for-staging"}]`), []domain.ImportRecord{{Locator: "record:0", Snapshot: snapshot}})
	if e != nil {
		t.Fatal(e)
	}
	tx.after = source.Revoke
	if _, e = service.Import(t.Context(), batch, reportStub{payload: payload}); !errors.Is(e, ErrUnauthorized) || tx.calls != 1 {
		t.Fatal("current authorization lost", e)
	}
	if _, e = service.Import(t.Context(), batch, reportStub{payload: payload}); !errors.Is(e, ErrUnauthorized) || tx.transactions != 1 {
		t.Fatal("revoked admission", e)
	}
}
