package eventhub

import (
	"bytes"
	"encoding/json"
)

// ObservationInput contains reported facts, never inferred desired state.
type ObservationInput struct {
	Type       Type    `json:"type"`
	OccurredAt *string `json:"occurred_at"`
	Payload    Payload `json:"payload"`
}

func (v ObservationInput) Canonical() ([]byte, error) {
	if v.OccurredAt != nil && !timestamp(*v.OccurredAt) {
		return nil, ErrInvalidEvent
	}
	valid := false
	switch p := v.Payload.(type) {
	case PlacementPayload:
		valid = v.Type == PlacementObserved && optionalTag(p.TagID) && one(p.ProtocolValid, "valid", "invalid", "unknown")
	case RemovalPayload:
		valid = v.Type == RemovalObserved && optionalTag(p.TagID)
	case PlaybackPayload:
		valid = v.Type == PlaybackObserved && one(p.State, "starting", "started", "stopped", "unknown") && (p.ContentID == nil && p.VersionID == nil || p.ContentID != nil && p.VersionID != nil && content(*p.ContentID) && version(*p.VersionID))
	}
	if !valid {
		return nil, ErrInvalidEvent
	}
	raw, err := canonical(v)
	if len(raw) > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}
	return raw, err
}

// Only the last exact input is retryable. A gap requires durable invalidation.
func CompareObservation(last, incoming uint64, previous, next []byte) error {
	if last == 1<<63-1 && incoming == last+1 {
		return ErrCapacityExceeded
	}
	if incoming == 0 || incoming > 1<<63-1 || last > 1<<63-1 {
		return ErrInvalidEvent
	}
	if incoming < last {
		return ErrStaleObservation
	}
	if incoming == last {
		if bytes.Equal(previous, next) {
			return nil
		}
		return ErrObservationConflict
	}
	if incoming != last+1 {
		return ErrSourceGap
	}
	return nil
}
func DecodeObservation(raw []byte) (ObservationInput, error) {
	var v ObservationInput
	if len(raw) > MaxPayloadBytes {
		return v, ErrPayloadTooLarge
	}
	var w struct {
		Type       Type            `json:"type"`
		OccurredAt *string         `json:"occurred_at"`
		Payload    json.RawMessage `json:"payload"`
	}
	if strict(raw, &w) != nil {
		return v, ErrInvalidEvent
	}
	switch w.Type {
	case PlacementObserved:
		var p PlacementPayload
		if strict(w.Payload, &p) != nil {
			return v, ErrInvalidEvent
		}
		v.Payload = p
	case RemovalObserved:
		var p RemovalPayload
		if strict(w.Payload, &p) != nil {
			return v, ErrInvalidEvent
		}
		v.Payload = p
	case PlaybackObserved:
		var p PlaybackPayload
		if strict(w.Payload, &p) != nil {
			return v, ErrInvalidEvent
		}
		v.Payload = p
	default:
		return v, ErrInvalidEvent
	}
	v.Type = w.Type
	v.OccurredAt = w.OccurredAt
	b, err := v.Canonical()
	if err != nil {
		return ObservationInput{}, err
	}
	if !bytes.Equal(b, raw) {
		return ObservationInput{}, ErrInvalidEvent
	}
	return v, nil
}
