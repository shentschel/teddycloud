package catalog

import "sort"

// MatchKind distinguishes absence, one proven candidate, and ambiguity.
type MatchKind uint8

const (
	NoMatch MatchKind = iota
	UniqueMatch
	AmbiguousMatch
)

// MatchResult is deterministic: candidates are sorted by opaque version ID.
// A caller can obtain a value from Unique only when Kind is UniqueMatch.
type MatchResult struct {
	kind       MatchKind
	candidates []ContentVersion
}

// VersionIndex is an immutable read index over a copied revision set.
type VersionIndex struct {
	byFingerprint map[AudioFingerprint][]ContentVersion
	byAudioID     map[AudioID][]ContentVersion
}

func NewVersionIndex(versions []ContentVersion) (VersionIndex, error) {
	byID := make(map[string]ContentVersion, len(versions))
	for _, version := range versions {
		if version.ID().IsZero() {
			return VersionIndex{}, ErrZeroContentVersionID
		}
		if version.ContentID().IsZero() {
			return VersionIndex{}, ErrZeroContentID
		}
		if version.Fingerprint().IsZero() {
			return VersionIndex{}, ErrInvalidFingerprint
		}
		key := version.ID().String()
		if existing, found := byID[key]; found {
			if existing != version {
				return VersionIndex{}, ErrVersionIdentityConflict
			}
			continue
		}
		byID[key] = version
	}

	index := VersionIndex{
		byFingerprint: make(map[AudioFingerprint][]ContentVersion),
		byAudioID:     make(map[AudioID][]ContentVersion),
	}
	for _, version := range byID {
		fingerprint := version.Fingerprint()
		index.byFingerprint[fingerprint] = append(index.byFingerprint[fingerprint], version)
		index.byAudioID[fingerprint.AudioID()] = append(index.byAudioID[fingerprint.AudioID()], version)
	}
	for key := range index.byFingerprint {
		sortVersions(index.byFingerprint[key])
	}
	for key := range index.byAudioID {
		sortVersions(index.byAudioID[key])
	}
	return index, nil
}

func (index VersionIndex) LookupExact(fingerprint AudioFingerprint) MatchResult {
	return newMatchResult(index.byFingerprint[fingerprint])
}

func (index VersionIndex) LookupAudioID(audioID AudioID) MatchResult {
	return newMatchResult(index.byAudioID[audioID])
}

func newMatchResult(candidates []ContentVersion) MatchResult {
	copyOfCandidates := append([]ContentVersion(nil), candidates...)
	switch len(copyOfCandidates) {
	case 0:
		return MatchResult{kind: NoMatch}
	case 1:
		return MatchResult{kind: UniqueMatch, candidates: copyOfCandidates}
	default:
		return MatchResult{kind: AmbiguousMatch, candidates: copyOfCandidates}
	}
}

func sortVersions(versions []ContentVersion) {
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].ID().String() < versions[j].ID().String()
	})
}

func (result MatchResult) Kind() MatchKind { return result.kind }

func (result MatchResult) Candidates() []ContentVersion {
	return append([]ContentVersion(nil), result.candidates...)
}

func (result MatchResult) Unique() (ContentVersion, bool) {
	if result.kind != UniqueMatch {
		return ContentVersion{}, false
	}
	return result.candidates[0], true
}
