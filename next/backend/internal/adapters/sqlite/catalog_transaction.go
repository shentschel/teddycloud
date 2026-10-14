package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var _ app.CatalogTransactor = (*LifecycleOwner)(nil)
var _ app.CatalogRepository = (*catalogRepository)(nil)

// WithinCatalogTransaction exposes the read-only PI-08 query checkpoint through
// the existing restore fence. Even successful callbacks roll back their SQL
// snapshot; query_only also prevents accidental writes on the leased connection.
func (owner *LifecycleOwner) WithinCatalogTransaction(ctx context.Context, operation func(app.CatalogRepository) error) error {
	if ctx == nil || operation == nil {
		return app.ErrInvalidInput
	}
	if owner == nil || owner.gate == nil {
		return app.ErrRepositoryUnavailable
	}
	if ctx.Value(contentOperationContextKey{}) != nil {
		return app.ErrRepositoryContention
	}
	ctx, cancel := context.WithTimeout(ctx, domain.CatalogTransactionTimeout)
	defer cancel()
	if err := owner.gate.enterOperation(ctx); err != nil {
		return repositoryError(ctx, err)
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return app.ErrRepositoryUnavailable
	}
	return owner.readCatalogTransaction(ctx, operation)
}

func (owner *LifecycleOwner) readCatalogTransaction(ctx context.Context, operation func(app.CatalogRepository) error) (result error) {
	connection, err := owner.database.db.Conn(ctx)
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer connection.Close()
	var previous int
	if err := connection.QueryRowContext(ctx, "PRAGMA query_only").Scan(&previous); err != nil {
		return repositoryError(ctx, err)
	}
	if _, err := connection.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return repositoryError(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		setting := "PRAGMA query_only=OFF"
		if previous != 0 {
			setting = "PRAGMA query_only=ON"
		}
		if _, err := connection.ExecContext(cleanup, setting); err != nil {
			result = errors.Join(result, app.ErrRepositoryUnavailable)
		}
	}()
	tx, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer tx.Rollback()
	var version, dirty int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0),COALESCE(MAX(dirty),0) FROM tc_schema_migrations").Scan(&version, &dirty); err != nil {
		return repositoryError(ctx, err)
	}
	if version != 5 || dirty != 0 {
		return app.ErrUnsupported
	}
	repository := &catalogRepository{transaction: tx, ctx: ctx, owner: owner, database: owner.database, generation: owner.generation.Load()}
	defer repository.revoke()
	result = operation(repository)
	repository.revoke()
	if result == nil {
		result = ctx.Err()
	}
	if result == nil {
		result = repository.failure
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(result, repositoryError(ctx, err))
	}
	return result
}

type catalogRepository struct {
	mutex       sync.Mutex
	transaction *sql.Tx
	ctx         context.Context
	owner       *LifecycleOwner
	database    *Database
	generation  uint64
	failure     error
}

func (r *catalogRepository) revoke() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.transaction = nil
}

// Decision restoration, replay and mutation need a separate bounded checkpoint.
// Returning Unsupported is deliberate; this adapter never invents empty states.
func (r *catalogRepository) LoadReview(context.Context, domain.CatalogSubject) (app.ReviewSnapshot, bool, error) {
	return app.ReviewSnapshot{}, false, r.unsupported()
}

func (r *catalogRepository) LoadOrder(context.Context, identity.ContentID) (app.OrderSnapshot, bool, error) {
	return app.OrderSnapshot{}, false, r.unsupported()
}

func (r *catalogRepository) LookupReview(context.Context, app.ReviewOperation) (app.ReviewResult, bool, error) {
	return app.ReviewResult{}, false, r.unsupported()
}

func (r *catalogRepository) LookupOrder(context.Context, app.OrderOperation) (app.OrderResult, bool, error) {
	return app.OrderResult{}, false, r.unsupported()
}

func (r *catalogRepository) CommitReview(context.Context, app.ReviewOperation, domain.CatalogReviewState, app.ReviewResult) error {
	return r.unsupported()
}

func (r *catalogRepository) CommitOrder(context.Context, app.OrderOperation, domain.CatalogOrderState, app.OrderResult) error {
	return r.unsupported()
}

func (r *catalogRepository) unsupported() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.transaction == nil {
		return app.ErrRepositoryUnavailable
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	return app.ErrUnsupported
}
