package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	assignmentapp "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	tagapp "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	catalogdomain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	tag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	"reflect"
	"testing"
)

func eventTag(t *testing.T, n int) tag.Tag {
	t.Helper()
	id, err := identity.ParseTagID(catalogSchemaID("tag", n))
	if err != nil {
		t.Fatal(err)
	}
	var uid [8]byte
	binary.BigEndian.PutUint64(uid[:], uint64(n))
	v, err := tag.NewTag(id, tag.UIDFromBytes(uid))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestEventCommitAtomicity(t *testing.T) {
	for _, family := range []string{"tag", "tag-metadata", "import", "content-reference", "unmanaged", "registration", "adoption", "review", "order", "supersession", "assignment"} {
		t.Run(family, func(t *testing.T) {
			var o *LifecycleOwner
			var run func() error
			switch family {
			case "tag":
				o, _ = eventOwner(t)
				run = func() error {
					return o.WithinTagTransaction(t.Context(), func(r tagapp.TagRepository) error { return r.Insert(t.Context(), eventTag(t, 1)) })
				}
			case "tag-metadata":
				o, _ = eventOwner(t)
				service := tagapp.New(o)
				value := registerMetadataTag(t, service)
				observation := tagMetadataObservation(t, value, 1, tag.FactKey(0), "true")
				command := tagapp.MetadataCommand{TagID: value.ID(), Change: tag.MetadataChange{ExpectedRevision: value.Revision(), Key: 0, Observations: []evidence.Observation{observation}}}
				run = func() error { _, err := service.UpdateMetadata(t.Context(), command); return err }
			case "import":
				owner, _, _, service := importFixture(t)
				o = owner
				report := generatedImport(t)
				batch, _ := catalogdomain.NewBatchID()
				run = func() error { _, err := service.Import(t.Context(), batch, report); return err }
			case "content-reference":
				o = blobOwner(t)
				run = func() error { _, err := blobRecord(t, o, blobKey(t, 1), blobCommand(t, 1, false)); return err }
			case "unmanaged":
				o, _ = eventOwner(t)
				run = func() error {
					return o.WithinTransaction(t.Context(), func(r catalogapp.ContentRepository) error {
						return r.Save(t.Context(), testContent(t, "Synthetic", false))
					})
				}
			case "registration":
				f := newRegistrationFixture(t)
				o = f.owner
				run = func() error { _, err := f.service.RegisterContent(t.Context(), f.command); return err }
			case "adoption":
				f, s, c := adoptionFixture(t)
				o = f.owner
				run = func() error { _, err := s.AdoptBaseline(t.Context(), c); return err }
			case "review":
				f, op, next, result := reviewWriteFixture(t, false)
				o = f.owner
				run = func() error { return commitReviewFixture(t, f, op, next, result) }
			case "order":
				f, _, op, next, result := orderWriteFixture(t)
				o = f.owner
				run = func() error { return commitOrderFixture(t, f, op, next, result) }
			case "supersession":
				f := newSupersessionFixture(t)
				o = f.owner
				run = func() error { _, err := f.sup.SupersedeVersion(t.Context(), f.command); return err }
			case "assignment":
				f := newAssignmentRepositoryFixture(t)
				owner, s, _ := assignmentActivateOwner(t, f)
				o = owner
				c := f.command(t, 1, assignmentapp.Assign, 0)
				run = func() error { _, err := s.Execute(t.Context(), c); return err }
			}
			if _, err := o.NewEventHub(t.Context(), eventTenant); err != nil {
				t.Fatal(err)
			}
			before := registrationSchemaRows(t, o.database.db)
			catalogSchemaExec(t, o.database.db, "CREATE TRIGGER event_injected_failure BEFORE INSERT ON tc_event_journal BEGIN SELECT RAISE(ABORT,'private journal failure'); END")
			if err := run(); err == nil {
				t.Fatal("owner committed without its journal")
			}
			if !reflect.DeepEqual(before, registrationSchemaRows(t, o.database.db)) {
				t.Fatal("failed append left partial owner state or consumed sequence")
			}
			catalogSchemaExec(t, o.database.db, "DROP TRIGGER event_injected_failure")
			if err := run(); err != nil {
				t.Fatal("retry after rollback", err)
			}
		})
	}
	t.Run("callback-rollback", func(t *testing.T) {
		o, _ := eventOwner(t)
		before := registrationSchemaRows(t, o.database.db)
		failure := errors.New("owner callback")
		err := o.WithinTagTransaction(t.Context(), func(r tagapp.TagRepository) error {
			if err := r.Insert(t.Context(), eventTag(t, 1)); err != nil {
				return err
			}
			return failure
		})
		if err != failure || !reflect.DeepEqual(before, registrationSchemaRows(t, o.database.db)) {
			t.Fatal("callback rollback not atomic", err)
		}
	})
	t.Run("event-count-bound", func(t *testing.T) {
		for _, n := range []int{64, 65} {
			o, _ := eventOwner(t)
			before := eventRows(t, o)
			err := o.WithinTagTransaction(t.Context(), func(r tagapp.TagRepository) error {
				for i := 1; i <= n; i++ {
					if e := r.Insert(t.Context(), eventTag(t, i)); e != nil {
						return e
					}
				}
				return nil
			})
			if n == 64 {
				if err != nil || eventRows(t, o) != before+64 {
					t.Fatal("exact capacity", err)
				}
			} else {
				if err == nil || eventRows(t, o) != before {
					t.Fatal("over capacity committed", err)
				}
			}
		}
	})
}
func TestEventCommitAtomicitySourceCounters(t *testing.T) {
	o, _ := eventOwner(t)
	before := registrationSchemaRows(t, o.database.db)
	err := o.database.withinSQLTransaction(context.Background(), func(tx *sql.Tx) error {
		if err := recordEvent(tx, eventFact{owner: ev.TagRegistry, typ: ev.TagChanged, kind: "tag", id: catalogSchemaID("tag", 1), revision: eventRevision(1), payload: ev.TagPayload{TagID: catalogSchemaID("tag", 1), Revision: "1", Change: "created"}}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil || !reflect.DeepEqual(before, registrationSchemaRows(t, o.database.db)) {
		t.Fatal("rollback changed counters")
	}
}
