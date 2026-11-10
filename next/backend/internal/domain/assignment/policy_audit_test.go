package assignment

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAssignmentClassificationSingleAuthority(t *testing.T) {
	// An independent truth table: only accepted, same-subject assertions enter
	// classification, irrespective of the descriptive source or assertion kind.
	for _, kind := range []EvidenceKind{OperatorAssertion, ImportedAssertion} {
		for _, disposition := range []EvidenceDisposition{PendingEvidence, DisputedEvidence, RejectedEvidence, AcceptedEvidence} {
			for _, claim := range []PhysicalClassification{Original, CustomCard} {
				s := emptyEvidence(t)
				e, err := NewEvidence(evidenceID(t, 1), s.Subject(), kind, "https://manufacturer.example/official-original", disposition)
				if err != nil {
					t.Fatal(err)
				}
				next, err := s.AdmitClassification(reviewCore(t), reviewRequest(t, 1, 0), e, claim)
				if err != nil {
					t.Fatal(err)
				}
				want := Unknown
				if disposition == AcceptedEvidence {
					want = claim
				}
				if next.Classification() != want || s.Classification() != Unknown || len(next.Records()) != 1 || next.Records()[0].Evidence.Kind() != kind {
					t.Fatal("source or pending assertion acquired authority")
				}
				if got, err := s.AdmitClassification(ReviewAuthority{}, reviewRequest(t, 1, 0), e, claim); !errors.Is(err, ErrReviewAuthority) || !reflect.DeepEqual(got, s) {
					t.Fatal("unauthenticated review admitted", err)
				}
			}
		}
	}
	for _, order := range [][]PhysicalClassification{{Original, CustomCard}, {CustomCard, Original}} {
		s := emptyEvidence(t)
		for i, claim := range order {
			s = addClassification(t, s, i+1, AcceptedEvidence, claim)
		}
		if s.Classification() != Conflict {
			t.Fatal("contrary accepted evidence selected a winner")
		}
		before := s
		foreign := claimEvidence(t, reviewTag(t, 2), 3, AcceptedEvidence)
		if got, err := s.AdmitClassification(reviewCore(t), reviewRequest(t, 3, 2), foreign, Original); err != ErrEvidenceSubject || !reflect.DeepEqual(got, before) {
			t.Fatal("cross-subject admission", err)
		}
		if got, err := s.ResolveClassification(reviewCore(t), reviewRequest(t, 3, 2), Original, []EvidenceID{evidenceID(t, 99)}); err != ErrInvalidResolution || !reflect.DeepEqual(got, before) {
			t.Fatal("foreign resolution support", err)
		}
		s, err := s.ResolveClassification(reviewCore(t), reviewRequest(t, 3, 2), order[0], []EvidenceID{evidenceID(t, 1)})
		if err != nil || s.Classification() != order[0] || len(s.Records()) != 3 {
			t.Fatal("resolution discarded dissent", err)
		}
		s = addClassification(t, s, 4, AcceptedEvidence, order[1])
		if s.Classification() != Conflict {
			t.Fatal("later contrary support did not reopen conflict")
		}
	}
}

func TestAssignmentNoHeuristicAuthority(t *testing.T) {
	s := emptyEvidence(t)
	for _, protection := range []ContentProtection{ProtectionUnknown, Unprotected, ProtectedCustom} {
		for _, public := range []bool{false, true} {
			for _, custom := range []bool{false, true} {
				dimensions, err := NewContentDimensions(public, custom, protection)
				if err != nil || dimensions.PublicSupport() != public || dimensions.CustomOrigin() != custom || dimensions.Protection() != protection {
					t.Fatal("independent dimensions collapsed")
				}
				if s.Classification() != Unknown {
					t.Fatal("content classified physical Tag")
				}
			}
		}
	}
	ledger := decisionFixture(t)
	ledger = admitDecision(t, ledger, decisionCommand(t, ledger, 1, Assign, 10))
	s = addBaseline(t, s, 1, AcceptedEvidence, baselineTarget(t, 2, 2))
	s = addClassification(t, s, 2, AcceptedEvidence, CustomCard)
	ledger = admitDecision(t, ledger, decisionCommand(t, ledger, 2, Unassign, 20))
	p, err := ledger.Project(time.Unix(20, 0), nil)
	if err != nil || p.State != Unassigned || p.Target != (OriginalTarget{}) || s.OriginalState() != OriginalTargetKnown {
		t.Fatal("original baseline became a fallback", err)
	}
}

func FuzzAssignmentDecisionSequence(f *testing.F) {
	for _, seed := range [][]byte{{0, 0, 1, 2}, {0, 3, 4, 5, 1, 2}, {2, 0}, {0, 6, 0, 1, 2, 3}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 32 {
			input = input[:32]
		}
		s := decisionFixture(t)
		var accepted []DecisionCommand
		var results []DecisionResult
		sealed := false
		lastAt := int64(0)
		for i, b := range input {
			if b%7 == 3 && len(accepted) > 0 {
				index := int(b) % len(accepted)
				next, got, err := s.Admit(accepted[index], DecisionReferences{}, time.Time{})
				if err != nil || got != results[index] || !reflect.DeepEqual(next, s) {
					t.Fatal("exact replay mutated ledger", err)
				}
				continue
			}
			kind := []DecisionKind{Assign, Unassign, Retire}[int(b)%3]
			at := lastAt + 10
			c := decisionCommand(t, s, i+1, kind, at)
			refs := decisionRefs(c)
			if b%7 == 4 {
				refs.TagRevision++
			}
			if b%7 == 5 {
				in := c.Input()
				in.ExpectedRevision++
				var err error
				c, err = NewDecisionCommand(in)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := s
			next, r, err := s.Admit(c, refs, time.Unix(int64(1000-i), 0))
			shouldReject := sealed || b%7 == 4 || b%7 == 5
			if shouldReject {
				if err == nil || !reflect.DeepEqual(next, before) {
					t.Fatal("rejected decision changed history")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			s = next
			lastAt = at
			sealed = kind == Retire
			accepted = append(accepted, c)
			results = append(results, r)
			// Oracle walks retained explicit transitions by effective time AND knowledge;
			// recorded times intentionally run backwards.
			for k := uint64(0); k <= uint64(len(accepted)); k++ {
				for _, when := range []int64{1, at - 1, at, at + 100} {
					p, err := s.Project(time.Unix(when, 0), &k)
					if err != nil {
						t.Fatal(err)
					}
					state := NoAssignment
					target := OriginalTarget{}
					revision := uint64(0)
					for j, command := range accepted[:k] {
						in := command.Input()
						if in.EffectiveAt.After(time.Unix(when, 0)) {
							break
						}
						revision = uint64(j + 1)
						target = OriginalTarget{}
						switch in.Kind {
						case Assign:
							state = Assigned
							target = in.Target
						case Unassign:
							state = Unassigned
						case Retire:
							state = Retired
						}
					}
					if p.State != state || p.Target != target || p.TransitionRevision != revision {
						t.Fatal("time/knowledge oracle mismatch")
					}
				}
			}
		}
	})
}
