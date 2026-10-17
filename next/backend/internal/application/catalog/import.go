package catalog

import (
	"context"
	"crypto/sha256"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"sync/atomic"
)

const OfflineImportRevision = "catalog-offline-v1/1"

// ImportReport is implemented by the trusted offline parser. It revalidates a
// successful report before the transaction and supplies no source authority.
type ImportReport interface {
	CatalogImportPayload(context.Context) (ImportPayload, error)
}
type ImportPayload = domain.ImportPayload

type ImportOperation struct {
	Artifact   domain.Artifact
	Capability domain.IngestCapability
	Payload    ImportPayload
}

func (o ImportOperation) Authorize() error {
	f := o.Artifact.Facts()
	if !o.Artifact.Source().Active() {
		return domain.ErrCatalogUnauthorized
	}
	if _, err := domain.NewArtifact(o.Capability, o.Artifact.Source(), f); err != nil {
		return err
	}
	if o.Payload.ByteLength() == 0 || sha256.Sum256(o.Payload.ArtifactBytes()) != f.SHA256 || o.Payload.ByteLength() != f.ByteLength || o.Payload.RecordCount() != f.RecordCount || f.ParserRevision != OfflineImportRevision || f.SourceRevision != "" {
		return ErrInvalidInput
	}
	return domain.CheckLimit(domain.StagingBytes, 0, o.Payload.RetainedBytes())
}

type ImportResult struct {
	Batch   domain.BatchID
	Digest  [32]byte
	Records uint64
}
type ImportRepository interface {
	CommitImport(context.Context, ImportOperation) (ImportResult, error)
}
type ImportService struct {
	transactions CatalogTransactor
	source       domain.SourceRegistration
	capability   domain.IngestCapability
}

func NewImportService(tx CatalogTransactor, source domain.SourceRegistration, capability domain.IngestCapability) (ImportService, error) {
	if tx == nil {
		return ImportService{}, ErrInvalidInput
	}
	id, err := domain.NewBatchID()
	if err != nil {
		return ImportService{}, ErrInvalidInput
	}
	_, err = domain.NewArtifact(capability, source, domain.ArtifactFacts{ID: id, SHA256: [32]byte{1}, ByteLength: 1, RecordCount: 1, ParserRevision: OfflineImportRevision})
	if err == nil && !source.Active() {
		err = domain.ErrCatalogUnauthorized
	}
	if err != nil {
		return ImportService{}, sanitizeError(context.Background(), err)
	}
	return ImportService{tx, source, capability}, nil
}
func (s ImportService) Import(ctx context.Context, batch domain.BatchID, report ImportReport) (result ImportResult, err error) {
	if ctx == nil || report == nil || batch.IsZero() || s.transactions == nil {
		return result, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, domain.CatalogOperationTimeout)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	payload, err := report.CatalogImportPayload(ctx)
	if err != nil {
		return result, ErrInvalidInput
	}
	artifact, err := domain.NewArtifact(s.capability, s.source, domain.ArtifactFacts{ID: batch, SHA256: sha256.Sum256(payload.ArtifactBytes()), ByteLength: payload.ByteLength(), RecordCount: payload.RecordCount(), ParserRevision: OfflineImportRevision})
	if err != nil {
		return result, sanitizeError(ctx, err)
	}
	op := ImportOperation{artifact, s.capability, payload}
	if err = op.Authorize(); err != nil {
		return result, sanitizeError(ctx, err)
	}
	transaction, stop := context.WithTimeout(ctx, domain.CatalogTransactionTimeout)
	defer stop()
	var called atomic.Bool
	var callbackErr error
	err = s.transactions.WithinCatalogTransaction(transaction, func(r CatalogRepository) error {
		if called.Swap(true) {
			callbackErr = ErrRepositoryUnavailable
			return callbackErr
		}
		port, ok := r.(ImportRepository)
		if !ok {
			callbackErr = ErrUnsupported
			return callbackErr
		}
		result, callbackErr = port.CommitImport(transaction, op)
		if callbackErr == nil {
			callbackErr = op.Authorize()
		}
		return callbackErr
	})
	if err == nil {
		err = callbackErr
	}
	if err == nil && !called.Load() {
		err = ErrRepositoryUnavailable
	}
	if err == nil {
		err = transaction.Err()
	}
	if err != nil {
		return ImportResult{}, sanitizeError(transaction, err)
	}
	if result.Batch != batch || result.Digest != artifact.Facts().SHA256 || result.Records != artifact.Facts().RecordCount {
		return ImportResult{}, ErrRepositoryUnavailable
	}
	return result, nil
}
