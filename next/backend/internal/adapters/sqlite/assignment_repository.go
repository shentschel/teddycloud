package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	c "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// assignmentRepository is an inert transaction-bound component. Only the private
// callback scope and test harnesses instantiate it. The caller supplies and owns
// the transaction and its lifetime; this type implements neither Port nor
// Transactor and exposes no writer. There is no runtime constructor or schema
// activation in this checkpoint. Calls must be serialized by the scope. No
// dependency or decoded state is cached across reads or guarded appends.
type assignmentRepository struct{ transaction *sql.Tx }

var assignmentHistoryInstances struct {
	next atomic.Uint64
	keys sync.Map // weak.Pointer[LifecycleOwner] -> uint64
}

// AssignmentHistoryScope is sampled by the history reader under the existing
// assignment fence. Weak owner identities prevent allocator address reuse
// from reviving cursors after close/reopen without retaining closed owners.
func (owner *LifecycleOwner) AssignmentHistoryScope() (uint64, uint64) {
	if owner == nil {
		return 0, 0
	}
	key := weak.Make(owner)
	instance, ok := assignmentHistoryInstances.keys.Load(key)
	if !ok {
		var loaded bool
		instance, loaded = assignmentHistoryInstances.keys.LoadOrStore(key, assignmentHistoryInstances.next.Add(1))
		if !loaded {
			runtime.AddCleanup(owner, func(key weak.Pointer[LifecycleOwner]) {
				assignmentHistoryInstances.keys.Delete(key)
			}, key)
		}
	}
	return instance.(uint64), owner.generation.Load()
}

