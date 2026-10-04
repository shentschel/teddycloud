package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"github.com/shentschel/teddycloud/next/backend/internal/testutil/taffixture"
)

func blobOwner(t *testing.T) *LifecycleOwner {
	t.Helper()
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "db.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		return r.Save(t.Context(), testContent(t, "Synthetic", false))
	}); err != nil {
		t.Fatal(err)
	}
	return owner
}

func blobCommand(t *testing.T, n int, known bool) content.ImportCommand {
	t.Helper()
	fixture, err := taffixture.New(8193, 123, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	id, err := identity.ParseContentVersionID(fmt.Sprintf("ver_%026d", n))
	if err != nil {
		t.Fatal(err)
	}
	audioID, err := catalog.NewAudioID(123)
	if err != nil {
		t.Fatal(err)
	}
	h := fixture.PayloadSHA1()
	hash, err := catalog.ParseAudioHash(hex.EncodeToString(h[:]))
	if err != nil {
		t.Fatal(err)
	}
	fp, err := catalog.NewAudioFingerprint(audioID, hash)
	if err != nil {
		t.Fatal(err)
	}
	order := catalog.UnknownVersionOrderEvidence()
	if known {
		order, err = catalog.NewVersionOrderEvidence("official-v1", math.MaxUint64)
		if err != nil {
			t.Fatal(err)
		}
	}
	version, err := catalog.NewContentVersion(id, testContent(t, "", false).ID(), fp, order)
	if err != nil {
		t.Fatal(err)
	}
	command, err := content.NewImportCommand(version, content.NewBlobID(fixture.BlobDigest()), fixture.CompleteBytes(), content.TAFProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func blobKey(t *testing.T, n int) content.ImportKey {
	t.Helper()
	key, err := content.ParseImportKey(fmt.Sprintf("imp_%026d", n))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func blobRecord(t *testing.T, owner *LifecycleOwner, key content.ImportKey, command content.ImportCommand) (contentstore.ImportResult, error) {
	t.Helper()
	var result contentstore.ImportResult
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		return s.WithinTransaction(ctx, func(r contentstore.BlobRepository) error {
			var err error
			result, err = r.RecordImport(ctx, key, command)
			return err
		})
	})
	return result, err
}

func blobLookup(t *testing.T, owner *LifecycleOwner, key content.ImportKey, c content.ImportCommand) (contentstore.ImportResult, bool, error) {
	t.Helper()
	var result contentstore.ImportResult
	var found bool
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		return s.WithinTransaction(ctx, func(r contentstore.BlobRepository) error {
			var err error
			result, found, err = r.LookupImport(ctx, key, c)
			return err
		})
	})
	return result, found, err
}

func assertBlobCounts(t *testing.T, owner *LifecycleOwner, want ...int) {
	t.Helper()
	for i, table := range []string{"tc_blobs", "tc_content_versions", "tc_version_blobs", "tc_blob_imports"} {
		var n int
		if err := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want[i] {
			t.Fatalf("%s count %d want %d", table, n, want[i])
		}
	}
}

