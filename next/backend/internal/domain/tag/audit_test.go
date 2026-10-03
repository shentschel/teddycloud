package tag

import (
	"encoding/binary"
	"strings"
	"testing"
)

func FuzzAuditCodecIdempotence(f *testing.F) {
	for _, seed := range []uint64{0, ^uint64(0), 0xabcdef0123456789} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], seed)
		uid := UIDFromBytes(raw)
		u, err := ParseUID(strings.ToLower(uid.String()))
		if err != nil || u != uid {
			t.Fatal("UID normalization", err)
		}
		r, err := ParseRUID(strings.ToLower(uid.RUID().String()))
		if err != nil || r.UID() != uid {
			t.Fatal("rUID normalization", err)
		}
		for i, b := range r.Bytes() {
			if b != raw[7-i] {
				t.Fatal("not byte reversal")
			}
		}
		for range 3 {
			u, err = ParseUID(u.String())
			if err != nil || u != uid {
				t.Fatal("non-idempotent UID")
			}
			r, err = ParseRUID(r.String())
			if err != nil || r != uid.RUID() {
				t.Fatal("non-idempotent rUID")
			}
		}
	})
}

// Fixed artificial bytes exercise every position, not a physical household tag.
func TestAuditPhysicalCodecMutations(t *testing.T) {
	canonical := "AB:CD:EF:01:23:45:67:89"
	uid, err := ParseUID(strings.ToLower(canonical))
	if err != nil || uid.String() != canonical || uid.RUID().String() != "8967452301EFCDAB" {
		t.Fatal("codec normalization changed byte identity")
	}
	for i := range canonical {
		bad := []byte(canonical)
		bad[i] = 'G'
		if got, err := ParseUID(string(bad)); err != ErrMalformedUID || got != (UID{}) {
			t.Fatalf("accepted UID mutation at byte %d", i)
		}
	}
	for i := range 16 {
		bad := []byte(uid.RUID().String())
		bad[i] = ':'
		if got, err := ParseRUID(string(bad)); err != ErrMalformedRUID || got != (RUID{}) {
			t.Fatalf("accepted rUID mutation at byte %d", i)
		}
	}
	for i := range 8 {
		changed := uid.RUID().Bytes()
		changed[i] ^= 1
		if _, err := NewUIDPair(uid, RUIDFromBytes(changed)); err != ErrUIDRUIDMismatch {
			t.Fatalf("accepted non-reversed pair at byte %d", i)
		}
	}
	bytes := uid.Bytes()
	bytes[0] ^= 1
	if uid.String() != canonical {
		t.Fatal("caller mutated UID bytes")
	}
}
