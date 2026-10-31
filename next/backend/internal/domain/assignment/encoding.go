package assignment

import (
	"encoding/binary"
	"errors"
	"sort"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// ErrInvalidCanonicalValue means a value cannot represent an A1a review value.
var ErrInvalidCanonicalValue = errors.New("invalid canonical assignment value")

// CanonicalEncodingVersion identifies this primitive value format, not a
// persisted ledger schema or a complete assignment command format.
const CanonicalEncodingVersion byte = 1

// Format v1: ASCII TCAE, u8 version, u8 type (Evidence=1, OriginalTarget=2,
// ReviewRequest=3, ReviewProvenance=4, ReviewRecord=5), then fields below in order.
// Integers are unsigned big endian. Text is u32 byte length + exact UTF-8 bytes;
// there is no trimming, Unicode normalization, escaping or locale conversion.
// A nested value is u32 byte length + its complete header and payload. Length 0
// denotes an absent Evidence/Target, permitted only in inactive record branches.
// Support is u32 count + framed ID strings, sorted by raw ID bytes. Order has no
// meaning in A1a, but duplicates are invalid. All records require support, so
// both nil and non-nil empty support reject; neither gains an identity.
//
// Field order:
//
//	Evidence: ID, subject, kind(u8), source, disposition(u8).
//	OriginalTarget: content ID, version ID.
//	ReviewRequest: command, expected revision(u64), policy revision(u64), reason.
//	ReviewProvenance: command, subject, actor, reason, policy revision(u64),
//	  expected revision(u64), resulting revision(u64).
//	ReviewRecord: kind(u8), provenance(nested), evidence(nested or absent),
//	  classification(u8), target(nested or absent), support.
//
// CanonicalBytes returns owned collision-safe equality/digest input, without a
// hashing policy. Each top-level value is self-delimiting. It conveys neither
// authority nor reference existence, support agreement or aggregate validity.
// No decoder, replay admission or persistence is activated here.
func (v Evidence) CanonicalBytes() ([]byte, error)         { return canonicalBytes(v) }
func (v OriginalTarget) CanonicalBytes() ([]byte, error)   { return canonicalBytes(v) }
func (v ReviewRequest) CanonicalBytes() ([]byte, error)    { return canonicalBytes(v) }
func (v ReviewProvenance) CanonicalBytes() ([]byte, error) { return canonicalBytes(v) }
func (v ReviewRecord) CanonicalBytes() ([]byte, error)     { return canonicalBytes(v) }

// CanonicalByteLen measures exactly the complete corresponding CanonicalBytes,
// including all headers, nested framing, text and support, without allocating
// the output buffer. It is not aggregate retention admission. Future writers
// must charge every retained command/result/snapshot/index copy in addition to
// these primitive bytes and must not reduce A1a's conservative reviewRetention.
func (v Evidence) CanonicalByteLen() (uint64, error)         { return canonicalByteLen(v) }
func (v OriginalTarget) CanonicalByteLen() (uint64, error)   { return canonicalByteLen(v) }
func (v ReviewRequest) CanonicalByteLen() (uint64, error)    { return canonicalByteLen(v) }
func (v ReviewProvenance) CanonicalByteLen() (uint64, error) { return canonicalByteLen(v) }
func (v ReviewRecord) CanonicalByteLen() (uint64, error)     { return canonicalByteLen(v) }

type canonicalValue interface {
	appendCanonical(*canonicalEncoder) error
}

type canonicalEncoder struct {
	bytes []byte
	size  uint64
	count bool
}

func canonicalBytes(v canonicalValue) ([]byte, error) {
	e := canonicalEncoder{}
	if err := v.appendCanonical(&e); err != nil {
		return nil, err
	}
	return e.bytes, nil
}

func canonicalByteLen(v canonicalValue) (uint64, error) {
	e := canonicalEncoder{count: true}
	if err := v.appendCanonical(&e); err != nil {
		return 0, err
	}
	return e.size, nil
}

func (e *canonicalEncoder) raw(b []byte) {
	e.size += uint64(len(b))
	if !e.count {
		e.bytes = append(e.bytes, b...)
	}
}
func (e *canonicalEncoder) u8(v byte) { e.raw([]byte{v}) }
func (e *canonicalEncoder) u32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	e.raw(b[:])
}
func (e *canonicalEncoder) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	e.raw(b[:])
}
func (e *canonicalEncoder) text(v string) {
	// All text/ID lengths have been validated against A1a bounds before here.
	e.u32(uint32(len(v)))
	e.size += uint64(len(v))
	if !e.count {
		e.bytes = append(e.bytes, v...)
	}
}
func (e *canonicalEncoder) header(kind byte) {
	e.raw([]byte{'T', 'C', 'A', 'E', CanonicalEncodingVersion, kind})
}
func (e *canonicalEncoder) nested(v canonicalValue) error {
	n, err := canonicalByteLen(v)
	if err != nil {
		return err
	}
	// Only the five bounded primitive types can reach here; none approaches u32.
	e.u32(uint32(n))
	return v.appendCanonical(e)
}