func TestImportReplayAndConflict(t *testing.T) {
	owner := blobOwner(t)
	command := blobCommand(t, 1, true)
	key := blobKey(t, 1)
	first, err := blobRecord(t, owner, key, command)
	if err != nil {
		t.Fatal(err)
	}
	if first != importResult(command) {
		t.Fatal("wrong immutable result")
	}
	replay, err := blobRecord(t, owner, key, command)
	if err != nil || replay != first {
		t.Fatalf("replay: %v", err)
	}
	got, found, err := blobLookup(t, owner, key, command)
	if err != nil || !found || got != first {
		t.Fatalf("lookup: %v %v", found, err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
	for _, c := range []content.ImportCommand{blobCommand(t, 2, true), blobCommand(t, 1, false)} {
		if _, err := blobRecord(t, owner, key, c); !errors.Is(err, contentstore.ErrConflict) {
			t.Fatalf("same key conflict: %v", err)
		}
		if _, _, err := blobLookup(t, owner, key, c); !errors.Is(err, contentstore.ErrConflict) {
			t.Fatalf("lookup conflict: %v", err)
		}
	}
	if _, err := blobRecord(t, owner, blobKey(t, 2), blobCommand(t, 1, false)); !errors.Is(err, contentstore.ErrConflict) {
		t.Fatalf("version conflict: %v", err)
	}
	c, err := content.NewImportCommand(command.Version(), content.NewBlobID([32]byte{1}), command.CompleteBytes(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blobRecord(t, owner, blobKey(t, 2), c); !errors.Is(err, contentstore.ErrConflict) {
		t.Fatalf("binding conflict: %v", err)
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1) // attempted new blob rolled back too
	if _, err := blobRecord(t, owner, blobKey(t, 2), command); err != nil {
		t.Fatal(err)
	}
	if _, err := blobRecord(t, owner, blobKey(t, 3), blobCommand(t, 2, false)); err != nil {
		t.Fatal(err)
	}
	assertBlobCounts(t, owner, 1, 2, 2, 3) // no AudioID/digest catalog merge
	if _, found, err := blobLookup(t, owner, blobKey(t, 99), command); err != nil || found {
		t.Fatalf("absent receipt: %v %v", found, err)
	}
}

func TestImportAtomicFailure(t *testing.T) {
	owner := blobOwner(t)
	c := blobCommand(t, 1, false)
	injected := errors.New("private SQL path secret")
	err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		return s.WithinTransaction(ctx, func(r contentstore.BlobRepository) error {
			if _, err := r.RecordImport(ctx, blobKey(t, 1), c); err != nil {
				return err
			}
			return injected
		})
	})
	if !errors.Is(err, contentstore.ErrUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsanitized callback error: %v", err)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
	if _, err := owner.database.db.ExecContext(t.Context(), `CREATE TRIGGER reject_receipt BEFORE INSERT ON tc_blob_imports BEGIN SELECT RAISE(ABORT,'private SQL secret'); END`); err != nil {
		t.Fatal(err)
	}
	err = owner.WithinContentOperation(t.Context(), func(ctx context.Context, s contentstore.Session) error {
		return s.WithinTransaction(ctx, func(r contentstore.BlobRepository) error { _, _ = r.RecordImport(ctx, blobKey(t, 1), c); return nil })
	})
	if !errors.Is(err, contentstore.ErrUnavailable) {
		t.Fatalf("ignored failure committed: %v", err)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
}

func TestImportContentRequired(t *testing.T) {
	owner := blobOwner(t)
	c := blobCommand(t, 1, false)
	if _, err := owner.database.db.ExecContext(t.Context(), `DELETE FROM tc_catalog_content`); err != nil {
		t.Fatal(err)
	}
	if _, err := blobRecord(t, owner, blobKey(t, 1), c); !errors.Is(err, contentstore.ErrContentNotFound) {
		t.Fatal(err)
	}
	assertBlobCounts(t, owner, 0, 0, 0, 0)
}

func TestBlobCorruptRecords(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE tc_blob_imports SET command=zeroblob(1048576)`,
		`UPDATE tc_blob_imports SET command=substr(command,1,138)`,
		`UPDATE tc_blob_imports SET fingerprint=zeroblob(32)`,
		`UPDATE tc_blob_imports SET encoding=2`,
		`UPDATE tc_content_versions SET audio_sha1=zeroblob(21)`,
		`UPDATE tc_content_versions SET audio_id=0`,
		`UPDATE tc_content_versions SET order_known=2`,
		`UPDATE tc_content_versions SET order_namespace=char(0)`,
		`UPDATE tc_content_versions SET order_position=zeroblob(7)`,
		`UPDATE tc_content_versions SET content_id='invalid'`,
		`UPDATE tc_blobs SET size=4096`,
		`UPDATE tc_blobs SET algorithm=2`,
		`UPDATE tc_blobs SET profile=2`,
		`DELETE FROM tc_version_blobs`,
		`DELETE FROM tc_blobs`,
		`DELETE FROM tc_content_versions`,
		`DELETE FROM tc_catalog_content`,
	} {
		t.Run(mutation, func(t *testing.T) {
			owner := blobOwner(t)
			c := blobCommand(t, 1, true)
			key := blobKey(t, 1)
			if _, err := blobRecord(t, owner, key, c); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{`PRAGMA foreign_keys=OFF`, `PRAGMA ignore_check_constraints=ON`, mutation, `PRAGMA ignore_check_constraints=OFF`, `PRAGMA foreign_keys=ON`} {
				if _, err := owner.database.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			result, found, err := blobLookup(t, owner, key, c)
			if !errors.Is(err, contentstore.ErrCorrupt) || found || result != (contentstore.ImportResult{}) {
				t.Fatalf("corrupt lookup returned data: %v %v", found, err)
			}
			if _, err := blobRecord(t, owner, key, c); !errors.Is(err, contentstore.ErrCorrupt) {
				t.Fatalf("corrupt replay: %v", err)
			}
			// A new receipt may not recreate corrupt referenced rows.
			if strings.HasPrefix(mutation, "DELETE") || strings.HasPrefix(mutation, "UPDATE tc_content_versions") || strings.HasPrefix(mutation, "UPDATE tc_blobs") {
				if _, err := blobRecord(t, owner, blobKey(t, 99), c); !errors.Is(err, contentstore.ErrCorrupt) {
					t.Fatalf("corrupt state repaired by new key: %v", err)
				}
			}
		})
	}
}

func TestImportCommitUncertainty(t *testing.T) {
	for _, phase := range []string{"precommit", "ambiguous-absent", "ambiguous-present", "lost-response"} {
		t.Run(phase, func(t *testing.T) {
			owner := blobOwner(t)
			c := blobCommand(t, 1, false)
			key := blobKey(t, 1)
			calls := 0
			err := owner.WithinContentOperation(t.Context(), func(ctx context.Context, session contentstore.Session) error {
				s := session.(*contentSession)
				injected := errors.New("private commit path secret")
				switch phase {
				case "precommit":
					s.beforeCommit = func() error { return injected }
				case "ambiguous-absent":
					s.commit = func(*sql.Tx) error { return injected }
				case "ambiguous-present":
					s.commit = func(tx *sql.Tx) error {
						if err := tx.Commit(); err != nil {
							return err
						}
						return injected
					}
				}
				err := s.WithinTransaction(ctx, func(r contentstore.BlobRepository) error { calls++; _, err := r.RecordImport(ctx, key, c); return err })
				if phase == "lost-response" && err == nil {
					return injected
				}
				return err
			})
			if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("failure/result: %v calls %d", err, calls)
			}
			if strings.HasPrefix(phase, "ambiguous") && !errors.Is(err, contentstore.ErrCommitUncertain) {
				t.Fatalf("lost uncertainty category: %v", err)
			}
			result, found, err := blobLookup(t, owner, key, c)
			if err != nil {
				t.Fatal(err)
			}
			want := phase == "ambiguous-present" || phase == "lost-response"
			if found != want || (found && result != importResult(c)) {
				t.Fatalf("readback %v want %v", found, want)
			}
			if _, err := blobRecord(t, owner, key, c); err != nil {
				t.Fatal(err)
			}
			assertBlobCounts(t, owner, 1, 1, 1, 1)
		})
	}
}

func TestBlobSanitizedErrors(t *testing.T) {
	for _, input := range []error{
		errors.New("SQL /private/path imp_private secret"),
		fmt.Errorf("private ID/path/SQL: %w", contentstore.ErrConflict),
		errors.Join(contentstore.ErrCommitUncertain, fmt.Errorf("private ID/path/SQL: %w", contentstore.ErrCorrupt)),
	} {
		err := contentBoundaryError(t.Context(), input)
		var visit func(error)
		visit = func(e error) {
			if e == nil {
				return
			}
			for _, private := range []string{"SQL", "private", "secret", "/path"} {
				if strings.Contains(e.Error(), private) {
					t.Fatalf("nested error leaked: %v", e)
				}
			}
			switch chain := e.(type) {
			case interface{ Unwrap() []error }:
				for _, child := range chain.Unwrap() {
					visit(child)
				}
			case interface{ Unwrap() error }:
				visit(chain.Unwrap())
			}
		}
		visit(err)
		if errors.Is(input, contentstore.ErrConflict) && !errors.Is(err, contentstore.ErrConflict) {
			t.Fatal("lost conflict category")
		}
		if errors.Is(input, contentstore.ErrCommitUncertain) && !errors.Is(err, contentstore.ErrCommitUncertain) {
			t.Fatal("lost uncertain category")
		}
	}
}
