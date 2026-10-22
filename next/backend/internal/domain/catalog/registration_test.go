package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// Complete expanded normative vectors, independent of catalogEncoder.
const registrationContentGolden = "0000000000000013706930382d726567697374726174696f6e2f31000000000000000e706930382d636174616c6f672f3100000000000000" +
	"0100000000000000203030303030303030303030303030303030303030303030303030303030303033000000000000001e636e745f303030" +
	"3030303030303030303030303030303030303030303031000000000000000000000000000000000000000000000000000000000000002030" +
	"3030303030303030303030303030303030303030303030303030303030303400000000000000203030303030303030303030303030303030" +
	"3030303030303030303030303030350000000000000020303030303030303030303030303030303030303030303030303030303030303600" +
	"000000000000026f700000000000000001310000000000000007636174616c6f670000000000000003000000000000000200000000000000" +
	"0100000000000000050000000000000020303030303030303030303030303030303030303030303030303030303030303700000000000000" +
	"0200000000000000203030303030303030303030303030303030303030303030303030303030303034000000000000002030303030303030" +
	"3030303030303030303030303030303030303030303030303500000000000000203030303030303030303030303030303030303030303030" +
	"30303030303030303600000000000000026f700000000000000001310000000000000007636174616c6f6700000000000000030000000000" +
	"0000020000000000000001000000000000000500000000000000203030303030303030303030303030303030303030303030303030303030" +
	"3030610000000000000020303030303030303030303030303030303030303030303030303030303030306200000000000000203030303030" +
	"30303030303030303030303030303030303030303030303030306300000000000000076f66666c696e650000000000000001310000000000" +
	"000007636174616c6f6700000000000000010000000000000002000000000000000100000000000000040000000000000006637265617465" +
	"0000000000000001000000000000001e6f62735f3030303030303030303030303030303030303030303030303039000000000000001e636e" +
	"745f303030303030303030303030303030303030303030303030303100000000000000000000000000000001000000000000000141000000" +
	"0000000000000000000000000000000000000000000000000000000020303030303030303030303030303030303030303030303030303030" +
	"3030303061000000000000002030303030303030303030303030303030303030303030303030303030303030620000000000000020303030" +
	"303030303030303030303030303030303030303030303030303030306300000000000000076f66666c696e65000000000000000131000000" +
	"0000000007636174616c6f670000000000000001000000000000000200000000000000010000000000000004000000000000002030303030" +
	"3030303030303030303030303030303030303030303030303030303844136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f6" +
	"1caaff8a00000000000000020000000000000001000000000000000131000000000000000000000000000000000000000000000001300000" +
	"0000000000027b7d00000000000000000000000000000014323032362d30312d30315430303a30303a30305a000000000000000000000000" +
	"000000010000000000000001000000000000001e6f62735f3030303030303030303030303030303030303030303030303039"

const registrationContentResultGolden = "000000000000001a706930382d726567697374726174696f6e2d726573756c742f3100000000000000010000000000000020303030303030" +
	"3030303030303030303030303030303030303030303030303033000000000000001e636e745f303030303030303030303030303030303030" +
	"3030303030303031000000000000000000000000000000010000000000000001000000000000000100000000000000000000000000000003" +
	"0000000000000001000000000000000100000000000000010000000000000001000000000000000141000000000000000000000000000000" +
	"0000000000000000000000000000000001000000000000001e6f62735f303030303030303030303030303030303030303030303030303900" +
	"0000000000000200000000000000000000000000000000000000000000000300000000000000000000000000000000"

