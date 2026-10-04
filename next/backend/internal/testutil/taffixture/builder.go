// Package taffixture builds deterministic, synthetic storage envelopes. Payload
// bytes are opaque generated data, not playable recordings or private media.
package taffixture

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

const MaxPayloadBytes = 1048576

var ErrInvalidFixture = errors.New("invalid synthetic TAF fixture")

// Fixture retains one fixed header plus counters/digests, never a payload buffer.
type Fixture struct {
	header [4096]byte
	size   uint64
	sha1   [20]byte
	digest [32]byte
}

func New(payloadBytes uint64, audioID uint32, pages []uint32) (Fixture, error) {
	if payloadBytes == 0 || payloadBytes > MaxPayloadBytes || len(pages) > 99 {
		return Fixture{}, ErrInvalidFixture
	}
	for i, p := range pages {
		if (i == 0 && p != 0) || (i > 0 && p <= pages[i-1]) || uint64(p)*4096 >= payloadBytes {
			return Fixture{}, ErrInvalidFixture
		}
	}
	f := Fixture{size: payloadBytes + 4096}
	payloadHash := sha1.New()
	var buffer [65536]byte
	for pos := uint64(0); pos < payloadBytes; {
		n := min(uint64(len(buffer)), payloadBytes-pos)
		fillPayload(buffer[:int(n)], pos)
		_, _ = payloadHash.Write(buffer[:int(n)])
		pos += n
	}
	copy(f.sha1[:], payloadHash.Sum(nil))
	message := []byte{0x0a, 20}
	message = append(message, f.sha1[:]...)
	message = append(message, 0x10)
	message = binary.AppendUvarint(message, payloadBytes)
	message = append(message, 0x18)
	message = binary.AppendUvarint(message, uint64(audioID))
	if len(pages) > 0 {
		packed := make([]byte, 0, 495)
		for _, p := range pages {
			packed = binary.AppendUvarint(packed, uint64(p))
		}
		message = append(message, 0x22)
		message = binary.AppendUvarint(message, uint64(len(packed)))
		message = append(message, packed...)
	}
	for fill := 0; fill <= 4092; fill++ {
		length := binary.AppendUvarint(nil, uint64(fill))
		if len(message)+1+len(length)+fill != 4092 {
			continue
		}
		message = append(message, 0x2a)
		message = append(message, length...)
		message = append(message, make([]byte, fill)...)
		binary.BigEndian.PutUint32(f.header[:4], 4092)
		copy(f.header[4:], message)
		completeHash := sha256.New()
		_, err := io.Copy(completeHash, &plainReader{reader: f.Open()})
		if err != nil {
			return Fixture{}, err
		}
		copy(f.digest[:], completeHash.Sum(nil))
		return f, nil
	}
	return Fixture{}, ErrInvalidFixture
}
func fillPayload(buffer []byte, offset uint64) {
	for i := range buffer {
		buffer[i] = byte((offset+uint64(i))*31 + 17)
	}
}
func (f Fixture) Header() [4096]byte    { return f.header }
func (f Fixture) PayloadSHA1() [20]byte { return f.sha1 }
func (f Fixture) BlobDigest() [32]byte  { return f.digest }
func (f Fixture) CompleteBytes() uint64 { return f.size }

// MutateHeader is bounded, copies its input, and deliberately preserves the old
// digest so tests can assert rejection of a damaged original envelope.
func (f Fixture) MutateHeader(offset int, mutation []byte) (Fixture, error) {
	if offset < 0 || offset > len(f.header) || len(mutation) > len(f.header)-offset {
		return Fixture{}, ErrInvalidFixture
	}
	copy(f.header[offset:], mutation)
	return f, nil
}
func (f Fixture) Open() *Reader { return &Reader{fixture: f} }

type Reader struct {
	fixture Fixture
	offset  uint64
	closed  bool
}

func (r *Reader) Close() error { r.closed = true; return nil }
func (r *Reader) Read(ctx context.Context, buffer []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	if r.offset >= r.fixture.size {
		return 0, io.EOF
	}
	n := int(min(uint64(len(buffer)), r.fixture.size-r.offset))
	for i := 0; i < n; {
		if r.offset < 4096 {
			amount := int(min(uint64(n-i), 4096-r.offset))
			copy(buffer[i:i+amount], r.fixture.header[int(r.offset):int(r.offset)+amount])
			r.offset += uint64(amount)
			i += amount
		} else {
			fillPayload(buffer[i:n], r.offset-4096)
			r.offset += uint64(n - i)
			i = n
		}
	}
	return n, nil
}

type plainReader struct{ reader *Reader }

func (r *plainReader) Read(p []byte) (int, error) { return r.reader.Read(context.Background(), p) }
