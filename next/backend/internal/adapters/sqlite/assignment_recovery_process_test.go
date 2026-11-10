//go:build !windows

package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const (
	assignmentBeforeCommit = "before-commit"
	assignmentAfterCommit  = "after-commit-before-response"
)

type assignmentCrashWitness struct {
	Phase    string
	Deadline time.Time
	Events   json.RawMessage
}

func TestAssignmentHelperProcessRecovery(t *testing.T) {
	for _, phase := range []string{assignmentBeforeCommit, assignmentAfterCommit} {
		t.Run(phase, func(t *testing.T) {
			f := newAssignmentRepositoryFixture(t)
			owner, service, path := assignmentActivateOwner(t, f)
			initial := f.command(t, 1, app.Assign, 0)
			initial.URI = app.OptionalURI{Present: true, Value: "HTTPS://Catalog.Example:443/a%62"}
			first := f.execute(t, initial)
			command := f.command(t, 2, app.RecordClassification, 1)
			want := app.Result{CommandID: command.CommandID, AssignmentID: f.id, TagID: f.tag, Revision: 2, PreviousCommand: first.CommandID}
			query := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: initial.EffectiveAt}, Limit: 3}
			beforeHistory := assignmentHistoryPage(t, service, query, first)
			beforeRows := registrationSchemaRows(t, owner.database.db)
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}

			helper := startRestartHelperFor(t, path, "TestAssignmentRecoveryProcessHelper",
				"TC_ASSIGNMENT_RECOVERY_PHASE="+phase, "TC_ASSIGNMENT_RECOVERY_TAG="+f.tag.String())
			readyErr := helper.waitUntilWriteIsPending()
			if err := helper.terminate(); err != nil {
				t.Fatalf("terminate helper: %v (readiness: %v)", err, readyErr)
			}
			reapedAt := time.Now()
			if readyErr != nil {
				t.Fatalf("helper readiness: %v\n%s", readyErr, helper.output())
			}
			status, ok := helper.command.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatalf("expected SIGKILL, got %v\n%s", helper.command.ProcessState, helper.output())
			}
			raw, err := os.ReadFile(path + ".assignment-boundary")
			if err != nil {
				t.Fatal(err)
			}
			var witness assignmentCrashWitness
			if err := json.Unmarshal(raw, &witness); err != nil {
				t.Fatal(err)
			}
			if witness.Phase != phase || !reapedAt.Before(witness.Deadline) {
				t.Fatalf("helper reaped at %s, witness %q with deadline %s", reapedAt, witness.Phase, witness.Deadline)
			}

			reopened, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close(context.Background()) })
			service = app.New(reopened)
			if err := verifyForeignKeys(t.Context(), reopened.database.db); err != nil {
				t.Fatal(err)
			}
			assignmentAssertReceipt(t, service, initial, first, true)
			if phase == assignmentBeforeCommit {
				assignmentAssertRows(t, reopened.database.db, beforeRows)
				assignmentAssertReceipt(t, service, command, app.Result{}, false)
				if got := assignmentHistoryPage(t, service, query, first); !reflect.DeepEqual(got, beforeHistory) {
					t.Fatal("uncommitted write changed retained history")
				}
				t.Log("reaped SIGKILL with staged rows before commit; reopen retained only the baseline")
				return
			}

			afterRows := registrationSchemaRows(t, reopened.database.db)
			afterHistory := assignmentHistoryPage(t, service, query, first, want)
			if afterHistory.HeadRevision != want.Revision || afterHistory.KnowledgeRevision != want.Revision ||
				afterHistory.Next != "" || !reflect.DeepEqual(afterHistory.Events[0], beforeHistory.Events[0]) ||
				!bytes.Equal(assignmentMarshal(t, afterHistory.Events), witness.Events) {
				t.Fatal("reopened history differs from the exact staged ledger")
			}
			assignmentAssertReceipt(t, service, command, want, true)
			if got, err := service.Execute(t.Context(), command); err != nil || got != want {
				t.Fatalf("authorized same-ID replay: %+v, %v", got, err)
			}
			if got := assignmentHistoryPage(t, service, query, first, want); !reflect.DeepEqual(got, afterHistory) {
				t.Fatal("replay changed retained history")
			}
			assignmentAssertRows(t, reopened.database.db, afterRows)
			t.Log("reaped SIGKILL after commit before response; exact history and receipt survived, replay left all rows unchanged")
		})
	}
}

