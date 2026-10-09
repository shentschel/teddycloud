package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

// All values are synthetic. Keep each promised data class independently
// identifiable, including values which would normally be valid identifiers.
var blobErrorCanaries = []string{
	"/private/f05a/media.taf",
	"SELECT token FROM private_provider",
	"Bearer f05a-secret-token",
	"E0:04:03:50:00:00:FA:05",
	"05fa0000500304e0",
	"imp_00000000000000000000000005",
	"cnt_00000000000000000000000005",
	"provider-response-f05a",
}

var blobErrorCategories = []error{
	context.Canceled, context.DeadlineExceeded,
	contentstore.ErrInvalidInput, contentstore.ErrBusy,
	contentstore.ErrSchemaUnavailable, contentstore.ErrCorrupt,
	contentstore.ErrRevoked, contentstore.ErrConflict,
	contentstore.ErrContentNotFound, contentstore.ErrUnavailable,
	contentstore.ErrMissing, contentstore.ErrUnsupported,
	contentstore.ErrMismatch, contentstore.ErrCanceled,
	contentstore.ErrCapacity, contentstore.ErrRetained,
	contentstore.ErrUncertain, contentstore.ErrCursor,
	contentstore.ErrCommitUncertain,
}

func blobPrivateTree(category error) error {
	leaf := errors.New(strings.Join(blobErrorCanaries, " | "))
	return fmt.Errorf("outer: %w", errors.Join(
		fmt.Errorf("inner: %w", leaf), category))
}

// Inspect nodes, not only Error() at the root: an error may hide a foreign
// cause behind a harmless message. Both Go unwrap shapes are traversed.
func blobTreeProblem(err error, allowed []error, foreign error) string {
	if err == nil {
		return ""
	}
	for _, canary := range blobErrorCanaries {
		if strings.Contains(err.Error(), canary) {
			return "canary in error node"
		}
	}
	if foreign != nil && errors.Is(err, foreign) {
		return "foreign cause retained"
	}
	for _, category := range allowed {
		if err == category {
			return ""
		}
	}
	switch node := err.(type) {
	case interface{ Unwrap() []error }:
		children := node.Unwrap()
		if len(children) == 0 {
			return "unapproved empty wrapper"
		}
		parts := make([]string, 0, len(children))
		for _, child := range children {
			if problem := blobTreeProblem(child, allowed, foreign); problem != "" {
				return problem
			}
			if child != nil {
				parts = append(parts, child.Error())
			}
		}
		if err.Error() != strings.Join(parts, "\n") {
			return "unapproved wrapper message"
		}
		return ""
	case interface{ Unwrap() error }:
		if problem := blobTreeProblem(node.Unwrap(), allowed, foreign); problem != "" {
			return problem
		}
		return "unexpected single wrapper at sanitized boundary"
	default:
		return "unapproved error leaf"
	}
}

func assertBlobErrorTree(t *testing.T, got, foreign error, want ...error) {
	t.Helper()
	if len(want) == 0 {
		if got != nil {
			t.Fatal("expected nil boundary error")
		}
		return
	}
	for _, category := range blobErrorCategories {
		expected := false
		for _, wanted := range want {
			expected = expected || wanted == category
		}
		if errors.Is(got, category) != expected {
			t.Fatalf("category %v: match=%v want=%v", category, errors.Is(got, category), expected)
		}
	}
	if problem := blobTreeProblem(got, want, foreign); problem != "" {
		t.Fatal(problem)
	}
}

type hiddenBlobCause struct{ cause error }

func (e hiddenBlobCause) Error() string { return "harmless outer message" }
func (e hiddenBlobCause) Unwrap() error { return e.cause }

