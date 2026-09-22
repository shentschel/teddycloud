package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

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

	transaction, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite transaction: %w", err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = transaction.Rollback()
		}
	}()

	if err := operation(contentRepository{transaction: transaction}); err != nil {
		if rollbackErr := transaction.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback sqlite transaction: %w", rollbackErr))
		}
		finished = true
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite transaction: %w", err)
	}
	finished = true
	return nil
}

func (repository contentRepository) Save(ctx context.Context, content domaincatalog.Content) error {
	model, article := nullableProductIdentifiers(content.Facts().ProductIdentifiers())
	if _, err := repository.transaction.ExecContext(
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
		return domaincatalog.Content{}, false, fmt.Errorf("find catalog content: %w", err)
	}

	product, err := productIdentifiers(modelText, articleText)
	if err != nil {
		return domaincatalog.Content{}, false, fmt.Errorf("rebuild catalog content: %w", err)
	}
	content, err := domaincatalog.NewContent(id, domaincatalog.NewContentFacts(title, product))
	if err != nil {
		return domaincatalog.Content{}, false, fmt.Errorf("rebuild catalog content: %w", err)
	}
	return content, true, nil
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
