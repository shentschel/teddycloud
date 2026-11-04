package assignment

import (
	"context"
	"fmt"
	"testing"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func identityTag(n int) (identity.TagID, error) {
	return identity.ParseTagID(fmt.Sprintf("tag_%026x", n))
}
func effective(t *testing.T, s *Service, in Command, at int64, knowledge *uint64) AssignmentView {
	t.Helper()
	view, err := s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: in.TagID, At: time.Unix(at, 0), KnowledgeRevision: knowledge})
	if err != nil {
		t.Fatal(err)
	}
	return view
}
func TestAssignmentDesiredFutureAndAsKnown(t *testing.T) {
	s, m := fixture(t)
	first := command(t, 1, Assign, 0)
	absent := effective(t, s, first, 100, nil)
	if absent.AggregatePresent || absent.Desired.State != d.NoAssignment {
		t.Fatal("absent aggregate gained fallback")
	}
	baseline := command(t, 1, RecordOriginalTarget, 0)
	execute(t, s, baseline)
	view := effective(t, s, first, 100, nil)
	if !view.AggregatePresent || view.OriginalState != d.OriginalTargetKnown || view.Desired.State != d.NoAssignment || view.Desired.Target != (d.OriginalTarget{}) {
		t.Fatal("baseline selected assignment")
	}
	assign := command(t, 2, Assign, 1)
	execute(t, s, assign)
	review := command(t, 3, RecordClassification, 2)
	execute(t, s, review)
	future := command(t, 4, Assign, 3)
	for target := range m.targets {
		if target != assign.Target {
			future.Target = target
		}
	}
	execute(t, s, future)
	unassign := command(t, 5, Unassign, 4)
	execute(t, s, unassign)
	retire := command(t, 6, RetireAssignment, 5)
	execute(t, s, retire)
	for _, test := range []struct {
		at         int64
		state      d.DesiredState
		transition uint64
	}{
		{19, d.NoAssignment, 0}, {20, d.Assigned, 2}, {39, d.Assigned, 2}, {40, d.Assigned, 4}, {50, d.Unassigned, 5}, {60, d.Retired, 6},
	} {
		view := effective(t, s, assign, test.at, nil)
		if view.Desired.State != test.state || view.Desired.TransitionRevision != test.transition || view.Desired.HeadRevision != 6 {
			t.Fatalf("at %d: %+v", test.at, view)
		}
	}
	view = effective(t, s, assign, 30, nil)
	if view.Desired.Target != assign.Target || !view.Desired.Until.Equal(time.Unix(40, 0)) {
		t.Fatal("future transition won early")
	}
	known := uint64(2)
	view = effective(t, s, assign, 100, &known)
	if view.Desired.Target != assign.Target || !view.Desired.Until.IsZero() || view.Classification != d.Unknown || view.OriginalState != d.OriginalTargetKnown {
		t.Fatal("future knowledge leaked")
	}
	zero := uint64(0)
	view = effective(t, s, assign, 100, &zero)
	if view.Desired.State != d.NoAssignment || view.Classification != d.Unknown || view.OriginalState != d.OriginalTargetUnknown {
		t.Fatal("zero knowledge leaked")
	}
	ahead := uint64(7)
	_, err := s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: assign.TagID, At: time.Unix(100, 0), KnowledgeRevision: &ahead})
	wantError(t, err, ErrKnowledgeRevision)
	// Retirement seals immediately, even when its intended time is future.
	_, err = s.Execute(context.Background(), command(t, 7, Unassign, 6))
	wantError(t, err, ErrRetired)
}
func TestAssignmentNoFallbackOrRetarget(t *testing.T) {
	s, m := fixture(t)
	in := command(t, 1, Assign, 0)
	execute(t, s, in)
	value := m.targets[in.Target]
	value.Superseded = true
	value.EditorialConflict = true
	m.targets[in.Target] = value
	view := effective(t, s, in, 100, nil)
	if view.Desired.Target != in.Target || !view.Superseded || !view.EditorialConflict || !view.CatalogOnly || view.BoundUnchecked || view.Dimensions.Protection() != d.ProtectedCustom || view.Classification != d.Unknown {
		t.Fatal("metadata/supersession changed explicit dimensions")
	}
	value.CatalogRevision++
	value.Bound = true
	m.targets[in.Target] = value
	view = effective(t, s, in, 100, nil)
	if view.Desired.Target != in.Target || view.CatalogRevision != 4 || view.CatalogOnly || !view.BoundUnchecked {
		t.Fatal("live annotations retargeted")
	}
	// Explicit superseded targets with current dependencies remain admissible.
	next := command(t, 2, Assign, 1)
	next.ExpectedCatalogRevision = 4
	execute(t, s, next)
	unassign := command(t, 3, Unassign, 2)
	execute(t, s, unassign)
	view = effective(t, s, in, 100, nil)
	if view.Desired.State != d.Unassigned || view.Desired.Target != (d.OriginalTarget{}) {
		t.Fatal("unassign used fallback")
	}
	// Historical retained targets cannot disappear into catalog-only/not-found.
	delete(m.targets, in.Target)
	_, err := s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: in.TagID, At: time.Unix(15, 0)})
	wantError(t, err, ErrUnavailable)
}
func TestAssignmentReviewConflictAsKnown(t *testing.T) {
	s, _ := fixture(t)
	first := command(t, 1, RecordClassification, 0)
	execute(t, s, first)
	other := command(t, 2, RecordClassification, 1)
	other.Classification = d.CustomCard
	execute(t, s, other)
	conflict := effective(t, s, first, 100, nil)
	if conflict.Classification != d.Conflict || conflict.Desired.State != d.NoAssignment {
		t.Fatal("contrary review did not conflict")
	}
	resolve := command(t, 3, ResolveClassification, 2)
	resolve.Support = []d.EvidenceID{first.Evidence.ID()}
	execute(t, s, resolve)
	resolved := effective(t, s, first, 100, nil)
	if resolved.Classification != d.Original {
		t.Fatal("resolution lost")
	}
	other = command(t, 4, RecordClassification, 3)
	other.Classification = d.CustomCard
	execute(t, s, other)
	if effective(t, s, first, 100, nil).Classification != d.Conflict {
		t.Fatal("new contrary support failed to reopen")
	}
	known := uint64(3)
	if effective(t, s, first, 100, &known).Classification != d.Original {
		t.Fatal("later evidence changed as-known classification")
	}
}
func TestAssignmentQueryValidation(t *testing.T) {
	s, _ := fixture(t)
	in := command(t, 1, Assign, 0)
	ahead := uint64(1)
	_, err := s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: in.TagID, At: time.Unix(100, 0), KnowledgeRevision: &ahead})
	wantError(t, err, ErrKnowledgeRevision)
	_, err = s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: in.TagID})
	wantError(t, err, ErrInvalidInput)
	_, err = s.GetEffectiveAssignment(context.Background(), EffectiveQuery{At: time.Unix(100, 0)})
	wantError(t, err, ErrInvalidInput)
}

func TestAssignmentMissingRetainedBaselineFailsClosed(t *testing.T) {
	s, m := fixture(t)
	baseline := command(t, 1, RecordOriginalTarget, 0)
	execute(t, s, baseline)
	delete(m.targets, baseline.Target)
	_, err := s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: baseline.TagID, At: time.Unix(100, 0)})
	wantError(t, err, ErrUnavailable)
	_, err = s.Execute(context.Background(), command(t, 2, Unassign, 1))
	wantError(t, err, ErrUnavailable)
	if m.commits != 1 {
		t.Fatal("missing retained reference admitted a write")
	}
	// Stored readback is still deterministic without rerunning live admission.
	_, found, err := s.LookupAssignmentCommand(context.Background(), baseline)
	if err != nil || !found {
		t.Fatal(err)
	}
}
