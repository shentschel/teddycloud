package migration

import (
	assignment "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
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

func TestAssignmentMigrationCompatibility(t *testing.T) {
	// Existing status/reason/raw vectors remain the compatibility oracle.
	TestSanitizedMigrationLosslessDeterministic(t)
	for _, class := range []Classification{Unclassified, Original, CustomCard} {
		raw := classificationFixture(t, 70, class)
		before := clone(raw)
		results, err := Run([]Record{raw})
		if err != nil || !reflect.DeepEqual(results[0].Raw(), before) {
			t.Fatal("raw fixture changed", err)
		}
		result := results[0]
		state, err := assignment.NewTagEvidence(raw.TagID)
		if err != nil {
			t.Fatal(err)
		}
		auth, err := assignment.BindAuthenticatedReviewAuthority("fixture-reviewer")
		if err != nil {
			t.Fatal(err)
		}
		rid, _ := assignment.ParseReviewID("rev_00000000000000000000000001")
		eid, _ := assignment.ParseEvidenceID("evd_00000000000000000000000001")
		request, err := assignment.NewReviewRequest(rid, 0, 1, "explicit fixture review")
		if err != nil {
			t.Fatal(err)
		}
		for _, disposition := range []assignment.EvidenceDisposition{assignment.PendingEvidence, assignment.DisputedEvidence, assignment.RejectedEvidence, assignment.AcceptedEvidence} {
			got, err := result.AdaptClassification(state, auth, request, eid, disposition)
			if class == Unclassified {
				if err != assignment.ErrInvalidEvidence || !reflect.DeepEqual(got, state) || result.Status() != ReviewRequired {
					t.Fatal("unresolved classification promoted", err)
				}
				continue
			}
			want := assignment.Unknown
			if disposition == assignment.AcceptedEvidence {
				want = class
			}
			if err != nil || got.Classification() != want || got.Records()[0].Evidence.Kind() != assignment.ImportedAssertion {
				t.Fatal("adapter disagrees with domain", err)
			}
			if result.Status() != Accepted || result.Protected() != (class == CustomCard) || !reflect.DeepEqual(result.Raw(), before) {
				t.Fatal("review rewrote legacy outcome")
			}
		}
		if class != Unclassified {
			if got, err := result.AdaptClassification(state, assignment.ReviewAuthority{}, request, eid, assignment.AcceptedEvidence); err != assignment.ErrReviewAuthority || !reflect.DeepEqual(got, state) {
				t.Fatal("raw source minted authority")
			}
			foreign, _ := identity.ParseTagID("tag_00000000000000000000000099")
			other, _ := assignment.NewTagEvidence(foreign)
			if got, err := result.AdaptClassification(other, auth, request, eid, assignment.AcceptedEvidence); err != assignment.ErrEvidenceSubject || !reflect.DeepEqual(got, other) {
				t.Fatal("cross-subject fixture review")
			}
			for _, mutation := range []func(*Record){func(r *Record) { r.TAF = Missing }, func(r *Record) { r.AudioIDs = []string{"1"}; r.Hashes = []string{"x"} }, func(r *Record) { r.UID = "invalid" }} {
				unresolved := clone(raw)
				mutation(&unresolved)
				reports, err := Run([]Record{unresolved})
				if err != nil {
					t.Fatal(err)
				}
				got, err := reports[0].AdaptClassification(state, auth, request, eid, assignment.AcceptedEvidence)
				if reports[0].Status() == Accepted || err != assignment.ErrInvalidEvidence || !reflect.DeepEqual(got, state) || !reflect.DeepEqual(reports[0].Raw(), unresolved) {
					t.Fatal("unresolved outcome lost", err)
				}
			}
		}
	}
}
