package assignment

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
)

type historyMemory struct{ *memory }

func (historyMemory) AssignmentHistoryScope() (uint64, uint64) { return 1, 0 }

func TestAssignmentHistoryKeyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		n     int
		fault error
	}{
		{"partial read with error", 1, errors.New("entropy unavailable")},
		{"full read with error", 32, errors.New("entropy unavailable")},
		{"short read without error", 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			key := newHistoryKey(func(dst []byte) (int, error) {
				calls++
				for i := range dst[:tc.n] {
					dst[i] = 0xa5
				}
				return tc.n, tc.fault
			})
			for range 2 {
				got, err := key()
				if got != ([32]byte{}) || err != ErrUnavailable {
					t.Fatalf("failed key initialization returned key material or wrong error: %v", err)
				}
			}
			if calls != 1 {
				t.Fatalf("failed entropy source invoked %d times, want 1", calls)
			}
		})
	}
}

func TestAssignmentHistoryPageLimits(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		byteLimited  bool
	}{
		{name: "entry ceiling", reason: "reviewed"},
		{name: "encoded response including continuation", reason: strings.Repeat("<", 1024), byteLimited: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, m := fixture(t)
			s := New(historyMemory{m})
			first := command(t, 1, Assign, 0)
			a, err := empty(first.AssignmentID, first.TagID)
			if err != nil {
				t.Fatal(err)
			}
			auth, err := d.BindAuthenticatedReviewAuthority(m.actor.Reference)
			if err != nil {
				t.Fatal(err)
			}
			// Build with the same append and admission as Execute, then let
			// the service reconstruct independently on every requested page.
			for n := 1; n <= MaxHistoryEntries+1; n++ {
				in := command(t, n, Assign, uint64(n-1))
				in.Reason = test.reason
				a, _, err = a.append(in, m.actor.Reference, auth, time.Unix(1000, 0))
				if err != nil {
					t.Fatalf("append %d: %v", n, err)
				}
			}
			installSnapshot(m, a.snapshot)
			q := HistoryQuery{EffectiveQuery: EffectiveQuery{TagID: first.TagID, At: first.EffectiveAt}, Limit: MaxHistoryEntries}
			next := 0
			for {
				page, err := s.ListAssignmentHistory(t.Context(), q)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(page)
				if err != nil || len(raw) > MaxHistoryBytes || len(page.Events) > q.Limit || len(page.Events) == 0 || len(page.Next) > MaxHistoryCursor {
					t.Fatalf("page limits: bytes %d, entries %d, cursor %d, error %v", len(raw), len(page.Events), len(page.Next), err)
				}
				if next == 0 {
					if page.Next == "" || (len(page.Events) < MaxHistoryEntries) != test.byteLimited {
						t.Fatalf("wrong binding limit: %d entries, %d bytes", len(page.Events), len(raw))
					}
					if test.byteLimited {
						oneMore, err := json.Marshal(HistoryEntry{a.snapshot.Events[len(page.Events)]})
						if err != nil || len(raw)+len(oneMore)+1 <= MaxHistoryBytes {
							t.Fatalf("byte-limited page stopped early: %d + %d", len(raw), len(oneMore))
						}
					}
				}
				for _, event := range page.Events {
					if !reflect.DeepEqual(event.Event, a.snapshot.Events[next]) {
						t.Fatalf("retained event/receipt changed at index %d", next)
					}
					next++
				}
				if page.Next == "" {
					break
				}
				q.Cursor = page.Next
			}
			if next != MaxHistoryEntries+1 || m.commits != 0 {
				t.Fatal("incomplete or mutating read", next, m.commits)
			}
		})
	}
}

func TestAssignmentHistoryOutcomes(t *testing.T) {
	_, m := fixture(t)
	s := New(historyMemory{m})
	first := command(t, 1, Assign, 0)
	execute(t, s, first)
	execute(t, s, command(t, 2, RecordOriginalTarget, 1))
	q := HistoryQuery{EffectiveQuery: EffectiveQuery{TagID: first.TagID, At: first.EffectiveAt}, Limit: 1}
	page, err := s.ListAssignmentHistory(t.Context(), q)
	if err != nil || page.Next == "" {
		t.Fatal("missing initial continuation", err)
	}
	future := uint64(3)
	for _, tc := range []struct {
		name string
		edit func(*HistoryQuery)
		want error
	}{
		{"zero size", func(q *HistoryQuery) { q.Limit = 0 }, ErrInvalidInput},
		{"negative size", func(q *HistoryQuery) { q.Limit = -1 }, ErrInvalidInput},
		{"size over limit", func(q *HistoryQuery) { q.Limit = MaxHistoryEntries + 1 }, ErrInvalidInput},
		{"cursor within cap", func(q *HistoryQuery) { q.Cursor = strings.Repeat("A", MaxHistoryCursor) }, ErrStaleCursor},
		{"cursor over cap", func(q *HistoryQuery) { q.Cursor = strings.Repeat("A", MaxHistoryCursor+1) }, ErrLimit},
		{"wrong time", func(q *HistoryQuery) { q.At = q.At.Add(time.Nanosecond); q.Cursor = page.Next }, ErrStaleCursor},
		{"wrong size", func(q *HistoryQuery) { q.Limit++; q.Cursor = page.Next }, ErrStaleCursor},
		{"future knowledge", func(q *HistoryQuery) { q.KnowledgeRevision = &future }, ErrKnowledgeRevision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := q
			tc.edit(&bad)
			got, err := s.ListAssignmentHistory(t.Context(), bad)
			if !reflect.DeepEqual(got, HistoryPage{}) || !errors.Is(err, tc.want) {
				t.Fatalf("outcome %v, want %v", err, tc.want)
			}
		})
	}
	q.Cursor = page.Next
	q.At = q.At.In(time.FixedZone("same instant", 3600))
	got, err := s.ListAssignmentHistory(t.Context(), q)
	if err != nil || len(got.Events) != 1 || got.Events[0].Result.Revision != 2 || got.Next != "" {
		t.Fatal("equivalent instant did not continue", got, err)
	}
	m.actor.Read = false
	_, err = s.ListAssignmentHistory(t.Context(), q)
	wantError(t, err, ErrUnauthorized)
	m.actor.Read = true
	for _, fault := range []error{ErrBusy, ErrUnsupportedState, ErrUnavailable} {
		m.fault = fault
		_, err = s.ListAssignmentHistory(t.Context(), q)
		wantError(t, err, fault)
	}
	m.fault = nil
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.ListAssignmentHistory(canceled, q)
	wantError(t, err, context.Canceled)
	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = s.ListAssignmentHistory(expired, q)
	wantError(t, err, context.DeadlineExceeded)

	// Corruption beyond the original page must take precedence over issuing
	// that page or accepting its continuation.
	snapshot := m.snapshots[first.TagID]
	snapshot.Events[1].Primitive[0] ^= 1
	for _, cursor := range []string{"", q.Cursor} {
		q.Cursor = cursor
		got, err := s.ListAssignmentHistory(t.Context(), q)
		wantError(t, err, ErrUnavailable)
		if !reflect.DeepEqual(got, HistoryPage{}) {
			t.Fatal("released data from a corrupt aggregate")
		}
	}
}
