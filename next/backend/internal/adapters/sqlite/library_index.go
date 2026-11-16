package sqlite

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	sqliteDriver "modernc.org/sqlite"
)

type libraryEpoch struct {
	connection    uint64
	changes, data int64
}
type libraryState struct {
	closed              atomic.Bool
	authority           *lib.ReadAuthority
	incarnation, secret [32]byte
	index               *sql.DB
	epoch               libraryEpoch
	snapshot            [32]byte
	progress            lib.Preparation
	after               string
	building            bool
}

func newLibraryState() *libraryState {
	s := &libraryState{authority: lib.NewReadAuthority()}
	s.rotate()
	return s
}
func (s *libraryState) rotate() { _, _ = rand.Read(s.incarnation[:]); _, _ = rand.Read(s.secret[:]) }
func (s *libraryState) discard() {
	if s.index != nil {
		_ = s.index.Close()
	}
	s.index = nil
	s.progress = lib.Preparation{}
	s.after = ""
	s.building = false
	s.snapshot = [32]byte{}
}
func (s *libraryState) publish() {
	// A nonce gives every rebuild a new generation even for identical source.
	var nonce [32]byte
	_, _ = rand.Read(nonce[:])
	h := sha256.New()
	h.Write([]byte(lib.Profile))
	h.Write([]byte(libraryIndexLayout))
	h.Write(s.incarnation[:])
	h.Write(nonce[:])
	var n [8]byte
	for _, v := range []uint64{s.epoch.connection, uint64(s.epoch.changes), uint64(s.epoch.data)} {
		binary.BigEndian.PutUint64(n[:], v)
		h.Write(n[:])
	}
	copy(s.snapshot[:], h.Sum(nil))
	s.progress.Ready = true
	s.building = false
}
func libraryMapError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, catalog.ErrUnsupported) {
		return lib.ErrUnsupported
	}
	if errors.Is(err, catalog.ErrLimitExceeded) {
		return lib.ErrLimitExceeded
	}
	if errors.Is(err, catalog.ErrRepositoryContention) {
		return lib.ErrBusy
	}
	var failure *sqliteDriver.Error
	if errors.As(err, &failure) && failure.Code()&255 == 13 {
		return lib.ErrLimitExceeded
	}
	if errors.As(err, &failure) && (failure.Code()&255 == 5 || failure.Code()&255 == 6) {
		return lib.BoundaryError(ctx, lib.ErrBusy)
	}
	return lib.BoundaryError(ctx, err)
}
func (s *libraryState) sample(ctx context.Context, c *sql.Conn, d *Database) (libraryEpoch, error) {
	e := libraryEpoch{connection: d.physicalGeneration.Load()}
	err := c.QueryRowContext(ctx, "SELECT total_changes(),data_version FROM pragma_data_version").Scan(&e.changes, &e.data)
	if err != nil {
		return e, libraryMapError(ctx, err)
	}
	if e.connection == 0 || e.changes < 0 || e.data < 0 {
		return e, lib.ErrUnavailable
	}
	return e, nil
}

type libraryRead struct {
	mu           sync.Mutex
	owner        *LifecycleOwner
	scope        lib.ReadScope
	ctx          context.Context
	active, used bool
	releaseEpoch *libraryEpoch
	failure      error
	initialPage  bool
}
type libraryOperationKey struct{}

var _ lib.Owner = (*LifecycleOwner)(nil)
var _ lib.Reader = (*libraryRead)(nil)

