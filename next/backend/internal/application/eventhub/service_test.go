package eventhub

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

type testJournal struct {
	mu      sync.Mutex
	records map[string][]Record
	calls   []string
}

func (j *testJournal) Replay(ctx context.Context, cap ReadCapability, cursor string, limit int) (ReplayPage, error) {
	if err := ctx.Err(); err != nil {
		return ReplayPage{}, err
	}
	_, kind, box, err := cap.Scope()
	if err != nil {
		return ReplayPage{}, err
	}
	key := kind + box
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, key)
	records := j.records[key]
	n := len(records)
	if cursor != "" {
		n, err = strconv.Atoi(cursor)
		if err != nil || n < 0 || n > len(records) {
			return ReplayPage{}, ErrInvalidCursor
		}
	}
	end := min(n+limit, len(records))
	page := ReplayPage{Barrier: strconv.Itoa(len(records)), Complete: end == len(records)}
	page.Records = append(page.Records, records[n:end]...)
	return page, nil
}
func (j *testJournal) add(key string, count int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.records == nil {
		j.records = map[string][]Record{}
	}
	for range count {
		n := len(j.records[key]) + 1
		j.records[key] = append(j.records[key], Record{Type: TagChanged, Bytes: []byte("canonical committed fake record"), Cursor: strconv.Itoa(n)})
	}
}
func testAuthority(t *testing.T) *Authority {
	t.Helper()
	e := testEnvelope()
	a, err := NewAuthority(e.Stream.TenantID, e.Source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestEventServiceLifecycle(t *testing.T) {
	a := testAuthority(t)
	j := &testJournal{}
	h, err := NewService(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	cap, _ := a.IssueRead("control", "", time.Time{})
	s, err := h.Subscribe(t.Context(), cap, "")
	if err != nil {
		t.Fatal(err)
	}
	j.add("control", 1)
	h.Hint()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	r, err := s.Next(ctx)
	if err != nil || r.Cursor != "1" {
		t.Fatal("committed scan", err)
	}
	a.Revoke()
	if h.Diagnostics().ActiveSubscribers != 0 || h.Diagnostics().QueuedBytes != 0 {
		t.Fatal("revocation retained queues")
	}
	if _, err := s.Next(ctx); err != ErrRevoked {
		t.Fatal("revoked subscriber", err)
	}
	s.Close()
	s.Close()
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestEventMissedHint(t *testing.T) {
	a := testAuthority(t)
	j := &testJournal{}
	h, _ := NewService(t.Context(), j)
	defer h.Close(context.Background())
	cap, _ := a.IssueRead("control", "", time.Time{})
	s, err := h.Subscribe(t.Context(), cap, "")
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the admission hint is consumed, then publish without a hint.
	deadline := time.Now().Add(time.Second)
	for {
		j.mu.Lock()
		scanned := len(j.calls) >= 2
		j.mu.Unlock()
		if scanned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatcher did not scan")
		}
		time.Sleep(time.Millisecond)
	}
	j.add("control", 1)
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	if _, err := s.Next(ctx); err != nil {
		t.Fatal("missed hint not recovered by poll", err)
	}
}
