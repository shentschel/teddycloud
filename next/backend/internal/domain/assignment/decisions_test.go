package assignment

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func decisionFixture(t *testing.T) DecisionLedger {
	t.Helper()
	id, err := ParseID(fmt.Sprintf("asn_%026x", 1))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewDecisionLedger(id, reviewTag(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func decisionCommand(t *testing.T, s DecisionLedger, n int, kind DecisionKind, at int64) DecisionCommand {
	t.Helper()
	id, err := ParseCommandID(fmt.Sprintf("%032x", n))
	if err != nil {
		t.Fatal(err)
	}
	in := DecisionInput{CommandID: id, AssignmentID: s.ID(), TagID: s.TagID(), Kind: kind,
		ExpectedRevision: s.Revision(), ExpectedTagRevision: 2, PolicyRevision: 7,
		Actor: "actor", Reason: "why", EffectiveAt: time.Unix(at, 0)}
	if kind == Assign {
		in.Target, in.ExpectedCatalogRevision = baselineTarget(t, 1, n), 3
	}
	c, err := NewDecisionCommand(in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func decisionRefs(c DecisionCommand) DecisionReferences {
	in := c.Input()
	return DecisionReferences{in.TagID, in.ExpectedTagRevision, in.ExpectedCatalogRevision, in.Target}
}
func admitDecision(t *testing.T, s DecisionLedger, c DecisionCommand) DecisionLedger {
	t.Helper()
	next, r, err := s.Admit(c, decisionRefs(c), time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != s.Revision()+1 || next.Revision() != r.Revision {
		t.Fatal("revision did not advance exactly once")
	}
	return next
}

func TestAssignmentCanonicalReplay(t *testing.T) {
	empty := decisionFixture(t)
	c := decisionCommand(t, empty, 1, Assign, 10)
	s := admitDecision(t, empty, c)
	original := s.Events()[0].Result
	s = admitDecision(t, s, decisionCommand(t, s, 2, Assign, 20))
	s = admitDecision(t, s, decisionCommand(t, s, 3, Retire, 30))
	// Stale expected revision, missing live references, different audit time and
	// retirement must not defeat exact replay or change the original result.
	next, got, err := s.Admit(c, DecisionReferences{}, time.Time{})
	if err != nil || got != original || !reflect.DeepEqual(next, s) {
		t.Fatalf("replay: %+v %v", got, err)
	}
	if r, found, err := s.LookupCommand(c); err != nil || !found || r != original {
		t.Fatal("lookup lost original result")
	}
	unknown := decisionCommand(t, empty, 99, Unassign, 10)
	if _, found, err := s.LookupCommand(unknown); err != nil || found {
		t.Fatal("absent command lookup")
	}
	for name, change := range map[string]func(*DecisionInput){
		"aggregate": func(in *DecisionInput) { in.AssignmentID, _ = ParseID(fmt.Sprintf("asn_%026x", 2)) },
		"Tag":       func(in *DecisionInput) { in.TagID = reviewTag(t, 2) },
		"kind": func(in *DecisionInput) {
			in.Kind, in.Target, in.ExpectedCatalogRevision = Unassign, OriginalTarget{}, 0
		},
		"expected":         func(in *DecisionInput) { in.ExpectedRevision++ },
		"Tag revision":     func(in *DecisionInput) { in.ExpectedTagRevision++ },
		"catalog revision": func(in *DecisionInput) { in.ExpectedCatalogRevision++ },
		"policy":           func(in *DecisionInput) { in.PolicyRevision++ },
		"actor":            func(in *DecisionInput) { in.Actor = "other" },
		"reason":           func(in *DecisionInput) { in.Reason = "other" },
		"time":             func(in *DecisionInput) { in.EffectiveAt = in.EffectiveAt.Add(time.Nanosecond) },
		"content":          func(in *DecisionInput) { in.Target = baselineTarget(t, 2, 1) },
		"version":          func(in *DecisionInput) { in.Target = baselineTarget(t, 1, 2) },
	} {
		t.Run(name, func(t *testing.T) {
			in := c.Input()
			change(&in)
			changed, err := NewDecisionCommand(in)
			if err != nil {
				t.Fatal(err)
			}
			next, _, err := s.Admit(changed, DecisionReferences{}, time.Time{})
			if err != ErrCommandConflict || !reflect.DeepEqual(next, s) {
				t.Fatalf("changed replay: %v", err)
			}
		})
	}
	// Equivalent UTC instants, including monotonic-clock removal, bind equally.
	in := c.Input()
	in.EffectiveAt = in.EffectiveAt.In(time.FixedZone("offset", 3600))
	zoned, err := NewDecisionCommand(in)
	if err != nil || zoned != c {
		t.Fatal("timezone changed canonical identity")
	}
	in.EffectiveAt = time.Now()
	clocked, err := NewDecisionCommand(in)
	if err != nil || clocked.Input().EffectiveAt != in.EffectiveAt.Round(0).UTC() {
		t.Fatal("monotonic state retained")
	}
}

func TestAssignmentDecisionCanonicalGolden(t *testing.T) {
	c := decisionCommand(t, decisionFixture(t), 1, Assign, 10)
	// Literal independent v1 vector. Nested target is the unchanged A1b format.
	want := []byte("TCAD\x01\x01" +
		"\x00\x00\x00\x20" + "00000000000000000000000000000001" +
		"\x00\x00\x00\x1e" + "asn_00000000000000000000000001" +
		"\x00\x00\x00\x1e" + "tag_00000000000000000000000001" + "\x01" +
		"\x00\x00\x00\x00\x00\x00\x00\x00" + "\x00\x00\x00\x00\x00\x00\x00\x02" +
		"\x00\x00\x00\x00\x00\x00\x00\x03" + "\x00\x00\x00\x00\x00\x00\x00\x07" +
		"\x00\x00\x00\x05actor" + "\x00\x00\x00\x03why" +
		"\x00\x00\x00\x00\x00\x00\x00\x0a\x00\x00\x00\x00" +
		"\x00\x00\x00\x4aTCAE\x01\x02" +
		"\x00\x00\x00\x1e" + "cnt_00000000000000000000000001" +
		"\x00\x00\x00\x1e" + "ver_00000000000000000000000001" +
		"\x00\x00\x00\x00\x00")
	s := admitDecision(t, decisionFixture(t), c)
	r := s.Events()[0].Result
	wantResult := append([]byte("TCAD\x01\x02\x00\x00\x00\xfe"), want...)
	wantResult = append(wantResult, []byte("\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00")...)
	wantEvent := append([]byte("TCAD\x01\x03\x00\x00\x01\x14"), wantResult...)
	wantEvent = append(wantEvent, []byte("\x00\x00\x00\x00\x00\x00\x00\x64\x00\x00\x00\x00")...)
	for _, tc := range []struct {
		value encodedReviewValue
		want  []byte
	}{{c, want}, {r, wantResult}, {s.Events()[0], wantEvent}} {
		got := checkedCanonical(t, tc.value)
		if !bytes.Equal(got, tc.want) {
			t.Fatalf("golden got %x\nwant %x", got, tc.want)
		}
		got[0] ^= 0xff
		if !bytes.Equal(checkedCanonical(t, tc.value), tc.want) {
			t.Fatal("canonical output alias")
		}
	}
	if s.Usage().RetainedBytes() != uint64(len(wantEvent)) {
		t.Fatal("event admission omitted nested input/result bytes")
	}
	// Same target under a new command is an explicit new decision.
	in := c.Input()
	in.CommandID, _ = ParseCommandID(fmt.Sprintf("%032x", 2))
	in.ExpectedRevision = 1
	in.EffectiveAt = time.Unix(20, 0)
	repeat, err := NewDecisionCommand(in)
	if err != nil {
		t.Fatal(err)
	}
	s = admitDecision(t, s, repeat)
	if len(s.Events()) != 2 || s.Events()[1].Result.PreviousCommand != c.Input().CommandID {
		t.Fatal("new command was collapsed or prior link lost")
	}
}

func TestAssignmentDecisionStrictTransitionTimes(t *testing.T) {
	kinds := []struct {
		name string
		kind DecisionKind
	}{{"Assign", Assign}, {"Unassign", Unassign}, {"Retire", Retire}}
	for _, previous := range kinds {
		for _, following := range kinds {
			for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
				t.Run(fmt.Sprintf("%s/%s/%s", previous.name, following.name, delta), func(t *testing.T) {
					empty := decisionFixture(t)
					first := decisionCommand(t, empty, 1, previous.kind, 10)
					head := admitDecision(t, empty, first)
					in := decisionCommand(t, head, 2, following.kind, 10).Input()
					// Also reject the same instant expressed with another UTC offset.
					in.EffectiveAt = in.EffectiveAt.Add(delta).In(time.FixedZone("offset", 3600))
					c, err := NewDecisionCommand(in)
					if err != nil {
						t.Fatal(err)
					}
					next, result, err := head.Admit(c, decisionRefs(c), time.Unix(100, 0))
					var want error
					if previous.kind == Retire {
						want = ErrDecisionRetired
					} else if delta <= 0 {
						want = ErrInvalidInterval
					}
					if err != want {
						t.Fatalf("transition admission: got %v, want %v", err, want)
					}
					if want != nil {
						if result != (DecisionResult{}) || !reflect.DeepEqual(next, head) {
							t.Fatal("rejected transition changed events, revision or usage")
						}
					} else if next.Revision() != 2 || len(next.Events()) != 2 || result.Revision != 2 {
						t.Fatal("one-nanosecond-later transition was not admitted exactly once")
					}
					// Exact replay is still checked before strict temporal admission.
					replayed, original, err := head.Admit(first, DecisionReferences{}, time.Time{})
					if err != nil || original != head.Events()[0].Result || !reflect.DeepEqual(replayed, head) {
						t.Fatal("strict times rejected exact replay")
					}
				})
			}
		}
	}
}

func TestAssignmentSameRevisionRace(t *testing.T) {
	s := decisionFixture(t)
	a, b := decisionCommand(t, s, 1, Assign, 10), decisionCommand(t, s, 2, Assign, 20)
	// A fake serialized owner demonstrates CAS on the installed snapshot. The
	// pure value itself cannot serialize two independent forks of revision zero.
	var mu sync.Mutex
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, c := range []DecisionCommand{a, b} {
		wg.Add(1)
		go func(c DecisionCommand) {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			next, _, err := s.Admit(c, decisionRefs(c), time.Unix(100, 0))
			if err == nil {
				s = next
			}
			outcomes <- err
		}(c)
	}
	wg.Wait()
	close(outcomes)
	accepted, conflicts := 0, 0
	for err := range outcomes {
		if err == nil {
			accepted++
		} else if err == ErrRevisionConflict {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || conflicts != 1 || s.Revision() != 1 || len(s.Events()) != 1 {
		t.Fatal("same revision permitted last-writer-wins")
	}
	for _, order := range [][2]DecisionCommand{{a, b}, {b, a}} {
		head := admitDecision(t, decisionFixture(t), order[0])
		next, _, err := head.Admit(order[1], decisionRefs(order[1]), time.Unix(100, 0))
		if err != ErrRevisionConflict || !reflect.DeepEqual(next, head) {
			t.Fatal("loser overwrote winner")
		}
	}
}

func TestAssignmentDecisionDependenciesAndValidation(t *testing.T) {
	s := decisionFixture(t)
	c := decisionCommand(t, s, 1, Assign, 10)
	for name, change := range map[string]func(*DecisionReferences){
		"Tag":               func(r *DecisionReferences) { r.TagID = reviewTag(t, 2) },
		"Tag revision":      func(r *DecisionReferences) { r.TagRevision++ },
		"catalog revision":  func(r *DecisionReferences) { r.CatalogRevision++ },
		"content ownership": func(r *DecisionReferences) { r.Target = baselineTarget(t, 2, 1) },
		"version":           func(r *DecisionReferences) { r.Target = baselineTarget(t, 1, 2) },
	} {
		t.Run(name, func(t *testing.T) {
			refs := decisionRefs(c)
			change(&refs)
			next, _, err := s.Admit(c, refs, time.Unix(100, 0))
			if err != ErrDependencyConflict || !reflect.DeepEqual(next, s) {
				t.Fatal("unchecked dependency admitted")
			}
		})
	}
	for _, id := range []string{"", strings.Repeat("0", 32), strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("1", 31), strings.Repeat("1", 33)} {
		if _, err := ParseCommandID(id); err == nil {
			t.Fatalf("invalid command ID %q", id)
		}
	}
	for name, change := range map[string]func(*DecisionInput){
		"command":           func(in *DecisionInput) { in.CommandID = CommandID{} },
		"aggregate":         func(in *DecisionInput) { in.AssignmentID = ID{} },
		"Tag":               func(in *DecisionInput) { in.TagID = identity.TagID{} },
		"kind":              func(in *DecisionInput) { in.Kind = 99 },
		"missing tuple":     func(in *DecisionInput) { in.Target = OriginalTarget{} },
		"missing content":   func(in *DecisionInput) { in.Target.content = identity.ContentID{} },
		"missing version":   func(in *DecisionInput) { in.Target.version = identity.ContentVersionID{} },
		"catalog zero":      func(in *DecisionInput) { in.ExpectedCatalogRevision = 0 },
		"Tag zero":          func(in *DecisionInput) { in.ExpectedTagRevision = 0 },
		"policy zero":       func(in *DecisionInput) { in.PolicyRevision = 0 },
		"expected overflow": func(in *DecisionInput) { in.ExpectedRevision = MaxDecisionRevision + 1 },
		"Tag overflow":      func(in *DecisionInput) { in.ExpectedTagRevision = MaxDecisionRevision + 1 },
		"catalog overflow":  func(in *DecisionInput) { in.ExpectedCatalogRevision = MaxDecisionRevision + 1 },
		"policy overflow":   func(in *DecisionInput) { in.PolicyRevision = MaxDecisionRevision + 1 },
		"time zero":         func(in *DecisionInput) { in.EffectiveAt = time.Time{} },
		"time range":        func(in *DecisionInput) { in.EffectiveAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"actor empty":       func(in *DecisionInput) { in.Actor = "" },
		"reason controls":   func(in *DecisionInput) { in.Reason = "bad\nreason" },
		"actor long":        func(in *DecisionInput) { in.Actor = strings.Repeat("x", MaxReviewReferenceBytes+1) },
		"reason long":       func(in *DecisionInput) { in.Reason = strings.Repeat("x", MaxReviewReasonBytes+1) },
		"UTF8":              func(in *DecisionInput) { in.Reason = "\xff" },
		"inactive target":   func(in *DecisionInput) { in.Kind = Unassign },
	} {
		t.Run(name, func(t *testing.T) {
			in := c.Input()
			change(&in)
			if _, err := NewDecisionCommand(in); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := (DecisionCommand{in}).CanonicalBytes(); err == nil {
				t.Fatal("invalid canonical input accepted")
			}
		})
	}
	if _, _, err := s.Admit(c, decisionRefs(c), time.Time{}); err == nil {
		t.Fatal("missing audit time accepted")
	}
	if _, _, err := (DecisionLedger{}).Admit(c, decisionRefs(c), time.Unix(100, 0)); err == nil {
		t.Fatal("zero ledger accepted")
	}
	if _, err := NewDecisionLedger(ID{}, s.TagID()); err == nil {
		t.Fatal("zero aggregate accepted")
	}
	if _, err := NewDecisionLedger(s.ID(), identity.TagID{}); err == nil {
		t.Fatal("zero Tag accepted")
	}
}

func TestAssignmentDecisionRevisionOverflow(t *testing.T) {
	s := decisionFixture(t)
	s = admitDecision(t, s, decisionCommand(t, s, 1, Assign, 10))
	// Internal arithmetic seam only: event/byte limits prevent a real retained
	// aggregate reaching MaxInt64. This is not a reconstruction/count fixture.
	s.revision = MaxDecisionRevision - 1
	c := decisionCommand(t, s, 2, Assign, 20)
	s = admitDecision(t, s, c)
	if s.Revision() != MaxDecisionRevision {
		t.Fatal("signed ceiling not reached exactly")
	}
	next, _, err := s.Admit(decisionCommand(t, s, 3, Unassign, 30), DecisionReferences{}, time.Unix(100, 0))
	if err != ErrRevisionOverflow || !reflect.DeepEqual(next, s) {
		t.Fatal("revision wrapped")
	}
	if _, _, err := s.Admit(c, DecisionReferences{}, time.Time{}); err != nil {
		t.Fatal("overflow blocked exact replay")
	}
}

func TestAssignmentDecisionProspectiveLimits(t *testing.T) {
	s := decisionFixture(t)
	c := decisionCommand(t, s, 1, Assign, 10)
	event := DecisionEvent{DecisionResult{Command: c, Revision: 1}, time.Unix(100, 0)}
	n, err := event.CanonicalByteLen()
	if err != nil {
		t.Fatal(err)
	}
	// Seed only usage arithmetic to isolate this slice's prospective checks.
	// These are not claims of full durable command/result/index accounting.
	for _, tc := range []struct {
		events, bytes uint64
		want          error
	}{
		{MaxEvidenceEvents - 1, 0, nil}, {MaxEvidenceEvents, 0, ErrEvidenceEventLimit},
		{0, MaxEvidenceRetainedBytes - n, nil}, {0, MaxEvidenceRetainedBytes - n + 1, ErrRetainedByteLimit},
	} {
		head := s
		head.usage, err = NewEvidenceUsage(tc.events, 0, 0, tc.bytes)
		if err != nil {
			t.Fatal(err)
		}
		next, _, err := head.Admit(c, decisionRefs(c), time.Unix(100, 0))
		if !errors.Is(err, tc.want) {
			t.Fatalf("prospective limit: %v", err)
		}
		if err != nil && !reflect.DeepEqual(next, head) {
			t.Fatal("failed admission mutated state")
		}
		if err == nil && (next.Usage().Events() != tc.events+1 || next.Usage().RetainedBytes() != tc.bytes+n) {
			t.Fatal("wrong prospective charge")
		}
	}
}

func TestAssignmentDecisionImmutability(t *testing.T) {
	empty := decisionFixture(t)
	c := decisionCommand(t, empty, 1, Assign, 10)
	first := admitDecision(t, empty, c)
	before := first.Events()
	second := admitDecision(t, first, decisionCommand(t, first, 2, Unassign, 20))
	copy := first.Events()
	copy[0].Result.Revision = 99
	copy[0].RecordedAt = time.Time{}
	in := c.Input()
	in.Actor = "changed"
	out := second.Events()
	out[0] = DecisionEvent{}
	if empty.Revision() != 0 || len(empty.Events()) != 0 || !reflect.DeepEqual(first.Events(), before) || second.Events()[0] != before[0] {
		t.Fatal("append or output mutated retained snapshot")
	}
	// The interval prototype remains independent and is never closed by ledger.
	proto := revision(t, 1, 10, 0)
	h, err := NewHistory([]Assignment{proto})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := h.CurrentAt(time.Unix(30, 0)); !ok || got != proto || !got.Effective().Until().IsZero() {
		t.Fatal("prototype was rewritten")
	}
}

func TestAssignmentDecisionLimitReachability(t *testing.T) {
	s := decisionFixture(t)
	// Build a valid retained prefix directly to avoid quadratic snapshot copying
	// in this fixture. The final exact/+1 steps use normal prospective admission.
	for n := 1; n < int(MaxEvidenceEvents); n++ {
		c := decisionCommand(t, s, n, Assign, int64(n))
		r := DecisionResult{Command: c, Revision: uint64(n)}
		if n > 1 {
			r.PreviousCommand = s.events[len(s.events)-1].Result.Command.Input().CommandID
		}
		e := DecisionEvent{Result: r, RecordedAt: time.Unix(100, 0).UTC()}
		length, err := e.CanonicalByteLen()
		if err != nil {
			t.Fatal(err)
		}
		s.usage, err = s.usage.Prospective(EvidenceDelta{Events: 1, RetainedBytes: length})
		if err != nil {
			t.Fatal(err)
		}
		s.events = append(s.events, e)
		s.revision = r.Revision
	}
	c := decisionCommand(t, s, int(MaxEvidenceEvents), Assign, int64(MaxEvidenceEvents))
	s = admitDecision(t, s, c)
	if uint64(len(s.Events())) != MaxEvidenceEvents || s.Usage().Events() != MaxEvidenceEvents {
		t.Fatal("exact event boundary not reached")
	}
	over := decisionCommand(t, s, int(MaxEvidenceEvents)+1, Assign, int64(MaxEvidenceEvents)+1)
	next, _, err := s.Admit(over, decisionRefs(over), time.Unix(100, 0))
	if err != ErrEvidenceEventLimit || !reflect.DeepEqual(next, s) {
		t.Fatalf("event +1: %v", err)
	}
	if _, _, err := s.Admit(c, DecisionReferences{}, time.Time{}); err != nil {
		t.Fatal("full ledger blocked replay")
	}
	// Every ID and scalar has fixed width. Max actor/reason, Assign's target
	// and a nonempty prior command give the largest event in this URI/support-
	// absent profile. Thus the 16 MiB byte boundary is unreachable before 4096
	// events here. The separate seeded test proves arithmetic, not reachability.
	in := c.Input()
	in.Actor, in.Reason = strings.Repeat("x", MaxReviewReferenceBytes), strings.Repeat("x", MaxReviewReasonBytes)
	maxCommand, err := NewDecisionCommand(in)
	if err != nil {
		t.Fatal(err)
	}
	maxEvent := s.Events()[len(s.Events())-1]
	maxEvent.Result.Command = maxCommand
	length, err := maxEvent.CanonicalByteLen()
	if err != nil || length != 1474 || length*MaxEvidenceEvents >= MaxEvidenceRetainedBytes {
		t.Fatalf("invalid byte upper bound: %d, %v", length, err)
	}
}
