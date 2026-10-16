package catalog

import (
	"errors"
	"testing"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

func TestCatalogBaselineAdoptionServiceAdmission(t *testing.T) {
	f := newFixture(t)
	id, _ := domain.NewCatalogCommandID()
	command := AdoptionCommand{ID: id, Content: f.subject.ContentID(), ExpectedAggregate: f.tx.repository.review.AggregateRevision, BaselineSHA256: [32]byte{1}, Reason: "adopt"}
	// Ordinary offline review authority cannot invoke the baseline capability.
	if _, err := f.service.AdoptBaseline(t.Context(), command); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	// An old port implementation cannot silently emulate adoption with Save.
	if _, _, err := f.service.ReadBaseline(t.Context(), command.Content); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if f.tx.repository.writes != 0 {
		t.Fatal("admission wrote through old port")
	}
	command.Content = domain.CatalogSubject{}.ContentID()
	if _, err := f.service.AdoptBaseline(t.Context(), command); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}
