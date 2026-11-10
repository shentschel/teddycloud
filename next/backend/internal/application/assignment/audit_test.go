package assignment

import (
	"errors"
	"reflect"
	"testing"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	c "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func TestAssignmentAuditNoBypass(t *testing.T) {
	for _, k := range []Kind{Assign, RecordClassification, ResolveClassification, RecordOriginalTarget, ResolveOriginalTarget, Unassign, RetireAssignment} {
		t.Run(string(rune('0'+k)), func(t *testing.T) {
			s, m := fixture(t)
			m.actor.Decide = false
			m.actor.Review = false
			in := command(t, 1, k, 0)
			in.Actor = "manufacturer"
			in.URI = OptionalURI{Present: true, Value: "https://manufacturer.example/owned"}
			got, err := s.Execute(t.Context(), in)
			if got != (Result{}) || err != ErrUnauthorized || m.commits != 0 || len(m.snapshots) != 0 {
				t.Fatal("request provenance bypassed authentication", err)
			}
		})
	}
	s, m := fixture(t)
	assign := command(t, 1, Assign, 0)
	assign.URI = OptionalURI{Present: true, Value: "https://manufacturer.example/original"}
	execute(t, s, assign)
	before := cloneSnapshot(m.snapshots[assign.TagID])
	bad := command(t, 2, RecordClassification, 1)
	other, _ := identity.ParseTagID("tag_00000000000000000000000002")
	bad.Evidence, _ = d.NewEvidence(bad.Evidence.ID(), other, d.ImportedAssertion, "official manufacturer", d.AcceptedEvidence)
	if got, err := s.Execute(t.Context(), bad); got != (Result{}) || err != ErrInvalidInput || !reflect.DeepEqual(before, m.snapshots[assign.TagID]) {
		t.Fatal("cross-subject claim wrote state", err)
	}
	// Same fingerprint, independent owners. Ambiguous accepted ownership must
	// reject even though a valid typed tuple and a reassuring URI are supplied.
	value := m.targets[assign.Target]
	value.Ownership.Alternatives = append(value.Ownership.Alternatives, value.Ownership.Alternatives[0])
	value.Ownership.Status = c.CatalogFactConflict
	m.targets[assign.Target] = value
	newCommand := command(t, 3, Assign, 1)
	if got, err := s.Execute(t.Context(), newCommand); got != (Result{}) || !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(before, m.snapshots[assign.TagID]) {
		t.Fatal("ambiguous retained target wrote state", err)
	}
	// Exact authorized replay survives changed dependencies without another write.
	if got, err := s.Execute(t.Context(), assign); err != nil || got != before.Events[0].Result || m.commits != 1 {
		t.Fatal("replay admitted a new decision", err)
	}
}

func TestAssignmentAuditTimeRetirementAndURI(t *testing.T) {
	s, m := fixture(t)
	first := command(t, 1, Assign, 0)
	first.URI = OptionalURI{Present: true, Value: "HTTPS://Unresolvable.Invalid:443/original%2Dcard"}
	one := execute(t, s, first)
	q := EffectiveQuery{TagID: first.TagID, At: first.EffectiveAt}
	initial, err := s.GetAssignment(t.Context(), q)
	if err != nil || initial.Classification != d.Unknown || initial.OriginalState != d.OriginalTargetUnknown {
		t.Fatal("URI/content dimensions escalated physical authority", err)
	}
	baseline := command(t, 2, RecordOriginalTarget, 1)
	content, _ := identity.ParseContentID("cnt_00000000000000000000000002")
	version, _ := identity.ParseContentVersionID("ver_00000000000000000000000002")
	baseline.Target, _ = d.NewOriginalTarget(content, version)
	execute(t, s, baseline)
	unassign := command(t, 3, Unassign, 2)
	execute(t, s, unassign)
	retire := command(t, 4, RetireAssignment, 3)
	retire.EffectiveAt = time.Unix(100000, 0)
	execute(t, s, retire)
	for _, tc := range []struct {
		at     time.Time
		state  d.DesiredState
		target d.OriginalTarget
	}{{first.EffectiveAt, d.Assigned, first.Target}, {unassign.EffectiveAt, d.Unassigned, d.OriginalTarget{}}, {retire.EffectiveAt.Add(-time.Nanosecond), d.Unassigned, d.OriginalTarget{}}, {retire.EffectiveAt, d.Retired, d.OriginalTarget{}}} {
		q.At = tc.at
		view, err := s.GetAssignment(t.Context(), q)
		if err != nil || !view.HeadRetired || view.Desired.State != tc.state || view.Desired.Target != tc.target || view.OriginalTarget != baseline.Target {
			t.Fatal("future tombstone/time/baseline conflated", view, err)
		}
	}
	q.At = retire.EffectiveAt
	q.KnowledgeRevision = &one.Revision
	view, err := s.GetAssignment(t.Context(), q)
	if err != nil || view.Desired.State != d.Assigned || view.Desired.Target != first.Target || view.OriginalState != d.OriginalTargetUnknown {
		t.Fatal("head replaced historical knowledge", err)
	}
	after := command(t, 5, RecordClassification, 4)
	if _, err := s.Execute(t.Context(), after); err != ErrRetired {
		t.Fatal("future retirement did not seal writes", err)
	}
	if len(m.snapshots[first.TagID].Events) != 4 {
		t.Fatal("rejected attempt retained")
	}
	if got, err := s.Execute(t.Context(), first); err != nil || got != one {
		t.Fatal("retirement blocked exact replay", err)
	}
}
