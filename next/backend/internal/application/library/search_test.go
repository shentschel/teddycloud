package library

import (
	"strings"
	"testing"
)

func TestLibrarySearchNormalization(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"  WOOZLE  Test ", "woozle test"}, {"Ä", "Ä"}, {"ä", "ä"}, {"é", "é"}, {"e\u0301", "e\u0301"},
		{"%_\\", "%_\\"}, {"", ""}, {"    ", ""}, {" A  B  ", "a b"}, {"\u00a0A\u2003 B", "\u00a0a\u2003 b"},
		{strings.Repeat("A", 256), strings.Repeat("a", 256)}, {strings.Repeat("é", 128), strings.Repeat("é", 128)},
	} {
		got, err := NormalizeSearch(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %q %v", tc.raw, got, err)
		}
		again, e := NormalizeSearch(got)
		if e != nil || again != got {
			t.Fatal("not idempotent")
		}
		q, e := NormalizeQuery(Query{Search: &tc.raw, Sort: TitleAsc}, Page{Limit: 1})
		if e != nil {
			t.Fatal(e)
		}
		if (q.Search == nil) != (got == "") || q.Search != nil && *q.Search != got {
			t.Fatal(q)
		}
	}
	for _, s := range []string{strings.Repeat("a", 257), strings.Repeat("é", 128) + "a", strings.Repeat(" ", 257)} {
		if _, e := NormalizeSearch(s); e != ErrLimitExceeded {
			t.Fatal(e)
		}
	}
	for i := 0; i <= 127; i++ {
		if i >= 32 && i != 127 {
			continue
		}
		s := "a" + string(rune(i)) + "b"
		if _, e := NormalizeSearch(s); e != ErrInvalidInput {
			t.Fatal(i, e)
		}
	}
	for _, s := range []string{"\xff", "\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80", "a\xe2\x82"} {
		if _, e := NormalizeSearch(s); e != ErrInvalidInput {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		s, want string
		ok      bool
	}{{"abc", "abd", true}, {"a\xff", "b", true}, {"\xfe\xff", "\xff", true}, {"\xff\xff", "", false}, {"", "", false}, {"é", "\xc3\xaa", true}} {
		got, ok := PrefixSuccessor(tc.s)
		if got != tc.want || ok != tc.ok {
			t.Fatal(tc, got, ok)
		}
	}
}
