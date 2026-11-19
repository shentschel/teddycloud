package eventhub

import (
	"context"
	"sync"
	"time"
)

type Service struct {
	mu            sync.Mutex
	journal       Journal
	subscriptions []*Subscription
	next          int
	closed        bool
	cancel        context.CancelFunc
	done          chan struct{}
	hints         chan struct{}
	diagnostics   Diagnostics
}

func NewService(parent context.Context, journal Journal) (*Service, error) {
	if parent == nil || journal == nil {
		return nil, ErrInvalidEvent
	}
	ctx, cancel := context.WithCancel(parent)
	h := &Service{journal: journal, cancel: cancel, done: make(chan struct{}), hints: make(chan struct{}, 1)}
	go h.run(ctx)
	return h, nil
}
func (h *Service) Hint() {
	if h == nil {
		return
	}
	select {
	case h.hints <- struct{}{}:
	default:
	}
}
func (h *Service) Subscribe(ctx context.Context, cap ReadCapability, cursor string) (*Subscription, error) {
	if ctx == nil {
		return nil, ErrInvalidEvent
	}
	if h == nil {
		return nil, ErrUnavailable
	}
	if err := cap.Authorize(); err != nil {
		return nil, err
	}
	page, err := h.journal.Replay(ctx, cap, cursor, 0)
	if err != nil {
		return nil, err
	}
	if cursor == "" {
		cursor = page.Barrier
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrRevoked
	}
	if err := cap.Authorize(); err != nil {
		return nil, err
	}
	active, stream := 0, 0
	for _, s := range h.subscriptions {
		if s.closed {
			continue
		}
		active++
		if s.capability.SameGrant(cap) {
			return nil, ErrCapacityExceeded
		}
		if s.capability.kind == cap.kind && s.capability.box == cap.box && s.capability.authority == cap.authority {
			stream++
		}
	}
	if active >= MaxSubscribers || stream >= MaxStreamSubscribers {
		h.diagnostics.DeniedAdmissions++
		return nil, ErrCapacityExceeded
	}
	s := &Subscription{service: h, capability: cap, cursor: cursor, wake: make(chan struct{}, 1)}
	unwatch, err := cap.watch(func() { h.mu.Lock(); defer h.mu.Unlock(); h.closeSubscription(s, ErrRevoked) })
	if err != nil {
		return nil, err
	}
	s.unwatch = unwatch
	live := h.subscriptions[:0]
	for _, old := range h.subscriptions {
		if !old.closed {
			live = append(live, old)
		}
	}
	h.subscriptions = append(live, s)
	h.diagnostics.ActiveSubscribers++
	h.Hint()
	return s, nil
}
func (h *Service) run(ctx context.Context) {
	defer func() {
		h.mu.Lock()
		h.closed = true
		for _, s := range h.subscriptions {
			h.closeSubscription(s, ErrRevoked)
		}
		h.mu.Unlock()
		close(h.done)
	}()
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-h.hints:
		}
		h.mu.Lock()
		groups := make(map[string][]*Subscription)
		keys := []string{}
		for _, s := range h.subscriptions {
			if s.closed {
				continue
			}
			key := s.capability.kind + ":" + s.capability.box
			if _, ok := groups[key]; !ok {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], s)
		}
		turns := []*Subscription{}
		if len(keys) > 0 {
			h.next %= len(keys)
			for depth := 0; depth < MaxStreamSubscribers; depth++ {
				for k := 0; k < len(keys); k++ {
					group := groups[keys[(h.next+k)%len(keys)]]
					if depth < len(group) {
						turns = append(turns, group[depth])
					}
				}
			}
			h.next = (h.next + 1) % len(keys)
		}
		h.mu.Unlock()
		count := len(turns)
		for i := 0; i < count; i++ {
			if ctx.Err() != nil {
				return
			}
			h.mu.Lock()
			if h.closed || len(h.subscriptions) == 0 {
				h.mu.Unlock()
				break
			}
			s := turns[i]
			cursor, closed := s.cursor, s.closed
			call, cancel := context.WithTimeout(ctx, time.Second)
			if !closed {
				s.cancel = cancel
			}
			h.mu.Unlock()
			if closed {
				cancel()
				continue
			}
			page, err := h.journal.Replay(call, s.capability, cursor, MaxTurnEvents)
			cancel()
			h.mu.Lock()
			s.cancel = nil
			if !s.closed {
				if err == nil {
					err = s.capability.Authorize()
				}
				if err != nil {
					h.closeSubscription(s, err)
				} else {
					turn := 0
					for _, r := range page.Records {
						charge := recordCharge(r)
						turn += charge
						if len(r.Bytes) > MaxEnvelopeBytes || len(r.Cursor) > 512 || turn > MaxTurnBytes || len(s.queue) >= MaxQueueEvents || s.charged+charge > MaxQueueBytes || h.diagnostics.QueuedBytes+uint64(charge) > MaxQueuedBytes {
							h.diagnostics.SlowDisconnects++
							h.closeSubscription(s, ErrCapacityExceeded)
							break
						}
						s.charged += charge
						h.diagnostics.QueuedBytes += uint64(charge)
						r.Bytes = append([]byte(nil), r.Bytes...)
						s.queue = append(s.queue, r)
						s.cursor = r.Cursor
						h.diagnostics.Replayed++
						select {
						case s.wake <- struct{}{}:
						default:
						}
					}
					if !page.Complete {
						h.Hint()
					}
				}
			}
			h.mu.Unlock()
		}
	}
}
func (h *Service) Diagnostics() Diagnostics {
	if h == nil {
		return Diagnostics{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.diagnostics
}
func (h *Service) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		for _, s := range h.subscriptions {
			h.closeSubscription(s, ErrRevoked)
		}
		h.cancel()
	}
	h.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-h.done:
		h.mu.Lock()
		h.diagnostics.UnfinishedShutdownTasks = 0
		h.mu.Unlock()
		return nil
	case <-bounded.Done():
		h.mu.Lock()
		h.diagnostics.UnfinishedShutdownTasks = 1
		h.mu.Unlock()
		return ErrShutdownIncomplete
	}
}
