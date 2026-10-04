package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
)

var (
	ErrLifecycleClosed           = errors.New("sqlite lifecycle owner is closed")
	ErrLifecycleOpen             = errors.New("sqlite lifecycle owner open failed")
	ErrLifecycleClose            = errors.New("sqlite lifecycle owner close failed")
	ErrLifecycleVersion          = errors.New("sqlite lifecycle version read failed")
	ErrLifecycleRestore          = errors.New("sqlite lifecycle restore failed")
	ErrRestoreSameDestination    = errors.New("sqlite restore destination is the current database")
	ErrRestoreDestinationSidecar = errors.New("sqlite restore destination sidecar already exists")
)

// LifecycleOwner is the single-process owner of one active application
// database handle. It exposes application ports, not Database or SQL values.
type LifecycleOwner struct {
	gate             *lifecycleGate
	database         *Database
	config           Config
	migrations       []Migration
	contentAdmitted  atomic.Bool
	generation       atomic.Uint64
	contentInventory contentstore.InventoryInvalidator
}

var _ applicationcatalog.Transactor = (*LifecycleOwner)(nil)
var _ applicationtag.Transactor = (*LifecycleOwner)(nil)

// OpenLifecycleOwner opens and exclusively owns the configured database.
func OpenLifecycleOwner(
	ctx context.Context,
	config Config,
	migrations []Migration,
) (*LifecycleOwner, error) {
	ownedMigrations := cloneMigrations(migrations)
	database, err := Open(ctx, config, ownedMigrations)
	if err != nil {
		var upgradeFailure *UpgradeError
		if errors.As(err, &upgradeFailure) {
			return nil, &UpgradeError{Backup: upgradeFailure.Backup, Err: lifecycleError(ctx, ErrLifecycleOpen)}
		}
		return nil, lifecycleError(ctx, ErrLifecycleOpen)
	}
	if err := preserveWALSidecars(ctx, database); err != nil {
		_ = database.Close()
		return nil, lifecycleError(ctx, ErrLifecycleOpen)
	}
	absolutePath, err := filepath.Abs(config.Path)
	if err != nil {
		_ = database.Close()
		return nil, ErrLifecycleOpen
	}
	config.Path = absolutePath
	return &LifecycleOwner{
		gate:       newLifecycleGate(),
		database:   database,
		config:     config,
		migrations: ownedMigrations,
	}, nil
}

// WithinTransaction admits one callback through the owner gate. Repository
// failures are sanitized before they can cross the application port.
func (owner *LifecycleOwner) WithinTransaction(
	ctx context.Context,
	operation func(applicationcatalog.ContentRepository) error,
) error {
	if operation == nil {
		return ErrNilTransactionOperation
	}
	if ctx.Value(contentOperationContextKey{}) != nil {
		return applicationcatalog.ErrRepositoryContention
	}
	if owner == nil || owner.gate == nil {
		return applicationcatalog.ErrRepositoryUnavailable
	}
	if err := owner.gate.enterOperation(ctx); err != nil {
		return err
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return applicationcatalog.ErrRepositoryUnavailable
	}

	var callbackFailure *lifecycleCallbackFailure
	err := owner.database.WithinTransaction(ctx, func(repository applicationcatalog.ContentRepository) error {
		callbackErr := operation(repository)
		if callbackErr == nil {
			return nil
		}
		callbackFailure = &lifecycleCallbackFailure{err: callbackErr}
		return callbackFailure
	})
	if err == nil {
		return nil
	}
	if callbackFailure != nil && err == callbackFailure {
		return callbackFailure.err
	}
	if callbackFailure != nil && errors.Is(err, callbackFailure) {
		return errors.Join(callbackFailure.err, repositoryBoundaryError(ctx, err))
	}
	return repositoryBoundaryError(ctx, err)
}

