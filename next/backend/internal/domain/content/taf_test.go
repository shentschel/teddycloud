package content

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"testing"
	"time"
)

type memorySource struct {
	reader         *bytes.Reader
	maxRead, calls int
}

func (s *memorySource) Read(ctx context.Context, p []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	s.calls++
	s.maxRead = max(s.maxRead, len(p))
	return s.reader.Read(p)
}
func (s *memorySource) Close() error { return nil }
func validateBytes(data []byte) (TAFEnvelope, error) {
	return ValidateTAF(context.Background(), &memorySource{reader: bytes.NewReader(data)}, FiniteTAFSource, DefaultTAFOptions())
}

// Headers below are encoded independently with standard unsigned varints and
// literal wire tags. They do not use the production decoder or fixture builder.
func padMessage(prefix []byte, suffix []byte) []byte {
	for n := 0; n <= 4092; n++ {
		length := binary.AppendUvarint(nil, uint64(n))
		if len(prefix)+1+len(length)+n+len(suffix) != 4092 {
			continue
		}
		data := []byte{0, 0, 0x0f, 0xfc}
		data = append(data, prefix...)
		data = append(data, 0x2a)
		data = append(data, length...)
		data = append(data, make([]byte, n)...)
		return append(data, suffix...)
	}
	panic("test message cannot be padded")
}
func requiredFields(payload []byte, declared uint64, audio uint64) []byte {
	h := sha1.Sum(payload)
	p := append([]byte{0x0a, 20}, h[:]...)
	p = append(p, 0x10)
	p = binary.AppendUvarint(p, declared)
	p = append(p, 0x18)
	return binary.AppendUvarint(p, audio)
}
func testFile(payload []byte, tracks []uint32) []byte {
	p := requiredFields(payload, uint64(len(payload)), 123)
	for _, page := range tracks {
		p = append(p, 0x20)
		p = binary.AppendUvarint(p, uint64(page))
	}
	return append(padMessage(p, nil), payload...)
}
func TestTAFEnvelopeProfile(t *testing.T) {
	// SHA1("abc") is a published, independently known vector.
	hash, _ := hex.DecodeString("a9993e364706816aba3e25717850c26c9cd0d89d")
	// Arbitrary order: fill before required fields; AudioID=0 is envelope evidence.
	fields := append([]byte{0x18, 0, 0x10, 3, 0x0a, 20}, hash...)
	data := append(padMessage(nil, fields), []byte("abc")...)
	e, err := validateBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if e.Profile() != 1 || e.AudioID() != 0 || e.PayloadBytes() != 3 || e.CompleteBytes() != 4099 || len(e.TrackPages()) != 0 {
		t.Fatal("incorrect facts")
	}
	if e.BlobID().Digest() != sha256.Sum256(data) || e.Header() != [4096]byte(data[:4096]) {
		t.Fatal("original bytes not retained/hashed")
	}
	if _, _, ok := e.TrackInterval(0); ok {
		t.Fatal("undivided audio invented a track")
	}
	// Optional fields are retained, accept MaxUint64/nonminimal varints, and
	// cannot alter offsets, size or AudioID. Packed/unpacked segments concatenate.
	payload := bytes.Repeat([]byte{7}, 8193)
	p := requiredFields(payload, uint64(len(payload)), math.MaxUint32)
	p = append(p, 0x22, 1, 0, 0x20, 1, 0x22, 1, 2)
	for _, tag := range []byte{0x30, 0x38, 0x40, 0x48} {
		p = append(p, tag)
		p = binary.AppendUvarint(p, math.MaxUint64)
	}
	e, err = validateBytes(append(padMessage(p, nil), payload...))
	if err != nil {
		t.Fatal(err)
	}
	pages := e.TrackPages()
	pages[0] = 10
	if e.TrackPages()[0] != 0 || e.AudioID() != math.MaxUint32 {
		t.Fatal("mutable tracks or truncated AudioID")
	}
	start, end, ok := e.TrackInterval(2)
	if !ok || start != 12288 || end != 12289 {
		t.Fatalf("last partial page interval %d %d %v", start, end, ok)
	}
	// Nonminimal but bounded values preserve complete-byte identity.
	p = append([]byte{0x18, 0x80, 0, 0x10, 0x83, 0, 0x0a, 20}, hash...)
	if _, err := validateBytes(append(padMessage(p, nil), []byte("abc")...)); err != nil {
		t.Fatal(err)
	}
	// Empty required fill is allowed when other bounded fields occupy the message.
	p = requiredFields([]byte("abc"), 3, 0)
	for len(p) < 4090 {
		p = append(p, 0x22, 0)
	}
	if len(p) != 4090 {
		t.Fatal("test parity")
	}
	p = append(p, 0x2a, 0)
	if _, err := validateBytes(append(append([]byte{0, 0, 15, 252}, p...), []byte("abc")...)); err != nil {
		t.Fatal(err)
	}
}

