package content

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"
)

const (
	TAFProfileV1          uint16 = 1
	TAFHeaderBytes               = 4096
	TAFProtoBytes                = 4092
	MaxTAFTracks                 = 99
	TAFStreamBufferBytes         = 64 * 1024
	MinTAFBytes           uint64 = 4097
	DefaultTAFBytes       uint64 = 512 * 1048576
	HardTAFBytes          uint64 = 1024 * 1048576
	DefaultImportDuration        = 10 * time.Minute
	HardImportDuration           = 15 * time.Minute
)

// Stable errors deliberately retain no reader diagnostics or media identifiers.
var (
	ErrInvalidTAF        = errors.New("invalid TAF envelope")
	ErrUnsupportedTAF    = errors.New("unsupported TAF format or source")
	ErrTAFLimit          = errors.New("TAF resource limit exceeded")
	ErrTAFIO             = errors.New("TAF input unavailable")
	ErrTAFCanceled       = errors.New("TAF validation canceled")
	ErrInvalidTAFOptions = errors.New("invalid TAF validation options")
)

// TAFOptions is immutable and cannot weaken the profile's hard ceilings. Its
// duration bounds validation; the future operation owner must carry the same
// earlier deadline through admission, publication and commit.
type TAFOptions struct {
	maxBytes uint64
	duration time.Duration
}

func NewTAFOptions(maxBytes uint64, duration time.Duration) (TAFOptions, error) {
	if maxBytes < MinTAFBytes || maxBytes > HardTAFBytes || duration <= 0 || duration > HardImportDuration {
		return TAFOptions{}, ErrInvalidTAFOptions
	}
	return TAFOptions{maxBytes: maxBytes, duration: duration}, nil
}
func DefaultTAFOptions() TAFOptions          { return TAFOptions{DefaultTAFBytes, DefaultImportDuration} }
func (o TAFOptions) MaxBytes() uint64        { return o.maxBytes }
func (o TAFOptions) Duration() time.Duration { return o.duration }

type TAFSourceMode uint8

const (
	// FiniteTAFSource promises closed, immutable complete input, not a live stream.
	FiniteTAFSource TAFSourceMode = iota + 1
	StreamingTAFSource
)

// TAFSource reads with cancellation/deadlines and Close must unblock pending
// I/O. Implementations must honor this contract; arbitrary blocking io.Readers
// are not accepted. ValidateTAF consumes but does not close a caller-owned source.
type TAFSource interface {
	Read(context.Context, []byte) (int, error)
	Close() error
}

// TAFEnvelope is verified storage evidence, never a playback certificate. The
// complete original header is retained, including nonminimal encoding/optional
// fields. Getters copy all mutable values.
type TAFEnvelope struct {
	header       [TAFHeaderBytes]byte
	payloadBytes uint64
	audioID      uint32
	payloadSHA1  [20]byte
	tracks       [MaxTAFTracks]uint32
	trackCount   int
	blob         BlobID
}

func (e TAFEnvelope) Profile() uint16 {
	if e.blob.IsZero() {
		return 0
	}
	return TAFProfileV1
}
func (e TAFEnvelope) BlobID() BlobID       { return e.blob }
func (e TAFEnvelope) PayloadBytes() uint64 { return e.payloadBytes }
func (e TAFEnvelope) CompleteBytes() uint64 {
	if e.blob.IsZero() {
		return 0
	}
	return TAFHeaderBytes + e.payloadBytes
}
func (e TAFEnvelope) AudioID() uint32              { return e.audioID }
func (e TAFEnvelope) PayloadSHA1() [20]byte        { return e.payloadSHA1 }
func (e TAFEnvelope) Header() [TAFHeaderBytes]byte { return e.header }
func (e TAFEnvelope) TrackPages() []uint32         { return append([]uint32(nil), e.tracks[:e.trackCount]...) }

