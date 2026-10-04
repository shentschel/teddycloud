package content

import (
	"strings"
	"testing"
)

func TestBlobIDCanonical(t *testing.T) {
	text := "sha256:" + strings.Repeat("01", 32)
	id, err := ParseBlobID(text)
	if err != nil || id.String() != text || id.Algorithm() != 1 || id.IsZero() {
		t.Fatal(id, err)
	}
	if NewBlobID(id.Digest()) != id {
		t.Fatal("raw/text inequality")
	}
	for _, s := range []string{"", text + "0", text[:70], strings.ToUpper(text), "sha1:" + strings.Repeat("0", 40), "sha256:" + strings.Repeat("g", 64), " " + text, text[:70] + "/", text[:70] + "\x00", "sha256:" + strings.Repeat("%0", 32)} {
		if _, err := ParseBlobID(s); err != ErrInvalidBlobID {
			t.Fatalf("accepted %q", s)
		}
	}
	if !(BlobID{}).IsZero() || (BlobID{}).String() != "" || (BlobID{}).Algorithm() != 0 || NewBlobID([32]byte{}).IsZero() {
		t.Fatal("zero identity")
	}
}
