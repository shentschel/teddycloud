// Package catalogimport parses sanitized offline artifacts into unreviewed
// candidates. It performs no persistence, identity allocation or external I/O.
package catalogimport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"reflect"
	"strconv"
)

// CatalogImportPayload rejects fabricated or mutated public report fields.
// Invalid representations remain in the complete safe artifact; the record
// snapshot is then a value-free rejection marker, never accepted evidence.
func (r Report) CatalogImportPayload(ctx context.Context) (domain.ImportPayload, error) {
	validated, err := Parse(ctx, r.ArtifactBytes())
	if err != nil || !reflect.DeepEqual(r, validated) {
		return domain.ImportPayload{}, ErrEnvelope
	}
	report, err := json.Marshal(validated.Records)
	if err != nil {
		return domain.ImportPayload{}, ErrEnvelope
	}
	records := make([]domain.ImportRecord, len(validated.Records))
	for i, record := range validated.Records {
		raw := record.SnapshotBytes()
		if len(raw) == 0 {
			raw = []byte(`{"disposition":"rejected","record_index":` + strconv.Itoa(i) + `}`)
		}
		snapshot, err := domain.NewRecordSnapshot(raw)
		if err != nil {
			return domain.ImportPayload{}, ErrEnvelope
		}
		records[i] = domain.ImportRecord{Locator: "record:" + strconv.Itoa(i), Snapshot: snapshot}
	}
	return domain.NewImportPayload(r.ArtifactBytes(), report, records)
}

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
