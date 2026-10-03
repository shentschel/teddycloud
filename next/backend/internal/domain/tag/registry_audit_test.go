package tag

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

// Fixed-width input bounds work independently of the fuzz engine's input size.
func FuzzAuditStateCommands(f *testing.F) {
	for _, seed := range []uint64{0, math.MaxUint64, 0x0123456789abcdef} {
		f.Add(seed)
	}
	f.Fuzz(auditStateCommands)
}

func TestTagAuditGeneratedStateCommands(t *testing.T) {
	for seed := uint64(0); seed < 32; seed++ {
		auditStateCommands(t, seed*0x0102040810204081)
	}
}

func auditStateCommands(t *testing.T, seed uint64) {
	t.Helper()
	initial, err := NewTag(mustTagIDNumber(t, 1), UIDFromBytes([8]byte{byte(seed), 0xab, 0xcd, 0xef}))
	if err != nil {
		t.Fatal(err)
	}
	current := initial
	var seen [4][2]bool
	var commands []MetadataChange
	for step := 0; step < 16; step++ {
		bits := byte(seed >> (uint(step%8) * 8))
		key := FactKey(bits % 4)
		truth := int((bits >> 2) & 1)
		review := evidence.Review((bits >> 3) % 4)
		text := []string{"false", "true"}[truth]
		source, err := evidence.NewSource("audit", "opaque", "synthetic")
		if err != nil {
			t.Fatal(err)
		}
		o, err := evidence.NewObservation(mustObservationIDNumber(t, step), source,
			evidence.Claim{Subject: current.ID().String(), Key: key.String(), Value: text},
			time.Date(2000+int(bits), 1, 1, 0, 0, 0, 1, time.UTC), evidence.Confidence(bits%3), review)
		if err != nil {
			t.Fatal(err)
		}
		c := MetadataChange{ExpectedRevision: current.Revision(), Key: key, Observations: []evidence.Observation{o}}
		before := current
		current = applyMetadata(t, current, c)
		if current.ID() != initial.ID() || current.UID() != initial.UID() || current.RUID() != initial.RUID() || current.Revision() != before.Revision()+1 {
			t.Fatal("identity or revision changed incorrectly")
		}
		if review == evidence.Accepted {
			seen[key][truth] = true
		}
		for k := FactKey(0); k < 4; k++ {
			want := MetadataUnknown
			switch {
			case seen[k][0] && seen[k][1]:
				want = MetadataConflict
			case seen[k][0]:
				want = MetadataObservedFalse
			case seen[k][1]:
				want = MetadataObservedTrue
			}
			if current.retained.view.facts[k].state != want {
				t.Fatalf("step %d key %s inferred or lost evidence", step, k)
			}
		}
		stale := MetadataChange{ExpectedRevision: before.Revision(), Key: key, Observations: []evidence.Observation{metadataObservation(t, current, 100+step, key, "true", evidence.Accepted)}}
		if got, changed, err := current.ApplyMetadata(stale); !errors.Is(err, ErrRevisionConflict) || changed || !got.ID().IsZero() {
			t.Fatal("stale command admitted", err)
		}
		commands = append(commands, c)
		for _, replay := range commands {
			got, changed, err := current.ApplyMetadata(replay)
			if err != nil || changed || !got.Equal(current) {
				t.Fatal("historical replay changed state", err)
			}
		}
		// Reordered storage rows retain exact revisions and canonical equality.
		var stored []StoredObservation
		for _, row := range current.retained.view.observations {
			stored = append([]StoredObservation{{row.observation, row.introducedRevision}}, stored...)
		}
		restored, err := RestoreMetadata(current.ID(), current.UID(), current.Revision(), stored, nil)
		if err != nil || !restored.Equal(current) {
			t.Fatal("storage permutation changed aggregate", err)
		}
		var a, b bytes.Buffer
		_, ea := current.retained.writeTo(&a)
		_, eb := restored.retained.writeTo(&b)
		if ea != nil || eb != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Fatal("canonical encoding differs")
		}
	}
	if initial.Revision() != 1 || initial.Metadata() != (Metadata{}) {
		t.Fatal("previous immutable value mutated")
	}
}

