// Package library defines the trusted local, all-catalog inventory reader.
package library

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"

	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const (
	Profile                 = "pi10-library/1"
	All                     = "all"
	Current                 = "current"
	IdentityAsc             = "identity-asc"
	TitleAsc                = "title-asc"
	TitleDesc               = "title-desc"
	MaxItems         uint64 = 128
	MaxResponseBytes uint64 = 1 << 20
	MaxCursorBytes          = 256
	MaxContents      uint64 = 100000
	MaxVersions      uint64 = 100000
	MaxStagingBytes  uint64 = 128 << 20
	MaxIndexBytes    uint64 = 256 << 20
)

// Pointers distinguish absent from present zero/empty options.
type Query struct {
	OwnerContentID *identity.ContentID
	Versions       string
	Sort           string
	Search         *string
}
type Page struct {
	Limit            uint64
	Cursor           string
	ExpectedSnapshot string
}
type Baseline struct {
	Title   string
	Model   *string
	Article *string
}
type Field struct {
	Status domain.CatalogFactStatus
	Value  *string
}
type Supersession struct {
	Status          catalog.SupersessionStatus
	Revision        uint64
	Predecessor     *string
	Successor       *string
	Terminal        *string
	IncomingCommand *string
	OutgoingCommand *string
}

// Strings below are canonical immutable IDs, copied only after complete source
// reconstruction. Optional values encode as null, never an empty substitute.
type Item struct {
	ContentID       string
	VersionID       string
	AudioID         uint64
	Hash            string
	ContentRevision uint64
	Review          catalog.CatalogReviewStatus
	ProtectedCustom bool
	Baseline        Baseline
	Title           Field
	Model           Field
	Article         Field
	Supersession    Supersession
	Media           catalog.MediaState
}
type Result struct {
	Items        []Item
	Snapshot     string
	Complete     bool
	Next         string
	Inspected    uint64
	EncodedBytes uint64
}

func NormalizeQuery(q Query, p Page) (Query, error) {
	if p.Limit == 0 {
		return Query{}, ErrInvalidInput
	}
	if p.Limit > MaxItems || len(p.Cursor) > MaxCursorBytes || len(p.ExpectedSnapshot) > 43 {
		return Query{}, ErrLimitExceeded
	}
	if p.Cursor != "" && p.ExpectedSnapshot == "" {
		return Query{}, ErrInvalidInput
	}
	if p.ExpectedSnapshot != "" {
		raw, err := base64.RawURLEncoding.Strict().DecodeString(p.ExpectedSnapshot)
		if err != nil || len(raw) != 32 || len(p.ExpectedSnapshot) != 43 || base64.RawURLEncoding.EncodeToString(raw) != p.ExpectedSnapshot {
			return Query{}, ErrInvalidInput
		}
	}
	if q.OwnerContentID != nil && q.OwnerContentID.IsZero() {
		return Query{}, ErrInvalidInput
	}
	if q.Versions == "" {
		q.Versions = All
	}
	if q.Sort == "" {
		q.Sort = IdentityAsc
	}
	if q.Versions == "mine" || q.Versions == "shared" {
		return Query{}, ErrUnsupported
	}
	if q.Versions != All && q.Versions != Current || q.Sort != IdentityAsc && q.Sort != TitleAsc && q.Sort != TitleDesc {
		return Query{}, ErrInvalidInput
	}
	if q.Search != nil {
		normalized, err := NormalizeSearch(*q.Search)
		if err != nil {
			return Query{}, err
		}
		q.Search = nil
		if normalized != "" {
			q.Search = &normalized
		}
	}
	if q.Search != nil && q.Sort == IdentityAsc {
		return Query{}, ErrUnsupported
	}

	return q, nil
}

// CanonicalJSON is the fixed internal accounting format, not a public DTO.
// encoding/json leaves UTF-8 intact; disabling HTML escaping preserves literal
// <>&. U+2028/U+2029 are replaced with their standard UTF-8 spelling as well.
func CanonicalJSON(value any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, ErrUnavailable
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	// json.Encoder always escapes these codepoints. Restore them only inside
	// string values, distinguishing a literal backslash from a JSON escape.
	var out bytes.Buffer
	quoted := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '"' {
			quoted = !quoted
		}
		if quoted && c == '\\' && i+1 < len(raw) {
			if i+5 < len(raw) && (string(raw[i:i+6]) == `\u2028` || string(raw[i:i+6]) == `\u2029`) {
				if raw[i+5] == '8' {
					out.WriteRune('\u2028')
				} else {
					out.WriteRune('\u2029')
				}
				i += 5
				continue
			}
			out.WriteByte(c)
			i++
			out.WriteByte(raw[i])
			continue
		}
		out.WriteByte(c)
	}
	return out.Bytes(), nil
}

func Account(result Result) (Result, error) {
	if result.Items == nil {
		result.Items = []Item{}
	}
	for {
		raw, err := CanonicalJSON(result)
		if err != nil {
			return Result{}, err
		}
		size := uint64(len(raw))
		if result.EncodedBytes == size {
			return result, nil
		}
		result.EncodedBytes = size
	}
}

func ValidateResult(p Page, r Result) error {
	return ValidateQueryResult(Query{Sort: IdentityAsc, Versions: All}, p, r)
}

