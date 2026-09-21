package assignment

import (
	"reflect"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func TestAuditHistoryRejectsEveryConflictingRevisionField(t *testing.T) {
	base := revision(t, 1, 10, 20)
	otherID, _ := ParseID("asn_1123456789abcdefghjkmnpqrs")
	otherTag, _ := identity.ParseTagID("tag_1123456789abcdefghjkmnpqrs")
	otherVersion, _ := identity.ParseContentVersionID("ver_1123456789abcdefghjkmnpqrs")
	for _, change := range []func(*Assignment){
		func(a *Assignment) { a.id = otherID },
		func(a *Assignment) { a.tag = otherTag },
		func(a *Assignment) { a.version = otherVersion },
		func(a *Assignment) { a.interval = revision(t, 1, 11, 20).interval },
		func(a *Assignment) { a.interval = revision(t, 1, 10, 21).interval },
	} {
		changed := base
		change(&changed)
		for _, entries := range [][]Assignment{{base, changed}, {changed, base}} {
			if _, err := NewHistory(entries); err != ErrRevisionConflict {
				t.Fatal("conflicting replay accepted")
			}
		}
	}
	for _, change := range []func(*Assignment){
		func(a *Assignment) { a.id = otherID },
		func(a *Assignment) { a.tag = otherTag },
	} {
		next := revision(t, 2, 20, 30)
		change(&next)
		if _, err := NewHistory([]Assignment{next, base}); err != ErrRevisionConflict {
			t.Fatal("history joined different aggregates/tags")
		}
	}
}

func TestAuditHistoryCanonicalTimeAndNanosecondBoundaries(t *testing.T) {
	from := time.Unix(100, 0)
	end := from.Add(time.Second)
	utc, err := NewInterval(from, end)
	if err != nil {
		t.Fatal(err)
	}
	offset := time.FixedZone("fixture-offset", 3600)
	zoned, err := NewInterval(from.In(offset), end.In(offset))
	if err != nil || zoned != utc {
		t.Fatal("equal instants differ by timezone representation")
	}
	a := revision(t, 1, 100, 101)
	b := revision(t, 3, 102, 0)
	input := []Assignment{b, a}
	history, err := NewHistory(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = Assignment{}
	copy := history.Entries()
	copy[0] = Assignment{}
	if !reflect.DeepEqual(history.Entries(), []Assignment{a, b}) {
		t.Fatal("history aliases caller slices")
	}
	for _, tc := range []struct {
		at   time.Time
		want uint64
	}{{from.Add(-time.Nanosecond), 0}, {from, 1}, {end.Add(-time.Nanosecond), 1}, {end, 0}, {time.Unix(102, 0).Add(-time.Nanosecond), 0}, {time.Unix(102, 0), 3}} {
		got, ok := history.CurrentAt(tc.at)
		if ok != (tc.want != 0) || got.Revision() != tc.want {
			t.Fatal("half-open interval, gap, or future revision rule violated")
		}
	}
	overlap := b
	overlap.interval, _ = NewInterval(end.Add(-time.Nanosecond), time.Time{})
	if _, err := NewHistory([]Assignment{a, overlap}); err != ErrOverlap {
		t.Fatal("one-nanosecond overlap accepted")
	}
}
