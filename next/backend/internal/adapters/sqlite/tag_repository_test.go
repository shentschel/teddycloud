package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	sqliteDriver "modernc.org/sqlite"
)

func TestTagRepositoryFreshReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tags.sqlite")
	for _, uid := range []domaintag.UID{domaintag.UIDFromBytes([8]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}), {}} {
		owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		s := applicationtag.New(owner)
		value := testRegistryTag(t, byte('0'+uid.Bytes()[0]%10), uid)
		command := applicationtag.RegisterCommand{OpaqueID: value.ID().String(), Physical: applicationtag.PhysicalIdentityInput{
			RUID: applicationtag.OptionalText{Present: true, Value: strings.ToLower(uid.RUID().String())},
		}}
		got, err := s.Register(t.Context(), command)
		if err != nil || !got.Equal(value) {
			t.Fatalf("register = %v, %v", got, err)
		}
		if err := owner.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		owner, err = OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		s = applicationtag.New(owner)
		for _, read := range []func() (domaintag.Tag, error){
			func() (domaintag.Tag, error) { return s.FindByID(t.Context(), value.ID().String()) },
			func() (domaintag.Tag, error) { return s.FindByUID(t.Context(), uid.String()) },
			func() (domaintag.Tag, error) { return s.FindByRUID(t.Context(), uid.RUID().String()) },
			func() (domaintag.Tag, error) { return s.Register(t.Context(), command) },
		} {
			got, err := read()
			if err != nil || !got.Equal(value) {
				t.Fatalf("reopen/replay = %v, %v", got, err)
			}
		}
		if err := owner.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTagIdentityUniqueness(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	first := testRegistryTag(t, '0', domaintag.UID{})
	if err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), first) }); err != nil {
		t.Fatal(err)
	}
	for _, collision := range []domaintag.Tag{
		first,
		testRegistryTag(t, '1', first.UID()),
		testRegistryTag(t, '0', domaintag.UIDFromBytes([8]byte{1})),
	} {
		err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), collision) })
		assertTagError(t, err, applicationtag.ErrIdentityConflict)
	}
	got, err := applicationtag.New(owner).FindByID(t.Context(), first.ID().String())
	if err != nil || !got.Equal(first) {
		t.Fatalf("collision altered identity: %v, %v", got, err)
	}
	err = owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), domaintag.Tag{}) })
	assertTagError(t, err, applicationtag.ErrInvalidInput)
	err = owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error {
		_, _, err := r.FindByID(t.Context(), identity.TagID{})
		return err
	})
	assertTagError(t, err, applicationtag.ErrInvalidInput)
}

func TestTagConcurrentCommands(t *testing.T) {
	t.Run("owner serializes registration", func(t *testing.T) {
		owner := openLifecycleApplicationOwner(t)
		s := applicationtag.New(owner)
		start, results := make(chan struct{}), make(chan error, 2)
		for _, digit := range []byte{'0', '1'} {
			value := testRegistryTag(t, digit, domaintag.UID{})
			go func() {
				<-start
				_, err := s.Register(t.Context(), applicationtag.RegisterCommand{OpaqueID: value.ID().String(), Physical: applicationtag.PhysicalIdentityInput{
					UID: applicationtag.OptionalText{Present: true, Value: value.UID().String()},
				}})
				results <- err
			}()
		}
		close(start)
		assertTagOneWinner(t, results)
	})
	t.Run("database constraint across connections", func(t *testing.T) {
		first, second := openContentionPair(t, time.Second)
		start, results := make(chan struct{}), make(chan error, 2)
		for i, db := range []*Database{first, second} {
			value := testRegistryTag(t, byte('0'+i), domaintag.UID{})
			go func() {
				<-start
				results <- db.withinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), value) })
			}()
		}
		close(start)
		assertTagOneWinner(t, results)
		var count int
		if err := first.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_tags`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("stored identities = %d, %v", count, err)
		}
	})
}

func TestTagRepositoryRejectsCorruptIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, column string
		value        any
		byUID        bool
	}{
		{"revision without history", "revision", 2, false},
		{"short blob", "uid", []byte{1}, false},
		{"text uid", "uid", "12345678", false},
		{"oversized id", "tag_id", strings.Repeat("x", 1024*1024), true},
		{"noncanonical id", "tag_id", "tag_" + strings.Repeat("u", 26), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			if tc.name == "text uid" {
				// Simulate imported/corrupt non-STRICT storage; SQLite will not
				// let a mere CHECK bypass write the wrong storage class.
				if _, err := owner.database.db.ExecContext(t.Context(), `DROP TABLE tc_tags`); err != nil {
					t.Fatal(err)
				}
				if _, err := owner.database.db.ExecContext(t.Context(), `CREATE TABLE tc_tags (tag_id TEXT PRIMARY KEY, uid BLOB, revision INTEGER)`); err != nil {
					t.Fatal(err)
				}
			}
			value := testRegistryTag(t, '0', domaintag.UID{})
			if err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), value) }); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.database.db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=ON`); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.database.db.ExecContext(t.Context(), `UPDATE tc_tags SET `+tc.column+` = ?`, tc.value); err != nil {
				t.Fatal(err)
			}
			err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error {
				var got domaintag.Tag
				var found bool
				var err error
				if tc.byUID {
					got, found, err = r.FindByUID(t.Context(), value.UID())
				} else {
					got, found, err = r.FindByID(t.Context(), value.ID())
				}
				if found || !got.ID().IsZero() {
					t.Fatal("corrupt read returned a Tag")
				}
				return err
			})
			assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
		})
	}
}

func testRegistryTag(t *testing.T, digit byte, uid domaintag.UID) domaintag.Tag {
	t.Helper()
	id, err := identity.ParseTagID("tag_" + strings.Repeat(string(digit), 26))
	if err != nil {
		t.Fatal(err)
	}
	value, err := domaintag.NewTag(id, uid)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertTagOneWinner(t *testing.T, results <-chan error) {
	t.Helper()
	winners, conflicts := 0, 0
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				winners++
			} else {
				assertTagError(t, err, applicationtag.ErrIdentityConflict)
				conflicts++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent command did not finish")
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners/conflicts = %d/%d", winners, conflicts)
	}
}

func assertTagError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	var driverError *sqliteDriver.Error
	if errors.As(err, &driverError) {
		t.Fatal("driver error leaked")
	}
	for _, forbidden := range []error{sql.ErrNoRows, sql.ErrTxDone, sql.ErrConnDone} {
		if errors.Is(err, forbidden) {
			t.Fatal("SQL sentinel leaked")
		}
	}
	for _, detail := range []string{"tc_tags", "sqlite", "sql logic", "no such table", "tag_", "12345678"} {
		if strings.Contains(strings.ToLower(err.Error()), detail) {
			t.Fatalf("detail leaked: %v", err)
		}
	}
}
