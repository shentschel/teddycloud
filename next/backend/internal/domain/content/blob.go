package content

import (
	"encoding/hex"
	"errors"
)

var ErrInvalidBlobID = errors.New("invalid blob identity")

// BlobID identifies complete original bytes, including the envelope. Algorithm
// 1 is SHA256; neither AudioID nor a payload SHA1 can be converted into this ID.
// The zero value is unallocated; a raw all-zero digest remains a valid digest.
type BlobID struct {
	digest [32]byte
	valid  bool
}

func NewBlobID(digest [32]byte) BlobID { return BlobID{digest: digest, valid: true} }

func ParseBlobID(text string) (BlobID, error) {
	if len(text) != 71 || text[:7] != "sha256:" {
		return BlobID{}, ErrInvalidBlobID
	}
	for i := 7; i < len(text); i++ {
		if !(text[i] >= '0' && text[i] <= '9' || text[i] >= 'a' && text[i] <= 'f') {
			return BlobID{}, ErrInvalidBlobID
		}
	}
	var digest [32]byte
	_, _ = hex.Decode(digest[:], []byte(text[7:]))
	return NewBlobID(digest), nil
}

func (id BlobID) IsZero() bool     { return !id.valid }
func (id BlobID) Digest() [32]byte { return id.digest }
func (id BlobID) Algorithm() uint8 {
	if !id.valid {
		return 0
	}
	return 1
}
func (id BlobID) String() string {
	if !id.valid {
		return ""
	}
	return "sha256:" + hex.EncodeToString(id.digest[:])
}
