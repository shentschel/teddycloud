package catalog

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCatalogBaselineSnapshotIdentityAndBounds(t *testing.T) {
	id := mustContentID(t, "cnt_"+idPayloadA)
	content, _ := NewContent(id, NewContentFacts("mechanical", ProductIdentifiers{}))
	pair := mustFingerprint(t, mustAudioID(t, 123), mustAudioHash(t, strings.Repeat("a", 40)))
	a := mustVersion(t, "ver_"+idPayloadA, id, pair, UnknownVersionOrderEvidence())
	b := mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", id, pair, UnknownVersionOrderEvidence())
	x, err := NewCatalogBaseline(content, []ContentVersion{b, a})
	if err != nil {
		t.Fatal(err)
	}
	y, err := NewCatalogBaseline(content, []ContentVersion{a, b})
	if err != nil || !bytes.Equal(x.Bytes(), y.Bytes()) || x.SHA256() != y.SHA256() {
		t.Fatal("unstable snapshot", err)
	}
	raw := x.Bytes()
	raw[0] ^= 1
	versions := x.Versions()
	versions[0] = ContentVersion{}
	if x.SHA256() != y.SHA256() {
		t.Fatal("mutable snapshot")
	}
	if _, err = NewCatalogBaseline(content, []ContentVersion{a, a}); !errors.Is(err, ErrVersionIdentityConflict) {
		t.Fatal(err)
	}
	other, _ := NewContent(mustContentID(t, "cnt_1123456789abcdefghjkmnpqrs"), content.Facts())
	if _, err = NewCatalogBaseline(other, []ContentVersion{a}); !errors.Is(err, ErrCatalogSubjectMismatch) {
		t.Fatal(err)
	}
	large := make([]ContentVersion, RetainedVersions.Maximum()+1)
	if _, err = NewCatalogBaseline(content, large); err == nil {
		t.Fatal("version bound")
	}
	tooLong, _ := NewContent(id, NewContentFacts(strings.Repeat("x", int(TextBytes.Maximum()+1)), ProductIdentifiers{}))
	if _, err = NewCatalogBaseline(tooLong, nil); err == nil {
		t.Fatal("text bound")
	}
}
