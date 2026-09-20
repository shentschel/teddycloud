package tag

import (
	"errors"
	"fmt"
)

const physicalIDBytes = 8

var (
	// ErrMalformedUID means UID text is not exactly eight colon-separated bytes.
	ErrMalformedUID = errors.New("malformed tag UID")
	// ErrMalformedRUID means rUID text is not exactly sixteen hexadecimal digits.
	ErrMalformedRUID = errors.New("malformed tag rUID")
	// ErrUIDRUIDMismatch means the supplied identifiers are not byte reversals.
	ErrUIDRUIDMismatch = errors.New("tag UID and rUID do not match")
)

// UID stores the physical UID bytes in device order.
type UID struct{ bytes [physicalIDBytes]byte }

// RUID stores the physical UID bytes in reverse order.
type RUID struct{ bytes [physicalIDBytes]byte }

// UIDPair is a validated pair of equivalent physical identifier codecs.
type UIDPair struct {
	uid  UID
	ruid RUID
}

func UIDFromBytes(value [physicalIDBytes]byte) UID { return UID{bytes: value} }

func RUIDFromBytes(value [physicalIDBytes]byte) RUID { return RUID{bytes: value} }

func ParseUID(text string) (UID, error) {
	// Reject size first: parsing work is constant and oversized input is never
	// included in errors.
	if len(text) != 23 {
		return UID{}, ErrMalformedUID
	}
	var value [physicalIDBytes]byte
	for i := range physicalIDBytes {
		offset := i * 3
		if i > 0 && text[offset-1] != ':' {
			return UID{}, ErrMalformedUID
		}
		parsed, ok := decodeHexByte(text[offset], text[offset+1])
		if !ok {
			return UID{}, ErrMalformedUID
		}
		value[i] = parsed
	}
	return UIDFromBytes(value), nil
}

func ParseRUID(text string) (RUID, error) {
	if len(text) != 16 {
		return RUID{}, ErrMalformedRUID
	}
	var value [physicalIDBytes]byte
	for i := range physicalIDBytes {
		parsed, ok := decodeHexByte(text[i*2], text[i*2+1])
		if !ok {
			return RUID{}, ErrMalformedRUID
		}
		value[i] = parsed
	}
	return RUIDFromBytes(value), nil
}

func NewUIDPair(uid UID, ruid RUID) (UIDPair, error) {
	if uid.RUID() != ruid {
		return UIDPair{}, ErrUIDRUIDMismatch
	}
	return UIDPair{uid: uid, ruid: ruid}, nil
}

func (u UID) Bytes() [physicalIDBytes]byte { return u.bytes }

func (r RUID) Bytes() [physicalIDBytes]byte { return r.bytes }

func (u UID) RUID() RUID { return RUID{bytes: reverse(u.bytes)} }

func (r RUID) UID() UID { return UID{bytes: reverse(r.bytes)} }

func (p UIDPair) UID() UID { return p.uid }

func (p UIDPair) RUID() RUID { return p.ruid }

func (u UID) String() string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X:%02X:%02X",
		u.bytes[0], u.bytes[1], u.bytes[2], u.bytes[3],
		u.bytes[4], u.bytes[5], u.bytes[6], u.bytes[7])
}

func (r RUID) String() string {
	return fmt.Sprintf("%02X%02X%02X%02X%02X%02X%02X%02X",
		r.bytes[0], r.bytes[1], r.bytes[2], r.bytes[3],
		r.bytes[4], r.bytes[5], r.bytes[6], r.bytes[7])
}

func reverse(value [physicalIDBytes]byte) [physicalIDBytes]byte {
	var result [physicalIDBytes]byte
	for i := range physicalIDBytes {
		result[i] = value[physicalIDBytes-1-i]
	}
	return result
}

func decodeHexByte(high, low byte) (byte, bool) {
	hi, ok := decodeHexNibble(high)
	if !ok {
		return 0, false
	}
	lo, ok := decodeHexNibble(low)
	if !ok {
		return 0, false
	}
	return hi<<4 | lo, true
}

func decodeHexNibble(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	default:
		return 0, false
	}
}
