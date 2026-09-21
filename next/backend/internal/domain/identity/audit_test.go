package identity

import (
	"strings"
	"testing"
)

func TestAuditOpaqueIDsNeverNormalizeIntoAnotherIdentity(t *testing.T) {
	parsers := []struct {
		prefix string
		parse  func(string) (string, error)
	}{
		{"tag_", func(s string) (string, error) { id, err := ParseTagID(s); return id.String(), err }},
		{"cnt_", func(s string) (string, error) { id, err := ParseContentID(s); return id.String(), err }},
		{"ver_", func(s string) (string, error) { id, err := ParseContentVersionID(s); return id.String(), err }},
		{"prd_", func(s string) (string, error) { id, err := ParseProductID(s); return id.String(), err }},
	}
	for _, parser := range parsers {
		t.Run(parser.prefix, func(t *testing.T) {
			canonical := parser.prefix + testPayload
			for _, input := range []string{strings.ToUpper(canonical), " " + canonical, canonical + "\n", " " + canonical[1:], canonical[:len(canonical)-1] + "\x00"} {
				if value, err := parser.parse(input); err == nil || value != "" {
					t.Fatal("noncanonical input yielded identity")
				}
			}
			for _, other := range parsers {
				value, err := parser.parse(other.prefix + testPayload)
				if (err == nil) != (parser.prefix == other.prefix) || (err != nil && value != "") {
					t.Fatal("aggregate prefix boundary bypassed")
				}
			}
			for i := range testPayload {
				for _, forbidden := range "ilouILO U/" {
					bad := []byte(canonical)
					bad[4+i] = byte(forbidden)
					if value, err := parser.parse(string(bad)); err != ErrInvalidIDCharacter || value != "" {
						t.Fatal("invalid payload byte accepted or corrected")
					}
				}
			}
		})
	}
}
