package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var _ app.Port = (*assignmentTransactionPort)(nil)
var _ app.Transactor = (*LifecycleOwner)(nil)

var assignmentOwnerActor = app.AuthenticatedActor{
	Reference: "teddycloud-next-core",
	Read:      true,
	Decide:    true,
	Review:    true,
}

var assignmentRestoreValidations sync.Map // weak.Pointer[LifecycleOwner] -> uint64

// WithinAssignmentTransaction is the only production assignment authority. It
// shares the LifecycleOwner fence and one SQLite transaction for every Tag and
// catalog dependency read, ledger append and commit.
func (owner *LifecycleOwner) WithinAssignmentTransaction(ctx context.Context, operation func(app.Port) error) error {
	if ctx == nil || operation == nil {
		return app.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner == nil || owner.gate == nil {
		return app.ErrUnavailable
	}
	if ctx.Value(contentOperationContextKey{}) != nil {
		return app.ErrContention
	}
	ctx, cancel := context.WithTimeout(ctx, app.TransactionTimeout)
	defer cancel()
	if !owner.assignmentAdmitted.CompareAndSwap(false, true) {
		return app.ErrBusy
	}
	defer owner.assignmentAdmitted.Store(false)
	if err := owner.gate.enterOperation(ctx); err != nil {
		return assignmentBoundaryError(ctx, err)
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return app.ErrUnavailable
	}

	return owner.assignmentSQLTransaction(ctx, operation)
}

// The shared helper rolls back on this marker and joins any finalization
// failure to it. Read-only callbacks also take this path.
var errAssignmentPrecommit = errors.New("assignment precommit stopped")

func (owner *LifecycleOwner) assignmentSQLTransaction(ctx context.Context, operation func(app.Port) error) error {
	// Acquire (or reopen) the pooled handle with the caller's deadline
	// before detaching cancellation from transaction finalization.
	connection, err := owner.database.db.Conn(ctx)
	if err != nil {
		return assignmentReadError(ctx, err)
	}
	if err := connection.Close(); err != nil {
		return assignmentReadError(ctx, err)
	}

	var precommit error
	commitReady := false
	finished := false
	defer func() {
		if !finished {
			// The shared transaction unwinds first. A panic cannot return a
			// connection with unverified rollback to the next owner operation.
			owner.discardAssignmentConnection()
		}
	}()
	// The gate already exclusively owns the single-connection pool. Keep SQL
	// finalization synchronous and observable: database/sql cancellation must
	// not race the explicit rollback. Repository work uses the bounded ctx.
	err = owner.database.withinSQLTransaction(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		// Bind callback authority to the selected lifecycle generation as well
		// as this SQL transaction. A restore revokes the generation before it
		// can select a replacement handle.
		scope := &assignmentTransaction{
			generation:         &owner.generation,
			admittedGeneration: owner.generation.Load(),
		}
		repository := &assignmentRepository{transaction: tx}
		precommit = repository.check(ctx)
		if precommit == nil {
			precommit = owner.validateAssignmentRestore(ctx, repository)
		}
		if precommit == nil {
			precommit = scope.run(ctx, tx, assignmentOwnerActor, operation)
		}
		if precommit == nil {
			precommit = ctx.Err()
		}
		commitReady = precommit == nil && scope.wrote
		if !commitReady {
			return errAssignmentPrecommit
		}
		return nil
	})
	finished = true
	if err == nil {
		return nil
	}
	if commitReady {
		// The callback was admitted to commit. A driver or cleanup failure can
		// hide a durable result. The fence stays held through finalization.
		owner.discardAssignmentConnection()
		return app.ErrIndeterminate
	}
	if err == errAssignmentPrecommit {
		if precommit == nil {
			return ctx.Err()
		}
		return assignmentBoundaryError(ctx, precommit)
	}
	if errors.Is(err, errAssignmentPrecommit) {
		// Commit was never possible. Remove the connection with a failed
		// rollback or failed cleanup before allowing another owner operation.
		owner.discardAssignmentConnection()
		return app.ErrUnavailable
	}
	return assignmentBoundaryError(ctx, err)
}

