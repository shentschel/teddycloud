package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

func TestTagAuditGeneratedRegistrationCommands(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	s := applicationtag.New(owner)
	for n := 0; n < 8; n++ {
		var raw [8]byte
		for i := range raw {
			raw[i] = byte(n*37 + i*19)
		}
		if n == 0 {
			raw = [8]byte{}
		}
		if n == 1 {
			raw = [8]byte{255, 255, 255, 255, 255, 255, 255, 255}
		}
		initial := testRegistryTag(t, byte('0'+n), domaintag.UIDFromBytes(raw))
		uid := applicationtag.OptionalText{Present: true, Value: strings.ToLower(initial.UID().String())}
		ruid := applicationtag.OptionalText{Present: true, Value: strings.ToLower(initial.RUID().String())}
		inputs := []applicationtag.PhysicalIdentityInput{{UID: uid}, {RUID: ruid}, {UID: uid, RUID: ruid}}
		current, err := s.Register(t.Context(), applicationtag.RegisterCommand{OpaqueID: initial.ID().String(), Physical: inputs[n%3]})
		if err != nil || !current.Equal(initial) {
			t.Fatal("registration", err)
		}
		for step := 0; step < 8; step++ {
			key := domaintag.FactKey(step % 4)
			c := applicationtag.MetadataCommand{TagID: current.ID(), Change: domaintag.MetadataChange{ExpectedRevision: current.Revision(), Key: key, Observations: []evidence.Observation{tagMetadataObservation(t, current, n*100+step, key, []string{"true", "false"}[step/4])}}}
			before := current
			current, err = s.UpdateMetadata(t.Context(), c)
			if err != nil || current.ID() != initial.ID() || current.UID() != initial.UID() || current.Revision() != before.Revision()+1 {
				t.Fatal("state command", err)
			}
			got, err := s.UpdateMetadata(t.Context(), c)
			if err != nil || !got.Equal(current) {
				t.Fatal("durable replay", err)
			}
			stale := c
			stale.Change.Observations = []evidence.Observation{tagMetadataObservation(t, current, 1000+n*100+step, key, "true")}
			_, err = s.UpdateMetadata(t.Context(), stale)
			assertAuditErrorTree(t, err, applicationtag.ErrRevisionConflict)
			for _, input := range inputs {
				got, err := s.Register(t.Context(), applicationtag.RegisterCommand{OpaqueID: initial.ID().String(), Physical: input})
				if err != nil || !got.Equal(current) {
					t.Fatal("registration replay changed retained state", err)
				}
			}
		}
		if current.Metadata().ProtocolValid() != domaintag.MetadataConflict || current.Metadata().Claimed() != domaintag.MetadataConflict || current.Metadata().CloudAuth() != domaintag.MetadataConflict || current.Metadata().Owned() != domaintag.MetadataConflict {
			t.Fatal("evidence conflict lost")
		}
		collision := applicationtag.RegisterCommand{OpaqueID: testRegistryTag(t, 'z', initial.UID()).ID().String(), Physical: inputs[0]}
		_, err = s.Register(t.Context(), collision)
		assertAuditErrorTree(t, err, applicationtag.ErrIdentityConflict)
		got, err := s.FindByID(t.Context(), initial.ID().String())
		if err != nil || !got.Equal(current) {
			t.Fatal("rejected command changed durable aggregate", err)
		}
	}
}

// Inspect every wrapped/joined node, not only Error() or the first Unwrap().
func assertAuditErrorTree(t *testing.T, err, want error) {
	t.Helper()
	assertTagError(t, err, want)
	queue := []error{err}
	for visited := 0; len(queue) > 0; visited++ {
		if visited >= 32 {
			t.Fatal("unbounded error chain")
		}
		node := queue[0]
		queue = queue[1:]
		if node == nil {
			continue
		}
		text := strings.ToLower(fmt.Sprintf("%v %+v %#v", node, node, node))
		for _, forbidden := range []string{"select ", "insert ", "tc_", "sqlite", "sql.", "modernc", "/private/", "c:\\", "ab:cd:ef", "896745", "synthetic-secret-canary"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("error tree leaked %q", forbidden)
			}
		}
		switch v := node.(type) {
		case interface{ Unwrap() []error }:
			queue = append(queue, v.Unwrap()...)
		case interface{ Unwrap() error }:
			queue = append(queue, v.Unwrap())
		}
	}
}

