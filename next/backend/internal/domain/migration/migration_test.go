package migration

import (
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func TestSanitizedMigrationLosslessDeterministic(t *testing.T) {
	records := fixtures(t)
	before := make([]Record, len(records))
	for i, r := range records {
		before[i] = clone(r)
	}
	results, err := Run(records)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]Status{"normal-original": Accepted, "custom-card": Accepted, "duplicate-a": Ambiguous, "duplicate-b": Ambiguous,
		"missing-taf-source": ReviewRequired, "crossed-arrays": Ambiguous, "unequal-arrays": Rejected, "conflicting-observations": Ambiguous,
		"four-member-set": Accepted, "set-member-0": Accepted, "set-member-1": Accepted, "set-member-2": Accepted, "set-member-3": Accepted}
	if len(results) != len(records) || !reflect.DeepEqual(records, before) {
		t.Fatal("migration changed or lost input")
	}
	rawBySource := map[evidence.Source]Record{}
	for _, r := range records {
		rawBySource[r.Source] = r
	}
	for _, result := range results {
		name := result.Raw().Source.Record()
		want, ok := expected[name]
		if !ok || result.Status() != want {
			t.Fatalf("wrong outcome for fixture %s", name)
		}
		if !reflect.DeepEqual(result.Raw(), rawBySource[result.Raw().Source]) {
			t.Fatal("raw evidence lost")
		}
		if len(result.Issues()) == 0 {
			t.Fatal("missing reason")
		}
		for _, issue := range result.Issues() {
			if issue.Source != result.Raw().Source || issue.Reason == "" {
				t.Fatal("missing provenance")
			}
		}
		if result.Protected() != (name == "custom-card" || name == "duplicate-b") {
			t.Fatal("custom protection changed")
		}
		if result.Status() != Accepted {
			if _, ok := result.Version(); ok {
				t.Fatal("uncertain version published")
			}
		}
		if name == "four-member-set" {
			grouping, ok := result.Set()
			if !ok || len(grouping.Members()) != 4 {
				t.Fatal("set missing")
			}
			seen := map[identity.ContentID]bool{}
			for i, m := range grouping.Members() {
				if seen[m.ContentID] || m.Position != uint32(i) {
					t.Fatal("set identity/order lost")
				}
				seen[m.ContentID] = true
			}
		}
		if strings.HasPrefix(name, "set-member-") {
			c, ok := result.Content()
			if !ok {
				t.Fatal("member missing")
			}
			if _, has := c.Facts().ProductIdentifiers().Model(); has {
				t.Fatal("synthetic model invented")
			}
			if _, has := c.Facts().ProductIdentifiers().Article(); has {
				t.Fatal("set article copied into member")
			}
		}
		if name == "normal-original" {
			if _, ok := result.Set(); ok {
				t.Fatal("tracks became members")
			}
		}
	}
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 50; i++ {
		rng.Shuffle(len(records), func(i, j int) { records[i], records[j] = records[j], records[i] })
		got, err := Run(records)
		if err != nil || !reflect.DeepEqual(got, results) {
			t.Fatal("permutation changed report")
		}
	}
	// Returned records and input slices cannot mutate retained evidence.
	raw := results[0].Raw()
	raw.Pairs = append(raw.Pairs, Pair{"200", strings.Repeat("b", 40)})
	if reflect.DeepEqual(raw, results[0].Raw()) {
		t.Fatal("test did not alter raw copy")
	}
	records[0].Tracks[0] = "changed"
	if !reflect.DeepEqual(results[0].Raw(), rawBySource[results[0].Raw().Source]) {
		t.Fatal("input alias")
	}
}
func TestMigrationLimitsRejectionAndSourceEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Record)
		want   Status
	}{
		{"malformed physical", func(r *Record) { *r = tagFixture(t, *r, 1, "invalid") }, Rejected},
		{"unreviewed class", func(r *Record) { *r = tagFixture(t, *r, 1, "11:22:33:44:55:66:77:88"); r.Classification = Unclassified }, ReviewRequired},
		{"bad pair", func(r *Record) { r.Pairs[0].Hash = "bad" }, Rejected},
		{"multiple pairs", func(r *Record) { r.Pairs = append(r.Pairs, r.Pairs[0]) }, Ambiguous},
		{"markup retained", func(r *Record) { r.Title = "<b>uninterpreted</b>" }, Accepted},
		{"control text", func(r *Record) { r.Title = "bad\ntext" }, Rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t, 1, "record")
			tc.change(&r)
			got, err := Run([]Record{r})
			if err != nil || len(got) != 1 || got[0].Status() != tc.want || !reflect.DeepEqual(got[0].Raw(), r) {
				t.Fatal("wrong rejection/preservation")
			}
		})
	}
	r := fixture(t, 1, "record")
	if _, err := Run([]Record{r, r}); err != ErrSource {
		t.Fatal("source replay accepted")
	}
	if _, err := Run(make([]Record, MaxRecords+1)); err != ErrLimit {
		t.Fatal("batch limit")
	}
	for _, change := range []func(*Record){
		func(r *Record) { r.Title = strings.Repeat("x", evidence.MaxText+1) },
		func(r *Record) { r.Pairs = make([]Pair, MaxItems+1) },
		func(r *Record) { r.Tracks = make([]string, MaxItems+1) },
	} {
		r := fixture(t, 1, "record")
		change(&r)
		if _, err := Run([]Record{r}); !errors.Is(err, ErrLimit) {
			t.Fatal("record limit")
		}
	}
}

func TestSetRequiresAcceptedMembers(t *testing.T) {
	records := fixtures(t)
	for i := range records {
		if records[i].Source.Record() == "set-member-2" {
			records[i].Title = "invalid\nmember"
		}
	}
	results, err := Run(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Raw().Source.Record() != "four-member-set" {
			continue
		}
		if result.Status() != ReviewRequired {
			t.Fatalf("set with rejected member should require review, got %v", result.Status())
		}
		if _, ok := result.Set(); ok {
			t.Fatal("set with rejected member was published")
		}
		return
	}
	t.Fatal("set result missing")
}

func FuzzMigrationDeterminism(f *testing.F) {
	f.Add("11:22:33:44:55:66:77:88", "100", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	f.Add("invalid", "0", "bad")
	f.Fuzz(func(t *testing.T, uid, audio, hash string) {
		r := tagFixture(t, fixture(t, 1, "fuzz-record"), 1, uid)
		r.Pairs = []Pair{{audio, hash}}
		before := clone(r)
		a, ea := Run([]Record{r})
		b, eb := Run([]Record{r})
		if !errors.Is(ea, eb) || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(before, r) {
			t.Fatal("migration not pure/deterministic")
		}
		if ea == nil && !reflect.DeepEqual(a[0].Raw(), r) {
			t.Fatal("lossy migration")
		}
	})
}