// The generic backup boundary verifies SQLite, foreign keys and migrations.
// Before a restored generation grants Assignment authority, validate every
// retained aggregate once as well. This keeps a canonically corrupt ledger from
// exposing an otherwise healthy subset or accepting new writes.
func (owner *LifecycleOwner) validateAssignmentRestore(ctx context.Context, repository *assignmentRepository) error {
	generation := owner.generation.Load()
	if generation == 0 {
		return nil
	}
	key := weak.Make(owner)
	if validated, ok := assignmentRestoreValidations.Load(key); ok && validated.(uint64) == generation {
		return nil
	}
	rows, err := repository.transaction.QueryContext(ctx, `SELECT tag_id FROM tc_assignments ORDER BY tag_id`)
	if err != nil {
		return assignmentReadError(ctx, err)
	}
	tags := make([]identity.TagID, 0)
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			break
		}
		tag, parseErr := identity.ParseTagID(raw)
		if parseErr != nil {
			err = app.ErrUnavailable
			break
		}
		tags = append(tags, tag)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return assignmentReadError(ctx, err)
	}
	for _, tag := range tags {
		if _, found, err := repository.load(ctx, tag); err != nil {
			return err
		} else if !found {
			return app.ErrUnavailable
		}
	}
	if _, loaded := assignmentRestoreValidations.LoadOrStore(key, generation); loaded {
		assignmentRestoreValidations.Store(key, generation)
	} else {
		runtime.AddCleanup(owner, func(key weak.Pointer[LifecycleOwner]) {
			assignmentRestoreValidations.Delete(key)
		}, key)
	}
	return nil
}

func (owner *LifecycleOwner) discardAssignmentConnection() {
	cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := owner.database.db.Conn(cleanup)
	if err != nil {
		return
	}
	_ = connection.Raw(func(any) error { return driver.ErrBadConn })
	_ = connection.Close()
	// Re-establish the owner's persistent WAL setting on the replacement.
	_ = preserveWALSidecars(cleanup, owner.database)
}

func assignmentBoundaryError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, catalogapp.ErrRepositoryContention) {
		return app.ErrContention
	}
	for _, allowed := range []error{
		context.Canceled, context.DeadlineExceeded,
		app.ErrInvalidInput, app.ErrUnauthorized, app.ErrTagNotFound,
		app.ErrTargetNotFound, app.ErrDependencyConflict,
		app.ErrRevisionConflict, app.ErrCommandConflict, app.ErrRetired,
		app.ErrURIUnsupported, app.ErrLimit, app.ErrBusy, app.ErrContention,
		app.ErrUnavailable, app.ErrIndeterminate, app.ErrUnsupportedState,
		app.ErrKnowledgeRevision,
	} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	return app.ErrUnavailable
}

// assignmentTransaction supplies callback scope only. A trusted harness owns
// authentication and the SQL transaction's begin/commit/rollback lifecycle.
// Keeping run private prevents this inert component from becoming a Transactor.
type assignmentTransaction struct {
	active             atomic.Bool
	wrote              bool
	generation         *atomic.Uint64
	admittedGeneration uint64
}

func (transaction *assignmentTransaction) generationValid() bool {
	return transaction.generation == nil || transaction.generation.Load() == transaction.admittedGeneration
}