// WithinTagTransaction shares the Content lifecycle fence and uses only the
// selected handle. A repository cannot outlive this single callback.
func (owner *LifecycleOwner) WithinTagTransaction(ctx context.Context, operation func(applicationtag.TagRepository) error) error {
	if operation == nil {
		return applicationtag.ErrInvalidInput
	}
	if ctx.Value(contentOperationContextKey{}) != nil {
		return applicationtag.ErrRepositoryContention
	}
	if owner == nil || owner.gate == nil {
		return applicationtag.ErrRepositoryUnavailable
	}
	if err := owner.gate.enterOperation(ctx); err != nil {
		return tagBoundaryError(ctx, err)
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return applicationtag.ErrRepositoryUnavailable
	}
	return owner.database.withinTagTransaction(ctx, operation)
}

// CurrentVersion reports the selected handle's clean schema version.
func (owner *LifecycleOwner) CurrentVersion(ctx context.Context) (int, error) {
	if owner == nil || owner.gate == nil {
		return 0, ErrLifecycleClosed
	}
	if err := owner.gate.enterOperation(ctx); err != nil {
		return 0, err
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return 0, ErrLifecycleClosed
	}
	version, err := owner.database.CurrentVersion(ctx)
	if err != nil {
		return 0, lifecycleError(ctx, ErrLifecycleVersion)
	}
	return version, nil
}

// UpgradeBackup reports the verified pre-upgrade snapshot without exposing the
// owned database handle. Metadata remains available after a successful upgrade.
func (owner *LifecycleOwner) UpgradeBackup(ctx context.Context) (BackupFile, bool, error) {
	if owner == nil || owner.gate == nil {
		return BackupFile{}, false, ErrLifecycleClosed
	}
	if err := owner.gate.enterOperation(ctx); err != nil {
		return BackupFile{}, false, err
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return BackupFile{}, false, ErrLifecycleClosed
	}
	snapshot, found := owner.database.UpgradeBackup()
	return snapshot, found, nil
}

// Restore drains the active operation, closes the old handle, restores only to
// an unused path, and selects a handle at the snapshot's exact schema version.
func (owner *LifecycleOwner) Restore(
	ctx context.Context,
	snapshot BackupFile,
	destination string,
) error {
	if ctx.Value(contentOperationContextKey{}) != nil {
		return contentstore.ErrBusy
	}
	if owner == nil || owner.gate == nil {
		return ErrLifecycleClosed
	}
	if err := owner.gate.enterLifecycle(ctx); err != nil {
		return err
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return ErrLifecycleClosed
	}

	target, err := owner.restorePreflight(ctx, snapshot, destination)
	if err != nil {
		return err
	}

	old := owner.database
	if err := owner.invalidateContentInventory(); err != nil {
		return err
	}
	owner.generation.Add(1)
	owner.database = nil
	if err := old.Close(); err != nil {
		return ErrLifecycleClose
	}
	if err := RestoreBackupToNew(ctx, snapshot, target, owner.migrations); err != nil {
		return lifecycleError(ctx, ErrLifecycleRestore)
	}

	restoredConfig := owner.config
	restoredConfig.Path = target
	restoredConfig.UpgradeBackupPath = ""
	restoredMigrations := cloneMigrations(owner.migrations[:snapshot.SchemaVersion])
	restored, err := Open(ctx, restoredConfig, restoredMigrations)
	if err != nil {
		return lifecycleError(ctx, ErrLifecycleRestore)
	}
	closeRestored := true
	defer func() {
		if closeRestored {
			_ = restored.Close()
		}
	}()
	if err := preserveWALSidecars(ctx, restored); err != nil {
		return lifecycleError(ctx, ErrLifecycleRestore)
	}
	version, err := restored.CurrentVersion(ctx)
	if err != nil || version != snapshot.SchemaVersion {
		return lifecycleError(ctx, ErrLifecycleRestore)
	}

	owner.database = restored
	owner.config = restoredConfig
	closeRestored = false
	return nil
}