func TestBlobSanitizedTreeAssertion(t *testing.T) {
	for _, canary := range blobErrorCanaries {
		foreign := errors.New(canary)
		for _, tree := range []error{
			hiddenBlobCause{foreign},
			errors.Join(contentstore.ErrBusy, hiddenBlobCause{foreign}),
			hiddenBlobCause{errors.Join(contentstore.ErrBusy, foreign)},
		} {
			if blobTreeProblem(tree, blobErrorCategories, nil) == "" {
				t.Fatal("recursive assertion accepted a hidden canary")
			}
		}
	}
	if blobTreeProblem(errors.New("unlisted harmless cause"), blobErrorCategories, nil) == "" {
		t.Fatal("recursive assertion accepted foreign leaf")
	}
	assertBlobErrorTree(t, errors.Join(contentstore.ErrBusy, contentstore.ErrCommitUncertain), nil,
		contentstore.ErrBusy, contentstore.ErrCommitUncertain)
}

func TestBlobSanitizedErrorMatrix(t *testing.T) {
	for _, category := range append([]error{nil}, blobErrorCategories...) {
		name := "unknown"
		if category != nil {
			name = category.Error()
		}
		for _, uncertain := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/uncertain=%v", name, uncertain), func(t *testing.T) {
				foreign := blobPrivateTree(category)
				input := foreign
				want := category
				if want == nil || want == contentstore.ErrCommitUncertain {
					want = contentstore.ErrUnavailable
				}
				wanted := []error{want}
				if uncertain || category == contentstore.ErrCommitUncertain {
					input = errors.Join(input, contentstore.ErrCommitUncertain)
					wanted = append(wanted, contentstore.ErrCommitUncertain)
				}
				assertBlobErrorTree(t, contentBoundaryError(t.Context(), input), foreign, wanted...)
				owner := blobOwner(t)
				output := owner.WithinContentOperation(t.Context(), func(ctx context.Context, session contentstore.Session) error {
					return session.WithinTransaction(ctx, func(contentstore.BlobRepository) error { return input })
				})
				assertBlobErrorTree(t, output, foreign, wanted...)
				for _, expired := range []error{context.Canceled, context.DeadlineExceeded} {
					ctx, cancel := context.WithCancel(t.Context())
					if expired == context.DeadlineExceeded {
						cancel()
						ctx, cancel = context.WithDeadline(t.Context(), time.Unix(1, 0))
					} else {
						cancel()
					}
					defer cancel()
					expected := []error{expired}
					if uncertain || category == contentstore.ErrCommitUncertain {
						expected = append(expected, contentstore.ErrCommitUncertain)
					}
					assertBlobErrorTree(t, contentBoundaryError(ctx, input), foreign, expected...)
					assertBlobErrorTree(t, contentBoundaryError(ctx, nil), nil)
				}
			})
		}
	}
	assertBlobErrorTree(t, contentBoundaryError(t.Context(), nil), nil)
	joined := errors.Join(blobPrivateTree(context.DeadlineExceeded), blobPrivateTree(context.Canceled))
	assertBlobErrorTree(t, contentBoundaryError(t.Context(), joined), joined, context.Canceled)
}

// Generate actual modernc errors rather than imitating its private Error type.
func TestBlobSanitizedSQLiteBusyLocked(t *testing.T) {
	first, second := openContentionPair(t, time.Millisecond)
	if _, err := first.db.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer first.db.ExecContext(context.Background(), "ROLLBACK")
	_, busy := second.db.ExecContext(t.Context(), "BEGIN IMMEDIATE")
	check := func(err error, code int) {
		t.Helper()
		if got, ok := sqlitePrimaryCode(err); !ok || got != code {
			t.Fatalf("fixture SQLite code = %d, found=%v, expected=%d", got, ok, code)
		}
		for _, uncertain := range []bool{false, true} {
			input := errors.Join(blobPrivateTree(nil), fmt.Errorf("driver: %w", err))
			want := []error{contentstore.ErrBusy}
			if uncertain {
				input = errors.Join(input, contentstore.ErrCommitUncertain)
				want = append(want, contentstore.ErrCommitUncertain)
			}
			assertBlobErrorTree(t, contentBoundaryError(t.Context(), input), err, want...)
			for _, deadline := range []bool{false, true} {
				ctx, cancel := context.WithCancel(t.Context())
				category := context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithDeadline(t.Context(), time.Unix(1, 0))
					category = context.DeadlineExceeded
				} else {
					cancel()
				}
				expected := []error{category}
				if uncertain {
					expected = append(expected, contentstore.ErrCommitUncertain)
				}
				assertBlobErrorTree(t, contentBoundaryError(ctx, input), err, expected...)
				cancel()
			}
		}
	}
	check(busy, 5)
	conn, err := first.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(t.Context(), "SELECT name FROM sqlite_schema")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("fixture must keep a live read cursor")
	}
	_, locked := conn.ExecContext(t.Context(), "DROP TABLE tc_blob_imports")
	check(locked, 6)
}

