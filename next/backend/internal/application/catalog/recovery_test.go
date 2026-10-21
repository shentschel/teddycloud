package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

type recoveryTransactor struct {
	base      *contractTransaction
	failure   error
	commit    bool
	cancel    context.CancelFunc
	callbacks int
}

func (tx *recoveryTransactor) WithinCatalogTransaction(ctx context.Context, fn func(CatalogRepository) error) error {
	err := tx.base.WithinCatalogTransaction(ctx, func(r CatalogRepository) error {
		tx.callbacks++
		if err := fn(r); err != nil {
			return err
		}
		if !tx.commit {
			if tx.cancel != nil {
				tx.cancel()
			}
			return tx.failure
		}
		return nil
	})
	if err != nil {
		return err
	}
	if tx.cancel != nil {
		tx.cancel()
	}
	return tx.failure
}

// A lost response has two externally identical outcomes. Only authorized
// command lookup with a fresh context can distinguish them; neither permits
// automatic callback retry or a successful provisional result.
func TestCatalogApplicationRecovery(t *testing.T) {
	for _, durable := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("durable=%t/canceled=%t", durable, canceled), func(t *testing.T) {
				f := newFixture(t)
				command := f.command(t)
				command.Observations = []domain.CatalogObservation{f.observation(t, payload, "retained candidate")}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				tx := &recoveryTransactor{base: f.tx, failure: fmt.Errorf("private/path token=value: %w", ErrIndeterminate), commit: durable}
				if canceled {
					tx.cancel = cancel
				}
				service, err := NewService(tx, f.source, f.service.review, f.namespace)
				require(t, err)
				before, _ := f.tx.repository.review.State.PersistenceEncoding()
				got, err := service.RecordObservations(ctx, command)
				if err != ErrIndeterminate || !reflect.DeepEqual(got, ReviewResult{}) || tx.callbacks != 1 {
					t.Fatal("uncertainty lost or callback retried", got, err, tx.callbacks)
				}
				if errors.Unwrap(err) != nil {
					t.Fatal("private cause retained")
				}
				read, found, err := f.service.LookupReview(t.Context(), domain.RecordObservations, command)
				require(t, err)
				if found != durable {
					t.Fatal("lookup lied about durable outcome", found, durable)
				}
				if !durable {
					after, _ := f.tx.repository.review.State.PersistenceEncoding()
					if !reflect.DeepEqual(before, after) || f.tx.repository.writes != 0 || len(f.tx.repository.reviews) != 0 || !reflect.DeepEqual(read, ReviewResult{}) {
						t.Fatal("failed transaction left partial state")
					}
				} else if read.AggregateRevision.Uint64() != 2 || read.Decision.Command != command.ID || read.Decision.Revision.Uint64() != 2 || f.tx.repository.writes != 1 || len(f.tx.repository.reviews) != 1 {
					t.Fatal("wrong durable command facts", read)
				}
				retry, err := f.service.RecordObservations(t.Context(), command)
				require(t, err)
				if durable && !reflect.DeepEqual(retry, read) || retry.AggregateRevision.Uint64() != 2 || f.tx.repository.writes != 1 {
					t.Fatal("retry rewrote durable state", retry)
				}
				stable, _ := f.tx.repository.review.State.PersistenceEncoding()
				command.Reason = "changed retry"
				conflict, err := f.service.RecordObservations(t.Context(), command)
				if err != ErrCommandConflict || !reflect.DeepEqual(conflict, ReviewResult{}) {
					t.Fatal("changed command accepted", conflict, err)
				}
				f.source.Revoke()
				if _, found, err := f.service.LookupReview(t.Context(), domain.RecordObservations, command); err != ErrUnauthorized || found {
					t.Fatal("recovery bypassed current authorization", found, err)
				}
				after, _ := f.tx.repository.review.State.PersistenceEncoding()
				if !reflect.DeepEqual(stable, after) || f.tx.repository.writes != 1 {
					t.Fatal("failed recovery changed history")
				}
			})
		}
	}
}

