package catalog

import "encoding/json"

type ImportRecord struct {
	Locator  string
	Snapshot RecordSnapshot
}
type ImportPayload struct {
	artifact, report string
	records          []ImportRecord
}

// NewImportPayload is a parser seam for sanitized bytes. Trusted in-process
// composition is assumed, as for the existing core capability constructors.
func NewImportPayload(artifact, report []byte, records []ImportRecord) (ImportPayload, error) {
	for _, check := range []struct {
		r Resource
		n uint64
	}{{BatchBytes, uint64(len(artifact))}, {RetainedBytes, uint64(len(report))}, {BatchRecords, uint64(len(records))}} {
		if err := CheckLimit(check.r, 0, check.n); err != nil {
			return ImportPayload{}, err
		}
	}
	if len(artifact) == 0 || len(report) == 0 || len(records) == 0 || !json.Valid(artifact) || !json.Valid(report) {
		return ImportPayload{}, ErrInvalidCatalog
	}
	for _, r := range records {
		if r.Snapshot.ByteLength() == 0 || len(r.Locator) == 0 || uint64(len(r.Locator)) > ReferenceBytes.Maximum() {
			return ImportPayload{}, ErrInvalidCatalog
		}
	}
	return ImportPayload{string(artifact), string(report), append([]ImportRecord(nil), records...)}, nil
}
func (p ImportPayload) ArtifactBytes() []byte   { return []byte(p.artifact) }
func (p ImportPayload) ReportBytes() []byte     { return []byte(p.report) }
func (p ImportPayload) Records() []ImportRecord { return append([]ImportRecord(nil), p.records...) }
func (p ImportPayload) RetainedBytes() uint64 {
	n := uint64(len(p.artifact) + len(p.report))
	for _, r := range p.records {
		n += r.Snapshot.ByteLength()
	}
	return n
}

func (p ImportPayload) ByteLength() uint64  { return uint64(len(p.artifact)) }
func (p ImportPayload) RecordCount() uint64 { return uint64(len(p.records)) }