const registrationVersionGolden = "0000000000000013706930382d726567697374726174696f6e2f31000000000000000e706930382d636174616c6f672f3100000000000000" +
	"0200000000000000203030303030303030303030303030303030303030303030303030303030303064000000000000001e636e745f303030" +
	"3030303030303030303030303030303030303030303031000000000000001e7665725f303030303030303030303030303030303030303030" +
	"3030303032000000000000000100000000000000010000000000000020303030303030303030303030303030303030303030303030303030" +
	"3030303034000000000000002030303030303030303030303030303030303030303030303030303030303030350000000000000020303030" +
	"303030303030303030303030303030303030303030303030303030303600000000000000026f700000000000000001310000000000000007" +
	"636174616c6f6700000000000000030000000000000002000000000000000100000000000000050000000000000020303030303030303030" +
	"3030303030303030303030303030303030303030303037000000000000000200000000000000203030303030303030303030303030303030" +
	"3030303030303030303030303030340000000000000020303030303030303030303030303030303030303030303030303030303030303500" +
	"00000000000020303030303030303030303030303030303030303030303030303030303030303600000000000000026f7000000000000000" +
	"01310000000000000007636174616c6f67000000000000000300000000000000020000000000000001000000000000000500000000000000" +
	"2030303030303030303030303030303030303030303030303030303030303030610000000000000020303030303030303030303030303030" +
	"3030303030303030303030303030303062000000000000002030303030303030303030303030303030303030303030303030303030303030" +
	"6300000000000000076f66666c696e650000000000000001310000000000000007636174616c6f6700000000000000010000000000000002" +
	"00000000000000010000000000000004000000000000000776657273696f6e0000000000000002000000000000001e6f62735f3030303030" +
	"303030303030303030303030303030303030303065000000000000001e636e745f3030303030303030303030303030303030303030303030" +
	"303031000000000000001e7665725f3030303030303030303030303030303030303030303030303032000000000000000400000000000000" +
	"0000000000000000010000000000000028313131313131313131313131313131313131313131313131313131313131313131313131313131" +
	"3100000000000000000000000000000020303030303030303030303030303030303030303030303030303030303030306100000000000000" +
	"2030303030303030303030303030303030303030303030303030303030303030620000000000000020303030303030303030303030303030" +
	"303030303030303030303030303030306300000000000000076f66666c696e650000000000000001310000000000000007636174616c6f67" +
	"0000000000000001000000000000000200000000000000010000000000000004000000000000002030303030303030303030303030303030" +
	"3030303030303030303030303030306644136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a0000000000000002" +
	"00000000000000010000000000000001310000000000000000000000000000000000000000000000013000000000000000027b7d00000000" +
	"000000000000000000000014323032362d30312d30315430303a30303a30305a0000000000000000000000000000001e6f62735f30303030" +
	"30303030303030303030303030303030303030303067000000000000001e636e745f30303030303030303030303030303030303030303030" +
	"30303031000000000000001e7665725f30303030303030303030303030303030303030303030303030320000000000000005000000000000" +
	"000000000000000000000000000000000000000000000000001e636e745f3030303030303030303030303030303030303030303030303031" +
	"0000000000000020303030303030303030303030303030303030303030303030303030303030303400000000000000203030303030303030" +
	"3030303030303030303030303030303030303030303030350000000000000020303030303030303030303030303030303030303030303030" +
	"303030303030303600000000000000026f700000000000000001310000000000000007636174616c6f670000000000000003000000000000" +
	"0002000000000000000100000000000000050000000000000020303030303030303030303030303030303030303030303030303030303030" +
	"313144136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a00000000000000020000000000000001000000000000" +
	"0001310000000000000000000000000000000000000000000000013000000000000000027b7d000000000000000000000000000000143230" +
	"32362d30312d30315430303a30303a30305a000000000000000000000000000000020000000000000004000000000000001e6f62735f3030" +
	"3030303030303030303030303030303030303030303030650000000000000005000000000000001e6f62735f303030303030303030303030" +
	"3030303030303030303030303067"

const registrationVersionResultGolden = "000000000000001a706930382d726567697374726174696f6e2d726573756c742f3100000000000000020000000000000020303030303030" +
	"3030303030303030303030303030303030303030303030303064000000000000001e636e745f303030303030303030303030303030303030" +
	"3030303030303031000000000000001e7665725f303030303030303030303030303030303030303030303030303200000000000000020000" +
	"0000000000010000000000000002000000000000000000000000000000020000000000000004000000000000000100000000000000010000" +
	"0000000000040000000000000000000000000000000100000000000000283131313131313131313131313131313131313131313131313131" +
	"313131313131313131313131313100000000000000000000000000000001000000000000001e6f62735f3030303030303030303030303030" +
	"3030303030303030303030650000000000000005000000000000000100000000000000010000000000000005000000000000000000000000" +
	"000000000000000000000000000000000000001e636e745f3030303030303030303030303030303030303030303030303031000000000000" +
	"0001000000000000001e6f62735f3030303030303030303030303030303030303030303030303067"

func registrationOpaque(n byte) [16]byte { var v [16]byte; v[15] = n; return v }
func registrationID(prefix string, n byte) string {
	return prefix + strings.Repeat("0", 25) + string("0123456789abcdefghjkmnpqrstvwxyz"[n])
}