func TestTAFEnvelopeMalformed(t *testing.T) {
	payload := []byte("abc")
	required := requiredFields(payload, 3, 123)
	cases := []struct {
		name           string
		prefix, suffix []byte
		want           error
	}{
		{"missing-sha1", required[22:], nil, ErrInvalidTAF},
		{"missing-size", append(append([]byte(nil), required[:22]...), required[24:]...), nil, ErrInvalidTAF},
		{"missing-audio", required[:24], nil, ErrInvalidTAF},
		{"duplicate-sha1", append(append([]byte(nil), required...), required[:22]...), nil, ErrInvalidTAF},
		{"duplicate-size", append(append([]byte(nil), required...), 0x10, 3), nil, ErrInvalidTAF},
		{"duplicate-audio", append(append([]byte(nil), required...), 0x18, 1), nil, ErrInvalidTAF},
		{"duplicate-fill", append(append([]byte(nil), required...), 0x2a, 0), nil, ErrInvalidTAF},
		{"duplicate-optional", append(append([]byte(nil), required...), 0x30, 0, 0x30, 0), nil, ErrInvalidTAF},
		{"tag-zero", append(append([]byte(nil), required...), 0), nil, ErrInvalidTAF},
		{"unknown", append(append([]byte(nil), required...), 0x50, 0), nil, ErrUnsupportedTAF},
		{"unknown-group", append(append([]byte(nil), required...), 0x53), nil, ErrUnsupportedTAF},
		{"wrong-known-group", append(append([]byte(nil), required...), 0x23), nil, ErrInvalidTAF},
		{"wrong-wire", append(append([]byte(nil), required...), 0x31, 0), nil, ErrInvalidTAF},
		{"short-hash", append([]byte{0x0a, 19}, required[2:]...), nil, ErrInvalidTAF},
		{"length-overflow", append(append([]byte(nil), required...), 0x22, 0xff, 0xff, 0x7f), nil, ErrInvalidTAF},
		{"unterminated", required, []byte{0x30, 0x80}, ErrInvalidTAF},
		{"tenth-byte-overflow", append(append([]byte(nil), required...), append([]byte{0x30}, append(bytes.Repeat([]byte{0x80}, 9), 2)...)...), nil, ErrInvalidTAF},
		{"eleventh-byte", append(append([]byte(nil), required...), append([]byte{0x30}, append(bytes.Repeat([]byte{0x80}, 10), 0)...)...), nil, ErrInvalidTAF},
		{"audio-overflow", requiredFields(payload, 3, uint64(math.MaxUint32)+1), nil, ErrInvalidTAF},
		{"track-overflow", append(append([]byte(nil), required...), 0x20, 0x80, 0x80, 0x80, 0x80, 0x10), nil, ErrInvalidTAF},
		{"packed-overrun", append(append([]byte(nil), required...), 0x22, 1, 0x80), nil, ErrInvalidTAF},
		{"empty-unpacked", required, []byte{0x20}, ErrInvalidTAF},
		{"zero-size", requiredFields(payload, 0, 1), nil, ErrInvalidTAF},
		{"size-overflow", requiredFields(payload, math.MaxUint64, 1), nil, ErrInvalidTAF},
		{"sentinel-incomplete", requiredFields(payload, 251658239, 1), nil, ErrInvalidTAF},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateBytes(append(padMessage(c.prefix, c.suffix), payload...))
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
	valid := testFile(payload, nil)
	for _, c := range []struct {
		name   string
		mutate func([]byte) []byte
		want   error
	}{
		{"proto-short", func(d []byte) []byte { d[3]--; return d }, ErrUnsupportedTAF},
		{"proto-long", func(d []byte) []byte { d[3]++; return d }, ErrUnsupportedTAF},
		{"truncated-header", func(d []byte) []byte { return d[:4095] }, ErrInvalidTAF},
		{"truncated-payload", func(d []byte) []byte { return d[:len(d)-1] }, ErrInvalidTAF},
		{"trailing", func(d []byte) []byte { return append(d, 0) }, ErrInvalidTAF},
		{"hash-mismatch", func(d []byte) []byte { d[len(d)-1] ^= 1; return d }, ErrInvalidTAF},
		{"nonzero-fill", func(d []byte) []byte { d[4095] = 1; return d }, ErrInvalidTAF},
		{"missing-fill", func(d []byte) []byte { d[30] = 0; return d }, ErrInvalidTAF},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateBytes(c.mutate(bytes.Clone(valid)))
			if !errors.Is(err, c.want) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestTAFEnvelopeLimits(t *testing.T) {
	for _, count := range []int{0, 1, 99, 100} {
		payload := bytes.Repeat([]byte{5}, max(1, (count-1)*4096+1))
		pages := make([]uint32, count)
		for i := range pages {
			pages[i] = uint32(i)
		}
		_, err := validateBytes(testFile(payload, pages))
		if count == 100 {
			if !errors.Is(err, ErrTAFLimit) {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	for _, pages := range [][]uint32{{1}, {0, 0}, {0, 2, 1}, {0, 1}, {0, math.MaxUint32}} {
		if _, err := validateBytes(testFile(bytes.Repeat([]byte{1}, 4096), pages)); !errors.Is(err, ErrInvalidTAF) {
			t.Fatalf("pages %v: %v", pages, err)
		}
	}
	for _, c := range []struct {
		bytes    uint64
		duration time.Duration
	}{
		{0, time.Minute}, {4096, time.Minute}, {HardTAFBytes + 1, time.Minute}, {4097, 0}, {4097, -1}, {4097, HardImportDuration + 1},
	} {
		if _, err := NewTAFOptions(c.bytes, c.duration); !errors.Is(err, ErrInvalidTAFOptions) {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		bytes    uint64
		duration time.Duration
	}{{4097, 1}, {HardTAFBytes, HardImportDuration}, {DefaultTAFBytes, DefaultImportDuration}} {
		if _, err := NewTAFOptions(c.bytes, c.duration); err != nil {
			t.Fatal(err)
		}
	}
	options, _ := NewTAFOptions(4099, time.Minute)
	for _, n := range []int{3, 4} {
		_, err := ValidateTAF(context.Background(), &memorySource{reader: bytes.NewReader(testFile(bytes.Repeat([]byte{1}, n), nil))}, FiniteTAFSource, options)
		if n == 3 && err != nil || n == 4 && !errors.Is(err, ErrTAFLimit) {
			t.Fatal(err)
		}
	}
	// Very large declarations fail after the header, before payload I/O.
	source := &memorySource{reader: bytes.NewReader(append(padMessage(requiredFields([]byte("a"), HardTAFBytes, 1), nil), 'a'))}
	if _, err := ValidateTAF(context.Background(), source, FiniteTAFSource, DefaultTAFOptions()); !errors.Is(err, ErrTAFLimit) || source.calls != 1 {
		t.Fatal(err, source.calls)
	}
	source = &memorySource{reader: bytes.NewReader(testFile(bytes.Repeat([]byte{2}, 200000), nil))}
	if _, err := ValidateTAF(context.Background(), source, FiniteTAFSource, DefaultTAFOptions()); err != nil || source.maxRead > 65536 {
		t.Fatal(err, source.maxRead)
	}
	source = &memorySource{reader: bytes.NewReader(nil)}
	if _, err := ValidateTAF(context.Background(), source, StreamingTAFSource, DefaultTAFOptions()); !errors.Is(err, ErrUnsupportedTAF) || source.calls != 0 {
		t.Fatal(err)
	}
	if _, err := ValidateTAF(context.Background(), source, FiniteTAFSource, TAFOptions{}); !errors.Is(err, ErrInvalidTAFOptions) {
		t.Fatal(err)
	}
}

func TestTAFEnvelopeEveryFieldBoundary(t *testing.T) {
	payload := []byte("abc")
	required := requiredFields(payload, 3, 1)
	for _, field := range []byte{1, 2, 3, 5, 6, 7, 8, 9} {
		t.Run(string(rune('0'+field)), func(t *testing.T) {
			prefix := bytes.Clone(required)
			switch field {
			case 1:
				prefix[0] = 8 // bytes field as varint
			case 2:
				prefix[22] = 18
			case 3:
				prefix[24] = 26
			default:
				prefix = append(prefix, field<<3|1) // fixed64 is forbidden
			}
			if _, err := validateBytes(append(padMessage(prefix, nil), payload...)); err != ErrInvalidTAF {
				t.Fatal(err)
			}
		})
	}
	for field := byte(6); field <= 9; field++ {
		prefix := append(bytes.Clone(required), field<<3, 0, field<<3, 0)
		if _, err := validateBytes(append(padMessage(prefix, nil), payload...)); err != ErrInvalidTAF {
			t.Fatal("duplicate optional", field, err)
		}
	}
	// Missing fill rather than a damaged fill tag: only repeated empty track fields
	// occupy the remainder of the exact proto envelope.
	prefix := bytes.Clone(required)
	for len(prefix) < 4092 {
		prefix = append(prefix, 0x22, 0)
	}
	if _, err := validateBytes(append(append([]byte{0, 0, 15, 252}, prefix...), payload...)); err != ErrInvalidTAF {
		t.Fatal("missing fill", err)
	}
	// Empty fill + empty trailing track segment is valid, whereas a claimed length
	// greater than the remaining message is invalid, even if uint64-representable.
	prefix = append(bytes.Clone(required), 0x22)
	prefix = binary.AppendUvarint(prefix, math.MaxUint64)
	if _, err := validateBytes(append(padMessage(prefix, nil), payload...)); err != ErrInvalidTAF {
		t.Fatal("length overflow", err)
	}
	// A ten-byte, nonminimal uint32 zero is accepted and retained exactly.
	prefix = append(bytes.Clone(required[:24]), 0x18)
	prefix = append(prefix, bytes.Repeat([]byte{0x80}, 9)...)
	prefix = append(prefix, 0)
	if _, err := validateBytes(append(padMessage(prefix, nil), payload...)); err != nil {
		t.Fatal(err)
	}
	// Payload SHA1/AudioID do not identify original header bytes. Optional evidence
	// alone changes complete BlobID without changing payload facts or track ranges.
	a, err := validateBytes(testFile(payload, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, err := validateBytes(append(padMessage(requiredFields(payload, 3, 123), []byte{0x30, 1}), payload...))
	if err != nil {
		t.Fatal(err)
	}
	if a.BlobID() == b.BlobID() || a.AudioID() != b.AudioID() || a.PayloadSHA1() != b.PayloadSHA1() {
		t.Fatal("complete identity aliases evidence")
	}
}

// This finite synthetic source proves that the legacy stream-size literal is
// not blacklisted. It represents 240MiB without retaining the payload in memory.
type zeroPayloadSource struct {
	header    []byte
	remaining uint64
}

func (s *zeroPayloadSource) Close() error { return nil }
func (s *zeroPayloadSource) Read(ctx context.Context, p []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if len(s.header) > 0 {
		n := copy(p, s.header)
		s.header = s.header[n:]
		return n, nil
	}
	if s.remaining == 0 {
		return 0, io.EOF
	}
	n := int(min(uint64(len(p)), s.remaining))
	clear(p[:n])
	s.remaining -= uint64(n)
	return n, nil
}
func TestTAFEnvelopeFiniteSentinelSize(t *testing.T) {
	const size uint64 = 251658239
	h := sha1.New()
	var chunk [65536]byte
	for remaining := size; remaining > 0; {
		n := min(remaining, uint64(len(chunk)))
		_, _ = h.Write(chunk[:int(n)])
		remaining -= n
	}
	prefix := append([]byte{0x0a, 20}, h.Sum(nil)...)
	prefix = append(prefix, 0x10)
	prefix = binary.AppendUvarint(prefix, size)
	prefix = append(prefix, 0x18, 0)
	e, err := ValidateTAF(context.Background(), &zeroPayloadSource{padMessage(prefix, nil), size}, FiniteTAFSource, DefaultTAFOptions())
	if err != nil || e.PayloadBytes() != size || e.AudioID() != 0 {
		t.Fatal(err)
	}
}

type faultSource struct {
	read func(context.Context, []byte) (int, error)
}

func (f faultSource) Read(ctx context.Context, p []byte) (int, error) { return f.read(ctx, p) }
func (f faultSource) Close() error                                    { return nil }
func TestTAFEnvelopeInputFailures(t *testing.T) {
	for _, read := range []func(context.Context, []byte) (int, error){
		func(_ context.Context, p []byte) (int, error) { return len(p) + 1, nil },
		func(_ context.Context, p []byte) (int, error) { return -1, nil },
		func(_ context.Context, p []byte) (int, error) { return 0, nil },
		func(_ context.Context, p []byte) (int, error) { return 0, errors.New("private reader details") },
	} {
		if _, err := ValidateTAF(context.Background(), faultSource{read}, FiniteTAFSource, DefaultTAFOptions()); err != ErrTAFIO {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ValidateTAF(ctx, &memorySource{reader: bytes.NewReader(nil)}, FiniteTAFSource, DefaultTAFOptions()); err != ErrTAFCanceled {
		t.Fatal(err)
	}
	options, _ := NewTAFOptions(4097, 5*time.Millisecond)
	blocked := faultSource{func(ctx context.Context, _ []byte) (int, error) { <-ctx.Done(); return 0, ctx.Err() }}
	if _, err := ValidateTAF(context.Background(), blocked, FiniteTAFSource, options); err != ErrTAFCanceled {
		t.Fatal(err)
	}
	// An earlier caller deadline is passed through, even with larger options.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := ValidateTAF(ctx, blocked, FiniteTAFSource, DefaultTAFOptions()); err != ErrTAFCanceled {
		t.Fatal(err)
	}
	// n>0 plus EOF on the last payload read is valid; another read proves EOF.
	data := testFile([]byte("abc"), nil)
	pos := 0
	s := faultSource{func(_ context.Context, p []byte) (int, error) {
		n := copy(p, data[pos:])
		pos += n
		if pos == len(data) {
			return n, io.EOF
		}
		return n, nil
	}}
	if _, err := ValidateTAF(context.Background(), s, FiniteTAFSource, DefaultTAFOptions()); err != nil {
		t.Fatal(err)
	}
	// Early EOF remains terminal, including a full header/chunk accompanied by
	// EOF. A source must not be allowed to grow after an earlier EOF indication.
	pos = 0
	early := faultSource{func(_ context.Context, p []byte) (int, error) { n := copy(p, data[pos:]); pos += n; return n, io.EOF }}
	if _, err := ValidateTAF(context.Background(), early, FiniteTAFSource, DefaultTAFOptions()); err != ErrInvalidTAF {
		t.Fatal("early EOF", err)
	}
	// Errors while proving final EOF cannot be mistaken for successful completion.
	pos = 0
	errorAtEOF := faultSource{func(_ context.Context, p []byte) (int, error) {
		if pos == len(data) {
			return 0, errors.New("private path")
		}
		n := copy(p, data[pos:])
		pos += n
		return n, nil
	}}
	if _, err := ValidateTAF(context.Background(), errorAtEOF, FiniteTAFSource, DefaultTAFOptions()); err != ErrTAFIO {
		t.Fatal("EOF read error", err)
	}
}

func FuzzTAFEnvelopeHeader(f *testing.F) {
	valid := testFile([]byte("abc"), nil)
	f.Add(valid[:4096])
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xff}, 4096))
	f.Fuzz(func(t *testing.T, header []byte) {
		if len(header) > 4096 {
			return
		}
		data := append(bytes.Clone(header), []byte("abc")...)
		e, err := validateBytes(data)
		if err == nil {
			if len(header) != 4096 || e.PayloadBytes() != 3 || e.BlobID().Digest() != sha256.Sum256(data) {
				t.Fatal("invalid success")
			}
			if _, err = validateBytes(append(bytes.Clone(data), 0)); err == nil {
				t.Fatal("trailing bytes accepted")
			}
		}
	})
}
