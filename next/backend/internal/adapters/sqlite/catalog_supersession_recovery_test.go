//go:build !windows

package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// Recovery exercises two selected process boundaries, not physical power loss.
// As with F01, capabilities never cross the helper serialization boundary:
// process recovery uses detached history; authorized replay uses retained exact
// trusted bindings and the real driver's durable-commit/lost-response boundary.
func supersessionRecoveryFixture(t *testing.T) supersessionFixture {
	t.Helper()
	f := newSupersessionFixture(t)
	input := f.command.Evidence[0].Input()
	id, err := evidence.ParseID(registrationIntegrationID(t, "obs"))
	if err != nil {
		t.Fatal(err)
	}
	input.ID = id
	second, err := domain.NewCatalogSupersessionEvidence(f.ingest, f.source, input)
	if err != nil {
		t.Fatal(err)
	}
	f.command.Evidence = append(f.command.Evidence, second)
	f.command.Support = append(f.command.Support, second.ID())
	return f
}

type supersessionRecoveryAccounting struct{ Bytes, Observations, Links, Events uint64 }

func supersessionRecoveryUsage(t *testing.T, owner *LifecycleOwner, content identity.ContentID) supersessionRecoveryAccounting {
	t.Helper()
	var usage supersessionUsage
	err := owner.WithinCatalogTransaction(t.Context(), func(repository app.CatalogRepository) error {
		r := repository.(*catalogRepository)
		var err error
		usage, err = r.supersessionUsage(t.Context(), content)
		if err != nil {
			return err
		}
		return r.registrationAccounting(t.Context(), content)
	})
	if err != nil {
		t.Fatal("actual combined accounting", err)
	}
	return supersessionRecoveryAccounting{usage.bytes, usage.observations, usage.links, usage.events}
}

// Independent expected charge from the contract's T/N column and coordinate
// rules. In particular each relation charges its coordinate, even when both
// relation values share one retained record. No SQLite/WAL byte-size oracle.
func supersessionRecoveryExpected(t *testing.T, op app.SupersessionOperation, result domain.CatalogSupersessionResult) supersessionRecoveryAccounting {
	t.Helper()
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalEncoding()
	if err != nil {
		t.Fatal(err)
	}
	textBytes := func(values ...string) uint64 {
		var n uint64
		for _, s := range values {
			n += 8 + uint64(len(s))
		}
		return n
	}
	c := op.Command
	n := uint64(len(raw)+len(encoded)) + textBytes(c.ID.String(), c.Content.String(), c.Old.ID().String(), c.New.ID().String(), c.Source.ID().String(), c.Source.Revision().String(), op.Capability.Reviewer().String()) + 9*8 + 2*32
	for _, relation := range c.Evidence {
		e := relation.Input()
		coordinate := e.Coordinate
		facts := coordinate.Artifact().Facts()
		n += textBytes(e.ID.String(), c.ID.String(), c.Content.String(), c.Old.ID().String(), c.New.ID().String(), c.Source.ID().String(), c.Source.Revision().String(), facts.ID.String(), e.AssertedOrigin, e.ObservedAt.UTC().Format(time.RFC3339Nano)) + 2*8
		n += textBytes(facts.ID.String(), facts.ParserRevision, facts.SourceRevision, coordinate.Locator()) + 32 + 4*8 + uint64(len(coordinate.Snapshot().Bytes()))
	}
	for _, id := range c.Support {
		n += textBytes(c.ID.String(), id.String())
	}
	return supersessionRecoveryAccounting{n, uint64(len(c.Evidence)), uint64(len(c.Support)), 1}
}

