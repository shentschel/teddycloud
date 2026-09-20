package tag

import (
	"errors"
	"strings"
	"testing"
)

func TestUIDRUIDCanonicalRoundTrip(t *testing.T) {
	uid, err := ParseUID("11:22:33:44:55:66:77:88")
	if err != nil {
		t.Fatal(err)
	}
	if got := uid.RUID().String(); got != "8877665544332211" {
		t.Fatalf("unexpected rUID: %s", got)
	}
	if got := uid.RUID().UID(); got != uid {
		t.Fatal("UID -> rUID -> UID did not round-trip")
	}

	ruid, err := ParseRUID("8877665544332211")
	if err != nil {
		t.Fatal(err)
	}
	if got := ruid.UID().String(); got != "11:22:33:44:55:66:77:88" {
		t.Fatalf("unexpected UID: %s", got)
	}
	if _, err := NewUIDPair(uid, ruid); err != nil {
		t.Fatalf("matching pair rejected: %v", err)
	}
	lowerCaseRUID, err := ParseRUID("8967452301efcdab")
	if err != nil || lowerCaseRUID.UID().String() != "AB:CD:EF:01:23:45:67:89" {
		t.Fatalf("lower-case codec was not canonicalized: %v", err)
	}
}

func TestPhysicalIdentifierRejectsMalformedAndOversizedInput(t *testing.T) {
	uidCases := []string{"", "E00403501F9921B2", "E0:04:03:50:1F:99:21", "GG:04:03:50:1F:99:21:B2", strings.Repeat("A", 4096)}
	for _, text := range uidCases {
		if _, err := ParseUID(text); !errors.Is(err, ErrMalformedUID) {
			t.Fatalf("UID input should be rejected, got %v", err)
		}
	}
	ruidCases := []string{"", "B2:21:99:1F:50:03:04:E0", "B221991F500304E", "X221991F500304E0", strings.Repeat("A", 4096)}
	for _, text := range ruidCases {
		if _, err := ParseRUID(text); !errors.Is(err, ErrMalformedRUID) {
			t.Fatalf("rUID input should be rejected, got %v", err)
		}
	}
}

func TestUIDPairRejectsMismatchWithoutLeakingIdentifiers(t *testing.T) {
	uid, _ := ParseUID("11:22:33:44:55:66:77:88")
	wrong, _ := ParseRUID("0000000000000000")
	_, err := NewUIDPair(uid, wrong)
	if !errors.Is(err, ErrUIDRUIDMismatch) {
		t.Fatalf("want mismatch, got %v", err)
	}
	if strings.Contains(err.Error(), uid.String()) || strings.Contains(err.Error(), wrong.String()) {
		t.Fatal("physical identifier leaked through error")
	}
}

func FuzzUIDByteOrderRoundTrip(f *testing.F) {
	f.Add([]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) < physicalIDBytes {
			return
		}
		var raw [physicalIDBytes]byte
		copy(raw[:], input[:physicalIDBytes])
		uid := UIDFromBytes(raw)
		if got := uid.RUID().UID(); got != uid {
			t.Fatal("byte-order round-trip failed")
		}
		parsedUID, err := ParseUID(uid.String())
		if err != nil || parsedUID != uid {
			t.Fatalf("canonical UID parse failed: %v", err)
		}
		parsedRUID, err := ParseRUID(uid.RUID().String())
		if err != nil || parsedRUID != uid.RUID() {
			t.Fatalf("canonical rUID parse failed: %v", err)
		}
	})
}
