package eventhub

import (
	"strings"
	"testing"
	"time"
)

func TestEventProducerAuthority(t *testing.T) {
	e := testEnvelope()
	a, err := NewAuthority(e.Stream.TenantID, e.Source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := a.IssueProducer(TagRegistry, "", e.Source.Epoch)
	if err != nil || cap.Authorize(e) != nil {
		t.Fatal(err)
	}
	var zero ProducerCapability
	if zero.Authorize(e) != ErrUnauthorizedProducer {
		t.Fatal("zero capability")
	}
	for _, owner := range []Owner{ContentStore, Catalog, Assignment, Lifecycle} {
		c, _ := a.IssueProducer(owner, "", e.Source.Epoch)
		if c.Authorize(e) == nil {
			t.Fatal("cross-owner fact admitted")
		}
	}
	e.Source.InstanceID = strings.Repeat("5", 32)
	if cap.Authorize(e) != ErrUnauthorizedProducer {
		t.Fatal("source identity substituted")
	}
	e = testEnvelope()
	e.Stream.TenantID = strings.Repeat("6", 32)
	if cap.Authorize(e) != ErrUnauthorizedProducer {
		t.Fatal("tenant substituted")
	}
	if _, err := a.IssueProducer(GatewayObservation, "", e.Source.Epoch); err != ErrUnauthorizedProducer {
		t.Fatal("unscoped ingress")
	}
	e = testEnvelope()
	a.Revoke()
	if cap.Authorize(e) != ErrUnauthorizedProducer {
		t.Fatal("revoked producer")
	}
}
func TestEventIsolation(t *testing.T) {
	a, _ := NewAuthority(strings.Repeat("1", 32), strings.Repeat("2", 32))
	b, _ := NewAuthority(strings.Repeat("1", 32), strings.Repeat("2", 32))
	c, _ := a.IssueRead("box", strings.Repeat("3", 32), time.Time{})
	if !a.OwnsRead(c) || b.OwnsRead(c) {
		t.Fatal("authority confused by equal text")
	}
	if _, err := a.IssueRead("control", strings.Repeat("3", 32), time.Time{}); err == nil {
		t.Fatal("mixed scope")
	}
	a.Revoke()
	if c.Authorize() != ErrRevoked {
		t.Fatal("revocation")
	}
	if _, err := a.IssueRead("control", "", time.Now().Add(-time.Second)); err != ErrRevoked {
		t.Fatal("expired grant")
	}
}
