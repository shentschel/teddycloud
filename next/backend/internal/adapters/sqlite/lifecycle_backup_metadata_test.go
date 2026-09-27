package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	sqliteDriver "modernc.org/sqlite"
)

func TestLifecycleUpgradePreservesRecoveryMetadata(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[broken], func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "source.sqlite")
			backupPath := filepath.Join(directory, "pre-upgrade.sqlite")
			createVersionOneDatabase(t, path)
			migrations := testMigrations()
			if broken {
				migrations[1].Statements = []string{"THIS IS NOT SQL"}
			}
			owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path, UpgradeBackupPath: backupPath}, migrations)
			var snapshot BackupFile
			if broken {
				var failure *UpgradeError
				if !errors.As(err, &failure) {
					t.Fatalf("upgrade error lost recovery metadata: %v", err)
				}
				var raw *sqliteDriver.Error
				if errors.As(err, &raw) {
					t.Fatalf("lifecycle open leaked driver: %v", err)
				}
				snapshot = failure.Backup
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close(t.Context())
				var found bool
				snapshot, found, err = owner.UpgradeBackup(t.Context())
				if err != nil || !found {
					t.Fatalf("upgrade metadata unavailable: %t %v", found, err)
				}
			}
			if snapshot.SchemaVersion != 1 {
				t.Fatalf("snapshot version %d, want 1", snapshot.SchemaVersion)
			}
			if err := VerifyBackup(t.Context(), snapshot, migrations); err != nil {
				t.Fatal(err)
			}
		})
	}
}
