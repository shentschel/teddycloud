package identity

import "errors"

const (
	payloadLength = 26
	idLength      = 4 + payloadLength
	alphabet      = "0123456789abcdefghjkmnpqrstvwxyz"
)

var (
	// ErrInvalidIDLength means an opaque ID was not exactly its canonical size.
	ErrInvalidIDLength = errors.New("invalid opaque identifier length")
	// ErrInvalidIDPrefix means an opaque ID belongs to a different aggregate.
	ErrInvalidIDPrefix = errors.New("invalid opaque identifier prefix")
	// ErrInvalidIDCharacter means an opaque ID contains a non-canonical byte.
	ErrInvalidIDCharacter = errors.New("invalid opaque identifier character")
)

// TagID is the stable internal identity of a physical Tag aggregate.
type TagID struct{ value string }

// ContentID is the stable internal identity of an editorial Content aggregate.
type ContentID struct{ value string }

// ContentVersionID is the stable internal identity of an immutable revision.
type ContentVersionID struct{ value string }

// ProductID is the stable internal identity of a commercial Product aggregate.
type ProductID struct{ value string }

func ParseTagID(text string) (TagID, error) {
	value, err := parse(text, "tag_")
	return TagID{value: value}, err
}

func ParseContentID(text string) (ContentID, error) {
	value, err := parse(text, "cnt_")
	return ContentID{value: value}, err
}

func ParseContentVersionID(text string) (ContentVersionID, error) {
	value, err := parse(text, "ver_")
	return ContentVersionID{value: value}, err
}

func ParseProductID(text string) (ProductID, error) {
	value, err := parse(text, "prd_")
	return ProductID{value: value}, err
}

func parse(text, prefix string) (string, error) {
	// Length is checked before scanning so arbitrarily large untrusted input has
	// bounded work and cannot be normalized into a valid identifier.
	if len(text) != idLength {
		return "", ErrInvalidIDLength
	}
	if text[:len(prefix)] != prefix {
		return "", ErrInvalidIDPrefix
	}
	for i := len(prefix); i < len(text); i++ {
		if !containsByte(alphabet, text[i]) {
			return "", ErrInvalidIDCharacter
		}
	}
	return text, nil
}

func containsByte(value string, target byte) bool {
	for i := range len(value) {
		if value[i] == target {
			return true
		}
	}
	return false
}

func (id TagID) String() string            { return id.value }
func (id ContentID) String() string        { return id.value }
func (id ContentVersionID) String() string { return id.value }
func (id ProductID) String() string        { return id.value }

func (id TagID) IsZero() bool            { return id.value == "" }
func (id ContentID) IsZero() bool        { return id.value == "" }
func (id ContentVersionID) IsZero() bool { return id.value == "" }
func (id ProductID) IsZero() bool        { return id.value == "" }
