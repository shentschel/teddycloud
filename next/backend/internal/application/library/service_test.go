package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type testOwner struct {
	auth      *ReadAuthority
	calls     int
	reader    Reader
	duplicate bool
	skip      bool
}

func (o *testOwner) AuthorizeLibrary(s ReadScope) error { return o.auth.Authorize(s) }
func (o *testOwner) WithinLibraryRead(c context.Context, s ReadScope, f func(context.Context, Reader) error) error {
	o.calls++
	if o.skip {
		return nil
	}
	if err := f(c, o.reader); err != nil {
		return err
	}
	if o.duplicate {
		return f(c, o.reader)
	}
	return nil
}

type testReader struct {
	result Result
	query  Query
	err    error
	hook   func()
}

func (r *testReader) ListLibrary(_ context.Context, q Query, _ Page) (Result, error) {
	r.query = q
	if r.hook != nil {
		r.hook()
	}
	return r.result, r.err
}
func (r *testReader) Prepare(context.Context) (Preparation, error) { return Preparation{}, r.err }
func (r *testReader) Advance(context.Context) (Preparation, error) { return Preparation{}, r.err }
func TestLibraryReadOnlyBoundsService(t *testing.T) {
	a := NewReadAuthority()
	scope := a.Issue()
	reader := &testReader{}
	reader.result, _ = Account(Result{Snapshot: strings.Repeat("A", 43), Complete: true})
	o := &testOwner{auth: a, reader: reader}
	s := NewService(o)
	for _, tc := range []struct {
		scope ReadScope
		p     Page
		want  error
	}{{ReadScope{}, Page{}, ErrUnauthorized}, {NewReadAuthority().Issue(), Page{}, ErrUnauthorized}, {scope, Page{}, ErrInvalidInput}, {scope, Page{Limit: 129}, ErrLimitExceeded}} {
		_, err := s.ListLibrary(t.Context(), Query{}, tc.p, tc.scope)
		if err != tc.want || o.calls != 0 {
			t.Fatal(err, o.calls)
		}
	}
	if _, err := s.ListLibrary(t.Context(), Query{}, Page{Limit: 1}, scope); err != nil || o.calls != 1 {
		t.Fatal(err, o.calls)
	}
	o.duplicate = true
	if r, err := s.ListLibrary(t.Context(), Query{}, Page{Limit: 1}, scope); err != ErrUnavailable || len(r.Items) != 0 {
		t.Fatal(r, err)
	}
	o.duplicate = false
	o.skip = true
	if _, err := s.ListLibrary(t.Context(), Query{}, Page{Limit: 1}, scope); err != ErrUnavailable {
		t.Fatal(err)
	}
	o.skip = false
	reader.hook = func() { a.Revoke() }
	if _, err := s.ListLibrary(t.Context(), Query{}, Page{Limit: 1}, scope); err != ErrUnauthorized {
		t.Fatal(err)
	}
	if BoundaryError(t.Context(), errors.New("SQL secret")) != ErrUnavailable {
		t.Fatal("raw boundary")
	}
}
func TestLibraryLifecycleFenceService(t *testing.T) {
	a := NewReadAuthority()
	scope := a.Issue()
	r := &testReader{}
	r.result, _ = Account(Result{Snapshot: strings.Repeat("A", 43), Complete: true})
	o := &testOwner{auth: a, reader: r}
	s := NewService(o)
	r.hook = func() {
		if _, err := s.Prepare(t.Context(), scope); err != ErrBusy {
			t.Error(err)
		}
	}
	if _, err := s.ListLibrary(t.Context(), Query{}, Page{Limit: 1}, scope); err != nil {
		t.Fatal(err)
	}
	c, cancel := context.WithCancel(t.Context())
	cancel()
	before := o.calls
	if _, err := s.ListLibrary(c, Query{}, Page{Limit: 1}, scope); !errors.Is(err, context.Canceled) || o.calls != before {
		t.Fatal(err)
	}
	c, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := s.Advance(c, scope); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	r.hook = nil
	r.err = errors.New("driver/path/token")
	for _, op := range []func() (Preparation, error){func() (Preparation, error) { return s.Prepare(t.Context(), scope) }, func() (Preparation, error) { return s.Advance(t.Context(), scope) }} {
		if p, err := op(); err != ErrUnavailable || p != (Preparation{}) {
			t.Fatal(p, err)
		}
	}
}

