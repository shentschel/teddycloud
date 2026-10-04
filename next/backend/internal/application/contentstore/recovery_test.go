package contentstore

import (
	"context"
	"errors"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"testing"
)

// These spies enforce application ordering. Real descriptor/selected-database
// retry and restore integration is exercised by the SQLite media tests; actual
// helper-process termination remains a separate, explicitly open checkpoint.
type gatedOwner struct {
	active  bool
	t       *testing.T
	session Session
}

func (o *gatedOwner) AttachContentInventory(context.Context, InventoryInvalidator) error { return nil }
func (o *gatedOwner) WithinContentOperation(ctx context.Context, f func(context.Context, Session) error) error {
	if o.active {
		return ErrBusy
	}
	o.active = true
	defer func() { o.active = false }()
	return f(ctx, o.session)
}

type referenceSession struct {
	Session
	owner *gatedOwner
	calls int
}

func (s *referenceSession) References(_ context.Context, _ content.BlobID) (bool, error) {
	if !s.owner.active {
		s.owner.t.Fatal("reference acquired outside lifecycle gate")
	}
	s.calls++
	return true, nil
}

type gatedMedia struct {
	MediaStore
	owner *gatedOwner
	id    content.BlobID
	calls int
}

func (m *gatedMedia) check() {
	if !m.owner.active {
		m.owner.t.Fatal("filesystem used outside gate")
	}
	m.calls++
}
func (*gatedMedia) InvalidateInventory() error { return nil }
func (m *gatedMedia) Inventory(ctx context.Context, _ string, _ int, lookup ReferenceLookup) (InventoryPage, error) {
	m.check()
	referenced, err := lookup(ctx, m.id)
	if err != nil {
		return InventoryPage{}, err
	}
	if !referenced {
		m.owner.t.Fatal("reference callback lost selected session")
	}
	return InventoryPage{Complete: true, Entries: []InventoryEntry{{Kind: ReferencedCanonical, BlobID: m.id}}}, nil
}
func (m *gatedMedia) Quarantine(context.Context, content.BlobID, uint64) error { m.check(); return nil }
func (m *gatedMedia) Verify(context.Context, content.BlobID, uint64) error {
	m.check()
	return ErrMissing
}

func TestContentStoreMaintenanceUsesSingleGate(t *testing.T) {
	owner := &gatedOwner{t: t}
	session := &referenceSession{owner: owner}
	owner.session = session
	media := &gatedMedia{owner: owner, id: content.NewBlobID([32]byte{1})}
	service, err := NewService(t.Context(), owner, media)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.Inventory(t.Context(), "", 1)
	if err != nil || !page.Complete || session.calls != 1 {
		t.Fatalf("inventory: %+v %v", page, err)
	}
	if err := service.Quarantine(t.Context(), media.id, 4097); err != nil {
		t.Fatal(err)
	}
	if err := service.Availability(t.Context(), media.id, 4097); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if media.calls != 3 || owner.active {
		t.Fatal("operation escaped or gate leaked")
	}
}