func supersessionRecoverySnapshot(t *testing.T, owner *LifecycleOwner, content identity.ContentID) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		Rows       map[string][][]any
		Accounting supersessionRecoveryAccounting
	}{catalogRecoveryRows(t, owner.database.db), supersessionRecoveryUsage(t, owner, content)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func supersessionRecoveryWrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSupersessionHelperProcessRecovery(t *testing.T) {
	for _, phase := range []string{"uncommitted", "durable-response-lost"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "supersession.sqlite")
			helper := startRestartHelperFor(t, path, "TestSupersessionRecoveryProcessHelper", "TC_SUPERSESSION_RECOVERY_PHASE="+phase, "TMPDIR="+filepath.Dir(path))
			if err := helper.waitUntilWriteIsPending(); err != nil {
				_ = helper.terminate()
				t.Fatalf("helper readiness: %v\n%s", err, helper.output())
			}
			if err := helper.terminate(); err != nil {
				t.Fatalf("helper termination: %v\n%s", err, helper.output())
			}
			status, ok := helper.command.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("missing actual SIGKILL", helper.command.ProcessState)
			}
			if phase == "uncommitted" {
				raw, err := os.ReadFile(path + ".deadline")
				deadline, parseErr := time.Parse(time.RFC3339Nano, string(raw))
				if err != nil || parseErr != nil || !time.Now().Before(deadline) {
					t.Fatal("kill did not precede transaction expiry", err, parseErr)
				}
			}
			commandRaw, err := os.ReadFile(path + ".command")
			if err != nil {
				t.Fatal(err)
			}
			command, _, err := domain.RehydrateCatalogSupersessionCommand(commandRaw, domain.CatalogPersistenceBindings{AllowUnbound: true})
			if err != nil {
				t.Fatal(err)
			}
			// Persisted command bytes are provenance and cannot mint a live grant.
			review, err := domain.NewReviewCapability(command.Source.Issuer(), command.Source)
			if err == nil {
				if _, err := domain.NewCatalogSupersessionCapability(review); err == nil {
					t.Fatal("detached process provenance granted authority")
				}
			}
			resultRaw, err := os.ReadFile(path + ".result")
			if err != nil {
				t.Fatal(err)
			}
			wantPath := path + ".before.json"
			count := 0
			if phase == "durable-response-lost" {
				wantPath = path + ".after.json"
				count = 1
			}
			want, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = owner.Close(context.Background()) })
				if got := supersessionRecoverySnapshot(t, owner, command.Content); !bytes.Equal(got, want) {
					t.Fatalf("reopen %d differs from complete %s row/accounting snapshot", attempt, phase)
				}
				reader := supersessionRecoveryReader(t, owner)
				history, err := reader.QuerySupersession(t.Context(), app.SupersessionQuery{Content: command.Content, Page: app.PageRequest{Limit: 128}})
				if err != nil || !history.Complete || len(history.Entries) != count || history.Inspected != uint64(count) || history.AggregateRevision.Uint64() != uint64(3+count) || history.SupersessionRevision.Uint64() != uint64(1+count) {
					t.Fatal("durable history readback", history, err)
				}
				if count == 1 && (!bytes.Equal(history.Entries[0].Command, commandRaw) || !bytes.Equal(history.Entries[0].Result, resultRaw)) {
					t.Fatal("durable canonical envelope changed")
				}
				page, err := catalogRead(t, owner, app.Query{Kind: app.GetVersion, Content: command.Content, Version: command.Old.ID(), Page: app.PageRequest{Limit: 1}})
				if err != nil || len(page.Entries) != 1 || page.Entries[0].Version != command.Old {
					t.Fatal("old tuple after kill", page, err)
				}
				expected := app.Current
				if count == 1 {
					expected = app.Superseded
				}
				if page.Entries[0].Supersession.Status != expected {
					t.Fatal("partial/absent edge misprojected", page)
				}
				if !bytes.Equal(supersessionRecoverySnapshot(t, owner, command.Content), want) {
					t.Fatal("readback changed recovered rows/accounting")
				}
				if err := verifyForeignKeys(t.Context(), owner.database.db); err != nil {
					t.Fatal(err)
				}
				if err := owner.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			calls, err := os.ReadFile(path + ".callback")
			if err != nil || string(calls) != "1" {
				t.Fatal("helper callback repeated", string(calls), err)
			}
			t.Log("actual SIGKILL; complete row/accounting and canonical readback across two fresh owners", phase)
		})
	}
}

