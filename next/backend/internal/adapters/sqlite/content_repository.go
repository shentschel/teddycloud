package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const contentionPollInterval = 10 * time.Millisecond

var ErrNilTransactionOperation = errors.New("sqlite transaction operation is nil")

var _ applicationcatalog.Transactor = (*Database)(nil)
var _ applicationcatalog.ContentRepository = contentRepository{}

type contentRepository struct {
	transaction *sql.Tx
}

// WithinTransaction runs operation on the adapter's single serialized
// connection and commits only when operation succeeds.
func (database *Database) WithinTransaction(
	ctx context.Context,
	operation func(applicationcatalog.ContentRepository) error,
) error {
	if operation == nil {
		return ErrNilTransactionOperation
	}
	return database.withinSQLTransaction(ctx, func(transaction *sql.Tx) error {
		return operation(contentRepository{transaction: transaction})
	})
}

// withinSQLTransaction is adapter-private plumbing shared by the distinct
// application ports. It never retries the callback.
func (database *Database) withinSQLTransaction(ctx context.Context, operation func(*sql.Tx) error) (resultErr error) {
	// Run after rollback and connection cleanup: cancellation can arrive after
	// the operation's error was mapped, and must dominate joined cleanup errors.
	defer func() {
		if resultErr != nil && ctx.Err() != nil {
			resultErr = ctx.Err()
		}
	}()
	connection, err := database.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire transaction connection: %w", repositoryError(ctx, err))
	}
	defer connection.Close()
	var busyTimeoutMillis int
	if err := connection.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeoutMillis); err != nil {
		return repositoryError(ctx, err)
	}
	// A canceled transaction can be rolled back by database/sql before Save's
	// cleanup runs. Restore on the leased connection, after rollback and before
	// it returns to the pool, so cancellation cannot leak a shortened timeout.
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := connection.ExecContext(restoreCtx, busyTimeoutPragma(busyTimeoutMillis)); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("restore transaction wait budget: %w", repositoryError(restoreCtx, err)),
			)
		}
	}()
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite transaction: %w", repositoryError(ctx, err))
	}
	finished := false
	defer func() {
		if !finished {
			_ = transaction.Rollback()
		}
	}()

	if err := operation(transaction); err != nil {
		if rollbackErr := transaction.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback sqlite transaction: %w", repositoryError(ctx, rollbackErr)))
		}
		finished = true
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite transaction: %w", repositoryError(ctx, err))
	}
	finished = true
	return nil
}

func (repository contentRepository) Save(ctx context.Context, content domaincatalog.Content) error {
	// Read the selected handle's ledger inside this transaction: an exact
	// pre-PI-08 restore has no managed table and must remain usable as-is.
	var version, dirty int
	if err := repository.transaction.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0), COALESCE(MAX(dirty), 0)
		 FROM tc_schema_migrations`,
	).Scan(&version, &dirty); err != nil {
		return repositoryError(ctx, err)
	}
	if version < 1 || dirty != 0 {
		return applicationcatalog.ErrRepositoryUnavailable
	}
	if version >= 5 {
		var managed bool
		if err := repository.transaction.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM tc_catalog_managed WHERE content_id = ?)`,
			content.ID().String(),
		).Scan(&managed); err != nil {
			return repositoryError(ctx, err)
		}
		if managed {
			return applicationcatalog.ErrUnauthorized
		}
	}
	// The guard and upsert share a SQL transaction and its read snapshot.
	// SQLite refuses to promote a stale snapshot to a writer after another
	// connection commits adoption; contention never replays the callback.
	model, article := nullableProductIdentifiers(content.Facts().ProductIdentifiers())
	if err := repository.execWithContention(
		ctx,
		`INSERT INTO tc_catalog_content (content_id, title, model_number, article_number)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(content_id) DO UPDATE SET
			title = excluded.title,
			model_number = excluded.model_number,
			article_number = excluded.article_number`,
		content.ID().String(),
		content.Facts().Title(),
		model,
		article,
	); err != nil {
		return fmt.Errorf("save catalog content: %w", err)
	}
	return nil
}

func (repository contentRepository) execWithContention(
	ctx context.Context,
	query string,
	arguments ...any,
) error {
	return execWithContention(ctx, repository.transaction, repositoryError, query, arguments...)
}