// The driver is test-only and per-test (no global sql.Register state). The
// actual database/sql, owner, session, repository and service code still run.
type blobFaultDriver struct{}

func (blobFaultDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type blobFaultConnector struct{ conn *blobFaultConn }

func (c blobFaultConnector) Driver() driver.Driver { return blobFaultDriver{} }
func (c blobFaultConnector) Connect(context.Context) (driver.Conn, error) {
	if err := c.conn.fail("connection"); err != nil {
		return nil, err
	}
	return c.conn, nil
}

type blobFaultConn struct {
	phase   string
	failure error
	hits    map[string]int
	digest  []byte
}

func (c *blobFaultConn) fail(phase string) error {
	c.hits[phase]++
	if c.phase == phase {
		return c.failure
	}
	return nil
}
func (c *blobFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *blobFaultConn) Close() error { return nil }
func (c *blobFaultConn) Begin() (driver.Tx, error) {
	if err := c.fail("begin"); err != nil {
		return nil, err
	}
	return blobFaultTx{c}, nil
}
func (c *blobFaultConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *blobFaultConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	phase := "write"
	if strings.HasPrefix(query, "PRAGMA busy_timeout(") {
		phase = "restore"
	}
	if err := c.fail(phase); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}
func (c *blobFaultConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "tc_schema_migrations"):
		return &blobFaultRows{values: []driver.Value{int64(4)}}, nil
	case query == "PRAGMA busy_timeout":
		if err := c.fail("timeout-read"); err != nil {
			return nil, err
		}
		return &blobFaultRows{values: []driver.Value{int64(5000)}}, nil
	case strings.Contains(query, "FROM tc_version_blobs vb"):
		if err := c.fail("reference-query"); err != nil {
			return nil, err
		}
		values := []driver.Value{c.digest, true, true}
		if c.phase == "reference-scan" {
			c.hits[c.phase]++
			values[1] = strings.Join(blobErrorCanaries, " | ")
		}
		return &blobFaultRows{values: values, conn: c}, nil
	default:
		if err := c.fail("repository-query"); err != nil {
			return nil, err
		}
		return nil, errors.New("unexpected query")
	}
}

type blobFaultTx struct{ conn *blobFaultConn }

func (tx blobFaultTx) Commit() error   { return tx.conn.fail("commit") }
func (tx blobFaultTx) Rollback() error { return nil }

type blobFaultRows struct {
	values []driver.Value
	conn   *blobFaultConn
	read   bool
}

func (r *blobFaultRows) Columns() []string {
	if len(r.values) == 3 {
		return []string{"digest", "version", "blob"}
	}
	return []string{"value"}
}
func (r *blobFaultRows) Close() error {
	if r.conn != nil {
		return r.conn.fail("reference-close")
	}
	return nil
}
func (r *blobFaultRows) Next(dest []driver.Value) error {
	if r.conn != nil {
		if err := r.conn.fail("reference-iteration"); err != nil {
			return err
		}
	}
	if r.read {
		return io.EOF
	}
	r.read = true
	copy(dest, r.values)
	return nil
}

func blobFaultSession(t *testing.T, phase string) (*contentSession, *blobFaultConn) {
	t.Helper()
	fault := &blobFaultConn{phase: phase, failure: blobPrivateTree(nil), hits: make(map[string]int)}
	db := sql.OpenDB(blobFaultConnector{fault})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	owner := &LifecycleOwner{database: &Database{db: db}, gate: newLifecycleGate()}
	session := &contentSession{owner: owner, database: owner.database, ctx: t.Context(), active: true}
	return session, fault
}

