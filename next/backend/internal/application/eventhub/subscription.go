package eventhub

import "context"

type Subscription struct {
	cancel     context.CancelFunc
	unwatch    func()
	service    *Service
	capability ReadCapability
	cursor     string
	queue      []Record
	charged    int
	closed     bool
	failure    error
	wake       chan struct{}
}

func (s *Subscription) Next(ctx context.Context) (Record, error) {
	if ctx == nil {
		return Record{}, ErrInvalidEvent
	}
	if s == nil || s.service == nil {
		return Record{}, ErrRevoked
	}
	for {
		h := s.service
		h.mu.Lock()
		if err := s.capability.Authorize(); err != nil {
			h.closeSubscription(s, err)
		}
		if s.closed {
			err := s.failure
			h.mu.Unlock()
			return Record{}, err
		}
		if len(s.queue) > 0 {
			r := s.queue[0]
			s.queue[0] = Record{}
			s.queue = s.queue[1:]
			n := recordCharge(r)
			s.charged -= n
			h.diagnostics.QueuedBytes -= uint64(n)
			h.mu.Unlock()
			return r, nil
		}
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			s.Close()
			return Record{}, ctx.Err()
		case <-s.wake:
		}
	}
}
func (s *Subscription) Close() {
	if s == nil || s.service == nil {
		return
	}
	h := s.service
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeSubscription(s, ErrRevoked)
}

// Charge the canonical envelope, cursor, type and 21 literal v1 framing bytes.
// This reserves the complete frame without implementing a transport.
func recordCharge(r Record) int { return len(r.Bytes) + len(r.Cursor) + len(r.Type) + 21 }
func (h *Service) closeSubscription(s *Subscription, err error) {
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.unwatch != nil {
		s.unwatch()
		s.unwatch = nil
	}
	s.failure = err
	h.diagnostics.QueuedBytes -= uint64(s.charged)
	h.diagnostics.ActiveSubscribers--
	s.charged = 0
	s.queue = nil
	close(s.wake)
}
