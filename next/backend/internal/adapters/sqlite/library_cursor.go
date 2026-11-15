package sqlite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"

	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const libraryCursorDomain = "pi10-library-cursor/1"

func libraryQueryDigest(q lib.Query, p lib.Page, scope lib.ReadScope, incarnation [32]byte) [32]byte {
	id, generation := scope.Binding()
	return libraryDigest(lib.Profile, q, p.Limit, id, generation, incarnation)
}
func libraryDigest(profile string, q lib.Query, limit uint64, id [32]byte, generation uint64, incarnation [32]byte) [32]byte {
	h := sha256.New()
	var n [8]byte
	text := func(s string) { binary.BigEndian.PutUint64(n[:], uint64(len(s))); h.Write(n[:]); h.Write([]byte(s)) }
	text(profile)
	owner := ""
	if q.OwnerContentID != nil {
		owner = q.OwnerContentID.String()
	}
	text(owner)
	text(q.Versions)
	text(q.Sort)
	search := ""
	if q.Search != nil {
		search = *q.Search
	}
	text(search)
	binary.BigEndian.PutUint64(n[:], limit)
	h.Write(n[:])
	h.Write(id[:])
	binary.BigEndian.PutUint64(n[:], generation)
	h.Write(n[:])
	h.Write(incarnation[:])
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
func libraryEncodeCursor(secret, snapshot, digest [32]byte, content, version string) string {
	payload := make([]byte, 125, 157)
	payload[0] = 1
	copy(payload[1:33], snapshot[:])
	copy(payload[33:63], content)
	copy(payload[63:93], version)
	copy(payload[93:125], digest[:])
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(libraryCursorDomain))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(payload))
}

type libraryPosition struct {
	snapshot         [32]byte
	content, version string
}

func libraryDecodeCursor(token string, secret, digest [32]byte) (libraryPosition, error) {
	var p libraryPosition
	if len(token) > lib.MaxCursorBytes {
		return p, lib.ErrLimitExceeded
	}
	if len(token) != 210 {
		return p, lib.ErrStaleCursor
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 157 || raw[0] != 1 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return p, lib.ErrStaleCursor
	}
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(libraryCursorDomain))
	mac.Write(raw[:125])
	if !hmac.Equal(raw[125:], mac.Sum(nil)) || !hmac.Equal(raw[93:125], digest[:]) {
		return p, lib.ErrStaleCursor
	}
	if _, err := identity.ParseContentID(string(raw[33:63])); err != nil {
		return p, lib.ErrStaleCursor
	}
	if _, err := identity.ParseContentVersionID(string(raw[63:93])); err != nil {
		return p, lib.ErrStaleCursor
	}
	copy(p.snapshot[:], raw[1:33])
	p.content = string(raw[33:63])
	p.version = string(raw[63:93])
	return p, nil
}
