package catalog

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func testArtifact(t *testing.T, source SourceRegistration, ingest IngestCapability) Artifact {
	t.Helper()
	id, err := NewBatchID()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := NewArtifact(ingest, source, ArtifactFacts{
		ID:		id,
		SHA256:		[32]byte{1},
		ByteLength:	100,
		RecordCount:	2,
		ParserRevision:	"offline/1",
		SourceRevision:	"opaque-z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func testObservation(t *testing.T, source SourceRegistration, ingest IngestCapability, suffix, title string) CatalogObservation {
	t.Helper()
	artifact := testArtifact(t, source, ingest)
	snapshot, err := NewRecordSnapshot([]byte(`{"unsupported":"retained"}`))
	if err != nil {
		t.Fatal(err)
	}
	coordinate, err := NewRecordCoordinate(artifact, 1, "https://invalid.example/opaque", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := ContentSubject(mustContentID(t, "cnt_"+idPayloadA))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := TitleClaim(title)
	if err != nil {
		t.Fatal(err)
	}
	id, err := evidence.ParseID("obs_" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := NewCatalogObservation(ingest, source, ObservationInput{
		ID:		id,
		Subject:	subject,
		Claim:		claim,
		Coordinate:	coordinate,
		AssertedOrigin:	"claimed publisher",
		ObservedAt:	time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("offset", 3600)),
		Confidence:	evidence.ConfidenceUnknown,
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func TestCatalogProvenanceAndConfidence(t *testing.T) {
	source, ingest := testRegistration(t, OfflineCustom, TitleClaimKey)
	observation := testObservation(t, source, ingest, idPayloadA, "Candidate")
	input := observation.Input()
	if input.AssertedOrigin == source.Issuer().String() || input.Coordinate.Artifact().Source() != source {
		t.Fatal("asserted origin replaced acquiring issuer")
	}
	if source.Spec().Acquisition != OfflineCustom || input.Coordinate.Index() != 1 || input.Coordinate.Locator() != "https://invalid.example/opaque" {
		t.Fatal("acquisition/coordinate lost")
	}
	if input.ObservedAt.Location() != time.UTC || input.Confidence != evidence.ConfidenceUnknown {
		t.Fatal("time/confidence not canonical")
	}
	for _, confidence := range []evidence.Confidence{evidence.ConfidenceUnknown, evidence.Tentative, evidence.Corroborated} {
		input.Confidence = confidence
		if _, err := NewCatalogObservation(IngestCapability{}, source, input); !errors.Is(err, ErrCatalogUnauthorized) {
			t.Fatal("confidence granted ingestion authority")
		}
		candidate, err := NewCatalogObservation(ingest, source, input)
		if err != nil || candidate.Input().Claim != observation.Input().Claim {
			t.Fatal("confidence changed claim")
		}
	}
	input.Confidence = 255
	if _, err := NewCatalogObservation(ingest, source, input); err == nil {
		t.Fatal("unknown confidence admitted")
	}
	input = observation.Input()
	input.AssertedOrigin = "changed"
	if observation.Input().AssertedOrigin != "claimed publisher" {
		t.Fatal("observation was mutable")
	}
	input.ObservedAt = time.Time{}
	if _, err := NewCatalogObservation(ingest, source, input); err == nil {
		t.Fatal("zero time admitted")
	}
	input = observation.Input()
	input.ID = evidence.ID{}
	if _, err := NewCatalogObservation(ingest, source, input); err == nil {
		t.Fatal("zero observation ID admitted")
	}
	input = observation.Input()
	input.Coordinate = RecordCoordinate{}
	if _, err := NewCatalogObservation(ingest, source, input); err == nil {
		t.Fatal("zero coordinate admitted")
	}
}

func TestCatalogObservationIdentityAndBindings(t *testing.T) {
	source, ingest := testRegistration(t, CoreOperator, TitleClaimKey, FingerprintClaimKey, OwnershipClaimKey)
	other, otherIngest := testRegistration(t, CoreOperator, TitleClaimKey)
	observation := testObservation(t, source, ingest, idPayloadA, "Candidate")
	if err := CheckObservationIdentity(observation, observation); err != nil {
		t.Fatal(err)
	}
	input := observation.Input()
	input.ObservedAt = input.ObservedAt.In(time.FixedZone("other", -7200))
	same, err := NewCatalogObservation(ingest, source, input)
	if err != nil || same != observation {
		t.Fatal("equal instants did not canonicalize")
	}
	otherSubject, _ := ContentSubject(mustContentID(t, "cnt_1123456789abcdefghjkmnpqrs"))
	if err := ValidateObservationBinding(observation, otherSubject, source); !errors.Is(err, ErrCatalogSubjectMismatch) {
		t.Fatal("cross-subject support admitted")
	}
	if err := ValidateObservationBinding(observation, input.Subject, other); !errors.Is(err, ErrCatalogSourceMismatch) {
		t.Fatal("cross-source support admitted")
	}
	if _, err := NewCatalogObservation(otherIngest, other, input); !errors.Is(err, ErrCatalogSourceMismatch) {
		t.Fatal("coordinate rebound to different source")
	}
	changes := []func(*ObservationInput){
		func(i *ObservationInput) { i.AssertedOrigin = "official" },
		func(i *ObservationInput) { i.Confidence = evidence.Corroborated },
		func(i *ObservationInput) { i.ObservedAt = i.ObservedAt.Add(time.Second) },
		func(i *ObservationInput) { i.Subject = otherSubject },
		func(i *ObservationInput) { i.Claim, _ = TitleClaim("Changed") },
		func(i *ObservationInput) {
			i.Coordinate, _ = NewRecordCoordinate(i.Coordinate.Artifact(), 0, "other", i.Coordinate.Snapshot())
		},
	}
	for _, change := range changes {
		changed := observation.Input()
		change(&changed)
		candidate, err := NewCatalogObservation(ingest, source, changed)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckObservationIdentity(observation, candidate); !errors.Is(err, ErrCatalogObservationConflict) {
			t.Fatal("changed same-ID payload admitted")
		}
		for _, pair := range [][]CatalogObservation{{observation, candidate}, {candidate, observation}} {
			if _, err := CanonicalCatalogObservations(pair); !errors.Is(err, ErrCatalogObservationConflict) {
				t.Fatal("same-ID conflict depended on arrival order")
			}
		}
	}
	if err := CheckObservationIdentity(CatalogObservation{}, observation); err == nil {
		t.Fatal("zero payload admitted")
	}
	version, err := identity.ParseContentVersionID("ver_" + idPayloadA)
	if err != nil {
		t.Fatal(err)
	}
	versionSubject, _ := VersionSubject(input.Subject.ContentID(), version)
	owner, _ := OwnershipClaim(otherSubject.ContentID())
	input.Subject = versionSubject
	input.Claim = owner
	if _, err := NewCatalogObservation(ingest, source, input); !errors.Is(err, ErrCatalogSubjectMismatch) {
		t.Fatal("cross-Content ownership admitted")
	}
	input.Claim, _ = OwnershipClaim(versionSubject.ContentID())
	if _, err := NewCatalogObservation(ingest, source, input); err != nil {
		t.Fatal(err)
	}
	widePair := mustFingerprint(t, mustAudioID(t, math.MaxUint64), mustAudioHash(t, strings.Repeat("A", 40)))
	input.Claim, _ = FingerprintClaim(widePair)
	wide, err := NewCatalogObservation(ingest, source, input)
	if err != nil || wide.Input().Claim.Fingerprint() != widePair {
		t.Fatal("wide fingerprint candidate was narrowed")
	}
	input.Subject = observation.Input().Subject
	if _, err := NewCatalogObservation(ingest, source, input); !errors.Is(err, ErrCatalogSubjectMismatch) {
		t.Fatal("fingerprint admitted on Content instead of version")
	}
}

func TestCatalogCanonicalEquality(t *testing.T) {
	source, ingest := testRegistration(t, OfflinePublic, TitleClaimKey)
	a := testObservation(t, source, ingest, idPayloadA, "A")
	b := testObservation(t, source, ingest, "1123456789abcdefghjkmnpqrs", "B")
	c := testObservation(t, source, ingest, "2123456789abcdefghjkmnpqrs", "A")
	want := []CatalogObservation{a, b, c}
	for _, input := range [][]CatalogObservation{
		{a, b, c}, {a, c, b}, {b, a, c}, {b, c, a}, {c, a, b}, {c, b, a}, {c, b, a, a},
	} {
		got, err := CanonicalCatalogObservations(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("candidate set changed under permutation: %v", err)
		}
		got[0] = CatalogObservation{}
		if input[0].ID().IsZero() {
			t.Fatal("canonical result shares mutable slice")
		}
	}
	atLimit := make([]CatalogObservation, 4096)
	for i := range atLimit {
		atLimit[i] = a
	}
	if got, err := CanonicalCatalogObservations(atLimit); err != nil || len(got) != 1 {
		t.Fatal("exact observation count rejected")
	}
	if _, err := CanonicalCatalogObservations(append(atLimit, a)); err == nil {
		t.Fatal("duplicate count bypassed admission")
	}
	if _, err := CanonicalCatalogObservations([]CatalogObservation{{}}); err == nil {
		t.Fatal("invalid observation admitted")
	}
}

func TestCatalogArtifactIdentityAndBounds(t *testing.T) {
	source, ingest := testRegistration(t, OfflinePublic, TitleClaimKey)
	artifact := testArtifact(t, source, ingest)
	for _, change := range []func(*ArtifactFacts){
		func(f *ArtifactFacts) { f.SHA256[0]++ },
		func(f *ArtifactFacts) { f.ByteLength++ },
		func(f *ArtifactFacts) { f.RecordCount++ },
		func(f *ArtifactFacts) { f.ParserRevision = "other" },
		func(f *ArtifactFacts) { f.SourceRevision = "other" },
	} {
		facts := artifact.Facts()
		change(&facts)
		changed, err := NewArtifact(ingest, source, facts)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckArtifactIdentity(artifact, changed); !errors.Is(err, ErrCatalogArtifactConflict) {
			t.Fatal("changed same-batch-ID data admitted")
		}
	}
	if err := CheckArtifactIdentity(artifact, artifact); err != nil {
		t.Fatal(err)
	}
	otherSource, otherIngest := testRegistration(t, OfflinePublic, TitleClaimKey)
	rebound, err := NewArtifact(otherIngest, otherSource, artifact.Facts())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckArtifactIdentity(artifact, rebound); !errors.Is(err, ErrCatalogArtifactConflict) {
		t.Fatal("batch source rebind admitted")
	}
	if err := CheckArtifactIdentity(Artifact{}, artifact); err == nil {
		t.Fatal("zero artifact admitted")
	}
	for _, change := range []func(*ArtifactFacts){
		func(f *ArtifactFacts) { f.ID = BatchID{} },
		func(f *ArtifactFacts) { f.SHA256 = [32]byte{} },
		func(f *ArtifactFacts) { f.ByteLength = 0 },
		func(f *ArtifactFacts) { f.ByteLength = BatchBytes.Maximum() + 1 },
		func(f *ArtifactFacts) { f.RecordCount = 0 },
		func(f *ArtifactFacts) { f.RecordCount = BatchRecords.Maximum() + 1 },
		func(f *ArtifactFacts) { f.ParserRevision = strings.Repeat("x", 129) },
		func(f *ArtifactFacts) { f.SourceRevision = strings.Repeat("x", 129) },
	} {
		facts := artifact.Facts()
		change(&facts)
		if _, err := NewArtifact(ingest, source, facts); err == nil {
			t.Fatal("invalid artifact admitted")
		}
	}
	facts := artifact.Facts()
	facts.ByteLength = BatchBytes.Maximum()
	facts.RecordCount = BatchRecords.Maximum()
	facts.ParserRevision = strings.Repeat("x", 128)
	facts.SourceRevision = strings.Repeat("x", 128)
	boundary, err := NewArtifact(ingest, source, facts)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := NewRecordSnapshot([]byte("{}"))
	if _, err := NewRecordCoordinate(boundary, 31, strings.Repeat("x", 128), snapshot); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		index		uint64
		locator		string
		snapshot	RecordSnapshot
	}{
		{32, "record", snapshot},
		{0, strings.Repeat("x", 129), snapshot},
		{0, "record", RecordSnapshot{}},
	} {
		if _, err := NewRecordCoordinate(boundary, attempt.index, attempt.locator, attempt.snapshot); err == nil {
			t.Fatal("invalid coordinate admitted")
		}
	}
}

func TestCatalogTypedClaimValidation(t *testing.T) {
	if _, err := TitleClaim(strings.Repeat("x", 4096)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", strings.Repeat("x", 4097), "a\nb", string([]byte{0xff})} {
		if _, err := TitleClaim(invalid); err == nil {
			t.Fatal("invalid title admitted")
		}
	}
	if _, err := ModelClaim(ModelNumber{}); err == nil {
		t.Fatal("zero model admitted")
	}
	if _, err := ArticleClaim(ArticleNumber{}); err == nil {
		t.Fatal("zero article admitted")
	}
	if _, err := FingerprintClaim(AudioFingerprint{}); err == nil {
		t.Fatal("split/zero fingerprint admitted")
	}
	if _, err := OwnershipClaim(identity.ContentID{}); err == nil {
		t.Fatal("zero owner admitted")
	}
	model, _ := ParseModelNumber("01-0100")
	article, _ := ParseArticleNumber("11000289")
	modelClaim, _ := ModelClaim(model)
	articleClaim, _ := ArticleClaim(article)
	if modelClaim.Key() == articleClaim.Key() || modelClaim.Text() != model.String() || articleClaim.Text() != article.String() {
		t.Fatal("model and article facts conflated")
	}
	if _, err := ContentSubject(identity.ContentID{}); err == nil {
		t.Fatal("zero subject admitted")
	}
	if _, err := VersionSubject(mustContentID(t, "cnt_"+idPayloadA), identity.ContentVersionID{}); err == nil {
		t.Fatal("zero version subject admitted")
	}
}
