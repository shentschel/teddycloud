package eventhub

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func testEnvelope() Envelope {
	revision := "1"
	return Envelope{Format: Format, Type: TagChanged, PayloadVersion: 1, EventID: "ev1:33333333333333333333333333333333:1", Sequence: "1", Stream: Stream{TenantID: strings.Repeat("1", 32), Kind: "control", Epoch: strings.Repeat("3", 32)}, Source: Source{Owner: TagRegistry, InstanceID: strings.Repeat("2", 32), Epoch: strings.Repeat("4", 32), Sequence: "1"}, Aggregate: Aggregate{Kind: "tag", ID: "tag_00000000000000000000000001", Revision: &revision}, CommittedAt: "2026-11-19T08:00:00.000000000Z", Payload: TagPayload{TagID: "tag_00000000000000000000000001", Revision: "1", Change: "created"}}
}
func TestEventEnvelope(t *testing.T) {
	e := testEnvelope()
	raw, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	golden := `{"format":"pi11-events/1","type":"tag.registry.changed","payload_version":1,"event_id":"ev1:33333333333333333333333333333333:1","sequence":"1","stream":{"tenant_id":"11111111111111111111111111111111","kind":"control","box_id":null,"epoch":"33333333333333333333333333333333"},"source":{"owner":"tag-registry","instance_id":"22222222222222222222222222222222","epoch":"44444444444444444444444444444444","sequence":"1"},"aggregate":{"kind":"tag","id":"tag_00000000000000000000000001","revision":"1"},"occurred_at":null,"committed_at":"2026-11-19T08:00:00.000000000Z","causation_id":null,"correlation_id":null,"payload":{"tag_id":"tag_00000000000000000000000001","revision":"1","change":"created"}}`
	if string(raw) != golden {
		t.Fatalf("canonical bytes: %s", raw)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Encode(decoded)
	if !bytes.Equal(raw, again) {
		t.Fatal("round trip")
	}
	for _, invalid := range []string{
		strings.Replace(golden, `"sequence":"1"`, `"sequence":"01"`, 1),
		strings.Replace(golden, `"sequence":"1"`, `"sequence":1`, 1),
		strings.Replace(golden, `"payload_version":1`, `"payload_version":1.0`, 1),
		strings.Replace(golden, `"format":`, `"format":"pi11-events/1","format":`, 1),
		strings.Replace(golden, `"box_id":null,`, "", 1),
		strings.Replace(golden, `"change":"created"`, `"change":"created","path":"secret"`, 1),
		strings.Replace(golden, `"tag.registry.changed"`, `"tag.removal.observed"`, 1),
		strings.Replace(golden, `"owner":"tag-registry"`, `"owner":"assignment"`, 1),
		strings.Replace(golden, `"revision":"1"`, `"revision":null`, 1),
		strings.Replace(golden, `"causation_id":null`, `"causation_id":"https://secret"`, 1),
		strings.Replace(golden, `"epoch":"33333333333333333333333333333333"`, `"epoch":"00000000000000000000000000000000"`, 1),
		strings.Replace(golden, `000000000Z`, `000000000+00:00`, 1),
		golden + " ", string([]byte{0xff}),
	} {
		if _, err := Decode([]byte(invalid)); err == nil {
			t.Fatalf("accepted malformed envelope: %s", invalid)
		}
	}
	for _, size := range []int{MaxEnvelopeBytes, MaxEnvelopeBytes + 1} {
		_, err := Decode(bytes.Repeat([]byte{'x'}, size))
		if err == nil {
			t.Fatal("oversize/malformed accepted")
		}
		if size > MaxEnvelopeBytes && !errors.Is(err, ErrPayloadTooLarge) {
			t.Fatal(err)
		}
	}
}
func FuzzEventEnvelope(f *testing.F) {
	raw, _ := Encode(testEnvelope())
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, raw []byte) {
		e, err := Decode(raw)
		if err != nil {
			return
		}
		again, err := Encode(e)
		if err != nil || !bytes.Equal(raw, again) || len(again) > MaxEnvelopeBytes {
			t.Fatal("noncanonical success")
		}
	})
}
