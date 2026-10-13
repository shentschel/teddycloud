package catalog

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCatalogLimits(t *testing.T) {
	expected := []uint64{
		256, 128, 1048576, 32, 65536, 16, 256, 64, 256,
		1024, 4096, 1024, 16384, 8388608, 4096, 128, 1048576,
		1024, 256, 67108864, 256, 1,
	}
	for i, maximum := range expected {
		resource := Resource(i)
		if resource.Maximum() != maximum {
			t.Fatalf("policy bound changed for %d", resource)
		}
		for _, pair := range [][2]uint64{{0, maximum}, {maximum, 0}, {maximum - 1, 1}} {
			if err := CheckLimit(resource, pair[0], pair[1]); err != nil {
				t.Fatalf("boundary rejected for %d: %v", resource, err)
			}
		}
		for _, pair := range [][2]uint64{{0, maximum + 1}, {maximum, 1}, {maximum + 1, 0}, {1, math.MaxUint64}} {
			var limit LimitError
			if err := CheckLimit(resource, pair[0], pair[1]); !errors.As(err, &limit) || limit.Resource != resource {
				t.Fatalf("one-over/overflow accepted for %d: %v", resource, err)
			}
		}
	}
	if CheckLimit(Resource(255), 0, 0) == nil {
		t.Fatal("unknown budget admitted")
	}
	if CatalogOperationTimeout != 10*time.Second || CatalogTransactionTimeout != 5*time.Second {
		t.Fatal("deadline policy changed")
	}
	for _, resource := range []Resource{ReferenceBytes, TextBytes} {
		maximum := int(resource.Maximum())
		if err := checkCatalogText(strings.Repeat("é", maximum/2), resource, false); err != nil {
			t.Fatal(err)
		}
		if err := checkCatalogText(strings.Repeat("é", maximum/2)+"x", resource, false); err == nil {
			t.Fatal("UTF-8 byte boundary ignored")
		}
		for _, invalid := range []string{"", "a\nb", "a\u0085b", string([]byte{0xff})} {
			if checkCatalogText(invalid, resource, false) == nil {
				t.Fatal("malformed text admitted")
			}
		}
	}
}

func TestCatalogSnapshotBounds(t *testing.T) {
	raw := []byte(`{"unknown":"safe","ref":"https://invalid.example/../opaque"}`)
	snapshot, err := NewRecordSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[2] = 'x'
	copyBytes := snapshot.Bytes()
	copyBytes[2] = 'z'
	if string(snapshot.Bytes()) != `{"unknown":"safe","ref":"https://invalid.example/../opaque"}` {
		t.Fatal("snapshot was mutable")
	}
	atBytes := "{}" + strings.Repeat(" ", 65534)
	if _, err := NewRecordSnapshot([]byte(atBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRecordSnapshot([]byte(atBytes + " ")); err == nil {
		t.Fatal("record byte one-over admitted")
	}
	for _, depth := range []int{16, 17} {
		raw := `{"nested":` + strings.Repeat("[", depth-1) + "0" + strings.Repeat("]", depth-1) + "}"
		_, err := NewRecordSnapshot([]byte(raw))
		if depth == 16 && err != nil || depth == 17 && err == nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
	for _, count := range []int{256, 257} {
		fields := make([]string, count)
		for i := range fields {
			fields[i] = fmt.Sprintf(`"f%d":0`, i)
		}
		_, err := NewRecordSnapshot([]byte("{" + strings.Join(fields, ",") + "}"))
		if count == 256 && err != nil || count == 257 && err == nil {
			t.Fatalf("fields %d: %v", count, err)
		}
	}
	for _, length := range []int{4096, 4097} {
		_, err := NewRecordSnapshot([]byte(`{"value":"` + strings.Repeat("x", length) + `"}`))
		if length == 4096 && err != nil || length == 4097 && err == nil {
			t.Fatalf("text %d: %v", length, err)
		}
	}
	for _, invalid := range []string{
		"", "null", "[]", `{"x":1,"x":2}`, `{"x":{"y":1,"y":2}}`,
		`{"x":1}{}`, `{"x":`, `{"x":[}}`, `{"x":"\u0001"}`,
		"{\"x\":\"" + string([]byte{0xff}) + "\"}",
	} {
		if _, err := NewRecordSnapshot([]byte(invalid)); err == nil {
			t.Fatal("malformed snapshot admitted")
		}
	}
	// Nested fields count against the same whole-record budget.
	nested := make([]string, 256)
	for i := range nested {
		nested[i] = fmt.Sprintf(`"f%d":0`, i)
	}
	if _, err := NewRecordSnapshot([]byte(`{"outer":{` + strings.Join(nested, ",") + "}}")); err == nil {
		t.Fatal("nested field total escaped bound")
	}
}

func TestCatalogProhibitedFields(t *testing.T) {
	for _, name := range []string{
		"token", "Access_Token", "AUTHORIZATION", "rUID", "uid",
		"password", "client-secret", "private_account_name", "accountId",
		"full.local.path", "cookie", "credentials", "tökén", "api_key", "username",
	} {
		if err := CheckImportedFieldName(name); err != SensitiveField {
			t.Fatalf("sensitive name was not typed rejection: %v", err)
		}
	}
	for _, name := range []string{
		"issuer", "principal", "acquisition", "review", "review.accepted",
		"capability", "source_id", "SourceRevision", "registration",
	} {
		if err := CheckImportedFieldName(name); err != ReservedAuthorityField {
			t.Fatalf("authority name was not typed rejection: %v", err)
		}
	}
	for _, name := range []string{"title", "origin", "sourceLabel", "series", "unknown_extension"} {
		if err := CheckImportedFieldName(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{"nested":[{"access_token":"DO-NOT-RETAIN"}]}`,
		`{"\u0074oken":"DO-NOT-RETAIN"}`,
		`{"issuer":"DO-NOT-RETAIN"}`,
	} {
		snapshot, err := NewRecordSnapshot([]byte(raw))
		var rejection FieldRejection
		if !errors.As(err, &rejection) || snapshot.ByteLength() != 0 || strings.Contains(err.Error(), "DO-NOT-RETAIN") {
			t.Fatal("sensitive rejection retained or exposed data")
		}
	}
}
