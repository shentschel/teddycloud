package sqlite

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"strings"

	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
)

func (r *libraryRead) ListLibrary(ctx context.Context, q lib.Query, p lib.Page) (result lib.Result, err error) {
	err = r.use(ctx, func(c context.Context) error {
		canonical, e := lib.NormalizeQuery(q, p)
		if e != nil {
			return e
		}
		q = canonical
		r.initialPage = p.Cursor == "" && p.ExpectedSnapshot == ""
		s := r.owner.library
		digest := libraryQueryDigest(q, p, r.scope, s.incarnation)
		position := libraryPosition{}
		var candidates []lib.Item
		if p.Cursor != "" {
			position, e = libraryDecodeCursor(p.Cursor, s.secret, digest)
			if e != nil {
				return e
			}
		}
		if s.index == nil || !s.progress.Ready {
			if p.Cursor != "" || p.ExpectedSnapshot != "" {
				return lib.ErrStaleCursor
			}
			return lib.ErrIndexNotReady
		}
		if p.ExpectedSnapshot != "" && p.ExpectedSnapshot != librarySnapshot(s) {
			return lib.ErrStaleCursor
		}
		if p.Cursor != "" && !hmac.Equal(position.snapshot[:], s.snapshot[:]) {
			return lib.ErrStaleCursor
		}
		e = r.source(c, true, func(c context.Context, tx *sql.Tx, v int, epoch libraryEpoch) error {
			// Exact identity recovery and candidate selection are covering B-tree seeks.
			// No source aggregate query or per-item detail/service call occurs here.
			var last []byte
			if p.Cursor != "" {
				var raw []byte
				e := s.index.QueryRowContext(c, "SELECT CASE WHEN typeof(payload)='blob' AND length(payload)<=32768 THEN payload END FROM items WHERE content=? AND version=?", position.content, position.version).Scan(&raw)
				if errors.Is(e, sql.ErrNoRows) {
					return lib.ErrStaleCursor
				}
				if e != nil {
					return libraryMapError(c, e)
				}
				item, e := libraryDecodeItem(raw, position.content, position.version)
				if e != nil {
					return e
				}
				if !libraryMatches(q, item) {
					return lib.ErrStaleCursor
				}
				keys, e := libraryItemKeys(item)
				if e != nil {
					return e
				}
				last = libraryOrderKey(q.Sort, keys)
			}
			statement, args := libraryRange(q, p.Limit, last)
			rows, e := s.index.QueryContext(c, statement, args...)
			if e != nil {
				return libraryMapError(c, e)
			}
			candidates = make([]lib.Item, 0, p.Limit+1)
			for rows.Next() {
				var raw, key []byte
				if e = rows.Scan(&key, &raw); e != nil {
					break
				}
				if len(key) < 60 {
					e = lib.ErrUnavailable
					break
				}
				item, decodeErr := libraryDecodeItem(raw, string(key[len(key)-60:len(key)-30]), string(key[len(key)-30:]))
				if decodeErr != nil {
					e = decodeErr
					break
				}
				candidates = append(candidates, item)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return libraryMapError(c, e)
			}
			return nil
		})
		if errors.Is(e, lib.ErrStaleCursor) && p.Cursor == "" && p.ExpectedSnapshot == "" {
			return lib.ErrIndexNotReady
		}
		if e != nil {
			return e
		}
		result, e = libraryPartition(candidates, p, librarySnapshot(s), func(last lib.Item) string {
			return libraryEncodeCursor(s.secret, s.snapshot, digest, last.ContentID, last.VersionID)
		})
		return e
	})
	if err != nil {
		return lib.Result{}, err
	}
	return result, nil
}

func libraryPartition(candidates []lib.Item, p lib.Page, snapshot string, cursor func(lib.Item) string) (lib.Result, error) {
	count := len(candidates)
	if uint64(count) > p.Limit {
		count = int(p.Limit)
	}
	for n := count; n >= 0; n-- {
		complete := n == len(candidates)
		if n == 0 && !complete {
			return lib.Result{}, lib.ErrLimitExceeded
		}
		result := lib.Result{Items: candidates[:n], Snapshot: snapshot, Complete: complete, Inspected: uint64(len(candidates))}
		if !complete {
			result.Next = cursor(candidates[n-1])
		}
		result, e := lib.Account(result)
		if e != nil {
			return lib.Result{}, e
		}
		if result.EncodedBytes <= lib.MaxResponseBytes {
			return result, nil
		}
	}
	return lib.Result{}, lib.ErrLimitExceeded
}

func libraryOrderKey(sort string, k libraryKeys) []byte {
	switch sort {
	case lib.TitleAsc:
		return k.asc
	case lib.TitleDesc:
		return k.desc
	default:
		return k.identity
	}
}
func libraryMatches(q lib.Query, item lib.Item) bool {
	if q.OwnerContentID != nil && item.ContentID != q.OwnerContentID.String() || q.Versions == lib.Current && item.Supersession.Status != catalog.Current {
		return false
	}
	title, err := lib.TitleKey(item)
	return err == nil && (q.Search == nil || title != "" && strings.HasPrefix(title, *q.Search))
}

// The binary title encoding has a terminator smaller (ascending) or larger
// (descending) than every valid title byte. Inversion therefore implements the
// exact mixed comparator, including a title that is a prefix of another title.
func libraryRange(q lib.Query, limit uint64, last []byte) (string, []any) {
	column, index := "identity_key", "library_identity"
	switch q.Sort {
	case lib.TitleAsc:
		column, index = "asc_key", "library_title_asc"
	case lib.TitleDesc:
		column, index = "desc_key", "library_title_desc"
	}
	owner := ""
	if q.OwnerContentID != nil {
		owner = q.OwnerContentID.String()
	}
	lower := []byte{}
	var upper []byte
	if q.Search != nil {
		lower = append([]byte{0}, []byte(*q.Search)...)
		if q.Sort == lib.TitleDesc {
			for i := 1; i < len(lower); i++ {
				lower[i] = ^lower[i]
			}
		}
		if next, ok := lib.PrefixSuccessor(string(lower)); ok {
			upper = []byte(next)
		}
	}
	op := ">="
	if last != nil && bytes.Compare(last, lower) >= 0 {
		lower = last
		op = ">"
	}
	statement := "SELECT " + column + ",CASE WHEN typeof(payload)='blob' AND length(payload)<=32768 THEN payload END FROM memberships INDEXED BY " + index + " WHERE lane=? AND owner=? AND " + column + op + "?"
	args := []any{q.Versions, owner, lower}
	if upper != nil {
		statement += " AND " + column + "<?"
		args = append(args, upper)
	}
	statement += " ORDER BY " + column + " LIMIT ?"
	args = append(args, limit+1)
	return statement, args
}
