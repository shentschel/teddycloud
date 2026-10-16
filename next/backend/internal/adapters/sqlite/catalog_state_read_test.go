package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

type catalogStateFixture struct {
	owner     *LifecycleOwner
	source    domain.SourceRegistration
	ingest    domain.IngestCapability
	review    domain.ReviewCapability
	namespace domain.CatalogOrderNamespace
	order     domain.CatalogOrderCapability
	versions  []catalogVersionRow
	subject   domain.CatalogSubject
}

func newCatalogStateFixture(t *testing.T, version bool) catalogStateFixture {
	t.Helper()
	f := catalogStateFixture{owner: catalogQueryOwner(t)}
	catalogSeedVersions(t, f.owner, 2)
	id, err := domain.NewSourceID()
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := domain.NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	f.source, f.ingest, err = domain.NewSourceRegistration(id, issuer, domain.SourceSpec{Adapter: "test", AdapterVersion: "1", Scope: "offline", Acquisition: domain.CoreOperator, AllowedClaims: []domain.ClaimKey{domain.TitleClaimKey, domain.FingerprintClaimKey}})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := domain.NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	f.review, err = domain.NewReviewCapability(reviewer, f.source)
	if err != nil {
		t.Fatal(err)
	}
	f.namespace, err = domain.NewCatalogOrderNamespace(f.source, "explicit-order/1")
	if err != nil {
		t.Fatal(err)
	}
	f.order, err = domain.NewCatalogOrderCapability(f.review, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	f.subject, err = domain.ContentSubject(catalogQueryContent(t, 1))
	if version {
		f.subject, err = domain.VersionSubject(catalogQueryContent(t, 1), catalogQueryVersion(t, 1))
	}
	if err != nil {
		t.Fatal(err)
	}
	spec := f.source.Spec()
	catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_sources VALUES (?,?,?,?,?,?,?,0)", f.source.ID().String(), f.source.Revision().String(), f.source.Issuer().String(), spec.Adapter, spec.AdapterVersion, spec.Scope, spec.Acquisition)
	for _, key := range spec.AllowedClaims {
		catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_source_claims VALUES (?,?,?,?)", f.source.ID().String(), f.source.Revision().String(), spec.Acquisition, key)
	}
	catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_managed VALUES (?,1,0)", f.subject.ContentID().String())
	var vid any
	if version {
		vid = f.subject.VersionID().String()
	}
	catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_subjects VALUES (?,?,?,1)", catalogSubjectKey(f.subject), f.subject.ContentID().String(), vid)
	for i := 1; i <= 2; i++ {
		audio, _ := domain.NewAudioID(123)
		hash, _ := domain.ParseAudioHash(strings.Repeat("01", 20))
		pair, _ := domain.NewAudioFingerprint(audio, hash)
		v, err := domain.NewContentVersion(catalogQueryVersion(t, i), f.subject.ContentID(), pair, domain.UnknownVersionOrderEvidence())
		if err != nil {
			t.Fatal(err)
		}
		f.versions = append(f.versions, catalogVersionRow{Version: v})
	}
	return f
}

func (f catalogStateFixture) coordinate(t *testing.T) domain.RecordCoordinate {
	t.Helper()
	id, err := domain.NewBatchID()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"sanitized":true}`)
	digest := sha256.Sum256(raw)
	artifact, err := domain.NewArtifact(f.ingest, f.source, domain.ArtifactFacts{ID: id, SHA256: digest, ByteLength: uint64(len(raw)), RecordCount: 1, ParserRevision: "1", SourceRevision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := domain.NewRecordSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, err := domain.NewRecordCoordinate(artifact, 0, "record-0", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return coordinate
}

func (f catalogStateFixture) insertCoordinate(t *testing.T, batch string, c domain.RecordCoordinate) {
	t.Helper()
	facts := c.Artifact().Facts()
	catalogSchemaExec(t, f.owner.database.db, "INSERT OR IGNORE INTO tc_catalog_batches VALUES (?,?,?,?,?,?,?,?)", batch, f.source.ID().String(), f.source.Revision().String(), facts.SHA256[:], facts.ByteLength, facts.RecordCount, facts.ParserRevision, facts.SourceRevision)
	catalogSchemaExec(t, f.owner.database.db, "INSERT OR IGNORE INTO tc_catalog_records VALUES (?,?,?,?)", batch, c.Index(), c.Locator(), c.Snapshot().Bytes())
}

func reviewStateParts(t *testing.T, raw []byte) catalogStoredState {
	t.Helper()
	state := catalogStoredState{raw: raw}
	d := catalogStateReader{raw: raw}
	d.text()
	d.content = d.text()
	version := d.text()
	d.subject = d.content
	if version != "" {
		d.subject = version
	}
	state.revision = d.number()
	d.number()
	d.number()
	for n := d.count(domain.RetainedObservations.Maximum()); n > 0; n-- {
		d.observation()
	}
	for n := d.count(domain.RetainedDecisions.Maximum()); n > 0; n-- {
		state.decisions = append(state.decisions, d.reviewDecision())
	}
	if d.err != nil || d.offset != len(raw) {
		t.Fatal("fixture encoding projection", d.err)
	}
	return state
}

func (f catalogStateFixture) storeReview(t *testing.T, state domain.CatalogReviewState, aggregate uint64) {
	t.Helper()
	raw, err := state.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_managed SET aggregate_revision=? WHERE content_id=?", aggregate, f.subject.ContentID().String())
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_subjects SET review_revision=? WHERE subject_id=?", state.Revision().Uint64(), catalogSubjectKey(f.subject))
	catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_review_states VALUES (?,?,?,?,1,?,?,?)", catalogSubjectKey(f.subject), f.subject.ContentID().String(), aggregate, state.Revision().Uint64(), raw, len(raw), sum[:])
}

func (f catalogStateFixture) reviewHistory(t *testing.T) (domain.CatalogReviewState, app.ReviewOperation, app.ReviewResult) {
	t.Helper()
	state, err := domain.NewCatalogReviewState(f.subject, false)
	if err != nil {
		t.Fatal(err)
	}
	f.storeReview(t, state, 1)
	claim, _ := domain.TitleClaim("reviewed title")
	if !f.subject.VersionID().IsZero() {
		claim, err = domain.FingerprintClaim(f.versions[0].Version.Fingerprint())
		if err != nil {
			t.Fatal(err)
		}
	}
	id, err := evidence.ParseID(catalogSchemaID("obs", 1))
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.NewCatalogObservation(f.ingest, f.source, domain.ObservationInput{ID: id, Subject: f.subject, Claim: claim, Coordinate: f.coordinate(t), ObservedAt: time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC), Confidence: evidence.Confidence(1)})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := domain.NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	command := domain.CatalogReviewCommand{ID: commandID, Kind: domain.RecordObservations, Subject: f.subject, Source: f.source, Expected: state.Revision(), Reason: "retain evidence", Observations: []domain.CatalogObservation{observation}}
	var original app.ReviewResult
	var operation app.ReviewOperation
	for i := 0; i < 2; i++ {
		before := state.Revision()
		next, result, err := state.Apply(f.review, command)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := next.PersistenceEncoding()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			d := catalogStateReader{raw: raw}
			d.text()
			d.text()
			d.text()
			d.number()
			d.number()
			d.number()
			d.number()
			d.text()
			d.text()
			d.text()
			d.claim()
			d.source()
			batch := d.text()
			f.insertCoordinate(t, batch, observation.Input().Coordinate)
			var text, audio, hash any
			if claim.Key() == domain.TitleClaimKey {
				text = claim.Text()
			} else {
				var b [8]byte
				binary.BigEndian.PutUint64(b[:], claim.Fingerprint().AudioID().Uint64())
				audio = b[:]
				h, err := hex.DecodeString(claim.Fingerprint().Hash().String())
				if err != nil {
					t.Fatal(err)
				}
				hash = h
			}
			input := observation.Input()
			catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_observations VALUES (?,?,?,?,?,?,?,NULL,?,?,?,?,?,?,?)", id.String(), catalogSubjectKey(f.subject), f.subject.ContentID().String(), claim.Key(), text, audio, hash, batch, 0, f.source.ID().String(), f.source.Revision().String(), input.AssertedOrigin, input.ObservedAt.Format(time.RFC3339Nano), input.Confidence)
		}
		parts := reviewStateParts(t, raw)
		decision := parts.decisions[len(parts.decisions)-1]
		catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_decisions VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", command.ID.String(), catalogSubjectKey(f.subject), f.subject.ContentID().String(), f.source.ID().String(), f.source.Revision().String(), f.review.Reviewer().String(), domain.CatalogDecisionPolicy, command.Kind, command.Key, i+1, i+2, before.Uint64(), result.Revision.Uint64(), command.Reason, decision.command, decision.result)
		for _, support := range command.Support {
			catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)", catalogSubjectKey(f.subject), f.source.ID().String(), f.source.Revision().String(), command.Key, command.ID.String(), support.String())
		}
		state = next
		f.storeReview(t, state, uint64(i+2))
		if i == 0 {
			aggregate, _ := domain.NewCatalogRevision(1)
			operation = app.ReviewOperation{ExpectedAggregate: aggregate, Capability: f.review, Command: command}
			revision, _ := domain.NewCatalogRevision(2)
			original = app.ReviewResult{AggregateRevision: revision, Decision: result}
		}
		commandID, err = domain.NewCatalogCommandID()
		if err != nil {
			t.Fatal(err)
		}
		command = domain.CatalogReviewCommand{ID: commandID, Kind: domain.AcceptSupport, Subject: f.subject, Source: f.source, Expected: state.Revision(), Key: claim.Key(), Support: []evidence.ID{id}, Reason: "explicit acceptance"}
	}
	return state, operation, original
}

func (f catalogStateFixture) storeOrder(t *testing.T, state domain.CatalogOrderState, aggregate uint64) {
	t.Helper()
	raw, err := state.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_managed SET aggregate_revision=? WHERE content_id=?", aggregate, f.subject.ContentID().String())
	catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_order_states VALUES (?,?,?,1,?,?,?)", f.subject.ContentID().String(), aggregate, state.Revision().Uint64(), raw, len(raw), sum[:])
	for _, v := range f.versions {
		catalogSchemaExec(t, f.owner.database.db, "INSERT INTO tc_catalog_order_state_versions VALUES (?,?,?)", f.subject.ContentID().String(), state.Revision().Uint64(), v.Version.ID().String())
	}
}

func (f catalogStateFixture) orderHistory(t *testing.T) (domain.CatalogOrderState, app.OrderOperation, app.OrderResult) {
	t.Helper()
	var versions []domain.ContentVersion
	for _, v := range f.versions {
		versions = append(versions, v.Version)
	}
	state, err := domain.NewCatalogOrderState(f.subject.ContentID(), versions)
	if err != nil {
		t.Fatal(err)
	}
	f.storeOrder(t, state, 1)
	coordinate := f.coordinate(t)
	id, err := evidence.ParseID(catalogSchemaID("obs", 1))
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.NewCatalogSequenceEvidence(f.ingest, f.namespace, domain.CatalogSequenceInput{ID: id, Content: f.subject.ContentID(), Version: versions[0].ID(), Position: 7, Coordinate: coordinate})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := domain.NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	command := domain.CatalogOrderCommand{ID: commandID, Content: f.subject.ContentID(), Namespace: f.namespace, Expected: state.Revision(), Reason: "explicit order", Evidence: []domain.CatalogSequenceEvidence{observation}, Entries: []domain.CatalogOrderEntry{{Version: versions[0].ID(), Position: 7, Support: []evidence.ID{id}}}}
	state, result, err := state.Apply(f.order, command)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := state.PersistenceEncoding()
	if err != nil {
		t.Fatal(err)
	}
	d := catalogStateReader{raw: raw}
	d.text()
	d.text()
	d.number()
	d.number()
	for n := d.count(1024); n > 0; n-- {
		d.text()
		d.text()
		d.number()
		d.text()
		d.text()
		d.number()
		d.number()
	}
	d.number()
	d.namespace()
	d.text()
	d.text()
	d.text()
	d.number()
	batch := fmt.Sprintf("%x", d.take(16))
	f.insertCoordinate(t, batch, coordinate)
	f.storeOrder(t, state, 2)
	// A later decision must not replace the original operation envelope.
	second := command
	second.ID, err = domain.NewCatalogCommandID()
	if err != nil {
		t.Fatal(err)
	}
	second.Expected = state.Revision()
	second.Evidence = nil
	second.Reason = "reaffirm stored support"
	state, _, err = state.Apply(f.order, second)
	if err != nil {
		t.Fatal(err)
	}
	f.storeOrder(t, state, 3)
	before, _ := domain.NewCatalogRevision(1)
	after, _ := domain.NewCatalogRevision(2)
	return state, app.OrderOperation{ExpectedAggregate: before, Capability: f.order, Command: command}, app.OrderResult{AggregateRevision: after, Decision: result}
}

func TestCatalogStateReadReviewRoundTrip(t *testing.T) {
	for _, version := range []bool{false, true} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := newCatalogStateFixture(t, version)
			want, operation, original := f.reviewHistory(t)
			err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
				snapshot, found, err := r.LoadReview(t.Context(), f.subject)
				if err != nil || !found {
					t.Fatal("load", found, err)
				}
				gotRaw, _ := snapshot.State.PersistenceEncoding()
				wantRaw, _ := want.PersistenceEncoding()
				if !bytes.Equal(gotRaw, wantRaw) || snapshot.AggregateRevision.Uint64() != 3 {
					t.Fatal("review lost facts")
				}
				if snapshot.State.Observations()[0].Input().Coordinate.Artifact().Source().Active() {
					t.Fatal("storage minted authority")
				}
				got, found, err := r.LookupReview(t.Context(), operation)
				if err != nil || !found || !reflect.DeepEqual(got, original) {
					t.Fatal("original replay", found, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogStateReadOrderRoundTrip(t *testing.T) {
	f := newCatalogStateFixture(t, false)
	want, operation, original := f.orderHistory(t)
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		snapshot, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
		if err != nil || !found {
			t.Fatal("load", found, err)
		}
		gotRaw, _ := snapshot.State.PersistenceEncoding()
		wantRaw, _ := want.PersistenceEncoding()
		if !bytes.Equal(gotRaw, wantRaw) || snapshot.AggregateRevision.Uint64() != 3 {
			t.Fatal("order lost facts")
		}
		got, found, err := r.LookupOrder(t.Context(), operation)
		if err != nil || !found || !reflect.DeepEqual(got, original) {
			t.Fatal("original order replay", found, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCatalogStateReadCleanMissing(t *testing.T) {
	f := newCatalogStateFixture(t, false)
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		for _, subject := range []domain.CatalogSubject{f.subject, func() domain.CatalogSubject { s, _ := domain.ContentSubject(catalogQueryContent(t, 99)); return s }()} {
			s, found, err := r.LoadReview(t.Context(), subject)
			if err != nil || found || !reflect.DeepEqual(s, app.ReviewSnapshot{}) {
				t.Fatal("review miss", found, err)
			}
		}
		s, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
		if err != nil || found || !reflect.DeepEqual(s, app.OrderSnapshot{}) {
			t.Fatal("order miss", found, err)
		}
		id, _ := domain.NewCatalogCommandID()
		rev, _ := domain.NewCatalogRevision(1)
		review := app.ReviewOperation{ExpectedAggregate: rev, Capability: f.review, Command: domain.CatalogReviewCommand{ID: id, Subject: f.subject, Source: f.source, Kind: domain.AcceptSupport, Expected: rev, Key: domain.TitleClaimKey, Reason: "unknown", Support: []evidence.ID{func() evidence.ID { id, _ := evidence.ParseID(catalogSchemaID("obs", 99)); return id }()}}}
		got, found, err := r.LookupReview(t.Context(), review)
		if err != nil || found || !reflect.DeepEqual(got, app.ReviewResult{}) {
			t.Fatal("review lookup miss", found, err)
		}
		order := app.OrderOperation{ExpectedAggregate: rev, Capability: f.order, Command: domain.CatalogOrderCommand{ID: id, Content: f.subject.ContentID(), Namespace: f.namespace, Expected: rev, Reason: "unknown", Entries: []domain.CatalogOrderEntry{{Version: f.versions[0].Version.ID(), Support: review.Command.Support}}}}
		gotOrder, found, err := r.LookupOrder(t.Context(), order)
		if err != nil || found || !reflect.DeepEqual(gotOrder, app.OrderResult{}) {
			t.Fatal("order lookup miss", found, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func corruptCatalogState(t *testing.T, f catalogStateFixture, query string, args ...any) {
	t.Helper()
	// Only disposable fixtures bypass constraints. Production connections remain
	// query_only and never execute these statements.
	rows, err := f.owner.database.db.QueryContext(t.Context(), "SELECT name FROM sqlite_schema WHERE type='trigger' AND name LIKE 'tc_catalog_%'")
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
		catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER "+name)
	}
	catalogSchemaExec(t, f.owner.database.db, "PRAGMA foreign_keys=OFF")
	catalogSchemaExec(t, f.owner.database.db, "PRAGMA ignore_check_constraints=ON")
	catalogSchemaExec(t, f.owner.database.db, query, args...)
}

func TestCatalogStateReadCorruptReview(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"digest", "UPDATE tc_catalog_review_states SET state_sha256=zeroblob(32) WHERE review_revision=3"},
		{"length", "UPDATE tc_catalog_review_states SET state_bytes=state_bytes+1 WHERE review_revision=3"},
		{"encoding", "UPDATE tc_catalog_review_states SET encoding=2 WHERE review_revision=3"},
		{"content", "UPDATE tc_catalog_review_states SET content_id='cnt_00000000000000000000000002' WHERE review_revision=3"},
		{"subject", "UPDATE tc_catalog_review_states SET subject_id='ver_00000000000000000000000001' WHERE review_revision=3"},
		{"revision", "UPDATE tc_catalog_subjects SET review_revision=2"},
		{"state revision", "UPDATE tc_catalog_review_states SET review_revision=4 WHERE review_revision=3"},
		{"protection", "UPDATE tc_catalog_managed SET protected_custom=1"},
		{"aggregate", "UPDATE tc_catalog_managed SET aggregate_revision=4"},
		{"source", "DELETE FROM tc_catalog_sources"},
		{"source binding", "UPDATE tc_catalog_sources SET adapter='spoof'"},
		{"source claim", "DELETE FROM tc_catalog_source_claims WHERE claim_key=1"},
		{"batch", "DELETE FROM tc_catalog_batches"},
		{"record", "DELETE FROM tc_catalog_records"},
		{"observation", "DELETE FROM tc_catalog_observations"},
		{"observation facts", "UPDATE tc_catalog_observations SET text_value='different'"},
		{"pending observation", "INSERT INTO tc_catalog_observations SELECT 'obs_00000000000000000000000099',subject_id,content_id,claim_key,text_value,audio_id,audio_sha1,owner_content_id,batch_id,record_index,source_id,registration_revision,asserted_origin,observed_at,confidence FROM tc_catalog_observations"},
		{"decision", "DELETE FROM tc_catalog_decisions WHERE kind=1"},
		{"support", "DELETE FROM tc_catalog_decision_support"},
		{"command", "UPDATE tc_catalog_decisions SET canonical_command=x'00'"},
		{"result", "UPDATE tc_catalog_decisions SET canonical_result=x'00'"},
		{"stale", "DELETE FROM tc_catalog_review_states WHERE review_revision=3"},
		{"absent envelope with history", "DELETE FROM tc_catalog_review_states"},
		{"duplicate history", "CREATE TABLE broken_review AS SELECT * FROM tc_catalog_review_states; DROP TABLE tc_catalog_review_states; ALTER TABLE broken_review RENAME TO tc_catalog_review_states; INSERT INTO tc_catalog_review_states SELECT * FROM tc_catalog_review_states WHERE review_revision=3"},
		{"row bound", "CREATE TABLE broken_review AS SELECT * FROM tc_catalog_review_states; DROP TABLE tc_catalog_review_states; ALTER TABLE broken_review RENAME TO tc_catalog_review_states; INSERT INTO tc_catalog_review_states WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1026) SELECT s.* FROM tc_catalog_review_states s,n WHERE s.review_revision=1"},
		{"byte bound", "UPDATE tc_catalog_review_states SET state=zeroblob(8388609) WHERE review_revision=3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCatalogStateFixture(t, false)
			_, operation, _ := f.reviewHistory(t)
			corruptCatalogState(t, f, tc.sql)
			err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
				s, found, err := r.LoadReview(t.Context(), f.subject)
				if !errors.Is(err, app.ErrRepositoryUnavailable) || found || !reflect.DeepEqual(s, app.ReviewSnapshot{}) {
					t.Fatalf("corruption became state/miss: %v %v", found, err)
				}
				result, found, err := r.LookupReview(t.Context(), operation)
				if !errors.Is(err, app.ErrRepositoryUnavailable) || found || !reflect.DeepEqual(result, app.ReviewResult{}) {
					t.Fatal("corrupt replay", found, err)
				}
				return err
			})
			if !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogStateReadCorruptOrder(t *testing.T) {
	for _, query := range []string{
		"UPDATE tc_catalog_order_states SET state_sha256=zeroblob(32) WHERE order_revision=2",
		"UPDATE tc_catalog_order_states SET state_bytes=0 WHERE order_revision=2",
		"UPDATE tc_catalog_order_states SET encoding=2 WHERE order_revision=2",
		"DELETE FROM tc_catalog_order_states WHERE order_revision=1",
		"DELETE FROM tc_catalog_order_state_versions WHERE order_revision=2",
		"INSERT INTO tc_content_versions VALUES ('ver_00000000000000000000000003','cnt_00000000000000000000000001',123,zeroblob(20),0,NULL,NULL)",
		"DELETE FROM tc_content_versions",
		"UPDATE tc_content_versions SET audio_id=124",
		"DELETE FROM tc_catalog_sources",
		"DELETE FROM tc_catalog_batches",
		"DELETE FROM tc_catalog_records",
		"UPDATE tc_catalog_records SET snapshot=x'7b7d'",
		"UPDATE tc_catalog_managed SET aggregate_revision=4",
	} {
		t.Run(query, func(t *testing.T) {
			f := newCatalogStateFixture(t, false)
			_, operation, _ := f.orderHistory(t)
			corruptCatalogState(t, f, query)
			err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
				s, found, err := r.LoadOrder(t.Context(), f.subject.ContentID())
				if !errors.Is(err, app.ErrRepositoryUnavailable) || found || !reflect.DeepEqual(s, app.OrderSnapshot{}) {
					t.Fatal("corrupt order", found, err)
				}
				result, found, err := r.LookupOrder(t.Context(), operation)
				if !errors.Is(err, app.ErrRepositoryUnavailable) || found || !reflect.DeepEqual(result, app.OrderResult{}) {
					t.Fatal("corrupt order replay", found, err)
				}
				return err
			})
			if !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogStateReadRejectsValidDigestInvalidBytes(t *testing.T) {
	for _, order := range []bool{false, true} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			f := newCatalogStateFixture(t, false)
			table, column := "tc_catalog_review_states", "review_revision"
			var raw []byte
			if order {
				state, _, _ := f.orderHistory(t)
				raw, _ = state.PersistenceEncoding()
				table, column = "tc_catalog_order_states", "order_revision"
			} else {
				state, _, _ := f.reviewHistory(t)
				raw, _ = state.PersistenceEncoding()
			}
			raw = append(raw, 0)
			digest := sha256.Sum256(raw)
			corruptCatalogState(t, f, "UPDATE "+table+" SET state=?,state_bytes=?,state_sha256=? WHERE "+column+"=3", raw, len(raw), digest[:])
			err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
				if order {
					_, _, err := r.LoadOrder(t.Context(), f.subject.ContentID())
					return err
				}
				_, _, err := r.LoadReview(t.Context(), f.subject)
				return err
			})
			if !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal("valid SHA accepted invalid encoding", err)
			}
		})
	}
}

func TestCatalogStateReadReplayConflicts(t *testing.T) {
	for _, order := range []bool{false, true} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			f := newCatalogStateFixture(t, false)
			var review app.ReviewOperation
			var sequence app.OrderOperation
			if order {
				_, sequence, _ = f.orderHistory(t)
			} else {
				_, review, _ = f.reviewHistory(t)
			}
			for _, aggregate := range []bool{false, true} {
				err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
					if order {
						operation := sequence
						if aggregate {
							operation.ExpectedAggregate, _ = domain.NewCatalogRevision(2)
						} else {
							operation.Command.Reason += " changed"
						}
						_, _, err := r.LookupOrder(t.Context(), operation)
						return err
					}
					operation := review
					if aggregate {
						operation.ExpectedAggregate, _ = domain.NewCatalogRevision(2)
					} else {
						operation.Command.Reason += " changed"
					}
					_, _, err := r.LookupReview(t.Context(), operation)
					return err
				})
				if !errors.Is(err, domain.ErrCatalogCommandConflict) {
					t.Fatal("changed replay", err)
				}
			}
			f.source.Revoke()
			err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
				if order {
					_, _, err := r.LookupOrder(t.Context(), sequence)
					return err
				}
				_, _, err := r.LookupReview(t.Context(), review)
				return err
			})
			if !errors.Is(err, domain.ErrCatalogUnauthorized) {
				t.Fatal("live source revocation", err)
			}
		})
	}
}

func TestCatalogStateReadFences(t *testing.T) {
	f := newCatalogStateFixture(t, false)
	_, operation, _ := f.reviewHistory(t)
	var escaped app.CatalogRepository
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		escaped = r
		if err := r.CommitOrder(t.Context(), app.OrderOperation{}, domain.CatalogOrderState{}, app.OrderResult{}); !errors.Is(err, app.ErrUnsupported) {
			t.Fatal(err)
		}
		if _, err := r.(*catalogRepository).transaction.ExecContext(t.Context(), "DELETE FROM tc_catalog_review_states"); err == nil {
			t.Fatal("write escaped query_only")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := escaped.LoadReview(t.Context(), f.subject); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatal("escaped review", err)
	}
	if _, _, err := escaped.LoadOrder(t.Context(), f.subject.ContentID()); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatal("escaped order", err)
	}
	if _, _, err := escaped.LookupReview(t.Context(), operation); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatal("escaped lookup", err)
	}
	if _, _, err := escaped.LookupOrder(t.Context(), app.OrderOperation{}); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatal("escaped lookup", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error { _, _, err := r.LoadReview(ctx, f.subject); return err })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancel", err)
	}
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		f.owner.generation.Add(1)
		_, _, err := r.LoadReview(t.Context(), f.subject)
		return err
	})
	if !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatal("generation", err)
	}
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_sources SET revoked=1")
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error { _, _, err := r.LookupReview(t.Context(), operation); return err })
	if !errors.Is(err, domain.ErrCatalogUnauthorized) {
		t.Fatal("revoked source", err)
	}
}

func TestCatalogStateReadV5Unsupported(t *testing.T) {
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v5.sqlite")}, SchemaMigrations()[:5])
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		subject, _ := domain.ContentSubject(catalogQueryContent(t, 1))
		_, _, a := r.LoadReview(t.Context(), subject)
		_, _, b := r.LoadOrder(t.Context(), subject.ContentID())
		_, _, c := r.LookupReview(t.Context(), app.ReviewOperation{})
		_, _, d := r.LookupOrder(t.Context(), app.OrderOperation{})
		for _, err := range []error{a, b, c, d} {
			if !errors.Is(err, app.ErrUnsupported) {
				t.Fatal(err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