func TestBlobSanitizedSessionFailures(t *testing.T) {
	for _, phase := range []string{"connection", "timeout-read", "begin", "write", "commit", "restore"} {
		t.Run(phase, func(t *testing.T) {
			session, fault := blobFaultSession(t, phase)
			err := session.WithinTransaction(t.Context(), func(repository contentstore.BlobRepository) error {
				if phase == "write" {
					err := repository.(*blobRepository).exec(t.Context(), "INSERT synthetic failure")
					assertBlobErrorTree(t, err, fault.failure, contentstore.ErrUnavailable)
					return err
				}
				return nil
			})
			want := []error{contentstore.ErrUnavailable}
			if phase == "commit" || phase == "restore" {
				want = append(want, contentstore.ErrCommitUncertain)
			}
			assertBlobErrorTree(t, err, fault.failure, want...)
			if fault.hits[phase] == 0 {
				t.Fatal("failure seam not reached")
			}
		})
	}
	t.Run("primary-and-restore", func(t *testing.T) {
		session, fault := blobFaultSession(t, "restore")
		primary := blobPrivateTree(contentstore.ErrConflict)
		err := session.WithinTransaction(t.Context(), func(contentstore.BlobRepository) error { return primary })
		assertBlobErrorTree(t, err, fault.failure, contentstore.ErrConflict)
		if fault.hits["restore"] == 0 || fault.hits["commit"] != 0 {
			t.Fatal("expected rollback with failed timeout restoration")
		}
	})
}

func TestBlobSanitizedReferenceFailures(t *testing.T) {
	for _, phase := range []string{"reference-query", "reference-scan", "reference-iteration", "reference-close"} {
		t.Run(phase, func(t *testing.T) {
			session, fault := blobFaultSession(t, phase)
			id := content.NewBlobID([32]byte{1})
			digest := id.Digest()
			fault.digest = digest[:]
			found, err := session.References(t.Context(), id)
			assertBlobErrorTree(t, err, fault.failure, contentstore.ErrUnavailable)
			if found || fault.hits[phase] == 0 {
				t.Fatal("failed reference lookup returned data or missed injection")
			}
		})
	}
}

type blobFaultMedia struct {
	contentstore.MediaStore
	failure    error
	invalidate error
	calls      int
}

func (m *blobFaultMedia) InvalidateInventory() error { return m.invalidate }
func (m *blobFaultMedia) ImportDuration() time.Duration {
	return time.Minute
}
func (m *blobFaultMedia) Publish(context.Context, content.BlobID, uint64, content.TAFSource, content.TAFSourceMode) (content.TAFEnvelope, error) {
	m.calls++
	return content.TAFEnvelope{}, m.failure
}
func (m *blobFaultMedia) Verify(context.Context, content.BlobID, uint64) error {
	m.calls++
	return m.failure
}
func (m *blobFaultMedia) Quarantine(context.Context, content.BlobID, uint64) error {
	m.calls++
	return m.failure
}
func (m *blobFaultMedia) Inventory(context.Context, string, int, contentstore.ReferenceLookup) (contentstore.InventoryPage, error) {
	m.calls++
	return contentstore.InventoryPage{Cursor: "must-not-escape"}, m.failure
}

