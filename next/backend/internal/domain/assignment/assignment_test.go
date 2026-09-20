package assignment

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const payload = "0123456789abcdefghjkmnpqrs"

func revision(t *testing.T, rev uint64, from, until int64) Assignment {
	t.Helper()
	id, _ := ParseID("asn_" + payload)
	tag, _ := identity.ParseTagID("tag_" + payload)
	version, _ := identity.ParseContentVersionID("ver_" + payload)
	end := time.Time{}
	if until != 0 {
		end = time.Unix(until, 0)
	}
	interval, err := NewInterval(time.Unix(from, 0), end)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(id, rev, tag, version, interval)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestHistoryRules(t *testing.T) {
	a, b, c := revision(t, 1, 10, 20), revision(t, 2, 20, 30), revision(t, 3, 40, 0)
	h, err := NewHistory([]Assignment{c, a, b, a})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Entries()) != 3 {
		t.Fatal("replay was not idempotent")
	}
	for _, tc := range []struct {
		at   int64
		want uint64
	}{{9, 0}, {10, 1}, {19, 1}, {20, 2}, {30, 0}, {39, 0}, {40, 3}, {100, 3}} {
		got, ok := h.CurrentAt(time.Unix(tc.at, 0))
		if ok != (tc.want != 0) || got.Revision() != tc.want {
			t.Fatal("effective time selection failed")
		}
	}
	copy := h.Entries()
	copy[0] = Assignment{}
	if h.Entries()[0].Revision() != 1 {
		t.Fatal("history alias")
	}
	for _, tc := range []struct {
		entries []Assignment
		err     error
	}{
		{[]Assignment{a, revision(t, 2, 19, 30)}, ErrOverlap},
		{[]Assignment{revision(t, 1, 10, 0), b}, ErrOverlap},
		{[]Assignment{a, revision(t, 1, 20, 30)}, ErrRevisionConflict},
		{[]Assignment{b, revision(t, 3, 10, 20)}, ErrRevisionConflict},
		{[]Assignment{{}}, ErrInvalidID},
		{make([]Assignment, MaxRevisions+1), ErrLimit},
	} {
		if _, err := NewHistory(tc.entries); !errors.Is(err, tc.err) {
			t.Fatalf("unexpected validation: %v", err)
		}
	}
	other := b
	other.tag, _ = identity.ParseTagID("tag_1123456789abcdefghjkmnpqrs")
	if _, err := NewHistory([]Assignment{a, other}); err != ErrRevisionConflict {
		t.Fatal("mixed tags accepted")
	}
	for _, tc := range [][2]time.Time{{{}, {}}, {time.Unix(1, 0), time.Unix(1, 0)}, {time.Unix(2, 0), time.Unix(1, 0)}} {
		if _, err := NewInterval(tc[0], tc[1]); err != ErrInvalidInterval {
			t.Fatal("bad interval accepted")
		}
	}
}
func FuzzIntervalBoundary(f *testing.F) {
	f.Add(uint32(10), uint32(5), uint32(2))
	f.Fuzz(func(t *testing.T, start, width, gap uint32) {
		from := int64(start) + 1
		end := from + int64(width) + 1
		next := end + int64(gap)
		a, b := revision(t, 1, from, end), revision(t, 2, next, 0)
		x, err := NewHistory([]Assignment{a, b})
		if err != nil {
			t.Fatal(err)
		}
		y, err := NewHistory([]Assignment{b, a})
		if err != nil || !reflect.DeepEqual(x, y) {
			t.Fatal("non-deterministic history")
		}
		if _, ok := x.CurrentAt(time.Unix(from-1, 0)); ok {
			t.Fatal("premature assignment")
		}
		if got, ok := x.CurrentAt(time.Unix(next, 0)); !ok || got.Revision() != 2 {
			t.Fatal("boundary mismatch")
		}
	})
}
