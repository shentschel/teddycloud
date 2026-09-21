package evidence

import (
	"testing"
	"time"
)

func TestAuditObservationIdentityRejectsEveryProvenanceMutation(t *testing.T) {
	base := observation(t, "0", "original", Accepted)
	otherSource, _ := NewSource("other-fixture", "r2", "other-record")
	mutations := []func(*Observation){
		func(o *Observation) { o.source = otherSource },
		func(o *Observation) { o.claim.Subject = "other-subject" },
		func(o *Observation) { o.claim.Key = "other-key" },
		func(o *Observation) { o.claim.Value = "custom" },
		func(o *Observation) { o.observedAt = time.Unix(2, 0).UTC() },
		func(o *Observation) { o.confidence = Corroborated },
		func(o *Observation) { o.review = Disputed },
	}
	for _, mutate := range mutations {
		changed := base
		mutate(&changed)
		for _, input := range [][]Observation{{base, changed}, {changed, base}} {
			if _, err := Canonical(input); err != ErrConflict {
				t.Fatal("same observation ID accepted conflicting provenance")
			}
		}
	}
}

func TestAuditAcceptedFactRetainsPendingRejectedAndDisputedEvidence(t *testing.T) {
	accepted := observation(t, "0", "original", Accepted)
	pending := observation(t, "1", "custom", Pending)
	rejected := observation(t, "2", "custom", Rejected)
	disputed := observation(t, "3", "custom", Disputed)
	fact, err := Accept(accepted.Claim(), []Observation{disputed, rejected, pending, accepted})
	if err != nil {
		t.Fatal(err)
	}
	if len(fact.Observations()) != 4 || len(fact.Support()) != 1 {
		t.Fatal("dissent or reviewed support was discarded")
	}
	if fact.Support()[0] != accepted.ID() {
		t.Fatal("unaccepted evidence became support")
	}
}
