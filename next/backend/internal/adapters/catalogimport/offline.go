package catalogimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

const EnvelopeFormat = "catalog-offline-v1"

var (
	ErrEnvelope  = errors.New("invalid or excessive offline catalog envelope")
	ErrSensitive = errors.New("sensitive offline catalog batch rejected")
	ErrCanceled  = errors.New("offline catalog parsing canceled")
)

// SensitiveBatchError exposes only the record coordinate, never a field name,
// value, snapshot or digest. RecordIndex is zero-based; -1 denotes the envelope.
type SensitiveBatchError struct{ RecordIndex int }

func (SensitiveBatchError) Error() string { return ErrSensitive.Error() }
func (SensitiveBatchError) Unwrap() error { return ErrSensitive }

// Parse accepts exactly {"format":"catalog-offline-v1","records":[...]}.
// Records contain record/kind/public-or-custom, optional publisher/title/model/
// article, explicit pairs [{audio_id,sha1}], and optional legacy_audio_ids and
// legacy_sha1s. Unknown safe record fields are retained and reported. No URI or
// image value is interpreted. This internal contract is not a legacy importer.
// Callers supply already sanitized artifacts; field checks cannot certify prose.
func Parse(ctx context.Context, input []byte) (Report, error) {
	if ctx == nil {
		return Report{}, ErrCanceled
	}
	ctx, cancel := context.WithTimeout(ctx, catalog.CatalogOperationTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return Report{}, ErrCanceled
	}
	if len(input) == 0 || !utf8.Valid(input) || uint64(len(input)) > catalog.BatchBytes.Maximum() {
		return Report{}, ErrEnvelope
	}
	// Scan every field (including unknown and duplicate fields) before making
	// any retained snapshot or digest. Errors never return a partially built report.
	d := json.NewDecoder(bytes.NewReader(input))
	d.UseNumber()
	if err := preflight(ctx, d, 0); err != nil {
		return Report{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return Report{}, ErrEnvelope
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(input, &envelope) != nil || len(envelope) != 2 || envelope["format"] == nil || envelope["records"] == nil {
		return Report{}, ErrEnvelope
	}
	var format string
	if json.Unmarshal(envelope["format"], &format) != nil || format != EnvelopeFormat {
		return Report{}, ErrEnvelope
	}
	var records []json.RawMessage
	if len(envelope["records"]) == 0 || envelope["records"][0] != '[' || json.Unmarshal(envelope["records"], &records) != nil || len(records) == 0 || uint64(len(records)) > catalog.BatchRecords.Maximum() {
		return Report{}, ErrEnvelope
	}
	report := Report{Records: make([]Record, 0, len(records))}
	for i, raw := range records {
		if ctx.Err() != nil {
			return Report{}, ErrCanceled
		}
		report.Records = append(report.Records, parseRecord(i, raw))
	}
	// Compare all records, including rejected/unsupported ones: no first-hit
	// dedupe, and a later invalid candidate cannot hide an identity collision.
	for i := range report.Records {
		for j := 0; j < i; j++ {
			a, b := &report.Records[i], &report.Records[j]
			if a.Locator != "" && a.Locator == b.Locator {
				a.issue(Rejected, "$.record", "duplicate-record-locator")
				b.issue(Rejected, "$.record", "duplicate-record-locator")
			}
			if a.Kind != b.Kind && (a.Kind == "custom" || b.Kind == "custom") && sharesPair(*a, *b) {
				a.issue(Ambiguous, "$.pairs", "custom-public-collision-unresolved")
				b.issue(Ambiguous, "$.pairs", "custom-public-collision-unresolved")
			}
		}
	}
	if ctx.Err() != nil {
		return Report{}, ErrCanceled
	}
	report.raw = string(input)
	report.Digest = sha256.Sum256(input)
	return report, nil
}

func preflight(ctx context.Context, d *json.Decoder, depth int) error {
	if ctx.Err() != nil {
		return ErrCanceled
	}
	t, err := d.Token()
	if err != nil {
		return ErrEnvelope
	}
	opening, container := t.(json.Delim)
	if !container {
		return nil
	}
	if depth >= int(catalog.SnapshotDepth.Maximum())+2 || opening != '{' && opening != '[' {
		return ErrEnvelope
	}
	keys := make(map[string]bool)
	position := 0
	for d.More() {
		if opening == '{' {
			t, err := d.Token()
			key, ok := t.(string)
			if err != nil || !ok {
				return ErrEnvelope
			}
			fieldErr := catalog.CheckImportedFieldName(key)
			if errors.Is(fieldErr, catalog.SensitiveField) {
				return SensitiveBatchError{RecordIndex: -1}
			}
			if fieldErr != nil && !errors.Is(fieldErr, catalog.ReservedAuthorityField) {
				return ErrEnvelope
			}
			if depth == 0 {
				if keys[key] || len(keys) >= 2 {
					return ErrEnvelope
				}
				keys[key] = true
			}
		}
		if err := preflight(ctx, d, depth+1); err != nil {
			if depth == 1 && opening == '[' && errors.Is(err, ErrSensitive) {
				return SensitiveBatchError{RecordIndex: position}
			}
			return err
		}
		position++
	}
	closing, err := d.Token()
	expected := json.Delim('}')
	if opening == '[' {
		expected = ']'
	}
	if err != nil || closing != expected {
		return ErrEnvelope
	}
	return nil
}

func parseRecord(index int, raw []byte) Record {
	r := Record{Index: index, Disposition: AcceptedForStaging}
	// This also bounds all nested unknown fields, strings and duplicate keys.
	snapshot, err := catalog.NewRecordSnapshot(raw)
	if err != nil {
		r.issue(Rejected, "$", "invalid-record-representation")
		return r
	}
	r.raw = string(snapshot.Bytes())
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields) // snapshot proved a complete JSON object
	keys := sortedKeys(fields)
	for _, key := range keys {
		value := fields[key]
		switch key {
		case "record", "kind", "publisher", "title", "model", "article":
			var s string
			if bytes.Equal(value, []byte("null")) || json.Unmarshal(value, &s) != nil {
				r.issue(Rejected, "$."+key, "expected-string")
				continue
			}
			switch key {
			case "record":
				r.Locator = s
			case "kind":
				r.Kind = s
			case "publisher":
				r.Publisher = s
			case "title":
				r.Title = s
			case "model":
				r.Model = s
			case "article":
				r.Article = s
			}
		case "pairs":
			parsePairs(&r, value)
		case "legacy_audio_ids":
			r.LegacyAudioIDs = parseIDs(&r, value, "$.legacy_audio_ids")
		case "legacy_sha1s":
			r.LegacySHA1s = parseHashes(&r, value, "$.legacy_sha1s")
		default:
			r.issue(Unsupported, "$."+key, "unsupported-safe-field")
		}
	}
	if r.Locator == "" || uint64(len(r.Locator)) > catalog.ReferenceBytes.Maximum() {
		r.issue(Rejected, "$.record", "invalid-record-locator")
	}
	if r.Kind != "public" && r.Kind != "custom" {
		r.issue(Rejected, "$.kind", "unknown-or-missing-kind")
	}
	_, ids := fields["legacy_audio_ids"]
	_, hashes := fields["legacy_sha1s"]
	if ids || hashes {
		if !ids || !hashes || len(r.LegacyAudioIDs) != len(r.LegacySHA1s) {
			r.issue(Rejected, "$.legacy_audio_ids", "unequal-legacy-arrays")
		} else {
			r.issue(Ambiguous, "$.legacy_audio_ids", "unproven-legacy-pairing")
		}
	}
	if len(r.Pairs) == 0 && !ids && !hashes && r.Title == "" && r.Model == "" && r.Article == "" {
		r.issue(Rejected, "$", "no-candidate-fields")
	}
	return r
}

func sortedKeys(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func array(r *Record, raw json.RawMessage, path string) []json.RawMessage {
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || uint64(len(values)) > catalog.RetainedVersions.Maximum() {
		r.issue(Rejected, path, "invalid-or-excessive-array")
		return nil
	}
	return values
}

func parseID(r *Record, raw json.RawMessage, path string) (uint64, bool) {
	id, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil || id == 0 {
		r.issue(Rejected, path, "invalid-audio-id")
		return 0, false
	}
	if id > uint64(^uint32(0)) {
		r.issue(Unsupported, path, "audio-id-outside-storage-width")
	}
	return id, true
}

func parseHash(r *Record, raw json.RawMessage, path string) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil || len(value) != 40 {
		r.issue(Rejected, path, "invalid-payload-sha1")
		return "", false
	}
	if _, err := hex.DecodeString(value); err != nil {
		r.issue(Rejected, path, "invalid-payload-sha1")
		return "", false
	}
	return value, true
}

func parsePairs(r *Record, raw json.RawMessage) {
	for i, value := range array(r, raw, "$.pairs") {
		path := "$.pairs[" + strconv.Itoa(i) + "]"
		var fields map[string]json.RawMessage
		if json.Unmarshal(value, &fields) != nil || fields == nil {
			r.issue(Rejected, path, "expected-pair-object")
			continue
		}
		for _, key := range sortedKeys(fields) {
			if key != "audio_id" && key != "sha1" {
				r.issue(Unsupported, path+"."+key, "unsupported-safe-field")
			}
		}
		id, a := parseID(r, fields["audio_id"], path+".audio_id")
		hash, b := parseHash(r, fields["sha1"], path+".sha1")
		if a && b {
			r.Pairs = append(r.Pairs, Pair{id, hash})
		}
	}
}

func parseIDs(r *Record, raw json.RawMessage, path string) []uint64 {
	var result []uint64
	for i, value := range array(r, raw, path) {
		if id, ok := parseID(r, value, path+"["+strconv.Itoa(i)+"]"); ok {
			result = append(result, id)
		}
	}
	return result
}

func parseHashes(r *Record, raw json.RawMessage, path string) []string {
	var result []string
	for i, value := range array(r, raw, path) {
		if hash, ok := parseHash(r, value, path+"["+strconv.Itoa(i)+"]"); ok {
			result = append(result, hash)
		}
	}
	return result
}

func sharesPair(a, b Record) bool {
	for _, x := range a.Pairs {
		for _, y := range b.Pairs {
			if x.AudioID == y.AudioID && strings.EqualFold(x.SHA1, y.SHA1) {
				return true
			}
		}
	}
	return false
}
