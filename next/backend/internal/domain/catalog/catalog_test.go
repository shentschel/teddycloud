package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const idPayloadA = "0123456789abcdefghjkmnpqrs"

func TestExternalIdentifiersAreTypedAndBounded(t *testing.T) {
	model, err := ParseModelNumber("01-0100")
	if err != nil || model.String() != "01-0100" {
		t.Fatalf("valid model rejected: %v", err)
	}
	article, err := ParseArticleNumber("11000289")
	if err != nil || article.String() != "11000289" {
		t.Fatalf("valid article rejected: %v", err)
	}
	product := NewProductIdentifiers(&model, &article)
	if got, ok := product.Model(); !ok || got != model {
		t.Fatal("model observation was not retained")
	}
	if got, ok := product.Article(); !ok || got != article {
		t.Fatal("article observation was not retained")
	}

	for _, text := range []string{"", "has spaces", "slash/value", strings.Repeat("x", 65), strings.Repeat("x", 4096)} {
		if _, err := ParseModelNumber(text); !errors.Is(err, ErrInvalidProductIdentifier) {
			t.Fatalf("malformed model should fail, got %v", err)
		}
	}
	for _, text := range []string{"", "0", "01", "-1", "1x", strings.Repeat("9", 4096)} {
		if _, err := ParseAudioID(text); !errors.Is(err, ErrInvalidAudioID) {
			t.Fatalf("malformed audio ID should fail, got %v", err)
		}
	}
	for _, text := range []string{"", strings.Repeat("a", 39), strings.Repeat("a", 41), strings.Repeat("g", 40), strings.Repeat("a", 4096)} {
		if _, err := ParseAudioHash(text); !errors.Is(err, ErrInvalidAudioHash) {
			t.Fatalf("malformed audio hash should fail, got %v", err)
		}
	}
}

func TestAggregateIdentityDoesNotDependOnMetadata(t *testing.T) {
	id := mustContentID(t, "cnt_"+idPayloadA)
	otherID := mustContentID(t, "cnt_1123456789abcdefghjkmnpqrs")
	modelA, _ := ParseModelNumber("01-0100")
	modelB, _ := ParseModelNumber("11000289")
	contentA, err := NewContent(id, NewContentFacts("First title", NewProductIdentifiers(&modelA, nil)))
	if err != nil {
		t.Fatal(err)
	}
	contentB, err := NewContent(id, NewContentFacts("Renamed title", NewProductIdentifiers(&modelB, nil)))
	if err != nil {
		t.Fatal(err)
	}
	contentC, err := NewContent(otherID, contentA.Facts())
	if err != nil {
		t.Fatal(err)
	}
	if !contentA.SameIdentity(contentB) {
		t.Fatal("metadata change altered aggregate identity")
	}
	if contentA.SameIdentity(contentC) {
		t.Fatal("equal metadata merged different aggregate identities")
	}
}

func TestResolverReturnsNoMatchUniqueAndAmbiguousWithoutFallback(t *testing.T) {
	contentID := mustContentID(t, "cnt_"+idPayloadA)
	hashA := mustAudioHash(t, strings.Repeat("a", 40))
	hashB := mustAudioHash(t, strings.Repeat("b", 40))
	audio100 := mustAudioID(t, 100)
	audio200 := mustAudioID(t, 200)
	fingerprint100A := mustFingerprint(t, audio100, hashA)
	fingerprint100B := mustFingerprint(t, audio100, hashB)
	fingerprint200A := mustFingerprint(t, audio200, hashA)
	crossed := mustFingerprint(t, audio200, hashB)

	versionA := mustVersion(t, "ver_"+idPayloadA, contentID, fingerprint100A, UnknownVersionOrderEvidence())
	versionB := mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", contentID, fingerprint100B, UnknownVersionOrderEvidence())
	versionC := mustVersion(t, "ver_2123456789abcdefghjkmnpqrs", contentID, fingerprint200A, UnknownVersionOrderEvidence())
	index, err := NewVersionIndex([]ContentVersion{versionC, versionB, versionA})
	if err != nil {
		t.Fatal(err)
	}

	if got := index.LookupExact(fingerprint100A); got.Kind() != UniqueMatch {
		t.Fatalf("exact pair should be unique, got %v", got.Kind())
	}
	if got := index.LookupAudioID(audio100); got.Kind() != AmbiguousMatch || len(got.Candidates()) != 2 {
		t.Fatalf("duplicate audio ID should be ambiguous, got %v", got.Kind())
	}
	if _, ok := index.LookupAudioID(audio100).Unique(); ok {
		t.Fatal("ambiguous lookup exposed a first-match fallback")
	}
	if got := index.LookupExact(crossed); got.Kind() != NoMatch {
		t.Fatalf("crossed pair should not match, got %v", got.Kind())
	}
	unknownAudio := mustAudioID(t, 300)
	if got := index.LookupAudioID(unknownAudio); got.Kind() != NoMatch {
		t.Fatalf("unknown audio ID should not match, got %v", got.Kind())
	}
}

func TestResolverReportsExactPairCollisionDeterministically(t *testing.T) {
	contentID := mustContentID(t, "cnt_"+idPayloadA)
	fingerprint := mustFingerprint(t, mustAudioID(t, 100), mustAudioHash(t, strings.Repeat("a", 40)))
	versionA := mustVersion(t, "ver_"+idPayloadA, contentID, fingerprint, UnknownVersionOrderEvidence())
	versionB := mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", contentID, fingerprint, UnknownVersionOrderEvidence())
	index, err := NewVersionIndex([]ContentVersion{versionB, versionA, versionA})
	if err != nil {
		t.Fatal(err)
	}
	result := index.LookupExact(fingerprint)
	if result.Kind() != AmbiguousMatch {
		t.Fatalf("exact collision should be ambiguous, got %v", result.Kind())
	}
	candidates := result.Candidates()
	if len(candidates) != 2 || candidates[0].ID() != versionA.ID() || candidates[1].ID() != versionB.ID() {
		t.Fatal("collision result was not de-duplicated and deterministically ordered")
	}
	mutated := result.Candidates()
	mutated[0] = ContentVersion{}
	if result.Candidates()[0].ID().IsZero() {
		t.Fatal("caller mutated resolver result")
	}
}

