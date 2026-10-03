package tag

import (
	"errors"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"math"
	"testing"
	"time"
)

func metadataObservation(t *testing.T, value Tag, n int, key FactKey, text string, review evidence.Review) evidence.Observation {
	t.Helper()
	source, _ := evidence.NewSource("source", "r1", "record")
	return mustObservation(t, n, value.ID(), key.String(), text, source, evidence.Corroborated, review, time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC))
}
func applyMetadata(t *testing.T, value Tag, c MetadataChange) Tag {
	t.Helper()
	next, changed, err := value.ApplyMetadata(c)
	if err != nil || !changed {
		t.Fatalf("apply: changed=%t, %v", changed, err)
	}
	return next
}
func TestTagMetadataTransitions(t *testing.T) {
	for key := FactKey(0); key < 4; key++ {
		t.Run(key.String(), func(t *testing.T) {
			initial, _ := NewTag(mustTagIDNumber(t, 1), UID{})
			value := initial
			for n, review := range []evidence.Review{evidence.Pending, evidence.Disputed, evidence.Rejected} {
				value = applyMetadata(t, value, MetadataChange{ExpectedRevision: value.Revision(), Key: key, Observations: []evidence.Observation{metadataObservation(t, value, n, key, "true", review)}})
				if value.Metadata() != initial.Metadata() {
					t.Fatal("unaccepted evidence changed a fact")
				}
			}
			yes := metadataObservation(t, value, 10, key, "true", evidence.Accepted)
			no := metadataObservation(t, value, 11, key, "false", evidence.Accepted)
			c := MetadataChange{ExpectedRevision: value.Revision(), Key: key, Observations: []evidence.Observation{yes, no}}
			value = applyMetadata(t, value, c)
			if value.retained.view.facts[key].state != MetadataConflict {
				t.Fatal("contrary evidence did not conflict")
			}
			for other := FactKey(0); other < 4; other++ {
				if other != key && value.retained.view.facts[other].state != MetadataUnknown {
					t.Fatal("cross-key inference")
				}
			}
			did := mustDecisionIDNumber(t, 1)
			resolution := MetadataChange{ExpectedRevision: value.Revision(), Key: key, Resolution: &Resolution{ID: did, Support: []evidence.ID{yes.ID()}}}
			value = applyMetadata(t, value, resolution)
			if value.retained.view.facts[key].state != MetadataObservedTrue || len(value.retained.view.observations) != 5 {
				t.Fatal("resolution erased dissent or chose wrong support")
			}
			replay, changed, err := value.ApplyMetadata(c)
			if err != nil || changed || !replay.Equal(value) {
				t.Fatal("observation replay advanced state", err)
			}
			replay, changed, err = value.ApplyMetadata(resolution)
			if err != nil || changed || !replay.Equal(value) {
				t.Fatal("decision replay advanced state", err)
			}
			value = applyMetadata(t, value, MetadataChange{ExpectedRevision: value.Revision(), Key: key, Observations: []evidence.Observation{metadataObservation(t, value, 12, key, "false", evidence.Accepted)}})
			if value.retained.view.facts[key].state != MetadataConflict {
				t.Fatal("later dissent ignored")
			}
			value = applyMetadata(t, value, MetadataChange{ExpectedRevision: value.Revision(), Key: key, Resolution: &Resolution{ID: mustDecisionIDNumber(t, 2), Support: []evidence.ID{no.ID()}}})
			if value.retained.view.facts[key].state != MetadataObservedFalse || len(value.retained.view.decisions) != 2 || value.ID() != initial.ID() || value.UID() != initial.UID() {
				t.Fatal("supersession changed identity/history")
			}
		})
	}
}

