package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	c "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// Only this harness owns transactions and persists A1-produced snapshots. This
// is fixture authoring, not the A2b.2 writer: no CAS, concurrency or fault-atomic
// acceptance is claimed. Every command/result/primitive and usage originates in
// the real A1 Service, which validates repository readback on subsequent calls.
type assignmentHarness struct {
	t         *testing.T
	db        *sql.DB
	snapshots map[identity.TagID]app.Snapshot
}
type assignmentHarnessPort struct {
	*assignmentRepository
	harness *assignmentHarness
}

func (h *assignmentHarness) WithinAssignmentTransaction(ctx context.Context, operation func(app.Port) error) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p := &assignmentHarnessPort{assignmentRepository: &assignmentRepository{transaction: tx}, harness: h}
	if err = operation(p); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *assignmentHarnessPort) Authenticate(ctx context.Context) (app.AuthenticatedActor, error) {
	return app.AuthenticatedActor{Reference: "fixture operator", Read: true, Decide: true, Review: true}, ctx.Err()
}
func (p *assignmentHarnessPort) Commit(ctx context.Context, expected uint64, snapshot app.Snapshot) error {
	t := p.harness.t
	t.Helper()
	if uint64(len(snapshot.Events)) != expected+1 {
		t.Fatal("invalid A1 fixture successor")
	}
	assignmentSeedSnapshot(t, p.transaction, snapshot)
	p.harness.snapshots[snapshot.TagID] = snapshot
	return ctx.Err()
}

func assignmentMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func assignmentEncodeFixture(t *testing.T, event app.Event) ([]byte, []byte, []byte) {
	t.Helper()
	in := event.Command
	input := assignmentInput{Format: "TCAS/1", Command: in.CommandID.String(), Assignment: in.AssignmentID.String(), Tag: in.TagID.String(), Kind: in.Kind, Expected: in.ExpectedRevision, TagRevision: in.ExpectedTagRevision, CatalogRevision: in.ExpectedCatalogRevision, Policy: in.PolicyRevision, Actor: event.Actor, Reason: in.Reason, Content: in.Target.ContentID().String(), Version: in.Target.VersionID().String(), Review: in.ReviewID.String(), Classification: in.Classification, Support: []string{}}
	if !in.EffectiveAt.IsZero() {
		input.Effective = in.EffectiveAt.Format(time.RFC3339Nano)
	}
	if in.Evidence != (d.Evidence{}) {
		var err error
		input.Evidence, err = in.Evidence.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range in.Support {
		input.Support = append(input.Support, id.String())
	}
	command := assignmentMarshal(t, input)
	result := assignmentMarshal(t, assignmentResult{event.Result.CommandID.String(), event.Result.AssignmentID.String(), event.Result.TagID.String(), event.Result.Revision, event.Result.PreviousCommand.String(), command})
	raw := assignmentMarshal(t, assignmentEvent{command, result, event.RecordedAt.Format(time.RFC3339Nano), event.Primitive})
	return command, result, raw
}
func assignmentSeedSnapshot(t *testing.T, tx *sql.Tx, snapshot app.Snapshot) {
	t.Helper()
	id, tag := snapshot.ID.String(), snapshot.TagID.String()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(query, err)
		}
	}
	if len(snapshot.Events) == 1 {
		exec(`INSERT INTO tc_assignments VALUES (?,?)`, id, tag)
	}
	replay, err := newAssignmentReplay(snapshot.ID, snapshot.TagID)
	if err != nil {
		t.Fatal(err)
	}
	var delta d.EvidenceDelta
	for _, event := range snapshot.Events {
		_, _, raw := assignmentEncodeFixture(t, event)
		delta, err = replay.append(event, raw)
		if err != nil {
			t.Fatal("replay A1 fixture", err)
		}
	}
	if replay.snapshot.Usage != snapshot.Usage {
		t.Fatal("repository charge differs from A1 complete usage")
	}
	event := snapshot.Events[len(snapshot.Events)-1]
	in := event.Command
	command, result, raw := assignmentEncodeFixture(t, event)
	sum := sha256.Sum256(raw)
	nullable := func(text string) any {
		if text == "" {
			return nil
		}
		return text
	}
	exec(`INSERT INTO tc_assignment_events VALUES (?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?)`, id, tag, event.Result.Revision, in.CommandID.String(), in.Kind, in.EffectiveAt.Unix(), in.EffectiveAt.Nanosecond(), event.RecordedAt.Unix(), event.RecordedAt.Nanosecond(), nullable(in.Target.ContentID().String()), nullable(in.Target.VersionID().String()), nullable(in.ReviewID.String()), raw, sum[:], delta.Observations, delta.SupportLinks, delta.RetainedBytes)
	support := in.Support
	if in.Evidence != (d.Evidence{}) {
		evidence, err := in.Evidence.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(evidence)
		exec(`INSERT INTO tc_assignment_evidence VALUES (?,?,?,?,1,?,?,?,?)`, in.Evidence.ID().String(), id, tag, event.Result.Revision, evidence, digest[:], nullable(in.Target.ContentID().String()), nullable(in.Target.VersionID().String()))
		support = []d.EvidenceID{in.Evidence.ID()}
	}
	for _, evidence := range support {
		exec(`INSERT INTO tc_assignment_support VALUES (?,?,?,?)`, id, tag, event.Result.Revision, evidence.String())
	}
	a, b := sha256.Sum256(command), sha256.Sum256(result)
	exec(`INSERT INTO tc_assignment_commands VALUES (?,?,?,?,1,?,?,?,?)`, in.CommandID.String(), id, tag, event.Result.Revision, command, result, a[:], b[:])
	usage := snapshot.Usage
	exec(`INSERT INTO tc_assignment_heads VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(assignment_id) DO UPDATE SET head_revision=excluded.head_revision,retired=excluded.retired,event_count=excluded.event_count,observation_count=excluded.observation_count,support_count=excluded.support_count,retained_bytes=excluded.retained_bytes`, id, tag, len(snapshot.Events), replay.projection.Retired, usage.Events(), usage.Observations(), usage.SupportLinks(), usage.RetainedBytes())
	projection := assignmentMarshal(t, replay.projection)
	digest := sha256.Sum256(projection)
	exec(`INSERT INTO tc_assignment_projections VALUES (?,?,?,1,?,?) ON CONFLICT(assignment_id) DO UPDATE SET head_revision=excluded.head_revision,canonical_projection=excluded.canonical_projection,projection_sha256=excluded.projection_sha256`, id, tag, len(snapshot.Events), projection, digest[:])
}

