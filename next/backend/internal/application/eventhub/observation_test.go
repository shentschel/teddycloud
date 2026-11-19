package eventhub

import (
	"bytes"
	"testing"
)

func TestEventObservationContinuity(t *testing.T) {
	input := ObservationInput{Type: PlacementObserved, Payload: PlacementPayload{ProtocolValid: "unknown"}}
	raw, err := input.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeObservation(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := decoded.Canonical()
	if !bytes.Equal(raw, again) {
		t.Fatal("observation round trip")
	}
	for _, tc := range []struct {
		last, next         uint64
		previous, incoming []byte
		want               error
	}{{0, 1, nil, raw, nil}, {1, 1, raw, raw, nil}, {1, 1, raw, []byte("changed"), ErrObservationConflict}, {2, 1, raw, raw, ErrStaleObservation}, {1, 3, raw, raw, ErrSourceGap}, {1, 0, raw, raw, ErrInvalidEvent}, {1<<63 - 1, 1 << 63, raw, raw, ErrCapacityExceeded}} {
		if got := CompareObservation(tc.last, tc.next, tc.previous, tc.incoming); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
	unknown := "tag_00000000000000000000000001"
	input.Payload = PlacementPayload{TagID: &unknown, ProtocolValid: "valid"}
	if _, err := input.Canonical(); err != nil {
		t.Fatal(err)
	}
	input = ObservationInput{Type: PlaybackObserved, Payload: PlaybackPayload{State: "started", ContentID: &unknown}}
	if _, err := input.Canonical(); err == nil {
		t.Fatal("partial inferred pair")
	}
}