func TestVersionIndexRejectsOneIdentityWithConflictingFacts(t *testing.T) {
	contentID := mustContentID(t, "cnt_"+idPayloadA)
	versionID := "ver_" + idPayloadA
	versionA := mustVersion(t, versionID, contentID,
		mustFingerprint(t, mustAudioID(t, 100), mustAudioHash(t, strings.Repeat("a", 40))),
		UnknownVersionOrderEvidence())
	versionB := mustVersion(t, versionID, contentID,
		mustFingerprint(t, mustAudioID(t, 101), mustAudioHash(t, strings.Repeat("b", 40))),
		UnknownVersionOrderEvidence())
	if _, err := NewVersionIndex([]ContentVersion{versionA, versionB}); !errors.Is(err, ErrVersionIdentityConflict) {
		t.Fatalf("want version identity conflict, got %v", err)
	}
}

func TestVersionIndexRejectsZeroVersion(t *testing.T) {
	if _, err := NewVersionIndex([]ContentVersion{{}}); !errors.Is(err, ErrZeroContentVersionID) {
		t.Fatalf("want zero version identity error, got %v", err)
	}
}

func TestVersionOrderingRequiresExplicitCompatibleEvidence(t *testing.T) {
	contentID := mustContentID(t, "cnt_"+idPayloadA)
	fingerprintA := mustFingerprint(t, mustAudioID(t, 9999), mustAudioHash(t, strings.Repeat("a", 40)))
	fingerprintB := mustFingerprint(t, mustAudioID(t, 1), mustAudioHash(t, strings.Repeat("b", 40)))
	unknownA := mustVersion(t, "ver_"+idPayloadA, contentID, fingerprintA, UnknownVersionOrderEvidence())
	unknownB := mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", contentID, fingerprintB, UnknownVersionOrderEvidence())
	if got := CompareVersions(unknownA, unknownB); got != VersionOrderingUnknown {
		t.Fatalf("numeric audio ID implied ordering: %v", got)
	}

	first := mustOrder(t, "provider/release-sequence", 1)
	second := mustOrder(t, "provider/release-sequence", 2)
	otherSource := mustOrder(t, "other-provider/revision", 2)
	samePosition := mustOrder(t, "provider/release-sequence", 1)
	orderedA := mustVersion(t, "ver_"+idPayloadA, contentID, fingerprintA, first)
	orderedB := mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", contentID, fingerprintB, second)
	conflictingSource := mustVersion(t, "ver_2123456789abcdefghjkmnpqrs", contentID, fingerprintB, otherSource)
	conflictingPosition := mustVersion(t, "ver_3123456789abcdefghjkmnpqrs", contentID, fingerprintB, samePosition)

	if got := CompareVersions(orderedA, orderedB); got != VersionOrderingBefore {
		t.Fatalf("want before, got %v", got)
	}
	if got := CompareVersions(orderedB, orderedA); got != VersionOrderingAfter {
		t.Fatalf("want after, got %v", got)
	}
	if got := CompareVersions(orderedA, orderedA); got != VersionOrderingSame {
		t.Fatalf("same identity should compare same, got %v", got)
	}
	if got := CompareVersions(orderedA, conflictingSource); got != VersionOrderingConflict {
		t.Fatalf("different evidence namespaces should conflict, got %v", got)
	}
	if got := CompareVersions(orderedA, conflictingPosition); got != VersionOrderingConflict {
		t.Fatalf("two identities at one accepted position should conflict, got %v", got)
	}
}

func FuzzAudioHashCanonicalization(f *testing.F) {
	f.Add(strings.Repeat("A", 40))
	f.Add(strings.Repeat("g", 40))
	f.Add(strings.Repeat("x", 4096))
	f.Fuzz(func(t *testing.T, text string) {
		hash, err := ParseAudioHash(text)
		if err == nil {
			if len(hash.String()) != audioHashLength || hash.String() != strings.ToLower(hash.String()) {
				t.Fatal("successful hash parse is not canonical")
			}
		}
	})
}

func mustContentID(t *testing.T, text string) identity.ContentID {
	t.Helper()
	id, err := identity.ParseContentID(text)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustAudioID(t *testing.T, value uint64) AudioID {
	t.Helper()
	id, err := NewAudioID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustAudioHash(t *testing.T, text string) AudioHash {
	t.Helper()
	hash, err := ParseAudioHash(text)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func mustFingerprint(t *testing.T, audioID AudioID, hash AudioHash) AudioFingerprint {
	t.Helper()
	fingerprint, err := NewAudioFingerprint(audioID, hash)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func mustVersion(
	t *testing.T,
	versionText string,
	contentID identity.ContentID,
	fingerprint AudioFingerprint,
	order VersionOrderEvidence,
) ContentVersion {
	t.Helper()
	versionID, err := identity.ParseContentVersionID(versionText)
	if err != nil {
		t.Fatal(err)
	}
	version, err := NewContentVersion(versionID, contentID, fingerprint, order)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func mustOrder(t *testing.T, namespace string, position uint64) VersionOrderEvidence {
	t.Helper()
	order, err := NewVersionOrderEvidence(namespace, position)
	if err != nil {
		t.Fatal(err)
	}
	return order
}
