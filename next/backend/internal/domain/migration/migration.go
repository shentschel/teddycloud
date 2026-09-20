// Package migration is a bounded, read-only in-memory fixture prototype.
// It allocates no identities, infers no assignment/recency, and performs no I/O.
// Callers supply independently allocated IDs and provenance. Raw records survive
// every per-record outcome. Envelope errors return no partial report and leave
// the caller's complete input untouched. This is not a production legacy reader.
package migration

import (
	"errors"
	"sort"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/set"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

const (
	MaxRecords = 256
	MaxItems   = 1024
)

var (
	ErrLimit  = errors.New("migration input limit exceeded")
	ErrSource = errors.New("migration requires unique source references")
)

type Classification uint8

const (
	Unclassified Classification = iota
	Original
	CustomCard
)

type Availability uint8

const (
	Unknown Availability = iota
	Missing
	Present
)

type Status uint8

const (
	Accepted Status = iota
	ReviewRequired
	Ambiguous
	Rejected
)

// Pair is explicit source pairing, never a zip of parallel arrays.
type Pair struct{ AudioID, Hash string }
type Flags struct{ Valid, Exists, Claimed, CloudAuth, NoCloud bool }

// Record is an internal sanitized fixture seam. Parallel arrays are retained
// only here as legacy evidence. SetID/Members must be explicit source relations;
// tracks are not interpreted as membership. Source locates even a missing TAF
// or unavailable upstream source. Neither availability proves ownership.
type Record struct {
	Source                   evidence.Source
	TagID                    identity.TagID
	ContentID                identity.ContentID
	VersionID                identity.ContentVersionID
	SetID                    set.ID
	UID, RUID                string
	Title, Model, Article    string
	Classification           Classification
	TAF, Upstream            Availability
	Flags                    Flags
	AudioIDs, Hashes, Tracks []string
	Pairs                    []Pair
	Members                  []set.Member
	Observations             []evidence.Observation
}

// Issue has a fixed reason code and a source reference, never input text.
type Issue struct {
	Status Status
	Reason string
	Source evidence.Source
}
type Result struct {
	raw      Record
	status   Status
	issues   []Issue
	content  catalog.Content
	version  catalog.ContentVersion
	grouping set.Set
}

func (r Result) Raw() Record                             { return clone(r.raw) }
func (r Result) Status() Status                          { return r.status }
func (r Result) Issues() []Issue                         { return append([]Issue(nil), r.issues...) }
func (r Result) Protected() bool                         { return r.raw.Classification == CustomCard }
func (r Result) Content() (catalog.Content, bool)        { return r.content, !r.content.ID().IsZero() }
func (r Result) Version() (catalog.ContentVersion, bool) { return r.version, !r.version.ID().IsZero() }
func (r Result) Set() (set.Set, bool)                    { return r.grouping, !r.grouping.ID().IsZero() }

func (r *Result) issue(status Status, reason string) {
	r.issues = append(r.issues, Issue{Status: status, Reason: reason, Source: r.raw.Source})
	if status > r.status {
		r.status = status
	}
}

// Run returns one result per source, sorted by source/name/revision/record.
// Candidates are published only for Accepted results. Missing/ambiguous inputs
// retain raw evidence and fixed reasons, without creating playable revisions.
func Run(records []Record) ([]Result, error) {
	if len(records) > MaxRecords {
		return nil, ErrLimit
	}
	sources := make(map[evidence.Source]bool)
	physical := make(map[tag.UID]int)
	logical := make(map[identity.TagID]int)
	contents := make(map[identity.ContentID]int)
	versions := make(map[identity.ContentVersionID]int)
	sets := make(map[set.ID]int)
	for _, r := range records {
		if !bounded(r) {
			return nil, ErrLimit
		}
		if r.Source.IsZero() || sources[r.Source] {
			return nil, ErrSource
		}
		sources[r.Source] = true
		if uid, ok := physicalUID(r); ok {
			physical[uid]++
		}
		if !r.TagID.IsZero() {
			logical[r.TagID]++
		}
		if !r.ContentID.IsZero() {
			contents[r.ContentID]++
		}
		if !r.VersionID.IsZero() {
			versions[r.VersionID]++
		}
		if !r.SetID.IsZero() {
			sets[r.SetID]++
		}
	}
	results := make([]Result, 0, len(records))
	for _, input := range records {
		r := Result{raw: clone(input)}
		raw := r.raw
		if raw.Classification > CustomCard || raw.TAF > Present || raw.Upstream > Present {
			r.issue(Rejected, "invalid-state")
		}
		if raw.TagID.IsZero() != (raw.UID == "" && raw.RUID == "") {
			r.issue(Rejected, "tag-reference-missing")
		}
		if raw.UID != "" || raw.RUID != "" {
			uid, ok := physicalUID(raw)
			if !ok {
				r.issue(Rejected, "invalid-physical-reference")
			} else if physical[uid] > 1 {
				r.issue(Ambiguous, "duplicate-physical-tag")
			}
		}
		if !raw.TagID.IsZero() && logical[raw.TagID] > 1 {
			r.issue(Ambiguous, "duplicate-tag-reference")
		}
		if (!raw.ContentID.IsZero() && contents[raw.ContentID] > 1) ||
			(!raw.VersionID.IsZero() && versions[raw.VersionID] > 1) ||
			(!raw.SetID.IsZero() && sets[raw.SetID] > 1) {
			r.issue(Ambiguous, "reused-aggregate-reference")
		}
		if raw.Classification == Unclassified && !raw.TagID.IsZero() {
			r.issue(ReviewRequired, "classification-unreviewed")
		}
		if raw.TAF != Present {
			r.issue(ReviewRequired, "taf-unavailable")
		}
		if raw.Upstream != Present {
			r.issue(ReviewRequired, "source-unavailable")
		}
		if !evidence.ValidText(raw.Title, evidence.MaxText) {
			r.issue(Rejected, "invalid-title")
		}
		var model *catalog.ModelNumber
		var article *catalog.ArticleNumber
		if raw.Model != "" {
			value, err := catalog.ParseModelNumber(raw.Model)
			if err != nil {
				r.issue(Rejected, "invalid-model")
			} else {
				model = &value
			}
		}
		if raw.Article != "" {
			value, err := catalog.ParseArticleNumber(raw.Article)
			if err != nil {
				r.issue(Rejected, "invalid-article")
			} else {
				article = &value
			}
		}
		for _, track := range raw.Tracks {
			if !evidence.ValidText(track, evidence.MaxText) {
				r.issue(Rejected, "invalid-track")
				break
			}
		}
		observations, err := evidence.Canonical(raw.Observations)
		if err != nil {
			r.issue(Rejected, "invalid-observation-history")
		} else {
			values := make(map[[2]string]string)
			conflict := false
			for _, o := range observations {
				c := o.Claim()
				key := [2]string{c.Subject, c.Key}
				if value, ok := values[key]; ok && value != c.Value {
					conflict = true
				}
				values[key] = c.Value
			}
			if conflict {
				r.issue(Ambiguous, "conflicting-observations")
			}
		}
		if len(raw.AudioIDs) != 0 || len(raw.Hashes) != 0 {
			if len(raw.AudioIDs) != len(raw.Hashes) {
				r.issue(Rejected, "unequal-audio-hash-arrays")
			} else {
				r.issue(Ambiguous, "unproven-audio-hash-pairing")
			}
		}
		var fingerprint catalog.AudioFingerprint
		for _, pair := range raw.Pairs {
			audio, ea := catalog.ParseAudioID(pair.AudioID)
			hash, eh := catalog.ParseAudioHash(pair.Hash)
			if ea != nil || eh != nil {
				r.issue(Rejected, "invalid-audio-pair")
				continue
			}
			fingerprint, _ = catalog.NewAudioFingerprint(audio, hash)
		}
		if !raw.SetID.IsZero() {
			if !raw.ContentID.IsZero() || !raw.VersionID.IsZero() || len(raw.Pairs) > 0 {
				r.issue(Rejected, "mixed-set-content-record")
			}
			grouping, err := set.New(raw.SetID, raw.Members)
			if err != nil {
				r.issue(Rejected, "invalid-set-members")
			} else {
				r.grouping = grouping
			}
			for _, member := range raw.Members {
				if contents[member.ContentID] != 1 {
					r.issue(ReviewRequired, "unresolved-set-member")
					break
				}
			}
		} else {
			if len(raw.Members) > 0 {
				r.issue(Rejected, "set-reference-missing")
			}
			content, err := catalog.NewContent(raw.ContentID, catalog.NewContentFacts(raw.Title, catalog.NewProductIdentifiers(model, article)))
			if err != nil {
				r.issue(Rejected, "content-reference-missing")
			} else {
				r.content = content
			}
			if len(raw.Pairs) == 0 {
				r.issue(ReviewRequired, "audio-pair-missing")
			}
			if len(raw.Pairs) > 1 {
				r.issue(Ambiguous, "multiple-version-pairs")
			}
			if len(raw.Pairs) == 1 && !fingerprint.IsZero() {
				version, err := catalog.NewContentVersion(raw.VersionID, raw.ContentID, fingerprint, catalog.UnknownVersionOrderEvidence())
				if err != nil {
					r.issue(Rejected, "version-reference-missing")
				} else {
					r.version = version
				}
			}
		}
		if len(r.issues) == 0 {
			r.issue(Accepted, "explicit-source-record")
		}
		if r.status != Accepted {
			r.content = catalog.Content{}
			r.version = catalog.ContentVersion{}
			r.grouping = set.Set{}
		}
		results = append(results, r)
	}
	acceptedContent := make(map[identity.ContentID]bool)
	for _, result := range results {
		if result.status == Accepted && !result.content.ID().IsZero() {
			acceptedContent[result.content.ID()] = true
		}
	}
	for i := range results {
		if results[i].status != Accepted || results[i].grouping.ID().IsZero() {
			continue
		}
		for _, member := range results[i].grouping.Members() {
			if acceptedContent[member.ContentID] {
				continue
			}
			results[i].issue(ReviewRequired, "unaccepted-set-member")
			results[i].grouping = set.Set{}
			break
		}
	}
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i].raw.Source, results[j].raw.Source
		if a.Name() != b.Name() {
			return a.Name() < b.Name()
		}
		if a.Revision() != b.Revision() {
			return a.Revision() < b.Revision()
		}
		return a.Record() < b.Record()
	})
	return results, nil
}

