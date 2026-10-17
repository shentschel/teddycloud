// Package catalogimport parses sanitized offline artifacts into unreviewed
// candidates. It performs no persistence, identity allocation or external I/O.
package catalogimport

import "crypto/sha256"

type Disposition string

const (
	AcceptedForStaging Disposition = "accepted-for-staging"
	Rejected           Disposition = "rejected"
	Unsupported        Disposition = "unsupported"
	Ambiguous          Disposition = "ambiguous"
)

// Issue contains a source coordinate and a fixed reason, never an input value.
type Issue struct {
	Path   string
	Reason string
}

// Pair is an assertion, not an accepted version or an identity. Wider AudioIDs
// remain visible but cannot be admitted to the current uint32 storage boundary.
type Pair struct {
	AudioID uint64
	SHA1    string
}

type Record struct {
	Index          int
	Locator        string
	Kind           string // asserted public/custom candidate class, not a capability
	Publisher      string // asserted text; no source registration or authority
	Title          string
	Model          string
	Article        string
	Pairs          []Pair
	LegacyAudioIDs []uint64
	LegacySHA1s    []string
	Disposition    Disposition
	Issues         []Issue
	raw            string
}

func (r Record) SnapshotBytes() []byte { return []byte(r.raw) }

// Report is deliberately not a success boolean: every record has a disposition.
// Unsupported/ambiguous records are semantically incomplete even though their
// safe bytes remain available. The trusted caller must sanitize prose first.
// B1.2 must bind a core source, budget staging and persist artifact/report
// atomically; none of those capabilities are supplied by this report.
type Report struct {
	Records []Record
	Digest  [sha256.Size]byte
	raw     string
}

func (r Report) ArtifactBytes() []byte { return []byte(r.raw) }

func (r *Record) issue(disposition Disposition, path, reason string) {
	r.Issues = append(r.Issues, Issue{Path: path, Reason: reason})
	priority := map[Disposition]int{AcceptedForStaging: 0, Unsupported: 1, Ambiguous: 2, Rejected: 3}
	if priority[disposition] > priority[r.Disposition] {
		r.Disposition = disposition
	}
}
