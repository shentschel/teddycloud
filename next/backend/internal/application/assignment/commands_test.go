package assignment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
)

func TestAssignmentAllCommandKinds(t *testing.T) {
	s, m := fixture(t)
	original := command(t, 1, RecordClassification, 0)
	execute(t, s, original)
	contrary := command(t, 2, RecordClassification, 1)
	contrary.Classification = d.CustomCard
	execute(t, s, contrary)
	resolution := command(t, 3, ResolveClassification, 2)
	resolution.Support = []d.EvidenceID{original.Evidence.ID()}
	execute(t, s, resolution)
	baseline := command(t, 4, RecordOriginalTarget, 3)
	execute(t, s, baseline)
	otherBaseline := command(t, 5, RecordOriginalTarget, 4)
	for target := range m.targets {
		if target != baseline.Target {
			otherBaseline.Target = target
		}
	}
	execute(t, s, otherBaseline)
	resolveBaseline := command(t, 6, ResolveOriginalTarget, 5)
	resolveBaseline.Support = []d.EvidenceID{baseline.Evidence.ID()}
	execute(t, s, resolveBaseline)
	assign := command(t, 7, Assign, 6)
	execute(t, s, assign)
	unassign := command(t, 8, Unassign, 7)
	execute(t, s, unassign)
	retire := command(t, 9, RetireAssignment, 8)
	execute(t, s, retire)
	for _, in := range []Command{original, contrary, resolution, baseline, otherBaseline, resolveBaseline, assign, unassign, retire} {
		got, found, err := s.LookupAssignmentCommand(context.Background(), in)
		if err != nil || !found || got.Revision != in.ExpectedRevision+1 {
			t.Fatal("mixed-ledger command readback", err)
		}
	}
	if len(m.snapshots[assign.TagID].Events) != 9 {
		t.Fatal("review lost a global revision")
	}
	_, err := s.Execute(context.Background(), command(t, 10, RecordClassification, 9))
	wantError(t, err, ErrRetired)
	// Same-Tag support and primitive review IDs remain fenced independently.
	s, _ = fixture(t)
	execute(t, s, original)
	reuse := command(t, 2, RecordClassification, 1)
	reuse.ReviewID = original.ReviewID
	_, err = s.Execute(context.Background(), reuse)
	wantError(t, err, ErrInvalidInput)
}

func TestAssignmentURIReservationAndInput(t *testing.T) {
	for _, uri := range []OptionalURI{{true, ""}, {true, "https://catalog.example/a"}, {false, "https://catalog.example/a"}, {true, "file:///secret"}} {
		s, m := fixture(t)
		in := command(t, 1, Assign, 0)
		in.URI = uri
		_, err := s.Execute(context.Background(), in)
		wantError(t, err, ErrURIUnsupported)
		if m.commits != 0 {
			t.Fatal("URI admitted")
		}
	}
	invalid := map[string]func(*Command){
		"overflow":             func(in *Command) { in.ExpectedRevision = d.MaxDecisionRevision + 1 },
		"reason bound":         func(in *Command) { in.Reason = strings.Repeat("x", 1025) },
		"reason controls":      func(in *Command) { in.Reason = "secret\npath" },
		"unknown policy":       func(in *Command) { in.PolicyRevision++ },
		"inactive review":      func(in *Command) { in.ReviewID, _ = d.ParseReviewID(fmt.Sprintf("rev_%026x", 1)) },
		"inactive support":     func(in *Command) { in.Support = []d.EvidenceID{{}} },
		"missing Tag revision": func(in *Command) { in.ExpectedTagRevision = 0 },
	}
	for name, change := range invalid {
		t.Run(name, func(t *testing.T) {
			s, m := fixture(t)
			in := command(t, 1, Assign, 0)
			change(&in)
			_, err := s.Execute(context.Background(), in)
			wantError(t, err, ErrInvalidInput)
			if m.commits != 0 {
				t.Fatal("invalid wrote")
			}
		})
	}
	s, _ := fixture(t)
	in := command(t, 1, RecordClassification, 0)
	// A forged evidence subject cannot borrow an authenticated actor.
	eid := in.Evidence.ID()
	foreign, _ := identityTag(2)
	in.Evidence, _ = d.NewEvidence(eid, foreign, d.OperatorAssertion, "operator", d.AcceptedEvidence)
	_, err := s.Execute(context.Background(), in)
	wantError(t, err, ErrInvalidInput)
}

