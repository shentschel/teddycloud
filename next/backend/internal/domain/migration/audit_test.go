package migration

import (
	"reflect"
	"strings"
	"testing"
)

func TestAuditSharedMetadataNeverMergesOriginalAndCustom(t *testing.T) {
	original := fixture(t, 41, "shared-original")
	custom := fixture(t, 42, "shared-custom")
	custom.Classification = CustomCard
	custom.Title = original.Title
	custom.Model = original.Model
	custom.Article = original.Article
	custom.Pairs = append([]Pair(nil), original.Pairs...)

	results, err := Run([]Record{custom, original})
	if err != nil || len(results) != 2 {
		t.Fatal("shared metadata migration failed")
	}
	seen := map[string]bool{}
	for _, result := range results {
		if result.Status() != Accepted {
			t.Fatal("shared title/model/fingerprint created ambiguity or rejection")
		}
		content, ok := result.Content()
		if !ok || seen[content.ID().String()] {
			t.Fatal("distinct editorial identities were merged")
		}
		seen[content.ID().String()] = true
		if result.Raw().Source.Record() == "shared-custom" && !result.Protected() {
			t.Fatal("Custom classification was discarded")
		}
	}
}

func TestAuditParallelArraysAndTracksRemainUntrustedEvidence(t *testing.T) {
	record := fixture(t, 43, "legacy-arrays")
	record.Pairs = nil
	record.AudioIDs = []string{"100", "200"}
	record.Hashes = []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	record.Tracks = []string{"Synthetic member A", "Synthetic member B"}
	want := clone(record)

	results, err := Run([]Record{record})
	if err != nil || len(results) != 1 {
		t.Fatal("legacy evidence migration failed")
	}
	result := results[0]
	if result.Status() != Ambiguous || !reflect.DeepEqual(result.Raw(), want) {
		t.Fatal("parallel arrays were paired or raw evidence was lost")
	}
	if _, ok := result.Version(); ok {
		t.Fatal("parallel arrays created a playable version")
	}
	if _, ok := result.Set(); ok {
		t.Fatal("track titles created Set membership")
	}
	found := false
	for _, issue := range result.Issues() {
		if issue.Reason == "unproven-audio-hash-pairing" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing explicit parallel-array review reason")
	}
}

func TestAuditClassificationFlagsStayIndependent(t *testing.T) {
	for i, flags := range []Flags{
		{},
		{Valid: true},
		{Exists: true},
		{Claimed: true},
		{CloudAuth: true},
		{NoCloud: true},
		{Valid: true, Exists: true, Claimed: true, CloudAuth: true, NoCloud: true},
	} {
		record := fixture(t, 50+i, "flag-combination")
		record.Classification = CustomCard
		record.Flags = flags
		results, err := Run([]Record{record})
		if err != nil || len(results) != 1 || !results[0].Protected() {
			t.Fatal("legacy flags changed explicit Custom classification")
		}
	}
}
