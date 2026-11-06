package assignment

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	c "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	e "github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// The only in-memory port is test code. Copy-on-transaction gives rollback and
// a global index independent of Tag. Scoped handles reject calls after exit.
type memory struct {
	mu                                       sync.Mutex
	actor                                    AuthenticatedActor
	tags                                     map[identity.TagID]TagSnapshot
	targets                                  map[d.OriginalTarget]TargetSnapshot
	snapshots                                map[identity.TagID]Snapshot
	commands                                 map[d.CommandID]Event
	calls, commits                           int
	fault                                    error
	beforeCommit                             func(*scope, Snapshot) error
	enter                                    func()
	doubleCallback, skipCallback, hideLookup bool
	last                                     *scope
}
type scope struct {
	m         *memory
	live      bool
	snapshots map[identity.TagID]Snapshot
	commands  map[d.CommandID]Event
	commits   int
}

func (m *memory) WithinAssignmentTransaction(ctx context.Context, fn func(Port) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.fault != nil {
		return m.fault
	}
	if m.skipCallback {
		return nil
	}
	p := &scope{m: m, live: true, snapshots: map[identity.TagID]Snapshot{}, commands: map[d.CommandID]Event{}}
	for id, s := range m.snapshots {
		p.snapshots[id] = cloneSnapshot(s)
	}
	for id, event := range m.commands {
		p.commands[id] = event
	}
	m.last = p
	defer func() { p.live = false }()
	if m.enter != nil {
		m.enter()
	}
	if err := fn(p); err != nil {
		return err
	}
	if m.doubleCallback {
		if err := fn(p); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.snapshots, m.commands = p.snapshots, p.commands
	m.commits += p.commits
	return nil
}
func (p *scope) check(ctx context.Context) error {
	if !p.live {
		return ErrUnavailable
	}
	return ctx.Err()
}
func (p *scope) Authenticate(ctx context.Context) (AuthenticatedActor, error) {
	if err := p.check(ctx); err != nil {
		return AuthenticatedActor{}, err
	}
	return p.m.actor, nil
}
func (p *scope) Tag(ctx context.Context, id identity.TagID) (TagSnapshot, bool, error) {
	if err := p.check(ctx); err != nil {
		return TagSnapshot{}, false, err
	}
	v, ok := p.m.tags[id]
	return v, ok, nil
}
func (p *scope) Target(ctx context.Context, id d.OriginalTarget) (TargetSnapshot, bool, error) {
	if err := p.check(ctx); err != nil {
		return TargetSnapshot{}, false, err
	}
	v, ok := p.m.targets[id]
	return v, ok, nil
}
func (p *scope) Load(ctx context.Context, id identity.TagID) (Snapshot, bool, error) {
	if err := p.check(ctx); err != nil {
		return Snapshot{}, false, err
	}
	v, ok := p.snapshots[id]
	return cloneSnapshot(v), ok, nil
}
func (p *scope) LookupCommand(ctx context.Context, id d.CommandID) (Event, bool, error) {
	if err := p.check(ctx); err != nil {
		return Event{}, false, err
	}
	v, ok := p.commands[id]
	if p.m.hideLookup {
		ok = false
	}
	return v, ok, nil
}
func (p *scope) Commit(ctx context.Context, expected uint64, snapshot Snapshot) error {
	if err := p.check(ctx); err != nil {
		return err
	}
	if p.m.beforeCommit != nil {
		if err := p.m.beforeCommit(p, snapshot); err != nil {
			return err
		}
	}
	prior, found := p.snapshots[snapshot.TagID]
	if uint64(len(prior.Events)) != expected {
		return ErrRevisionConflict
	}
	if found && prior.ID != snapshot.ID {
		return ErrCommandConflict
	}
	if len(snapshot.Events) != int(expected)+1 {
		return ErrUnavailable
	}
	for tag, s := range p.snapshots {
		if tag != snapshot.TagID && s.ID == snapshot.ID {
			return ErrCommandConflict
		}
	}
	event := snapshot.Events[len(snapshot.Events)-1]
	if _, ok := p.commands[event.Command.CommandID]; ok {
		return ErrCommandConflict
	}
	tag := p.m.tags[snapshot.TagID]
	if tag.Revision != event.Command.ExpectedTagRevision {
		return ErrDependencyConflict
	}
	if event.Command.Kind.target() {
		if p.m.targets[event.Command.Target].CatalogRevision != event.Command.ExpectedCatalogRevision {
			return ErrDependencyConflict
		}
	}
	p.snapshots[snapshot.TagID] = cloneSnapshot(snapshot)
	p.commands[event.Command.CommandID] = event
	p.commits++
	return nil
}
func fixture(t *testing.T) (*Service, *memory) {
	t.Helper()
	m := &memory{actor: AuthenticatedActor{Reference: "operator", Read: true, Decide: true, Review: true}, tags: map[identity.TagID]TagSnapshot{}, targets: map[d.OriginalTarget]TargetSnapshot{}, snapshots: map[identity.TagID]Snapshot{}, commands: map[d.CommandID]Event{}}
	for n := 1; n <= 2; n++ {
		tag, _ := identity.ParseTagID(fmt.Sprintf("tag_%026x", n))
		m.tags[tag] = TagSnapshot{tag, 2}
		contentID, _ := identity.ParseContentID(fmt.Sprintf("cnt_%026x", n))
		versionID, _ := identity.ParseContentVersionID(fmt.Sprintf("ver_%026x", n))
		target, err := d.NewOriginalTarget(contentID, versionID)
		if err != nil {
			t.Fatal(err)
		}
		content, _ := c.NewContent(contentID, c.NewContentFacts("same title", c.ProductIdentifiers{}))
		audio, _ := c.NewAudioID(1)
		hash, _ := c.ParseAudioHash(strings.Repeat("a", 40))
		fingerprint, _ := c.NewAudioFingerprint(audio, hash)
		version, _ := c.NewContentVersion(versionID, contentID, fingerprint, c.VersionOrderEvidence{})
		owner, _ := c.OwnershipClaim(contentID)
		fp, _ := c.FingerprintClaim(fingerprint)
		support, err := e.ParseID(fmt.Sprintf("obs_%026x", n))
		if err != nil {
			t.Fatal(err)
		}
		field := func(claim c.CatalogClaim) c.CatalogField {
			return c.CatalogField{Status: c.CatalogFactAccepted, Alternatives: []c.CatalogAlternative{{Claim: claim, Support: []e.ID{support}}}}
		}
		dims, _ := d.NewContentDimensions(true, true, d.ProtectedCustom)
		m.targets[target] = TargetSnapshot{Content: content, Version: version, CatalogRevision: 3, Managed: true, Ownership: field(owner), Fingerprint: field(fp), Dimensions: dims}
	}
	s := New(m)
	s.now = func() time.Time { return time.Unix(1000, 0) }
	return s, m
}
func command(t *testing.T, n int, k Kind, rev uint64) Command {
	t.Helper()
	id, _ := d.ParseCommandID(fmt.Sprintf("%032x", n))
	assignment, _ := d.ParseID(fmt.Sprintf("asn_%026x", 1))
	tag, _ := identity.ParseTagID(fmt.Sprintf("tag_%026x", 1))
	in := Command{CommandID: id, AssignmentID: assignment, TagID: tag, Kind: k, ExpectedRevision: rev, ExpectedTagRevision: 2, PolicyRevision: PolicyRevision, Reason: "explicit reason"}
	if k.target() {
		content, _ := identity.ParseContentID(fmt.Sprintf("cnt_%026x", 1))
		version, _ := identity.ParseContentVersionID(fmt.Sprintf("ver_%026x", 1))
		in.Target, _ = d.NewOriginalTarget(content, version)
		in.ExpectedCatalogRevision = 3
	}
	if k.review() {
		in.ReviewID, _ = d.ParseReviewID(fmt.Sprintf("rev_%026x", n))
		if k == RecordClassification || k == RecordOriginalTarget {
			eid, _ := d.ParseEvidenceID(fmt.Sprintf("evd_%026x", n))
			in.Evidence, _ = d.NewEvidence(eid, tag, d.OperatorAssertion, "operator assertion", d.AcceptedEvidence)
		}
		if k == RecordClassification || k == ResolveClassification {
			in.Classification = d.Original
		}
	} else {
		in.EffectiveAt = time.Unix(int64(n*10), 0)
	}
	return in
}
func execute(t *testing.T, s *Service, in Command) Result {
	t.Helper()
	r, err := s.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("command %d/%d: %v", in.Kind, in.ExpectedRevision, err)
	}
	if r.Revision != in.ExpectedRevision+1 {
		t.Fatal("global head did not advance once")
	}
	return r
}
func wantError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAssignmentAuthority(t *testing.T) {
	for _, k := range []Kind{Assign, Unassign, RecordClassification, ResolveClassification, RecordOriginalTarget, ResolveOriginalTarget, RetireAssignment} {
		t.Run(fmt.Sprint(k), func(t *testing.T) {
			s, m := fixture(t)
			in := command(t, 1, k, 0)
			in.Actor = "spoofed manufacturer"
			if k == ResolveClassification || k == ResolveOriginalTarget {
				eid, _ := d.ParseEvidenceID(fmt.Sprintf("evd_%026x", 1))
				in.Support = []d.EvidenceID{eid}
			}
			_, err := s.Execute(context.Background(), in)
			wantError(t, err, ErrUnauthorized)
			if m.commits != 0 {
				t.Fatal("spoofed actor wrote")
			}
			in.Actor = ""
			m.actor.Decide = false
			m.actor.Review = false
			_, err = s.Execute(context.Background(), in)
			wantError(t, err, ErrUnauthorized)
		})
	}
	s, m := fixture(t)
	in := command(t, 1, RecordClassification, 0)
	in.Actor = m.actor.Reference
	execute(t, s, in)
	stored := m.snapshots[in.TagID].Events[0]
	if stored.Actor != "operator" || stored.Command.Actor != "" {
		t.Fatal("request actor retained as authority")
	}
	m.actor.Reference = "another authenticated actor"
	_, _, err := s.LookupAssignmentCommand(context.Background(), in)
	wantError(t, err, ErrUnauthorized)
	in.Actor = ""
	_, _, err = s.LookupAssignmentCommand(context.Background(), in)
	wantError(t, err, ErrCommandConflict)
	m.actor.Read = false
	_, err = s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: in.TagID, At: time.Unix(100, 0)})
	wantError(t, err, ErrUnauthorized)
	m.actor.Reference = ""
	_, err = s.Execute(context.Background(), command(t, 2, Assign, 1))
	wantError(t, err, ErrUnauthorized)
}