func (owner *LifecycleOwner) WithinLibraryRead(ctx context.Context, scope lib.ReadScope, operation func(context.Context, lib.Reader) error) (err error) {
	if ctx == nil {
		return lib.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := owner.AuthorizeLibrary(scope); err != nil {
		return err
	}
	if operation == nil {
		return lib.ErrInvalidInput
	}
	if ctx.Value(libraryOperationKey{}) != nil || ctx.Value(contentOperationContextKey{}) != nil {
		return lib.ErrBusy
	}
	ctx, cancel := context.WithTimeout(context.WithValue(context.WithValue(ctx, contentOperationContextKey{}, owner), libraryOperationKey{}, owner), lib.OperationTimeout)
	defer cancel()
	if err := owner.gate.enterContentOperation(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return lib.ErrBusy
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return lib.ErrUnauthorized
	}
	if err := owner.AuthorizeLibrary(scope); err != nil {
		return err
	}
	read := &libraryRead{owner: owner, scope: scope, ctx: ctx, active: true}
	defer func() { read.mu.Lock(); read.active = false; read.mu.Unlock() }()
	err = operation(ctx, read)
	read.mu.Lock()
	read.active = false
	if err == nil {
		err = read.failure
	}
	read.mu.Unlock()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && read.releaseEpoch != nil {
		// Recheck after application response validation, before releasing the owner
		// fence. A reconnected physical connection cannot compare equal.
		conn, e := owner.database.db.Conn(ctx)
		if e != nil {
			err = libraryMapError(ctx, e)
		} else {
			final, e := owner.library.sample(ctx, conn, owner.database)
			_ = conn.Close()
			if e != nil {
				err = e
			} else if final != *read.releaseEpoch {
				err = lib.ErrStaleCursor
				if read.initialPage {
					err = lib.ErrIndexNotReady
				}
			}
		}
		if err != nil {
			owner.library.discard()
		}
	}
	if err == nil {
		err = owner.AuthorizeLibrary(scope)
	}
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, lib.ErrUnavailable)) {
		owner.library.discard()
	}
	return libraryMapError(ctx, err)
}