func supersessionRecoveryReader(t *testing.T, owner *LifecycleOwner) app.SupersessionService {
	t.Helper()
	principal, err := domain.NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	source := newRegistrationIntegrationSource(t, principal, domain.CoreOperator)
	base, err := app.NewService(owner, source.source, source.review, source.space)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := domain.NewCatalogSupersessionCapability(source.review)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSupersessionService(base, cap)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSupersessionRecoveryProcessHelper(t *testing.T) {
	phase := os.Getenv("TC_SUPERSESSION_RECOVERY_PHASE")
	if os.Getenv(restartHelperEnvironment) != "1" || phase == "" {
		return
	}
	if phase != "uncommitted" && phase != "durable-response-lost" {
		t.Fatal("unknown recovery phase")
	}
	path := os.Getenv("TEDDYCLOUD_SQLITE_RESTART_PATH")
	f := supersessionRecoveryFixture(t)
	op := f.op(f.command)
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		t.Fatal(err)
	}
	supersessionRecoveryWrite(t, path+".command", raw)
	// Commit a separate expected database through the real service. The process
	// under test starts from its precommit backup, never its own recovered rows.
	if _, err := f.owner.database.CreateBackup(t.Context(), path, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	supersessionRecoveryWrite(t, path+".before.json", supersessionRecoverySnapshot(t, f.owner, f.command.Content))
	result, err := f.sup.SupersedeVersion(t.Context(), f.command)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalEncoding()
	if err != nil {
		t.Fatal(err)
	}
	supersessionRecoveryWrite(t, path+".result", encoded)
	if got, want := supersessionRecoveryUsage(t, f.owner, f.command.Content), supersessionRecoveryExpected(t, op, result); got != want {
		t.Fatal("independent expected accounting", got, want)
	}
	supersessionRecoveryWrite(t, path+".after.json", supersessionRecoverySnapshot(t, f.owner, f.command.Content))
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.owner, err = OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(3, "supersession-recovery-ready")
	if ready == nil {
		t.Fatal("missing readiness pipe")
	}
	defer ready.Close()
	park := func() {
		if _, err := ready.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		<-time.After(time.Hour)
		t.Fatal("helper not terminated")
	}
	err = f.owner.WithinCatalogTransaction(t.Context(), func(repository app.CatalogRepository) error {
		marker, err := os.OpenFile(path+".callback", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if _, err := marker.WriteString("1"); err != nil {
			_ = marker.Close()
			return err
		}
		if err := marker.Close(); err != nil {
			return err
		}
		got, err := repository.(app.SupersessionRepository).CommitSupersession(t.Context(), op)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, result) {
			t.Fatal("staged result differs from independent commit")
		}
		r := repository.(*catalogRepository)
		// Verify the full edge, both evidence/support rows, provenance and accounting
		// exist on the leased transaction before signaling the parent to kill it.
		usage, err := r.supersessionUsage(t.Context(), f.command.Content)
		if err != nil {
			return err
		}
		if usage.events != 1 || usage.observations != 2 || usage.links != 2 {
			t.Fatal("staged rows incomplete", usage)
		}
		if err := r.registrationAccounting(t.Context(), f.command.Content); err != nil {
			return err
		}
		if phase == "uncommitted" {
			deadline, ok := r.ctx.Deadline()
			if !ok {
				t.Fatal("unbounded transaction")
			}
			supersessionRecoveryWrite(t, path+".deadline", []byte(deadline.Format(time.RFC3339Nano)))
			park()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	park() // Real transaction returned after durable commit; response is withheld.
}

func TestSupersessionDurableResponseRecovery(t *testing.T) {
	for _, mode := range []string{"before-commit", "after-commit", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f := supersessionRecoveryFixture(t)
			before := catalogRecoveryRows(t, f.owner.database.db)
			fault := &catalogCommitFault{mode: mode}
			catalogInstallFault(t, f.owner, fault) // Wraps the real SQLite driver's commit.
			got, err := f.sup.SupersedeVersion(t.Context(), f.command)
			if err != app.ErrIndeterminate || !reflect.DeepEqual(got, domain.CatalogSupersessionResult{}) || fault.commits.Load() != 1 {
				t.Fatal("uncertain response/commit count", got, err, fault.commits.Load())
			}
			config := f.owner.config
			if err := f.owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.owner, err = OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.owner.Close(context.Background()) })
			raw, err := f.source.PersistenceEncoding()
			if err != nil {
				t.Fatal(err)
			}
			source, err := domain.RehydrateSourceRegistration(raw, domain.CatalogPersistenceBindings{Sources: []domain.SourceRegistration{f.source}})
			if err != nil || source != f.source {
				t.Fatal("trusted exact source rebind", err)
			}
			f.bind(t)
			f.sup, err = app.NewSupersessionService(f.base, f.cap)
			if err != nil {
				t.Fatal(err)
			}
			recovered := catalogRecoveryRows(t, f.owner.database.db)
			result, found, err := f.sup.ReadSupersession(t.Context(), f.command)
			if err != nil || found != (mode != "before-commit") {
				t.Fatal("uncertain outcome readback", found, err)
			}
			if mode == "before-commit" {
				if !reflect.DeepEqual(before, recovered) || supersessionRecoveryUsage(t, f.owner, f.command.Content) != (supersessionRecoveryAccounting{}) {
					t.Fatal("precommit rollback leaked rows/accounting")
				}
				result, err = f.sup.SupersedeVersion(t.Context(), f.command)
				if err != nil {
					t.Fatal("explicit exact retry after absent readback", err)
				}
			}
			if result.Old != f.command.Old || result.New != f.command.New || result.AggregateRevision.Uint64() != 4 || result.SupersessionRevision.Uint64() != 2 || result.OrderRevision.Uint64() != 3 {
				t.Fatal("original result envelope", result)
			}
			if usage, want := supersessionRecoveryUsage(t, f.owner, f.command.Content), supersessionRecoveryExpected(t, f.op(f.command), result); usage != want {
				t.Fatal("durable/retried accounting", usage, want)
			}
			retained := supersessionRecoverySnapshot(t, f.owner, f.command.Content)
			for range 2 {
				read, found, err := f.sup.ReadSupersession(t.Context(), f.command)
				if err != nil || !found || !reflect.DeepEqual(read, result) {
					t.Fatal("exact retained readback", found, err)
				}
				replay, err := f.sup.SupersedeVersion(t.Context(), f.command)
				if err != nil || !reflect.DeepEqual(replay, result) {
					t.Fatal("exact original replay", replay, err)
				}
			}
			changed := f.command
			changed.Reason = "changed retained command"
			for _, readOnly := range []bool{true, false} {
				if readOnly {
					_, _, err = f.sup.ReadSupersession(t.Context(), changed)
				} else {
					_, err = f.sup.SupersedeVersion(t.Context(), changed)
				}
				if !errors.Is(err, app.ErrCommandConflict) {
					t.Fatal("changed-key replay accepted", err)
				}
			}
			if !bytes.Equal(retained, supersessionRecoverySnapshot(t, f.owner, f.command.Content)) {
				t.Fatal("replay/conflict leaked or charged state")
			}
			if err := verifyForeignKeys(t.Context(), f.owner.database.db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