func registrationFixture(t *testing.T, kind CatalogRegistrationKind) (CatalogRegistrationCapability, CatalogRegistrationCommand) {
	t.Helper()
	op := SourceRegistration{binding: &sourceBinding{id: SourceID{value: registrationOpaque(4)}, revision: RegistrationRevision{value: registrationOpaque(5)}, issuer: IssuerPrincipal{value: registrationOpaque(6)}, adapter: "op", adapterVersion: "1", scope: "catalog", acquisition: CoreOperator, claims: 1<<TitleClaimKey | 1<<OwnershipClaimKey}}
	offline := SourceRegistration{binding: &sourceBinding{id: SourceID{value: registrationOpaque(10)}, revision: RegistrationRevision{value: registrationOpaque(11)}, issuer: IssuerPrincipal{value: registrationOpaque(12)}, adapter: "offline", adapterVersion: "1", scope: "catalog", acquisition: OfflinePublic, claims: 1<<TitleClaimKey | 1<<FingerprintClaimKey}}
	reviewer := IssuerPrincipal{value: registrationOpaque(7)}
	opReview, _ := NewReviewCapability(reviewer, op)
	offReview, _ := NewReviewCapability(reviewer, offline)
	c, err := NewCatalogRegistrationCapability(opReview, []ReviewCapability{opReview, offReview})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := identity.ParseContentID(registrationID("cnt_", 1))
	subject, _ := ContentSubject(content)
	command := CatalogRegistrationCommand{ID: CatalogCommandID{value: registrationOpaque(3)}, Kind: kind, Subject: subject, Source: op, Reason: "create"}
	if kind == RegisterContent {
		claim, _ := TitleClaim("A")
		command.Observations = []CatalogObservation{registrationObservation(t, offline, subject, claim, 9, 8)}
		command.Selections = []CatalogRegistrationSelection{{Key: TitleClaimKey, Observation: command.Observations[0].ID()}}
	} else {
		version, _ := identity.ParseContentVersionID(registrationID("ver_", 2))
		command.Subject, _ = VersionSubject(content, version)
		command.ID = CatalogCommandID{value: registrationOpaque(13)}
		command.ExpectedKind = CatalogExpectedRevision
		command.Expected = CatalogRevision{value: 1}
		command.Reason = "version"
		fingerprint, _ := FingerprintClaim(mustFingerprint(t, mustAudioID(t, 1), mustAudioHash(t, strings.Repeat("11", 20))))
		owner, _ := OwnershipClaim(content)
		command.Observations = []CatalogObservation{registrationObservation(t, offline, command.Subject, fingerprint, 14, 15), registrationObservation(t, op, command.Subject, owner, 16, 17)}
		command.Selections = []CatalogRegistrationSelection{{Key: FingerprintClaimKey, Observation: command.Observations[0].ID()}, {Key: OwnershipClaimKey, Observation: command.Observations[1].ID()}}
	}
	return c, command
}