// One repository use per callback. Handles cannot escape or recursively enter
// another owner operation, nor retry a failed operation inside the same lease.
func (r *libraryRead) use(ctx context.Context, op func(context.Context) error) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		if err != nil {
			r.failure = err
		}
	}()
	if !r.active {
		return lib.ErrUnauthorized
	}
	if r.used {
		return lib.ErrBusy
	}
	r.used = true
	if err := r.owner.AuthorizeLibrary(r.scope); err != nil {
		return err
	}
	if ctx == nil {
		return lib.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	call, cancel := context.WithCancel(r.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	err = op(call)
	if ctx.Err() != nil {
		r.owner.library.discard()
		return ctx.Err()
	}
	return libraryMapError(call, err)
}

// Epoch is sampled on the same explicitly identified physical connection before
// BEGIN, after its end and immediately before release under the lifecycle gate.
// No retained source transaction or capability survives this operation.
func (r *libraryRead) source(ctx context.Context, requireEpoch bool, op func(context.Context, *sql.Tx, int, libraryEpoch) error) (err error) {
	s := r.owner.library
	call, cancel := context.WithTimeout(ctx, lib.TransactionTimeout)
	defer cancel()
	conn, err := r.owner.database.db.Conn(call)
	if err != nil {
		return libraryMapError(call, err)
	}
	defer conn.Close()
	failed := true
	defer func() {
		if failed {
			s.discard()
		}
	}()
	before, err := s.sample(call, conn, r.owner.database)
	if err != nil {
		return err
	}
	if requireEpoch && before.connection != s.epoch.connection {
		return lib.ErrStaleCursor
	}
	tx, err := conn.BeginTx(call, nil)
	if err != nil {
		return libraryMapError(call, err)
	}
	ended := false
	defer func() {
		if !ended {
			if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
				err = lib.ErrUnavailable
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
	}()
	var version, dirty int
	if err = tx.QueryRowContext(call, "SELECT COALESCE(MAX(version),0),COALESCE(MAX(dirty),0) FROM tc_schema_migrations").Scan(&version, &dirty); err != nil {
		return libraryMapError(call, err)
	}
	if version != 9 && version != 10 || dirty != 0 {
		return lib.ErrUnsupported
	}
	if requireEpoch && before != s.epoch {
		return lib.ErrStaleCursor
	}
	if err = op(call, tx, version, before); err != nil {
		return err
	}
	if err = tx.Rollback(); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return lib.ErrUnavailable
	}
	ended = true
	after, err := s.sample(call, conn, r.owner.database)
	if err != nil {
		return err
	}
	if before != after {
		return lib.ErrStaleCursor
	}
	final, err := s.sample(call, conn, r.owner.database)
	if err != nil {
		return err
	}
	if before != final {
		return lib.ErrStaleCursor
	}
	if err = r.owner.AuthorizeLibrary(r.scope); err != nil {
		return err
	}
	if err = call.Err(); err != nil {
		return err
	}
	r.releaseEpoch = &before
	failed = false
	return nil
}
func (r *libraryRead) Prepare(ctx context.Context) (p lib.Preparation, err error) {
	err = r.use(ctx, func(c context.Context) error {
		s := r.owner.library
		s.discard()
		connector, e := sqliteDriver.NewConnector(":memory:")
		if e != nil {
			return lib.ErrUnavailable
		}
		s.index = sql.OpenDB(connector)
		s.index.SetMaxOpenConns(1)
		s.index.SetMaxIdleConns(1)
		if _, e = s.index.ExecContext(c, libraryIndexSchema); e != nil {
			s.discard()
			return libraryMapError(c, e)
		}
		e = r.source(c, false, func(c context.Context, tx *sql.Tx, v int, epoch libraryEpoch) error {
			// Bounded admission counts reject impossible builds before reconstruction.
			var contents, versions uint64
			if e := tx.QueryRowContext(c, "SELECT (SELECT count(*) FROM (SELECT 1 FROM tc_catalog_content LIMIT 100001)),(SELECT count(*) FROM (SELECT 1 FROM tc_content_versions LIMIT 100001))").Scan(&contents, &versions); e != nil {
				return libraryMapError(c, e)
			}
			if contents > lib.MaxContents || versions > lib.MaxVersions {
				return lib.ErrLimitExceeded
			}
			var corrupt bool
			if e := tx.QueryRowContext(c, "SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check LIMIT 1)").Scan(&corrupt); e != nil {
				return libraryMapError(c, e)
			}
			if corrupt {
				return lib.ErrUnavailable
			}
			repo := &catalogRepository{transaction: tx, ctx: c, schemaVersion: v}
			if e := repo.supersessionReadSchema(c); e != nil {
				return libraryMapError(c, e)
			}
			s.epoch = epoch
			s.building = true
			return nil
		})
		if e != nil {
			s.discard()
			return e
		}
		p = s.progress
		return nil
	})
	if err != nil {
		return lib.Preparation{}, err
	}
	return p, nil
}
func (r *libraryRead) Advance(ctx context.Context) (p lib.Preparation, err error) {
	err = r.use(ctx, func(c context.Context) error {
		s := r.owner.library
		if s.index == nil {
			return lib.ErrIndexNotReady
		}
		if !s.building {
			return lib.ErrIndexNotReady
		}
		var done bool
		e := r.source(c, true, func(c context.Context, tx *sql.Tx, v int, epoch libraryEpoch) error {
			query := "SELECT CASE WHEN typeof(content_id)='text' AND length(CAST(content_id AS BLOB))=30 THEN content_id END FROM tc_catalog_content"
			var args []any
			if s.after != "" {
				query += " WHERE content_id>?"
				args = append(args, s.after)
			}
			query += " ORDER BY content_id COLLATE BINARY LIMIT 129"
			rows, e := tx.QueryContext(c, query, args...)
			if e != nil {
				return libraryMapError(c, e)
			}
			var ids []identity.ContentID
			for rows.Next() {
				var raw sql.NullString
				if e = rows.Scan(&raw); e != nil {
					break
				}
				var id identity.ContentID
				id, e = identity.ParseContentID(raw.String)
				if e != nil || !raw.Valid {
					e = lib.ErrUnavailable
					break
				}
				ids = append(ids, id)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return libraryMapError(c, e)
			}
			done = len(ids) <= 128
			if len(ids) > 128 {
				ids = ids[:128]
			}
			repo := &catalogRepository{transaction: tx, ctx: c, owner: r.owner, database: r.owner.database, generation: r.owner.generation.Load(), schemaVersion: v, statements: make(map[string]*sql.Stmt)}
			defer repo.closeStatements()
			defer repo.revoke()
			write, e := s.index.BeginTx(c, nil)
			if e != nil {
				return libraryMapError(c, e)
			}
			defer write.Rollback()
			advancedItems, managedItems := 0, 0
			for index, id := range ids {
				if s.progress.Contents >= lib.MaxContents {
					return lib.ErrLimitExceeded
				}
				entries, revision, e := repo.libraryAggregate(c, id)
				if e != nil {
					return libraryMapError(c, e)
				}
				for _, entry := range entries {
					item := libraryProject(entry, revision.Uint64())
					if e := lib.ValidateItem(item); e != nil {
						return e
					}
					raw, e := lib.CanonicalJSON(item)
					if e != nil {
						return e
					}
					keys, e := libraryItemKeys(item)
					if e != nil {
						return e
					}
					added := libraryStagingSize(item, raw, keys)
					if e = libraryCapacity(s.progress.Items, s.progress.Bytes, added); e != nil {
						return e
					}
					if e = libraryInsertItem(c, write, item, keys); e != nil {
						return libraryMapError(c, e)
					}
					s.progress.Items++
					s.progress.Bytes += added
				}
				s.progress.Contents++
				s.after = id.String()
				advancedItems += len(entries)
				if len(entries) > 0 && entries[0].Review != catalog.CatalogUnmanaged {
					managedItems += len(entries)
				}
				// B key/payload work and complete managed provenance validation
				// must fit the unchanged transaction deadline, including race
				// instrumentation. Stop only between complete Contents; cheap
				// inventories retain the existing 128-Content advance behavior.
				if (advancedItems >= 1024 || managedItems >= 16) && index+1 < len(ids) {
					done = false
					break
				}
				repo.versions = nil
				repo.graphs = [2]*catalogGraphSnapshot{}
				repo.foreignKeys = nil
			}
			var pageCount, pageSize uint64
			if e = write.QueryRowContext(c, "SELECT page_count,page_size FROM pragma_page_count,pragma_page_size").Scan(&pageCount, &pageSize); e != nil {
				return libraryMapError(c, e)
			}
			if pageSize == 0 || pageCount > lib.MaxIndexBytes/pageSize {
				return lib.ErrLimitExceeded
			}
			s.progress.Pages = pageCount
			return libraryMapError(c, write.Commit())
		})
		if e != nil {
			s.discard()
			return e
		}
		if done {
			s.publish()
		}
		p = s.progress
		return nil
	})
	if err != nil {
		return lib.Preparation{}, err
	}
	return p, nil
}
func libraryCapacity(items, staged, added uint64) error {
	if items >= lib.MaxVersions || staged > lib.MaxStagingBytes || added > lib.MaxStagingBytes-staged {
		return lib.ErrLimitExceeded
	}
	return nil
}
func libraryProject(record catalog.CatalogRecord, revision uint64) lib.Item {
	optional := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	field := func(f domain.CatalogField) lib.Field {
		out := lib.Field{Status: f.Status}
		if f.Status == domain.CatalogFactAccepted && len(f.Alternatives) == 1 {
			v := f.Alternatives[0].Claim.Text()
			out.Value = &v
		}
		return out
	}
	model, mp := record.Baseline.ProductIdentifiers().Model()
	article, ap := record.Baseline.ProductIdentifiers().Article()
	baseline := lib.Baseline{Title: record.Baseline.Title()}
	if mp {
		baseline.Model = optional(model.String())
	}
	if ap {
		baseline.Article = optional(article.String())
	}
	s := record.Supersession
	command := func(id domain.CatalogCommandID) *string {
		if id.IsZero() {
			return nil
		}
		v := id.String()
		return &v
	}
	return lib.Item{ContentID: record.Content.String(), VersionID: record.Version.ID().String(), AudioID: record.Version.Fingerprint().AudioID().Uint64(), Hash: record.Version.Fingerprint().Hash().String(), ContentRevision: revision, Review: record.Review, ProtectedCustom: record.ProtectedCustom, Baseline: baseline, Title: field(record.Title), Model: field(record.Model), Article: field(record.Article), Supersession: lib.Supersession{Status: s.Status, Revision: s.SupersessionRevision.Uint64(), Predecessor: optional(s.Predecessor.String()), Successor: optional(s.Successor.String()), Terminal: optional(s.Terminal.String()), IncomingCommand: command(s.IncomingCommand), OutgoingCommand: command(s.OutgoingCommand)}, Media: record.Media}
}
func librarySnapshot(s *libraryState) string {
	return base64.RawURLEncoding.EncodeToString(s.snapshot[:])
}

// Layout changes invalidate the generation independently of the public cursor
// codec. Keys include the complete D3 tuple, so continuation is one B-tree seek.
const libraryIndexLayout = "pi10-library-index/2:ascii-prefix/1"
const libraryIndexSchema = `PRAGMA page_size=4096; PRAGMA max_page_count=65536;
CREATE TABLE items(content TEXT COLLATE BINARY NOT NULL,version TEXT COLLATE BINARY NOT NULL,payload BLOB NOT NULL,PRIMARY KEY(content,version)) WITHOUT ROWID;
CREATE TABLE memberships(lane TEXT NOT NULL,owner TEXT NOT NULL,identity_key BLOB NOT NULL,asc_key BLOB NOT NULL,desc_key BLOB NOT NULL,payload BLOB NOT NULL);
CREATE UNIQUE INDEX library_identity ON memberships(lane,owner,identity_key,payload);
CREATE UNIQUE INDEX library_title_asc ON memberships(lane,owner,asc_key,payload);
CREATE UNIQUE INDEX library_title_desc ON memberships(lane,owner,desc_key,payload);`

type libraryKeys struct{ identity, asc, desc []byte }

func libraryItemKeys(item lib.Item) (libraryKeys, error) {
	title, err := lib.TitleKey(item)
	if err != nil {
		return libraryKeys{}, lib.ErrUnavailable
	}
	k := libraryKeys{identity: []byte(item.ContentID + item.VersionID)}
	missing := byte(0)
	if title == "" {
		missing = 1
	}
	k.asc = append([]byte{missing}, []byte(title)...)
	k.asc = append(k.asc, 0)
	k.asc = append(k.asc, k.identity...)
	k.desc = []byte{missing}
	for i := 0; i < len(title); i++ {
		k.desc = append(k.desc, ^title[i])
	}
	k.desc = append(k.desc, 255)
	k.desc = append(k.desc, k.identity...)
	return k, nil
}
func libraryMemberships(item lib.Item, visit func(string, string)) {
	visit(lib.All, "")
	visit(lib.All, item.ContentID)
	if item.Supersession.Status == catalog.Current {
		visit(lib.Current, "")
		visit(lib.Current, item.ContentID)
	}
}

// Account the canonical summary and every partition membership key before
// insertion. Private encoded copies and SQLite overhead are page-capped.
func libraryStagingSize(item lib.Item, raw []byte, keys libraryKeys) uint64 {
	n := len(raw) + len(keys.identity)
	libraryMemberships(item, func(lane, owner string) {
		n += len(lane) + len(owner) + len(keys.identity) + len(keys.asc) + len(keys.desc)
	})
	return uint64(n)
}
func libraryInsertItem(ctx context.Context, tx *sql.Tx, item lib.Item, keys libraryKeys) error {
	raw, err := libraryEncodeItem(item)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO items VALUES (?,?,?)", item.ContentID, item.VersionID, raw); err != nil {
		return err
	}
	err = nil
	libraryMemberships(item, func(lane, owner string) {
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO memberships VALUES (?,?,?,?,?,?)", lane, owner, keys.identity, keys.asc, keys.desc, raw)
		}
	})
	return err
}