func (transaction *assignmentTransaction) run(ctx context.Context, tx *sql.Tx, actor app.AuthenticatedActor, operation func(app.Port) error) (result error) {
	if ctx == nil || operation == nil {
		return app.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if transaction == nil || tx == nil {
		return app.ErrUnavailable
	}
	if !transaction.generationValid() {
		return app.ErrUnavailable
	}
	if !transaction.active.CompareAndSwap(false, true) {
		return app.ErrBusy
	}
	defer transaction.active.Store(false)

	port := &assignmentTransactionPort{
		repository:         &assignmentRepository{transaction: tx},
		actor:              actor,
		ctx:                ctx,
		live:               true,
		generation:         transaction.generation,
		admittedGeneration: transaction.admittedGeneration,
	}
	defer func() {
		wrote, failure := port.revoke()
		transaction.wrote = wrote
		if result == nil {
			result = failure
		}
		if result == nil && !transaction.generationValid() {
			result = app.ErrUnavailable
		}
		if result == nil {
			result = ctx.Err()
		}
	}()
	return operation(port)
}

type assignmentTransactionPort struct {
	mutex              sync.Mutex
	repository         *assignmentRepository
	actor              app.AuthenticatedActor
	ctx                context.Context
	live               bool
	failure            error
	committed          bool
	generation         *atomic.Uint64
	admittedGeneration uint64
}

func (port *assignmentTransactionPort) revoke() (bool, error) {
	port.mutex.Lock()
	defer port.mutex.Unlock()
	port.live = false
	port.repository = nil
	return port.committed && port.failure == nil, port.failure
}

func (port *assignmentTransactionPort) call(ctx context.Context) (*assignmentRepository, error) {
	if ctx == nil {
		return nil, app.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if port == nil || !port.live || port.repository == nil {
		return nil, app.ErrUnavailable
	}
	if port.generation != nil && port.generation.Load() != port.admittedGeneration {
		return nil, app.ErrUnavailable
	}
	if err := port.ctx.Err(); err != nil {
		return nil, err
	}
	return port.repository, nil
}

func (port *assignmentTransactionPort) Authenticate(ctx context.Context) (app.AuthenticatedActor, error) {
	if port == nil {
		return app.AuthenticatedActor{}, app.ErrUnavailable
	}
	port.mutex.Lock()
	defer port.mutex.Unlock()
	if _, err := port.call(ctx); err != nil {
		return app.AuthenticatedActor{}, err
	}
	return port.actor, nil
}

func (port *assignmentTransactionPort) Tag(ctx context.Context, id identity.TagID) (app.TagSnapshot, bool, error) {
	if port == nil {
		return app.TagSnapshot{}, false, app.ErrUnavailable
	}
	port.mutex.Lock()
	defer port.mutex.Unlock()
	repository, err := port.call(ctx)
	if err != nil {
		return app.TagSnapshot{}, false, err
	}
	return repository.Tag(ctx, id)
}

func (port *assignmentTransactionPort) Target(ctx context.Context, target d.OriginalTarget) (app.TargetSnapshot, bool, error) {
	if port == nil {
		return app.TargetSnapshot{}, false, app.ErrUnavailable
	}
	port.mutex.Lock()
	defer port.mutex.Unlock()
	repository, err := port.call(ctx)
	if err != nil {
		return app.TargetSnapshot{}, false, err
	}
	return repository.Target(ctx, target)
}

func (port *assignmentTransactionPort) Load(ctx context.Context, tag identity.TagID) (app.Snapshot, bool, error) {
	if port == nil {
		return app.Snapshot{}, false, app.ErrUnavailable
	}
	port.mutex.Lock()
	defer port.mutex.Unlock()
	repository, err := port.call(ctx)
	if err != nil {
		return app.Snapshot{}, false, err
	}
	return repository.Load(ctx, tag)
}

func (port *assignmentTransactionPort) LookupCommand(ctx context.Context, id d.CommandID) (app.Event, bool, error) {
	if port == nil {
		return app.Event{}, false, app.ErrUnavailable
	}
	port.mutex.Lock()
	defer port.mutex.Unlock()
	repository, err := port.call(ctx)
	if err != nil {
		return app.Event{}, false, err
	}
	return repository.LookupCommand(ctx, id)
}

func (port *assignmentTransactionPort) Commit(ctx context.Context, expected uint64, snapshot app.Snapshot) error {
	if port == nil {
		return app.ErrUnavailable
	}
	port.mutex.Lock()
	defer port.mutex.Unlock()
	repository, err := port.call(ctx)
	if err != nil {
		return err
	}
	if port.committed {
		port.failure = app.ErrUnavailable
		return port.failure
	}
	port.committed = true
	if err := repository.commit(ctx, expected, snapshot); err != nil {
		port.failure = err
		return err
	}
	return nil
}