func TestBlobSanitizedServiceAndOwner(t *testing.T) {
	for _, method := range []string{"attach", "invalidate", "Import", "LookupImport", "Availability", "Inventory", "Quarantine", "owner", "session"} {
		t.Run(method, func(t *testing.T) {
			owner := blobOwner(t)
			foreign := blobPrivateTree(contentstore.ErrCorrupt)
			media := &blobFaultMedia{failure: foreign}
			if method == "attach" {
				media.invalidate = foreign
			}
			service, err := contentstore.NewService(t.Context(), owner, media)
			if method == "attach" {
				assertBlobErrorTree(t, err, foreign, contentstore.ErrCorrupt)
				if service != nil || owner.contentInventory != nil {
					t.Fatal("failed attachment retained state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			command, key := blobCommand(t, 5, false), blobKey(t, 5)
			switch method {
			case "invalidate":
				media.invalidate = foreign
				_, _, err = service.LookupImport(t.Context(), key, command)
			case "Import":
				fixture, e := taffixture.New(8193, 123, []uint32{0, 1})
				if e != nil {
					t.Fatal(e)
				}
				source := fixture.Open()
				defer source.Close()
				var result contentstore.ImportResult
				result, err = service.Import(t.Context(), key, command, source, content.FiniteTAFSource)
				if result != (contentstore.ImportResult{}) {
					t.Fatal("failed import returned a result")
				}
			case "LookupImport":
				session, fault := blobFaultSession(t, "repository-query")
				fault.failure = foreign
				// Keep the real owner/gate and substitute only its SQL transport.
				original := owner.database
				owner.database = session.database
				defer func() { owner.database = original }()
				var result contentstore.ImportResult
				var found bool
				result, found, err = service.LookupImport(t.Context(), key, command)
				if result != (contentstore.ImportResult{}) || found || fault.hits["repository-query"] == 0 {
					t.Fatal("failed lookup returned data or missed query")
				}
			case "Availability":
				err = service.Availability(t.Context(), command.BlobID(), command.CompleteBytes())
			case "Inventory":
				var page contentstore.InventoryPage
				page, err = service.Inventory(t.Context(), "", 1)
				if page.Cursor != "" || len(page.Entries) != 0 {
					t.Fatal("failed inventory returned data")
				}
			case "Quarantine":
				err = service.Quarantine(t.Context(), command.BlobID(), command.CompleteBytes())
			case "owner", "session":
				err = owner.WithinContentOperation(t.Context(), func(ctx context.Context, session contentstore.Session) error {
					if method == "owner" {
						return foreign
					}
					return session.WithinTransaction(ctx, func(contentstore.BlobRepository) error { return foreign })
				})
			}
			assertBlobErrorTree(t, err, foreign, contentstore.ErrCorrupt)
			if method == "Import" || method == "Availability" || method == "Inventory" || method == "Quarantine" {
				if media.calls != 1 {
					t.Fatal("media failure seam not reached once")
				}
			}
		})
	}
}

func TestBlobSanitizedRealRepositoryWrite(t *testing.T) {
	owner := blobOwner(t)
	message := strings.Join(blobErrorCanaries, " | ")
	query := "CREATE TRIGGER f05a_reject BEFORE INSERT ON tc_blob_imports BEGIN SELECT RAISE(ABORT,'" + message + "'); END"
	if _, err := owner.database.db.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, session contentstore.Session) error {
		return session.WithinTransaction(ctx, func(repository contentstore.BlobRepository) error {
			_, err := repository.RecordImport(ctx, blobKey(t, 5), blobCommand(t, 5, false))
			assertBlobErrorTree(t, err, nil, contentstore.ErrUnavailable)
			return err
		})
	})
	assertBlobErrorTree(t, err, nil, contentstore.ErrUnavailable)
	assertBlobCounts(t, owner, 0, 0, 0, 0)
}

func TestBlobSanitizedCommitWithOtherOutcomes(t *testing.T) {
	for _, category := range []error{contentstore.ErrConflict, contentstore.ErrCorrupt, context.Canceled, context.DeadlineExceeded} {
		t.Run(category.Error(), func(t *testing.T) {
			session, fault := blobFaultSession(t, "commit")
			fault.failure = blobPrivateTree(category)
			err := session.WithinTransaction(t.Context(), func(contentstore.BlobRepository) error { return nil })
			assertBlobErrorTree(t, err, fault.failure, category, contentstore.ErrCommitUncertain)
			if fault.hits["commit"] != 1 || fault.hits["restore"] != 1 {
				t.Fatal("commit failure or cleanup seam was not exercised")
			}
		})
	}
}
