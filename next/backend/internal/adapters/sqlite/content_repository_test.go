package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	sqliteDriver "modernc.org/sqlite"
)

const contentIDText = "cnt_0123456789abcdefghjkmnpqrs"

func TestContentRepositoryCommitRoundTrip(t *testing.T) {
	database := openApplicationDatabase(t)
	want := testContent(t, "Bedtime Bear", true)

	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), want)
	}); err != nil {
		t.Fatalf("save content: %v", err)
	}

	var got domaincatalog.Content
	var found bool
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		var err error
		got, found, err = repository.FindByID(t.Context(), want.ID())
		return err
	}); err != nil {
		t.Fatalf("find content: %v", err)
	}
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = (%#v, %t), want (%#v, true)", got, found, want)
	}
}

func TestContentRepositorySanitizesStorageFailures(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(applicationcatalog.ContentRepository) error
	}{
		{
			name: "save",
			run: func(repository applicationcatalog.ContentRepository) error {
				return repository.Save(t.Context(), testContent(t, "Failure", true))
			},
		},
		{
			name: "find",
			run: func(repository applicationcatalog.ContentRepository) error {
				_, _, err := repository.FindByID(t.Context(), testContent(t, "Failure", false).ID())
				return err
			},
		},
	} {
		t.Run(operation.name, func(t *testing.T) {
			database := openApplicationDatabase(t)
			if _, err := database.db.ExecContext(t.Context(), `DROP TABLE tc_catalog_content`); err != nil {
				t.Fatal(err)
			}
			err := database.WithinTransaction(
				t.Context(),
				func(repository applicationcatalog.ContentRepository) error {
					return operation.run(repository)
				},
			)
			assertApplicationRepositoryFailure(t, err)
		})
	}
}

func TestContentRepositorySanitizesClosedHandle(t *testing.T) {
	database := openApplicationDatabase(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	entered := false
	err := database.WithinTransaction(
		t.Context(),
		func(applicationcatalog.ContentRepository) error {
			entered = true
			return nil
		},
	)
	if entered {
		t.Fatal("callback entered through a closed database")
	}
	assertApplicationRepositoryFailure(t, err)
}

func assertApplicationRepositoryFailure(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, applicationcatalog.ErrRepositoryUnavailable) {
		t.Fatalf("repository error = %v, want %v", err, applicationcatalog.ErrRepositoryUnavailable)
	}
	var driverError *sqliteDriver.Error
	if errors.As(err, &driverError) {
		t.Fatalf("driver error crossed application boundary: %v", err)
	}
	for _, storageError := range []error{sql.ErrNoRows, sql.ErrTxDone, sql.ErrConnDone} {
		if errors.Is(err, storageError) {
			t.Fatalf("database/sql sentinel crossed application boundary: %v", err)
		}
	}
	message := strings.ToLower(err.Error())
	for _, detail := range []string{"tc_catalog_content", "no such table", "sql logic", "database is closed"} {
		if strings.Contains(message, detail) {
			t.Fatalf("storage diagnostic %q crossed application boundary: %v", detail, err)
		}
	}
}

func TestContentRepositoryRollsBackOperationError(t *testing.T) {
	database := openApplicationDatabase(t)
	want := testContent(t, "Will roll back", false)
	operationError := errors.New("operation failed")

	err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		if err := repository.Save(t.Context(), want); err != nil {
			return err
		}
		return operationError
	})
	if !errors.Is(err, operationError) {
		t.Fatalf("transaction error = %v, want %v", err, operationError)
	}
	assertContentAbsent(t, database, want.ID())
}