func TestAssignmentServiceDependencies(t *testing.T) {
	cases := map[string]struct {
		mutate func(*memory, Command)
		change func(*Command)
		want   error
	}{
		"missing Tag":      {mutate: func(m *memory, in Command) { delete(m.tags, in.TagID) }, want: ErrTagNotFound},
		"Tag revision":     {change: func(in *Command) { in.ExpectedTagRevision++ }, want: ErrDependencyConflict},
		"catalog revision": {change: func(in *Command) { in.ExpectedCatalogRevision++ }, want: ErrDependencyConflict},
		"missing tuple":    {mutate: func(m *memory, in Command) { delete(m.targets, in.Target) }, want: ErrTargetNotFound},
		"wrong stored ownership": {mutate: func(m *memory, in Command) {
			v := m.targets[in.Target]
			for key, x := range m.targets {
				if key != in.Target {
					v.Version = x.Version
					break
				}
			}
			m.targets[in.Target] = v
		}, want: ErrUnavailable},
		"unmanaged": {mutate: func(m *memory, in Command) { v := m.targets[in.Target]; v.Managed = false; m.targets[in.Target] = v }, want: ErrUnsupportedState},
		"unreviewed": {mutate: func(m *memory, in Command) {
			v := m.targets[in.Target]
			v.Ownership = c.CatalogField{}
			m.targets[in.Target] = v
		}, want: ErrUnsupportedState},
		"conflicted fingerprint": {mutate: func(m *memory, in Command) {
			v := m.targets[in.Target]
			v.Fingerprint.Status = c.CatalogFactConflict
			m.targets[in.Target] = v
		}, want: ErrUnsupportedState},
		"no accepted support": {mutate: func(m *memory, in Command) {
			v := m.targets[in.Target]
			v.Fingerprint.Alternatives[0].Support = nil
			m.targets[in.Target] = v
		}, want: ErrUnsupportedState},
		"stale aggregate": {change: func(in *Command) { in.ExpectedRevision = 1 }, want: ErrRevisionConflict},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			s, m := fixture(t)
			in := command(t, 1, Assign, 0)
			if test.change != nil {
				test.change(&in)
			}
			if test.mutate != nil {
				test.mutate(m, in)
			}
			_, err := s.Execute(context.Background(), in)
			wantError(t, err, test.want)
			if m.commits != 0 {
				t.Fatal("failed admission committed")
			}
		})
	}
	for _, dependency := range []string{"Tag", "catalog"} {
		t.Run("changed at CAS "+dependency, func(t *testing.T) {
			s, m := fixture(t)
			in := command(t, 1, Assign, 0)
			m.beforeCommit = func(_ *scope, _ Snapshot) error {
				if dependency == "Tag" {
					v := m.tags[in.TagID]
					v.Revision++
					m.tags[in.TagID] = v
				} else {
					v := m.targets[in.Target]
					v.CatalogRevision++
					m.targets[in.Target] = v
				}
				return nil
			}
			_, err := s.Execute(context.Background(), in)
			wantError(t, err, ErrDependencyConflict)
			if len(m.snapshots) != 0 {
				t.Fatal("partial commit")
			}
		})
	}
	// Original-baseline writers have the same exact tuple admission.
	for _, k := range []Kind{RecordOriginalTarget, ResolveOriginalTarget} {
		s, m := fixture(t)
		in := command(t, 1, k, 0)
		if k == ResolveOriginalTarget {
			eid, _ := d.ParseEvidenceID(fmt.Sprintf("evd_%026x", 1))
			in.Support = []d.EvidenceID{eid}
		}
		delete(m.targets, in.Target)
		_, err := s.Execute(context.Background(), in)
		wantError(t, err, ErrTargetNotFound)
	}
}

