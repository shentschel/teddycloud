package tag

import (
	"strings"
	"testing"
)

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
