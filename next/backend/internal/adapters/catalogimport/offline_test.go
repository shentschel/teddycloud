package catalogimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

const h1 = "1111111111111111111111111111111111111111"
const h2 = "2222222222222222222222222222222222222222"

func batch(records ...string) []byte {
	return []byte(`{"format":"catalog-offline-v1","records":[` + strings.Join(records, ",") + `]}`)
}

func record(extra string) string {
	return `{"record":"generated","kind":"public","title":"Generated"` + extra + `}`
}

func parsed(t *testing.T, input []byte) Report {
	t.Helper()
	r, err := Parse(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Records) == 0 {
		t.Fatal("missing complete record report")
	}
	return r
}

func TestCatalogImportDispositions(t *testing.T) {
	input, err := os.ReadFile("testdata/dispositions.json")
	if err != nil {
		t.Fatal(err)
	}
	r := parsed(t, input)
	want := []Disposition{AcceptedForStaging, Rejected, Unsupported, Ambiguous}
	for i, d := range want {
		if r.Records[i].Index != i || r.Records[i].Disposition != d {
			t.Fatalf("record %d: %+v", i, r.Records[i])
		}
	}
	if len(r.Records[0].Pairs) != 2 || r.Records[0].Pairs[0].AudioID != 7 || r.Records[0].Pairs[1].AudioID != 9 {
		t.Fatal("explicit pairs dropped or combined")
	}
	if r.Records[2].Pairs[0].AudioID != ^uint64(0) || len(r.Records[2].Issues) != 2 {
		t.Fatal("width/unsupported suberrors hidden")
	}
	if r.Digest != sha256.Sum256(input) || !bytes.Equal(r.ArtifactBytes(), input) {
		t.Fatal("artifact identity changed")
	}
	if !reflect.DeepEqual(r, parsed(t, input)) {
		t.Fatal("non-deterministic report")
	}
	// Mutations of caller input or returned byte copies cannot alter snapshots.
	wantRaw := string(input)
	input[0] = '!'
	artifact := r.ArtifactBytes()
	artifact[0] = '!'
	snapshot := r.Records[0].SnapshotBytes()
	snapshot[0] = '!'
	if string(r.ArtifactBytes()) != wantRaw || r.Records[0].SnapshotBytes()[0] != '{' {
		t.Fatal("retention aliases caller bytes")
	}
	duplicates := parsed(t, batch(record(""), record("")))
	for _, rec := range duplicates.Records {
		if rec.Disposition != Rejected {
			t.Fatal("first-hit duplicate locator")
		}
	}
	partial := parsed(t, batch(record(`,"pairs":[{"audio_id":7,"sha1":"`+h1+`"},{"audio_id":0,"sha1":"bad"}],"series":"Generated"`))).Records[0]
	if partial.Disposition != Rejected || len(partial.Pairs) != 1 || len(partial.Issues) != 3 {
		t.Fatal("valid pair concealed invalid siblings or unsupported field")
	}
}

func TestCatalogImportArrayAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		want        Disposition
	}{
		{"equal-unproven", `,"legacy_audio_ids":[7,9],"legacy_sha1s":["` + h1 + `","` + h2 + `"]`, Ambiguous},
		{"crossed", `,"legacy_audio_ids":[7,9],"legacy_sha1s":["` + h2 + `","` + h1 + `"]`, Ambiguous},
		{"unequal", `,"legacy_audio_ids":[7,9],"legacy_sha1s":["` + h1 + `"]`, Rejected},
		{"missing-side", `,"legacy_audio_ids":[7]`, Rejected},
		{"malformed-side", `,"legacy_audio_ids":[7,"9"],"legacy_sha1s":["` + h1 + `","` + h2 + `"]`, Rejected},
		{"explicit-and-unproven", `,"pairs":[{"audio_id":7,"sha1":"` + h1 + `"}],"legacy_audio_ids":[9],"legacy_sha1s":["` + h2 + `"]`, Ambiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parsed(t, batch(record(tc.extra))).Records[0]
			if r.Disposition != tc.want {
				t.Fatalf("%+v", r)
			}
			if tc.name == "explicit-and-unproven" {
				if len(r.Pairs) != 1 {
					t.Fatal("explicit candidate lost")
				}
			} else if len(r.Pairs) != 0 {
				t.Fatal("parallel arrays converted to pairs")
			}
			if r.Disposition == Ambiguous && (len(r.LegacyAudioIDs) == 0 || len(r.LegacySHA1s) == 0) {
				t.Fatal("raw array candidates lost")
			}
		})
	}
}