func TestTagAuditErrorTrees(t *testing.T) {
	private := errors.New("SELECT tc_tags modernc sqlite /private/database C:\\private AB:CD:EF:01:23:45:67:89 8967452301EFCDAB synthetic-secret-canary")
	for _, sentinel := range []error{applicationtag.ErrInvalidInput, applicationtag.ErrIdentityConflict, applicationtag.ErrRevisionConflict, applicationtag.ErrEvidenceConflict, applicationtag.ErrLimitExceeded, applicationtag.ErrRepositoryContention, applicationtag.ErrRepositoryUnavailable, context.Canceled, context.DeadlineExceeded} {
		err := fmt.Errorf("private wrapper: %w", errors.Join(private, sql.ErrTxDone, fmt.Errorf("nested: %w", sentinel)))
		assertAuditErrorTree(t, tagBoundaryError(t.Context(), err), sentinel)
	}
	owner := openLifecycleApplicationOwner(t)
	s := applicationtag.New(owner)
	value := registerMetadataTag(t, s)
	if _, err := owner.database.db.ExecContext(t.Context(), `CREATE TRIGGER audit_failure BEFORE UPDATE ON tc_tags BEGIN SELECT RAISE(ABORT,'SELECT tc_tags /private/database synthetic-secret-canary'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{TagID: value.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Observations: []evidence.Observation{tagMetadataObservation(t, value, 1, domaintag.CloudAuth, "true")}}})
	assertAuditErrorTree(t, err, applicationtag.ErrRepositoryUnavailable)
	got, err := s.FindByID(t.Context(), value.ID().String())
	if err != nil || !got.Equal(value) {
		t.Fatal("failed command changed history", err)
	}
}

func TestTagAuditMixedRestoreRevokesHandles(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	s := applicationtag.New(owner) // Keep this service across every handle switch.
	value := registerMetadataTag(t, s)
	content := testContent(t, "audit snapshot", true)
	var oldTag applicationtag.MetadataRepository
	var oldContent applicationcatalog.ContentRepository
	if err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { oldTag = r.(applicationtag.MetadataRepository); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		oldContent = r
		return r.Save(t.Context(), content)
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "audit.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		change := domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Observations: []evidence.Observation{tagMetadataObservation(t, value, round, domaintag.CloudAuth, "true")}}
		next, err := s.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{TagID: value.ID(), Change: change})
		if err != nil {
			t.Fatal(err)
		}
		assertRecoveryContent(t, owner, content)
		if err := owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite")); err != nil {
			t.Fatal(err)
		}
		assertAuditErrorTree(t, oldTag.CompareAndSwap(t.Context(), 1, next), applicationtag.ErrRepositoryUnavailable)
		assertAuditErrorTree(t, oldTag.Insert(t.Context(), value), applicationtag.ErrRepositoryUnavailable)
		_, _, err = oldTag.FindByUID(t.Context(), value.UID())
		assertAuditErrorTree(t, err, applicationtag.ErrRepositoryUnavailable)
		if err := oldContent.Save(t.Context(), content); !errors.Is(err, applicationcatalog.ErrRepositoryUnavailable) {
			t.Fatal("old Content handle usable", err)
		}
		got, err := s.FindByRUID(t.Context(), value.RUID().String())
		if err != nil || !got.Equal(value) {
			t.Fatal("service retained old database", err)
		}
		assertRecoveryContent(t, owner, content)
	}
}
