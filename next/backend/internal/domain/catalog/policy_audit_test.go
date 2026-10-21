package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

// Test all arrival and acceptance permutations against the same independent
// oracle: two different accepted values conflict, regardless of confidence.
func TestCatalogPolicyPermutations(t *testing.T) {
	permutations := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, arrival := range permutations {
		for _, acceptance := range permutations {
			state, source, ingest, review := reviewFixture(t)
			observations := []CatalogObservation{
				testObservation(t, source, ingest, idPayloadA, "A"),
				testObservation(t, source, ingest, "1123456789abcdefghjkmnpqrs", "B"),
				testObservation(t, source, ingest, "2123456789abcdefghjkmnpqrs", "A"),
			}
			for i := range observations {
				input := observations[i].Input()
				input.Confidence = []evidence.Confidence{evidence.Corroborated, evidence.ConfidenceUnknown, evidence.Tentative}[i]
				var err error
				observations[i], err = NewCatalogObservation(ingest, source, input)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, i := range arrival {
				state = stageReview(t, state, source, review, observations[i])
				if state.Field(TitleClaimKey).Status != CatalogFactUnknown {
					t.Fatal("confidence/arrival promoted pending evidence")
				}
			}
			for _, i := range acceptance {
				state, _ = applyReview(t, state, review, reviewCommand(t, state, source, AcceptSupport, observations[i].ID()))
			}
			field := state.Field(TitleClaimKey)
			if field.Status != CatalogFactConflict || len(field.Alternatives) != 2 || len(state.Observations()) != 3 {
				t.Fatal("permutation lost conflict or dissent")
			}
			if _, ok := field.Value(); ok {
				t.Fatal("conflict exposed a winner")
			}
			if field.Alternatives[0].Claim.Text() != "A" || len(field.Alternatives[0].Support) != 2 || field.Alternatives[1].Claim.Text() != "B" {
				t.Fatal("support changed with arrival order")
			}
			raw, err := state.PersistenceEncoding()
			if err != nil {
				t.Fatal(err)
			}
			detached, err := RehydrateCatalogReviewState(raw, CatalogPersistenceBindings{AllowUnbound: true})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(detached.Field(TitleClaimKey), field) {
				t.Fatal("persistence changed conflict")
			}
			detachedSource := detached.Observations()[0].Input().Coordinate.Artifact().Source()
			minted, err := NewReviewCapability(review.Reviewer(), detachedSource)
			if err != nil {
				t.Fatal(err)
			}
			if err = minted.Authorize(detachedSource, TitleClaimKey); !errors.Is(err, ErrCatalogUnauthorized) {
				t.Fatal("serialized provenance minted authority")
			}
			var forged ReviewCapability
			encoded, _ := json.Marshal(review)
			if err := json.Unmarshal(encoded, &forged); err != nil {
				t.Fatal(err)
			}
			rejectedReview(t, state, forged, reviewCommand(t, state, source, ResolveConflict, observations[1].ID()), ErrCatalogUnauthorized)
			source.Revoke()
			restored, err := RehydrateCatalogReviewState(raw, CatalogPersistenceBindings{AllowUnbound: true, Sources: []SourceRegistration{source}})
			if err != nil {
				t.Fatal(err)
			}
			rejectedReview(t, restored, review, reviewCommand(t, restored, source, ResolveConflict, observations[1].ID()), ErrCatalogUnauthorized)
		}
	}
}

// Bounded model-based sequence: the oracle keeps accepted values separately
// from the implementation's retained decisions, and checks every transition.
func FuzzCatalogDecisionSequence(f *testing.F) {
	for _, seed := range [][]byte{{0, 1, 2, 3, 4, 5}, {2, 2, 3, 0, 1}, {4, 0, 3, 2}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, sequence []byte) {
		if len(sequence) > 24 {
			return
		}
		state, source, ingest, review := reviewFixture(t)
		a := testObservation(t, source, ingest, idPayloadA, "A")
		b := testObservation(t, source, ingest, "1123456789abcdefghjkmnpqrs", "B")
		state = stageReview(t, state, source, review, a, b)
		selected := map[string]bool{}
		revoked := false
		for _, step := range sequence {
			before, _ := state.PersistenceEncoding()
			if step%6 == 5 {
				source.Revoke()
				revoked = true
				continue
			}
			kind, support, value := AcceptSupport, a.ID(), "A"
			if step%2 == 1 {
				support, value = b.ID(), "B"
			}
			if step%6 == 2 || step%6 == 3 {
				kind = ResolveConflict
			}
			command := reviewCommand(t, state, source, kind, support)
			cap := review
			if step%6 == 4 {
				cap = ReviewCapability{}
			}
			next, result, err := state.Apply(cap, command)
			if revoked || step%6 == 4 {
				after, _ := next.PersistenceEncoding()
				if !errors.Is(err, ErrCatalogUnauthorized) || !bytes.Equal(before, after) || result.Revision.Uint64() != 0 {
					t.Fatal("unauthorized transition mutated state")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == ResolveConflict {
				selected = map[string]bool{}
			}
			selected[value] = true
			field := next.Field(TitleClaimKey)
			if len(selected) == 2 {
				if field.Status != CatalogFactConflict {
					t.Fatal("oracle conflict lost")
				}
			} else {
				claim, ok := field.Value()
				if !ok || !selected[claim.Text()] {
					t.Fatal("oracle accepted value changed")
				}
			}
			if next.Revision().Uint64() != state.Revision().Uint64()+1 {
				t.Fatal("revision did not advance exactly once")
			}
			replay, again, err := next.Apply(cap, command)
			if err != nil || replay.Revision() != next.Revision() || !reflect.DeepEqual(result, again) {
				t.Fatal("exact replay changed result")
			}
			raw, err := next.PersistenceEncoding()
			if err != nil {
				t.Fatal(err)
			}
			restored, err := RehydrateCatalogReviewState(raw, CatalogPersistenceBindings{AllowUnbound: true, Sources: []SourceRegistration{source}, Reviewers: []IssuerPrincipal{review.Reviewer()}})
			if err != nil {
				t.Fatal(err)
			}
			roundtrip, _ := restored.PersistenceEncoding()
			if !bytes.Equal(raw, roundtrip) {
				t.Fatal("retained history changed")
			}
			state = restored
		}
	})
}
