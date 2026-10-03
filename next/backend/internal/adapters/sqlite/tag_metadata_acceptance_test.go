package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	sqliteDriver "modernc.org/sqlite"
)

// A connector around the real SQLite driver observes SQL execution without
// adding production hooks or replacing the persisted rows with mocks.
type metadataTrace struct {
	phases                      []string
	queries                     []string
	observationPass             int
	decisionPass                int
	byteObservationRows         int
	materializedObservationRows int
}

type metadataTraceConnector struct {
	driver.Connector
	trace *metadataTrace
}

func (c metadataTraceConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return metadataTraceConn{Conn: conn, trace: c.trace}, nil
}

type metadataTraceConn struct {
	driver.Conn
	trace *metadataTrace
}

func (c metadataTraceConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c metadataTraceConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c metadataTraceConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	lower := strings.ToLower(q)
	phase := ""
	switch {
	case strings.Contains(lower, "typeof(") && strings.Contains(lower, "tc_tag_"):
		phase = "scalars"
	case strings.HasPrefix(q, "SELECT observation_id,introduced_revision"):
		c.trace.observationPass++
		phase = "bytes"
		if c.trace.observationPass > 1 {
			phase = "materialization"
		}
	case strings.HasPrefix(q, "SELECT decision_id,fact_key"):
		c.trace.decisionPass++
		phase = "bytes"
		if c.trace.decisionPass > 1 {
			phase = "materialization"
		}
	case strings.HasPrefix(q, "SELECT decision_id,observation_id"):
		phase = "scalars"
	case strings.HasPrefix(q, "SELECT observation_id FROM tc_tag_decision_support"):
		phase = "materialization"
	case strings.Contains(q, "SELECT observation_id FROM tc_tag_observations o"):
		phase = "bytes"
	case strings.Contains(lower, "tc_tag_"):
		phase = "counts"
	}
	if phase != "" {
		c.trace.queries = append(c.trace.queries, q)
		if len(c.trace.phases) == 0 || c.trace.phases[len(c.trace.phases)-1] != phase {
			c.trace.phases = append(c.trace.phases, phase)
		}
	}
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(q, "SELECT observation_id,introduced_revision") {
		return metadataTraceRows{Rows: rows, trace: c.trace, materializing: phase == "materialization"}, nil
	}
	return rows, nil
}

type metadataTraceRows struct {
	driver.Rows
	trace         *metadataTrace
	materializing bool
}

func (r metadataTraceRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	if err == nil {
		if r.materializing {
			r.trace.materializedObservationRows++
		} else {
			r.trace.byteObservationRows++
		}
	}
	return err
}

func assertMetadataTrace(t *testing.T, trace metadataTrace, phases ...string) {
	t.Helper()
	if !reflect.DeepEqual(trace.phases, phases) {
		t.Fatalf("load phases %v, want %v", trace.phases, phases)
	}
	for _, q := range trace.queries {
		lower := strings.ToLower(q)
		for _, forbidden := range []string{" join ", "group_concat(", "json_group_", "select *"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("joined/aggregated payload query: %s", q)
			}
		}
	}
}

