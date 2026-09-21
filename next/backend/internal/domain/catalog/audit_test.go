package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestAuditPairMatrixAcrossContentIdentities(t *testing.T) {
	a := mustContentID(t, "cnt_"+idPayloadA)
	b := mustContentID(t, "cnt_1123456789abcdefghjkmnpqrs")
	pair := func(audio uint64, hash string) AudioFingerprint {
		return mustFingerprint(t, mustAudioID(t, audio), mustAudioHash(t, strings.Repeat(hash, 40)))
	}
	versions := []ContentVersion{
		mustVersion(t, "ver_"+idPayloadA, a, pair(1, "a"), UnknownVersionOrderEvidence()),
		mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", b, pair(1, "A"), UnknownVersionOrderEvidence()),
		mustVersion(t, "ver_2123456789abcdefghjkmnpqrs", b, pair(2, "b"), UnknownVersionOrderEvidence()),
	}
	for _, input := range [][]ContentVersion{versions, {versions[2], versions[1], versions[0], versions[0]}} {
		index, err := NewVersionIndex(input)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			audio uint64
			hash  string
			kind  MatchKind
			count int
		}{{1, "a", AmbiguousMatch, 2}, {2, "b", UniqueMatch, 1}, {1, "b", NoMatch, 0}, {2, "a", NoMatch, 0}, {3, "c", NoMatch, 0}} {
			result := index.LookupExact(pair(tc.audio, tc.hash))
			if result.Kind() != tc.kind || len(result.Candidates()) != tc.count {
				t.Fatal("exact pair matrix violated")
			}
			if _, ok := result.Unique(); ok != (tc.kind == UniqueMatch) {
				t.Fatal("absence/ambiguity exposed a fallback")
			}
		}
		candidates := index.LookupExact(pair(1, "a")).Candidates()
		if !reflect.DeepEqual(candidates, versions[:2]) {
			t.Fatal("same pair merged editorial identities or changed deterministic order")
		}
	}
}

func TestAuditImmutableVersionConflicts(t *testing.T) {
	content := mustContentID(t, "cnt_"+idPayloadA)
	fingerprint := mustFingerprint(t, mustAudioID(t, 1), mustAudioHash(t, strings.Repeat("a", 40)))
	base := mustVersion(t, "ver_"+idPayloadA, content, fingerprint, mustOrder(t, "fixture", 1))
	for _, changed := range []ContentVersion{
		mustVersion(t, base.ID().String(), mustContentID(t, "cnt_1123456789abcdefghjkmnpqrs"), fingerprint, base.OrderEvidence()),
		mustVersion(t, base.ID().String(), content, fingerprint, mustOrder(t, "fixture", 2)),
		mustVersion(t, base.ID().String(), content, fingerprint, UnknownVersionOrderEvidence()),
	} {
		for _, entries := range [][]ContentVersion{{base, changed}, {changed, base}} {
			if _, err := NewVersionIndex(entries); err != ErrVersionIdentityConflict {
				t.Fatal("same-ID ownership/order conflict overwritten")
			}
			if CompareVersions(entries[0], entries[1]) != VersionOrderingConflict {
				t.Fatal("same-ID conflicting immutable facts reported as the same version")
			}
		}
	}
}

func TestAuditUnknownOrderingCannotUseIndexOrder(t *testing.T) {
	content := mustContentID(t, "cnt_"+idPayloadA)
	pair := mustFingerprint(t, mustAudioID(t, 1), mustAudioHash(t, strings.Repeat("a", 40)))
	a := mustVersion(t, "ver_"+idPayloadA, content, pair, UnknownVersionOrderEvidence())
	b := mustVersion(t, "ver_1123456789abcdefghjkmnpqrs", content, pair, mustOrder(t, "fixture", 999))
	for _, args := range [][2]ContentVersion{{a, b}, {b, a}} {
		if CompareVersions(args[0], args[1]) != VersionOrderingUnknown {
			t.Fatal("one-sided evidence inferred recency")
		}
	}
}
