//go:build !linux

package contentfs

import (
	"context"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"testing"
)

func TestBlobCapabilityFailures(t *testing.T) {
	if s, err := Open(context.Background(), "/deployment/root", content.DefaultTAFOptions()); s != nil || err != ErrUnsupported {
		t.Fatal("unsupported platform admitted", err)
	}
}
