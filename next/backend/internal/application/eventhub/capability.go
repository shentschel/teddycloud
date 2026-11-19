package eventhub

import (
	"sync"
	"time"
)

// Authority is trusted local composition, never a serialized credential.
type Authority struct {
	mu               sync.Mutex
	tenant, instance string
	generation, next uint64
	closed           bool
	watchers         map[uint64]func()
}
type ReadCapability struct {
	authority      *Authority
	generation, id uint64
	kind, box      string
	expires        time.Time
}
type ProducerCapability struct {
	authority  *Authority
	generation uint64
	owner      Owner
	box, epoch string
}

func NewAuthority(tenant, instance string) (*Authority, error) {
	if !ValidOpaqueID(tenant) || !ValidOpaqueID(instance) {
		return nil, ErrInvalidEvent
	}
	return &Authority{tenant: tenant, instance: instance, generation: 1}, nil
}
func (a *Authority) IssueRead(kind, box string, expires time.Time) (ReadCapability, error) {
	if a == nil {
		return ReadCapability{}, ErrRevoked
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.next == 1<<63-1 {
		return ReadCapability{}, ErrRevoked
	}
	if !(kind == "control" && box == "" || kind == "box" && ValidOpaqueID(box)) {
		return ReadCapability{}, ErrInvalidEvent
	}
	if !expires.IsZero() && !time.Now().Before(expires) {
		return ReadCapability{}, ErrRevoked
	}
	a.next++
	return ReadCapability{a, a.generation, a.next, kind, box, expires}, nil
}
func (a *Authority) IssueProducer(owner Owner, box, epoch string) (ProducerCapability, error) {
	if a == nil {
		return ProducerCapability{}, ErrRevoked
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ProducerCapability{}, ErrRevoked
	}
	if !ValidOpaqueID(epoch) || !one(string(owner), string(TagRegistry), string(ContentStore), string(Catalog), string(Assignment), string(GatewayObservation), string(Lifecycle)) {
		return ProducerCapability{}, ErrUnauthorizedProducer
	}
	if box != "" && !ValidOpaqueID(box) || owner == GatewayObservation && box == "" || box != "" && owner != GatewayObservation && owner != Lifecycle {
		return ProducerCapability{}, ErrUnauthorizedProducer
	}
	return ProducerCapability{a, a.generation, owner, box, epoch}, nil
}
func (a *Authority) Revoke() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.generation == 1<<63-1 {
		a.closed = true
	} else {
		a.generation++
	}
	callbacks := a.watchers
	a.watchers = nil
	a.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}
func (a *Authority) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.closed = true
	callbacks := a.watchers
	a.watchers = nil
	a.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}
func (c ReadCapability) watch(callback func()) (func(), error) {
	a := c.authority
	if a == nil {
		return nil, ErrRevoked
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || c.generation != a.generation || !c.expires.IsZero() && !time.Now().Before(c.expires) {
		return nil, ErrRevoked
	}
	if a.watchers == nil {
		a.watchers = make(map[uint64]func())
	}
	if _, exists := a.watchers[c.id]; exists {
		return nil, ErrCapacityExceeded
	}
	a.watchers[c.id] = callback
	return func() { a.mu.Lock(); delete(a.watchers, c.id); a.mu.Unlock() }, nil
}
func (c ReadCapability) Authorize() error {
	a := c.authority
	if a == nil {
		return ErrRevoked
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || c.generation != a.generation || !c.expires.IsZero() && !time.Now().Before(c.expires) {
		return ErrRevoked
	}
	return nil
}
func (c ReadCapability) Scope() (tenant, kind, box string, err error) {
	if err = c.Authorize(); err != nil {
		return
	}
	return c.authority.tenant, c.kind, c.box, nil
}
func (c ReadCapability) SameGrant(o ReadCapability) bool {
	return c.authority == o.authority && c.id == o.id && c.generation == o.generation
}
func (c ProducerCapability) Authorize(e Envelope) error {
	a := c.authority
	if a == nil {
		return ErrUnauthorizedProducer
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.generation != c.generation || e.Stream.TenantID != a.tenant || e.Source.InstanceID != a.instance || e.Source.Epoch != c.epoch || e.Source.Owner != c.owner {
		return ErrUnauthorizedProducer
	}
	if c.box == "" && e.Stream.Kind != "control" || c.box != "" && (e.Stream.Kind != "box" || e.Stream.BoxID == nil || *e.Stream.BoxID != c.box) {
		return ErrUnauthorizedProducer
	}
	if p, ok := e.Payload.(InvalidationPayload); ok && e.Type == CatalogInvalidated && p.Reason == "source-revocation" {
		return ErrUnsupported
	}
	return Validate(e)
}

// OwnsRead rejects capabilities from another owner even for equal tenant text.
func (a *Authority) OwnsRead(c ReadCapability) bool {
	return a != nil && c.authority == a && c.Authorize() == nil
}
