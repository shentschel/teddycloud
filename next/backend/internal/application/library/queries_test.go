package library

import (
	"encoding/base64"
	"math"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func TestLibraryPagePartitionInput(t *testing.T) {
	snapshot := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	id, _ := identity.ParseContentID("cnt_00000000000000000000000001")
	zero := identity.ContentID{}
	cases := []struct {
		name string
		q    Query
		p    Page
		want error
	}{
		{"default", Query{}, Page{Limit: 1}, nil},
		{"zero", Query{}, Page{}, ErrInvalidInput},
		{"129", Query{}, Page{Limit: 129}, ErrLimitExceeded},
		{"overflow", Query{}, Page{Limit: math.MaxUint64}, ErrLimitExceeded},
		{"cursor-only", Query{}, Page{Limit: 1, Cursor: "x"}, ErrInvalidInput},
		{"snapshot43", Query{}, Page{Limit: 1, ExpectedSnapshot: snapshot}, nil},
		{"snapshot44", Query{}, Page{Limit: 1, ExpectedSnapshot: snapshot + "x"}, ErrLimitExceeded},
		{"snapshot-bad", Query{}, Page{Limit: 1, ExpectedSnapshot: "x"}, ErrInvalidInput},
		{"noncanonical", Query{}, Page{Limit: 1, ExpectedSnapshot: strings.Repeat("A", 42) + "B"}, ErrInvalidInput},
		{"owner-zero", Query{OwnerContentID: &zero}, Page{Limit: 1}, ErrInvalidInput},
		{"owner", Query{OwnerContentID: &id}, Page{Limit: 1}, ErrUnsupported},
		{"current", Query{Versions: Current}, Page{Limit: 1}, ErrUnsupported},
		{"title-asc", Query{Sort: TitleAsc}, Page{Limit: 1}, ErrUnsupported},
		{"title-desc", Query{Sort: TitleDesc}, Page{Limit: 1}, ErrUnsupported},
		{"unknown-versions", Query{Versions: "mine"}, Page{Limit: 1}, ErrInvalidInput},
		{"unknown-sort", Query{Sort: "random"}, Page{Limit: 1}, ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := NormalizeQuery(tc.q, tc.p)
			if err != tc.want {
				t.Fatal(err, tc.want)
			}
			if err == nil && (q.Versions != All || q.Sort != IdentityAsc) {
				t.Fatal(q)
			}
		})
	}
	for _, tc := range []struct {
		s    string
		want error
	}{{"", ErrUnsupported}, {"   ", ErrUnsupported}, {"Ä %_\\", ErrUnsupported}, {strings.Repeat("a", 256), ErrUnsupported}, {strings.Repeat("a", 257), ErrLimitExceeded}, {"\xff", ErrInvalidInput}, {"a\n", ErrInvalidInput}, {"\x00", ErrInvalidInput}, {"\x7f", ErrInvalidInput}} {
		if _, err := NormalizeQuery(Query{Search: &tc.s}, Page{Limit: 1}); err != tc.want {
			t.Fatal(tc.s, err, tc.want)
		}
	}
	for _, n := range []int{256, 257} {
		_, err := NormalizeQuery(Query{}, Page{Limit: 1, Cursor: strings.Repeat("x", n), ExpectedSnapshot: snapshot})
		want := error(nil)
		if n == 257 {
			want = ErrLimitExceeded
		}
		if err != want {
			t.Fatal(n, err)
		}
	}
}
func TestLibraryProjectionCanonicalJSON(t *testing.T) {
	r, err := Account(Result{Snapshot: strings.Repeat("A", 43), Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CanonicalJSON(r)
	const golden = `{"Items":[],"Snapshot":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","Complete":true,"Next":"","Inspected":0,"EncodedBytes":128}`
	if err != nil || string(raw) != golden {
		t.Fatalf("%s (%v)", raw, err)
	}
	value := "<&é\u2028\u2029\n\"\\\\u2028"
	raw, _ = CanonicalJSON(Baseline{Title: value})
	want := "{\"Title\":\"<&é\u2028\u2029\\n\\\"\\\\\\\\u2028\",\"Model\":null,\"Article\":null}"
	if string(raw) != want {
		t.Fatalf("%q != %q", raw, want)
	}
}

func TestLibraryProjectionItemGolden(t *testing.T) {
	item := Item{ContentID: "cnt_00000000000000000000000001", VersionID: "ver_00000000000000000000000002", AudioID: 123, Hash: strings.Repeat("0", 40), ContentRevision: 1, Media: 1, Baseline: Baseline{Title: "<é>\"\\"}}
	const golden = `{"ContentID":"cnt_00000000000000000000000001","VersionID":"ver_00000000000000000000000002","AudioID":123,"Hash":"0000000000000000000000000000000000000000","ContentRevision":1,"Review":0,"ProtectedCustom":false,"Baseline":{"Title":"<é>\"\\","Model":null,"Article":null},"Title":{"Status":0,"Value":null},"Model":{"Status":0,"Value":null},"Article":{"Status":0,"Value":null},"Supersession":{"Status":0,"Revision":0,"Predecessor":null,"Successor":null,"Terminal":null,"IncomingCommand":null,"OutgoingCommand":null},"Media":1}`
	raw, err := CanonicalJSON(item)
	if err != nil || string(raw) != golden {
		t.Fatalf("%s %v", raw, err)
	}
	if err := ValidateItem(item); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Item){func(i *Item) { i.AudioID = 0 }, func(i *Item) { i.Title.Value = new(string) }, func(i *Item) { i.Supersession.IncomingCommand = new(string) }, func(i *Item) { i.Baseline.Title = strings.Repeat("x", 4097) }} {
		bad := item
		change(&bad)
		if err := ValidateItem(bad); err != ErrUnavailable {
			t.Fatal(bad, err)
		}
	}
}

func TestLibraryPagePartitionResponseGolden(t *testing.T) {
	item := Item{ContentID: "cnt_00000000000000000000000001", VersionID: "ver_00000000000000000000000002", AudioID: 123, Hash: strings.Repeat("0", 40), ContentRevision: 1, Media: 1, Baseline: Baseline{Title: "<é>\"\\"}}
	result, err := Account(Result{Items: []Item{item}, Snapshot: strings.Repeat("A", 43), Next: "ASAhIiMkJSYnKCkqKywtLi8wMTIzNDU2Nzg5Ojs8PT4_Y250XzAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAxdmVyXzAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAyQEFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaW1xdXl90S0hRIPYqeccTPVZPSkAvOoPTvmze-1a9dg_Uy3l0HQ", Inspected: 2})
	const golden = `{"Items":[{"ContentID":"cnt_00000000000000000000000001","VersionID":"ver_00000000000000000000000002","AudioID":123,"Hash":"0000000000000000000000000000000000000000","ContentRevision":1,"Review":0,"ProtectedCustom":false,"Baseline":{"Title":"<é>\"\\","Model":null,"Article":null},"Title":{"Status":0,"Value":null},"Model":{"Status":0,"Value":null},"Article":{"Status":0,"Value":null},"Supersession":{"Status":0,"Revision":0,"Predecessor":null,"Successor":null,"Terminal":null,"IncomingCommand":null,"OutgoingCommand":null},"Media":1}],"Snapshot":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","Complete":false,"Next":"ASAhIiMkJSYnKCkqKywtLi8wMTIzNDU2Nzg5Ojs8PT4_Y250XzAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAxdmVyXzAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAyQEFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaW1xdXl90S0hRIPYqeccTPVZPSkAvOoPTvmze-1a9dg_Uy3l0HQ","Inspected":2,"EncodedBytes":862}`
	raw, e := CanonicalJSON(result)
	if err != nil || e != nil || string(raw) != golden {
		t.Fatalf("%s %v %v", raw, err, e)
	}
}