func TestTagMetadataReplayAndRejections(t *testing.T) {
	initial, _ := NewTag(mustTagIDNumber(t, 1), UID{})
	yes := metadataObservation(t, initial, 1, CloudAuth, "true", evidence.Accepted)
	other := metadataObservation(t, initial, 2, CloudAuth, "false", evidence.Pending)
	first := MetadataChange{ExpectedRevision: 1, Key: CloudAuth, Observations: []evidence.Observation{yes, other}}
	value := applyMetadata(t, initial, first)
	tests := []struct {
		name string
		c    MetadataChange
		want error
	}{
		{"partial observation", MetadataChange{ExpectedRevision: 1, Key: CloudAuth, Observations: []evidence.Observation{yes}}, ErrEvidenceConflict},
		{"mixed replay and new", MetadataChange{ExpectedRevision: 1, Key: CloudAuth, Observations: []evidence.Observation{yes, metadataObservation(t, value, 3, CloudAuth, "true", evidence.Accepted)}}, ErrEvidenceConflict},
		{"changed observation", MetadataChange{ExpectedRevision: 1, Key: CloudAuth, Observations: []evidence.Observation{metadataObservation(t, value, 1, CloudAuth, "false", evidence.Accepted), other}}, ErrEvidenceConflict},
		{"stale", MetadataChange{ExpectedRevision: 1, Key: Owned, Observations: []evidence.Observation{metadataObservation(t, value, 3, Owned, "true", evidence.Accepted)}}, ErrRevisionConflict},
		{"pending support", MetadataChange{ExpectedRevision: 2, Key: CloudAuth, Resolution: &Resolution{ID: mustDecisionIDNumber(t, 1), Support: []evidence.ID{other.ID()}}}, ErrEvidenceConflict},
		{"unsupported", MetadataChange{ExpectedRevision: 2, Key: CloudAuth, Resolution: &Resolution{ID: mustDecisionIDNumber(t, 1), Support: []evidence.ID{mustObservationIDNumber(t, 99)}}}, ErrEvidenceConflict},
		{"cross key", MetadataChange{ExpectedRevision: 2, Key: Owned, Resolution: &Resolution{ID: mustDecisionIDNumber(t, 1), Support: []evidence.ID{yes.ID()}}}, ErrEvidenceConflict},
		{"cross tag", MetadataChange{ExpectedRevision: 2, Key: CloudAuth, Observations: []evidence.Observation{metadataObservation(t, func() Tag { x, _ := NewTag(mustTagIDNumber(t, 2), UID{}); return x }(), 5, CloudAuth, "true", evidence.Accepted)}}, ErrInvalidRetainedEncoding},
		{"overflow", MetadataChange{ExpectedRevision: Revision(math.MaxInt64), Key: CloudAuth, Observations: []evidence.Observation{yes}}, ErrInvalidMetadata},
		{"65 observations", MetadataChange{ExpectedRevision: 2, Key: CloudAuth, Observations: make([]evidence.Observation, 65)}, ErrHistoryLimit},
		{"4097 support", MetadataChange{ExpectedRevision: 2, Key: CloudAuth, Resolution: &Resolution{ID: mustDecisionIDNumber(t, 1), Support: make([]evidence.ID, 4097)}}, ErrHistoryLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := value.ApplyMetadata(tc.c); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	// New resolution can include new evidence in the same atomic revision.
	no := metadataObservation(t, value, 6, CloudAuth, "false", evidence.Accepted)
	c := MetadataChange{ExpectedRevision: 2, Key: CloudAuth, Observations: []evidence.Observation{no}, Resolution: &Resolution{ID: mustDecisionIDNumber(t, 2), Support: []evidence.ID{no.ID()}}}
	value = applyMetadata(t, value, c)
	if _, changed, err := value.ApplyMetadata(c); err != nil || changed {
		t.Fatal("combined replay", err)
	}
	c.Observations = nil
	if _, _, err := value.ApplyMetadata(c); !errors.Is(err, ErrEvidenceConflict) {
		t.Fatal("partial decision replay accepted", err)
	}
	c.Observations = []evidence.Observation{no}
	c.Resolution.Support = []evidence.ID{yes.ID()}
	if _, _, err := value.ApplyMetadata(c); !errors.Is(err, ErrEvidenceConflict) {
		t.Fatal("changed decision replay accepted", err)
	}
}

func TestTagMetadataPreflightParityAndBoundaries(t *testing.T) {
	for _, view := range []retainedTagView{completeRetainedView(t), boundaryRetainedView(t, 485585, 1), boundaryRetainedView(t, 485586, 0)} {
		observations := make([]StoredObservation, len(view.observations))
		decisions := make([]StoredDecision, len(view.decisions))
		for i, o := range view.observations {
			observations[i] = StoredObservation{o.observation, o.introducedRevision}
		}
		for i, d := range view.decisions {
			decisions[i] = StoredDecision{d.id, d.key, d.expectedRevision, d.resultRevision, d.selected}
		}
		want, wantErr := encodedLen(view)
		size, err := NewHistorySize(view.id, view.uid, view.revision)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range observations {
			if err == nil {
				err = size.AddObservation(view.id, view.revision, o.Observation, o.IntroducedRevision)
			}
		}
		for _, d := range decisions {
			if err == nil {
				err = size.AddDecision(d.ID, d.Key, d.ExpectedRevision, d.ResultRevision, view.revision, len(d.Support))
			}
		}
		for _, f := range view.facts {
			if err == nil {
				err = size.AddActive(len(f.active))
			}
		}
		var got int64
		if err == nil {
			got, err = size.Finish()
		}
		if !errors.Is(err, wantErr) || (err == nil && got != want) {
			t.Fatalf("size parity got=%d/%v want=%d/%v", got, err, want, wantErr)
		}
		restored, err := RestoreMetadata(view.id, view.uid, view.revision, observations, decisions)
		if !errors.Is(err, wantErr) {
			t.Fatalf("restore boundary: %v want %v", err, wantErr)
		}
		if err == nil {
			if !restored.Equal(mustTagFromRetainedView(t, view)) {
				t.Fatal("restored semantic difference")
			}
		}
	}
}

func TestTagMetadataCommandQuotaAndRanking(t *testing.T) {
	initial, _ := NewTag(mustTagIDNumber(t, 1), UID{})
	observations := make([]evidence.Observation, 64)
	for i := range observations {
		observations[i] = metadataObservation(t, initial, i, Claimed, "true", evidence.Accepted)
	}
	value := applyMetadata(t, initial, MetadataChange{ExpectedRevision: 1, Key: Claimed, Observations: observations})
	if len(value.retained.view.observations) != 64 {
		t.Fatal("exact command limit rejected")
	}
	// Time and confidence cannot displace a contrary accepted value.
	source, _ := evidence.NewSource("source", "older", "record")
	no, err := evidence.NewObservation(mustObservationIDNumber(t, 100), source, evidence.Claim{Subject: value.ID().String(), Key: "claimed", Value: "false"}, time.Date(1990, 1, 1, 0, 0, 0, 1, time.UTC), evidence.ConfidenceUnknown, evidence.Accepted)
	if err != nil {
		t.Fatal(err)
	}
	value = applyMetadata(t, value, MetadataChange{ExpectedRevision: value.Revision(), Key: Claimed, Observations: []evidence.Observation{no}})
	if value.Metadata().Claimed() != MetadataConflict {
		t.Fatal("confidence/time selected a winner")
	}
	// Count gates precede validation of malformed members and any aggregate copy.
	tooMany := make([]evidence.Observation, 65)
	alloc := testing.AllocsPerRun(100, func() {
		_, _, err = value.ApplyMetadata(MetadataChange{ExpectedRevision: value.Revision(), Key: Owned, Observations: tooMany})
	})
	if !errors.Is(err, ErrHistoryLimit) || alloc != 0 {
		t.Fatalf("count preflight: %v, %.2f allocations", err, alloc)
	}
	for _, year := range []int{-1, 10000} {
		o, err := evidence.NewObservation(mustObservationIDNumber(t, 101), source, evidence.Claim{Subject: value.ID().String(), Key: "owned", Value: "true"}, time.Date(year, 1, 1, 0, 0, 0, 1, time.UTC), evidence.Tentative, evidence.Accepted)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := value.ApplyMetadata(MetadataChange{ExpectedRevision: value.Revision(), Key: Owned, Observations: []evidence.Observation{o}}); !errors.Is(err, ErrInvalidRetainedEncoding) {
			t.Fatal("out-of-range registry timestamp", err)
		}
	}
}

func TestTagMetadataStorageQuotaGates(t *testing.T) {
	size := HistorySize{}
	for i := 0; i < 4; i++ {
		if err := size.AddDecision(mustDecisionIDNumber(t, i), ProtocolValid, 1, 2, 2, 4096); err != nil {
			t.Fatal(err)
		}
	}
	if err := size.AddDecision(mustDecisionIDNumber(t, 5), ProtocolValid, 1, 2, 2, 1); err != ErrHistoryLimit {
		t.Fatal("cumulative support overflow", err)
	}
	if err := (&HistorySize{}).AddDecision(mustDecisionIDNumber(t, 1), ProtocolValid, 1, 2, 2, 4097); err != ErrHistoryLimit {
		t.Fatal("decision support overflow", err)
	}
	size = HistorySize{}
	for i := 0; i < 4096; i++ {
		if err := size.AddDecision(mustDecisionIDNumber(t, i), ProtocolValid, 1, 2, 2, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := size.AddDecision(mustDecisionIDNumber(t, 4096), ProtocolValid, 1, 2, 2, 1); err != ErrHistoryLimit {
		t.Fatal("decision count overflow", err)
	}
}