// Assert exact sentinel identity and absence of an unwrap chain, rather than
// merely searching Error() text. Repositories may wrap arbitrary sensitive
// storage causes, but only the stable typed distinction crosses the service.
func TestCatalogApplicationSanitizedErrors(t *testing.T) {
	cases := []struct {
		name        string
		cause, want error
	}{
		{"unknown", errors.New("filesystem SQL schema token raw-content"), ErrRepositoryUnavailable},
		{"invalid", ErrInvalidInput, ErrInvalidInput},
		{"unauthorized", ErrUnauthorized, ErrUnauthorized},
		{"source", ErrSourceMismatch, ErrSourceMismatch},
		{"subject", ErrSubjectMismatch, ErrSubjectMismatch},
		{"missing", ErrSubjectNotFound, ErrSubjectNotFound},
		{"revision", ErrRevisionConflict, ErrRevisionConflict},
		{"overflow", ErrRevisionOverflow, ErrRevisionOverflow},
		{"command", ErrCommandConflict, ErrCommandConflict},
		{"evidence", ErrEvidenceConflict, ErrEvidenceConflict},
		{"limit", ErrLimitExceeded, ErrLimitExceeded},
		{"unsupported", ErrUnsupported, ErrUnsupported},
		{"cursor", ErrStaleCursor, ErrStaleCursor},
		{"uncertain", ErrIndeterminate, ErrIndeterminate},
		{"busy", ErrRepositoryContention, ErrRepositoryContention},
		{"unavailable", ErrRepositoryUnavailable, ErrRepositoryUnavailable},
		{"canceled", context.Canceled, context.Canceled},
		{"deadline", context.DeadlineExceeded, context.DeadlineExceeded},
		{"domain-invalid", domain.ErrInvalidCatalog, ErrInvalidInput},
		{"domain-authority", domain.ErrCatalogUnauthorized, ErrUnauthorized},
		{"domain-source", domain.ErrCatalogSourceMismatch, ErrSourceMismatch},
		{"domain-subject", domain.ErrCatalogSubjectMismatch, ErrSubjectMismatch},
		{"domain-revision", domain.ErrCatalogRevisionConflict, ErrRevisionConflict},
		{"domain-overflow", domain.ErrCatalogRevisionOverflow, ErrRevisionOverflow},
		{"domain-command", domain.ErrCatalogCommandConflict, ErrCommandConflict},
		{"domain-observation", domain.ErrCatalogObservationConflict, ErrEvidenceConflict},
		{"domain-artifact", domain.ErrCatalogArtifactConflict, ErrEvidenceConflict},
		{"domain-unsupported", domain.ErrCatalogUnsupported, ErrUnsupported},
		{"domain-limit", domain.LimitError{Resource: domain.MutationObservations}, ErrLimitExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			command := f.command(t)
			command.Observations = []domain.CatalogObservation{f.observation(t, payload, "candidate")}
			f.tx.repository.failure = errors.Join(fmt.Errorf("/private/catalog.sqlite SQL tc_catalog_decisions token=secret raw={private}: %w", tc.cause), errors.New("credential=other-secret"))
			got, err := f.service.RecordObservations(t.Context(), command)
			if err != tc.want || errors.Unwrap(err) != nil || !reflect.DeepEqual(got, ReviewResult{}) || f.tx.repository.writes != 0 {
				t.Fatal("unsafe or untyped mutation error", got, err)
			}
			read, found, err := f.service.LookupReview(t.Context(), domain.RecordObservations, command)
			if err != tc.want || errors.Unwrap(err) != nil || found || !reflect.DeepEqual(read, ReviewResult{}) || f.tx.repository.writes != 0 {
				t.Fatal("unsafe readback error", read, found, err)
			}
		})
	}
}