func registrationObservation(t *testing.T, source SourceRegistration, subject CatalogSubject, claim CatalogClaim, id, batch byte) CatalogObservation {
	t.Helper()
	ingest := IngestCapability{binding: source.binding}
	artifact, err := NewArtifact(ingest, source, ArtifactFacts{ID: BatchID{value: registrationOpaque(batch)}, SHA256: sha256.Sum256([]byte("{}")), ByteLength: 2, RecordCount: 1, ParserRevision: "1"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewRecordSnapshot([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	coordinate, err := NewRecordCoordinate(artifact, 0, "0", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	observationID, _ := evidence.ParseID(registrationID("obs_", id))
	o, err := NewCatalogObservation(ingest, source, ObservationInput{ID: observationID, Subject: subject, Claim: claim, Coordinate: coordinate, ObservedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func registrationBindings(c CatalogRegistrationCapability) CatalogPersistenceBindings {
	b := CatalogPersistenceBindings{Reviewers: []IssuerPrincipal{c.Reviewer()}}
	for _, review := range c.evidence {
		b.Sources = append(b.Sources, SourceRegistration{binding: review.binding})
	}
	return b
}

func TestRegistrationGoldenVectors(t *testing.T) {
	for _, kind := range []CatalogRegistrationKind{RegisterContent, RegisterCatalogVersion} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			c, command := registrationFixture(t, kind)
			raw, err := command.CanonicalEncoding(c)
			if err != nil {
				t.Fatal(err)
			}
			state, result, err := NewRegisteredCatalogReviewState(c, command)
			if err != nil {
				t.Fatal(err)
			}
			resultRaw, err := result.CanonicalEncoding()
			if err != nil {
				t.Fatal(err)
			}
			wantCommand, wantResult := registrationContentGolden, registrationContentResultGolden
			if kind == RegisterCatalogVersion {
				wantCommand, wantResult = registrationVersionGolden, registrationVersionResultGolden
			}
			if hex.EncodeToString(raw) != wantCommand {
				t.Fatalf("command golden differs: %x", raw)
			}
			if hex.EncodeToString(resultRaw) != wantResult {
				t.Fatalf("result golden differs: %x", resultRaw)
			}
			// Set normalization, including unused grant order, has no wire effect.
			reversed := command
			reversed.Observations = append([]CatalogObservation(nil), command.Observations...)
			reversed.Selections = append([]CatalogRegistrationSelection(nil), command.Selections...)
			for i, j := 0, len(reversed.Observations)-1; i < j; i, j = i+1, j-1 {
				reversed.Observations[i], reversed.Observations[j] = reversed.Observations[j], reversed.Observations[i]
			}
			for i, j := 0, len(reversed.Selections)-1; i < j; i, j = i+1, j-1 {
				reversed.Selections[i], reversed.Selections[j] = reversed.Selections[j], reversed.Selections[i]
			}
			c.evidence[0], c.evidence[1] = c.evidence[1], c.evidence[0]
			again, err := reversed.CanonicalEncoding(c)
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatalf("permutation differs: %v", err)
			}
			changed := command
			changed.Reason = "changed"
			again, err = changed.CanonicalEncoding(c)
			if err != nil || bytes.Equal(raw, again) {
				t.Fatal("reason absent from identity")
			}
			if kind == RegisterCatalogVersion {
				changed = command
				changed.Expected.value++
				again, err = changed.CanonicalEncoding(c)
				if err != nil || bytes.Equal(raw, again) {
					t.Fatal("expected condition absent from identity")
				}
			}
			other, alt := registrationFixture(t, kind)
			other.operator.binding.scope = "different scope"
			again, err = alt.CanonicalEncoding(other)
			if err != nil || bytes.Equal(raw, again) {
				t.Fatal("source binding absent from identity")
			}
			other, alt = registrationFixture(t, kind)
			principal := IssuerPrincipal{value: registrationOpaque(23)}
			other.operator.reviewer = principal
			for i := range other.evidence {
				other.evidence[i].reviewer = principal
			}
			again, err = alt.CanonicalEncoding(other)
			if err != nil || bytes.Equal(raw, again) {
				t.Fatal("reviewer absent from identity")
			}
			unused, _ := testRegistration(t, OfflinePublic, TitleClaimKey)
			unusedReview, _ := NewReviewCapability(c.Reviewer(), unused)
			extra, err := NewCatalogRegistrationCapability(c.operator, append(append([]ReviewCapability(nil), c.evidence...), unusedReview))
			if err != nil {
				t.Fatal(err)
			}
			again, err = command.CanonicalEncoding(extra)
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatal("unused source entered canonical bytes")
			}
			encoded, err := state.PersistenceEncoding()
			if err != nil {
				t.Fatal(err)
			}
			for _, bindings := range []CatalogPersistenceBindings{registrationBindings(c), {AllowUnbound: true}} {
				restored, err := RehydrateCatalogReviewState(encoded, bindings)
				if err != nil {
					t.Fatal(err)
				}
				roundtrip, err := restored.PersistenceEncoding()
				if err != nil || !bytes.Equal(encoded, roundtrip) {
					t.Fatalf("review v2 roundtrip: %v", err)
				}
			}
			for _, offset := range []int{0, len(encoded) / 2, len(encoded) - 1} {
				corrupt := append([]byte(nil), encoded...)
				corrupt[offset] ^= 1
				if _, err := RehydrateCatalogReviewState(corrupt, registrationBindings(c)); err == nil {
					t.Fatal("corrupt state accepted")
				}
			}
		})
	}
}

func TestRegistrationInputBounds(t *testing.T) {
	c, base := registrationFixture(t, RegisterContent)
	cases := []struct {
		name   string
		change func(*CatalogRegistrationCommand)
		want   error
	}{
		{"zero command", func(c *CatalogRegistrationCommand) { c.ID = CatalogCommandID{} }, ErrInvalidCatalog},
		{"zero subject", func(c *CatalogRegistrationCommand) { c.Subject = CatalogSubject{} }, ErrInvalidCatalog},
		{"empty reason", func(c *CatalogRegistrationCommand) { c.Reason = "" }, ErrInvalidCatalog},
		{"control reason", func(c *CatalogRegistrationCommand) { c.Reason = "x\n" }, ErrInvalidCatalog},
		{"invalid union", func(c *CatalogRegistrationCommand) { c.ExpectedKind = CatalogExpectedRevision }, ErrInvalidCatalog},
		{"duplicate observations", func(c *CatalogRegistrationCommand) { c.Observations = append(c.Observations, c.Observations[0]) }, ErrInvalidCatalog},
		{"duplicate support", func(c *CatalogRegistrationCommand) { c.Selections = append(c.Selections, c.Selections[0]) }, ErrInvalidCatalog},
		{"observations one over", func(c *CatalogRegistrationCommand) { c.Observations = make([]CatalogObservation, 65) }, LimitError{Resource: MutationObservations}},
		{"support one over", func(c *CatalogRegistrationCommand) { c.Selections = make([]CatalogRegistrationSelection, 257) }, LimitError{Resource: SelectedSupport}},
		{"reason one over", func(c *CatalogRegistrationCommand) { c.Reason = strings.Repeat("r", 4097) }, LimitError{Resource: TextBytes}},
		{"batch key collision", func(c *CatalogRegistrationCommand) {
			c.ID.value = c.Observations[0].input.Coordinate.artifact.facts.ID.value
		}, ErrCatalogCommandConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command := base
			command.Observations = append([]CatalogObservation(nil), base.Observations...)
			command.Selections = append([]CatalogRegistrationSelection(nil), base.Selections...)
			tc.change(&command)
			_, _, err := NewRegisteredCatalogReviewState(c, command)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	base.Reason = strings.Repeat("r", 4096)
	if _, _, err := NewRegisteredCatalogReviewState(c, base); err != nil {
		t.Fatal(err)
	}
	vc, v := registrationFixture(t, RegisterCatalogVersion)
	v.Expected = CatalogRevision{value: math.MaxInt64}
	if _, _, err := NewRegisteredCatalogReviewState(vc, v); !errors.Is(err, ErrCatalogRevisionOverflow) {
		t.Fatal(err)
	}
	v.Expected.value = math.MaxInt64 - 1
	if _, r, err := NewRegisteredCatalogReviewState(vc, v); err != nil || r.AggregateRevision.value != math.MaxInt64 {
		t.Fatal("last aggregate revision", err)
	}
	v.Observations = append([]CatalogObservation(nil), v.Observations...)
	v.Observations[0].input.Claim.fingerprint.audioID.value = math.MaxUint32 + 1
	if _, _, err := NewRegisteredCatalogReviewState(vc, v); !errors.Is(err, ErrCatalogUnsupported) {
		t.Fatal("unsupported width", err)
	}
	for _, count := range []int{256, 257} {
		reviews := make([]ReviewCapability, 0, count)
		reviews = append(reviews, c.operator)
		for i := 1; i < count; i++ {
			s, _ := testRegistration(t, OfflinePublic, TitleClaimKey)
			r, _ := NewReviewCapability(c.Reviewer(), s)
			reviews = append(reviews, r)
		}
		_, err := NewCatalogRegistrationCapability(c.operator, reviews)
		if count == 256 && err != nil || count == 257 && !errors.Is(err, LimitError{Resource: SourceRegistrations}) {
			t.Fatal("source count", count, err)
		}
	}
	for _, size := range []int{int(RetainedBytes.Maximum()), int(RetainedBytes.Maximum()) + 1} {
		e := catalogEncoder{}
		e.raw(strings.Repeat("x", size))
		if size == int(RetainedBytes.Maximum()) && e.err != nil || size > int(RetainedBytes.Maximum()) && !errors.Is(e.err, LimitError{Resource: RetainedBytes}) {
			t.Fatal("encoded byte boundary")
		}
	}
	// A full incoming observation set succeeds without duplicate coalescing.
	c, base = registrationFixture(t, RegisterContent)
	prototype := base.Observations[0]
	base.Observations = nil
	base.Selections = nil
	for i := 1; i <= 64; i++ {
		o := prototype
		o.input.ID, _ = evidence.ParseID(fmt.Sprintf("obs_%026x", i))
		base.Observations = append(base.Observations, o)
		base.Selections = append(base.Selections, CatalogRegistrationSelection{Key: TitleClaimKey, Observation: o.ID()})
	}
	state, _, err := NewRegisteredCatalogReviewState(c, base)
	if err != nil || len(state.Observations()) != 64 {
		t.Fatal("exact observation boundary", err)
	}
	for _, raw := range []string{"{", `{"x":1,"x":2}`, "[]"} {
		if _, err := NewRecordSnapshot([]byte(raw)); err == nil {
			t.Fatal("malformed snapshot accepted")
		}
	}
	// Cumulative support and event bounds count bootstrap, before mutation.
	review := c.evidence[1]
	source := SourceRegistration{binding: review.binding}
	decision := CatalogReviewCommand{ID: CatalogCommandID{value: registrationOpaque(24)}, Kind: AcceptSupport, Subject: base.Subject, Source: source, Expected: state.Revision(), Key: TitleClaimKey, Support: []evidence.ID{base.Observations[0].ID()}, Reason: "support"}
	for _, links := range []uint64{RetainedSupportLinks.Maximum() - 1, RetainedSupportLinks.Maximum()} {
		candidate := state
		candidate.links = links
		_, _, err := candidate.Apply(review, decision)
		if links == RetainedSupportLinks.Maximum()-1 && err != nil || links == RetainedSupportLinks.Maximum() && !errors.Is(err, LimitError{Resource: RetainedSupportLinks}) {
			t.Fatal("cumulative support", err)
		}
	}
	candidate := state
	candidate.decisions = make([]catalogDecision, 1023)
	_, _, err = candidate.Apply(review, decision)
	if !errors.Is(err, LimitError{Resource: RetainedDecisions}) {
		t.Fatal("bootstrap decision budget", err)
	}
}

func TestRegistrationAuthority(t *testing.T) {
	for _, name := range []string{"zero", "detached", "revoked", "wrong source", "wrong revision", "spoofed reviewer", "offline ownership"} {
		t.Run(name, func(t *testing.T) {
			c, command := registrationFixture(t, RegisterContent)
			want := ErrCatalogUnauthorized
			switch name {
			case "zero":
				c = CatalogRegistrationCapability{}
			case "detached":
				c.operator.binding.persistenceOnly = true
			case "revoked":
				command.Observations[0].input.Coordinate.artifact.source.Revoke()
			case "wrong source", "wrong revision":
				b := command.Observations[0].input.Coordinate.artifact.source.binding
				copy := sourceBinding{id: b.id, revision: b.revision, issuer: b.issuer, adapter: b.adapter, adapterVersion: b.adapterVersion, scope: b.scope, acquisition: b.acquisition, claims: b.claims}
				copy.revision.value = registrationOpaque(20)
				if name == "wrong source" {
					copy.id.value = registrationOpaque(21)
				}
				command.Observations[0].input.Coordinate.artifact.source = SourceRegistration{binding: &copy}
				want = ErrCatalogSourceMismatch
			case "spoofed reviewer":
				c.evidence[1].reviewer = IssuerPrincipal{value: registrationOpaque(22)}
			case "offline ownership":
				c, command = registrationFixture(t, RegisterCatalogVersion)
				offline := command.Observations[0].input.Coordinate.artifact.source
				offline.binding.claims |= 1 << OwnershipClaimKey
				command.Observations[1].input.Coordinate.artifact.source = offline
			}
			if _, _, err := NewRegisteredCatalogReviewState(c, command); !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		})
	}
	c, command := registrationFixture(t, RegisterContent)
	state, _, err := NewRegisteredCatalogReviewState(c, command)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := state.PersistenceEncoding()
	restored, err := RehydrateCatalogReviewState(raw, registrationBindings(c))
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := restored.LookupRegistration(c, command); err != nil || !found {
		t.Fatal("trusted rebind", err)
	}
	command.Source.Revoke()
	if _, _, err := restored.LookupRegistration(c, command); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatal("revoked replay", err)
	}
	if _, err := RehydrateCatalogReviewState(raw, registrationBindings(c)); err != nil {
		t.Fatal("revocation erased history", err)
	}
}

func TestRegistrationEvidence(t *testing.T) {
	for _, name := range []string{"missing selection", "cross subject", "cross version", "contradictory selection", "changed record", "confidence alone", "custom protection"} {
		t.Run(name, func(t *testing.T) {
			c, command := registrationFixture(t, RegisterContent)
			want := ErrCatalogUnsupported
			switch name {
			case "missing selection":
				command.Selections[0].Observation, _ = evidence.ParseID(registrationID("obs_", 20))
			case "cross subject":
				command.Observations[0].input.Subject.content, _ = identity.ParseContentID(registrationID("cnt_", 20))
				want = ErrCatalogSubjectMismatch
			case "cross version":
				c, command = registrationFixture(t, RegisterCatalogVersion)
				command.Observations[0].input.Subject.version, _ = identity.ParseContentVersionID(registrationID("ver_", 20))
				want = ErrCatalogSubjectMismatch
			case "contradictory selection":
				o := command.Observations[0]
				o.input.ID, _ = evidence.ParseID(registrationID("obs_", 20))
				o.input.Claim, _ = TitleClaim("B")
				command.Observations = append(command.Observations, o)
				command.Selections = append(command.Selections, CatalogRegistrationSelection{Key: TitleClaimKey, Observation: o.ID()})
			case "changed record":
				o := command.Observations[0]
				o.input.ID, _ = evidence.ParseID(registrationID("obs_", 20))
				o.input.Coordinate.locator = "changed"
				command.Observations = append(command.Observations, o)
				want = ErrCatalogObservationConflict
			case "confidence alone":
				command.Observations[0].input.Confidence = evidence.Corroborated
				command.Selections = nil
			case "custom protection":
				command.Observations[0].input.Coordinate.artifact.source.binding.acquisition = OfflineCustom
				state, result, err := NewRegisteredCatalogReviewState(c, command)
				if err != nil || !state.ProtectedCustom() || !result.ProtectedCustom {
					t.Fatal("custom protection", err)
				}
				return
			}
			if _, _, err := NewRegisteredCatalogReviewState(c, command); !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		})
	}
	// Ordinary resolution replaces bootstrap selection, retaining its evidence.
	c, command := registrationFixture(t, RegisterContent)
	o := command.Observations[0]
	o.input.ID, _ = evidence.ParseID(registrationID("obs_", 20))
	o.input.Claim, _ = TitleClaim("B")
	command.Observations = append(command.Observations, o)
	state, original, err := NewRegisteredCatalogReviewState(c, command)
	if err != nil {
		t.Fatal(err)
	}
	source := o.input.Coordinate.artifact.source
	review, _ := NewReviewCapability(c.Reviewer(), source)
	resolve := CatalogReviewCommand{ID: CatalogCommandID{value: registrationOpaque(21)}, Kind: ResolveConflict, Subject: command.Subject, Source: source, Expected: state.Revision(), Key: TitleClaimKey, Support: []evidence.ID{o.ID()}, Reason: "resolve"}
	state, _, err = state.Apply(review, resolve)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := state.Field(TitleClaimKey).Value()
	if !ok || value.Text() != "B" || state.Revision().value != 2 || len(state.Observations()) != 2 {
		t.Fatal("bootstrap fold lost history")
	}
	result, found, err := state.LookupRegistration(c, command)
	if err != nil || !found || !registrationEqual(result, original) {
		t.Fatal("original registration result lost", err)
	}
	changed := command
	changed.Reason = "different"
	if _, _, err := state.LookupRegistration(c, changed); !errors.Is(err, ErrCatalogCommandConflict) {
		t.Fatal("changed replay", err)
	}
	changed = command
	changed.Selections = nil
	if _, _, err := state.LookupRegistration(c, changed); !errors.Is(err, ErrCatalogCommandConflict) {
		t.Fatal("key conflict must precede semantic evidence", err)
	}
	resolve.ID = command.ID
	resolve.Expected = state.Revision()
	if _, _, err := state.Apply(review, resolve); !errors.Is(err, ErrCatalogCommandConflict) {
		t.Fatal("cross-kind key collision", err)
	}
	raw, err := state.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RehydrateCatalogReviewState(raw, registrationBindings(c))
	if err != nil {
		t.Fatal("tail replay", err)
	}
	if !reflect.DeepEqual(state.Field(TitleClaimKey), restored.Field(TitleClaimKey)) {
		t.Fatal("tail replay changed field")
	}
	added := o
	added.input.ID, _ = evidence.ParseID(registrationID("obs_", 1))
	added.input.Claim, _ = TitleClaim("later dissent")
	record := CatalogReviewCommand{ID: CatalogCommandID{value: registrationOpaque(25)}, Kind: RecordObservations, Subject: command.Subject, Source: source, Expected: state.Revision(), Observations: []CatalogObservation{added}, Reason: "retain dissent"}
	state, _, err = state.Apply(review, record)
	if err != nil {
		t.Fatal(err)
	}
	if state.observations[2].ID() != added.ID() {
		t.Fatal("bootstrap-first observation order lost")
	}
	raw, err = state.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RehydrateCatalogReviewState(raw, registrationBindings(c)); err != nil {
		t.Fatal("append-order tail replay", err)
	}
}

func TestRegistrationManifestAndMedia(t *testing.T) {
	state, namespace, ingest, capability, versions := orderFixture(t)
	a := sequenceFixture(t, namespace, ingest, versions[0], idPayloadA, 0)
	b := sequenceFixture(t, namespace, ingest, versions[1], "1123456789abcdefghjkmnpqrs", 1)
	oldCommand := orderCommandFixture(t, state, namespace, a, b)
	state, original := applyOrder(t, state, capability, oldCommand)
	base, _ := state.PersistenceEncoding()
	newID, _ := identity.ParseContentVersionID(registrationID("ver_", 20))
	pair := mustFingerprint(t, mustAudioID(t, 1), mustAudioHash(t, strings.Repeat("11", 20)))
	version, _ := NewContentVersion(newID, state.content, pair, UnknownVersionOrderEvidence())
	command := CatalogCommandID{value: registrationOpaque(21)}
	grown, err := state.AppendRegisteredVersion(command, CatalogRevision{value: 4}, version)
	if err != nil {
		t.Fatal(err)
	}
	if grown.Compare(versions[0], versions[1]) != VersionOrderingBefore || grown.Compare(version, versions[0]) != VersionOrderingUnknown || grown.Compare(version, version) != VersionOrderingSame || grown.EffectiveOrder().Status != CatalogFactUnknown {
		t.Fatal("growth changed order semantics")
	}
	if !bytes.Equal(base, []byte(grown.growthBase)) || state.Revision().value != 2 || grown.Revision().value != 3 {
		t.Fatal("growth mutated historical snapshot")
	}
	if replay, found, err := grown.LookupCommand(capability, oldCommand); err != nil || !found || !reflect.DeepEqual(replay, original) {
		t.Fatal("old result changed", err)
	}
	for _, v := range []ContentVersion{version, versions[0]} {
		if _, err := grown.AppendRegisteredVersion(CatalogCommandID{value: registrationOpaque(22)}, CatalogRevision{value: 5}, v); !errors.Is(err, ErrCatalogCommandConflict) {
			t.Fatal("occupied version", err)
		}
	}
	known := version
	known.order = mustOrder(t, "asserted", 1)
	if _, err := state.AppendRegisteredVersion(command, CatalogRevision{value: 4}, known); !errors.Is(err, ErrCatalogUnsupported) {
		t.Fatal("known order admitted", err)
	}
	changed := version
	changed.fingerprint = versions[0].Fingerprint()
	if grown.Compare(changed, version) != VersionOrderingConflict {
		t.Fatal("tuple mismatch")
	}
	// Add an ordinary decision after growth; it must replay against that manifest.
	c := sequenceFixture(t, namespace, ingest, version, "2123456789abcdefghjkmnpqrs", 2)
	post := orderCommandFixture(t, grown, namespace, c)
	grown, _ = applyOrder(t, grown, capability, post)
	secondID, _ := identity.ParseContentVersionID(registrationID("ver_", 22))
	second, _ := NewContentVersion(secondID, state.content, pair, UnknownVersionOrderEvidence())
	grown, err = grown.AppendRegisteredVersion(CatalogCommandID{value: registrationOpaque(23)}, CatalogRevision{value: 6}, second)
	if err != nil {
		t.Fatal(err)
	}
	if grown.Compare(version, second) != VersionOrderingUnknown || grown.EffectiveOrder().Status != CatalogFactUnknown {
		t.Fatal("second growth inferred order")
	}
	bindings := CatalogPersistenceBindings{Sources: []SourceRegistration{namespace.Source()}, Namespaces: []CatalogOrderNamespace{namespace}, Reviewers: []IssuerPrincipal{capability.review.Reviewer()}}
	// A v2 stream must begin with growth. A syntactically valid ordinary
	// decision first must reject, rather than indexing an absent growth event.
	ordinary := orderCommandFixture(t, state, namespace, a)
	ordinaryState, _ := applyOrder(t, state, capability, ordinary)
	bad := catalogEncoder{data: make([]byte, 0)}
	state.encodeGrowthState(&bad, string(base), &catalogOrderGrowthEvent{decision: &ordinaryState.decisions[len(ordinaryState.decisions)-1]})
	if _, err := RehydrateCatalogOrderState(bad.data, bindings); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatal("ordinary-first growth stream", err)
	}
	raw, err := grown.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []CatalogPersistenceBindings{bindings, {AllowUnbound: true}} {
		restored, err := RehydrateCatalogOrderState(raw, binding)
		if err != nil {
			t.Fatal("growth replay", err)
		}
		encoded, err := restored.PersistenceEncoding()
		if err != nil || !bytes.Equal(raw, encoded) || restored.Compare(versions[0], version) != VersionOrderingBefore {
			t.Fatal("growth roundtrip", err)
		}
	}
	for _, offset := range []int{0, len(raw) / 2, len(raw) - 1} {
		corrupt := append([]byte(nil), raw...)
		corrupt[offset] ^= 1
		if _, err := RehydrateCatalogOrderState(corrupt, bindings); err == nil {
			t.Fatal("corrupt growth accepted")
		}
	}
	for _, count := range []int{1023, 1024} {
		candidate := state
		candidate.revision.value = uint64(count) + 1
		candidate.versions = make([]ContentVersion, count)
		for i := range candidate.versions {
			id, _ := identity.ParseContentVersionID(fmt.Sprintf("ver_%026x", i+100))
			candidate.versions[i], _ = NewContentVersion(id, state.content, pair, UnknownVersionOrderEvidence())
		}
		_, err := candidate.AppendRegisteredVersion(command, CatalogRevision{value: 2000}, version)
		if count == 1023 && err != nil || count == 1024 && !errors.Is(err, LimitError{Resource: RetainedVersions}) {
			t.Fatal("manifest boundary", count, err)
		}
	}
	// Media has no domain side effects: the exact immutable tuple is retained.
	for _, old := range state.versions {
		found := false
		for _, current := range grown.versions {
			if current.ID() == old.ID() {
				found = current == old
			}
		}
		if !found {
			t.Fatal("immutable media tuple changed")
		}
	}
	if version.OrderEvidence().Known() {
		t.Fatal("invented original order")
	}
}
