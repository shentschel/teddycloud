package migration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/set"
)

// All identities, titles and fingerprints below are synthetic. No source paths,
// provider tokens, certificates or household identifiers are fixture inputs.
// IDs are fixed allocations, independent of titles, models and fingerprints.
func fixture(t *testing.T, n int, name string) Record {
	t.Helper()
	payload := fmt.Sprintf("%026d", n)
	source, err := evidence.NewSource("sanitized-fixture", "r1", name)
	if err != nil {
		t.Fatal(err)
	}
	content, err := identity.ParseContentID("cnt_" + payload)
	if err != nil {
		t.Fatal(err)
	}
	version, err := identity.ParseContentVersionID("ver_" + payload)
	if err != nil {
		t.Fatal(err)
	}
	return Record{Source: source, ContentID: content, VersionID: version, Title: "Synthetic shared title", Model: "TEST-MODEL",
		Classification: Original, TAF: Present, Upstream: Present, Pairs: []Pair{{"100", strings.Repeat("a", 40)}},
		Tracks: []string{"Synthetic track one", "Synthetic track two"}}
}
func tagFixture(t *testing.T, r Record, n int, uid string) Record {
	t.Helper()
	id, err := identity.ParseTagID(fmt.Sprintf("tag_%026d", n))
	if err != nil {
		t.Fatal(err)
	}
	r.TagID = id
	r.UID = uid
	return r
}
func fixtures(t *testing.T) []Record {
	t.Helper()
	normal := tagFixture(t, fixture(t, 1, "normal-original"), 1, "11:22:33:44:55:66:77:88")
	custom := tagFixture(t, fixture(t, 2, "custom-card"), 2, "22:22:33:44:55:66:77:88")
	custom.Classification = CustomCard
	custom.Flags = Flags{Valid: true, Exists: false, Claimed: true, CloudAuth: false, NoCloud: true}
	duplicateA := tagFixture(t, fixture(t, 3, "duplicate-a"), 3, "AB:CD:11:22:33:44:55:66")
	duplicateB := tagFixture(t, fixture(t, 4, "duplicate-b"), 4, "ab:cd:11:22:33:44:55:66")
	duplicateB.UID = ""
	duplicateB.RUID = "665544332211cdab"
	duplicateB.Classification = CustomCard
	missing := fixture(t, 5, "missing-taf-source")
	missing.TAF = Missing
	missing.Upstream = Missing
	crossed := fixture(t, 6, "crossed-arrays")
	crossed.Pairs = nil
	crossed.AudioIDs = []string{"100", "200"}
	crossed.Hashes = []string{strings.Repeat("b", 40), strings.Repeat("a", 40)}
	unequal := fixture(t, 7, "unequal-arrays")
	unequal.AudioIDs = []string{"100"}
	unequal.Hashes = nil
	conflict := fixture(t, 8, "conflicting-observations")
	for i, value := range []string{"original", "custom"} {
		id, _ := evidence.ParseID(fmt.Sprintf("obs_%026d", i+1))
		source, _ := evidence.NewSource("synthetic-evidence", fmt.Sprintf("r%d", i+1), "classification")
		o, err := evidence.NewObservation(id, source, evidence.Claim{Subject: conflict.ContentID.String(), Key: "classification", Value: value}, time.Unix(1, 0), evidence.Corroborated, evidence.Accepted)
		if err != nil {
			t.Fatal(err)
		}
		conflict.Observations = append(conflict.Observations, o)
	}
	records := []Record{normal, custom, duplicateA, duplicateB, missing, crossed, unequal, conflict}
	source, _ := evidence.NewSource("sanitized-fixture", "r1", "four-member-set")
	id, _ := set.ParseID("set_00000000000000000000000001")
	grouping := Record{Source: source, SetID: id, Title: "Synthetic Set", Article: "TEST-ARTICLE", TAF: Present, Upstream: Present}
	for i := 0; i < 4; i++ {
		member := fixture(t, 10+i, fmt.Sprintf("set-member-%d", i))
		member.Model = ""
		member.Article = ""
		records = append(records, member)
		grouping.Members = append(grouping.Members, set.Member{Position: uint32(3 - i), ContentID: member.ContentID})
	}
	return append(records, grouping)
}