func (v Evidence) appendCanonical(e *canonicalEncoder) error {
	if _, err := ParseEvidenceID(v.id.String()); err != nil {
		return ErrInvalidCanonicalValue
	}
	if _, err := identity.ParseTagID(v.subject.String()); err != nil {
		return ErrInvalidCanonicalValue
	}
	if _, err := NewEvidence(v.id, v.subject, v.kind, v.source, v.disposition); err != nil {
		return ErrInvalidCanonicalValue
	}
	e.header(1)
	e.text(v.id.String())
	e.text(v.subject.String())
	e.u8(byte(v.kind))
	e.text(v.source)
	e.u8(byte(v.disposition))
	return nil
}

func (v OriginalTarget) appendCanonical(e *canonicalEncoder) error {
	if _, err := identity.ParseContentID(v.content.String()); err != nil {
		return ErrInvalidCanonicalValue
	}
	if _, err := identity.ParseContentVersionID(v.version.String()); err != nil {
		return ErrInvalidCanonicalValue
	}
	e.header(2)
	e.text(v.content.String())
	e.text(v.version.String())
	return nil
}

func (v ReviewRequest) appendCanonical(e *canonicalEncoder) error {
	if _, err := ParseReviewID(v.command.String()); err != nil {
		return ErrInvalidCanonicalValue
	}
	if _, err := NewReviewRequest(v.command, v.expectedRevision, v.policyRevision, v.reason); err != nil {
		return ErrInvalidCanonicalValue
	}
	e.header(3)
	e.text(v.command.String())
	e.u64(v.expectedRevision)
	e.u64(v.policyRevision)
	e.text(v.reason)
	return nil
}

func (v ReviewProvenance) appendCanonical(e *canonicalEncoder) error {
	q := ReviewRequest{v.Command, v.ExpectedRevision, v.PolicyRevision, v.Reason}
	if _, err := canonicalByteLen(q); err != nil {
		return err
	}
	if _, err := identity.ParseTagID(v.Subject.String()); err != nil {
		return ErrInvalidCanonicalValue
	}
	// Match A1a's uint64 revision value semantics; a future D3 ledger must apply
	// its own signed-64 ceiling and dependency/admission checks.
	if !safeReviewText(v.Actor, MaxReviewReferenceBytes) || v.ExpectedRevision == ^uint64(0) || v.Revision != v.ExpectedRevision+1 {
		return ErrInvalidCanonicalValue
	}
	e.header(4)
	e.text(v.Command.String())
	e.text(v.Subject.String())
	e.text(v.Actor)
	e.text(v.Reason)
	e.u64(v.PolicyRevision)
	e.u64(v.ExpectedRevision)
	e.u64(v.Revision)
	return nil
}

func (v ReviewRecord) appendCanonical(e *canonicalEncoder) error {
	// Bound input work before copying or sorting support. No empty list can
	// represent an A1a claim or resolution, regardless of its nilness.
	if len(v.Support) == 0 || uint64(len(v.Support)) > MaxSelectedSupport {
		return ErrInvalidCanonicalValue
	}
	for _, id := range v.Support {
		if _, err := ParseEvidenceID(id.String()); err != nil {
			return ErrInvalidCanonicalValue
		}
	}
	claim := v.Kind == ClassificationClaim || v.Kind == OriginalTargetClaim
	class := v.Kind == ClassificationClaim || v.Kind == ClassificationResolution
	switch v.Kind {
	case ClassificationClaim, OriginalTargetClaim, ClassificationResolution, OriginalTargetResolution:
	default:
		return ErrInvalidCanonicalValue
	}
	if claim {
		if v.Evidence.subject != v.Provenance.Subject || len(v.Support) != 1 || v.Support[0] != v.Evidence.id {
			return ErrInvalidCanonicalValue
		}
	} else if v.Evidence != (Evidence{}) {
		return ErrInvalidCanonicalValue
	}
	if class {
		if (v.Classification != Original && v.Classification != CustomCard) || v.Target != (OriginalTarget{}) {
			return ErrInvalidCanonicalValue
		}
	} else if v.Classification != Unknown {
		return ErrInvalidCanonicalValue
	}
	ids := append([]EvidenceID(nil), v.Support...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].value < ids[j].value })
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return ErrInvalidCanonicalValue
		}
	}
	e.header(5)
	e.u8(byte(v.Kind))
	if err := e.nested(v.Provenance); err != nil {
		return err
	}
	if claim {
		if err := e.nested(v.Evidence); err != nil {
			return err
		}
	} else {
		e.u32(0)
	}
	e.u8(byte(v.Classification))
	if !class {
		if err := e.nested(v.Target); err != nil {
			return err
		}
	} else {
		e.u32(0)
	}
	e.u32(uint32(len(ids)))
	for _, id := range ids {
		e.text(id.String())
	}
	return nil
}
