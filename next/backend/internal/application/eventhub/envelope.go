package eventhub

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	assignment "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const TimestampLayout = "2006-01-02T15:04:05.000000000Z"

func ValidOpaqueID(s string) bool {
	if len(s) != 32 || s == strings.Repeat("0", 32) {
		return false
	}
	b, e := hex.DecodeString(s)
	return e == nil && hex.EncodeToString(b) == s
}
func Decimal(s string, zero bool) (uint64, bool) {
	if s == "" || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, e := strconv.ParseUint(s, 10, 63)
	return n, e == nil && (zero || n > 0)
}
func ValidStream(s Stream) bool {
	return ValidOpaqueID(s.TenantID) && ValidOpaqueID(s.Epoch) && (s.Kind == "control" && s.BoxID == nil || s.Kind == "box" && s.BoxID != nil && ValidOpaqueID(*s.BoxID))
}
func timestamp(s string) bool {
	t, e := time.Parse(TimestampLayout, s)
	return e == nil && t.Format(TimestampLayout) == s
}
func optionalDecimal(s *string) bool {
	if s == nil {
		return true
	}
	_, ok := Decimal(*s, false)
	return ok
}
func reference(s *string) bool {
	if s == nil {
		return true
	}
	if len(*s) == 0 || len(*s) > 128 {
		return false
	}
	for _, c := range *s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c)) {
			return false
		}
	}
	return true
}
func tag(s string) bool      { _, e := identity.ParseTagID(s); return e == nil }
func content(s string) bool  { _, e := identity.ParseContentID(s); return e == nil }
func version(s string) bool  { _, e := identity.ParseContentVersionID(s); return e == nil }
func same(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func one(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func optionalTag(s *string) bool { return s == nil || tag(*s) }

// Validate binds each payload to its owner, aggregate and exact stream scope.
func Validate(e Envelope) error {
	if e.Format != Format || e.PayloadVersion != 1 {
		return ErrUnsupportedVersion
	}
	if _, ok := Decimal(e.Sequence, false); !ok {
		return ErrInvalidEvent
	}
	if !ValidStream(e.Stream) || e.EventID != "ev1:"+e.Stream.Epoch+":"+e.Sequence || !ValidOpaqueID(e.Source.InstanceID) || !ValidOpaqueID(e.Source.Epoch) {
		return ErrInvalidEvent
	}
	if _, ok := Decimal(e.Source.Sequence, false); !ok {
		return ErrInvalidEvent
	}
	if !optionalDecimal(e.Aggregate.Revision) || !timestamp(e.CommittedAt) || e.OccurredAt != nil && !timestamp(*e.OccurredAt) || !reference(e.CausationID) || !reference(e.CorrelationID) {
		return ErrInvalidEvent
	}
	if !utf8.ValidString(e.Aggregate.ID) {
		return ErrInvalidEvent
	}
	box := e.Stream.Kind == "box"
	if box && (e.Aggregate.Kind != "box" || e.Aggregate.ID != *e.Stream.BoxID || e.Aggregate.Revision == nil) {
		return ErrInvalidEvent
	}
	if !box && e.Aggregate.Kind == "box" {
		return ErrInvalidEvent
	}
	valid := false
	owner := e.Source.Owner
	switch p := e.Payload.(type) {
	case TagPayload:
		valid = e.Type == TagChanged && !box && owner == TagRegistry && e.Aggregate.Kind == "tag" && e.Aggregate.ID == p.TagID && tag(p.TagID) && e.Aggregate.Revision != nil && *e.Aggregate.Revision == p.Revision && one(p.Change, "created", "metadata")
	case ContentPayload:
		valid = e.Type == ContentChanged && !box && owner == ContentStore && e.Aggregate.Kind == "content" && e.Aggregate.ID == p.ContentID && content(p.ContentID) && version(p.VersionID) && e.Aggregate.Revision == nil && p.Change == "bound"
	case CatalogPayload:
		valid = e.Type == CatalogChanged && !box && owner == Catalog && e.Aggregate.Kind == "content" && e.Aggregate.ID == p.ContentID && content(p.ContentID) && same(e.Aggregate.Revision, p.Revision) && one(p.Change, "baseline", "adoption", "review", "order", "registration", "supersession", "media", "multiple")
		if p.Revision == nil && p.Change != "baseline" {
			valid = false
		}
	case AssignmentPayload:
		_, err := assignment.ParseID(p.AssignmentID)
		valid = e.Type == AssignmentChanged && !box && owner == Assignment && e.Aggregate.Kind == "assignment" && e.Aggregate.ID == p.AssignmentID && err == nil && tag(p.TagID) && e.Aggregate.Revision != nil && *e.Aggregate.Revision == p.Revision && one(p.Change, "assign", "unassign", "classification", "original-target", "retire", "multiple")
	case PlacementPayload:
		valid = e.Type == PlacementObserved && box && owner == GatewayObservation && optionalTag(p.TagID) && one(p.ProtocolValid, "valid", "invalid", "unknown")
	case RemovalPayload:
		valid = e.Type == RemovalObserved && box && owner == GatewayObservation && optionalTag(p.TagID)
	case PlaybackPayload:
		valid = e.Type == PlaybackObserved && box && owner == GatewayObservation && one(p.State, "starting", "started", "stopped", "unknown") && (p.ContentID == nil && p.VersionID == nil || p.ContentID != nil && p.VersionID != nil && content(*p.ContentID) && version(*p.VersionID))
	case InvalidationPayload:
		switch e.Type {
		case CatalogInvalidated:
			valid = !box && owner == Catalog && e.Aggregate.Kind == "catalog-scope" && e.Aggregate.ID == e.Stream.TenantID && e.Aggregate.Revision == nil && one(p.Reason, "source-registration", "source-revocation", "import")
		case StorageInvalidated:
			valid = !box && one(string(owner), string(ContentStore), string(Lifecycle)) && e.Aggregate.Kind == "storage-scope" && e.Aggregate.ID == e.Stream.TenantID && e.Aggregate.Revision == nil && one(p.Reason, "reconcile", "quarantine", "restart")
		case BoxInvalidated:
			valid = box && one(string(owner), string(GatewayObservation), string(Lifecycle)) && one(p.Reason, "source-gap", "source-replaced", "restart", "disconnected")
		}
	}
	if !valid {
		return ErrInvalidEvent
	}
	return nil
}
func canonical(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(v); e != nil {
		return nil, ErrInvalidEvent
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}
func Encode(e Envelope) ([]byte, error) {
	if err := Validate(e); err != nil {
		return nil, err
	}
	p, err := canonical(e.Payload)
	if err != nil {
		return nil, err
	}
	if len(p) > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}
	b, err := canonical(e)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxEnvelopeBytes {
		return nil, ErrPayloadTooLarge
	}
	return b, nil
}
func strict(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrInvalidEvent
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalidEvent
	}
	return nil
}

// Decode requires canonical bytes. Re-encoding catches duplicate/missing keys,
// alternate escaping, omitted nulls and lossy Unicode decoding as well as order.
func Decode(raw []byte) (Envelope, error) {
	var e Envelope
	if len(raw) > MaxEnvelopeBytes {
		return e, ErrPayloadTooLarge
	}
	if !utf8.Valid(raw) {
		return e, ErrInvalidEvent
	}
	var wire struct {
		Format         string          `json:"format"`
		Type           Type            `json:"type"`
		PayloadVersion int             `json:"payload_version"`
		EventID        string          `json:"event_id"`
		Sequence       string          `json:"sequence"`
		Stream         Stream          `json:"stream"`
		Source         Source          `json:"source"`
		Aggregate      Aggregate       `json:"aggregate"`
		OccurredAt     *string         `json:"occurred_at"`
		CommittedAt    string          `json:"committed_at"`
		CausationID    *string         `json:"causation_id"`
		CorrelationID  *string         `json:"correlation_id"`
		Payload        json.RawMessage `json:"payload"`
	}
	if err := strict(raw, &wire); err != nil {
		return e, err
	}
	if wire.Format != Format || wire.PayloadVersion != 1 {
		return e, ErrUnsupportedVersion
	}
	if len(wire.Payload) > MaxPayloadBytes {
		return e, ErrPayloadTooLarge
	}
	var p Payload
	switch wire.Type {
	case TagChanged:
		var v TagPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	case ContentChanged:
		var v ContentPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	case CatalogChanged:
		var v CatalogPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	case AssignmentChanged:
		var v AssignmentPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	case PlacementObserved:
		var v PlacementPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	case RemovalObserved:
		var v RemovalPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	case PlaybackObserved:
		var v PlaybackPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	case CatalogInvalidated, BoxInvalidated, StorageInvalidated:
		var v InvalidationPayload
		if strict(wire.Payload, &v) != nil {
			return e, ErrInvalidEvent
		}
		p = v
	default:
		return e, ErrInvalidEvent
	}
	e = Envelope{wire.Format, wire.Type, wire.PayloadVersion, wire.EventID, wire.Sequence, wire.Stream, wire.Source, wire.Aggregate, wire.OccurredAt, wire.CommittedAt, wire.CausationID, wire.CorrelationID, p}
	b, err := Encode(e)
	if err != nil {
		return Envelope{}, err
	}
	if !bytes.Equal(b, raw) {
		return Envelope{}, ErrInvalidEvent
	}
	return e, nil
}