func TestAssignmentGlobalReplayAndCAS(t *testing.T) {
	s, m := fixture(t)
	first := command(t, 1, Assign, 0)
	original := execute(t, s, first)
	sameTarget := command(t, 2, Assign, 1)
	execute(t, s, sameTarget)
	retire := command(t, 3, RetireAssignment, 2)
	execute(t, s, retire)
	delete(m.targets, first.Target)
	delete(m.tags, first.TagID)
	before := m.commits
	replay, err := s.Execute(context.Background(), first)
	if err != nil || replay != original || m.commits != before {
		t.Fatal("replay re-admitted dependencies or wrote", err)
	}
	got, found, err := s.LookupAssignmentCommand(context.Background(), first)
	if err != nil || !found || got != original {
		t.Fatal("lost deterministic readback", err)
	}
	changes := map[string]func(*Command){
		"reason": func(in *Command) { in.Reason += " changed" }, "expected head": func(in *Command) { in.ExpectedRevision++ },
		"Tag revision": func(in *Command) { in.ExpectedTagRevision++ }, "catalog revision": func(in *Command) { in.ExpectedCatalogRevision++ },
		"effective": func(in *Command) { in.EffectiveAt = in.EffectiveAt.Add(time.Second) },
		"cross Tag": func(in *Command) {
			in.TagID, _ = identity.ParseTagID(fmt.Sprintf("tag_%026x", 2))
			in.AssignmentID, _ = d.ParseID(fmt.Sprintf("asn_%026x", 2))
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			in := first
			change(&in)
			_, err := s.Execute(context.Background(), in)
			wantError(t, err, ErrCommandConflict)
		})
	}
	absent := command(t, 99, Unassign, 3)
	_, ok, err := s.LookupAssignmentCommand(context.Background(), absent)
	if err != nil || ok {
		t.Fatal("absent lookup", err)
	}
	// Defense is also at the commit fence, independent of lookup completeness.
	s, m = fixture(t)
	execute(t, s, first)
	m.hideLookup = true
	cross := first
	cross.TagID, _ = identity.ParseTagID(fmt.Sprintf("tag_%026x", 2))
	cross.AssignmentID, _ = d.ParseID(fmt.Sprintf("asn_%026x", 2))
	_, err = s.Execute(context.Background(), cross)
	wantError(t, err, ErrCommandConflict)
	if len(m.snapshots) != 1 || m.commits != 1 {
		t.Fatal("global command collision partially committed")
	}
	s, m = fixture(t)
	execute(t, s, first)
	loser := command(t, 2, Unassign, 0)
	_, err = s.Execute(context.Background(), loser)
	wantError(t, err, ErrRevisionConflict)
	// One aggregate ID cannot be reused for a second Tag.
	other := command(t, 3, Unassign, 0)
	other.TagID = cross.TagID
	_, err = s.Execute(context.Background(), other)
	wantError(t, err, ErrCommandConflict)
}