func TestCatalogImportNoAuthority(t *testing.T) {
	for _, name := range []string{"source_id", "source_revision", "issuer", "acquisition", "review", "accepted", "capability", "registration"} {
		r := parsed(t, batch(record(`,"`+name+`":"spoofed"`))).Records[0]
		if r.Disposition != Rejected {
			t.Fatalf("authority field %s admitted", name)
		}
	}
	r := parsed(t, batch(record(`,"publisher":"trusted-core"`))).Records[0]
	if r.Disposition != AcceptedForStaging || r.Publisher != "trusted-core" {
		t.Fatal("publisher assertion conflated with core registration")
	}
	a := `{"record":"generated-public","kind":"public","pairs":[{"audio_id":7,"sha1":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`
	b := `{"record":"generated-custom","kind":"custom","pairs":[{"audio_id":7,"sha1":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]}`
	for _, input := range [][]byte{batch(a, b), batch(b, a)} {
		r := parsed(t, input)
		for _, rec := range r.Records {
			if rec.Disposition != Ambiguous || len(rec.Pairs) != 1 {
				t.Fatal("custom-first winner/dedupe")
			}
		}
		if r.Records[0].Locator == r.Records[1].Locator {
			t.Fatal("identities merged")
		}
	}
	// Enforce the offline seam mechanically as well as observing malicious refs.
	for _, path := range []string{"offline.go", "report.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if name == "os" || name == "io/fs" || strings.HasPrefix(name, "net") || strings.Contains(name, "sqlite") || strings.Contains(name, "application") {
				t.Fatalf("I/O/authority import: %s", name)
			}
		}
	}
}