// Close drains the active operation and permanently closes the selected handle.
func (owner *LifecycleOwner) Close(ctx context.Context) error {
	if ctx.Value(contentOperationContextKey{}) != nil {
		return contentstore.ErrBusy
	}
	if owner == nil || owner.gate == nil {
		return nil
	}
	if err := owner.gate.enterLifecycle(ctx); err != nil {
		return err
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return nil
	}
	database := owner.database
	inventoryErr := owner.invalidateContentInventory()
	owner.generation.Add(1)
	owner.database = nil
	if err := database.Close(); err != nil {
		return errors.Join(inventoryErr, lifecycleError(ctx, ErrLifecycleClose))
	}
	return inventoryErr
}

func (owner *LifecycleOwner) restorePreflight(
	ctx context.Context,
	snapshot BackupFile,
	destination string,
) (string, error) {
	target, err := filepath.Abs(destination)
	if err != nil {
		return "", ErrLifecycleRestore
	}
	if filepath.Clean(target) == filepath.Clean(owner.config.Path) {
		return "", ErrRestoreSameDestination
	}
	target, err = unusedTarget(target, ErrRestoreExists)
	if err != nil {
		if errors.Is(err, ErrRestoreExists) {
			return "", ErrRestoreExists
		}
		return "", ErrLifecycleRestore
	}
	for _, sidecar := range []string{target + "-wal", target + "-shm"} {
		if _, err := os.Lstat(sidecar); err == nil {
			return "", fmt.Errorf("%w: %s", ErrRestoreDestinationSidecar, filepath.Base(sidecar))
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", ErrLifecycleRestore
		}
	}
	if snapshot.SchemaVersion < 0 || snapshot.SchemaVersion > len(owner.migrations) {
		return "", ErrBackupInvalid
	}
	if err := VerifyBackup(ctx, snapshot, owner.migrations); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrBackupInvalid
	}
	return target, nil
}

func repositoryBoundaryError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, applicationcatalog.ErrRepositoryContention) {
		return applicationcatalog.ErrRepositoryContention
	}
	return applicationcatalog.ErrRepositoryUnavailable
}

type lifecycleCallbackFailure struct {
	err error
}

func (failure *lifecycleCallbackFailure) Error() string {
	return failure.err.Error()
}

func (failure *lifecycleCallbackFailure) Unwrap() error {
	return failure.err
}

func lifecycleError(ctx context.Context, fallback error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return fallback
}

func cloneMigrations(migrations []Migration) []Migration {
	result := make([]Migration, len(migrations))
	for index, migration := range migrations {
		result[index] = migration
		result[index].Statements = append([]string(nil), migration.Statements...)
	}
	return result
}

type persistWALController interface {
	FileControlPersistWAL(string, int) (int, error)
}

func preserveWALSidecars(ctx context.Context, database *Database) error {
	connection, err := database.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(driverConnection any) error {
		controller, ok := driverConnection.(persistWALController)
		if !ok {
			return errors.New("sqlite driver lacks persistent WAL control")
		}
		mode, err := controller.FileControlPersistWAL("main", 1)
		if err != nil {
			return err
		}
		if mode != 1 {
			return errors.New("sqlite driver refused persistent WAL mode")
		}
		return nil
	})
}

type lifecycleGate struct {
	mutex            sync.Mutex
	active           bool
	lifecycleWaiters int
	changed          chan struct{}
}

func newLifecycleGate() *lifecycleGate {
	return &lifecycleGate{changed: make(chan struct{})}
}

func (gate *lifecycleGate) enterOperation(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		gate.mutex.Lock()
		if !gate.active && gate.lifecycleWaiters == 0 {
			gate.active = true
			gate.mutex.Unlock()
			if err := ctx.Err(); err != nil {
				gate.leave()
				return err
			}
			return nil
		}
		changed := gate.changed
		gate.mutex.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (gate *lifecycleGate) enterLifecycle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.mutex.Lock()
	gate.lifecycleWaiters++
	gate.signalLocked()
	for gate.active {
		changed := gate.changed
		gate.mutex.Unlock()
		select {
		case <-ctx.Done():
			gate.mutex.Lock()
			gate.lifecycleWaiters--
			gate.signalLocked()
			gate.mutex.Unlock()
			return ctx.Err()
		case <-changed:
			gate.mutex.Lock()
		}
	}
	gate.active = true
	gate.lifecycleWaiters--
	gate.signalLocked()
	gate.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		gate.leave()
		return err
	}
	return nil
}

