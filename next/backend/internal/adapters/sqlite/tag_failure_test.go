//go:build !windows

package sqlite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

const tagRestartHelperEnvironment = "TEDDYCLOUD_TAG_RESTART_HELPER"

type lostTagResponse struct {
	delegate applicationtag.Transactor
	lost     bool
}

func (transaction *lostTagResponse) WithinTagTransaction(
	ctx context.Context,
	operation func(applicationtag.TagRepository) error,
) error {
	err := transaction.delegate.WithinTagTransaction(ctx, operation)
	if err == nil && !transaction.lost {
		transaction.lost = true
		return applicationtag.ErrRepositoryUnavailable
	}
	return err
}

func TestTagUncertainOutcome(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	reliable := applicationtag.New(owner)
	initial := registerMetadataTag(t, reliable)
	observation := tagMetadataObservation(t, initial, 1, domaintag.CloudAuth, "true")
	command := applicationtag.MetadataCommand{
		TagID: initial.ID(),
		Change: domaintag.MetadataChange{
			ExpectedRevision: initial.Revision(),
			Key:              domaintag.CloudAuth,
			Observations:     []evidence.Observation{observation},
		},
	}

	uncertain := applicationtag.New(&lostTagResponse{delegate: owner})
	if _, err := uncertain.UpdateMetadata(t.Context(), command); !errors.Is(err, applicationtag.ErrRepositoryUnavailable) {
		t.Fatalf("lost response = %v", err)
	}
	readback, err := reliable.FindByID(t.Context(), initial.ID().String())
	if err != nil || readback.Revision() != initial.Revision()+1 {
		t.Fatalf("uncertain outcome readback = revision %d, %v", readback.Revision(), err)
	}
	replayed, err := reliable.UpdateMetadata(t.Context(), command)
	if err != nil || !replayed.Equal(readback) {
		t.Fatalf("exact replay after lost response = %v", err)
	}
	var observations int
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_tag_observations`).Scan(&observations); err != nil || observations != 1 {
		t.Fatalf("replay duplicated evidence: %d, %v", observations, err)
	}
}

func TestTagCorruptRecord(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	service := applicationtag.New(owner)
	value := registerMetadataTag(t, service)
	command := applicationtag.MetadataCommand{
		TagID: value.ID(),
		Change: domaintag.MetadataChange{
			ExpectedRevision: value.Revision(),
			Key:              domaintag.CloudAuth,
			Observations: []evidence.Observation{
				tagMetadataObservation(t, value, 1, domaintag.CloudAuth, "true"),
			},
		},
	}
	if _, err := service.UpdateMetadata(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.database.db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.database.db.ExecContext(t.Context(), `UPDATE tc_tag_observations SET review=99`); err != nil {
		t.Fatal(err)
	}
	got, err := service.FindByID(t.Context(), value.ID().String())
	assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
	if !got.ID().IsZero() {
		t.Fatal("corrupt storage returned a partial Tag")
	}
}

func TestTagRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tag-restart.sqlite")
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	committed := registerMetadataTag(t, applicationtag.New(owner))
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	helper := startTagRestartHelper(t, path)
	if err := helper.waitUntilPending(); err != nil {
		_ = helper.terminate()
		t.Fatalf("wait for Tag helper: %v\n%s", err, helper.output())
	}
	if err := helper.terminate(); err != nil {
		t.Fatalf("terminate Tag helper: %v\n%s", err, helper.output())
	}

	reopened, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	service := applicationtag.New(reopened)
	got, err := service.FindByID(t.Context(), committed.ID().String())
	if err != nil || !got.Equal(committed) {
		t.Fatalf("committed Tag after restart: %v", err)
	}
	uncommitted := testRegistryTag(t, '1', domaintag.UIDFromBytes([8]byte{1}))
	if _, err := service.FindByID(t.Context(), uncommitted.ID().String()); !errors.Is(err, applicationtag.ErrTagNotFound) {
		t.Fatalf("uncommitted Tag survived restart: %v", err)
	}
}

func TestTagRestartCrashHelper(t *testing.T) {
	if os.Getenv(tagRestartHelperEnvironment) != "1" {
		return
	}
	path := os.Getenv("TEDDYCLOUD_TAG_RESTART_PATH")
	ready := os.NewFile(3, "tag-restart-ready")
	if path == "" || ready == nil {
		t.Fatal("Tag restart helper configuration missing")
	}
	defer ready.Close()
	database, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	value := testRegistryTag(t, '1', domaintag.UIDFromBytes([8]byte{1}))
	if err := database.withinTagTransaction(t.Context(), func(repository applicationtag.TagRepository) error {
		if err := repository.Insert(t.Context(), value); err != nil {
			return err
		}
		if _, err := ready.Write([]byte{1}); err != nil {
			return err
		}
		<-time.After(time.Hour)
		return errors.New("Tag restart helper was not terminated")
	}); err != nil {
		t.Fatal(err)
	}
}

type tagRestartHelper struct {
	command *exec.Cmd
	ready   *os.File
	wait    chan error
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	done    bool
}

func startTagRestartHelper(t *testing.T, path string) *tagRestartHelper {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	helper := &tagRestartHelper{ready: reader, wait: make(chan error, 1)}
	helper.command = exec.Command(executable, "-test.run=^TestTagRestartCrashHelper$", "-test.timeout=30s")
	helper.command.Env = append(os.Environ(), tagRestartHelperEnvironment+"=1", "TEDDYCLOUD_TAG_RESTART_PATH="+path)
	helper.command.ExtraFiles = []*os.File{writer}
	helper.command.Stdout = &helper.stdout
	helper.command.Stderr = &helper.stderr
	if err := helper.command.Start(); err != nil {
		reader.Close()
		writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	go func() { helper.wait <- helper.command.Wait() }()
	t.Cleanup(func() {
		if err := helper.terminate(); err != nil {
			t.Errorf("cleanup Tag helper: %v\n%s", err, helper.output())
		}
	})
	return helper
}

func (helper *tagRestartHelper) waitUntilPending() error {
	buffer := []byte{0}
	done := make(chan error, 1)
	go func() {
		_, err := helper.ready.Read(buffer)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil || buffer[0] != 1 {
			return fmt.Errorf("read readiness: %w", err)
		}
		return nil
	case err := <-helper.wait:
		helper.done = true
		return fmt.Errorf("helper exited early: %w", err)
	case <-time.After(5 * time.Second):
		return errors.New("Tag helper readiness timeout")
	}
}

func (helper *tagRestartHelper) terminate() error {
	if helper.done {
		return nil
	}
	helper.done = true
	_ = helper.ready.Close()
	if err := helper.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-helper.wait:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("Tag helper termination timeout")
	}
}

func (helper *tagRestartHelper) output() string {
	return helper.stdout.String() + helper.stderr.String()
}