// Fixtures use compact, parseable rows. The per-decision overflow necessarily
// also exceeds the number of available distinct observations, explained below.
func seedMetadataHistory(t *testing.T, owner *LifecycleOwner, value domaintag.Tag, observations int, supports []int) {
	t.Helper()
	tx, err := owner.database.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	obs, err := tx.PrepareContext(t.Context(), `INSERT INTO tc_tag_observations VALUES (?,?,2,0,1,'s','r','x','2000-01-01T00:00:00.000000001Z',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	for n := 0; n < observations; n++ {
		if _, err := obs.ExecContext(t.Context(), fmt.Sprintf("obs_%026d", n), value.ID().String()); err != nil {
			t.Fatal(err)
		}
	}
	link, err := tx.PrepareContext(t.Context(), `INSERT INTO tc_tag_decision_support VALUES (?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	for d, count := range supports {
		id := fmt.Sprintf("dec_%026d", d)
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO tc_tag_decisions VALUES (?,?,0,?,?)`, id, value.ID().String(), d+2, d+3); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < count; n++ {
			if _, err := link.ExecContext(t.Context(), value.ID().String(), id, fmt.Sprintf("obs_%026d", n)); err != nil {
				t.Fatal(err)
			}
		}
	}
	revision := max(2, len(supports)+2)
	if _, err := tx.ExecContext(t.Context(), `UPDATE tc_tags SET revision=? WHERE tag_id=?`, revision, value.ID().String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func readMetadataRevision(t *testing.T, owner *LifecycleOwner) domaintag.Revision {
	t.Helper()
	var revision int64
	if err := owner.database.db.QueryRowContext(t.Context(), `SELECT revision FROM tc_tags`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return domaintag.Revision(revision)
}

func tracedPersistedMetadata(t *testing.T, owner *LifecycleOwner, value domaintag.Tag) (domaintag.Tag, error, metadataTrace) {
	t.Helper()
	// Use the current persisted revision, including corrupt
	// histories that deliberately cannot be restored into a domain aggregate.
	dsn, _, err := connectionDSN(owner.config)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteDriver.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	trace := metadataTrace{}
	db := sql.OpenDB(metadataTraceConnector{Connector: connector, trace: &trace})
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := (&tagRepository{transaction: tx}).loadMetadata(t.Context(), value.ID(), value.UID(), readMetadataRevision(t, owner))
	return got, err, trace
}

func TestTagMetadataPersistedDecisionAndSupportBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		observations int
		supports     []int
		over         bool
	}{
		{"decisions_exact", 1, repeatedSupport(domaintag.MaxRetainedDecisions, 1), false},
		{"decisions_over", 1, repeatedSupport(domaintag.MaxRetainedDecisions+1, 1), true},
		{"per_decision_exact", 4096, []int{4096}, false},
		{"per_decision_over", 4096, []int{4097}, true},
		{"cumulative_exact", 4096, []int{4096, 4096, 4096, 4096}, false},
		{"cumulative_over", 4096, []int{4096, 4096, 4096, 4096, 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			value := registerMetadataTag(t, applicationtag.New(owner))
			// A 4097-link decision cannot reference 4096 distinct observations.
			// Bypass FK enforcement to exercise the per-decision count gate,
			// rather than failing at the earlier observation-count gate.
			if tc.name == "per_decision_over" {
				if _, err := owner.database.db.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
			}
			seedMetadataHistory(t, owner, value, tc.observations, tc.supports)
			got, err, trace := tracedPersistedMetadata(t, owner, value)
			if tc.over {
				assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
				if !got.ID().IsZero() {
					t.Fatal("partial result on quota refusal")
				}
				assertMetadataTrace(t, trace, "counts")
				if tc.name == "per_decision_over" && len(trace.queries) != 4 {
					t.Fatalf("per-decision gate not reached: %d queries", len(trace.queries))
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				observations, decisions, links := 0, 0, 0
				err := got.VisitHistory(func(evidence.Observation, domaintag.Revision) error { observations++; return nil }, func(_ domaintag.DecisionID, _ domaintag.FactKey, _ domaintag.Revision, _ domaintag.Revision, n int, _ func(int) evidence.ID) error {
					decisions++
					links += n
					return nil
				})
				wantLinks := 0
				for _, n := range tc.supports {
					wantLinks += n
				}
				if err != nil || observations != tc.observations || decisions != len(tc.supports) || links != wantLinks {
					t.Fatalf("history counts %d/%d/%d: %v", observations, decisions, links, err)
				}
				assertMetadataTrace(t, trace, "counts", "scalars", "bytes", "materialization")
			}
		})
	}
}

func repeatedSupport(count, n int) []int {
	result := make([]int, count)
	for i := range result {
		result[i] = n
	}
	return result
}

func TestTagMetadataPersistedScalarByteBoundaries(t *testing.T) {
	for _, field := range []string{"source_name", "source_revision", "source_record"} {
		for _, over := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_over_%t", field, over), func(t *testing.T) {
				owner := openLifecycleApplicationOwner(t)
				value := registerMetadataTag(t, applicationtag.New(owner))
				seedMetadataHistory(t, owner, value, 1, nil)
				if _, err := owner.database.db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=ON`); err != nil {
					t.Fatal(err)
				}
				// UTF-8 bytes, not character count; escaped markup is valid input.
				text := strings.Repeat("é", 64)
				if over {
					text += "x"
				}
				if _, err := owner.database.db.ExecContext(t.Context(), "UPDATE tc_tag_observations SET "+field+"=?", text); err != nil {
					t.Fatal(err)
				}
				got, err, trace := tracedPersistedMetadata(t, owner, value)
				if over {
					assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
					if !got.ID().IsZero() {
						t.Fatal("partial scalar result")
					}
					assertMetadataTrace(t, trace, "counts", "scalars")
				} else {
					if err != nil {
						t.Fatal(err)
					}
					assertMetadataTrace(t, trace, "counts", "scalars", "bytes", "materialization")
				}
			})
		}
	}
}