func TestAssignmentRecoveryProcessHelper(t *testing.T) {
	if os.Getenv(restartHelperEnvironment) != "1" {
		return
	}
	phase := os.Getenv("TC_ASSIGNMENT_RECOVERY_PHASE")
	if phase != assignmentBeforeCommit && phase != assignmentAfterCommit {
		t.Fatalf("invalid helper phase %q", phase)
	}
	path := os.Getenv("TEDDYCLOUD_SQLITE_RESTART_PATH")
	tag, err := identity.ParseTagID(os.Getenv("TC_ASSIGNMENT_RECOVERY_TAG"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	service := app.New(owner)
	view, err := service.GetAssignment(t.Context(), app.EffectiveQuery{TagID: tag, At: time.Now().UTC()})
	if err != nil || !view.AggregatePresent || view.Desired.HeadRevision != 1 || view.Desired.State != d.Assigned {
		t.Fatalf("load helper baseline: %+v, %v", view, err)
	}
	f := assignmentRepositoryFixture{db: owner.database, id: view.Desired.AssignmentID, tag: tag, target: view.Desired.Target}
	command := f.command(t, 2, app.RecordClassification, 1)
	ready := os.NewFile(3, "assignment-recovery-ready")
	defer ready.Close()
	park := func(ctx context.Context, witness assignmentCrashWitness) {
		if err := os.WriteFile(path+".assignment-boundary", assignmentMarshal(t, witness), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ready.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		<-ctx.Done()
		t.Fatal("helper was not killed before its transaction deadline")
	}

	service = app.New(assignmentTransactorFunc(func(ctx context.Context, run func(app.Port) error) error {
		witness := assignmentCrashWitness{Phase: phase}
		err := owner.WithinAssignmentTransaction(ctx, func(port app.Port) error {
			if err := run(port); err != nil {
				return err
			}
			// Reconstruct from the leased SQLite transaction after the service
			// append, including the ledger, projection, usage and command receipt.
			snapshot, found, err := port.Load(ctx, tag)
			if err != nil || !found || len(snapshot.Events) != 2 {
				t.Fatalf("staged ledger: found %t, events %d, error %v", found, len(snapshot.Events), err)
			}
			want := app.Result{CommandID: command.CommandID, AssignmentID: f.id, TagID: tag, Revision: 2, PreviousCommand: snapshot.Events[0].Result.CommandID}
			event, found, err := port.LookupCommand(ctx, command.CommandID)
			if err != nil || !found || event.Result != want || !reflect.DeepEqual(event, snapshot.Events[1]) {
				t.Fatalf("staged receipt: %+v, found %t, error %v", event.Result, found, err)
			}
			witness.Events = assignmentMarshal(t, []app.HistoryEntry{{Event: snapshot.Events[0]}, {Event: event}})
			witness.Deadline, found = port.(*assignmentTransactionPort).ctx.Deadline()
			if !found || ctx.Err() != nil {
				t.Fatal("missing live transaction deadline")
			}
			if phase == assignmentBeforeCommit {
				park(ctx, witness)
			}
			return nil
		})
		if err != nil {
			return err
		}
		// The real owner has finalized SQLite. Execute is still waiting for
		// its transactor to return, so the caller cannot receive the result.
		park(ctx, witness)
		return nil
	}))
	got, err := service.Execute(t.Context(), command)
	t.Fatalf("helper returned to the service caller: %+v, %v", got, err)
}