func assignmentReadError(ctx context.Context, err error) error {
	if err == nil {
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return app.ErrUnavailable
}

// Every entry point admits exactly the staged ledger, not v10 disguised as v9.
// Validate the complete migration ledger and FKs before returning even absence.
func (r *assignmentRepository) check(ctx context.Context) error {
	if ctx == nil {
		return app.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.transaction == nil {
		return app.ErrUnavailable
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT version,migration_id,checksum,dirty FROM tc_schema_migrations ORDER BY version`)
	if err != nil {
		return assignmentReadError(ctx, err)
	}
	migrations := SchemaMigrations()
	i := 0
	for rows.Next() {
		var version, dirty int
		var id, sum string
		if err = rows.Scan(&version, &id, &sum, &dirty); err != nil {
			break
		}
		if i >= len(migrations) || version != migrations[i].Version || id != migrations[i].ID || sum != migrationChecksum(migrations[i]) || dirty != 0 {
			rows.Close()
			return app.ErrUnsupportedState
		}
		i++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return assignmentReadError(ctx, err)
	}
	if i != 10 {
		return app.ErrUnsupportedState
	}
	var corrupt bool
	err = r.transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_foreign_key_check LIMIT 1)`).Scan(&corrupt)
	if err != nil {
		return assignmentReadError(ctx, err)
	}
	if corrupt {
		return app.ErrUnavailable
	}
	return nil
}

func (r *assignmentRepository) Tag(ctx context.Context, id identity.TagID) (app.TagSnapshot, bool, error) {
	if err := r.check(ctx); err != nil {
		return app.TagSnapshot{}, false, err
	}
	if id.IsZero() {
		return app.TagSnapshot{}, false, app.ErrInvalidInput
	}
	// Reuse the exact metadata reconstruction, never its transactor or Save.
	value, found, err := (&tagRepository{transaction: r.transaction}).FindByID(ctx, id)
	if err != nil {
		return app.TagSnapshot{}, false, assignmentReadError(ctx, err)
	}
	if !found {
		return app.TagSnapshot{}, false, nil
	}
	return app.TagSnapshot{ID: value.ID(), Revision: uint64(value.Revision())}, true, nil
}

// These fixed-order envelopes reproduce A1's TCAS/1 bytes. They are private
// storage details, not a public command codec. Decode then re-encode and compare
// full bytes: a matching digest alone is never canonical/semantic validation.
type assignmentInput struct {
	Format                                         string
	Command, Assignment, Tag                       string
	Kind                                           app.Kind
	Expected, TagRevision, CatalogRevision, Policy uint64
	Actor, Reason                                  string
	Effective                                      string
	Content, Version, Review                       string
	Evidence                                       []byte
	Classification                                 d.PhysicalClassification
	Support                                        []string
	URI                                            d.SourceURIEncoding
}
type assignmentResult struct {
	Command, Assignment, Tag string
	Revision                 uint64
	Previous                 string
	Input                    json.RawMessage
}
type assignmentEvent struct {
	Input     json.RawMessage
	Result    json.RawMessage
	At        string
	Primitive []byte
}
type assignmentTransition struct {
	Revision             uint64
	Kind                 app.Kind
	At, Content, Version string
}
type assignmentProjection struct {
	Format, Assignment, Tag string
	Head                    uint64
	Retired                 bool
	Classification          d.PhysicalClassification
	OriginalState           d.OriginalTargetState
	Content, Version        string
	Transitions             []assignmentTransition
}

func assignmentEncodeEvent(event app.Event) (command, result, raw []byte, err error) {
	in := event.Command
	input := assignmentInput{Format: "TCAS/1", Command: in.CommandID.String(), Assignment: in.AssignmentID.String(), Tag: in.TagID.String(), Kind: in.Kind, Expected: in.ExpectedRevision, TagRevision: in.ExpectedTagRevision, CatalogRevision: in.ExpectedCatalogRevision, Policy: in.PolicyRevision, Actor: event.Actor, Reason: in.Reason, Content: in.Target.ContentID().String(), Version: in.Target.VersionID().String(), Review: in.ReviewID.String(), Classification: in.Classification, Support: []string{}, URI: in.URI.Encoding()}
	if !in.EffectiveAt.IsZero() {
		input.Effective = in.EffectiveAt.Format(time.RFC3339Nano)
	}
	if in.Evidence != (d.Evidence{}) {
		input.Evidence, err = in.Evidence.CanonicalBytes()
		if err != nil {
			return nil, nil, nil, app.ErrUnavailable
		}
	}
	for _, id := range in.Support {
		input.Support = append(input.Support, id.String())
	}
	command, err = json.Marshal(input)
	if err != nil {
		return nil, nil, nil, app.ErrUnavailable
	}
	result, err = json.Marshal(assignmentResult{event.Result.CommandID.String(), event.Result.AssignmentID.String(), event.Result.TagID.String(), event.Result.Revision, event.Result.PreviousCommand.String(), command})
	if err != nil {
		return nil, nil, nil, app.ErrUnavailable
	}
	raw, err = json.Marshal(assignmentEvent{command, result, event.RecordedAt.Format(time.RFC3339Nano), event.Primitive})
	if err != nil {
		return nil, nil, nil, app.ErrUnavailable
	}
	decoded, err := assignmentDecodeEvent(raw)
	if err != nil || !reflect.DeepEqual(decoded, event) {
		return nil, nil, nil, app.ErrUnavailable
	}
	return command, result, raw, nil
}

func assignmentJSON(raw []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return app.ErrUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF {
		return app.ErrUnavailable
	}
	canonical, err := json.Marshal(v)
	if err != nil || !bytes.Equal(canonical, raw) {
		return app.ErrUnavailable
	}
	return nil
}
func assignmentDigest(raw, digest []byte) bool {
	sum := sha256.Sum256(raw)
	return len(raw) > 0 && uint64(len(raw)) <= d.MaxEvidenceRetainedBytes && bytes.Equal(digest, sum[:])
}

// Evidence has only bounded framed text, two enum bytes and no recursive data.
func assignmentDecodeEvidence(raw []byte) (d.Evidence, error) {
	zero := d.Evidence{}
	if len(raw) < 6 || !bytes.Equal(raw[:6], []byte{'T', 'C', 'A', 'E', 1, 1}) {
		return zero, app.ErrUnavailable
	}
	offset := 6
	text := func(max int) (string, bool) {
		if len(raw)-offset < 4 {
			return "", false
		}
		n := int(binary.BigEndian.Uint32(raw[offset:]))
		offset += 4
		if n > max || n > len(raw)-offset {
			return "", false
		}
		s := string(raw[offset : offset+n])
		offset += n
		return s, true
	}
	idText, ok := text(30)
	if !ok {
		return zero, app.ErrUnavailable
	}
	tagText, ok := text(30)
	if !ok || offset >= len(raw) {
		return zero, app.ErrUnavailable
	}
	kind := d.EvidenceKind(raw[offset])
	offset++
	source, ok := text(d.MaxReviewReferenceBytes)
	if !ok || len(raw)-offset != 1 {
		return zero, app.ErrUnavailable
	}
	id, err := d.ParseEvidenceID(idText)
	if err != nil {
		return zero, app.ErrUnavailable
	}
	tag, err := identity.ParseTagID(tagText)
	if err != nil {
		return zero, app.ErrUnavailable
	}
	value, err := d.NewEvidence(id, tag, kind, source, d.EvidenceDisposition(raw[offset]))
	if err != nil {
		return zero, app.ErrUnavailable
	}
	encoded, err := value.CanonicalBytes()
	if err != nil || !bytes.Equal(raw, encoded) {
		return zero, app.ErrUnavailable
	}
	return value, nil
}

func assignmentDecodeEvent(raw []byte) (app.Event, error) {
	var envelope assignmentEvent
	var input assignmentInput
	var result assignmentResult
	bad := func() (app.Event, error) { return app.Event{}, app.ErrUnavailable }
	if assignmentJSON(raw, &envelope) != nil || assignmentJSON(envelope.Input, &input) != nil || assignmentJSON(envelope.Result, &result) != nil {
		return bad()
	}
	if input.Format != "TCAS/1" || !bytes.Equal(result.Input, envelope.Input) || input.Policy != app.PolicyRevision || input.TagRevision == 0 || input.TagRevision > d.MaxDecisionRevision || len(input.Support) > int(d.MaxSelectedSupport) || input.Support == nil {
		return bad()
	}
	var event app.Event
	in := &event.Command
	var err error
	in.CommandID, err = d.ParseCommandID(input.Command)
	if err != nil {
		return bad()
	}
	in.AssignmentID, err = d.ParseID(input.Assignment)
	if err != nil {
		return bad()
	}
	in.TagID, err = identity.ParseTagID(input.Tag)
	if err != nil {
		return bad()
	}
	in.Kind = input.Kind
	in.URI, err = app.RestoreOptionalURI(input.URI)
	if err != nil || (in.URI.Present && in.Kind != app.Assign) {
		return bad()
	}
	in.ExpectedRevision = input.Expected
	in.ExpectedTagRevision = input.TagRevision
	in.ExpectedCatalogRevision = input.CatalogRevision
	in.PolicyRevision = input.Policy
	in.Reason = input.Reason
	event.Actor = input.Actor
	if input.Effective != "" {
		in.EffectiveAt, err = time.Parse(time.RFC3339Nano, input.Effective)
		if err != nil || in.EffectiveAt.Location() != time.UTC || in.EffectiveAt.Format(time.RFC3339Nano) != input.Effective {
			return bad()
		}
	}
	target := in.Kind == app.Assign || in.Kind == app.RecordOriginalTarget || in.Kind == app.ResolveOriginalTarget
	if target {
		content, e := identity.ParseContentID(input.Content)
		if e != nil {
			return bad()
		}
		version, e := identity.ParseContentVersionID(input.Version)
		if e != nil {
			return bad()
		}
		in.Target, err = d.NewOriginalTarget(content, version)
		if err != nil || input.CatalogRevision == 0 || input.CatalogRevision > d.MaxDecisionRevision {
			return bad()
		}
	} else if input.Content != "" || input.Version != "" || input.CatalogRevision != 0 {
		return bad()
	}
	if input.Review != "" {
		in.ReviewID, err = d.ParseReviewID(input.Review)
		if err != nil {
			return bad()
		}
	}
	if input.Evidence != nil {
		in.Evidence, err = assignmentDecodeEvidence(input.Evidence)
		if err != nil {
			return bad()
		}
	}
	in.Classification = input.Classification
	for i, s := range input.Support {
		id, e := d.ParseEvidenceID(s)
		if e != nil || i > 0 && s <= input.Support[i-1] {
			return bad()
		}
		in.Support = append(in.Support, id)
	}
	event.RecordedAt, err = time.Parse(time.RFC3339Nano, envelope.At)
	if err != nil || event.RecordedAt.IsZero() || event.RecordedAt.Location() != time.UTC || event.RecordedAt.Format(time.RFC3339Nano) != envelope.At || event.RecordedAt.Year() < 1 || event.RecordedAt.Year() > 9999 {
		return bad()
	}
	if result.Command != input.Command || result.Assignment != input.Assignment || result.Tag != input.Tag || result.Revision != input.Expected+1 || input.Expected >= d.MaxEvidenceEvents {
		return bad()
	}
	event.Result = app.Result{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID, Revision: result.Revision}
	if result.Previous != "" {
		event.Result.PreviousCommand, err = d.ParseCommandID(result.Previous)
		if err != nil {
			return bad()
		}
	}
	event.Primitive = envelope.Primitive
	return event, nil
}

// replay checks all seven command kinds with A1 domain admission. Global
// revisions include reviews; primitive decision/review revisions remain local.
// Usage duplicates A1's conservative COMPLETE charge, without discounts.
type assignmentReplay struct {
	snapshot   app.Snapshot
	decisions  d.DecisionLedger
	reviews    d.TagEvidence
	projection assignmentProjection
}

func newAssignmentReplay(id d.ID, tag identity.TagID) (assignmentReplay, error) {
	decisions, err := d.NewDecisionLedger(id, tag)
	if err != nil {
		return assignmentReplay{}, app.ErrUnavailable
	}
	reviews, err := d.NewTagEvidence(tag)
	if err != nil {
		return assignmentReplay{}, app.ErrUnavailable
	}
	return assignmentReplay{snapshot: app.Snapshot{ID: id, TagID: tag}, decisions: decisions, reviews: reviews, projection: assignmentProjection{Format: "TCAP/1", Assignment: id.String(), Tag: tag.String(), Transitions: []assignmentTransition{}}}, nil
}
func (a *assignmentReplay) append(event app.Event, raw []byte) (d.EvidenceDelta, error) {
	bad := func() (d.EvidenceDelta, error) { return d.EvidenceDelta{}, app.ErrUnavailable }
	in := event.Command
	revision := uint64(len(a.snapshot.Events))
	if a.projection.Retired || in.AssignmentID != a.snapshot.ID || in.TagID != a.snapshot.TagID || in.ExpectedRevision != revision || event.Result.Revision != revision+1 || event.Result.AssignmentID != in.AssignmentID || event.Result.TagID != in.TagID || event.Result.CommandID != in.CommandID || in.PolicyRevision != app.PolicyRevision || in.Actor != "" {
		return bad()
	}
	previous := d.CommandID{}
	if revision > 0 {
		previous = a.snapshot.Events[revision-1].Command.CommandID
	}
	if event.Result.PreviousCommand != previous {
		return bad()
	}
	auth, err := d.BindAuthenticatedReviewAuthority(event.Actor)
	if err != nil {
		return bad()
	}
	delta := d.EvidenceDelta{Events: 1}
	var primitive []byte
	review := in.Kind >= app.RecordClassification && in.Kind <= app.ResolveOriginalTarget
	if review {
		if !in.EffectiveAt.IsZero() || in.ReviewID.IsZero() {
			return bad()
		}
		request, e := d.NewReviewRequest(in.ReviewID, a.reviews.Revision(), in.PolicyRevision, in.Reason)
		if e != nil {
			return bad()
		}
		claim := in.Kind == app.RecordClassification || in.Kind == app.RecordOriginalTarget
		if claim && (len(in.Support) != 0 || in.Evidence.Subject() != in.TagID) || !claim && in.Evidence != (d.Evidence{}) {
			return bad()
		}
		if (in.Kind == app.RecordOriginalTarget || in.Kind == app.ResolveOriginalTarget) && in.Classification != d.Unknown {
			return bad()
		}
		next := a.reviews
		switch in.Kind {
		case app.RecordClassification:
			next, err = a.reviews.AdmitClassification(auth, request, in.Evidence, in.Classification)
		case app.ResolveClassification:
			next, err = a.reviews.ResolveClassification(auth, request, in.Classification, in.Support)
		case app.RecordOriginalTarget:
			next, err = a.reviews.AdmitOriginalTarget(auth, request, in.Evidence, in.Target)
		case app.ResolveOriginalTarget:
			next, err = a.reviews.ResolveOriginalTarget(auth, request, in.Target, in.Support)
		}
		if err != nil {
			return bad()
		}
		records := next.Records()
		record := records[len(records)-1]
		primitive, err = record.CanonicalBytes()
		if claim {
			delta.Observations = 1
		}
		delta.SupportLinks = uint64(len(record.Support))
		delta.RetainedBytes = 1024 + 6*uint64(len(event.Actor)+len(in.Reason)+len(in.Evidence.Source())) + 256*delta.SupportLinks
		if uint64(len(primitive)) > delta.RetainedBytes {
			delta.RetainedBytes = uint64(len(primitive))
		}
		a.reviews = next
	} else {
		if !in.ReviewID.IsZero() || in.Evidence != (d.Evidence{}) || in.Classification != d.Unknown || len(in.Support) != 0 {
			return bad()
		}
		kind := d.DecisionKind(0)
		switch in.Kind {
		case app.Assign:
			kind = d.Assign
		case app.Unassign:
			kind = d.Unassign
		case app.RetireAssignment:
			kind = d.Retire
		default:
			return bad()
		}
		command, e := d.NewDecisionCommand(d.DecisionInput{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID, Kind: kind, ExpectedRevision: a.decisions.Revision(), ExpectedTagRevision: in.ExpectedTagRevision, ExpectedCatalogRevision: in.ExpectedCatalogRevision, PolicyRevision: in.PolicyRevision, Actor: event.Actor, Reason: in.Reason, EffectiveAt: in.EffectiveAt, Target: in.Target})
		if e != nil {
			return bad()
		}
		next, result, e := a.decisions.Admit(command, d.DecisionReferences{TagID: in.TagID, TagRevision: in.ExpectedTagRevision, CatalogRevision: in.ExpectedCatalogRevision, Target: in.Target}, event.RecordedAt)
		if e != nil {
			return bad()
		}
		primitive, err = (d.DecisionEvent{Result: result, RecordedAt: event.RecordedAt}).CanonicalBytes()
		delta.RetainedBytes = uint64(len(primitive))
		a.decisions = next
		a.projection.Transitions = append(a.projection.Transitions, assignmentTransition{event.Result.Revision, in.Kind, in.EffectiveAt.Format(time.RFC3339Nano), in.Target.ContentID().String(), in.Target.VersionID().String()})
	}
	if err != nil || !bytes.Equal(primitive, event.Primitive) {
		return bad()
	}
	var envelope assignmentEvent
	if assignmentJSON(raw, &envelope) != nil {
		return bad()
	}
	delta.RetainedBytes += uint64(len(envelope.Input)+len(envelope.Result)+len(raw)) + 1024
	usage, err := a.snapshot.Usage.Prospective(delta)
	if err != nil {
		return bad()
	}
	a.snapshot.Usage = usage
	a.snapshot.Events = append(a.snapshot.Events, event)
	a.projection.Head = revision + 1
	a.projection.Retired = in.Kind == app.RetireAssignment
	a.projection.Classification = a.reviews.Classification()
	a.projection.OriginalState = a.reviews.OriginalState()
	baseline, _ := a.reviews.OriginalBaseline()
	a.projection.Content = baseline.ContentID().String()
	a.projection.Version = baseline.VersionID().String()
	return delta, nil
}

func (r *assignmentRepository) Load(ctx context.Context, tag identity.TagID) (app.Snapshot, bool, error) {
	if err := r.check(ctx); err != nil {
		return app.Snapshot{}, false, err
	}
	return r.load(ctx, tag)
}
func (r *assignmentRepository) load(ctx context.Context, tag identity.TagID) (app.Snapshot, bool, error) {
	bad := func(err error) (app.Snapshot, bool, error) {
		return app.Snapshot{}, false, assignmentReadError(ctx, err)
	}
	if tag.IsZero() {
		return app.Snapshot{}, false, app.ErrInvalidInput
	}
	var idText string
	err := r.transaction.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(assignment_id AS BLOB))=30 THEN assignment_id END FROM tc_assignments WHERE tag_id=?`, tag.String()).Scan(&idText)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Snapshot{}, false, nil
	}
	if err != nil {
		return bad(err)
	}
	id, err := d.ParseID(idText)
	if err != nil {
		return bad(err)
	}
	if _, found, err := r.Tag(ctx, tag); err != nil || !found {
		return bad(app.ErrUnavailable)
	}
	var head, retired, events, observations, links, retained uint64
	var storedTag string
	err = r.transaction.QueryRowContext(ctx, `SELECT tag_id,head_revision,retired,event_count,observation_count,support_count,retained_bytes FROM tc_assignment_heads WHERE assignment_id=?`, idText).Scan(&storedTag, &head, &retired, &events, &observations, &links, &retained)
	if err != nil {
		return bad(err)
	}
	if head == 0 || head != events || retired > 1 || retained == 0 || storedTag != tag.String() {
		return bad(app.ErrUnavailable)
	}
	// Count and sum every retained copy BEFORE selecting BLOBs or allocating a
	// history. Selection by both identities catches malformed foreign projections.
	var eventCount, evidenceCount, supportCount, commandCount, projectionCount, size uint64
	err = r.transaction.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM tc_assignment_events WHERE assignment_id=?1 OR tag_id=?2),
 (SELECT count(*) FROM tc_assignment_evidence WHERE assignment_id=?1 OR tag_id=?2),
 (SELECT count(*) FROM tc_assignment_support WHERE assignment_id=?1 OR tag_id=?2),
 (SELECT count(*) FROM tc_assignment_commands WHERE assignment_id=?1 OR tag_id=?2),
 (SELECT count(*) FROM tc_assignment_projections WHERE assignment_id=?1 OR tag_id=?2),
 (SELECT COALESCE(sum(length(canonical_event)),0) FROM tc_assignment_events WHERE assignment_id=?1 OR tag_id=?2)+
 (SELECT COALESCE(sum(length(canonical_evidence)),0) FROM tc_assignment_evidence WHERE assignment_id=?1 OR tag_id=?2)+
 (SELECT COALESCE(sum(length(canonical_command)+length(canonical_result)),0) FROM tc_assignment_commands WHERE assignment_id=?1 OR tag_id=?2)+
 (SELECT COALESCE(sum(length(canonical_projection)),0) FROM tc_assignment_projections WHERE assignment_id=?1 OR tag_id=?2)`, idText, tag.String()).Scan(&eventCount, &evidenceCount, &supportCount, &commandCount, &projectionCount, &size)
	if err != nil {
		return bad(err)
	}
	if eventCount != events || evidenceCount != observations || supportCount != links || commandCount != events || projectionCount != 1 || size > retained {
		return bad(app.ErrUnavailable)
	}
	usage, err := d.NewEvidenceUsage(events, observations, links, retained)
	if err != nil {
		// Corroborate the oversized head with the counted rows and charges
		// before classifying it as unsupported. A mismatched head is corrupt.
		var charge uint64
		err = r.transaction.QueryRowContext(ctx, `SELECT COALESCE(sum(retained_bytes),0)
 FROM tc_assignment_events WHERE assignment_id=? OR tag_id=?`, idText, tag.String()).Scan(&charge)
		if err != nil {
			return bad(err)
		}
		if charge != retained {
			return bad(app.ErrUnavailable)
		}
		return app.Snapshot{}, false, app.ErrUnsupportedState
	}
	replay, err := newAssignmentReplay(id, tag)
	if err != nil {
		return bad(err)
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT assignment_id,tag_id,revision,CASE WHEN length(CAST(command_id AS BLOB))=32 THEN command_id END,kind,effective_seconds,effective_nanos,recorded_seconds,recorded_nanos,content_id,version_id,review_id,encoding,canonical_event,event_sha256,new_observations,selected_support,retained_bytes FROM tc_assignment_events WHERE assignment_id=? OR tag_id=? ORDER BY revision`, idText, tag.String())
	if err != nil {
		return bad(err)
	}
	type checkedEvent struct {
		event    app.Event
		envelope assignmentEvent
	}
	checked := make([]checkedEvent, 0, int(events))
	seen := map[d.CommandID]bool{}
	for rows.Next() {
		var owner, subject, command string
		var revision, kind, encoding, newObs, support, charge uint64
		var es, en, rs, rn int64
		var content, version, review sql.NullString
		var raw, digest []byte
		err = rows.Scan(&owner, &subject, &revision, &command, &kind, &es, &en, &rs, &rn, &content, &version, &review, &encoding, &raw, &digest, &newObs, &support, &charge)
		if err != nil {
			break
		}
		if owner != idText || subject != tag.String() || encoding != 1 || revision != uint64(len(checked))+1 || !assignmentDigest(raw, digest) {
			err = app.ErrUnavailable
			break
		}
		event, e := assignmentDecodeEvent(raw)
		if e != nil {
			err = e
			break
		}
		in := event.Command
		if seen[in.CommandID] || command != in.CommandID.String() || kind != uint64(in.Kind) || es != in.EffectiveAt.Unix() || en != int64(in.EffectiveAt.Nanosecond()) || rs != event.RecordedAt.Unix() || rn != int64(event.RecordedAt.Nanosecond()) || content.String != in.Target.ContentID().String() || version.String != in.Target.VersionID().String() || content.Valid != !in.Target.ContentID().IsZero() || version.Valid != !in.Target.VersionID().IsZero() || review.String != in.ReviewID.String() || review.Valid != !in.ReviewID.IsZero() {
			err = app.ErrUnavailable
			break
		}
		seen[in.CommandID] = true
		delta, e := replay.append(event, raw)
		if e != nil || delta.Observations != newObs || delta.SupportLinks != support || delta.RetainedBytes != charge {
			err = app.ErrUnavailable
			break
		}
		var envelope assignmentEvent
		_ = json.Unmarshal(raw, &envelope)
		checked = append(checked, checkedEvent{event, envelope})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return bad(err)
	}
	if uint64(len(checked)) != events || replay.snapshot.Usage != usage || replay.projection.Retired != (retired == 1) {
		return bad(app.ErrUnavailable)
	}
	targets := map[d.OriginalTarget]bool{}
	for _, item := range checked {
		event := item.event
		in := event.Command
		var raw, result, commandHash, resultHash []byte
		var encoding, revision uint64
		var owner, subject string
		err = r.transaction.QueryRowContext(ctx, `SELECT assignment_id,tag_id,revision,encoding,canonical_command,canonical_result,command_sha256,result_sha256 FROM tc_assignment_commands WHERE command_id=?`, in.CommandID.String()).Scan(&owner, &subject, &revision, &encoding, &raw, &result, &commandHash, &resultHash)
		if err != nil {
			return bad(err)
		}
		if owner != idText || subject != tag.String() || revision != event.Result.Revision || encoding != 1 || !assignmentDigest(raw, commandHash) || !assignmentDigest(result, resultHash) || !bytes.Equal(raw, item.envelope.Input) || !bytes.Equal(result, item.envelope.Result) {
			return bad(app.ErrUnavailable)
		}
		if err = r.checkEvidence(ctx, event); err != nil {
			return bad(err)
		}
		if in.Target != (d.OriginalTarget{}) && !targets[in.Target] {
			value, found, e := r.Target(ctx, in.Target)
			if e != nil {
				return bad(e)
			}
			if !found || !value.Managed {
				return bad(app.ErrUnavailable)
			}
			targets[in.Target] = true
		}
	}
	var projection, digest []byte
	var projectionHead, encoding uint64
	err = r.transaction.QueryRowContext(ctx, `SELECT tag_id,head_revision,encoding,canonical_projection,projection_sha256 FROM tc_assignment_projections WHERE assignment_id=?`, idText).Scan(&storedTag, &projectionHead, &encoding, &projection, &digest)
	if err != nil {
		return bad(err)
	}
	want, _ := json.Marshal(replay.projection)
	if storedTag != tag.String() || projectionHead != head || encoding != 1 || !assignmentDigest(projection, digest) || !bytes.Equal(projection, want) {
		return bad(app.ErrUnavailable)
	}
	// Bound the complete canonical row representation as well as A1's exact
	// charge. Variable blobs were summed above; normalized row images frame each
	// text/blob with u64 length, scalars with u64 and hashes as fixed 32 bytes.
	// SQL page/B-tree overhead is outside the canonical retention contract.
	normalized := assignmentIndexBytes(replay.snapshot)
	if size+normalized > retained {
		return bad(app.ErrUnavailable)
	}

	return replay.snapshot, true, ctx.Err()
}

func assignmentIndexBytes(snapshot app.Snapshot) uint64 {
	text := func(values ...string) uint64 {
		n := uint64(0)
		for _, s := range values {
			n += 8 + uint64(len(s))
		}
		return n
	}
	id, tag := snapshot.ID.String(), snapshot.TagID.String()
	// Identity, head (six scalars), projection (two scalars/hash/blob frame).
	n := text(id, tag)*3 + 6*8 + 2*8 + 32 + 8
	for _, event := range snapshot.Events {
		in := event.Command
		// Event's ten scalars, digest and canonical-event blob frame.
		n += text(id, tag, in.CommandID.String(), in.Target.ContentID().String(), in.Target.VersionID().String(), in.ReviewID.String()) + 10*8 + 32 + 8
		// Global command index: two scalars, two digests and two blob frames.
		n += text(in.CommandID.String(), id, tag) + 2*8 + 64 + 2*8
		support := in.Support
		if in.Evidence != (d.Evidence{}) {
			n += text(in.Evidence.ID().String(), id, tag, in.Target.ContentID().String(), in.Target.VersionID().String()) + 2*8 + 32 + 8
			support = []d.EvidenceID{in.Evidence.ID()}
		}
		for _, evidence := range support {
			n += text(id, tag, evidence.String()) + 8
		}
	}
	return n
}

func assignmentCloneSnapshot(snapshot app.Snapshot) app.Snapshot {
	snapshot.Events = append([]app.Event(nil), snapshot.Events...)
	for i := range snapshot.Events {
		snapshot.Events[i].Command.Support = append([]d.EvidenceID(nil), snapshot.Events[i].Command.Support...)
		snapshot.Events[i].Primitive = append([]byte(nil), snapshot.Events[i].Primitive...)
	}
	return snapshot
}

func assignmentWriteError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx == nil {
		return app.ErrInvalidInput
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var driverError *sqliteDriver.Error
	if errors.As(err, &driverError) {
		if driverError.Code() == sqlite3.SQLITE_BUSY_SNAPSHOT {
			return app.ErrRevisionConflict
		}
		if code := driverError.Code() & 0xff; code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED {
			return app.ErrContention
		}
	}
	return app.ErrUnavailable
}

// commit is deliberately private and transaction-bound. Its caller owns the
// supplied transaction and must roll it back on every returned error. The full
// successor is replayed before the single new event and all derived material are
// installed; no partial state is made visible before the caller commits.
func (r *assignmentRepository) commit(ctx context.Context, expected uint64, snapshot app.Snapshot) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if expected >= d.MaxEvidenceEvents || uint64(len(snapshot.Events)) != expected+1 || snapshot.ID.IsZero() || snapshot.TagID.IsZero() {
		return app.ErrInvalidInput
	}
	replay, err := newAssignmentReplay(snapshot.ID, snapshot.TagID)
	if err != nil {
		return app.ErrUnavailable
	}
	var prior app.Snapshot
	var command, result, raw []byte
	var delta d.EvidenceDelta
	for i, event := range snapshot.Events {
		command, result, raw, err = assignmentEncodeEvent(event)
		if err != nil {
			return err
		}
		delta, err = replay.append(event, raw)
		if err != nil {
			return app.ErrUnavailable
		}
		if uint64(i+1) == expected {
			prior = assignmentCloneSnapshot(replay.snapshot)
		}
	}
	if !reflect.DeepEqual(replay.snapshot, snapshot) {
		return app.ErrUnavailable
	}

	if expected == 0 {
		var conflicts uint64
		err = r.transaction.QueryRowContext(ctx, `SELECT count(*) FROM tc_assignments WHERE assignment_id=? OR tag_id=?`, snapshot.ID.String(), snapshot.TagID.String()).Scan(&conflicts)
		if err != nil {
			return assignmentWriteError(ctx, err)
		}
		if conflicts != 0 {
			return app.ErrCommandConflict
		}
	} else {
		current, found, loadErr := r.load(ctx, snapshot.TagID)
		if loadErr != nil {
			return loadErr
		}
		if !found || !reflect.DeepEqual(current, prior) {
			return app.ErrRevisionConflict
		}
	}

	event := snapshot.Events[expected]
	in := event.Command
	var commandConflicts uint64
	err = r.transaction.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM tc_assignment_commands WHERE command_id=?1)+
 (SELECT count(*) FROM tc_assignment_events WHERE command_id=?1)`, in.CommandID.String()).Scan(&commandConflicts)
	if err != nil {
		return assignmentWriteError(ctx, err)
	}
	if commandConflicts != 0 {
		return app.ErrCommandConflict
	}
	var tagRevision uint64
	err = r.transaction.QueryRowContext(ctx, `SELECT revision FROM tc_tags WHERE tag_id=?`, snapshot.TagID.String()).Scan(&tagRevision)
	if errors.Is(err, sql.ErrNoRows) || err == nil && tagRevision != in.ExpectedTagRevision {
		return app.ErrDependencyConflict
	}
	if err != nil {
		return assignmentWriteError(ctx, err)
	}
	if !in.Target.ContentID().IsZero() {
		var catalogRevision uint64
		err = r.transaction.QueryRowContext(ctx, `SELECT m.aggregate_revision FROM tc_catalog_managed m JOIN tc_content_versions v ON v.content_id=m.content_id WHERE m.content_id=? AND v.version_id=?`, in.Target.ContentID().String(), in.Target.VersionID().String()).Scan(&catalogRevision)
		if errors.Is(err, sql.ErrNoRows) || err == nil && catalogRevision != in.ExpectedCatalogRevision {
			return app.ErrDependencyConflict
		}
		if err != nil {
			return assignmentWriteError(ctx, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	exec := func(query string, args ...any) error {
		_, execErr := r.transaction.ExecContext(ctx, query, args...)
		return assignmentWriteError(ctx, execErr)
	}
	if expected == 0 {
		if err := exec(`INSERT INTO tc_assignments VALUES (?,?)`, snapshot.ID.String(), snapshot.TagID.String()); err != nil {
			return err
		}
	}
	nullable := func(text string) any {
		if text == "" {
			return nil
		}
		return text
	}
	eventHash := sha256.Sum256(raw)
	if err := exec(`INSERT INTO tc_assignment_events VALUES (?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?)`, snapshot.ID.String(), snapshot.TagID.String(), event.Result.Revision, in.CommandID.String(), in.Kind, in.EffectiveAt.Unix(), in.EffectiveAt.Nanosecond(), event.RecordedAt.Unix(), event.RecordedAt.Nanosecond(), nullable(in.Target.ContentID().String()), nullable(in.Target.VersionID().String()), nullable(in.ReviewID.String()), raw, eventHash[:], delta.Observations, delta.SupportLinks, delta.RetainedBytes); err != nil {
		return err
	}
	support := in.Support
	if in.Evidence != (d.Evidence{}) {
		evidence, evidenceErr := in.Evidence.CanonicalBytes()
		if evidenceErr != nil {
			return app.ErrUnavailable
		}
		evidenceHash := sha256.Sum256(evidence)
		if err := exec(`INSERT INTO tc_assignment_evidence VALUES (?,?,?,?,1,?,?,?,?)`, in.Evidence.ID().String(), snapshot.ID.String(), snapshot.TagID.String(), event.Result.Revision, evidence, evidenceHash[:], nullable(in.Target.ContentID().String()), nullable(in.Target.VersionID().String())); err != nil {
			return err
		}
		support = []d.EvidenceID{in.Evidence.ID()}
	}
	for _, evidence := range support {
		if err := exec(`INSERT INTO tc_assignment_support VALUES (?,?,?,?)`, snapshot.ID.String(), snapshot.TagID.String(), event.Result.Revision, evidence.String()); err != nil {
			return err
		}
	}
	commandHash, resultHash := sha256.Sum256(command), sha256.Sum256(result)
	if err := exec(`INSERT INTO tc_assignment_commands VALUES (?,?,?,?,1,?,?,?,?)`, in.CommandID.String(), snapshot.ID.String(), snapshot.TagID.String(), event.Result.Revision, command, result, commandHash[:], resultHash[:]); err != nil {
		return err
	}
	usage := snapshot.Usage
	if expected == 0 {
		if err := exec(`INSERT INTO tc_assignment_heads VALUES (?,?,?,?,?,?,?,?)`, snapshot.ID.String(), snapshot.TagID.String(), event.Result.Revision, replay.projection.Retired, usage.Events(), usage.Observations(), usage.SupportLinks(), usage.RetainedBytes()); err != nil {
			return err
		}
	} else {
		changed, updateErr := r.transaction.ExecContext(ctx, `UPDATE tc_assignment_heads SET head_revision=?,retired=?,event_count=?,observation_count=?,support_count=?,retained_bytes=? WHERE assignment_id=? AND tag_id=? AND head_revision=?`, event.Result.Revision, replay.projection.Retired, usage.Events(), usage.Observations(), usage.SupportLinks(), usage.RetainedBytes(), snapshot.ID.String(), snapshot.TagID.String(), expected)
		if updateErr != nil {
			return assignmentWriteError(ctx, updateErr)
		}
		rows, rowsErr := changed.RowsAffected()
		if rowsErr != nil {
			return assignmentWriteError(ctx, rowsErr)
		}
		if rows != 1 {
			return app.ErrRevisionConflict
		}
	}
	projection, err := json.Marshal(replay.projection)
	if err != nil {
		return app.ErrUnavailable
	}
	projectionHash := sha256.Sum256(projection)
	if expected == 0 {
		if err := exec(`INSERT INTO tc_assignment_projections VALUES (?,?,?,1,?,?)`, snapshot.ID.String(), snapshot.TagID.String(), event.Result.Revision, projection, projectionHash[:]); err != nil {
			return err
		}
	} else {
		changed, updateErr := r.transaction.ExecContext(ctx, `UPDATE tc_assignment_projections SET head_revision=?,canonical_projection=?,projection_sha256=? WHERE assignment_id=? AND tag_id=? AND head_revision=?`, event.Result.Revision, projection, projectionHash[:], snapshot.ID.String(), snapshot.TagID.String(), expected)
		if updateErr != nil {
			return assignmentWriteError(ctx, updateErr)
		}
		rows, rowsErr := changed.RowsAffected()
		if rowsErr != nil {
			return assignmentWriteError(ctx, rowsErr)
		}
		if rows != 1 {
			return app.ErrRevisionConflict
		}
	}
	return ctx.Err()
}

func (r *assignmentRepository) checkEvidence(ctx context.Context, event app.Event) error {
	in := event.Command
	support := in.Support
	if in.Evidence != (d.Evidence{}) {
		support = []d.EvidenceID{in.Evidence.ID()}
		var owner, subject string
		var revision, encoding uint64
		var raw, digest []byte
		var content, version sql.NullString
		err := r.transaction.QueryRowContext(ctx, `SELECT assignment_id,tag_id,introduced_revision,encoding,canonical_evidence,evidence_sha256,content_id,version_id FROM tc_assignment_evidence WHERE evidence_id=?`, in.Evidence.ID().String()).Scan(&owner, &subject, &revision, &encoding, &raw, &digest, &content, &version)
		if err != nil {
			return assignmentReadError(ctx, err)
		}
		want, _ := in.Evidence.CanonicalBytes()
		if owner != in.AssignmentID.String() || subject != in.TagID.String() || revision != event.Result.Revision || encoding != 1 || !assignmentDigest(raw, digest) || !bytes.Equal(raw, want) || content.String != in.Target.ContentID().String() || version.String != in.Target.VersionID().String() || content.Valid != !in.Target.ContentID().IsZero() || version.Valid != !in.Target.VersionID().IsZero() {
			return app.ErrUnavailable
		}
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT tag_id,evidence_id FROM tc_assignment_support WHERE assignment_id=? AND revision=? ORDER BY evidence_id COLLATE BINARY`, in.AssignmentID.String(), event.Result.Revision)
	if err != nil {
		return assignmentReadError(ctx, err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var tag, id string
		if err := rows.Scan(&tag, &id); err != nil {
			return assignmentReadError(ctx, err)
		}
		if i >= len(support) || tag != in.TagID.String() || id != support[i].String() {
			return app.ErrUnavailable
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return assignmentReadError(ctx, err)
	}
	if i != len(support) {
		return app.ErrUnavailable
	}
	return nil
}

// Lookup is GLOBAL. Missing an index row is corrupt when a retained event still
// names the command. A hit validates the whole owning aggregate before release.
func (r *assignmentRepository) LookupCommand(ctx context.Context, id d.CommandID) (app.Event, bool, error) {
	if err := r.check(ctx); err != nil {
		return app.Event{}, false, err
	}
	if id.IsZero() {
		return app.Event{}, false, app.ErrInvalidInput
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT assignment_id,tag_id FROM tc_assignment_commands WHERE command_id=?1 UNION SELECT assignment_id,tag_id FROM tc_assignment_events WHERE command_id=?1`, id.String())
	if err != nil {
		return app.Event{}, false, assignmentReadError(ctx, err)
	}
	var owner, subject string
	n := 0
	for rows.Next() {
		n++
		if err = rows.Scan(&owner, &subject); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return app.Event{}, false, assignmentReadError(ctx, err)
	}
	if n == 0 {
		return app.Event{}, false, nil
	}
	if n != 1 {
		return app.Event{}, false, app.ErrUnavailable
	}
	tag, err := identity.ParseTagID(subject)
	if err != nil {
		return app.Event{}, false, app.ErrUnavailable
	}
	snapshot, found, err := r.load(ctx, tag)
	if err != nil {
		return app.Event{}, false, err
	}
	if !found || snapshot.ID.String() != owner {
		return app.Event{}, false, app.ErrUnavailable
	}
	for _, event := range snapshot.Events {
		if event.Command.CommandID == id {
			return event, true, nil
		}
	}
	return app.Event{}, false, app.ErrUnavailable
}

// Target reconstructs the requested immutable tuple, never a terminal/latest
// catalog target. Low-level catalog envelope/media readers are reused on THIS
// transaction with the actual schema number (10). No catalog transaction,
// callback port, v9 compatibility gate or writer is invoked.
func (r *assignmentRepository) Target(ctx context.Context, target d.OriginalTarget) (app.TargetSnapshot, bool, error) {
	zero := app.TargetSnapshot{}
	if err := r.check(ctx); err != nil {
		return zero, false, err
	}
	if _, err := target.CanonicalByteLen(); err != nil {
		return zero, false, app.ErrInvalidInput
	}
	dependency := &catalogRepository{transaction: r.transaction, ctx: ctx, schemaVersion: 10, statements: make(map[string]*sql.Stmt)}
	defer dependency.closeStatements()
	bad := func(err error) (app.TargetSnapshot, bool, error) { return zero, false, assignmentReadError(ctx, err) }
	var owner string
	err := r.transaction.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(content_id AS BLOB))=30 THEN content_id END FROM tc_content_versions WHERE version_id=?`, target.VersionID().String()).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, false, nil
	}
	if err != nil {
		return bad(err)
	}
	if owner != target.ContentID().String() {
		return bad(app.ErrUnavailable)
	}
	var title, model, article sql.NullString
	var modelPresent, articlePresent bool
	var aggregate, protected sql.NullInt64
	err = r.transaction.QueryRowContext(ctx, `SELECT
 CASE WHEN typeof(c.title)='text' AND length(CAST(c.title AS BLOB))<=4096 THEN c.title END,
 CASE WHEN typeof(c.model_number)='text' AND length(CAST(c.model_number AS BLOB))<=64 THEN c.model_number END,
 CASE WHEN typeof(c.article_number)='text' AND length(CAST(c.article_number AS BLOB))<=64 THEN c.article_number END,
 c.model_number IS NOT NULL,c.article_number IS NOT NULL,m.aggregate_revision,m.protected_custom
 FROM tc_catalog_content c LEFT JOIN tc_catalog_managed m USING(content_id) WHERE c.content_id=?`, owner).Scan(&title, &model, &article, &modelPresent, &articlePresent, &aggregate, &protected)
	if err != nil {
		return bad(err)
	}
	if !title.Valid || title.String != "" && !evidence.ValidText(title.String, 4096) || model.Valid != modelPresent || article.Valid != articlePresent || aggregate.Valid != protected.Valid {
		return bad(app.ErrUnavailable)
	}
	product, err := productIdentifiers(model, article)
	if err != nil {
		return bad(err)
	}
	if !aggregate.Valid {
		return zero, false, app.ErrUnsupportedState
	}
	if aggregate.Int64 < 1 || protected.Int64 < 0 || protected.Int64 > 1 {
		return bad(app.ErrUnavailable)
	}
	// Validate contiguous aggregate indices including real v9 supersessions.
	// Do not call the old reader's schemaVersion==9 dispatch on a v10 database.
	var count, distinct, minimum, maximum uint64
	err = r.transaction.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT n),COALESCE(min(n),1),COALESCE(max(n),1) FROM (
 SELECT result_aggregate_revision n FROM tc_catalog_decisions WHERE content_id=?1
 UNION ALL SELECT aggregate_revision FROM tc_catalog_order_states WHERE content_id=?1 AND order_revision>1
 UNION ALL SELECT aggregate_revision FROM tc_catalog_order_states_v2 WHERE content_id=?1 AND order_revision>1
 UNION ALL SELECT result_aggregate FROM tc_catalog_supersessions WHERE content_id=?1)`, owner).Scan(&count, &distinct, &minimum, &maximum)
	if err != nil {
		return bad(err)
	}
	if count > c.RetainedDecisions.Maximum() || count != distinct || maximum != uint64(aggregate.Int64) || count+1 != maximum || count > 0 && minimum != 2 {
		return bad(app.ErrUnavailable)
	}
	versions, err := dependency.catalogVersions(ctx, target.ContentID())
	if err != nil {
		return bad(err)
	}
	var version c.ContentVersion
	bound := false
	for _, row := range versions {
		if row.Version.ID() == target.VersionID() {
			version = row.Version
			bound = row.Size != 0
		}
	}
	if version.ID().IsZero() {
		return bad(app.ErrUnavailable)
	}
	subject, _ := c.VersionSubject(target.ContentID(), target.VersionID())
	state, err := r.targetReview(ctx, dependency, subject, uint64(aggregate.Int64), versions)
	if err != nil {
		return bad(err)
	}
	ownership, fingerprint := state.Field(c.OwnershipClaimKey), state.Field(c.FingerprintClaimKey)
	own, ownOK := ownership.Value()
	fp, fpOK := fingerprint.Value()
	if !ownOK || !fpOK || own.Owner() != target.ContentID() || fp.Fingerprint() != version.Fingerprint() || len(ownership.Alternatives) != 1 || len(fingerprint.Alternatives) != 1 || len(ownership.Alternatives[0].Support) == 0 || len(fingerprint.Alternatives[0].Support) == 0 {
		return zero, false, app.ErrUnsupportedState
	}
	graph, err := r.targetSupersession(ctx, dependency, target.ContentID(), versions)
	if err != nil {
		return bad(err)
	}
	superseded := false
	for _, edge := range graph.History() {
		if edge.Command.Old.ID() == version.ID() {
			superseded = true
		}
	}
	content, err := c.NewContent(target.ContentID(), c.NewContentFacts(title.String, product))
	if err != nil {
		return bad(err)
	}
	var public, custom bool
	err = r.transaction.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM tc_catalog_observations o JOIN tc_catalog_sources s USING(source_id,registration_revision) WHERE o.content_id=?1 AND s.acquisition=1),
 EXISTS(SELECT 1 FROM tc_catalog_observations o JOIN tc_catalog_sources s USING(source_id,registration_revision) WHERE o.content_id=?1 AND s.acquisition=2)`, owner).Scan(&public, &custom)
	if err != nil {
		return bad(err)
	}
	protection := d.Unprotected
	if protected.Int64 == 1 {
		protection = d.ProtectedCustom
	}
	dimensions, _ := d.NewContentDimensions(public, custom, protection)
	return app.TargetSnapshot{Content: content, Version: version, CatalogRevision: uint64(aggregate.Int64), Managed: true, Ownership: ownership, Fingerprint: fingerprint, Dimensions: dimensions, Bound: bound, Superseded: superseded, EditorialConflict: state.Field(c.TitleClaimKey).Status == c.CatalogFactConflict || state.Field(c.ModelClaimKey).Status == c.CatalogFactConflict || state.Field(c.ArticleClaimKey).Status == c.CatalogFactConflict}, true, ctx.Err()
}

// Read all exact subject envelopes and compare their normalized evidence and
// support through the existing read-only projector. Source bindings reconstructed
// from SQL remain permanently inactive provenance, never new live authority.
func (r *assignmentRepository) targetReview(ctx context.Context, dependency *catalogRepository, subject c.CatalogSubject, aggregate uint64, versions []catalogVersionRow) (c.CatalogReviewState, error) {
	var zero c.CatalogReviewState
	key := catalogSubjectKey(subject)
	owner := subject.ContentID().String()
	var content string
	var version sql.NullString
	var expected uint64
	err := r.transaction.QueryRowContext(ctx, `SELECT content_id,version_id,review_revision FROM tc_catalog_subjects WHERE subject_id=?`, key).Scan(&content, &version, &expected)
	if err != nil || content != owner || version.String != subject.VersionID().String() || version.Valid == subject.VersionID().IsZero() || expected == 0 {
		return zero, app.ErrUnavailable
	}
	table := `(SELECT subject_id,content_id,aggregate_revision,review_revision,encoding,state,state_bytes,state_sha256 FROM tc_catalog_review_states UNION ALL SELECT subject_id,content_id,aggregate_revision,review_revision,encoding,state,state_bytes,state_sha256 FROM tc_catalog_review_states_v2)`
	var count, size, decisions uint64
	err = r.transaction.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(state)),0),(SELECT count(*) FROM tc_catalog_decisions WHERE subject_id=?1) FROM `+table+` WHERE subject_id=?1`, key).Scan(&count, &size, &decisions)
	if err != nil || count == 0 || count > c.RetainedDecisions.Maximum()+1 || size > c.RetainedBytes.Maximum() || decisions+1 != expected {
		return zero, app.ErrUnavailable
	}
	rows, err := r.transaction.QueryContext(ctx, `SELECT aggregate_revision,review_revision,encoding,state,state_bytes,state_sha256,content_id FROM `+table+` WHERE subject_id=? ORDER BY review_revision`, key)
	if err != nil {
		return zero, assignmentReadError(ctx, err)
	}
	var states []catalogStoredState
	for rows.Next() {
		var s catalogStoredState
		var declared uint64
		var digest []byte
		var content string
		if err = rows.Scan(&s.aggregate, &s.revision, &s.encoding, &s.raw, &declared, &digest, &content); err != nil {
			break
		}
		sum := sha256.Sum256(s.raw)
		if content != owner || s.revision == 0 || s.revision > expected || s.aggregate == 0 || s.aggregate > aggregate || declared != uint64(len(s.raw)) || !bytes.Equal(sum[:], digest) || (s.encoding != 1 && s.encoding != 2) || len(states) > 0 && (s.revision <= states[len(states)-1].revision || s.aggregate <= states[len(states)-1].aggregate) || s.encoding == 2 && s.revision != uint64(len(states))+1 {
			err = app.ErrUnavailable
			break
		}
		states = append(states, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return zero, assignmentReadError(ctx, err)
	}
	if uint64(len(states)) != count || states[len(states)-1].revision != expected {
		return zero, app.ErrUnavailable
	}
	for i := range states {
		s := &states[i]
		reader := catalogStateReader{raw: s.raw, repository: dependency, ctx: ctx, content: owner, subject: key, sources: make(map[string]c.SourceRegistration)}
		if err = reader.checkEnvelope(s, versions, i == len(states)-1); err != nil {
			return zero, assignmentReadError(ctx, err)
		}
		bindings := c.CatalogPersistenceBindings{AllowUnbound: true}
		for _, source := range reader.sources {
			bindings.Sources = append(bindings.Sources, source)
		}
		state, e := c.RehydrateCatalogReviewState(s.raw, bindings)
		if e != nil || state.Subject() != subject || state.Revision().Uint64() != s.revision {
			return zero, app.ErrUnavailable
		}
		if i > 0 {
			prior := states[i-1].decisions
			if len(s.decisions) <= len(prior) {
				return zero, app.ErrUnavailable
			}
			for j, p := range prior {
				if p.id != s.decisions[j].id || !bytes.Equal(p.command, s.decisions[j].command) || !bytes.Equal(p.result, s.decisions[j].result) {
					return zero, app.ErrUnavailable
				}
			}
		}
		zero = state
	}
	return zero, ctx.Err()
}

