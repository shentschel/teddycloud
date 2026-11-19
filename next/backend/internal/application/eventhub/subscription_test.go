package eventhub

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEventSlowConsumer(t *testing.T) {
	a := testAuthority(t)
	j := &testJournal{}
	j.add("control", MaxQueueEvents+1)
	h, _ := NewService(t.Context(), j)
	defer h.Close(context.Background())
	cap, _ := a.IssueRead("control", "", time.Time{})
	s, err := h.Subscribe(t.Context(), cap, "0")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.Diagnostics().SlowDisconnects == 0 {
		if time.Now().After(deadline) {
			t.Fatal("slow consumer was not disconnected")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Next(t.Context()); err != ErrCapacityExceeded {
		t.Fatal("overflow continued delivery", err)
	}
	d := h.Diagnostics()
	if d.ActiveSubscribers != 0 || d.QueuedBytes != 0 {
		t.Fatal("queue charges leaked", d)
	}
}
func TestEventSubscriptionCapacity(t *testing.T) {
	a := testAuthority(t)
	j := &testJournal{}
	h, _ := NewService(t.Context(), j)
	defer h.Close(context.Background())
	for i := 0; i < MaxStreamSubscribers; i++ {
		cap, _ := a.IssueRead("control", "", time.Time{})
		if _, err := h.Subscribe(t.Context(), cap, ""); err != nil {
			t.Fatal(err)
		}
	}
	cap, _ := a.IssueRead("control", "", time.Time{})
	if _, err := h.Subscribe(t.Context(), cap, ""); err != ErrCapacityExceeded {
		t.Fatal("stream subscriber cap", err)
	}
	for i := 0; i < MaxSubscribers-MaxStreamSubscribers; i++ {
		cap, _ := a.IssueRead("box", fmt.Sprintf("%032x", i+1), time.Time{})
		if _, err := h.Subscribe(t.Context(), cap, ""); err != nil {
			t.Fatal(err)
		}
	}
	cap, _ = a.IssueRead("box", fmt.Sprintf("%032x", MaxSubscribers), time.Time{})
	if _, err := h.Subscribe(t.Context(), cap, ""); err != ErrCapacityExceeded {
		t.Fatal("owner subscriber cap", err)
	}
}
func TestEventFairness(t *testing.T) {
	a := testAuthority(t)
	j := &testJournal{}
	j.add("control", 512)
	box := fmt.Sprintf("%032x", 1)
	j.add("box"+box, 1)
	h, _ := NewService(t.Context(), j)
	defer h.Close(context.Background())
	for range 8 {
		cap, _ := a.IssueRead("control", "", time.Time{})
		if _, err := h.Subscribe(t.Context(), cap, "0"); err != nil {
			t.Fatal(err)
		}
	}
	cap, _ := a.IssueRead("box", box, time.Time{})
	s, err := h.Subscribe(t.Context(), cap, "0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.Next(ctx); err != nil {
		t.Fatal("hot control stream starved a box", err)
	}
}
func TestEventSubscriptionCancellation(t *testing.T) {
	a := testAuthority(t)
	j := &testJournal{}
	h, _ := NewService(t.Context(), j)
	defer h.Close(context.Background())
	cap, _ := a.IssueRead("control", "", time.Time{})
	s, _ := h.Subscribe(t.Context(), cap, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Next(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	if h.Diagnostics().ActiveSubscribers != 0 {
		t.Fatal("cancellation leaked registration")
	}
}

// Fill exactly the byte budget using valid canonical envelopes and fixed-size
// replay checkpoints. Counts remain below the independent 128-event cap.
func testByteBudgetRecords(t *testing.T) []Record {
	t.Helper()
	e := testEnvelope()
	base, _ := Encode(e)
	maximum := len(base) + 252 + 136 + len(e.Type) + 21
	count := (MaxQueueBytes + maximum - 1) / maximum
	records := make([]Record, count)
	remaining := MaxQueueBytes
	for i := range records {
		size := remaining / (count - i)
		remaining -= size
		extra := size - (len(base) + 136 + len(e.Type) + 21)
		v := e
		if extra <= 126 {
			if extra > 0 {
				v.CausationID = eventTextForQueue(strings.Repeat("a", extra+2))
			}
		} else {
			v.CausationID = eventTextForQueue(strings.Repeat("a", 128))
			v.CorrelationID = eventTextForQueue(strings.Repeat("b", extra-126+2))
		}
		raw, err := Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		records[i] = Record{Type: v.Type, Bytes: raw, Cursor: fmt.Sprintf("%0136d", i+1)}
	}
	if len(records) >= MaxQueueEvents {
		t.Fatal("byte boundary masked by event count")
	}
	return records
}
func TestEventQueueByteCapacity(t *testing.T) {
	a := testAuthority(t)
	records := testByteBudgetRecords(t)
	j := &testJournal{records: map[string][]Record{"control": records}}
	h, _ := NewService(t.Context(), j)
	defer h.Close(context.Background())
	cap, _ := a.IssueRead("control", "", time.Time{})
	s, err := h.Subscribe(t.Context(), cap, "0")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for h.Diagnostics().Replayed < uint64(len(records)) {
		if time.Now().After(deadline) {
			t.Fatal("exact byte budget not queued", h.Diagnostics())
		}
		time.Sleep(time.Millisecond)
	}
	if h.Diagnostics().QueuedBytes != MaxQueueBytes {
		t.Fatal("actual canonical frame bytes were not charged exactly", h.Diagnostics())
	}
	j.mu.Lock()
	extra := records[0]
	extra.Cursor = fmt.Sprintf("%0136d", len(records)+1)
	j.records["control"] = append(j.records["control"], extra)
	j.mu.Unlock()
	h.Hint()
	for h.Diagnostics().SlowDisconnects == 0 {
		if time.Now().After(deadline) {
			t.Fatal("byte overflow did not disconnect")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = s.Next(t.Context()); err != ErrCapacityExceeded || h.Diagnostics().QueuedBytes != 0 {
		t.Fatal("overflow retained queued bytes", err)
	}
}
func TestEventGlobalQueueBytes(t *testing.T) {
	a := testAuthority(t)
	records := testByteBudgetRecords(t)
	box := fmt.Sprintf("%032x", 1)
	other := fmt.Sprintf("%032x", 2)
	j := &testJournal{records: map[string][]Record{"control": records, "box" + box: records, "box" + other: records}}
	h, _ := NewService(t.Context(), j)
	defer h.Close(context.Background())
	for i := 0; i < 16; i++ {
		kind, key := "control", ""
		if i >= 8 {
			kind, key = "box", box
		}
		cap, _ := a.IssueRead(kind, key, time.Time{})
		if _, err := h.Subscribe(t.Context(), cap, "0"); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.Diagnostics().QueuedBytes < MaxQueuedBytes {
		if time.Now().After(deadline) {
			t.Fatal("exact global byte budget not admitted", h.Diagnostics())
		}
		time.Sleep(time.Millisecond)
	}
	cap, _ := a.IssueRead("box", other, time.Time{})
	s, err := h.Subscribe(t.Context(), cap, "0")
	if err != nil {
		t.Fatal(err)
	}
	for h.Diagnostics().SlowDisconnects == 0 {
		if time.Now().After(deadline) {
			t.Fatal("global overflow not rejected")
		}
		time.Sleep(time.Millisecond)
	}
	d := h.Diagnostics()
	if d.ActiveSubscribers != 16 || d.QueuedBytes != MaxQueuedBytes {
		t.Fatal("unrelated subscriber was closed", d)
	}
	if _, err = s.Next(t.Context()); err != ErrCapacityExceeded {
		t.Fatal(err)
	}
}
func eventTextForQueue(s string) *string { return &s }