func TestUncommittedContentIsNotVisible(t *testing.T) {
	database := openApplicationDatabase(t)
	want := testContent(t, "Uncommitted", false)
	rollback := errors.New("rollback requested")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			if err := repository.Save(t.Context(), want); err != nil {
				return err
			}
			close(firstEntered)
			<-releaseFirst
			return rollback
		})
	}()
	<-firstEntered

	observerEntered := make(chan struct{})
	observerDone := make(chan error, 1)
	go func() {
		observerDone <- database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			close(observerEntered)
			_, found, err := repository.FindByID(t.Context(), want.ID())
			if err == nil && found {
				return errors.New("uncommitted content became visible")
			}
			return err
		})
	}()
	waitForConnectionWaiter(t, database, 1)
	select {
	case <-observerEntered:
		t.Fatal("observer entered while write transaction was uncommitted")
	default:
	}

	close(releaseFirst)
	if err := <-firstDone; !errors.Is(err, rollback) {
		t.Fatalf("first transaction error = %v, want %v", err, rollback)
	}
	if err := <-observerDone; err != nil {
		t.Fatalf("observer transaction: %v", err)
	}
}

func TestWriteTransactionsAreSerialized(t *testing.T) {
	database := openApplicationDatabase(t)
	first := testContent(t, "First", false)
	second := testContent(t, "Second", false)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			close(firstEntered)
			<-releaseFirst
			return repository.Save(t.Context(), first)
		})
	}()
	<-firstEntered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
			close(secondEntered)
			return repository.Save(t.Context(), second)
		})
	}()
	waitForConnectionWaiter(t, database, 1)
	select {
	case <-secondEntered:
		t.Fatal("second writer entered before first writer completed")
	default:
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first transaction: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second transaction: %v", err)
	}

	var got domaincatalog.Content
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		var found bool
		var err error
		got, found, err = repository.FindByID(t.Context(), first.ID())
		if err == nil && !found {
			return errors.New("serialized content missing")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got.Facts().Title() != "Second" {
		t.Fatalf("final title = %q, want Second", got.Facts().Title())
	}
}

func TestSchemaMigrationsAreDefensiveCopies(t *testing.T) {
	first := SchemaMigrations()
	if err := validateMigrations(first); err != nil {
		t.Fatalf("validate application migrations: %v", err)
	}
	first[0].ID = "changed"
	first[0].Statements[0] = "changed"
	second := SchemaMigrations()
	if second[0].ID != "0001-catalog-content" || !strings.HasPrefix(second[0].Statements[0], "CREATE TABLE") {
		t.Fatal("caller mutated canonical migrations")
	}
}

func openApplicationDatabase(t *testing.T) *Database {
	t.Helper()
	database, err := Open(t.Context(), Config{Path: filepath.Join(t.TempDir(), "application.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close application database: %v", err)
		}
	})
	return database
}

func testContent(t *testing.T, title string, withProductIdentifiers bool) domaincatalog.Content {
	t.Helper()
	id, err := identity.ParseContentID(contentIDText)
	if err != nil {
		t.Fatal(err)
	}
	product := domaincatalog.NewProductIdentifiers(nil, nil)
	if withProductIdentifiers {
		model, err := domaincatalog.ParseModelNumber("01-0100")
		if err != nil {
			t.Fatal(err)
		}
		article, err := domaincatalog.ParseArticleNumber("11000289")
		if err != nil {
			t.Fatal(err)
		}
		product = domaincatalog.NewProductIdentifiers(&model, &article)
	}
	content, err := domaincatalog.NewContent(id, domaincatalog.NewContentFacts(title, product))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func assertContentAbsent(t *testing.T, database *Database, id identity.ContentID) {
	t.Helper()
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		_, found, err := repository.FindByID(t.Context(), id)
		if err == nil && found {
			return errors.New("content unexpectedly persisted")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func waitForConnectionWaiter(t *testing.T, database *Database, minimum int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for database.db.Stats().WaitCount < minimum {
		if time.Now().After(deadline) {
			t.Fatalf("connection wait count = %d, want at least %d", database.db.Stats().WaitCount, minimum)
		}
		time.Sleep(time.Millisecond)
	}
}
