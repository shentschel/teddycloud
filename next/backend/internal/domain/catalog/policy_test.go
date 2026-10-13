package catalog

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testRegistration(t *testing.T, acquisition AcquisitionClass, claims ...ClaimKey) (SourceRegistration, IngestCapability) {
	t.Helper()
	id, err := NewSourceID()
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	source, ingest, err := NewSourceRegistration(id, issuer, SourceSpec{
		Adapter:        "offline-json",
		AdapterVersion: "1",
		Scope:          "catalog",
		Acquisition:    acquisition,
		AllowedClaims:  claims,
	})
	if err != nil {
		t.Fatal(err)
	}
	return source, ingest
}

func TestCatalogIssuerBinding(t *testing.T) {
	source, ingest := testRegistration(t, OfflinePublic, TitleClaimKey, FingerprintClaimKey)
	other, otherIngest := testRegistration(t, OfflinePublic, TitleClaimKey)
	if source.ID().IsZero() || source.Issuer().IsZero() || source.Revision().IsZero() {
		t.Fatal("core omitted opaque binding")
	}
	if source.ID() == other.ID() || source.Issuer() == other.Issuer() {
		t.Fatal("independent core identities collided")
	}
	if err := ingest.Authorize(source, TitleClaimKey); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		handle IngestCapability
		source SourceRegistration
		want   error
	}{
		{IngestCapability{}, source, ErrCatalogUnauthorized},
		{ingest, SourceRegistration{}, ErrCatalogUnauthorized},
		{otherIngest, source, ErrCatalogSourceMismatch},
		{ingest, other, ErrCatalogSourceMismatch},
	} {
		if err := attempt.handle.Authorize(attempt.source, TitleClaimKey); !errors.Is(err, attempt.want) {
			t.Fatalf("spoof result: %v", err)
		}
	}
	if err := ingest.Authorize(source, OwnershipClaimKey); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatalf("unregistered claim allowed: %v", err)
	}
	// Familiar descriptive source text and serialized handles carry no authority.
	payload := []byte(`{"binding":{"issuer":"official","claims":255},"reviewer":"admin","value":"official"}`)
	var spoofed IngestCapability
	var spoofedSource SourceRegistration
	var spoofedIssuer IssuerPrincipal
	var spoofedID SourceID
	for _, target := range []any{&spoofed, &spoofedSource, &spoofedIssuer, &spoofedID} {
		if err := json.Unmarshal(payload, target); err != nil {
			t.Fatal(err)
		}
	}
	if !spoofedIssuer.IsZero() || !spoofedID.IsZero() || spoofedSource.binding != nil {
		t.Fatal("import text constructed identity")
	}
	if err := spoofed.Authorize(source, TitleClaimKey); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatalf("import minted authority: %v", err)
	}
	reviewer, _ := NewIssuerPrincipal()
	review, err := NewReviewCapability(reviewer, source)
	if err != nil || review.Reviewer() != reviewer || review.Reviewer() == source.Issuer() {
		t.Fatal("reviewer and acquiring issuer were conflated")
	}
	if err := review.Authorize(source, TitleClaimKey); err != nil {
		t.Fatal(err)
	}
	if err := review.Authorize(other, TitleClaimKey); !errors.Is(err, ErrCatalogSourceMismatch) {
		t.Fatal("review scope escaped registration")
	}
	var spoofedReview ReviewCapability
	if err := json.Unmarshal(payload, &spoofedReview); err != nil {
		t.Fatal(err)
	}
	if err := spoofedReview.Authorize(source, TitleClaimKey); !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatal("import minted review authority")
	}
	// A new revision retains its old binding and cannot reuse its handle.
	revised, revisedIngest, err := NewSourceRegistration(source.ID(), source.Issuer(), source.Spec())
	if err != nil || revised.Revision() == source.Revision() {
		t.Fatal("revision was not independent")
	}
	if err := revisedIngest.Authorize(source, TitleClaimKey); !errors.Is(err, ErrCatalogSourceMismatch) {
		t.Fatal("new revision rebound old authority")
	}
	if err := ingest.Authorize(source, TitleClaimKey); err != nil {
		t.Fatal("old binding changed")
	}
	spec := source.Spec()
	spec.AllowedClaims[0] = OwnershipClaimKey
	if !source.Allows(TitleClaimKey) || source.Allows(OwnershipClaimKey) {
		t.Fatal("caller mutated registration")
	}
}

