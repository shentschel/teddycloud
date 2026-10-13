package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

// Resource identifies a fixed PI-08 budget, not a caller-selected maximum.
type Resource uint8

const (
	SourceRegistrations Resource = iota
	ReferenceBytes
	BatchBytes
	BatchRecords
	RecordBytes
	SnapshotDepth
	SnapshotFields
	MutationObservations
	SelectedSupport
	RetainedVersions
	RetainedObservations
	RetainedDecisions
	RetainedSupportLinks
	RetainedBytes
	TextBytes
	QueryEntries
	QueryBytes
	ResolutionCandidates
	CursorBytes
	StagingBytes
	StagingBatches
	ActiveOperations
)

var resourceBounds = [...]uint64{
	256, 128, 1 << 20, 32, 64 << 10, 16, 256, 64, 256,
	1024, 4096, 1024, 16384, 8 << 20, 4096, 128, 1 << 20,
	1024, 256, 64 << 20, 256, 1,
}

const CatalogOperationTimeout = 10 * time.Second
const CatalogTransactionTimeout = 5 * time.Second

// LimitError retains only the budget kind; input text and values never escape.
type LimitError struct{ Resource Resource }

func (LimitError) Error() string {
	return "catalog resource limit exceeded"
}

var ErrInvalidCatalog = errors.New("invalid catalog evidence")

func (r Resource) Maximum() uint64 {
	if int(r) >= len(resourceBounds) {
		return 0
	}
	return resourceBounds[r]
}

// CheckLimit checks before addition, allocation or copying. It cannot overflow.
// Encoders must charge actual encoded bytes, including escaping, separately.
func CheckLimit(resource Resource, current, added uint64) error {
	maximum := resource.Maximum()
	if maximum == 0 {
		return ErrInvalidCatalog
	}
	if current > maximum || added > maximum-current {
		return LimitError{Resource: resource}
	}
	return nil
}

func checkCatalogText(text string, resource Resource, optional bool) error {
	if err := CheckLimit(resource, 0, uint64(len(text))); err != nil {
		return err
	}
	if optional && text == "" {
		return nil
	}
	if !evidence.ValidText(text, int(resource.Maximum())) {
		return ErrInvalidCatalog
	}
	return nil
}

// FieldRejection is deliberately value-free, including for errors and logging.
type FieldRejection uint8

const (
	SensitiveField FieldRejection = iota + 1
	ReservedAuthorityField
)

func (FieldRejection) Error() string {
	return "prohibited catalog field"
}

// CheckImportedFieldName is a conservative hook, not a secret-free certificate
// for prose. The trusted caller must sanitize artifacts before retaining a digest.
// Case and separator changes cannot disguise known sensitive/authority names.
func CheckImportedFieldName(name string) error {
	if err := checkCatalogText(name, ReferenceBytes, false); err != nil {
		return err
	}
	var normalized strings.Builder
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			normalized.WriteRune(r)
		} else if r != '-' && r != '_' && r != '.' && r != ' ' {
			return SensitiveField
		}
	}
	key := normalized.String()
	for _, forbidden := range []string{
		"credential", "password", "passwd", "secret", "token", "auth",
		"cookie", "uid", "account", "username", "apikey", "privatekey", "physicalid",
		"localpath", "fullpath",
	} {
		if strings.Contains(key, forbidden) {
			return SensitiveField
		}
	}
	for _, forbidden := range []string{
		"issuer", "principal", "capability", "acquisition", "review",
		"accepted", "sourceid", "sourcerevision", "registration",
	} {
		if strings.Contains(key, forbidden) {
			return ReservedAuthorityField
		}
	}
	return nil
}

// RecordSnapshot retains exact safe JSON bytes as an immutable string. It is
// representation validation only: no source format, pairing or import behavior.
type RecordSnapshot struct{ raw string }

func NewRecordSnapshot(raw []byte) (RecordSnapshot, error) {
	if err := CheckLimit(RecordBytes, 0, uint64(len(raw))); err != nil {
		return RecordSnapshot{}, err
	}
	if len(raw) == 0 || !utf8.Valid(raw) {
		return RecordSnapshot{}, ErrInvalidCatalog
	}
	// Preflight nesting before the decoder allocates strings or object keys.
	depth := uint64(0)
	quoted, escaped := false, false
	for _, b := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		if b == '"' {
			quoted = true
		} else if b == '{' || b == '[' {
			if err := CheckLimit(SnapshotDepth, depth, 1); err != nil {
				return RecordSnapshot{}, err
			}
			depth++
		} else if b == '}' || b == ']' {
			if depth == 0 {
				return RecordSnapshot{}, ErrInvalidCatalog
			}
			depth--
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	fields := uint64(0)
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return RecordSnapshot{}, ErrInvalidCatalog
	}
	if err := validateSnapshotContainer(decoder, '{', &fields); err != nil {
		return RecordSnapshot{}, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return RecordSnapshot{}, ErrInvalidCatalog
	}
	// Copy only after byte/depth/field/text/sensitive-name admission succeeded.
	return RecordSnapshot{raw: string(raw)}, nil
}

func validateSnapshotContainer(decoder *json.Decoder, opening json.Delim, fields *uint64) error {
	keys := make(map[string]struct{})
	for decoder.More() {
		if opening == '{' {
			if err := CheckLimit(SnapshotFields, *fields, 1); err != nil {
				return err
			}
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return ErrInvalidCatalog
			}
			if err := CheckImportedFieldName(key); err != nil {
				return err
			}
			if _, duplicate := keys[key]; duplicate {
				return ErrInvalidCatalog
			}
			keys[key] = struct{}{}
			*fields++
		}
		token, err := decoder.Token()
		if err != nil {
			return ErrInvalidCatalog
		}
		switch value := token.(type) {
		case json.Delim:
			if value != '{' && value != '[' {
				return ErrInvalidCatalog
			}
			if err := validateSnapshotContainer(decoder, value, fields); err != nil {
				return err
			}
		case string:
			if err := checkCatalogText(value, TextBytes, true); err != nil {
				return err
			}
		}
	}
	closing, err := decoder.Token()
	expected := json.Delim('}')
	if opening == '[' {
		expected = ']'
	}
	if err != nil || closing != expected {
		return ErrInvalidCatalog
	}
	return nil
}

func (s RecordSnapshot) Bytes() []byte {
	return []byte(s.raw)
}

func (s RecordSnapshot) ByteLength() uint64 {
	return uint64(len(s.raw))
}
