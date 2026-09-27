package tag

import (
	"errors"
	"strings"
	"testing"
)

const decisionIDTestPayload = "0123456789abcdefghjkmnpqrs"

func TestDecisionIDCanonicalRoundTrip(t *testing.T) {
	text := "dec_" + decisionIDTestPayload
	id, err := ParseDecisionID(text)
	if err != nil {
		t.Fatalf("canonical decision ID rejected: %v", err)
	}
	if id.String() != text {
		t.Fatalf("round-trip changed decision ID: %q", id.String())
	}
	if id.IsZero() {
		t.Fatal("parsed decision ID is zero")
	}
}

func TestDecisionIDZeroValueDiffersFromAllZeroPayload(t *testing.T) {
	var zero DecisionID
	if !zero.IsZero() || zero.String() != "" {
		t.Fatal("DecisionID zero value is not empty")
	}

	text := "dec_" + strings.Repeat("0", 26)
	id, err := ParseDecisionID(text)
	if err != nil {
		t.Fatalf("all-zero payload rejected: %v", err)
	}
	if id.IsZero() || id.String() != text {
		t.Fatal("valid all-zero payload was treated as the zero value")
	}
}

func TestDecisionIDRejectsWrongPrefixAndLength(t *testing.T) {
	for _, text := range []string{
		"",
		"dec_" + decisionIDTestPayload[:25],
		"dec_" + decisionIDTestPayload + "0",
		"tag_" + decisionIDTestPayload,
		"DEC_" + decisionIDTestPayload,
	} {
		assertInvalidDecisionID(t, text)
	}
}

func TestDecisionIDAcceptsEveryOpaqueAlphabetCharacter(t *testing.T) {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	for i := range len(alphabet) {
		payload := strings.Repeat(string(alphabet[i]), 26)
		text := "dec_" + payload
		id, err := ParseDecisionID(text)
		if err != nil {
			t.Fatalf("alphabet byte %q rejected: %v", alphabet[i], err)
		}
		if id.String() != text {
			t.Fatalf("alphabet byte %q did not round-trip", alphabet[i])
		}
	}
}

func TestDecisionIDRejectsAmbiguousAndUppercaseCharacters(t *testing.T) {
	for _, character := range "ilouABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		text := "dec_" + strings.Repeat("0", 25) + string(character)
		assertInvalidDecisionID(t, text)
	}
}

func TestDecisionIDRejectsWhitespaceControlAndNonASCII(t *testing.T) {
	invalidPayloads := []string{
		strings.Repeat("0", 25) + " ",
		strings.Repeat("0", 25) + "\t",
		strings.Repeat("0", 25) + "\n",
		strings.Repeat("0", 25) + "\x00",
		strings.Repeat("0", 25) + "\x1f",
		strings.Repeat("0", 25) + "\x7f",
		strings.Repeat("0", 24) + "é",
		strings.Repeat("0", 25) + string([]byte{0xff}),
	}
	for _, payload := range invalidPayloads {
		assertInvalidDecisionID(t, "dec_"+payload)
	}
}

func TestDecisionIDRejectsOversizedInputWithoutEcho(t *testing.T) {
	secret := strings.Repeat("private-decision-material", 4096)
	id, err := ParseDecisionID(secret)
	if id != (DecisionID{}) {
		t.Fatal("oversized input returned a non-zero decision ID")
	}
	if !errors.Is(err, ErrInvalidDecisionID) {
		t.Fatalf("want generic invalid decision ID error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("oversized decision ID leaked through error")
	}
}

func TestDecisionIDErrorsNeverEchoInput(t *testing.T) {
	secret := "dec_" + strings.Repeat("z", 25) + "!"
	_, err := ParseDecisionID(secret)
	if !errors.Is(err, ErrInvalidDecisionID) {
		t.Fatalf("want generic invalid decision ID error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("decision ID leaked through error")
	}
}

func assertInvalidDecisionID(t *testing.T, text string) {
	t.Helper()
	id, err := ParseDecisionID(text)
	if id != (DecisionID{}) {
		t.Fatalf("invalid input returned a non-zero decision ID: %q", id.String())
	}
	if !errors.Is(err, ErrInvalidDecisionID) {
		t.Fatalf("invalid input accepted or returned wrong error: %v", err)
	}
}