func FuzzAuditEvidencePermutation(f *testing.F) {
	for _, seed := range []uint64{0, math.MaxUint64, 0x123456789abcdef} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		initial, err := NewTag(mustTagIDNumber(t, 1), UID{})
		if err != nil {
			t.Fatal(err)
		}
		key := FactKey(seed % 4)
		observations := make([]evidence.Observation, 4)
		for i := range observations {
			source, err := evidence.NewSource("audit", "unranked", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			observations[i], err = evidence.NewObservation(mustObservationIDNumber(t, i), source,
				evidence.Claim{Subject: initial.ID().String(), Key: key.String(), Value: []string{"true", "false"}[i%2]},
				time.Date(1900+int(byte(seed>>uint(i*8))), 1, 1, 0, 0, 0, 1, time.UTC), evidence.Confidence((seed>>uint(i*8))%3), evidence.Accepted)
			if err != nil {
				t.Fatal(err)
			}
		}
		baseline := applyMetadata(t, initial, MetadataChange{ExpectedRevision: 1, Key: key, Observations: observations})
		// All 24 permutations, both one atomic batch and separate arrivals.
		for a := 0; a < 4; a++ {
			for b := 0; b < 4; b++ {
				if b == a {
					continue
				}
				for c := 0; c < 4; c++ {
					if c == a || c == b {
						continue
					}
					d := 6 - a - b - c
					order := []evidence.Observation{observations[a], observations[b], observations[c], observations[d]}
					batch := applyMetadata(t, initial, MetadataChange{ExpectedRevision: 1, Key: key, Observations: order})
					if !batch.Equal(baseline) {
						t.Fatal("batch order affected canonical equality")
					}
					sequential := initial
					for _, o := range order {
						sequential = applyMetadata(t, sequential, MetadataChange{ExpectedRevision: sequential.Revision(), Key: key, Observations: []evidence.Observation{o}})
					}
					if sequential.Metadata() != baseline.Metadata() || sequential.retained.view.facts[key].state != MetadataConflict {
						t.Fatal("arrival order selected a winner")
					}
					if len(sequential.retained.view.decisions) != 0 || len(sequential.retained.view.observations) != 4 {
						t.Fatal("implicit resolution or lost dissent")
					}
				}
			}
		}
	})
}

func TestTagAuditRevisionAndCommandBounds(t *testing.T) {
	initial, err := NewTag(mustTagIDNumber(t, 1), UID{})
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []Revision{math.MinInt64, -1, 0, math.MaxInt64} {
		c := MetadataChange{ExpectedRevision: revision, Key: Owned, Observations: []evidence.Observation{metadataObservation(t, initial, 1, Owned, "true", evidence.Accepted)}}
		got, changed, err := initial.ApplyMetadata(c)
		if err == nil || changed || !got.ID().IsZero() {
			t.Fatal("invalid/overflow revision admitted")
		}
		if _, err := RestoreMetadata(initial.ID(), initial.UID(), revision, nil, nil); err == nil {
			t.Fatal("invalid history revision admitted")
		}
	}
	for _, size := range []int{0, MaxNewObservationsPerCommand, MaxNewObservationsPerCommand + 1} {
		observations := make([]evidence.Observation, size)
		for i := range observations {
			observations[i] = metadataObservation(t, initial, i, Owned, "true", evidence.Accepted)
		}
		got, changed, err := initial.ApplyMetadata(MetadataChange{ExpectedRevision: 1, Key: Owned, Observations: observations})
		if size == MaxNewObservationsPerCommand {
			if err != nil || !changed || got.Revision() != 2 {
				t.Fatal("exact bound", err)
			}
		} else if err == nil || changed || !got.ID().IsZero() {
			t.Fatal("empty/oversize command admitted")
		}
	}
}
