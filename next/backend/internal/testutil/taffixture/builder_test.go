package taffixture_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

func TestDeterministicEnvelopeFixtures(t *testing.T) {
	for _, count := range []int{0, 1, 99} {
		pages := make([]uint32, count)
		for i := range pages {
			pages[i] = uint32(i)
		}
		size := uint64(max(1, (count-1)*4096+1))
		fixture, err := taffixture.New(size, 42, pages)
		if err != nil {
			t.Fatal(err)
		}
		again, err := taffixture.New(size, 42, pages)
		if err != nil || fixture != again {
			t.Fatal("nondeterministic", err)
		}
		e, err := content.ValidateTAF(context.Background(), fixture.Open(), content.FiniteTAFSource, content.DefaultTAFOptions())
		if err != nil || e.BlobID().Digest() != fixture.BlobDigest() || e.PayloadSHA1() != fixture.PayloadSHA1() || e.CompleteBytes() != fixture.CompleteBytes() || len(e.TrackPages()) != count {
			t.Fatal("fixture rejected", err)
		}
		mutated, err := fixture.MutateHeader(4095, []byte{1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = content.ValidateTAF(context.Background(), mutated.Open(), content.FiniteTAFSource, content.DefaultTAFOptions()); !errors.Is(err, content.ErrInvalidTAF) {
			t.Fatal(err)
		}
		if fixture.Header() == mutated.Header() || fixture.BlobDigest() != mutated.BlobDigest() {
			t.Fatal("mutation aliasing")
		}
	}
	for _, tc := range []struct {
		size  uint64
		pages []uint32
	}{{0, nil}, {taffixture.MaxPayloadBytes + 1, nil}, {1, []uint32{1}}, {4097, []uint32{0, 0}}, {1, make([]uint32, 100)}} {
		if _, err := taffixture.New(tc.size, 1, tc.pages); err != taffixture.ErrInvalidFixture {
			t.Fatal(err)
		}
	}
	f, _ := taffixture.New(3, 1, nil)
	for _, tc := range []struct {
		offset int
		data   []byte
	}{{-1, nil}, {4097, nil}, {4095, []byte{1, 2}}} {
		if _, err := f.MutateHeader(tc.offset, tc.data); err != taffixture.ErrInvalidFixture {
			t.Fatal(err)
		}
	}
	r := f.Open()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background(), make([]byte, 1)); err == nil {
		t.Fatal("closed fixture readable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Open().Read(ctx, make([]byte, 1)); err != context.Canceled {
		t.Fatal(err)
	}
	// Payload generation remains identical across arbitrary chunk boundaries.
	a, b := f.Open(), f.Open()
	large := make([]byte, 4099)
	n, _ := a.Read(context.Background(), large)
	var small []byte
	buf := make([]byte, 7)
	for {
		n, err := b.Read(context.Background(), buf)
		small = append(small, buf[:n]...)
		if err != nil {
			break
		}
	}
	if n != 4099 || !bytes.Equal(large, small) {
		t.Fatal("chunk-dependent fixture")
	}
}