// TrackInterval is half-open in complete-file bytes. Profile validation has
// already established all arithmetic and nonempty intervals.
func (e TAFEnvelope) TrackInterval(index int) (start, end uint64, ok bool) {
	if e.blob.IsZero() || index < 0 || index >= e.trackCount {
		return 0, 0, false
	}
	start = TAFHeaderBytes + uint64(e.tracks[index])*TAFHeaderBytes
	end = e.CompleteBytes()
	if index+1 < e.trackCount {
		end = TAFHeaderBytes + uint64(e.tracks[index+1])*TAFHeaderBytes
	}
	return start, end, true
}

// ValidateTAF reads a fixed header and at most 64KiB at a time, hashes all bytes,
// and performs exactly one additional byte read to prove EOF. The caller's
// earlier deadline wins. No payload buffering, codec inference or catalog lookup.
func ValidateTAF(ctx context.Context, source TAFSource, mode TAFSourceMode, options TAFOptions) (TAFEnvelope, error) {
	if source == nil || ctx == nil {
		return TAFEnvelope{}, ErrInvalidTAF
	}
	if mode != FiniteTAFSource {
		return TAFEnvelope{}, ErrUnsupportedTAF
	}
	if _, err := NewTAFOptions(options.maxBytes, options.duration); err != nil {
		return TAFEnvelope{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.duration)
	defer cancel()
	var envelope TAFEnvelope
	if err := readExact(ctx, source, envelope.header[:], false); err != nil {
		return TAFEnvelope{}, err
	}
	if binary.BigEndian.Uint32(envelope.header[:4]) != TAFProtoBytes {
		return TAFEnvelope{}, ErrUnsupportedTAF
	}
	if err := decodeHeader(&envelope); err != nil {
		return TAFEnvelope{}, err
	}
	if envelope.payloadBytes == 0 || envelope.payloadBytes > math.MaxUint64-TAFHeaderBytes {
		return TAFEnvelope{}, ErrInvalidTAF
	}
	if envelope.payloadBytes+TAFHeaderBytes > options.maxBytes {
		return TAFEnvelope{}, ErrTAFLimit
	}
	for i, p := range envelope.tracks[:envelope.trackCount] {
		page := uint64(p) * TAFHeaderBytes // uint32 * 4096 always fits uint64
		if (i == 0 && p != 0) || (i > 0 && p <= envelope.tracks[i-1]) || page >= envelope.payloadBytes {
			return TAFEnvelope{}, ErrInvalidTAF
		}
	}
	payloadHash, completeHash := sha1.New(), sha256.New()
	_, _ = completeHash.Write(envelope.header[:])
	var buffer [TAFStreamBufferBytes]byte
	for remaining := envelope.payloadBytes; remaining > 0; {
		length := min(remaining, uint64(len(buffer)))
		chunk := buffer[:int(length)]
		if err := readExact(ctx, source, chunk, length == remaining); err != nil {
			return TAFEnvelope{}, err
		}
		_, _ = payloadHash.Write(chunk)
		_, _ = completeHash.Write(chunk)
		remaining -= length
	}
	var extra [1]byte
	n, err := checkedRead(ctx, source, extra[:])
	if err != nil && err != io.EOF {
		return TAFEnvelope{}, err
	}
	if n != 0 || err != io.EOF {
		return TAFEnvelope{}, ErrInvalidTAF
	}
	var actualSHA1 [20]byte
	copy(actualSHA1[:], payloadHash.Sum(nil))
	if actualSHA1 != envelope.payloadSHA1 {
		return TAFEnvelope{}, ErrInvalidTAF
	}
	var digest [32]byte
	copy(digest[:], completeHash.Sum(nil))
	envelope.blob = NewBlobID(digest)
	return envelope, nil
}

func checkedRead(ctx context.Context, source TAFSource, dest []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ErrTAFCanceled
	}
	n, err := source.Read(ctx, dest)
	if ctx.Err() != nil {
		return 0, ErrTAFCanceled
	}
	if n < 0 || n > len(dest) {
		return 0, ErrTAFIO
	}
	if err != nil && err != io.EOF {
		return n, ErrTAFIO
	}
	if n == 0 && err == nil {
		return 0, ErrTAFIO
	}
	return n, err
}
func readExact(ctx context.Context, source TAFSource, dest []byte, allowEOF bool) error {
	for len(dest) > 0 {
		n, err := checkedRead(ctx, source, dest)
		dest = dest[n:]
		if err != nil {
			if err == io.EOF {
				if len(dest) == 0 && allowEOF {
					return nil
				}
				return ErrInvalidTAF
			}
			return err
		}
	}
	return nil
}

