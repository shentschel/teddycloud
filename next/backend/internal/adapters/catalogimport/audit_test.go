package catalogimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"
)

func TestCatalogParallelArrayRegression(t *testing.T) {
	for _, audio := range []string{"[7,9]", "[9,7]"} {
		for _, hashes := range []string{`["` + h1 + `","` + h2 + `"]`, `["` + h2 + `","` + h1 + `"]`, `["` + h1 + `"]`} {
			raw := batch(record(`,"legacy_audio_ids":` + audio + `,"legacy_sha1s":` + hashes))
			report := parsed(t, raw)
			r := report.Records[0]
			want := Ambiguous
			if hashes == `["`+h1+`"]` {
				want = Rejected
			}
			if r.Disposition != want || len(r.Pairs) != 0 || !bytes.Contains(r.SnapshotBytes(), []byte(audio)) || !bytes.Contains(r.SnapshotBytes(), []byte(hashes)) {
				t.Fatal("positional pairing, truncation or raw coordinate loss")
			}
			if _, err := report.CatalogImportPayload(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Mutating a valid report to fabricate an explicit winner cannot bypass
			// the adapter's revalidation of the original unproven array artifact.
			report.Records[0].Pairs = []Pair{{AudioID: 7, SHA1: h1}}
			report.Records[0].Disposition = AcceptedForStaging
			if _, err := report.CatalogImportPayload(t.Context()); err == nil {
				t.Fatal("mutated report minted authoritative pairing")
			}
		}
	}
	for _, reverse := range []bool{false, true} {
		public := fmt.Sprintf(`{"record":"p","kind":"public","pairs":[{"audio_id":7,"sha1":%q}]}`, h1)
		custom := fmt.Sprintf(`{"record":"c","kind":"custom","pairs":[{"audio_id":7,"sha1":%q}]}`, h1)
		rows := []string{public, custom}
		if reverse {
			rows[0], rows[1] = rows[1], rows[0]
		}
		report := parsed(t, batch(rows...))
		if len(report.Records) != 2 {
			t.Fatal("custom/public deduplicated")
		}
		for _, r := range report.Records {
			if r.Disposition != Ambiguous || len(r.Pairs) != 1 {
				t.Fatal("custom-first or public-first winner")
			}
		}
	}
}

func FuzzCatalogImportEnvelope(f *testing.F) {
	for _, seed := range [][]byte{batch(record("")), batch(record(`,"legacy_audio_ids":[7,9],"legacy_sha1s":["` + h2 + `","` + h1 + `"]`)), batch(record(`,"extension":{"token":"synthetic-secret"}`)), []byte(`{"format":"catalog-offline-v1","records":null}`)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		// Bound individual fuzz iterations as well as the plan's wall-clock budget.
		if len(raw) > 65536 {
			return
		}
		report, err := Parse(t.Context(), raw)
		if err != nil {
			if !reflect.DeepEqual(report, Report{}) {
				t.Fatal("failed parse retained partial artifact/digest")
			}
			return
		}
		again, err := Parse(t.Context(), raw)
		if err != nil || !reflect.DeepEqual(report, again) || report.Digest != sha256.Sum256(raw) || !bytes.Equal(report.ArtifactBytes(), raw) {
			t.Fatal("nondeterministic or changed artifact")
		}
		if len(report.Records) == 0 || len(report.Records) > 32 {
			t.Fatal("record bound lost")
		}
		for i, r := range report.Records {
			if r.Index != i || r.Disposition == "" {
				t.Fatal("incomplete report")
			}
		}
		if _, err := report.CatalogImportPayload(t.Context()); err != nil {
			t.Fatal("valid parser report cannot be retained", err)
		}
		report.Digest[0] ^= 1
		if _, err := report.CatalogImportPayload(t.Context()); err == nil {
			t.Fatal("digest mutation accepted")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		canceled, err := Parse(ctx, raw)
		if err == nil || !reflect.DeepEqual(canceled, Report{}) {
			t.Fatal("canceled parse retained input")
		}
	})
}
