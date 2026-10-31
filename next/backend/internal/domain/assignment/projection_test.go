package assignment

import (
	"reflect"
	"testing"
	"time"
)

func projected(t *testing.T, s DecisionLedger, at int64, knowledge *uint64) DesiredAssignment {
	t.Helper()
	p, err := s.Project(time.Unix(at, 0), knowledge)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestAssignmentTimelineProjection(t *testing.T) {
	s := decisionFixture(t)
	for _, tc := range []struct {
		kind DecisionKind
		at   int64
	}{{Assign, 10}, {Assign, 20}, {Unassign, 30}} {
		s = admitDecision(t, s, decisionCommand(t, s, int(s.Revision()+1), tc.kind, tc.at))
	}
	for _, tc := range []struct {
		at          int64
		state       DesiredState
		revision    uint64
		from, until int64
	}{
		{9, NoAssignment, 0, 0, 0}, {10, Assigned, 1, 10, 20}, {19, Assigned, 1, 10, 20},
		{20, Assigned, 2, 20, 30}, {29, Assigned, 2, 20, 30}, {30, Unassigned, 3, 30, 0}, {40, Unassigned, 3, 30, 0},
	} {
		p := projected(t, s, tc.at, nil)
		if p.State != tc.state || p.TransitionRevision != tc.revision || p.HeadRevision != 3 || p.KnowledgeRevision != 3 || p.AssignmentID != s.ID() || p.TagID != s.TagID() {
			t.Fatalf("at %d: %+v", tc.at, p)
		}
		if (tc.from == 0) != p.From.IsZero() || tc.from != 0 && p.From.Unix() != tc.from || (tc.until == 0) != p.Until.IsZero() || tc.until != 0 && p.Until.Unix() != tc.until {
			t.Fatalf("derived interval at %d: %+v", tc.at, p)
		}
		if p.State == Assigned {
			if p.Target != baselineTarget(t, 1, int(tc.revision)) {
				t.Fatal("wrong exact tuple")
			}
		} else if p.Target != (OriginalTarget{}) {
			t.Fatal("unassigned retained a target")
		}
	}
	p, err := s.Project(time.Unix(20, 0).Add(-time.Nanosecond), nil)
	if err != nil || p.TransitionRevision != 1 {
		t.Fatal("half-open nanosecond boundary")
	}
	// Inserting a past transition is rejected, with full old state retained.
	c := decisionCommand(t, s, 4, Assign, 29)
	next, _, err := s.Admit(c, decisionRefs(c), time.Unix(100, 0))
	if err != ErrInvalidInterval || !reflect.DeepEqual(next, s) {
		t.Fatal("retroactive insertion accepted")
	}
}

func TestAssignmentFutureAndAsKnown(t *testing.T) {
	s := decisionFixture(t)
	s = admitDecision(t, s, decisionCommand(t, s, 1, Assign, 10))
	s = admitDecision(t, s, decisionCommand(t, s, 2, Assign, 1000))
	for _, tc := range []struct {
		at              int64
		knowledge, want uint64
	}{{9, 2, 0}, {10, 2, 1}, {999, 2, 1}, {1000, 2, 2}, {1001, 1, 1}, {1001, 0, 0}} {
		p := projected(t, s, tc.at, &tc.knowledge)
		if p.TransitionRevision != tc.want || p.KnowledgeRevision != tc.knowledge || p.HeadRevision != 2 {
			t.Fatalf("as-known %+v", p)
		}
		if tc.knowledge == 1 && !p.Until.IsZero() {
			t.Fatal("future knowledge leaked an interval end")
		}
	}
	for _, r := range []uint64{3, ^uint64(0)} {
		if _, err := s.Project(time.Unix(10, 0), &r); err != ErrKnowledgeRevision {
			t.Fatal("future knowledge clamped")
		}
	}
	if _, err := s.Project(time.Time{}, nil); err != ErrInvalidDecision {
		t.Fatal("implicit time accepted")
	}
	if _, err := (DecisionLedger{}).Project(time.Unix(10, 0), nil); err != ErrInvalidReference {
		t.Fatal("invalid ledger projected as unassigned")
	}
}

func TestAssignmentTransitionRevisionOrdering(t *testing.T) {
	s := decisionFixture(t)
	for n, kind := range []DecisionKind{Assign, Assign, Unassign, Retire} {
		s = admitDecision(t, s, decisionCommand(t, s, n+1, kind, int64(n+1)*10))
	}
	// Equal audit times do not affect transition ordering or as-known selection.
	// Effective times strictly increase even across different transition kinds.
	for r, state := range []DesiredState{NoAssignment, Assigned, Assigned, Unassigned, Retired} {
		knowledge := uint64(r)
		p := projected(t, s, 40, &knowledge)
		if p.State != state || p.TransitionRevision != knowledge {
			t.Fatal("retained knowledge did not select the last eligible transition")
		}
		if r == 2 && p.Target != baselineTarget(t, 1, 2) {
			t.Fatal("earlier knowledge selected the wrong target")
		}
	}
	if p := projected(t, s, 9, nil); p.State != NoAssignment {
		t.Fatal("future transitions won early")
	}
}

func TestAssignmentRetirement(t *testing.T) {
	s := decisionFixture(t)
	a := decisionCommand(t, s, 1, Assign, 10)
	s = admitDecision(t, s, a)
	r := decisionCommand(t, s, 2, Retire, 1000)
	s = admitDecision(t, s, r)
	if !s.Sealed() || len(s.Events()) != 2 {
		t.Fatal("retirement did not retain tombstone and seal")
	}
	for _, kind := range []DecisionKind{Assign, Unassign, Retire} {
		c := decisionCommand(t, s, 3, kind, 1001)
		next, _, err := s.Admit(c, decisionRefs(c), time.Unix(100, 0))
		if err != ErrDecisionRetired || !reflect.DeepEqual(next, s) {
			t.Fatal("future retirement permitted a new transition")
		}
	}
	for _, c := range []DecisionCommand{a, r} {
		if _, _, err := s.Admit(c, DecisionReferences{}, time.Time{}); err != nil {
			t.Fatal("tombstone blocked replay")
		}
	}
	if p := projected(t, s, 999, nil); p.State != Assigned || p.Target != a.Input().Target || p.Until.Unix() != 1000 {
		t.Fatal("historical target lost")
	}
	if p := projected(t, s, 1000, nil); p.State != Retired || p.Target != (OriginalTarget{}) {
		t.Fatal("retirement not effective at boundary")
	}
	known := uint64(1)
	if p := projected(t, s, 1001, &known); p.State != Assigned || !p.Until.IsZero() {
		t.Fatal("tombstone leaked into earlier knowledge")
	}
	first := decisionFixture(t)
	first = admitDecision(t, first, decisionCommand(t, first, 4, Retire, 10))
	if p := projected(t, first, 10, nil); p.State != Retired {
		t.Fatal("first-command retire failed")
	}
}

func TestAssignmentDecisionNoFallback(t *testing.T) {
	// Accepted original baseline is deliberately separate; it cannot flow into
	// a ledger constructor/projector or substitute for an absent Assign tuple.
	evidence := emptyEvidence(t)
	evidence = addBaseline(t, evidence, 1, AcceptedEvidence, baselineTarget(t, 1, 1))
	if _, ok := evidence.OriginalBaseline(); !ok {
		t.Fatal("baseline fixture missing")
	}
	s := decisionFixture(t)
	if p := projected(t, s, 100, nil); p.State != NoAssignment || p.Target != (OriginalTarget{}) {
		t.Fatal("baseline became assignment")
	}
	s = admitDecision(t, s, decisionCommand(t, s, 1, Unassign, 10))
	if p := projected(t, s, 100, nil); p.State != Unassigned || p.Target != (OriginalTarget{}) {
		t.Fatal("explicit unassign fell back")
	}
	c := decisionCommand(t, s, 2, Assign, 20)
	in := c.Input()
	in.Target = OriginalTarget{}
	if _, err := NewDecisionCommand(in); err != ErrInvalidReference {
		t.Fatal("missing target used baseline/catalog fallback")
	}
	s = admitDecision(t, s, c)
	// Later independent baseline/catalog-like tuples cannot retarget a decision.
	evidence = addBaseline(t, evidence, 2, AcceptedEvidence, baselineTarget(t, 2, 2))
	if p := projected(t, s, 100, nil); p.Target != c.Input().Target {
		t.Fatal("implicit supersession retargeting")
	}
}
