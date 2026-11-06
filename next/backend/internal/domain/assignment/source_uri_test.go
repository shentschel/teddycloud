package assignment

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const sourceURIRoot = "https://catalog.example/"

func TestAssignmentSourceURIProfile(t *testing.T) {
	label := strings.Repeat("a", 63)
	host253 := strings.Repeat(label+".", 3) + strings.Repeat("b", 61)
	atLimit := "HTTPS://CATALOG.EXAMPLE:443/" +
		strings.Repeat("x", 2048-len("HTTPS://CATALOG.EXAMPLE:443/")-3) + "%7E"
	accepted := []struct{ name, raw, want string }{
		{"example", "HTTPS://Catalog.Example:443/a%62", sourceURIRoot + "ab"},
		{"empty path", "https://catalog.example", sourceURIRoot},
		{"root", sourceURIRoot, sourceURIRoot},
		{"unreserved", sourceURIRoot + "AZaz09-._~/%41%7a%30%39%2D%2e%5F%7E/", sourceURIRoot + "AZaz09-._~/Az09-._~/"},
		{"case and segments", sourceURIRoot + "A/B/.a/.../b./", sourceURIRoot + "A/B/.a/.../b./"},
		{"labels", "hTtPs://2-A.Ex-ample9:443/", "https://2-a.ex-ample9/"},
		{"dns syntax only", "https://127.0.0.1.example/internal", "https://127.0.0.1.example/internal"},
		{"host and label limit", "https://" + host253, "https://" + host253 + "/"},
		{"input byte limit", atLimit, sourceURIRoot + strings.Repeat("x", len(atLimit)-len("HTTPS://CATALOG.EXAMPLE:443/")-3) + "~"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSourceURI(tc.raw)
			again, againErr := ParseSourceURI(tc.raw)
			restored, restoreErr := RestoreSourceURI(SourceURIProfileV1, tc.want)
			if err != nil || againErr != nil || restoreErr != nil ||
				got.Profile() != "pi09-uri/1" || got.Locator() != tc.want || got != again || got != restored {
				t.Fatalf("unexpected admission or restoration: %v / %v / %v", err, againErr, restoreErr)
			}
		})
	}

	rejected := map[string]string{
		"empty": "", "input over limit": atLimit + "x",
		"leading space": " " + sourceURIRoot, "space in path": sourceURIRoot + "a b",
		"trailing space": sourceURIRoot + "x ", "tab": sourceURIRoot + "a\tb",
		"newline": sourceURIRoot + "a\nb", "nul": sourceURIRoot + "a\x00b",
		"DEL": sourceURIRoot + "\x7f", "raw backslash": sourceURIRoot + `a\b`,
		"Unicode host": "https://cátalog.example/a", "Unicode path": sourceURIRoot + "🧸",
		"encoded Unicode": sourceURIRoot + "%c3%a9", "invalid UTF-8": sourceURIRoot + "\xff",
		"empty authority": "https:///catalog.example", "userinfo": "https://u:p@catalog.example/a",
		"username": "https://u@catalog.example/a", "empty userinfo": "https://@catalog.example/a",
		"escaped host": "https://cat%61log.example/a", "bracketed name": "https://[catalog.example]/",
		"empty port": "https://catalog.example:/", "different port": "https://catalog.example:444/",
		"padded port": "https://catalog.example:0443/", "duplicate port": "https://catalog.example:443:443/",
		"single label": "https://localhost/", "leading dot": "https://.catalog.example/",
		"trailing dot": "https://catalog.example./", "empty label": "https://catalog..example/",
		"leading hyphen": "https://-catalog.example/", "trailing hyphen": "https://catalog-.example/",
		"underscore host": "https://catalog_host.example/", "numeric final label": "https://catalog.123/",
		"label over limit": "https://" + label + "a.example/",
		"host over limit":  "https://" + host253 + "b/",
		"ipv4":             "https://127.0.0.1/", "ipv6": "https://[::1]/", "unbracketed ipv6": "https://::1/",
		"ipv6 zone": "https://[fe80::1%25eth0]/", "short ipv4": "https://127.1/",
		"single decimal ipv4": "https://2130706433/", "octal ipv4": "https://0177.0.0.1/",
		"hex ipv4": "https://0x7f.0x0.0x0.0x1/", "mixed radix ipv4": "https://0177.0x1/",
		"empty hex digits ipv4": "https://127.0x/", "single hex ipv4": "https://0x7f000001/",
		"query credentials": sourceURIRoot + "a?token=x", "empty query": sourceURIRoot + "?",
		"fragment": sourceURIRoot + "a#x", "empty fragment": sourceURIRoot + "#",
		"bare escape": sourceURIRoot + "%", "short escape": sourceURIRoot + "%2",
		"nonhex escape": sourceURIRoot + "%gg", "raw dot": sourceURIRoot + "./a",
		"raw dot-dot": sourceURIRoot + "a/..", "encoded dot": sourceURIRoot + "%2E",
		"encoded dot-dot": sourceURIRoot + "%2e%2e", "mixed dot-dot": sourceURIRoot + ".%2e/",
		"encoded slash": sourceURIRoot + "%2f", "encoded backslash": sourceURIRoot + "%5C",
		"encoded control": sourceURIRoot + "%0A", "encoded DEL": sourceURIRoot + "%7F",
		"encoded percent": sourceURIRoot + "%25", "double encoding": sourceURIRoot + "%252f",
		"encoded space": sourceURIRoot + "%20", "reserved path byte": sourceURIRoot + "a;b",
		"encoded reserved path byte": sourceURIRoot + "a%3bb",
		"leading repeated slash":     sourceURIRoot + "/a", "repeated slash": sourceURIRoot + "a//b",
		"relative": "catalog.example/a", "network path": "//catalog.example/a",
		"opaque HTTPS": "https:catalog.example/a", "one slash": "https:/catalog.example/a",
		"local path": "/local/path", "Windows path": `C:\private\file`,
		"file": "file:///private/file", "http": "http://catalog.example/a",
		"data": "data:text/plain,hello", "javascript": "javascript:alert(1)",
		"custom executable": "exec+app://catalog.example/run",
	}
	for name, raw := range rejected {
		t.Run(name, func(t *testing.T) {
			for range 2 {
				got, err := ParseSourceURI(raw)
				if got != (SourceURI{}) || err != ErrInvalidSourceURI {
					t.Fatalf("expected empty value and fixed error, got %v", err)
				}
			}
		})
	}

	t.Run("pure parser imports", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "source_uri.go", nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			if imported.Path.Value != `"strings"` && imported.Path.Value != `"errors"` {
				t.Fatalf("unexpected parser dependency %s", imported.Path.Value)
			}
		}
	})
}

func FuzzAssignmentSourceURI(f *testing.F) {
	for _, raw := range []string{
		"", sourceURIRoot, "HTTPS://Catalog.Example:443/a%62", "https://127.0x1/a",
		sourceURIRoot + "a?token=x", sourceURIRoot + "%2E%2e", sourceURIRoot + "%252f",
		"file:///private/file", sourceURIRoot + strings.Repeat("a", MaxSourceURIBytes-len(sourceURIRoot)),
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, err := ParseSourceURI(raw)
		again, againErr := ParseSourceURI(raw)
		if got != again || err != againErr {
			t.Fatal("nondeterministic parse")
		}
		if err != nil {
			if err != ErrInvalidSourceURI || got != (SourceURI{}) {
				t.Fatal("rejection exposed more than the fixed error")
			}
			return
		}
		if len(raw) > MaxSourceURIBytes || got.Profile() != SourceURIProfileV1 {
			t.Fatal("unbounded or unversioned admission")
		}
		restored, err := RestoreSourceURI(got.Profile(), got.Locator())
		if err != nil || restored != got || strings.ContainsAny(got.Locator(), "%?#\\") {
			t.Fatal("invalid canonical locator")
		}
	})
}
