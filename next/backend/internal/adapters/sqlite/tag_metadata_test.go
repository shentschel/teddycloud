package sqlite

import (
	"context"
	"errors"
	"fmt"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tagMetadataObservation(t *testing.T, value domaintag.Tag, n int, key domaintag.FactKey, text string) evidence.Observation {
	t.Helper()
	id, err := evidence.ParseID(fmt.Sprintf("obs_%026d", n))
	if err != nil {
		t.Fatal(err)
	}
	source, _ := evidence.NewSource("source", "opaque-r1", "record")
	o, err := evidence.NewObservation(id, source, evidence.Claim{Subject: value.ID().String(), Key: key.String(), Value: text}, time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC), evidence.Tentative, evidence.Accepted)
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func registerMetadataTag(t *testing.T, s applicationtag.Service) domaintag.Tag {
	t.Helper()
	v := testRegistryTag(t, '0', domaintag.UID{})
	got, err := s.Register(t.Context(), applicationtag.RegisterCommand{OpaqueID: v.ID().String(), Physical: applicationtag.PhysicalIdentityInput{UID: applicationtag.OptionalText{Present: true, Value: v.UID().String()}}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestTagMetadataPersistenceReplayAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	s := applicationtag.New(owner)
	value := registerMetadataTag(t, s)
	var commands []applicationtag.MetadataCommand
	for key := domaintag.FactKey(0); key < 4; key++ {
		yes := tagMetadataObservation(t, value, int(key)*2, key, "true")
		no := tagMetadataObservation(t, value, int(key)*2+1, key, "false")
		c := applicationtag.MetadataCommand{TagID: value.ID(), Change: domaintag.MetadataChange{ExpectedRevision: value.Revision(), Key: key, Observations: []evidence.Observation{yes, no}}}
		value, err = s.UpdateMetadata(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, c)
		did, _ := domaintag.ParseDecisionID(fmt.Sprintf("dec_%026d", key))
		c = applicationtag.MetadataCommand{TagID: value.ID(), Change: domaintag.MetadataChange{ExpectedRevision: value.Revision(), Key: key, Resolution: &domaintag.Resolution{ID: did, Support: []evidence.ID{yes.ID()}}}}
		value, err = s.UpdateMetadata(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, c)
	}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, err = OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(t.Context())
	s = applicationtag.New(owner)
	for _, c := range commands {
		got, err := s.UpdateMetadata(t.Context(), c)
		if err != nil || !got.Equal(value) {
			t.Fatal("durable replay", err)
		}
	}
	for _, read := range []func() (domaintag.Tag, error){
		func() (domaintag.Tag, error) { return s.FindByID(t.Context(), value.ID().String()) },
		func() (domaintag.Tag, error) { return s.FindByUID(t.Context(), value.UID().String()) },
		func() (domaintag.Tag, error) { return s.FindByRUID(t.Context(), value.RUID().String()) },
	} {
		got, err := read()
		if err != nil || !got.Equal(value) {
			t.Fatal("reopen history", err)
		}
	}
	var n int
	for table, want := range map[string]int{"tc_tag_observations": 8, "tc_tag_decisions": 4, "tc_tag_decision_support": 4} {
		if err := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != want {
			t.Fatalf("history %s = %d, %v", table, n, err)
		}
	}
}
func TestTagMetadataAtomicity(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	s := applicationtag.New(owner)
	initial := registerMetadataTag(t, s)
	if _, err := owner.database.db.ExecContext(t.Context(), `CREATE TRIGGER fail_tag_revision BEFORE UPDATE OF revision ON tc_tags BEGIN SELECT RAISE(ABORT,'private injected SQL detail'); END`); err != nil {
		t.Fatal(err)
	}
	c := applicationtag.MetadataCommand{TagID: initial.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Observations: []evidence.Observation{tagMetadataObservation(t, initial, 1, domaintag.CloudAuth, "true")}}}
	_, err := s.UpdateMetadata(t.Context(), c)
	assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
	got, err := s.FindByID(t.Context(), initial.ID().String())
	if err != nil || !got.Equal(initial) {
		t.Fatal("revision failure changed aggregate", err)
	}
	var count int
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_tag_observations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("evidence escaped rollback", count, err)
	}
	// Even a callback swallowing CAS failure cannot retain inserted evidence.
	next, _, err := initial.ApplyMetadata(c.Change)
	if err != nil {
		t.Fatal(err)
	}
	err = owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error {
		assertTagError(t, r.(applicationtag.MetadataRepository).CompareAndSwap(t.Context(), 1, next), applicationtag.ErrRepositoryUnavailable)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_tag_observations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("savepoint did not fence failed CAS", count, err)
	}
	if _, err := owner.database.db.ExecContext(t.Context(), `DROP TRIGGER fail_tag_revision`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMetadata(t.Context(), c); err != nil {
		t.Fatal("retry after rollback", err)
	}
}
func TestTagConcurrentMetadata(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	s := applicationtag.New(owner)
	value := registerMetadataTag(t, s)
	start := make(chan struct{})
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		o := tagMetadataObservation(t, value, n, domaintag.Owned, "true")
		go func() {
			<-start
			_, err := s.UpdateMetadata(context.Background(), applicationtag.MetadataCommand{TagID: value.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.Owned, Observations: []evidence.Observation{o}}})
			results <- err
		}()
	}
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, applicationtag.ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("unexpected winners", wins, conflicts)
	}
	var count int
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_tag_observations`).Scan(&count); err != nil || count != 1 {
		t.Fatal("lost or partial evidence", count, err)
	}
	got, err := s.FindByID(t.Context(), value.ID().String())
	if err != nil || got.Revision() != 2 {
		t.Fatal(err)
	}
	// Explicit caller re-admission of the other command adds its evidence.
	for n := 0; n < 2; n++ {
		o := tagMetadataObservation(t, value, n, domaintag.Owned, "true")
		_, err := s.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{TagID: value.ID(), Change: domaintag.MetadataChange{ExpectedRevision: got.Revision(), Key: domaintag.Owned, Observations: []evidence.Observation{o}}})
		if err == nil {
			break
		}
		if !errors.Is(err, applicationtag.ErrEvidenceConflict) {
			t.Fatal(err)
		}
	}
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_tag_observations`).Scan(&count); err != nil || count != 2 {
		t.Fatal("retry lost evidence", count, err)
	}
}
func TestTagMetadataPersistedPreflight(t *testing.T) {
	for _, tc := range []struct{ name, mutation string }{
		{"source bytes", `UPDATE tc_tag_observations SET source_name=?`},
		{"invalid enum", `UPDATE tc_tag_observations SET review=9`},
		{"timestamp", `UPDATE tc_tag_observations SET observed_at='2000-02-30T00:00:00.000000001Z'`},
		{"introduced revision", `UPDATE tc_tag_observations SET introduced_revision=999`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			s := applicationtag.New(owner)
			value := registerMetadataTag(t, s)
			_, err := s.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{TagID: value.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Observations: []evidence.Observation{tagMetadataObservation(t, value, 1, domaintag.CloudAuth, "true")}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owner.database.db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=ON`); err != nil {
				t.Fatal(err)
			}
			var args []any
			if tc.name == "source bytes" {
				args = []any{strings.Repeat("<", 1024*1024)}
			}
			if _, err := owner.database.db.ExecContext(t.Context(), tc.mutation, args...); err != nil {
				t.Fatal(err)
			}
			got, err := s.FindByID(t.Context(), value.ID().String())
			assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
			if !got.ID().IsZero() {
				t.Fatal("partial successful read")
			}
		})
	}
}

func TestTagMetadataPersistedCountAndEncodedLimits(t *testing.T) {
	for _, n := range []int{4096, 4097} {
		t.Run(fmt.Sprintf("observations_%d", n), func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			s := applicationtag.New(owner)
			value := registerMetadataTag(t, s)
			source := strings.Repeat("<", 128)
			_, err := owner.database.db.ExecContext(t.Context(), `WITH RECURSIVE numbers(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM numbers WHERE n<?)
   INSERT INTO tc_tag_observations (observation_id,tag_id,introduced_revision,fact_key,value,source_name,source_revision,source_record,observed_at,confidence,review)
   SELECT printf('obs_%026d',n),?,n+1,0,1,?,?,?,'2000-01-01T00:00:00.000000001Z',1,1 FROM numbers`, n, value.ID().String(), source, source, source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owner.database.db.ExecContext(t.Context(), `UPDATE tc_tags SET revision=? WHERE tag_id=?`, n+1, value.ID().String()); err != nil {
				t.Fatal(err)
			}
			got, err := s.FindByID(t.Context(), value.ID().String())
			assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
			if !got.ID().IsZero() {
				t.Fatal("oversized aggregate returned")
			}
			err = owner.WithinTagTransaction(t.Context(), func(repository applicationtag.TagRepository) error {
				r := repository.(*tagRepository)
				countErr := r.metadataCounts(t.Context(), value.ID())
				if n == 4097 {
					assertTagError(t, countErr, applicationtag.ErrRepositoryUnavailable)
				} else if countErr != nil {
					t.Fatal(countErr)
				}
				if err := r.metadataScalars(t.Context(), value.ID()); err != nil {
					t.Fatal("all individual fields must pass scalar bounds", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTagMetadataCrossTagIdentityAndSupport(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	s := applicationtag.New(owner)
	first := registerMetadataTag(t, s)
	second := testRegistryTag(t, '1', domaintag.UIDFromBytes([8]byte{1}))
	err := owner.WithinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), second) })
	if err != nil {
		t.Fatal(err)
	}
	o := tagMetadataObservation(t, first, 1, domaintag.CloudAuth, "true")
	_, err = s.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{TagID: first.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Observations: []evidence.Observation{o}}})
	if err != nil {
		t.Fatal(err)
	}
	// A reused global observation ID conflicts, even if it is absent in Tag 2.
	_, err = s.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{TagID: second.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Observations: []evidence.Observation{tagMetadataObservation(t, second, 1, domaintag.CloudAuth, "true")}}})
	assertTagError(t, err, applicationtag.ErrEvidenceConflict)
	did, _ := domaintag.ParseDecisionID("dec_" + strings.Repeat("0", 26))
	_, err = s.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{TagID: second.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Resolution: &domaintag.Resolution{ID: did, Support: []evidence.ID{o.ID()}}}})
	assertTagError(t, err, applicationtag.ErrEvidenceConflict)
	got, err := s.FindByID(t.Context(), second.ID().String())
	if err != nil || !got.Equal(second) {
		t.Fatal("cross-tag rejection changed second tag", err)
	}
}