// Supersession is an annotation. Reconstruct its bounded canonical graph and
// check exact normalized tuples/evidence/support; never substitute its terminal.
func (r *assignmentRepository) targetSupersession(ctx context.Context, dependency *catalogRepository, content identity.ContentID, versions []catalogVersionRow) (c.CatalogSupersessionState, error) {
	var zero c.CatalogSupersessionState
	var count, size uint64
	err := r.transaction.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(canonical_command)+length(canonical_result)),0) FROM tc_catalog_supersessions WHERE content_id=?`, content.String()).Scan(&count, &size)
	if err != nil {
		return zero, assignmentReadError(ctx, err)
	}
	if count > 1023 || size > c.RetainedBytes.Maximum() {
		return zero, app.ErrUnavailable
	}
	raw := registrationFrame(nil, []byte("pi08-version-supersession-state/1"))
	raw = registrationFrame(raw, []byte(content.String()))
	raw = binary.BigEndian.AppendUint64(raw, count+1)
	raw = binary.BigEndian.AppendUint64(raw, uint64(len(versions)))
	for _, v := range versions {
		raw = supersessionVersionFrame(raw, v.Version)
	}
	raw = binary.BigEndian.AppendUint64(raw, count)
	rows, err := r.transaction.QueryContext(ctx, `SELECT canonical_command,canonical_result,command_sha256,result_sha256 FROM tc_catalog_supersessions WHERE content_id=? ORDER BY result_supersession`, content.String())
	if err != nil {
		return zero, assignmentReadError(ctx, err)
	}
	type pair struct{ command, result []byte }
	var pairs []pair
	for rows.Next() {
		var p pair
		var a, b []byte
		if err = rows.Scan(&p.command, &p.result, &a, &b); err != nil {
			break
		}
		if !assignmentDigest(p.command, a) || !assignmentDigest(p.result, b) {
			err = app.ErrUnavailable
			break
		}
		raw = registrationFrame(raw, p.command)
		raw = registrationFrame(raw, p.result)
		pairs = append(pairs, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return zero, assignmentReadError(ctx, err)
	}
	if uint64(len(pairs)) != count || uint64(len(raw)) > c.RetainedBytes.Maximum() {
		return zero, app.ErrUnavailable
	}
	bindings := c.CatalogPersistenceBindings{AllowUnbound: true}
	seen := map[string]bool{}
	for _, p := range pairs {
		sources, e := dependency.supersessionProject(ctx, p.command, false)
		if e != nil {
			return zero, assignmentReadError(ctx, e)
		}
		for key, source := range sources {
			if !seen[key] {
				bindings.Sources = append(bindings.Sources, source)
				seen[key] = true
			}
		}
		if uint64(len(bindings.Sources)) > c.SourceRegistrations.Maximum() {
			return zero, app.ErrUnavailable
		}
	}
	state, err := c.RehydrateCatalogSupersessionState(raw, bindings)
	if err != nil {
		return zero, app.ErrUnavailable
	}
	for _, edge := range state.History() {
		command, result := edge.Command, edge.Result
		reader := catalogStateReader{repository: dependency, ctx: ctx}
		reader.match(`SELECT EXISTS(SELECT 1 FROM tc_catalog_supersessions WHERE command_id=? AND content_id=? AND old_version_id=? AND new_version_id=? AND source_id=? AND registration_revision=? AND reviewer_principal=? AND expected_aggregate=? AND result_aggregate=? AND expected_supersession=? AND result_supersession=? AND old_review_revision=? AND new_review_revision=? AND order_revision=? AND protected_custom=? AND encoding=1)`, command.ID.String(), content.String(), command.Old.ID().String(), command.New.ID().String(), command.Source.ID().String(), command.Source.Revision().String(), edge.Reviewer.String(), command.ExpectedAggregate.Uint64(), result.AggregateRevision.Uint64(), command.ExpectedSupersession.Uint64(), result.SupersessionRevision.Uint64(), result.OldReviewRevision.Uint64(), result.NewReviewRevision.Uint64(), result.OrderRevision.Uint64(), result.ProtectedCustom)
		if reader.err != nil {
			return zero, assignmentReadError(ctx, reader.err)
		}
	}
	return state, ctx.Err()
}
