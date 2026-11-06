package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
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

	commitReady := false
	err := owner.database.withinSQLTransaction(ctx, func(tx *sql.Tx) error {
		// Reject v9, synthetic v11, dirty ledgers and corrupt references before
		// granting even the callback-scoped authority.
		if err := (&assignmentRepository{transaction: tx}).check(ctx); err != nil {
			return err
		}
		scope := &assignmentTransaction{}
		if err := scope.run(ctx, tx, assignmentOwnerActor, operation); err != nil {
			return err
		}
		commitReady = true
		return nil
	})
	if err == nil {
		return nil
	}
	if commitReady {
		return app.ErrIndeterminate
	}
	return assignmentBoundaryError(ctx, err)
}

func assignmentBoundaryError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
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
type assignmentTransaction struct{ active atomic.Bool }

func (transaction *assignmentTransaction) run(ctx context.Context, tx *sql.Tx, actor app.AuthenticatedActor, operation func(app.Port) error) error {
	if ctx == nil || operation == nil {
		return app.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if transaction == nil || tx == nil {
		return app.ErrUnavailable
	}
	if !transaction.active.CompareAndSwap(false, true) {
		return app.ErrBusy
	}
	defer transaction.active.Store(false)

	port := &assignmentTransactionPort{repository: &assignmentRepository{transaction: tx}, actor: actor, live: true}
	err := operation(port)
	failure := port.revoke()
	if err != nil {
		return err
	}
	if failure != nil {
		return failure
	}
	return ctx.Err()
}

type assignmentTransactionPort struct {
	mutex      sync.Mutex
	repository *assignmentRepository
	actor      app.AuthenticatedActor
	live       bool
	failure    error
	committed  bool
}

func (port *assignmentTransactionPort) revoke() error {
	if port == nil {
		return app.ErrUnavailable
	}
	port.mutex.Lock()
	defer port.mutex.Unlock()
	port.live = false
	port.repository = nil
	return port.failure
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
	if port.committed {
		port.failure = app.ErrUnavailable
		return port.failure
	}
	repository, err := port.call(ctx)
	if err != nil {
		return err
	}
	port.committed = true
	if err := repository.commit(ctx, expected, snapshot); err != nil {
		port.failure = err
		return err
	}
	return nil
}
