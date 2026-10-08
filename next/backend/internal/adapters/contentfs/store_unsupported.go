//go:build !linux

package contentfs

import (
	"context"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

type platformStore struct{}

func (*platformStore) ImportDuration() time.Duration { return 0 }

func (*platformStore) setRangeOptions(RangeOptions) {}
func (*platformStore) readRange(context.Context, content.BlobID, uint64, ByteRange, RangeSink) error {
	return ErrUnsupported
}

func openStore(context.Context, string, content.TAFOptions) (*Store, error) {
	return nil, ErrUnsupported
}
func (*platformStore) publish(context.Context, content.BlobID, uint64, content.TAFSource, content.TAFSourceMode) (content.TAFEnvelope, error) {
	return content.TAFEnvelope{}, ErrUnsupported
}
func (*platformStore) close() error { return nil }

func openForInventory(context.Context, string, content.TAFOptions) (*Store, error) {
	return nil, ErrUnsupported
}
func (*Store) Inventory(context.Context, string, int, ReferenceLookup) (InventoryPage, error) {
	return InventoryPage{}, ErrUnsupported
}
func (*Store) InvalidateInventory() error                               { return ErrUnsupported }
func (*Store) Quarantine(context.Context, content.BlobID, uint64) error { return ErrUnsupported }