func TestLibraryTotalOrderService(t *testing.T) {
	a := NewReadAuthority()
	scope := a.Issue()
	reader := &testReader{}
	service := NewService(&testOwner{auth: a, reader: reader})
	titleA, titleZ := "A", "z"
	item := func(n string, title *string) Item {
		i := Item{ContentID: "cnt_00000000000000000000000001", VersionID: "ver_0000000000000000000000000" + n, AudioID: 123, Hash: strings.Repeat("0", 40), ContentRevision: 1, Media: 1}
		if title != nil {
			i.Review = 2
			i.Title = Field{Status: 1, Value: title}
		}
		return i
	}
	first, last := item("1", &titleZ), item("2", &titleA)
	missing := item("3", nil)
	set := func(items []Item) {
		reader.result, _ = Account(Result{Items: items, Inspected: uint64(len(items)), Snapshot: strings.Repeat("A", 43), Complete: true})
	}
	set([]Item{first, last, missing})
	if _, e := service.ListLibrary(t.Context(), Query{Sort: TitleDesc}, Page{Limit: 16}, scope); e != nil {
		t.Fatal(e)
	}
	set([]Item{last, first, missing})
	if _, e := service.ListLibrary(t.Context(), Query{Sort: TitleDesc}, Page{Limit: 16}, scope); e != ErrUnavailable {
		t.Fatal(e)
	}
	set([]Item{missing, first})
	if _, e := service.ListLibrary(t.Context(), Query{Sort: TitleDesc}, Page{Limit: 16}, scope); e != ErrUnavailable {
		t.Fatal(e)
	}
	set([]Item{first, first})
	if _, e := service.ListLibrary(t.Context(), Query{Sort: TitleDesc}, Page{Limit: 16}, scope); e != ErrUnavailable {
		t.Fatal(e)
	}
	set([]Item{first})
	search := "  Z  "
	if _, e := service.ListLibrary(t.Context(), Query{Sort: TitleAsc, Search: &search}, Page{Limit: 16}, scope); e != nil || reader.query.Search == nil || *reader.query.Search != "z" {
		t.Fatal(e, reader.query)
	}
	search = "a"
	if _, e := service.ListLibrary(t.Context(), Query{Sort: TitleAsc, Search: &search}, Page{Limit: 16}, scope); e != ErrUnavailable {
		t.Fatal(e)
	}
	if _, e := service.ListLibrary(t.Context(), Query{Versions: Current, Sort: TitleAsc}, Page{Limit: 16}, scope); e != ErrUnavailable {
		t.Fatal(e)
	}
}

func TestLibraryNoNPlusOneService(t *testing.T) {
	for _, limit := range []uint64{1, 16, 128} {
		a := NewReadAuthority()
		reader := &testReader{}
		owner := &testOwner{auth: a, reader: reader}
		s := NewService(owner)
		items := make([]Item, limit)
		for i := range items {
			items[i] = Item{ContentID: "cnt_00000000000000000000000001", VersionID: fmt.Sprintf("ver_%026d", i+1), AudioID: 123, Hash: strings.Repeat("1", 40), ContentRevision: 1, Media: 1}
		}
		reader.result, _ = Account(Result{Items: items, Inspected: limit, Snapshot: strings.Repeat("A", 43), Complete: true})
		reads := 0
		reader.hook = func() { reads++ }
		for i := 0; i < 35; i++ {
			if _, e := s.ListLibrary(t.Context(), Query{}, Page{Limit: limit}, a.Issue()); e != nil {
				t.Fatal(e)
			}
		}
		if owner.calls != 35 || reads != 35 {
			t.Fatal("service work grows with page size", limit, owner.calls, reads)
		}
	}
}