func (gate *lifecycleGate) leave() {
	gate.mutex.Lock()
	gate.active = false
	gate.signalLocked()
	gate.mutex.Unlock()
}

// Content operations have no retained queue. In particular, an existing
// Content/Tag transaction callback cannot recursively admit a content session.
// Lifecycle waiters keep priority; callers can retry a busy admission later.
func (gate *lifecycleGate) enterContentOperation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	if gate.active || gate.lifecycleWaiters > 0 {
		return contentstore.ErrBusy
	}
	gate.active = true
	return nil
}

func (gate *lifecycleGate) signalLocked() {
	close(gate.changed)
	gate.changed = make(chan struct{})
}

type contentOperationContextKey struct{}

// AttachContentInventory binds exactly one store to this owner. Installation
// and every invalidation run under the same fence as restore and media calls.
func (owner *LifecycleOwner) AttachContentInventory(ctx context.Context, inventory contentstore.InventoryInvalidator) error {
	if ctx == nil || inventory == nil {
		return contentstore.ErrInvalidInput
	}
	if owner == nil || owner.gate == nil {
		return contentstore.ErrUnavailable
	}
	if ctx.Value(contentOperationContextKey{}) != nil {
		return contentstore.ErrBusy
	}
	if err := owner.gate.enterContentOperation(ctx); err != nil {
		return contentBoundaryError(ctx, err)
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return contentstore.ErrUnavailable
	}
	if owner.contentInventory != nil {
		return contentstore.ErrConflict
	}
	if err := inventory.InvalidateInventory(); err != nil {
		return contentBoundaryError(ctx, err)
	}
	owner.contentInventory = inventory
	return nil
}

func (owner *LifecycleOwner) invalidateContentInventory() error {
	if owner.contentInventory == nil {
		return nil
	}
	return contentBoundaryError(context.Background(), owner.contentInventory.InvalidateInventory())
}

var _ contentstore.OperationOwner = (*LifecycleOwner)(nil)
var _ contentstore.MediaOwner = (*LifecycleOwner)(nil)

// WithinContentOperation admits at most one content caller, then enters the
// common lifecycle gate once. No SQL transaction spans this callback.
func (owner *LifecycleOwner) WithinContentOperation(ctx context.Context, operation func(context.Context, contentstore.Session) error) error {
	if operation == nil {
		return contentstore.ErrInvalidInput
	}
	if owner == nil || owner.gate == nil {
		return contentstore.ErrUnavailable
	}
	if ctx.Value(contentOperationContextKey{}) != nil || !owner.contentAdmitted.CompareAndSwap(false, true) {
		return contentstore.ErrBusy
	}
	defer owner.contentAdmitted.Store(false)
	operationContext, cancel := context.WithTimeout(context.WithValue(ctx, contentOperationContextKey{}, owner), contentstore.OperationTimeout)
	defer cancel()
	admission, cancelAdmission := context.WithTimeout(operationContext, contentstore.AdmissionTimeout)
	err := owner.gate.enterContentOperation(admission)
	cancelAdmission()
	if err != nil {
		return contentBoundaryError(ctx, err)
	}
	defer owner.gate.leave()
	if owner.database == nil {
		return contentstore.ErrUnavailable
	}
	version, err := owner.database.CurrentVersion(operationContext)
	if err != nil {
		return contentBoundaryError(ctx, err)
	}
	if version != 4 {
		return contentstore.ErrSchemaUnavailable
	}
	session := &contentSession{owner: owner, database: owner.database, generation: owner.generation.Load(), ctx: operationContext, active: true}
	defer session.revoke()
	err = operation(operationContext, session)
	if err == nil {
		err = operationContext.Err()
	}
	return contentBoundaryError(operationContext, err)
}
