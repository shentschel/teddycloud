package evidence

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const payload = "0123456789abcdefghjkmnpqrs"

func observation(t *testing.T, first, value string, review Review) Observation {
	t.Helper()
	id, err := ParseID("obs_" + first + payload[1:])
	if err != nil {
		t.Fatal(err)
	}
	source, _ := NewSource("fixture", "r1", "record")
	o, err := NewObservation(id, source, Claim{"internal-subject", "classification", value}, time.Unix(1, 0), Tentative, review)
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func TestAcceptedFactPreservesDissent(t *testing.T) {
	a, b := observation(t, "0", "original", Accepted), observation(t, "1", "custom", Pending)
	fact, err := Accept(a.Claim(), []Observation{b, a, a})
	if err != nil || len(fact.Observations()) != 2 || len(fact.Support()) != 1 {
		t.Fatal("support/dissent lost")
	}
	altered := fact.Observations()
	altered[0] = Observation{}
	if fact.Observations()[0].ID().IsZero() {
		t.Fatal("observation alias")
	}
	supports := fact.Support()
	supports[0] = ID{}
	if fact.Support()[0].IsZero() {
		t.Fatal("support alias")
	}
	c := observation(t, "2", "custom", Accepted)
	if _, err := Accept(a.Claim(), []Observation{a, c}); err != ErrConflict {
		t.Fatal("conflicting accepted fact chosen")
	}
	if _, err := Accept(b.Claim(), []Observation{b}); err != ErrUnsupported {
		t.Fatal("unreviewed evidence accepted")
	}
	if _, err := Canonical([]Observation{a, observation(t, "0", "changed", Accepted)}); err != ErrConflict {
		t.Fatal("identity conflict lost")
	}
	x, _ := Canonical([]Observation{a, b})
	y, _ := Canonical([]Observation{b, a})
	if !reflect.DeepEqual(x, y) {
		t.Fatal("non-deterministic observations")
	}
	if _, err := Canonical(make([]Observation, MaxObservations+1)); err != ErrLimit {
		t.Fatal("limit ignored")
	}
	if _, err := Canonical([]Observation{{}}); err != ErrInvalid {
		t.Fatal("zero observation accepted")
	}
	for _, text := range []string{"", "line\nbreak", strings.Repeat("x", 129), string([]byte{255})} {
		if _, err := NewSource(text, "r1", "record"); err != ErrInvalid {
			t.Fatal("invalid source accepted")
		}
	}
}
func FuzzEvidenceText(f *testing.F) {
	f.Add("source")
	f.Add("\x00")
	f.Fuzz(func(t *testing.T, text string) {
		source, err := NewSource(text, "r1", "record")
		if err == nil && (source.Name() != text || !ValidText(text, 128)) {
			t.Fatal("source rewritten")
		}
	})
}
