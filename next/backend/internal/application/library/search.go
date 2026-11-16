package library

import (
	"strings"
	"unicode/utf8"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

// SearchProfile freezes the byte-preserving normalization used by index keys
// and cursor-bound search values. No locale or Unicode case folding is involved.
const SearchProfile = "ascii-prefix/1"
const MaxSearchBytes = 256

func NormalizeTitle(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrInvalidInput
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return "", ErrInvalidInput
		}
	}
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

func NormalizeSearch(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrInvalidInput
	}
	if len(s) > MaxSearchBytes {
		return "", ErrLimitExceeded
	}
	normalized, err := NormalizeTitle(s)
	if err != nil {
		return "", err
	}
	if len(normalized) > MaxSearchBytes {
		return "", ErrLimitExceeded
	}
	return normalized, nil
}

// PrefixSuccessor returns the exclusive binary upper bound, which need not be
// UTF-8. An empty or all-0xff prefix has no finite successor.
func PrefixSuccessor(prefix string) (string, bool) {
	for i := len(prefix) - 1; i >= 0; i-- {
		if prefix[i] != 255 {
			b := []byte(prefix[:i+1])
			b[i]++
			return string(b), true
		}
	}
	return "", false
}

func TitleKey(item Item) (string, error) {
	if item.Title.Status != domain.CatalogFactAccepted || item.Title.Value == nil {
		return "", nil
	}
	return NormalizeTitle(*item.Title.Value)
}

// CompareItems implements D3's mixed comparator; identities always ascend,
// including ties within descending title order and both missing-title groups.
func CompareItems(a, b Item, sort string) (int, error) {
	if sort != IdentityAsc && sort != TitleAsc && sort != TitleDesc {
		return 0, ErrInvalidInput
	}
	if sort != IdentityAsc {
		ak, err := TitleKey(a)
		if err != nil {
			return 0, ErrUnavailable
		}
		bk, err := TitleKey(b)
		if err != nil {
			return 0, ErrUnavailable
		}
		if ak == "" && bk != "" {
			return 1, nil
		}
		if ak != "" && bk == "" {
			return -1, nil
		}
		cmp := strings.Compare(ak, bk)
		if sort == TitleDesc {
			cmp = -cmp
		} else if sort != TitleAsc {
			return 0, ErrInvalidInput
		}
		if cmp != 0 {
			return cmp, nil
		}
	}
	if c := strings.Compare(a.ContentID, b.ContentID); c != 0 {
		return c, nil
	}
	return strings.Compare(a.VersionID, b.VersionID), nil
}
