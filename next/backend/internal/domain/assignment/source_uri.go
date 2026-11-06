package assignment

import (
	"errors"
	"strings"
)

const (
	SourceURIProfileV1 = "pi09-uri/1"
	MaxSourceURIBytes  = 2048
)

var ErrInvalidSourceURI = errors.New("invalid assignment source URI")

// SourceURI is descriptive provenance. Its zero value represents absence.
type SourceURI struct {
	profile string
	locator string
}

func (u SourceURI) Profile() string { return u.profile }
func (u SourceURI) Locator() string { return u.locator }

// ParseSourceURI admits a present locator under the current profile.
func ParseSourceURI(raw string) (SourceURI, error) {
	return parseSourceURIv1(raw)
}

func parseSourceURIv1(raw string) (SourceURI, error) {
	if len(raw) > MaxSourceURIBytes {
		return SourceURI{}, ErrInvalidSourceURI
	}
	for i := range len(raw) {
		b := raw[i]
		if b <= ' ' || b >= 0x7f || b == '\\' || b == '?' || b == '#' {
			return SourceURI{}, ErrInvalidSourceURI
		}
	}

	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || !strings.EqualFold(scheme, "https") {
		return SourceURI{}, ErrInvalidSourceURI
	}
	authority, path, hasPath := strings.Cut(rest, "/")
	host, port, hasPort := strings.Cut(authority, ":")
	if (hasPort && port != "443") || !sourceDNSHost(host) {
		return SourceURI{}, ErrInvalidSourceURI
	}

	canonical := strings.Builder{}
	canonical.Grow(len(raw) + 1)
	canonical.WriteString("https://")
	canonical.WriteString(strings.ToLower(host))
	canonical.WriteByte('/')
	if hasPath {
		for i := 0; i < len(path); i++ {
			b := path[i]
			if b == '%' {
				if i+2 >= len(path) {
					return SourceURI{}, ErrInvalidSourceURI
				}
				hi, lo := sourceHexDigit(path[i+1]), sourceHexDigit(path[i+2])
				if hi < 0 || lo < 0 {
					return SourceURI{}, ErrInvalidSourceURI
				}
				b = byte(hi*16 + lo)
				i += 2
			} else if b == '/' {
				canonical.WriteByte(b)
				continue
			}
			if !sourceAlnum(b) && b != '-' && b != '.' && b != '_' && b != '~' {
				return SourceURI{}, ErrInvalidSourceURI
			}
			canonical.WriteByte(b)
		}
	}
	locator := canonical.String()
	decodedPath := locator[len("https://")+len(host):]
	if strings.Contains(decodedPath, "//") {
		return SourceURI{}, ErrInvalidSourceURI
	}
	for segment := range strings.SplitSeq(decodedPath, "/") {
		if segment == "." || segment == ".." {
			return SourceURI{}, ErrInvalidSourceURI
		}
	}
	return SourceURI{profile: SourceURIProfileV1, locator: locator}, nil
}

func sourceDNSHost(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	allNumeric, lastDecimal := true, false
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		decimal := true
		for i := range len(label) {
			b := label[i]
			if !sourceAlnum(b) && b != '-' {
				return false
			}
			decimal = decimal && b >= '0' && b <= '9'
		}
		allNumeric = allNumeric && (decimal || sourceHexLabel(label))
		lastDecimal = decimal
	}
	return !lastDecimal && !allNumeric
}

func sourceHexLabel(label string) bool {
	if len(label) < 2 || label[0] != '0' || (label[1] != 'x' && label[1] != 'X') {
		return false
	}
	for i := 2; i < len(label); i++ {
		if sourceHexDigit(label[i]) < 0 {
			return false
		}
	}
	return true
}

func sourceAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func sourceHexDigit(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}