type assignmentRepositoryFixture struct {
	db                     *Database
	upstream               supersessionFixture
	harness                *assignmentHarness
	service                *app.Service
	tag                    identity.TagID
	id                     d.ID
	target, other, foreign d.OriginalTarget
}

func newAssignmentRepositoryFixture(t *testing.T) *assignmentRepositoryFixture {
	t.Helper()
	// Genuine v9 catalog imports, operator registrations, ownership/fingerprint
	// support. Upgrade only in the harness.
	upstream := newSupersessionFixture(t)
	// A second independently registered Content/version has the SAME fingerprint.
	// Tuple ownership, rather than descriptive/audio similarity, is authoritative.
	foreign := upstream.registrationFixture
	subject, _ := c.ContentSubject(catalogQueryContent(t, 200))
	commandID, _ := c.NewCatalogCommandID()
	claim, _ := c.TitleClaim("Other content")
	observation := foreign.observation(t, subject, claim)
	foreign.command = catalogapp.RegistrationCommand{ID: commandID, Subject: subject, ExpectedKind: c.CatalogExpectedAbsent, Observations: []c.CatalogObservation{observation}, Selections: []c.CatalogRegistrationSelection{{Key: c.TitleClaimKey, Observation: observation.ID()}}, Reason: "independent owner"}
	if _, err := foreign.service.RegisterContent(t.Context(), foreign.command); err != nil {
		t.Fatal(err)
	}
	foreignVersion := foreign.version(t, 201, 1)
	if _, err := foreign.service.RegisterCatalogVersion(t.Context(), foreignVersion); err != nil {
		t.Fatal(err)
	}

	path := dbPathForSupersessionTest(t, upstream.owner.database)
	if err := upstream.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	db, err := Open(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(t.TempDir(), "v9.sqlite")}, assignmentSchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &assignmentRepositoryFixture{db: db, upstream: upstream}
	f.tag, _ = identity.ParseTagID(catalogSchemaID("tag", 1))
	f.id, _ = d.ParseID(catalogSchemaID("asn", 1))
	f.target, _ = d.NewOriginalTarget(upstream.command.Content, upstream.command.Old.ID())
	f.other, _ = d.NewOriginalTarget(upstream.command.Content, upstream.command.New.ID())
	f.foreign, _ = d.NewOriginalTarget(subject.ContentID(), foreignVersion.Subject.VersionID())
	for _, n := range []int{1, 2, 3} {
		catalogSchemaExec(t, db.db, `INSERT INTO tc_tags VALUES (?,?,1)`, catalogSchemaID("tag", n), bytes.Repeat([]byte{byte(n)}, 8))
	}
	f.harness = &assignmentHarness{t: t, db: db.db, snapshots: map[identity.TagID]app.Snapshot{}}
	f.service = app.New(f.harness)
	return f
}
func (f *assignmentRepositoryFixture) command(t *testing.T, n int, kind app.Kind, expected uint64) app.Command {
	t.Helper()
	id, err := d.ParseCommandID(fmt.Sprintf("%032x", n))
	if err != nil {
		t.Fatal(err)
	}
	var revision uint64
	{
		var stored uint64
		if err := f.db.db.QueryRowContext(t.Context(), `SELECT aggregate_revision FROM tc_catalog_managed WHERE content_id=?`, f.target.ContentID().String()).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		revision = stored
	}
	in := app.Command{CommandID: id, AssignmentID: f.id, TagID: f.tag, Kind: kind, ExpectedRevision: expected, ExpectedTagRevision: 1, PolicyRevision: app.PolicyRevision, Reason: "reviewed <fixture> & exact target"}
	if kind == app.Assign || kind == app.RecordOriginalTarget || kind == app.ResolveOriginalTarget {
		in.Target = f.target
		in.ExpectedCatalogRevision = revision
	}
	if kind >= app.RecordClassification && kind <= app.ResolveOriginalTarget {
		in.ReviewID, _ = d.ParseReviewID(catalogSchemaID("rev", n))
		if kind == app.RecordClassification || kind == app.RecordOriginalTarget {
			evidence, _ := d.ParseEvidenceID(catalogSchemaID("evd", n))
			in.Evidence, err = d.NewEvidence(evidence, f.tag, d.OperatorAssertion, "fixture source", d.AcceptedEvidence)
			if err != nil {
				t.Fatal(err)
			}
		}
		if kind == app.RecordClassification || kind == app.ResolveClassification {
			in.Classification = d.Original
		}
	} else {
		in.EffectiveAt = time.Date(2026, 1, 1, 0, n, 0, 123, time.UTC)
	}
	return in
}
func (f *assignmentRepositoryFixture) execute(t *testing.T, in app.Command) app.Result {
	t.Helper()
	result, err := f.service.Execute(t.Context(), in)
	if err != nil {
		t.Fatal("A1 fixture command", in.Kind, in.ExpectedRevision, err)
	}
	return result
}
func (f *assignmentRepositoryFixture) reader(t *testing.T) (*assignmentRepository, *sql.Tx) {
	t.Helper()
	tx, err := f.db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return &assignmentRepository{transaction: tx}, tx
}
func assignmentWantError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v; want %v", err, want)
	}
}

