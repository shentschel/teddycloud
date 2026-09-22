package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	sqliteDriver "modernc.org/sqlite"
)

var (
	ErrBackupExists  = errors.New("sqlite backup destination already exists")
	ErrBackupInvalid = errors.New("sqlite backup verification failed")
	ErrRestoreExists = errors.New("sqlite restore destination already exists")
)

// BackupFile describes a verified database-only snapshot. External media and
// credentials require a separate coordinated backup.
type BackupFile struct {
	Path          string
	SHA256        string
	SchemaVersion int
}

type sqliteBackuper interface {
	NewBackup(string) (*sqliteDriver.Backup, error)
	NewRestore(string) (*sqliteDriver.Backup, error)
}

// CreateBackup snapshots committed state, including uncheckpointed WAL pages.
// It publishes only a verified complete file and never replaces a destination.
func (database *Database) CreateBackup(ctx context.Context, destination string, migrations []Migration) (BackupFile, error) {
	if database == nil || database.db == nil {
		return BackupFile{}, fmt.Errorf("backup database is closed")
	}
	if err := validateMigrations(migrations); err != nil {
		return BackupFile{}, err
	}
	target, err := unusedTarget(destination, ErrBackupExists)
	if err != nil {
		return BackupFile{}, err
	}
	temporary, cleanup, err := temporaryDatabase(target)
	if err != nil {
		return BackupFile{}, err
	}
	defer cleanup()

	connection, err := database.db.Conn(ctx)
	if err != nil {
		return BackupFile{}, fmt.Errorf("acquire backup connection: %w", err)
	}
	defer connection.Close()
	if err := connection.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(sqliteBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not support backup")
		}
		operation, err := backuper.NewBackup(temporary)
		if err != nil {
			return err
		}
		return finishBackup(operation)
	}); err != nil {
		return BackupFile{}, fmt.Errorf("snapshot database: %w", err)
	}
	version, err := verifyDatabaseFile(ctx, temporary, migrations)
	if err != nil {
		return BackupFile{}, err
	}
	digest, err := fileDigest(temporary)
	if err != nil {
		return BackupFile{}, err
	}
	if err := os.Link(temporary, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return BackupFile{}, ErrBackupExists
		}
		return BackupFile{}, fmt.Errorf("publish backup: %w", err)
	}
	return BackupFile{Path: target, SHA256: digest, SchemaVersion: version}, nil
}

// VerifyBackup checks digest, SQLite integrity and the migration ledger.
func VerifyBackup(ctx context.Context, snapshot BackupFile, migrations []Migration) error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	if snapshot.Path == "" || len(snapshot.SHA256) != 64 {
		return ErrBackupInvalid
	}
	digest, err := fileDigest(snapshot.Path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBackupInvalid, err)
	}
	if !strings.EqualFold(digest, snapshot.SHA256) {
		return fmt.Errorf("%w: digest mismatch", ErrBackupInvalid)
	}
	version, err := verifyDatabaseFile(ctx, snapshot.Path, migrations)
	if err != nil {
		return err
	}
	if version != snapshot.SchemaVersion {
		return fmt.Errorf("%w: schema version %d, expected %d", ErrBackupInvalid, version, snapshot.SchemaVersion)
	}
	return nil
}

// RestoreBackupToNew restores into a new path while the caller fences access.
// A later operational switch can select the verified restored database.
func RestoreBackupToNew(ctx context.Context, snapshot BackupFile, destination string, migrations []Migration) error {
	target, err := unusedTarget(destination, ErrRestoreExists)
	if err != nil {
		return err
	}
	if err := VerifyBackup(ctx, snapshot, migrations); err != nil {
		return err
	}
	temporary, cleanup, err := temporaryDatabase(target)
	if err != nil {
		return err
	}
	defer cleanup()
	connector, err := sqliteDriver.NewConnector(fileURI(temporary, false))
	if err != nil {
		return fmt.Errorf("open restore destination: %w", err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	connection, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("acquire restore connection: %w", err)
	}
	restoreErr := connection.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(sqliteBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not support restore")
		}
		operation, err := backuper.NewRestore(snapshot.Path)
		if err != nil {
			return err
		}
		return finishBackup(operation)
	})
	closeErr := errors.Join(connection.Close(), db.Close())
	if err := errors.Join(restoreErr, closeErr); err != nil {
		return fmt.Errorf("restore database: %w", err)
	}
	if err := VerifyBackup(ctx, snapshot, migrations); err != nil {
		return err
	}
	version, err := verifyDatabaseFile(ctx, temporary, migrations)
	if err != nil {
		return err
	}
	if version != snapshot.SchemaVersion {
		return fmt.Errorf("%w: restored schema version %d, expected %d", ErrBackupInvalid, version, snapshot.SchemaVersion)
	}
	if err := os.Link(temporary, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrRestoreExists
		}
		return fmt.Errorf("publish restored database: %w", err)
	}
	return nil
}

func finishBackup(operation *sqliteDriver.Backup) error {
	more, stepErr := operation.Step(-1)
	finishErr := operation.Finish()
	if more && stepErr == nil {
		stepErr = fmt.Errorf("backup did not finish")
	}
	return errors.Join(stepErr, finishErr)
}

func unusedTarget(path string, existsError error) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("sqlite destination path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite destination: %w", err)
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", existsError
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return absolute, nil
}

func temporaryDatabase(target string) (string, func(), error) {
	directory, err := os.MkdirTemp(filepath.Dir(target), ".tc-sqlite-")
	if err != nil {
		return "", nil, fmt.Errorf("create sqlite staging directory: %w", err)
	}
	return filepath.Join(directory, "database.sqlite"), func() { _ = os.RemoveAll(directory) }, nil
}

func fileURI(path string, readonly bool) string {
	location := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if readonly {
		query := location.Query()
		query.Set("mode", "ro")
		location.RawQuery = query.Encode()
	}
	return location.String()
}

func verifyDatabaseFile(ctx context.Context, path string, migrations []Migration) (int, error) {
	connector, err := sqliteDriver.NewConnector(fileURI(path, true))
	if err != nil {
		return 0, fmt.Errorf("%w: open snapshot: %v", ErrBackupInvalid, err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return 0, fmt.Errorf("%w: integrity query: %v", ErrBackupInvalid, err)
	}
	if integrity != "ok" {
		return 0, fmt.Errorf("%w: integrity check: %s", ErrBackupInvalid, integrity)
	}
	applied, err := readAppliedMigrations(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBackupInvalid, err)
	}
	if err := validateAppliedMigrations(applied, migrations); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBackupInvalid, err)
	}
	return len(applied), nil
}

func fileDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: snapshot is not a regular file", ErrBackupInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash sqlite file: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