func ValidateQueryResult(q Query, p Page, r Result) error {
	if uint64(len(r.Items)) > p.Limit || r.Inspected > p.Limit+1 || r.Inspected < uint64(len(r.Items)) || r.Complete != (r.Next == "") || !r.Complete && len(r.Items) == 0 {
		return ErrUnavailable
	}
	if len(r.Snapshot) != 43 || r.EncodedBytes > MaxResponseBytes || len(r.Next) > MaxCursorBytes {
		return ErrUnavailable
	}
	rawSnapshot, e := base64.RawURLEncoding.Strict().DecodeString(r.Snapshot)
	if e != nil || len(rawSnapshot) != 32 || base64.RawURLEncoding.EncodeToString(rawSnapshot) != r.Snapshot {
		return ErrUnavailable
	}
	for i, item := range r.Items {
		if err := ValidateItem(item); err != nil {
			return err
		}
		if q.OwnerContentID != nil && item.ContentID != q.OwnerContentID.String() || q.Versions == Current && item.Supersession.Status != catalog.Current {
			return ErrUnavailable
		}
		key, e := TitleKey(item)
		if e != nil || q.Search != nil && (key == "" || !strings.HasPrefix(key, *q.Search)) {
			return ErrUnavailable
		}
		if i > 0 {
			cmp, e := CompareItems(r.Items[i-1], item, q.Sort)
			if e != nil || cmp >= 0 {
				return ErrUnavailable
			}
		}
	}
	copy, err := Account(r)
	if err != nil || copy.EncodedBytes != r.EncodedBytes {
		return ErrUnavailable
	}
	return nil
}

// ValidateItem checks compact projection representation before response encoding
// can allocate. Complete provenance/graph authority remains build-time PI-08 work.
func ValidateItem(item Item) error {
	if _, err := identity.ParseContentID(item.ContentID); err != nil {
		return ErrUnavailable
	}
	if _, err := identity.ParseContentVersionID(item.VersionID); err != nil {
		return ErrUnavailable
	}
	hash, err := domain.ParseAudioHash(item.Hash)
	if err != nil || hash.String() != item.Hash || item.AudioID == 0 || item.AudioID > math.MaxUint32 || item.ContentRevision == 0 || item.ContentRevision > math.MaxInt64 || item.Review > catalog.CatalogReviewed || item.Media != catalog.CatalogOnly && item.Media != catalog.MediaBoundUnchecked {
		return ErrUnavailable
	}
	if len(item.Baseline.Title) > 4096 || item.Baseline.Title != "" && !evidence.ValidText(item.Baseline.Title, 4096) {
		return ErrUnavailable
	}
	if item.Baseline.Model != nil {
		if _, err := domain.ParseModelNumber(*item.Baseline.Model); err != nil {
			return ErrUnavailable
		}
	}
	if item.Baseline.Article != nil {
		if _, err := domain.ParseArticleNumber(*item.Baseline.Article); err != nil {
			return ErrUnavailable
		}
	}
	reviewed := false
	for i, f := range []Field{item.Title, item.Model, item.Article} {
		if f.Status > domain.CatalogFactConflict || (f.Status == domain.CatalogFactAccepted) != (f.Value != nil) {
			return ErrUnavailable
		}
		if f.Status != domain.CatalogFactUnknown {
			reviewed = true
		}
		if f.Value != nil {
			if !evidence.ValidText(*f.Value, 4096) {
				return ErrUnavailable
			}
			if i == 1 {
				if _, err := domain.ParseModelNumber(*f.Value); err != nil {
					return ErrUnavailable
				}
			}
			if i == 2 {
				if _, err := domain.ParseArticleNumber(*f.Value); err != nil {
					return ErrUnavailable
				}
			}
		}
	}
	if reviewed != (item.Review == catalog.CatalogReviewed) || item.Review == catalog.CatalogUnmanaged && item.ProtectedCustom {
		return ErrUnavailable
	}
	s := item.Supersession
	if s.Status > catalog.Superseded {
		return ErrUnavailable
	}
	for _, v := range []*string{s.Predecessor, s.Successor, s.Terminal} {
		if v != nil {
			if _, err := identity.ParseContentVersionID(*v); err != nil {
				return ErrUnavailable
			}
		}
	}
	for _, v := range []*string{s.IncomingCommand, s.OutgoingCommand} {
		if v != nil {
			raw, err := hex.DecodeString(*v)
			if err != nil || len(raw) != 16 || strings.ToLower(*v) != *v || *v == strings.Repeat("0", 32) {
				return ErrUnavailable
			}
		}
	}
	if s.Status == catalog.Unavailable {
		if s.Revision != 0 || s.Predecessor != nil || s.Successor != nil || s.Terminal != nil || s.IncomingCommand != nil || s.OutgoingCommand != nil {
			return ErrUnavailable
		}
	} else {
		if item.Review == catalog.CatalogUnmanaged || s.Revision == 0 || s.Revision > 1024 || s.Revision > item.ContentRevision || s.Terminal == nil || (s.Predecessor == nil) != (s.IncomingCommand == nil) || (s.Successor == nil) != (s.OutgoingCommand == nil) {
			return ErrUnavailable
		}
		if s.Status == catalog.Current && (s.Successor != nil || *s.Terminal != item.VersionID) || s.Status == catalog.Superseded && (s.Successor == nil || *s.Terminal == item.VersionID) {
			return ErrUnavailable
		}
		if s.Predecessor != nil && *s.Predecessor == item.VersionID || s.Successor != nil && *s.Successor == item.VersionID {
			return ErrUnavailable
		}
	}
	return nil
}