// Compact, private payloads omit identities already in the covering order key.
// Large summaries are compressed; decoded size is bounded before JSON parsing.
// This is volatile index storage, never a source encoding or public DTO.
type libraryPayload struct {
	Audio        uint64                      `json:"a"`
	Hash         string                      `json:"h"`
	Revision     uint64                      `json:"r"`
	Review       catalog.CatalogReviewStatus `json:"v,omitzero"`
	Custom       bool                        `json:"c,omitempty"`
	Baseline     lib.Baseline                `json:"b,omitzero"`
	Title        lib.Field                   `json:"t,omitzero"`
	Model        lib.Field                   `json:"m,omitzero"`
	Article      lib.Field                   `json:"n,omitzero"`
	Supersession lib.Supersession            `json:"s,omitzero"`
	Media        catalog.MediaState          `json:"f"`
}

var libraryPayloadWriters = sync.Pool{New: func() any { w, _ := zlib.NewWriterLevel(io.Discard, zlib.BestSpeed); return w }}

func libraryEncodeItem(i lib.Item) ([]byte, error) {
	raw, e := lib.CanonicalJSON(libraryPayload{i.AudioID, i.Hash, i.ContentRevision, i.Review, i.ProtectedCustom, i.Baseline, i.Title, i.Model, i.Article, i.Supersession, i.Media})
	if e != nil {
		return nil, e
	}
	if len(raw) > 32768 {
		return nil, lib.ErrLimitExceeded
	}
	if len(raw) < 256 {
		return append([]byte{0}, raw...), nil
	}
	var b bytes.Buffer
	b.WriteByte(1)
	w := libraryPayloadWriters.Get().(*zlib.Writer)
	w.Reset(&b)
	_, e = w.Write(raw)
	if e == nil {
		e = w.Close()
	}
	libraryPayloadWriters.Put(w)
	if e != nil {
		return nil, lib.ErrUnavailable
	}
	return b.Bytes(), nil
}
func libraryDecodeItem(raw []byte, content, version string) (lib.Item, error) {
	if len(raw) < 2 || len(raw) > 32768 {
		return lib.Item{}, lib.ErrUnavailable
	}
	data := raw[1:]
	if raw[0] == 1 {
		reader, e := zlib.NewReader(bytes.NewReader(data))
		if e != nil {
			return lib.Item{}, lib.ErrUnavailable
		}
		data, e = io.ReadAll(io.LimitReader(reader, 32769))
		closeErr := reader.Close()
		if e != nil || closeErr != nil || len(data) > 32768 {
			return lib.Item{}, lib.ErrUnavailable
		}
	} else if raw[0] != 0 {
		return lib.Item{}, lib.ErrUnavailable
	}
	var p libraryPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return lib.Item{}, lib.ErrUnavailable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return lib.Item{}, lib.ErrUnavailable
	}
	i := lib.Item{ContentID: content, VersionID: version, AudioID: p.Audio, Hash: p.Hash, ContentRevision: p.Revision, Review: p.Review, ProtectedCustom: p.Custom, Baseline: p.Baseline, Title: p.Title, Model: p.Model, Article: p.Article, Supersession: p.Supersession, Media: p.Media}
	if e := lib.ValidateItem(i); e != nil {
		return lib.Item{}, e
	}
	return i, nil
}
