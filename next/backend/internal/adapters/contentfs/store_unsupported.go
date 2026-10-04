//go:build !linux

package contentfs

import (
	"context"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

type platformStore struct{}

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