func physicalUID(r Record) (tag.UID, bool) {
	var uid tag.UID
	if r.UID != "" {
		parsed, err := tag.ParseUID(r.UID)
		if err != nil {
			return tag.UID{}, false
		}
		uid = parsed
	}
	if r.RUID != "" {
		reverse, err := tag.ParseRUID(r.RUID)
		if err != nil || (r.UID != "" && reverse.UID() != uid) {
			return tag.UID{}, false
		}
		uid = reverse.UID()
	}
	return uid, r.UID != "" || r.RUID != ""
}
func bounded(r Record) bool {
	if len(r.AudioIDs) > MaxItems || len(r.Hashes) > MaxItems || len(r.Tracks) > MaxItems ||
		len(r.Pairs) > MaxItems || len(r.Members) > set.MaxMembers || len(r.Observations) > evidence.MaxObservations {
		return false
	}
	for _, s := range []string{r.UID, r.RUID, r.Title, r.Model, r.Article} {
		if len(s) > evidence.MaxText {
			return false
		}
	}
	for _, list := range [][]string{r.AudioIDs, r.Hashes, r.Tracks} {
		for _, s := range list {
			if len(s) > evidence.MaxText {
				return false
			}
		}
	}
	for _, p := range r.Pairs {
		if len(p.AudioID) > evidence.MaxText || len(p.Hash) > evidence.MaxText {
			return false
		}
	}
	return true
}
func clone(r Record) Record {
	r.AudioIDs = copySlice(r.AudioIDs)
	r.Hashes = copySlice(r.Hashes)
	r.Tracks = copySlice(r.Tracks)
	r.Pairs = copySlice(r.Pairs)
	r.Members = copySlice(r.Members)
	r.Observations = copySlice(r.Observations)
	return r
}
func copySlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	result := make([]T, len(s))
	copy(result, s)
	return result
}