func TestCatalogImportUnsupportedFields(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	for _, ref := range []string{server.URL + "/generated", "file:///generated-secret", "http://169.254.169.254/generated", "javascript:generated()", "data:image/svg+xml,generated", "../../generated"} {
		encoded, _ := json.Marshal(ref)
		r := parsed(t, batch(record(`,"image":`+string(encoded)+`,"uri":`+string(encoded)+`,"extension":{"nested":[1,true]}`))).Records[0]
		if r.Disposition != Unsupported || len(r.Issues) != 3 || !bytes.Contains(r.SnapshotBytes(), encoded) {
			t.Fatal("unknown field/reference dropped or executed")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("parser performed network I/O")
	}
	for _, extra := range []string{`,"kind":"future"`, `,"title":null`, `,"pairs":[{"audio_id":7,"sha1":"bad"}]`, `,"extension":{"x":1,"x":2}`} {
		if parsed(t, batch(record(extra))).Records[0].Disposition != Rejected {
			t.Fatal("invalid enum/type/duplicate admitted")
		}
	}
	if parsed(t, batch(record(`,"extension":{"x":1,"\u0078":2}`))).Records[0].Disposition != Rejected {
		t.Fatal("escaped duplicate key admitted")
	}
	for _, name := range []string{"token", "UID", "rUID", "private_account", "AUTH-header", "password", "full_path"} {
		input := batch(record(""), record(`,"extension":{"`+name+`":"generated-sensitive-marker"}`))
		r, err := Parse(context.Background(), input)
		if !errors.Is(err, ErrSensitive) || !reflect.DeepEqual(r, Report{}) || strings.Contains(err.Error(), "marker") {
			t.Fatalf("sensitive batch retained/hashed: %s %v %+v", name, err, r)
		}
		var sensitive SensitiveBatchError
		if !errors.As(err, &sensitive) || sensitive.RecordIndex != 1 {
			t.Fatal("missing safe record coordinate")
		}
	}
	// Sensitive values behind duplicate keys must not escape through map decoding.
	r, err := Parse(context.Background(), batch(record(`,"extension":{},"extension":{"token":"marker"}`)))
	if !errors.Is(err, ErrSensitive) || !reflect.DeepEqual(r, Report{}) {
		t.Fatal("duplicate concealed sensitive field")
	}
}

func TestCatalogImportBounds(t *testing.T) {
	for _, n := range []int{1024, 1025} {
		values := strings.TrimSuffix(strings.Repeat("7,", n), ",")
		got := parsed(t, batch(record(`,"legacy_audio_ids":[`+values+`]`))).Records[0]
		if len(got.LegacyAudioIDs) == 0 && n == 1024 || len(got.LegacyAudioIDs) != 0 && n == 1025 {
			t.Fatal("candidate array limit")
		}
		if got.Disposition != Rejected {
			t.Fatal("missing array side not rejected")
		}
	}
	for _, n := range []int{128, 129} {
		input := batch(`{"record":"` + strings.Repeat("r", n) + `","kind":"public","title":"Generated"}`)
		got := parsed(t, input).Records[0].Disposition
		if (got == AcceptedForStaging) != (n == 128) {
			t.Fatal("locator bound")
		}
	}
	for _, n := range []int{4096, 4097} {
		got := parsed(t, batch(`{"record":"r","kind":"public","title":"`+strings.Repeat("t", n)+`"}`)).Records[0].Disposition
		if (got == AcceptedForStaging) != (n == 4096) {
			t.Fatal("string bound")
		}
	}
	for _, id := range []string{"4294967295", "4294967296", "18446744073709551615", "18446744073709551616", "-1", "1.0", "1e2", "\"7\"", "0"} {
		got := parsed(t, batch(record(`,"pairs":[{"audio_id":`+id+`,"sha1":"`+h1+`"}]`))).Records[0].Disposition
		want := Rejected
		if id == "4294967295" {
			want = AcceptedForStaging
		}
		if id == "4294967296" || id == "18446744073709551615" {
			want = Unsupported
		}
		if got != want {
			t.Fatalf("width %s: %s", id, got)
		}
	}
	for _, n := range []int{32, 33} {
		var records []string
		for i := 0; i < n; i++ {
			records = append(records, `{"record":"r`+strconv.Itoa(i)+`","kind":"public","title":"Generated"}`)
		}
		r, err := Parse(context.Background(), batch(records...))
		if n == 32 && (err != nil || len(r.Records) != 32) || n == 33 && (err != ErrEnvelope || len(r.Records) != 0) {
			t.Fatal("record count bound", err)
		}
	}
	// Exact encoded byte limits include all whitespace and escaping.
	for _, n := range []int{int(catalog.RecordBytes.Maximum()), int(catalog.RecordBytes.Maximum()) + 1} {
		base := record("")
		raw := base[:len(base)-1] + strings.Repeat(" ", n-len(base)) + "}"
		got := parsed(t, batch(raw)).Records[0].Disposition
		if (got == AcceptedForStaging) != (n == int(catalog.RecordBytes.Maximum())) {
			t.Fatal("record byte bound")
		}
	}
	for _, n := range []int{1 << 20, (1 << 20) + 1} {
		input := batch(record(""))
		input = append(input, bytes.Repeat([]byte{' '}, n-len(input))...)
		r, err := Parse(context.Background(), input)
		if n == 1<<20 && err != nil || n > 1<<20 && (err != ErrEnvelope || len(r.Records) != 0) {
			t.Fatal("batch byte bound", err)
		}
	}
	for _, fields := range []int{256, 257} {
		extra := ""
		for i := 3; i < fields; i++ {
			extra += `,"f` + strconv.Itoa(i) + `":0`
		}
		got := parsed(t, batch(record(extra))).Records[0].Disposition
		if fields == 256 && got != Unsupported || fields == 257 && got != Rejected {
			t.Fatal("field count bound", got)
		}
	}
	for _, depth := range []int{16, 17} {
		extra := `,"extension":` + strings.Repeat("[", depth-1) + "0" + strings.Repeat("]", depth-1)
		r, err := Parse(context.Background(), batch(record(extra)))
		if depth == 16 && (err != nil || r.Records[0].Disposition != Unsupported) || depth == 17 && err == nil && r.Records[0].Disposition != Rejected {
			t.Fatal("depth bound", err)
		}
	}
	for _, input := range [][]byte{[]byte(`{"format":"future","records":[]}`), append(batch(record("")), []byte("{}")...), []byte(`{"format":"catalog-offline-v1","format":"catalog-offline-v1","records":[]}`), append(batch(record("")), 0xff), batch(`null`)} {
		r, err := Parse(context.Background(), input)
		if err == nil && r.Records[0].Disposition != Rejected {
			t.Fatal("malformed envelope/record accepted")
		}
		if err != nil && !reflect.DeepEqual(r, Report{}) {
			t.Fatal("partial report escaped")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := Parse(ctx, batch(record(""))); err != ErrCanceled || !reflect.DeepEqual(r, Report{}) {
		t.Fatal("canceled parse retained bytes")
	}
}