func TestTagMetadataPersistedExactEncodedBytes(t *testing.T) {
	for _, over := range []bool{false, true} {
		t.Run(fmt.Sprintf("over_%t", over), func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			value := registerMetadataTag(t, applicationtag.New(owner))
			const count = domaintag.MaxRetainedObservations
			base, _ := domaintag.NewHistorySize(value.ID(), value.UID(), 2)
			plain := strings.Repeat("a", 128)
			source, _ := evidence.NewSource(plain, plain, plain)
			for n := 0; n < count; n++ {
				o := sizedPersistedObservation(t, value, n, source)
				if err := base.AddObservation(value.ID(), 2, o, 2); err != nil {
					t.Fatal(err)
				}
			}
			n, err := base.Finish()
			if err != nil {
				t.Fatal(err)
			}
			target := domaintag.MaxEncodedTagBytes
			if over {
				target++
			}
			delta := target - n
			escapes := (delta + 4) / 5 // '<' occupies six encoded bytes instead of one.
			drop := escapes*5 - delta
			tx, err := owner.database.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stmt, err := tx.PrepareContext(t.Context(), `INSERT INTO tc_tag_observations VALUES (?,?,2,0,1,?,?,?,'2000-01-01T00:00:00.000000001Z',1,0)`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for i := 0; i < count; i++ {
				var fields [3]string
				for j := range fields {
					k := min(int64(128), escapes)
					escapes -= k
					length := 128
					if i == count-1 && j == 2 {
						length -= int(drop)
					}
					if k > int64(length) {
						t.Fatal("fixture padding overflow")
					}
					fields[j] = strings.Repeat("<", int(k)) + strings.Repeat("a", length-int(k))
				}
				if _, err := stmt.ExecContext(t.Context(), fmt.Sprintf("obs_%026d", i), value.ID().String(), fields[0], fields[1], fields[2]); err != nil {
					t.Fatal(err)
				}
			}
			if escapes != 0 {
				t.Fatal("fixture cannot reach byte boundary")
			}
			if _, err := tx.ExecContext(t.Context(), `UPDATE tc_tags SET revision=2`); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			got, err, trace := tracedPersistedMetadata(t, owner, value)
			if over {
				assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
				if !got.ID().IsZero() {
					t.Fatal("partial byte-limit result")
				}
				assertMetadataTrace(t, trace, "counts", "scalars", "bytes")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				actual, err := got.EncodedSize()
				if err != nil || actual != target {
					t.Fatalf("encoded bytes %d, want %d: %v", actual, target, err)
				}
				assertMetadataTrace(t, trace, "counts", "scalars", "bytes", "materialization")
				if trace.byteObservationRows != count || trace.materializedObservationRows != count {
					t.Fatalf("two-pass row reads: %+v", trace)
				}
			}
		})
	}
}

func TestTagMetadataPersistedStreamingStopsBeforeMaterialization(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	value := registerMetadataTag(t, applicationtag.New(owner))
	seedMetadataHistory(t, owner, value, domaintag.MaxRetainedObservations, nil)
	text := strings.Repeat("<", 128)
	if _, err := owner.database.db.ExecContext(t.Context(), `UPDATE tc_tag_observations SET source_name=?,source_revision=?,source_record=?`, text, text, text); err != nil {
		t.Fatal(err)
	}
	got, err, trace := tracedPersistedMetadata(t, owner, value)
	assertTagError(t, err, applicationtag.ErrRepositoryUnavailable)
	if !got.ID().IsZero() {
		t.Fatal("partial streamed result")
	}
	assertMetadataTrace(t, trace, "counts", "scalars", "bytes")
	if trace.byteObservationRows <= 0 || trace.byteObservationRows >= domaintag.MaxRetainedObservations || trace.materializedObservationRows != 0 {
		t.Fatalf("byte refusal did not stop streaming before later rows/copies: %+v", trace)
	}
}

