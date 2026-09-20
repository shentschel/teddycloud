package identity

import (
	"errors"
	"strings"
	"testing"
)

const testPayload = "0123456789abcdefghjkmnpqrs"

func TestOpaqueIDsAcceptOnlyTheirCanonicalForm(t *testing.T) {
	tests := []struct {
		name  string
		parse func(string) error
		valid string
	}{
		{"tag", func(text string) error { _, err := ParseTagID(text); return err }, "tag_" + testPayload},
		{"content", func(text string) error { _, err := ParseContentID(text); return err }, "cnt_" + testPayload},
		{"version", func(text string) error { _, err := ParseContentVersionID(text); return err }, "ver_" + testPayload},
		{"product", func(text string) error { _, err := ParseProductID(text); return err }, "prd_" + testPayload},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse(test.valid); err != nil {
				t.Fatalf("valid ID rejected: %v", err)
			}
			for _, malformed := range []string{
				"", test.valid[:len(test.valid)-1], test.valid + "0",
				strings.Repeat("x", 4096), "bad_" + testPayload,
				test.valid[:len(test.valid)-1] + "i",
			} {
				if err := test.parse(malformed); err == nil {
					t.Fatalf("malformed ID accepted")
				}
			}
		})
	}
}

func TestOpaqueIDErrorsDoNotEchoInput(t *testing.T) {
	secret := strings.Repeat("private-value", 400)
	_, err := ParseTagID(secret)
	if !errors.Is(err, ErrInvalidIDLength) {
		t.Fatalf("want length error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("identifier leaked through error")
	}
}

func FuzzParseTagIDIsBoundedAndNeverPanics(f *testing.F) {
	f.Add("tag_" + testPayload)
	f.Add(strings.Repeat("x", 1024))
	f.Fuzz(func(t *testing.T, text string) {
		id, err := ParseTagID(text)
		if err == nil && id.String() != text {
			t.Fatalf("successful parse changed canonical ID")
		}
	})
}