func TestAssignmentRepositoryRoundTrip(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	first := f.command(t, 1, app.RecordClassification, 0)
	f.execute(t, first)
	// Review-first history is known but unassigned, retained from revision one.
	view, err := f.service.GetEffectiveAssignment(t.Context(), app.EffectiveQuery{TagID: f.tag, At: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil || !view.AggregatePresent || view.Desired.State != d.NoAssignment || view.Classification != d.Original {
		t.Fatal("first review", view, err)
	}
	second := f.command(t, 2, app.RecordClassification, 1)
	second.Classification = d.CustomCard
	f.execute(t, second)
	resolution := f.command(t, 3, app.ResolveClassification, 2)
	resolution.Support = []d.EvidenceID{first.Evidence.ID()}
	f.execute(t, resolution)
	baseline := f.command(t, 4, app.RecordOriginalTarget, 3)
	f.execute(t, baseline)
	contrary := f.command(t, 5, app.RecordOriginalTarget, 4)
	contrary.Target = f.other
	f.execute(t, contrary)
	resolve := f.command(t, 6, app.ResolveOriginalTarget, 5)
	resolve.Support = []d.EvidenceID{baseline.Evidence.ID()}
	f.execute(t, resolve)
	assign := f.command(t, 7, app.Assign, 6)
	f.execute(t, assign)
	unassign := f.command(t, 8, app.Unassign, 7)
	f.execute(t, unassign)
	retire := f.command(t, 9, app.RetireAssignment, 8)
	f.execute(t, retire)
	// A second aggregate proves lookup is global rather than scoped to f.tag.
	foreign := f.command(t, 10, app.Assign, 0)
	foreign.TagID, _ = identity.ParseTagID(catalogSchemaID("tag", 2))
	foreign.AssignmentID, _ = d.ParseID(catalogSchemaID("asn", 2))
	f.execute(t, foreign)
	before := registrationSchemaRows(t, f.db.db)
	r, tx := f.reader(t)
	for tag, want := range f.harness.snapshots {
		got, found, err := r.Load(t.Context(), tag)
		if err != nil || !found || !reflect.DeepEqual(got, want) {
			t.Fatal("canonical snapshot", found, err)
		}
		for _, event := range want.Events {
			got, found, err := r.LookupCommand(t.Context(), event.Command.CommandID)
			if err != nil || !found || !reflect.DeepEqual(got, event) {
				t.Fatal("global original event", found, err)
			}
		}
	}
	missingTag, _ := identity.ParseTagID(catalogSchemaID("tag", 3))
	if _, found, err := r.Load(t.Context(), missingTag); err != nil || found {
		t.Fatal("known Tag without aggregate", found, err)
	}
	unknown, _ := identity.ParseTagID(catalogSchemaID("tag", 99))
	if _, found, err := r.Tag(t.Context(), unknown); err != nil || found {
		t.Fatal("unknown Tag", found, err)
	}
	missingCommand, _ := d.ParseCommandID(fmt.Sprintf("%032x", 999))
	if _, found, err := r.LookupCommand(t.Context(), missingCommand); err != nil || found {
		t.Fatal("absent command", found, err)
	}
	// No alias to a cached snapshot or command/primitive buffer.
	got, _, _ := r.Load(t.Context(), f.tag)
	got.Events[0].Primitive[0] ^= 1
	again, _, err := r.Load(t.Context(), f.tag)
	if err != nil || !reflect.DeepEqual(again, f.harness.snapshots[f.tag]) {
		t.Fatal("readback alias", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, registrationSchemaRows(t, f.db.db)) {
		t.Fatal("read mutated retained rows")
	}
	for _, in := range []app.Command{first, second, resolution, baseline, contrary, resolve, assign, unassign, retire, foreign} {
		got, found, err := f.service.LookupAssignmentCommand(t.Context(), in)
		if err != nil || !found || got.Revision != in.ExpectedRevision+1 {
			t.Fatal("A1 authorized replay/readback", found, err)
		}
	}
}

func TestAssignmentNoCatalogRetarget(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	assign := f.command(t, 1, app.Assign, 0)
	originalResult := f.execute(t, assign)
	query := app.EffectiveQuery{TagID: f.tag, At: assign.EffectiveAt}
	before, err := f.service.GetEffectiveAssignment(t.Context(), query)
	if err != nil || before.Superseded || before.Desired.Target != f.target {
		t.Fatal("before supersession", before, err)
	}
	original := f.harness.snapshots[f.tag]
	// Author the later catalog state through a REAL v9 owner on an independent
	// copy of the verified pre-v10 fixture backup. Never reopen v10 as v9 or
	// route the inert repository through a production catalog transactor.
	backup, ok := f.db.UpgradeBackup()
	if !ok || backup.SchemaVersion != 9 {
		t.Fatal("missing v9 fixture backup")
	}
	copyPath := filepath.Join(t.TempDir(), "later-v9.sqlite")
	raw, err := os.ReadFile(backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(copyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: copyPath}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	upstream := f.upstream
	upstream.owner = owner
	upstream.bind(t)
	upstream.sup, err = catalogapp.NewSupersessionService(upstream.base, upstream.cap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = upstream.sup.SupersedeVersion(t.Context(), upstream.command); err != nil {
		t.Fatal(err)
	}
	if err = owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	later, err := Open(t.Context(), Config{Path: copyPath, UpgradeBackupPath: filepath.Join(t.TempDir(), "later-pre-v10.sqlite")}, assignmentSchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = later.Close() })
	catalogSchemaExec(t, later.db, `INSERT INTO tc_tags VALUES (?,?,1)`, f.tag.String(), bytes.Repeat([]byte{1}, 8))
	tx, err := later.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assignmentSeedSnapshot(t, tx, original)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.db = later
	f.harness.db = later.db
	for _, knowledge := range []*uint64{nil, new(uint64(1))} {
		query.KnowledgeRevision = knowledge
		view, err := f.service.GetEffectiveAssignment(t.Context(), query)
		if err != nil || view.Desired.Target != f.target || view.Desired.Target == f.other || !view.Superseded || !view.CatalogOnly || view.CatalogRevision <= before.CatalogRevision {
			t.Fatal("supersession changed explicit target", view, err)
		}
	}
	r, tx := f.reader(t)
	snapshot, found, err := r.Load(t.Context(), f.tag)
	if err != nil || !found || !reflect.DeepEqual(snapshot, original) {
		t.Fatal("retained assignment changed", found, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Execute(t.Context(), assign)
	if err != nil || got != originalResult {
		t.Fatal("exact replay after supersession", got, err)
	}
	changed := assign
	changed.ExpectedCatalogRevision++
	_, _, err = f.service.LookupAssignmentCommand(t.Context(), changed)
	assignmentWantError(t, err, app.ErrCommandConflict)
}

func TestAssignmentCatalogOnlyTarget(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	r, tx := f.reader(t)
	value, found, err := r.Target(t.Context(), f.target)
	own, ok := value.Ownership.Value()
	fp, fpOK := value.Fingerprint.Value()
	if err != nil || !found || !value.Managed || value.Bound || !ok || !fpOK || own.Owner() != f.target.ContentID() || fp.Fingerprint() != value.Version.Fingerprint() || len(value.Ownership.Alternatives[0].Support) == 0 || len(value.Fingerprint.Alternatives[0].Support) == 0 {
		t.Fatal("managed catalog-only", found, err)
	}
	foreignValue, foreignFound, foreignErr := r.Target(t.Context(), f.foreign)
	if foreignErr != nil || !foreignFound || foreignValue.Version.Fingerprint() != value.Version.Fingerprint() || foreignValue.Content.ID() == value.Content.ID() {
		t.Fatal("independent managed owner", foreignFound, foreignErr)
	}
	// A valid existing version under a different Content is a rejected tuple.
	foreign, _ := d.NewOriginalTarget(f.foreign.ContentID(), f.target.VersionID())
	_, found, err = r.Target(t.Context(), foreign)
	if found {
		t.Fatal("foreign tuple found")
	}
	assignmentWantError(t, err, app.ErrUnavailable)
	absentVersion, _ := identity.ParseContentVersionID(catalogSchemaID("ver", 99))
	absent, _ := d.NewOriginalTarget(f.target.ContentID(), absentVersion)
	if _, found, err := r.Target(t.Context(), absent); err != nil || found {
		t.Fatal("genuine missing version", found, err)
	}
	// Dependency reads observe this transaction's private, uncommitted changes.
	if _, err = tx.ExecContext(t.Context(), `INSERT INTO tc_tags VALUES (?,?,1)`, catalogSchemaID("tag", 4), bytes.Repeat([]byte{4}, 8)); err != nil {
		t.Fatal(err)
	}
	privateTag, _ := identity.ParseTagID(catalogSchemaID("tag", 4))
	tag, found, err := r.Tag(t.Context(), privateTag)
	if err != nil || !found || tag.Revision != 1 {
		t.Fatal("Tag escaped injected transaction", tag, err)
	}
	if _, err = tx.ExecContext(t.Context(), `UPDATE tc_catalog_managed SET aggregate_revision=aggregate_revision+1 WHERE content_id=?`, f.target.ContentID().String()); err != nil {
		t.Fatal(err)
	}
	_, _, err = r.Target(t.Context(), f.target)
	assignmentWantError(t, err, app.ErrUnavailable)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// No media binding is required for explicit assignment admission.
	f.execute(t, f.command(t, 1, app.Assign, 0))
}

// Mutations are isolated and rolled back, including dropped immutable triggers.
// Deferred FKs permit forensic damage without turning enforcement off globally;
// each repository read must independently detect it before returning any data.
func TestAssignmentRepositoryCorruption(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	class := f.command(t, 1, app.RecordClassification, 0)
	f.execute(t, class)
	contrary := f.command(t, 2, app.RecordClassification, 1)
	contrary.Classification = d.CustomCard
	f.execute(t, contrary)
	resolution := f.command(t, 3, app.ResolveClassification, 2)
	resolution.Support = []d.EvidenceID{class.Evidence.ID()}
	f.execute(t, resolution)
	baseline := f.command(t, 4, app.RecordOriginalTarget, 3)
	f.execute(t, baseline)
	f.execute(t, f.command(t, 5, app.Assign, 4))
	f.execute(t, f.command(t, 6, app.Unassign, 5))
	foreignClass := f.command(t, 11, app.RecordClassification, 0)
	foreignClass.TagID, _ = identity.ParseTagID(catalogSchemaID("tag", 2))
	foreignClass.AssignmentID, _ = d.ParseID(catalogSchemaID("asn", 2))
	foreignEvidence, _ := d.ParseEvidenceID(catalogSchemaID("evd", 11))
	foreignClass.Evidence, _ = d.NewEvidence(foreignEvidence, foreignClass.TagID, d.OperatorAssertion, "other Tag", d.AcceptedEvidence)
	f.execute(t, foreignClass)
	id, tag := f.id.String(), f.tag.String()
	type mutation struct {
		name, query string
		args        []any
		edit        func(*testing.T, *sql.Tx)
	}
	edits := []mutation{
		{name: "identity missing", query: `DELETE FROM tc_assignments WHERE assignment_id=?`, args: []any{id}},
		{name: "identity foreign Tag", query: `UPDATE tc_assignments SET tag_id=? WHERE assignment_id=?`, args: []any{catalogSchemaID("tag", 3), id}},
		{name: "head missing", query: `DELETE FROM tc_assignment_heads WHERE assignment_id=?`, args: []any{id}},
		{name: "head revision", query: `UPDATE tc_assignment_heads SET head_revision=4,event_count=4 WHERE assignment_id=?`, args: []any{id}},
		{name: "head retirement", query: `UPDATE tc_assignment_heads SET retired=1 WHERE assignment_id=?`, args: []any{id}},
		{name: "head event usage", query: `UPDATE tc_assignment_heads SET event_count=4 WHERE assignment_id=?`, args: []any{id}},
		{name: "head observation usage", query: `UPDATE tc_assignment_heads SET observation_count=observation_count+1 WHERE assignment_id=?`, args: []any{id}},
		{name: "head support usage", query: `UPDATE tc_assignment_heads SET support_count=support_count+1 WHERE assignment_id=?`, args: []any{id}},
		{name: "head byte usage", query: `UPDATE tc_assignment_heads SET retained_bytes=retained_bytes+1 WHERE assignment_id=?`, args: []any{id}},
		{name: "head over limit", query: `UPDATE tc_assignment_heads SET event_count=4097,head_revision=4097 WHERE assignment_id=?`, args: []any{id}},
		{name: "projection missing", query: `DELETE FROM tc_assignment_projections WHERE assignment_id=?`, args: []any{id}},
		{name: "projection digest", query: `UPDATE tc_assignment_projections SET projection_sha256=zeroblob(32) WHERE assignment_id=?`, args: []any{id}},
		{name: "projection revision", query: `UPDATE tc_assignment_projections SET head_revision=4 WHERE assignment_id=?`, args: []any{id}},
		{name: "projection foreign Tag", query: `UPDATE tc_assignment_projections SET tag_id=? WHERE assignment_id=?`, args: []any{catalogSchemaID("tag", 3), id}},
		{name: "projection encoding", query: `UPDATE tc_assignment_projections SET encoding=2 WHERE assignment_id=?`, args: []any{id}},
		{name: "event missing first", query: `DELETE FROM tc_assignment_events WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "event revision gap", query: `UPDATE tc_assignment_events SET revision=7 WHERE assignment_id=? AND revision=6`, args: []any{id}},
		{name: "event foreign aggregate", query: `UPDATE tc_assignment_events SET assignment_id=? WHERE assignment_id=? AND revision=2`, args: []any{catalogSchemaID("asn", 2), id}},
		{name: "event kind", query: `UPDATE tc_assignment_events SET kind=4 WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "event effective time", query: `UPDATE tc_assignment_events SET effective_nanos=effective_nanos+1 WHERE assignment_id=? AND revision=4`, args: []any{id}},
		{name: "event recorded time", query: `UPDATE tc_assignment_events SET recorded_nanos=recorded_nanos+1 WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "event target", query: `UPDATE tc_assignment_events SET version_id=? WHERE assignment_id=? AND revision=5`, args: []any{f.other.VersionID().String(), id}},
		{name: "event foreign tuple", query: `UPDATE tc_assignment_events SET content_id=? WHERE assignment_id=? AND revision=5`, args: []any{f.foreign.ContentID().String(), id}},
		{name: "event review index", query: `UPDATE tc_assignment_events SET review_id=? WHERE assignment_id=? AND revision=1`, args: []any{catalogSchemaID("rev", 99), id}},
		{name: "event digest", query: `UPDATE tc_assignment_events SET event_sha256=zeroblob(32) WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "event delta observations", query: `UPDATE tc_assignment_events SET new_observations=0 WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "event delta support", query: `UPDATE tc_assignment_events SET selected_support=0 WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "event delta bytes", query: `UPDATE tc_assignment_events SET retained_bytes=retained_bytes+1 WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "event oversized before materialization", query: `UPDATE tc_assignment_events SET canonical_event=zeroblob(16777217) WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "evidence missing", query: `DELETE FROM tc_assignment_evidence WHERE evidence_id=?`, args: []any{class.Evidence.ID().String()}},
		{name: "evidence digest", query: `UPDATE tc_assignment_evidence SET evidence_sha256=zeroblob(32) WHERE evidence_id=?`, args: []any{class.Evidence.ID().String()}},
		{name: "evidence introduced revision", query: `UPDATE tc_assignment_evidence SET introduced_revision=2 WHERE evidence_id=?`, args: []any{class.Evidence.ID().String()}},
		{name: "evidence foreign Tag", query: `UPDATE tc_assignment_evidence SET tag_id=? WHERE evidence_id=?`, args: []any{catalogSchemaID("tag", 2), class.Evidence.ID().String()}},
		{name: "evidence exact target", query: `UPDATE tc_assignment_evidence SET version_id=? WHERE evidence_id=?`, args: []any{f.other.VersionID().String(), baseline.Evidence.ID().String()}},
		{name: "support missing", query: `DELETE FROM tc_assignment_support WHERE assignment_id=? AND revision=1`, args: []any{id}},
		{name: "support other aggregate evidence", query: `UPDATE tc_assignment_support SET evidence_id=? WHERE assignment_id=? AND revision=3`, args: []any{foreignEvidence.String(), id}},
		{name: "support wrong dimension evidence", query: `UPDATE tc_assignment_support SET evidence_id=? WHERE assignment_id=? AND revision=3`, args: []any{baseline.Evidence.ID().String(), id}},
		{name: "support future revision", query: `UPDATE tc_assignment_support SET revision=4 WHERE assignment_id=? AND revision=3`, args: []any{id}},
		{name: "support foreign Tag", query: `UPDATE tc_assignment_support SET tag_id=? WHERE assignment_id=? AND revision=1`, args: []any{catalogSchemaID("tag", 2), id}},
		{name: "command missing", query: `DELETE FROM tc_assignment_commands WHERE command_id=?`, args: []any{class.CommandID.String()}},
		{name: "command revision", query: `UPDATE tc_assignment_commands SET revision=99 WHERE command_id=?`, args: []any{class.CommandID.String()}},
		{name: "command foreign Tag", query: `UPDATE tc_assignment_commands SET tag_id=? WHERE command_id=?`, args: []any{catalogSchemaID("tag", 2), class.CommandID.String()}},
		{name: "command input digest", query: `UPDATE tc_assignment_commands SET command_sha256=zeroblob(32) WHERE command_id=?`, args: []any{class.CommandID.String()}},
		{name: "command result digest", query: `UPDATE tc_assignment_commands SET result_sha256=zeroblob(32) WHERE command_id=?`, args: []any{class.CommandID.String()}},
		{name: "dependency missing version", query: `DELETE FROM tc_content_versions WHERE version_id=?`, args: []any{f.target.VersionID().String()}},
		{name: "dependency missing Tag", query: `DELETE FROM tc_tags WHERE tag_id=?`, args: []any{tag}},
		{name: "dependency Tag corrupt UID", query: `UPDATE tc_tags SET uid=zeroblob(7) WHERE tag_id=?`, args: []any{tag}},
		{name: "managed fingerprint missing support", query: `DELETE FROM tc_catalog_registration_support WHERE subject_id=? AND claim_key=4`, args: []any{f.target.VersionID().String()}},
		{name: "managed ownership missing support", query: `DELETE FROM tc_catalog_registration_support WHERE subject_id=? AND claim_key=5`, args: []any{f.target.VersionID().String()}},
	}
	// Hash-valid damaged blobs must also reject: semantic and canonical equality
	// is required independently of the digest, normalized indices and usage.
	for _, table := range []struct{ table, column, digest, where string }{
		{"tc_assignment_projections", "canonical_projection", "projection_sha256", "assignment_id='" + id + "'"},
		{"tc_assignment_events", "canonical_event", "event_sha256", "assignment_id='" + id + "' AND revision=1"},
		{"tc_assignment_evidence", "canonical_evidence", "evidence_sha256", "evidence_id='" + class.Evidence.ID().String() + "'"},
		{"tc_assignment_commands", "canonical_command", "command_sha256", "command_id='" + class.CommandID.String() + "'"},
		{"tc_assignment_commands", "canonical_result", "result_sha256", "command_id='" + class.CommandID.String() + "'"},
	} {
		edits = append(edits, mutation{name: table.table + " hash-valid changed bytes", edit: func(t *testing.T, tx *sql.Tx) {
			var raw []byte
			if err := tx.QueryRowContext(t.Context(), "SELECT "+table.column+" FROM "+table.table+" WHERE "+table.where).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			raw = append(raw, ' ')
			sum := sha256.Sum256(raw)
			if _, err := tx.ExecContext(t.Context(), "UPDATE "+table.table+" SET "+table.column+"=?,"+table.digest+"=? WHERE "+table.where, raw, sum[:]); err != nil {
				t.Fatal(err)
			}
		}})
	}
	edits = append(edits, mutation{name: "semantic primitive with valid digest", edit: func(t *testing.T, tx *sql.Tx) {
		var raw []byte
		if err := tx.QueryRowContext(t.Context(), `SELECT canonical_event FROM tc_assignment_events WHERE assignment_id=? AND revision=1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var event assignmentEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		event.Primitive[0] ^= 1
		raw = assignmentMarshal(t, event)
		sum := sha256.Sum256(raw)
		if _, err := tx.ExecContext(t.Context(), `UPDATE tc_assignment_events SET canonical_event=?,event_sha256=? WHERE assignment_id=? AND revision=1`, raw, sum[:], id); err != nil {
			t.Fatal(err)
		}
	}})
	before := registrationSchemaRows(t, f.db.db)
	for _, edit := range edits {
		t.Run(edit.name, func(t *testing.T) {
			tx, err := f.db.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			rows, err := tx.QueryContext(t.Context(), `SELECT name FROM sqlite_schema WHERE type='trigger'`)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					t.Fatal(err)
				}
				names = append(names, name)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, name := range names {
				if _, err := tx.ExecContext(t.Context(), `DROP TRIGGER "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
					t.Fatal(err)
				}
			}
			for _, query := range []string{`PRAGMA defer_foreign_keys=ON`, `PRAGMA ignore_check_constraints=ON`} {
				if _, err := tx.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			if edit.edit != nil {
				edit.edit(t, tx)
			} else if result, err := tx.ExecContext(t.Context(), edit.query, edit.args...); err != nil {
				t.Fatal("corruption setup failed", err)
			} else if count, err := result.RowsAffected(); err != nil || count != 1 {
				t.Fatal("corruption did not damage exactly one row", count, err)
			}
			r := &assignmentRepository{transaction: tx}
			snapshot, found, err := r.Load(t.Context(), f.tag)
			assignmentWantError(t, err, app.ErrUnavailable)
			if found || !reflect.DeepEqual(snapshot, app.Snapshot{}) {
				t.Fatal("released partial/corrupt snapshot")
			}
			event, found, err := r.LookupCommand(t.Context(), class.CommandID)
			assignmentWantError(t, err, app.ErrUnavailable)
			if found || !reflect.DeepEqual(event, app.Event{}) {
				t.Fatal("corrupt command reported found")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			// PRAGMA ignore_check_constraints is connection-local, unlike rolled-back DDL.
			catalogSchemaExec(t, f.db.db, `PRAGMA ignore_check_constraints=OFF`)
		})
	}
	if !reflect.DeepEqual(before, registrationSchemaRows(t, f.db.db)) {
		t.Fatal("corruption probes changed fixture")
	}
}

func TestAssignmentRepositoryInert(t *testing.T) {
	if migrations := SchemaMigrations(); len(migrations) != 9 || migrations[8].Version != 9 {
		t.Fatal("default schema activated")
	}
	normal, err := Open(t.Context(), Config{Path: filepath.Join(t.TempDir(), "normal.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer normal.Close()
	var tables int
	if err := normal.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'tc_assignment%'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("normal runtime assignment objects", tables, err)
	}
	tx, err := normal.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &assignmentRepository{transaction: tx}
	tag, _ := identity.ParseTagID(catalogSchemaID("tag", 1))
	_, _, err = r.Load(t.Context(), tag)
	assignmentWantError(t, err, app.ErrUnsupportedState)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	_, _, err = r.Load(t.Context(), tag)
	assignmentWantError(t, err, app.ErrUnavailable)
	_, _, err = (*assignmentRepository)(nil).Load(t.Context(), tag)
	assignmentWantError(t, err, app.ErrUnavailable)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = r.Load(canceled, tag)
	assignmentWantError(t, err, context.Canceled)
	for _, interfaceType := range []reflect.Type{reflect.TypeFor[app.Port](), reflect.TypeFor[app.Transactor]()} {
		if reflect.TypeFor[*assignmentRepository]().Implements(interfaceType) {
			t.Fatal("production port activated", interfaceType)
		}
	}
	for _, name := range []string{"Commit", "Save", "WithinAssignmentTransaction", "Authenticate"} {
		if _, exists := reflect.TypeFor[*assignmentRepository]().MethodByName(name); exists {
			t.Fatal("repository owns authority/write", name)
		}
	}
	// Review real production sources, not just this type's method set. An inert
	// component must have no constructor, composite literal, new() or runtime
	// reference outside its own file. Tests alone may construct it.
	files := token.NewFileSet()
	err = filepath.WalkDir(filepath.Join("..", "..", ".."), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		own := filepath.Base(path) == "assignment_repository.go"
		ast.Inspect(source, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && id.Name == "assignmentRepository" && !own {
				t.Errorf("production reachability: %s", files.Position(id.Pos()))
			}
			if !own {
				return true
			}
			if declaration, ok := node.(*ast.FuncDecl); ok && declaration.Recv == nil && declaration.Name.IsExported() {
				t.Errorf("exported constructor/function: %s", declaration.Name.Name)
			}
			if literal, ok := node.(*ast.CompositeLit); ok {
				if name, ok := literal.Type.(*ast.Ident); ok && name.Name == "assignmentRepository" {
					t.Error("production repository instantiation")
				}
			}
			if call, ok := node.(*ast.CallExpr); ok {
				if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "new" && len(call.Args) == 1 {
					if name, ok := call.Args[0].(*ast.Ident); ok && name.Name == "assignmentRepository" {
						t.Error("production new repository")
					}
				}
				if method, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch method.Sel.Name {
					case "Begin", "BeginTx", "Commit", "Rollback", "Exec", "ExecContext", "Save", "WithinCatalogTransaction", "WithinTagTransaction", "WithinAssignmentTransaction":
						t.Errorf("repository owns transaction/write: %s", method.Sel.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Exact staged schema only: corrupt/dirty ledger, missing migration and future
	// v11 reject without pretending this fixture is a supported v9 catalog lease.
	staged := assignmentSchemaFixture(t)
	for _, query := range []string{`UPDATE tc_schema_migrations SET dirty=1 WHERE version=10`, `UPDATE tc_schema_migrations SET checksum='bad' WHERE version=10`, `DELETE FROM tc_schema_migrations WHERE version=10`, `INSERT INTO tc_schema_migrations VALUES (11,'future','bad',0,NULL)`} {
		tx, err := staged.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
		r := &assignmentRepository{transaction: tx}
		_, _, err = r.Load(t.Context(), tag)
		assignmentWantError(t, err, app.ErrUnsupportedState)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
