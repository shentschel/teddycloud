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
	"unicode/utf8"
)

// CatalogImportPayload rejects fabricated or mutated public report fields.
// Invalid representations remain in the complete safe artifact; the record
// snapshot is then a value-free rejection marker, never accepted evidence.
func (r Report) CatalogImportPayload(ctx context.Context) (domain.ImportPayload, error) {
	validated, err := Parse(ctx, r.ArtifactBytes())
	if err != nil || !reflect.DeepEqual(r, validated) {
		return domain.ImportPayload{}, ErrEnvelope
	}
	// Admit the exact JSON size before Marshal allocates its complete buffer.
	// Encoder.Encode also buffers a complete value, so a limited writer would
	// check too late. This walks only our fixed report types, without copies.
	var encodedBytes uint64
	if err := reportJSONSize(ctx, reflect.ValueOf(validated.Records), &encodedBytes); err != nil {
		return domain.ImportPayload{}, err
	}
	report, err := json.Marshal(validated.Records)
	if err != nil {
		return domain.ImportPayload{}, ErrEnvelope
	}
	if uint64(len(report)) != encodedBytes {
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

// reportJSONSize mirrors encoding/json for the report's exported, untagged
// structs, slices, strings and integers. New field kinds fail closed until this
// admission walk is extended alongside the encoder. No custom marshalers exist
// in these fixed types.
func reportJSONSize(ctx context.Context, v reflect.Value, size *uint64) error {
	if ctx.Err() != nil {
		return ErrCanceled
	}
	add := func(n uint64) error {
		if err := domain.CheckLimit(domain.RetainedBytes, *size, n); err != nil {
			return err
		}
		*size += n
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		n := uint64(2) // quotes
		for i := 0; i < len(s); {
			b := s[i]
			if b < utf8.RuneSelf {
				switch b {
				case '"', '\\', '\b', '\f', '\n', '\r', '\t':
					n += 2
				case '<', '>', '&':
					n += 6
				default:
					if b < 0x20 {
						n += 6
					} else {
						n++
					}
				}
				i++
			} else {
				r, width := utf8.DecodeRuneInString(s[i:])
				if r == utf8.RuneError && width == 1 || r == '\u2028' || r == '\u2029' {
					n += 6
				} else {
					n += uint64(width)
				}
				i += width
			}
		}
		return add(n)
	case reflect.Int:
		return add(uint64(len(strconv.FormatInt(v.Int(), 10))))
	case reflect.Uint64:
		return add(uint64(len(strconv.FormatUint(v.Uint(), 10))))
	case reflect.Slice:
		if v.IsNil() {
			return add(4) // null
		}
		if err := add(2); err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				if err := add(1); err != nil {
					return err
				}
			}
			if err := reportJSONSize(ctx, v.Index(i), size); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		if err := add(2); err != nil {
			return err
		}
		fields := 0
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if field.Tag.Get("json") != "" || field.Anonymous {
				return ErrEnvelope
			}
			if fields > 0 {
				if err := add(1); err != nil {
					return err
				}
			}
			fields++
			if err := reportJSONSize(ctx, reflect.ValueOf(field.Name), size); err != nil {
				return err
			}
			if err := add(1); err != nil { // colon
				return err
			}
			if err := reportJSONSize(ctx, v.Field(i), size); err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrEnvelope
	}
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