func sizedPersistedObservation(t *testing.T, value domaintag.Tag, n int, source evidence.Source) evidence.Observation {
	t.Helper()
	id, err := evidence.ParseID(fmt.Sprintf("obs_%026d", n))
	if err != nil {
		t.Fatal(err)
	}
	o, err := evidence.NewObservation(id, source, evidence.Claim{Subject: value.ID().String(), Key: "protocol_valid", Value: "true"}, time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC), evidence.Tentative, evidence.Pending)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// Both services have established a read snapshot before either may write.
type metadataBarrierTransactor struct {
	database *Database
	read     chan<- struct{}
	release  <-chan struct{}
}

func (b metadataBarrierTransactor) WithinTagTransaction(ctx context.Context, fn func(applicationtag.TagRepository) error) error {
	if b.read == nil {
		return b.database.withinTagTransaction(ctx, fn)
	}
	return b.database.withinTagTransaction(ctx, func(r applicationtag.TagRepository) error {
		return fn(&metadataBarrierRepository{MetadataRepository: r.(applicationtag.MetadataRepository), read: b.read, release: b.release})
	})
}

type metadataBarrierRepository struct {
	applicationtag.MetadataRepository
	read    chan<- struct{}
	release <-chan struct{}
	paused  bool
}

func (r *metadataBarrierRepository) FindByID(ctx context.Context, id identity.TagID) (domaintag.Tag, bool, error) {
	v, found, err := r.MetadataRepository.FindByID(ctx, id)
	if err == nil && !r.paused {
		r.paused = true
		r.read <- struct{}{}
		select {
		case <-r.release:
		case <-ctx.Done():
			return domaintag.Tag{}, false, ctx.Err()
		}
	}
	return v, found, err
}

func TestTagConcurrentMetadataSeparateConnections(t *testing.T) {
	first, second := openContentionPair(t, 100*time.Millisecond)
	initial := testRegistryTag(t, '0', domaintag.UID{})
	if err := first.withinTagTransaction(t.Context(), func(r applicationtag.TagRepository) error { return r.Insert(t.Context(), initial) }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	read := make(chan struct{}, 2)
	releases := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	results := make(chan struct {
		n   int
		v   domaintag.Tag
		err error
	}, 2)
	commands := [2]applicationtag.MetadataCommand{}
	for n, db := range []*Database{first, second} {
		commands[n] = applicationtag.MetadataCommand{TagID: initial.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.Owned, Observations: []evidence.Observation{tagMetadataObservation(t, initial, n, domaintag.Owned, "true")}}}
		service := applicationtag.New(metadataBarrierTransactor{database: db, read: read, release: releases[n]})
		go func() {
			v, err := service.UpdateMetadata(ctx, commands[n])
			results <- struct {
				n   int
				v   domaintag.Tag
				err error
			}{n, v, err}
		}()
	}
	for range 2 {
		select {
		case <-read:
		case <-ctx.Done():
			t.Fatal("both snapshots not established")
		}
	}
	close(releases[0])
	winner := <-results
	close(releases[1])
	loser := <-results
	if winner.n != 0 || winner.err != nil {
		t.Fatalf("winner %d: %v", winner.n, winner.err)
	}
	if loser.n != 1 || (!errors.Is(loser.err, applicationtag.ErrRevisionConflict) && !errors.Is(loser.err, applicationtag.ErrEvidenceConflict)) {
		t.Fatalf("loser %d: %v", loser.n, loser.err)
	}
	if !loser.v.ID().IsZero() {
		t.Fatal("loser returned partial aggregate")
	}
	s := applicationtag.New(metadataBarrierTransactor{database: first})
	got, err := s.FindByID(t.Context(), initial.ID().String())
	if err != nil || !got.Equal(winner.v) {
		t.Fatalf("lost winner: %v", err)
	}
	commands[1].Change.ExpectedRevision = got.Revision()
	if _, err := s.UpdateMetadata(t.Context(), commands[1]); err != nil {
		t.Fatal("loser re-admission", err)
	}
	var n int
	if err := first.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tc_tag_observations`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("lost/partial evidence %d: %v", n, err)
	}
}
