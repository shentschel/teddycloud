//go:build !windows

package sqlite

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

const restartHelperEnvironment = "TEDDYCLOUD_SQLITE_RESTART_HELPER"

func TestRestartAfterInterruptedUncommittedWrite(t *testing.T) {
	path := t.TempDir() + "/restart.sqlite"
	committed := testContent(t, "Committed content", true)

	database, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatalf("open initial database: %v", err)
	}
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), committed)
	}); err != nil {
		t.Fatalf("save committed content: %v", err)
	}
	versionBefore := currentVersion(t, database)
	ledgerBefore := migrationLedger(t, database)
	if err := database.Close(); err != nil {
		t.Fatalf("close initial database: %v", err)
	}

	helper := startRestartHelper(t, path)
	if err := helper.waitUntilWriteIsPending(); err != nil {
		_ = helper.terminate()
		t.Fatalf("wait for helper write: %v\n%s", err, helper.output())
	}
	if err := helper.terminate(); err != nil {
		t.Fatalf("terminate helper: %v\n%s", err, helper.output())
	}

	reopened, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatalf("reopen database after interrupted write: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened database: %v", err)
		}
	})

	if got := currentVersion(t, reopened); got != versionBefore {
		t.Fatalf("schema version after restart = %d, want %d", got, versionBefore)
	}
	if got := migrationLedger(t, reopened); !reflect.DeepEqual(got, ledgerBefore) {
		t.Fatalf("migration ledger after restart = %#v, want %#v", got, ledgerBefore)
	}

	if err := reopened.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		got, found, err := repository.FindByID(t.Context(), committed.ID())
		if err != nil {
			return err
		}
		if !found {
			return errors.New("committed content missing after restart")
		}
		if !reflect.DeepEqual(got, committed) {
			return fmt.Errorf("content after restart = %#v, want %#v", got, committed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRestartCrashHelper(t *testing.T) {
	if os.Getenv(restartHelperEnvironment) != "1" {
		return
	}
	path := os.Getenv("TEDDYCLOUD_SQLITE_RESTART_PATH")
	if path == "" {
		t.Fatal("restart helper database path is empty")
	}
	ready := os.NewFile(3, "restart-helper-ready")
	if ready == nil {
		t.Fatal("restart helper readiness pipe is unavailable")
	}
	defer ready.Close()

	database, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatalf("open helper database: %v", err)
	}
	uncommitted := testContent(t, "Uncommitted replacement", false)
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		if err := repository.Save(t.Context(), uncommitted); err != nil {
			return err
		}
		if _, err := ready.Write([]byte{1}); err != nil {
			return fmt.Errorf("signal pending write: %w", err)
		}
		<-time.After(time.Hour)
		return errors.New("restart helper was not terminated")
	}); err != nil {
		t.Fatalf("hold uncommitted helper write: %v", err)
	}
}

func migrationLedger(t *testing.T, database *Database) []appliedMigration {
	t.Helper()
	ledger, err := readAppliedMigrations(t.Context(), database.db)
	if err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	return ledger
}

type restartHelper struct {
	command *exec.Cmd
	ready   *os.File
	wait    chan error
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	done    bool
}

func startRestartHelper(t *testing.T, path string) *restartHelper {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("find test executable: %v", err)
	}
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create helper readiness pipe: %v", err)
	}

	helper := &restartHelper{
		ready: readyReader,
		wait:  make(chan error, 1),
	}
	helper.command = exec.Command(
		executable,
		"-test.run=^TestSQLiteRestartCrashHelper$",
		"-test.timeout=30s",
	)
	helper.command.Env = append(
		os.Environ(),
		restartHelperEnvironment+"=1",
		"TEDDYCLOUD_SQLITE_RESTART_PATH="+path,
	)
	helper.command.ExtraFiles = []*os.File{readyWriter}
	helper.command.Stdout = &helper.stdout
	helper.command.Stderr = &helper.stderr
	if err := helper.command.Start(); err != nil {
		readyReader.Close()
		readyWriter.Close()
		t.Fatalf("start restart helper: %v", err)
	}
	if err := readyWriter.Close(); err != nil {
		_ = helper.command.Process.Kill()
		_ = helper.command.Wait()
		readyReader.Close()
		t.Fatalf("close parent readiness writer: %v", err)
	}
	go func() {
		helper.wait <- helper.command.Wait()
	}()
	t.Cleanup(func() {
		if err := helper.terminate(); err != nil {
			t.Errorf("cleanup restart helper: %v\n%s", err, helper.output())
		}
	})
	return helper
}

func (helper *restartHelper) waitUntilWriteIsPending() error {
	ready := make(chan error, 1)
	go func() {
		var signal [1]byte
		_, err := io.ReadFull(helper.ready, signal[:])
		if err == nil && signal[0] != 1 {
			err = fmt.Errorf("unexpected readiness signal %d", signal[0])
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("restart helper readiness timed out")
	}
}

func (helper *restartHelper) terminate() error {
	if helper == nil || helper.done {
		return nil
	}
	helper.done = true
	if helper.ready != nil {
		_ = helper.ready.Close()
	}
	if err := helper.command.Process.Kill(); err != nil {
		return fmt.Errorf("kill restart helper: %w", err)
	}
	select {
	case err := <-helper.wait:
		if err == nil {
			return errors.New("restart helper exited successfully after kill")
		}
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("restart helper did not exit after kill")
	}
}

func (helper *restartHelper) output() string {
	return "stdout:\n" + helper.stdout.String() + "stderr:\n" + helper.stderr.String()
}
