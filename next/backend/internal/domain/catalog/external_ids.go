package catalog

import (
	"errors"
	"strconv"
)

const (
	maxProductIdentifierLength = 64
	audioHashLength            = 40
)

var (
	// ErrInvalidProductIdentifier means model/article text is empty, too long,
	// or contains bytes outside its deliberately conservative wire alphabet.
	ErrInvalidProductIdentifier = errors.New("invalid external product identifier")
	// ErrInvalidAudioID means an audio ID is zero or not canonical decimal text.
	ErrInvalidAudioID = errors.New("invalid audio identifier")
	// ErrInvalidAudioHash means an audio hash is not exactly 40 hexadecimal digits.
	ErrInvalidAudioHash = errors.New("invalid audio hash")
	// ErrInvalidFingerprint means an audio fingerprint contains a zero component.
	ErrInvalidFingerprint = errors.New("invalid audio fingerprint")
)

// ModelNumber is a provider-observed member model, not aggregate identity.
type ModelNumber struct{ value string }

// ArticleNumber is a provider-observed commercial article, not a member model.
type ArticleNumber struct{ value string }

// AudioID is a provider-observed numeric audio identifier. Numeric order has no
// version-order meaning.
type AudioID struct{ value uint64 }

// AudioHash is the canonical lower-case legacy 20-byte catalog hash.
type AudioHash struct{ value string }

// AudioFingerprint keeps audio ID and hash inseparable for exact matching.
// The pair is evidence and is not a globally unique aggregate identifier.
type AudioFingerprint struct {
	audioID AudioID
	hash    AudioHash
}

// ProductIdentifiers keeps optional model and article observations separate.
type ProductIdentifiers struct {
	model      ModelNumber
	hasModel   bool
	article    ArticleNumber
	hasArticle bool
}

func ParseModelNumber(text string) (ModelNumber, error) {
	if !validProductIdentifier(text) {
		return ModelNumber{}, ErrInvalidProductIdentifier
	}
	return ModelNumber{value: text}, nil
}

func ParseArticleNumber(text string) (ArticleNumber, error) {
	if !validProductIdentifier(text) {
		return ArticleNumber{}, ErrInvalidProductIdentifier
	}
	return ArticleNumber{value: text}, nil
}

func NewAudioID(value uint64) (AudioID, error) {
	if value == 0 {
		return AudioID{}, ErrInvalidAudioID
	}
	return AudioID{value: value}, nil
}

func ParseAudioID(text string) (AudioID, error) {
	if len(text) == 0 || len(text) > 20 || (len(text) > 1 && text[0] == '0') {
		return AudioID{}, ErrInvalidAudioID
	}
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			return AudioID{}, ErrInvalidAudioID
		}
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return AudioID{}, ErrInvalidAudioID
	}
	return NewAudioID(value)
}

func ParseAudioHash(text string) (AudioHash, error) {
	if len(text) != audioHashLength {
		return AudioHash{}, ErrInvalidAudioHash
	}
	canonical := make([]byte, audioHashLength)
	for i := range len(text) {
		value := text[i]
		switch {
		case value >= '0' && value <= '9', value >= 'a' && value <= 'f':
			canonical[i] = value
		case value >= 'A' && value <= 'F':
			canonical[i] = value + ('a' - 'A')
		default:
			return AudioHash{}, ErrInvalidAudioHash
		}
	}
	return AudioHash{value: string(canonical)}, nil
}

func NewAudioFingerprint(audioID AudioID, hash AudioHash) (AudioFingerprint, error) {
	if audioID.IsZero() || hash.IsZero() {
		return AudioFingerprint{}, ErrInvalidFingerprint
	}
	return AudioFingerprint{audioID: audioID, hash: hash}, nil
}

func NewProductIdentifiers(model *ModelNumber, article *ArticleNumber) ProductIdentifiers {
	var result ProductIdentifiers
	if model != nil {
		result.model = *model
		result.hasModel = true
	}
	if article != nil {
		result.article = *article
		result.hasArticle = true
	}
	return result
}

func validProductIdentifier(text string) bool {
	if len(text) == 0 || len(text) > maxProductIdentifierLength {
		return false
	}
	for i := range len(text) {
		value := text[i]
		if !((value >= 'a' && value <= 'z') ||
			(value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') ||
			value == '-' || value == '_' || value == '.') {
			return false
		}
	}
	return true
}

func (id ModelNumber) String() string   { return id.value }
func (id ArticleNumber) String() string { return id.value }
func (id AudioID) Uint64() uint64       { return id.value }
func (id AudioID) String() string       { return strconv.FormatUint(id.value, 10) }
func (id AudioHash) String() string     { return id.value }
func (id AudioID) IsZero() bool         { return id.value == 0 }
func (id AudioHash) IsZero() bool       { return id.value == "" }

func (fingerprint AudioFingerprint) AudioID() AudioID { return fingerprint.audioID }
func (fingerprint AudioFingerprint) Hash() AudioHash  { return fingerprint.hash }
func (fingerprint AudioFingerprint) IsZero() bool {
	return fingerprint.audioID.IsZero() || fingerprint.hash.IsZero()
}

func (ids ProductIdentifiers) Model() (ModelNumber, bool) { return ids.model, ids.hasModel }
func (ids ProductIdentifiers) Article() (ArticleNumber, bool) {
	return ids.article, ids.hasArticle
}
