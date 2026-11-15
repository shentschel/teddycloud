package sqlite

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"

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
			if p.Cursor != "" {
				var exists int
				e := s.index.QueryRowContext(c, "SELECT 1 FROM items WHERE content=? AND version=?", position.content, position.version).Scan(&exists)
				if errors.Is(e, sql.ErrNoRows) {
					return lib.ErrStaleCursor
				}
				if e != nil {
					return libraryMapError(c, e)
				}
			}
			rows, e := s.index.QueryContext(c, "SELECT CASE WHEN typeof(payload)='blob' AND length(payload)<=32768 THEN payload END FROM items WHERE (content,version)>(?,?) ORDER BY content,version LIMIT ?", position.content, position.version, p.Limit+1)
			if e != nil {
				return libraryMapError(c, e)
			}
			candidates := make([]lib.Item, 0, p.Limit+1)
			for rows.Next() {
				var raw []byte
				if e = rows.Scan(&raw); e != nil {
					break
				}
				var item lib.Item
				if e = json.Unmarshal(raw, &item); e != nil {
					e = lib.ErrUnavailable
					break
				}
				if e = lib.ValidateItem(item); e != nil {
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
			result, e = libraryPartition(candidates, p, librarySnapshot(s), func(last lib.Item) string {
				return libraryEncodeCursor(s.secret, s.snapshot, digest, last.ContentID, last.VersionID)
			})
			return e
		})
		if errors.Is(e, lib.ErrStaleCursor) && p.Cursor == "" && p.ExpectedSnapshot == "" {
			return lib.ErrIndexNotReady
		}
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
