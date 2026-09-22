package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrInvalidMigration = errors.New("invalid sqlite migration")
	ErrDirtyMigration   = errors.New("sqlite migration is dirty")
	ErrSchemaNewer      = errors.New("sqlite schema is newer than supported")
	ErrChecksumDrift    = errors.New("sqlite migration checksum drift")
)

// Migration is one immutable, ordered schema change.
type Migration struct {
	Version    int
	ID         string
	Statements []string
}

type appliedMigration struct {
	version  int
	id       string
	checksum string
	dirty    bool
}

const createMigrationLedger = `
CREATE TABLE IF NOT EXISTS tc_schema_migrations (
	version INTEGER PRIMARY KEY,
	migration_id TEXT NOT NULL UNIQUE,
	checksum TEXT NOT NULL,
	dirty INTEGER NOT NULL CHECK (dirty IN (0, 1)),
	applied_at TEXT
) STRICT`

func validateMigrations(migrations []Migration) error {
	ids := make(map[string]struct{}, len(migrations))
	for index, migration := range migrations {
		expectedVersion := index + 1
		if migration.Version != expectedVersion {
			return fmt.Errorf(
				"%w: version %d at index %d, want %d",
				ErrInvalidMigration,
				migration.Version,
				index,
				expectedVersion,
			)
		}
		if strings.TrimSpace(migration.ID) == "" {
			return fmt.Errorf("%w: version %d has an empty ID", ErrInvalidMigration, migration.Version)
		}
		if _, exists := ids[migration.ID]; exists {
			return fmt.Errorf("%w: duplicate ID %q", ErrInvalidMigration, migration.ID)
		}
		ids[migration.ID] = struct{}{}
		if len(migration.Statements) == 0 {
			return fmt.Errorf("%w: version %d has no statements", ErrInvalidMigration, migration.Version)
		}
		for statementIndex, statement := range migration.Statements {
			if strings.TrimSpace(statement) == "" {
				return fmt.Errorf(
					"%w: version %d statement %d is empty",
					ErrInvalidMigration,
					migration.Version,
					statementIndex,
				)
			}
		}
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB, migrations []Migration) error {
	if _, err := db.ExecContext(ctx, createMigrationLedger); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	applied, err := readAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	if err := validateAppliedMigrations(applied, migrations); err != nil {
		return err
	}

	for index := len(applied); index < len(migrations); index++ {
		if err := applyMigration(ctx, db, migrations[index]); err != nil {
			return err
		}
	}
	return nil
}

func readAppliedMigrations(ctx context.Context, db *sql.DB) ([]appliedMigration, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT version, migration_id, checksum, dirty
		 FROM tc_schema_migrations
		 ORDER BY version`,
	)
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()

	var applied []appliedMigration
	for rows.Next() {
		var migration appliedMigration
		if err := rows.Scan(
			&migration.version,
			&migration.id,
			&migration.checksum,
			&migration.dirty,
		); err != nil {
			return nil, fmt.Errorf("scan migration ledger: %w", err)
		}
		applied = append(applied, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration ledger: %w", err)
	}
	return applied, nil
}

func validateAppliedMigrations(applied []appliedMigration, supported []Migration) error {
	for index, existing := range applied {
		if existing.dirty {
			return fmt.Errorf("%w: version %d (%s)", ErrDirtyMigration, existing.version, existing.id)
		}
		expectedVersion := index + 1
		if existing.version != expectedVersion {
			return fmt.Errorf(
				"%w: ledger version %d follows %d",
				ErrSchemaNewer,
				existing.version,
				expectedVersion-1,
			)
		}
		if index >= len(supported) {
			return fmt.Errorf("%w: found version %d", ErrSchemaNewer, existing.version)
		}
		expected := supported[index]
		if existing.id != expected.ID || existing.checksum != migrationChecksum(expected) {
			return fmt.Errorf(
				"%w: version %d (%s)",
				ErrChecksumDrift,
				existing.version,
				existing.id,
			)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	checksum := migrationChecksum(migration)
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO tc_schema_migrations
			(version, migration_id, checksum, dirty, applied_at)
		 VALUES (?, ?, ?, 1, NULL)`,
		migration.Version,
		migration.ID,
		checksum,
	); err != nil {
		return fmt.Errorf("mark migration %d dirty: %w", migration.Version, err)
	}

	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", migration.Version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	for statementIndex, statement := range migration.Statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf(
				"execute migration %d statement %d: %w",
				migration.Version,
				statementIndex,
				err,
			)
		}
	}
	result, err := transaction.ExecContext(
		ctx,
		`UPDATE tc_schema_migrations
		 SET dirty = 0, applied_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE version = ? AND dirty = 1`,
		migration.Version,
	)
	if err != nil {
		return fmt.Errorf("mark migration %d clean: %w", migration.Version, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read migration %d ledger update: %w", migration.Version, err)
	}
	if affected != 1 {
		return fmt.Errorf("mark migration %d clean: updated %d rows", migration.Version, affected)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", migration.Version, err)
	}
	committed = true
	return nil
}

func migrationChecksum(migration Migration) string {
	hash := sha256.New()
	writeChecksumPart(hash, strconv.Itoa(migration.Version))
	writeChecksumPart(hash, migration.ID)
	for _, statement := range migration.Statements {
		writeChecksumPart(hash, statement)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type checksumWriter interface {
	Write([]byte) (int, error)
}

func writeChecksumPart(writer checksumWriter, value string) {
	_, _ = writer.Write([]byte(strconv.Itoa(len(value))))
	_, _ = writer.Write([]byte{':'})
	_, _ = writer.Write([]byte(value))
	_, _ = writer.Write([]byte{'\n'})
}