func TestAssignmentCanonicalEnvelope(t *testing.T) {
	in := command(t, 1, Assign, 0)
	b, err := canonicalCommand(in, "operator")
	if err != nil {
		t.Fatal(err)
	}
	// Golden shape binds independently typed identities, GLOBAL revision and the
	// reserved URI slot; it does not serialize opaque IDs as empty JSON objects.
	want := `{"Format":"TCAS/1","Command":"00000000000000000000000000000001","Assignment":"asn_00000000000000000000000001","Tag":"tag_00000000000000000000000001","Kind":1,"Expected":0,"TagRevision":2,"CatalogRevision":3,"Policy":1,"Actor":"operator","Reason":"explicit reason","Effective":"1970-01-01T00:00:10Z","Content":"cnt_00000000000000000000000001","Version":"ver_00000000000000000000000001","Review":"","Evidence":null,"Classification":0,"Support":[],"URI":{"Present":false,"Profile":"","Locator":""}}`
	if string(b) != want {
		t.Fatalf("canonical golden mismatch: %s", b)
	}
	in.Actor = "operator"
	in.EffectiveAt = in.EffectiveAt.In(time.FixedZone("offset", 3600))
	equivalent, err := canonicalCommand(in, "operator")
	if err != nil || !bytes.Equal(b, equivalent) {
		t.Fatal("equivalent input changed canonical command", err)
	}
	in.Actor = ""
	changed, err := canonicalCommand(in, "other")
	if err != nil || bytes.Equal(b, changed) {
		t.Fatal("actor missing from identity")
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	// Set semantics are independent of request order, with duplicates rejected.
	in = command(t, 2, ResolveClassification, 1)
	a, _ := d.ParseEvidenceID(fmt.Sprintf("evd_%026x", 1))
	z, _ := d.ParseEvidenceID(fmt.Sprintf("evd_%026x", 2))
	in.Support = []d.EvidenceID{z, a}
	first, _ := canonicalCommand(in, "operator")
	in.Support = []d.EvidenceID{a, z}
	second, _ := canonicalCommand(in, "operator")
	if !bytes.Equal(first, second) {
		t.Fatal("support order changed identity")
	}
	in.Support = []d.EvidenceID{a, a}
	_, err = canonicalCommand(in, "operator")
	wantError(t, err, ErrInvalidInput)
	in.Support = make([]d.EvidenceID, 65)
	_, err = canonicalCommand(in, "operator")
	wantError(t, err, ErrLimit)
}

func TestAssignmentCompleteRetention(t *testing.T) {
	s, m := fixture(t)
	in := command(t, 1, Assign, 0)
	execute(t, s, in)
	snap := m.snapshots[in.TagID]
	primitive := uint64(len(snap.Events[0].Primitive))
	extra, err := retainedEnvelopeBytes(snap.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	if snap.Usage.Events() != 1 || snap.Usage.RetainedBytes() != primitive+extra {
		t.Fatal("complete decision material missing or counted twice")
	}
	review := command(t, 2, RecordClassification, 1)
	execute(t, s, review)
	next := m.snapshots[in.TagID]
	event := next.Events[1]
	extra, _ = retainedEnvelopeBytes(event)
	envelope := uint64(1024 + 6*(len(event.Actor)+len(review.Reason)+len(review.Evidence.Source())) + 256)
	if next.Usage.RetainedBytes() != snap.Usage.RetainedBytes()+envelope+extra || next.Usage.Events() != 2 || next.Usage.Observations() != 1 || next.Usage.SupportLinks() != 1 {
		t.Fatal("mixed usage double counted a domain ledger")
	}
	before := next.Usage
	executeReplay, err := s.Execute(context.Background(), review)
	if err != nil || executeReplay.Revision != 2 || m.snapshots[in.TagID].Usage != before {
		t.Fatal("replay charged retention", err)
	}
	// Exact/+1 arithmetic with a COMPLETE real new-event delta; earlier usage is
	// synthetic here. Reachable whole-aggregate boundaries are exercised below.
	delta := d.EvidenceDelta{Events: 1, Observations: 1, SupportLinks: 1, RetainedBytes: envelope + extra}
	for _, test := range []struct {
		name  string
		usage func(uint64) (d.EvidenceUsage, error)
	}{
		{"bytes", func(over uint64) (d.EvidenceUsage, error) {
			return d.NewEvidenceUsage(1, 0, 0, d.MaxEvidenceRetainedBytes-delta.RetainedBytes+over)
		}},
		{"events", func(over uint64) (d.EvidenceUsage, error) {
			return d.NewEvidenceUsage(d.MaxEvidenceEvents-1+over, 0, 0, 0)
		}},
		{"observations", func(over uint64) (d.EvidenceUsage, error) {
			return d.NewEvidenceUsage(1, d.MaxEvidenceObservations-1+over, 0, 0)
		}},
		{"support", func(over uint64) (d.EvidenceUsage, error) {
			return d.NewEvidenceUsage(1, 0, d.MaxEvidenceSupportLinks-1+over, 0)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			at, _ := test.usage(0)
			if _, err := at.Prospective(delta); err != nil {
				t.Fatal("exact", err)
			}
			over, _ := test.usage(1)
			if _, err := over.Prospective(delta); err == nil {
				t.Fatal("+1 admitted")
			}
		})
	}
}

func TestAssignmentReachableRetentionBoundaries(t *testing.T) {
	auth, _ := d.BindAuthenticatedReviewAuthority("x")
	in := command(t, 1, Unassign, 0)
	in.Reason = "x"
	a, err := empty(in.AssignmentID, in.TagID)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= int(d.MaxEvidenceEvents); n++ {
		in = command(t, n, Unassign, uint64(n-1))
		in.Reason = "x"
		a, _, err = a.append(in, "x", auth, time.Unix(1, 0))
		if err != nil {
			t.Fatalf("event %d: %v", n, err)
		}
	}
	if a.snapshot.Usage.Events() != 4096 || a.snapshot.Usage.RetainedBytes() > d.MaxEvidenceRetainedBytes {
		t.Fatal("event exact not reachable")
	}
	before := a.snapshot.Usage
	_, _, err = a.append(command(t, 4097, Unassign, 4096), "x", auth, time.Unix(1, 0))
	wantError(t, sanitize(err), ErrLimit)
	if a.snapshot.Usage != before {
		t.Fatal("rejection mutated usage")
	}
	t.Logf("4096 complete events: %d bytes", before.RetainedBytes())
	service, m := fixture(t)
	m.actor.Reference = "x"
	installSnapshot(m, a.snapshot)
	view := effective(t, service, in, 100000, nil)
	if view.Desired.HeadRevision != 4096 || view.Desired.State != d.Unassigned {
		t.Fatal("bounded service read lost exhausted history")
	}
	_, err = service.Execute(context.Background(), command(t, 4097, Unassign, 4096))
	wantError(t, err, ErrLimit)
	if m.commits != 0 {
		t.Fatal("event limit committed")
	}
}

func TestAssignmentRetainedSupportExactAndPlusOne(t *testing.T) {
	auth, _ := d.BindAuthenticatedReviewAuthority("x")
	base := command(t, 1, RecordClassification, 0)
	a, err := empty(base.AssignmentID, base.TagID)
	if err != nil {
		t.Fatal(err)
	}
	var support []d.EvidenceID
	n := 0
	claim := func(class d.PhysicalClassification) {
		n++
		in := command(t, n, RecordClassification, uint64(n-1))
		in.Classification = class
		in.Reason = "x"
		in.Evidence, _ = d.NewEvidence(in.Evidence.ID(), in.TagID, d.OperatorAssertion, "x", d.AcceptedEvidence)
		var err error
		a, _, err = a.append(in, "x", auth, time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		if class == d.Original {
			support = append(support, in.Evidence.ID())
		}
	}
	resolve := func(ids []d.EvidenceID) {
		n++
		in := command(t, n, ResolveClassification, uint64(n-1))
		in.Reason = "x"
		in.Support = ids
		var err error
		a, _, err = a.append(in, "x", auth, time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
	}
	for range 64 {
		claim(d.Original)
	}
	for d.MaxEvidenceSupportLinks-a.snapshot.Usage.SupportLinks() >= 65 {
		claim(d.CustomCard)
		resolve(support)
	}
	remaining := d.MaxEvidenceSupportLinks - a.snapshot.Usage.SupportLinks()
	if remaining < 2 || remaining > 64 {
		t.Fatalf("unexpected remainder %d", remaining)
	}
	claim(d.CustomCard)
	in := command(t, n+1, ResolveClassification, uint64(n))
	in.Reason = "x"
	in.Support = support[:remaining-1]
	exact, _, err := a.append(in, "x", auth, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if exact.snapshot.Usage.SupportLinks() != d.MaxEvidenceSupportLinks {
		t.Fatal("support exact missed")
	}
	in.Support = support[:remaining]
	_, _, err = a.append(in, "x", auth, time.Unix(1, 0))
	wantError(t, err, d.ErrSupportLinkLimit)
	service, m := fixture(t)
	m.actor.Reference = "x"
	installSnapshot(m, exact.snapshot)
	view := effective(t, service, in, 100000, nil)
	if view.Classification != d.Original || view.Desired.HeadRevision != exact.snapshot.Usage.Events() {
		t.Fatal("complete support readback lost review")
	}
	t.Logf("16384 complete support links: %d events, %d observations, %d bytes", exact.snapshot.Usage.Events(), exact.snapshot.Usage.Observations(), exact.snapshot.Usage.RetainedBytes())
}

// previewDecisionCharge independently assembles all retained decision material
// without copying the existing history or calling prospective admission.
func previewDecisionCharge(t *testing.T, a aggregate, in Command, actor string) uint64 {
	t.Helper()
	dc, err := d.NewDecisionCommand(in.decisionInput(actor, a.decisions.Revision()))
	if err != nil {
		t.Fatal(err)
	}
	dr := d.DecisionResult{Command: dc, Revision: a.decisions.Revision() + 1}
	result := Result{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID, Revision: in.ExpectedRevision + 1}
	if len(a.snapshot.Events) > 0 {
		previous := a.snapshot.Events[len(a.snapshot.Events)-1].Command.CommandID
		dr.PreviousCommand = previous
		result.PreviousCommand = previous
	}
	primitive, err := (d.DecisionEvent{Result: dr, RecordedAt: time.Unix(1, 0)}).CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Command: in, Actor: actor, Result: result, RecordedAt: time.Unix(1, 0), Primitive: primitive}
	extra, err := retainedEnvelopeBytes(event)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(len(primitive)) + extra
}
func TestAssignmentRetainedBytesExactAndPlusOne(t *testing.T) {
	auth, _ := d.BindAuthenticatedReviewAuthority("x")
	in := command(t, 1, Unassign, 0)
	a, err := empty(in.AssignmentID, in.TagID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	// Leave enough for two final variable-sized, fully valid commands. Reasons
	// use ordinary text and '<', whose actual JSON escape bytes are charged.
	for d.MaxEvidenceRetainedBytes-a.snapshot.Usage.RetainedBytes() > 25000 {
		n++
		in = command(t, n, Unassign, uint64(n-1))
		in.Reason = strings.Repeat("x", 1024)
		a, _, err = a.append(in, "x", auth, time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
	}
	remaining := d.MaxEvidenceRetainedBytes - a.snapshot.Usage.RetainedBytes()
	type reason struct{ length, escaped int }
	choices := map[uint64]reason{}
	first := command(t, n+1, Unassign, uint64(n))
	for length := 1; length <= 1024; length++ {
		first.Reason = strings.Repeat("x", length)
		charge := previewDecisionCharge(t, a, first, "x")
		for escaped := 0; escaped <= length; escaped++ {
			choices[charge+20*uint64(escaped)] = reason{length, escaped}
		}
	}
	// Second command has the same fixed-width previous CommandID, with one
	// additional digit possible in the revision fields. Build a tiny shell for
	// its exact lengths; only preview encoding reads it.
	shell := a
	shell.snapshot = Snapshot{ID: a.snapshot.ID, TagID: a.snapshot.TagID, Events: append(append([]Event(nil), a.snapshot.Events...), Event{Command: first})}
	second := command(t, n+2, Unassign, uint64(n+1))
	var firstReason, secondReason reason
	found := false
	for length := 5; length <= 1024 && !found; length++ {
		second.Reason = strings.Repeat("x", length)
		charge := previewDecisionCharge(t, shell, second, "x")
		for escaped := 0; escaped <= length-4; escaped++ {
			need := charge + 20*uint64(escaped)
			if need > remaining {
				continue
			}
			if r, ok := choices[remaining-need]; ok {
				firstReason, secondReason, found = r, reason{length, escaped}, true
				break
			}
		}
	}
	if !found {
		t.Fatalf("could not construct exact retained-byte boundary, remaining %d", remaining)
	}
	text := func(r reason) string { return strings.Repeat("<", r.escaped) + strings.Repeat("x", r.length-r.escaped) }
	first.Reason = text(firstReason)
	a, _, err = a.append(first, "x", auth, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	second.Reason = text(secondReason)
	exact, _, err := a.append(second, "x", auth, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if exact.snapshot.Usage.RetainedBytes() != d.MaxEvidenceRetainedBytes {
		t.Fatalf("exact bytes: %d", exact.snapshot.Usage.RetainedBytes())
	}
	// Three fewer raw bytes remove 19 charged bytes; one extra '<' adds 20.
	second.Reason = text(reason{secondReason.length - 3, secondReason.escaped + 1})
	if previewDecisionCharge(t, a, second, "x") != d.MaxEvidenceRetainedBytes-a.snapshot.Usage.RetainedBytes()+1 {
		t.Fatal("fixture did not cross by precisely one byte")
	}
	_, _, err = a.append(second, "x", auth, time.Unix(1, 0))
	wantError(t, err, d.ErrRetainedByteLimit)
	service, m := fixture(t)
	m.actor.Reference = "x"
	service.now = func() time.Time { return time.Unix(1, 0) }
	installSnapshot(m, a.snapshot)
	_, err = service.Execute(context.Background(), second)
	wantError(t, err, ErrLimit)
	if m.commits != 0 {
		t.Fatal("byte overflow committed")
	}
	installSnapshot(m, exact.snapshot)
	view := effective(t, service, second, 100000, nil)
	if view.Desired.HeadRevision != exact.snapshot.Usage.Events() {
		t.Fatal("byte-exhausted history unreadable")
	}
	t.Logf("16 MiB exact complete encoding: %d events", exact.snapshot.Usage.Events())
}

func TestAssignmentObservationBoundaryReachability(t *testing.T) {
	// Every observation requires its own event in the accepted primitive API.
	// A shortest classification claim minimizes all variable text and uses the
	// shortest global/local revision. Original-target claims add tuple material.
	// Later revisions and longer text can only increase the complete charge.
	in := command(t, 1, RecordClassification, 0)
	in.Reason = "x"
	in.Evidence, _ = d.NewEvidence(in.Evidence.ID(), in.TagID, d.OperatorAssertion, "x", d.AcceptedEvidence)
	a, _ := empty(in.AssignmentID, in.TagID)
	auth, _ := d.BindAuthenticatedReviewAuthority("x")
	next, _, err := a.append(in, "x", auth, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	minimum := next.snapshot.Usage.RetainedBytes()
	if minimum*d.MaxEvidenceObservations <= d.MaxEvidenceRetainedBytes {
		t.Fatalf("lower bound %d does not prove observation boundary unreachable", minimum)
	}
	t.Logf("4096 observations need at least %d bytes (%d per minimal claim), exceeding 16 MiB", minimum*d.MaxEvidenceObservations, minimum)
}

func installSnapshot(m *memory, snapshot Snapshot) {
	m.snapshots[snapshot.TagID] = cloneSnapshot(snapshot)
	m.commands = map[d.CommandID]Event{}
	for _, event := range snapshot.Events {
		m.commands[event.Command.CommandID] = event
	}
}