func TestAssignmentTransactionLifecycle(t *testing.T) {
	s, m := fixture(t)
	in := command(t, 1, Assign, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Execute(ctx, in)
	wantError(t, err, context.Canceled)
	if m.calls != 0 {
		t.Fatal("canceled operation entered transaction")
	}
	ctx, cancel = context.WithCancel(context.Background())
	m.beforeCommit = func(_ *scope, _ Snapshot) error { cancel(); return nil }
	_, err = s.Execute(ctx, in)
	wantError(t, err, context.Canceled)
	if m.commits != 0 {
		t.Fatal("cancellation committed")
	}
	m.beforeCommit = nil
	execute(t, s, in)
	_, _, err = m.last.Load(context.Background(), in.TagID)
	wantError(t, err, ErrUnavailable)
	for _, fault := range []error{ErrContention, ErrUnsupportedState, ErrIndeterminate, errors.New("database path secret")} {
		m.fault = fault
		_, err = s.Execute(context.Background(), in)
		if fault.Error() == "database path secret" {
			wantError(t, err, ErrUnavailable)
		} else {
			wantError(t, err, fault)
		}
	}
	m.fault = nil
	m.doubleCallback = true
	_, err = s.Execute(context.Background(), command(t, 2, Unassign, 1))
	wantError(t, err, ErrUnavailable)
	if m.commits != 1 {
		t.Fatal("double callback committed")
	}
	m.doubleCallback = false
	m.skipCallback = true
	_, err = s.Execute(context.Background(), in)
	wantError(t, err, ErrUnavailable)
	s, m = fixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	m.enter = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() { _, err := s.Execute(context.Background(), in); done <- err }()
	<-entered
	_, err = s.Execute(context.Background(), in)
	wantError(t, err, ErrBusy)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentCorruptReadback(t *testing.T) {
	for _, change := range []func(*Snapshot){
		func(s *Snapshot) { s.Events[0].Result.Revision++ }, func(s *Snapshot) { s.Events[0].Primitive[0] ^= 1 },
		func(s *Snapshot) { s.Usage = d.EvidenceUsage{} }, func(s *Snapshot) { s.Events = append(s.Events, s.Events[0]) },
		func(s *Snapshot) { s.Events[0].Actor = "" }, func(s *Snapshot) { s.Events[0].Command.Actor = "operator" },
	} {
		s, m := fixture(t)
		in := command(t, 1, Assign, 0)
		execute(t, s, in)
		snapshot := m.snapshots[in.TagID]
		change(&snapshot)
		m.snapshots[in.TagID] = snapshot
		_, err := s.GetEffectiveAssignment(context.Background(), EffectiveQuery{TagID: in.TagID, At: time.Unix(100, 0)})
		wantError(t, err, ErrUnavailable)
		_, _, err = s.LookupAssignmentCommand(context.Background(), in)
		wantError(t, err, ErrUnavailable)
	}
	t.Run("noncanonical URI in retained history", func(t *testing.T) {
		s, m := fixture(t)
		in := command(t, 1, Assign, 0)
		in.URI = OptionalURI{Present: true, Value: "HTTPS://Catalog.Example:443/a%62"}
		execute(t, s, in)
		snapshot := m.snapshots[in.TagID]
		snapshot.Events[0].Command.URI = in.URI
		m.snapshots[in.TagID] = snapshot
		_, err := s.GetEffectiveAssignment(t.Context(), EffectiveQuery{TagID: in.TagID, At: time.Unix(100, 0)})
		wantError(t, err, ErrUnavailable)
		_, _, err = s.LookupAssignmentCommand(t.Context(), in)
		wantError(t, err, ErrUnavailable)
	})
	t.Run("noncanonical URI in command index", func(t *testing.T) {
		s, m := fixture(t)
		in := command(t, 1, Assign, 0)
		in.URI = OptionalURI{Present: true, Value: "HTTPS://Catalog.Example:443/a%62"}
		execute(t, s, in)
		indexed := m.commands[in.CommandID]
		indexed.Command.URI = in.URI
		m.commands[in.CommandID] = indexed
		_, _, err := s.LookupAssignmentCommand(t.Context(), in)
		wantError(t, err, ErrUnavailable)
	})
	// The snapshot/command index must agree; an index hit alone is not a result.
	s, m := fixture(t)
	in := command(t, 1, Assign, 0)
	execute(t, s, in)
	delete(m.snapshots, in.TagID)
	_, _, err := s.LookupAssignmentCommand(context.Background(), in)
	wantError(t, err, ErrUnavailable)
}

func TestAssignmentImmutablePortValues(t *testing.T) {
	s, m := fixture(t)
	in := command(t, 1, RecordClassification, 0)
	execute(t, s, in)
	old := cloneSnapshot(m.snapshots[in.TagID])
	execute(t, s, command(t, 2, Assign, 1))
	if !reflect.DeepEqual(old.Events, m.snapshots[in.TagID].Events[:1]) {
		t.Fatal("append rewrote retained event")
	}
}

func TestAssignmentConcurrentOwnerFences(t *testing.T) {
	for _, globalCollision := range []bool{false, true} {
		t.Run(fmt.Sprint(globalCollision), func(t *testing.T) {
			first, m := fixture(t)
			second := New(m)
			second.now = first.now
			left := command(t, 1, Assign, 0)
			right := command(t, 2, Assign, 0)
			want := ErrRevisionConflict
			if globalCollision {
				right.CommandID = left.CommandID
				right.TagID, _ = identityTag(2)
				right.AssignmentID, _ = d.ParseID(fmt.Sprintf("asn_%026x", 2))
				want = ErrCommandConflict
			}
			start := make(chan struct{})
			done := make(chan error, 2)
			go func() { <-start; _, err := first.Execute(context.Background(), left); done <- err }()
			go func() { <-start; _, err := second.Execute(context.Background(), right); done <- err }()
			close(start)
			one, two := <-done, <-done
			if one == nil {
				wantError(t, two, want)
			} else {
				wantError(t, one, want)
				if two != nil {
					t.Fatal(two)
				}
			}
			if m.commits != 1 || len(m.snapshots) != 1 {
				t.Fatal("owner fence admitted both commands")
			}
		})
	}
}
func TestAssignmentTransitionAdmissionAcrossReviews(t *testing.T) {
	s, m := fixture(t)
	in := command(t, 1, Assign, 0)
	execute(t, s, in)
	execute(t, s, command(t, 2, RecordClassification, 1))
	for _, at := range []time.Time{in.EffectiveAt, in.EffectiveAt.Add(-time.Nanosecond)} {
		unassign := command(t, 3, Unassign, 2)
		unassign.EffectiveAt = at
		_, err := s.Execute(context.Background(), unassign)
		wantError(t, err, ErrInvalidInput)
	}
	if len(m.snapshots[in.TagID].Events) != 2 {
		t.Fatal("timeline rejection changed head")
	}
	execute(t, s, command(t, 3, Unassign, 2))
	if m.calls != 5 {
		t.Fatalf("unexpected callback count %d", m.calls)
	}
}
