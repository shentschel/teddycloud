package assignment

import (
	"errors"
	"testing"
)

// These are prospective value-object arithmetic boundaries. They deliberately
// do not claim end-to-end reachability of A1's not-yet-defined canonical encoding.
func TestAssignmentProspectiveLimits(t *testing.T) {
	tests := []struct {
		name    string
		current EvidenceUsage
		delta   EvidenceDelta
		want    error
	}{
		{"empty", EvidenceUsage{}, EvidenceDelta{}, nil},
		{"observations exact with bytes headroom", EvidenceUsage{observations: MaxEvidenceObservations - 1, retainedBytes: 4095 * 1024}, EvidenceDelta{Observations: 1, RetainedBytes: 1024}, nil},
		{"observations plus one with bytes headroom", EvidenceUsage{observations: MaxEvidenceObservations, retainedBytes: 4096 * 1024}, EvidenceDelta{Observations: 1, RetainedBytes: 1024}, ErrObservationLimit},
		{"bytes exact with count headroom", EvidenceUsage{observations: 1, retainedBytes: MaxEvidenceRetainedBytes - 1024}, EvidenceDelta{Observations: 1, RetainedBytes: 1024}, nil},
		{"bytes plus one with count headroom", EvidenceUsage{observations: 1, retainedBytes: MaxEvidenceRetainedBytes - 1024}, EvidenceDelta{Observations: 1, RetainedBytes: 1025}, ErrRetainedByteLimit},
		{"both exact", EvidenceUsage{observations: MaxEvidenceObservations - 1, retainedBytes: MaxEvidenceRetainedBytes - 1024}, EvidenceDelta{Observations: 1, RetainedBytes: 1024}, nil},
		{"count exact bytes over", EvidenceUsage{observations: MaxEvidenceObservations - 1, retainedBytes: MaxEvidenceRetainedBytes}, EvidenceDelta{Observations: 1, RetainedBytes: 1}, ErrRetainedByteLimit},
		{"bytes exact count over", EvidenceUsage{observations: MaxEvidenceObservations, retainedBytes: MaxEvidenceRetainedBytes - 1}, EvidenceDelta{Observations: 1, RetainedBytes: 1}, ErrObservationLimit},
		{"events exact", EvidenceUsage{events: MaxEvidenceEvents - 1}, EvidenceDelta{Events: 1}, nil},
		{"events plus one", EvidenceUsage{events: MaxEvidenceEvents}, EvidenceDelta{Events: 1}, ErrEvidenceEventLimit},
		{"support exact", EvidenceUsage{supportLinks: MaxEvidenceSupportLinks - 1}, EvidenceDelta{SupportLinks: 1}, nil},
		{"support plus one", EvidenceUsage{supportLinks: MaxEvidenceSupportLinks}, EvidenceDelta{SupportLinks: 1}, ErrSupportLinkLimit},
		{"command observations exact", EvidenceUsage{}, EvidenceDelta{Observations: MaxNewObservations}, nil},
		{"command observations plus one", EvidenceUsage{}, EvidenceDelta{Observations: MaxNewObservations + 1}, ErrCommandEvidenceLimit},
		{"command support exact", EvidenceUsage{}, EvidenceDelta{SupportLinks: MaxSelectedSupport}, nil},
		{"command support plus one", EvidenceUsage{}, EvidenceDelta{SupportLinks: MaxSelectedSupport + 1}, ErrCommandEvidenceLimit},
		{"event overflow", EvidenceUsage{events: 1}, EvidenceDelta{Events: ^uint64(0)}, ErrEvidenceEventLimit},
		{"observation overflow", EvidenceUsage{observations: 1}, EvidenceDelta{Observations: ^uint64(0)}, ErrCommandEvidenceLimit},
		{"support overflow", EvidenceUsage{supportLinks: 1}, EvidenceDelta{SupportLinks: ^uint64(0)}, ErrCommandEvidenceLimit},
		{"byte overflow", EvidenceUsage{retainedBytes: 1}, EvidenceDelta{RetainedBytes: ^uint64(0)}, ErrRetainedByteLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.current
			got, err := before.Prospective(tc.delta)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.current != before {
				t.Fatal("input usage changed")
			}
			if err != nil {
				if got != before {
					t.Fatal("failed admission changed usage")
				}
				return
			}
			want := EvidenceUsage{before.events + tc.delta.Events, before.observations + tc.delta.Observations, before.supportLinks + tc.delta.SupportLinks, before.retainedBytes + tc.delta.RetainedBytes}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestAssignmentUsageRejectsOversizedHistoricState(t *testing.T) {
	for _, tc := range []struct {
		usage EvidenceUsage
		want  error
	}{
		{EvidenceUsage{events: MaxEvidenceEvents + 1}, ErrEvidenceEventLimit},
		{EvidenceUsage{observations: MaxEvidenceObservations + 1}, ErrObservationLimit},
		{EvidenceUsage{supportLinks: MaxEvidenceSupportLinks + 1}, ErrSupportLinkLimit},
		{EvidenceUsage{retainedBytes: MaxEvidenceRetainedBytes + 1}, ErrRetainedByteLimit},
	} {
		if _, err := NewEvidenceUsage(tc.usage.events, tc.usage.observations, tc.usage.supportLinks, tc.usage.retainedBytes); err != tc.want {
			t.Fatalf("historic usage: %v", err)
		}
		got, err := tc.usage.Prospective(EvidenceDelta{})
		if err != tc.want || got != tc.usage {
			t.Fatal("invalid current state accepted or changed")
		}
	}

	u, err := NewEvidenceUsage(MaxEvidenceEvents, MaxEvidenceObservations, MaxEvidenceSupportLinks, MaxEvidenceRetainedBytes)
	if err != nil || u.Events() != MaxEvidenceEvents || u.Observations() != MaxEvidenceObservations || u.SupportLinks() != MaxEvidenceSupportLinks || u.RetainedBytes() != MaxEvidenceRetainedBytes {
		t.Fatal("exact usage rejected")
	}
}