func execWithContention(
	ctx context.Context,
	transaction *sql.Tx,
	mapError func(context.Context, error) error,
	query string,
	arguments ...any,
) (resultErr error) {
	// Error mapping and wait-budget restoration can both race cancellation.
	// Keep cleanup independent of ctx, but classify the final error using ctx.
	defer func() {
		if resultErr != nil && ctx.Err() != nil {
			resultErr = ctx.Err()
		}
	}()
	var busyTimeoutMillis int
	if err := transaction.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeoutMillis); err != nil {
		return mapError(ctx, err)
	}

	pollMillis := int(contentionPollInterval / time.Millisecond)
	if busyTimeoutMillis < pollMillis {
		pollMillis = busyTimeoutMillis
	}
	if pollMillis < 1 {
		pollMillis = 1
	}
	if _, err := transaction.ExecContext(ctx, busyTimeoutPragma(pollMillis)); err != nil {
		return mapError(ctx, err)
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := transaction.ExecContext(
			restoreCtx,
			busyTimeoutPragma(busyTimeoutMillis),
		); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("restore repository wait budget: %w", mapError(restoreCtx, err)),
			)
		}
	}()

	deadline := time.Now().Add(time.Duration(busyTimeoutMillis) * time.Millisecond)
	for {
		_, err := transaction.ExecContext(ctx, query, arguments...)
		if err == nil {
			return nil
		}
		mappedErr := mapError(ctx, err)
		if ctx.Err() != nil {
			return mappedErr
		}
		code, ok := sqlitePrimaryCode(err)
		if !ok || code != sqlite3.SQLITE_BUSY || !time.Now().Before(deadline) {
			return mappedErr
		}
	}
}

func busyTimeoutPragma(timeoutMillis int) string {
	return "PRAGMA busy_timeout(" + strconv.Itoa(timeoutMillis) + ")"
}

func (repository contentRepository) FindByID(
	ctx context.Context,
	id identity.ContentID,
) (domaincatalog.Content, bool, error) {
	var title string
	var modelText sql.NullString
	var articleText sql.NullString
	if err := repository.transaction.QueryRowContext(
		ctx,
		`SELECT title, model_number, article_number
		 FROM tc_catalog_content
		 WHERE content_id = ?`,
		id.String(),
	).Scan(&title, &modelText, &articleText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domaincatalog.Content{}, false, nil
		}
		return domaincatalog.Content{}, false, fmt.Errorf("find catalog content: %w", repositoryError(ctx, err))
	}

	product, err := productIdentifiers(modelText, articleText)
	if err != nil {
		return domaincatalog.Content{}, false, applicationcatalog.ErrRepositoryUnavailable
	}
	content, err := domaincatalog.NewContent(id, domaincatalog.NewContentFacts(title, product))
	if err != nil {
		return domaincatalog.Content{}, false, applicationcatalog.ErrRepositoryUnavailable
	}
	return content, true, nil
}

func repositoryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	code, ok := sqlitePrimaryCode(err)
	if ok && (code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED) {
		return applicationcatalog.ErrRepositoryContention
	}
	return applicationcatalog.ErrRepositoryUnavailable
}

func sqlitePrimaryCode(err error) (int, bool) {
	var sqliteErr *sqliteDriver.Error
	if !errors.As(err, &sqliteErr) {
		return 0, false
	}
	return sqliteErr.Code() & 0xff, true
}

func nullableProductIdentifiers(product domaincatalog.ProductIdentifiers) (any, any) {
	var model any
	if value, ok := product.Model(); ok {
		model = value.String()
	}
	var article any
	if value, ok := product.Article(); ok {
		article = value.String()
	}
	return model, article
}

func productIdentifiers(
	modelText sql.NullString,
	articleText sql.NullString,
) (domaincatalog.ProductIdentifiers, error) {
	var model *domaincatalog.ModelNumber
	if modelText.Valid {
		value, err := domaincatalog.ParseModelNumber(modelText.String)
		if err != nil {
			return domaincatalog.ProductIdentifiers{}, err
		}
		model = &value
	}
	var article *domaincatalog.ArticleNumber
	if articleText.Valid {
		value, err := domaincatalog.ParseArticleNumber(articleText.String)
		if err != nil {
			return domaincatalog.ProductIdentifiers{}, err
		}
		article = &value
	}
	return domaincatalog.NewProductIdentifiers(model, article), nil
}