func TestCatalogRegistrationBounds(t *testing.T) {
	source, _ := testRegistration(t, OfflineCustom, TitleClaimKey)
	for _, field := range []string{"adapter", "version", "scope"} {
		for _, size := range []int{128, 129} {
			spec := source.Spec()
			text := strings.Repeat("x", size)
			switch field {
			case "adapter":
				spec.Adapter = text
			case "version":
				spec.AdapterVersion = text
			case "scope":
				spec.Scope = text
			}
			_, _, err := NewSourceRegistration(source.ID(), source.Issuer(), spec)
			if size == 128 && err != nil || size == 129 && err == nil {
				t.Fatalf("%s boundary %d: %v", field, size, err)
			}
		}
	}
	for _, malformed := range []string{"", "line\nbreak", string([]byte{0xff})} {
		spec := source.Spec()
		spec.Adapter = malformed
		if _, _, err := NewSourceRegistration(source.ID(), source.Issuer(), spec); err == nil {
			t.Fatal("invalid reference admitted")
		}
	}
	for _, spec := range []SourceSpec{
		{Adapter: "a", AdapterVersion: "v", Scope: "s", Acquisition: 0, AllowedClaims: []ClaimKey{TitleClaimKey}},
		{Adapter: "a", AdapterVersion: "v", Scope: "s", Acquisition: 255, AllowedClaims: []ClaimKey{TitleClaimKey}},
		{Adapter: "a", AdapterVersion: "v", Scope: "s", Acquisition: OfflinePublic, AllowedClaims: []ClaimKey{OwnershipClaimKey}},
		{Adapter: "a", AdapterVersion: "v", Scope: "s", Acquisition: OfflineCustom, AllowedClaims: []ClaimKey{OwnershipClaimKey}},
		{Adapter: "a", AdapterVersion: "v", Scope: "s", Acquisition: OfflinePublic, AllowedClaims: []ClaimKey{TitleClaimKey, TitleClaimKey}},
		{Adapter: "a", AdapterVersion: "v", Scope: "s", Acquisition: OfflinePublic, AllowedClaims: []ClaimKey{255}},
		{Adapter: "a", AdapterVersion: "v", Scope: "s", Acquisition: LocalVerification, AllowedClaims: []ClaimKey{TitleClaimKey}},
	} {
		if _, _, err := NewSourceRegistration(source.ID(), source.Issuer(), spec); err == nil {
			t.Fatal("invalid registration admitted")
		}
	}
	if _, _, err := NewSourceRegistration(SourceID{}, source.Issuer(), source.Spec()); err == nil {
		t.Fatal("zero source admitted")
	}
	if _, _, err := NewSourceRegistration(source.ID(), IssuerPrincipal{}, source.Spec()); err == nil {
		t.Fatal("zero issuer admitted")
	}
	if _, err := NewReviewCapability(IssuerPrincipal{}, source); err == nil {
		t.Fatal("zero reviewer admitted")
	}
	if _, err := NewReviewCapability(source.Issuer(), SourceRegistration{}); err == nil {
		t.Fatal("zero review source admitted")
	}
	local, _ := testRegistration(t, LocalVerification)
	if local.Allows(TitleClaimKey) {
		t.Fatal("local receipt source gained editorial authority")
	}
	sources := make([]SourceRegistration, 256)
	for i := range sources {
		sources[i], _ = testRegistration(t, BaselineSnapshot, TitleClaimKey)
	}
	bounded, err := BoundSources(sources)
	if err != nil || len(bounded) != 256 {
		t.Fatalf("boundary rejected: %v", err)
	}
	bounded[0] = SourceRegistration{}
	if sources[0].binding == nil {
		t.Fatal("allowlist was not copied")
	}
	if _, err := BoundSources(append(sources, source)); err == nil {
		t.Fatal("one-over allowlist admitted")
	}
	if _, err := BoundSources([]SourceRegistration{source, source}); err == nil {
		t.Fatal("duplicate registration admitted")
	}
	if _, err := BoundSources([]SourceRegistration{{}}); err == nil {
		t.Fatal("zero registration admitted")
	}
}

func TestCatalogOpaqueDisplayGolden(t *testing.T) {
	value := [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	for _, got := range []string{
		(SourceID{value: value}).String(),
		(IssuerPrincipal{value: value}).String(),
		(RegistrationRevision{value: value}).String(),
	} {
		if got != "000102030405060708090a0b0c0d0e0f" {
			t.Fatalf("opaque display changed: %s", got)
		}
	}
}