// Header parsing is iterative and limited to 4092 original bytes and 99 tracks.
func decodeHeader(e *TAFEnvelope) error {
	data := e.header[4:]
	var seen [10]bool
	for len(data) > 0 {
		tag, rest, err := takeVarint(data)
		if err != nil {
			return err
		}
		data = rest
		field, wire := tag>>3, tag&7
		if field == 0 {
			return ErrInvalidTAF
		}
		if field > 9 {
			return ErrUnsupportedTAF
		}
		if field != 4 && seen[field] {
			return ErrInvalidTAF
		}
		seen[field] = true
		switch field {
		case 1, 5:
			if wire != 2 {
				return ErrInvalidTAF
			}
			var value []byte
			value, data, err = takeBytes(data)
			if err != nil {
				return err
			}
			if field == 1 {
				if len(value) != 20 {
					return ErrInvalidTAF
				}
				copy(e.payloadSHA1[:], value)
			} else {
				for _, b := range value {
					if b != 0 {
						return ErrInvalidTAF
					}
				}
			}
		case 2, 3, 6, 7, 8, 9:
			if wire != 0 {
				return ErrInvalidTAF
			}
			var value uint64
			value, data, err = takeVarint(data)
			if err != nil {
				return err
			}
			if field == 2 {
				e.payloadBytes = value
			}
			if field == 3 {
				if value > math.MaxUint32 {
					return ErrInvalidTAF
				}
				e.audioID = uint32(value)
			}
		case 4:
			var entries []byte
			if wire == 2 {
				entries, data, err = takeBytes(data)
			} else if wire == 0 {
				var value uint64
				value, data, err = takeVarint(data)
				if err != nil {
					return err
				}
				if value > math.MaxUint32 {
					return ErrInvalidTAF
				}
				if e.trackCount == MaxTAFTracks {
					return ErrTAFLimit
				}
				e.tracks[e.trackCount] = uint32(value)
				e.trackCount++
				continue
			} else {
				return ErrInvalidTAF
			}
			if err != nil {
				return err
			}
			for len(entries) > 0 {
				var value uint64
				value, entries, err = takeVarint(entries)
				if err != nil {
					return err
				}
				if value > math.MaxUint32 {
					return ErrInvalidTAF
				}
				if e.trackCount == MaxTAFTracks {
					return ErrTAFLimit
				}
				e.tracks[e.trackCount] = uint32(value)
				e.trackCount++
			}
		}
	}
	if !seen[1] || !seen[2] || !seen[3] || !seen[5] {
		return ErrInvalidTAF
	}
	return nil
}

func takeVarint(data []byte) (uint64, []byte, error) {
	var value uint64
	for i := 0; i < 10 && i < len(data); i++ {
		b := data[i]
		if i == 9 && b > 1 {
			return 0, nil, ErrInvalidTAF
		}
		value |= uint64(b&127) << (7 * i)
		if b&128 == 0 {
			return value, data[i+1:], nil
		}
	}
	return 0, nil, ErrInvalidTAF
}
func takeBytes(data []byte) ([]byte, []byte, error) {
	length, rest, err := takeVarint(data)
	if err != nil {
		return nil, nil, err
	}
	if length > uint64(len(rest)) {
		return nil, nil, ErrInvalidTAF
	}
	return rest[:int(length)], rest[int(length):], nil
}
