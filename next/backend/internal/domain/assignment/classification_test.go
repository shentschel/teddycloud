package assignment

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func reviewTag(t *testing.T, n int) identity.TagID {
	t.Helper()
	id, err := identity.ParseTagID(fmt.Sprintf("tag_%026x", n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func evidenceID(t *testing.T, n int) EvidenceID {
	t.Helper()
	id, err := ParseEvidenceID(fmt.Sprintf("evd_%026x", n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func reviewRequest(t *testing.T, n int, revision uint64) ReviewRequest {
	t.Helper()
	id, err := ParseReviewID(fmt.Sprintf("rev_%026x", n))
	if err != nil {
		t.Fatal(err)
	}
	q, err := NewReviewRequest(id, revision, 7, "explicit review")
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func reviewCore(t *testing.T) ReviewAuthority {
	t.Helper()
	a, err := newReviewAuthority("core-reviewer", reviewClassification|reviewOriginalTarget|reviewResolution)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func emptyEvidence(t *testing.T) TagEvidence {
	t.Helper()
	s, err := NewTagEvidence(reviewTag(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func claimEvidence(t *testing.T, tag identity.TagID, n int, disposition EvidenceDisposition) Evidence {
	t.Helper()
	e, err := NewEvidence(evidenceID(t, n), tag, OperatorAssertion, "operator-assertion", disposition)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func baselineTarget(t *testing.T, content, version int) OriginalTarget {
	t.Helper()
	c, err := identity.ParseContentID(fmt.Sprintf("cnt_%026x", content))
	if err != nil {
		t.Fatal(err)
	}
	v, err := identity.ParseContentVersionID(fmt.Sprintf("ver_%026x", version))
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewOriginalTarget(c, v)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func addClassification(t *testing.T, s TagEvidence, n int, disposition EvidenceDisposition, class PhysicalClassification) TagEvidence {
	t.Helper()
	next, err := s.AdmitClassification(reviewCore(t), reviewRequest(t, int(s.Revision()+1), s.Revision()), claimEvidence(t, s.Subject(), n, disposition), class)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func addBaseline(t *testing.T, s TagEvidence, n int, disposition EvidenceDisposition, target OriginalTarget) TagEvidence {
	t.Helper()
	next, err := s.AdmitOriginalTarget(reviewCore(t), reviewRequest(t, int(s.Revision()+1), s.Revision()), claimEvidence(t, s.Subject(), n, disposition), target)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestAssignmentClassificationPermutations(t *testing.T) {
	// Every ordering of agreeing, contrary and non-authoritative support has the
	// same result. Neither first/last position nor majority wins.
	var visit func([]int, []int)
	visit = func(order, remaining []int) {
		if len(remaining) != 0 {
			for i, n := range remaining {
				rest := append([]int(nil), remaining[:i]...)
				rest = append(rest, remaining[i+1:]...)
				visit(append(append([]int(nil), order...), n), rest)
			}
			return
		}
		s := emptyEvidence(t)
		for _, n := range order {
			class, disposition := Original, AcceptedEvidence
			if n >= 3 {
				class = CustomCard
			}
			if n == 4 {
				disposition = PendingEvidence
			}
			s = addClassification(t, s, n, disposition, class)
		}
		if s.Classification() != Conflict || s.Revision() != 4 || len(s.Records()) != 4 || s.Usage().Observations() != 4 || s.Usage().SupportLinks() != 4 {
			t.Fatalf("order %v lost evidence or selected a winner", order)
		}
		if _, ok := s.OriginalBaseline(); ok {
			t.Fatal("classification inferred an original target")
		}
	}
	visit(nil, []int{1, 2, 3, 4})

	// Independent content dimensions include public+custom+ProtectedCustom.
	for _, public := range []bool{false, true} {
		for _, custom := range []bool{false, true} {
			for _, protection := range []ContentProtection{ProtectionUnknown, Unprotected, ProtectedCustom} {
				d, err := NewContentDimensions(public, custom, protection)
				if err != nil || d.PublicSupport() != public || d.CustomOrigin() != custom || d.Protection() != protection {
					t.Fatal("content dimensions collapsed")
				}
				for _, class := range []PhysicalClassification{Unknown, Original, CustomCard, Conflict} {
					s := emptyEvidence(t)
					if class != Unknown {
						s = addClassification(t, s, 1, AcceptedEvidence, Original)
					}
					if class == CustomCard {
						s = addClassification(t, emptyEvidence(t), 1, AcceptedEvidence, CustomCard)
					}
					if class == Conflict {
						s = addClassification(t, s, 2, AcceptedEvidence, CustomCard)
					}
					if s.Classification() != class || s.OriginalState() != OriginalTargetUnknown {
						t.Fatal("content facts changed physical state")
					}
				}
			}
		}
	}
}

func TestAssignmentClassificationResolutionReopens(t *testing.T) {
	for _, chosen := range []PhysicalClassification{Original, CustomCard} {
		contrary := CustomCard
		if chosen == CustomCard {
			contrary = Original
		}
		initial := emptyEvidence(t)
		s := addClassification(t, initial, 1, AcceptedEvidence, chosen)
		s = addClassification(t, s, 2, AcceptedEvidence, contrary)
		s = addClassification(t, s, 3, AcceptedEvidence, chosen)
		if initial.Revision() != 0 || len(initial.Records()) != 0 || s.Classification() != Conflict {
			t.Fatal("snapshots mutated or conflict lost")
		}
		before := s.Records()
		support := []EvidenceID{evidenceID(t, 3), evidenceID(t, 1)}
		resolved, err := s.ResolveClassification(reviewCore(t), reviewRequest(t, 4, 3), chosen, support)
		if err != nil || resolved.Classification() != chosen {
			t.Fatalf("resolution: %v", err)
		}
		if !reflect.DeepEqual(s.Records(), before) || !reflect.DeepEqual(resolved.Records()[:3], before) {
			t.Fatal("resolution erased dissent or changed a prior snapshot")
		}
		r := resolved.Records()[3]
		if r.Kind != ClassificationResolution || !reflect.DeepEqual(r.Support, []EvidenceID{evidenceID(t, 1), evidenceID(t, 3)}) {
			t.Fatal("selected support not canonical")
		}
		support[0] = EvidenceID{}
		r.Support[0] = EvidenceID{}
		if resolved.Records()[3].Support[0].IsZero() {
			t.Fatal("resolution aliases input or output")
		}
		resolved = addClassification(t, resolved, 4, AcceptedEvidence, chosen)
		resolved = addClassification(t, resolved, 5, DisputedEvidence, contrary)
		if resolved.Classification() != chosen {
			t.Fatal("agreeing/disputed support reopened conflict")
		}
		resolved = addClassification(t, resolved, 6, AcceptedEvidence, contrary)
		if resolved.Classification() != Conflict {
			t.Fatal("new contrary accepted support did not reopen conflict")
		}
		// A nonempty subset is sufficient; dissent and unselected agreeing support
		// remain retained. Review is not a majority vote.
		resolved, err = resolved.ResolveClassification(reviewCore(t), reviewRequest(t, 8, 7), contrary, []EvidenceID{evidenceID(t, 2)})
		if err != nil || resolved.Classification() != contrary || len(resolved.Records()) != 8 {
			t.Fatal("subset resolution failed")
		}
	}
}

func TestAssignmentNonAcceptedSupportAndInvalidResolution(t *testing.T) {
	s := emptyEvidence(t)
	for n, disposition := range []EvidenceDisposition{PendingEvidence, DisputedEvidence, RejectedEvidence} {
		s = addClassification(t, s, n+1, disposition, Original)
		if s.Classification() != Unknown {
			t.Fatal("unaccepted evidence became authoritative")
		}
	}
	s = addClassification(t, s, 4, AcceptedEvidence, Original)
	s = addClassification(t, s, 5, AcceptedEvidence, CustomCard)
	s = addBaseline(t, s, 6, AcceptedEvidence, baselineTarget(t, 1, 1))
	for _, support := range [][]EvidenceID{
		nil, {{}}, {evidenceID(t, 99)}, {evidenceID(t, 1)}, {evidenceID(t, 2)}, {evidenceID(t, 3)},
		{evidenceID(t, 4), evidenceID(t, 4)}, {evidenceID(t, 4), evidenceID(t, 5)}, {evidenceID(t, 6)},
	} {
		got, err := s.ResolveClassification(reviewCore(t), reviewRequest(t, 7, 6), Original, support)
		if err != ErrInvalidResolution || !reflect.DeepEqual(got, s) {
			t.Fatalf("invalid support admitted: %v", err)
		}
	}
	for _, class := range []PhysicalClassification{Unknown, Conflict, 255} {
		if _, err := s.ResolveClassification(reviewCore(t), reviewRequest(t, 7, 6), class, []EvidenceID{evidenceID(t, 4)}); err != ErrInvalidResolution {
			t.Fatal("invalid resolution class")
		}
	}
}

func TestAssignmentOriginalBaselineNoFallback(t *testing.T) {
	s := emptyEvidence(t)
	a, b := baselineTarget(t, 1, 1), baselineTarget(t, 1, 2)
	if a.ContentID() != b.ContentID() || a.VersionID() == b.VersionID() {
		t.Fatal("fixture tuple")
	}
	assertAbsent := func(state TagEvidence) {
		t.Helper()
		if got, ok := state.OriginalBaseline(); ok || got != (OriginalTarget{}) {
			t.Fatal("missing/conflicted baseline supplied a fallback")
		}
	}
	assertAbsent(s)
	s = addClassification(t, s, 1, AcceptedEvidence, CustomCard)
	assertAbsent(s)
	for n, disposition := range []EvidenceDisposition{PendingEvidence, DisputedEvidence, RejectedEvidence} {
		s = addBaseline(t, s, n+2, disposition, a)
		assertAbsent(s)
	}
	s = addBaseline(t, s, 5, AcceptedEvidence, a)
	s = addBaseline(t, s, 6, AcceptedEvidence, a)
	if got, ok := s.OriginalBaseline(); !ok || got != a || s.Classification() != CustomCard {
		t.Fatal("baseline relation collapsed physical classification")
	}
	s = addBaseline(t, s, 7, AcceptedEvidence, b)
	if s.OriginalState() != OriginalTargetConflict {
		t.Fatal("contrary explicit version did not conflict")
	}
	assertAbsent(s)
	for _, ids := range [][]EvidenceID{nil, {evidenceID(t, 1)}, {evidenceID(t, 2)}, {evidenceID(t, 5), evidenceID(t, 7)}, {evidenceID(t, 5), evidenceID(t, 5)}} {
		got, err := s.ResolveOriginalTarget(reviewCore(t), reviewRequest(t, 8, 7), a, ids)
		if err != ErrInvalidResolution || !reflect.DeepEqual(got, s) {
			t.Fatal("invalid baseline resolution admitted")
		}
	}
	old := s.Records()
	resolved, err := s.ResolveOriginalTarget(reviewCore(t), reviewRequest(t, 8, 7), a, []EvidenceID{evidenceID(t, 6), evidenceID(t, 5)})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := resolved.OriginalBaseline(); !ok || got != a || !reflect.DeepEqual(resolved.Records()[:7], old) {
		t.Fatal("baseline resolution lost support/dissent")
	}
	resolved = addBaseline(t, resolved, 8, AcceptedEvidence, a)
	resolved = addBaseline(t, resolved, 9, PendingEvidence, b)
	if resolved.OriginalState() != OriginalTargetKnown {
		t.Fatal("pending baseline reopened conflict")
	}
	resolved = addBaseline(t, resolved, 10, AcceptedEvidence, b)
	if resolved.OriginalState() != OriginalTargetConflict || resolved.Classification() != CustomCard {
		t.Fatal("baseline not reopened independently")
	}
	assertAbsent(resolved)
	// An equal version ID with a different ContentID is a different explicit
	// tuple. Ownership validation remains a future service responsibility.
	x := addBaseline(t, emptyEvidence(t), 11, AcceptedEvidence, a)
	x = addBaseline(t, x, 12, AcceptedEvidence, baselineTarget(t, 2, 1))
	if x.OriginalState() != OriginalTargetConflict {
		t.Fatal("ContentID dropped from target equality")
	}
}

func TestAssignmentIndependentReviewDimensions(t *testing.T) {
	a, b := baselineTarget(t, 1, 1), baselineTarget(t, 2, 2)
	s := addBaseline(t, emptyEvidence(t), 1, AcceptedEvidence, a)
	if s.Classification() != Unknown {
		t.Fatal("original target classified a physical Tag")
	}
	s = addClassification(t, s, 2, AcceptedEvidence, Original)
	s = addClassification(t, s, 3, AcceptedEvidence, CustomCard)
	if target, ok := s.OriginalBaseline(); !ok || target != a {
		t.Fatal("physical conflict changed the original baseline")
	}
	s = addBaseline(t, s, 4, AcceptedEvidence, b)
	s, err := s.ResolveOriginalTarget(reviewCore(t), reviewRequest(t, 5, 4), a, []EvidenceID{evidenceID(t, 1)})
	if err != nil || s.Classification() != Conflict || s.OriginalState() != OriginalTargetKnown {
		t.Fatal("baseline resolution incidentally resolved physical conflict")
	}
	s = addBaseline(t, s, 5, AcceptedEvidence, b)
	s, err = s.ResolveClassification(reviewCore(t), reviewRequest(t, 7, 6), CustomCard, []EvidenceID{evidenceID(t, 3)})
	if err != nil || s.Classification() != CustomCard || s.OriginalState() != OriginalTargetConflict {
		t.Fatal("physical resolution incidentally resolved baseline conflict")
	}
	if got, ok := s.OriginalBaseline(); ok || got != (OriginalTarget{}) {
		t.Fatal("physical resolution supplied a fallback target")
	}
}

func TestAssignmentAuthority(t *testing.T) {
	s := emptyEvidence(t)
	q := reviewRequest(t, 1, 0)
	e := claimEvidence(t, s.Subject(), 1, AcceptedEvidence)
	var spoofed ReviewAuthority
	if err := json.Unmarshal([]byte(`{"actor":"core-reviewer","permissions":255}`), &spoofed); err != nil {
		t.Fatal(err)
	}
	for _, authority := range []ReviewAuthority{{}, spoofed} {
		got, err := s.AdmitClassification(authority, q, e, Original)
		if err != ErrReviewAuthority || !reflect.DeepEqual(got, s) {
			t.Fatal("request minted review authority")
		}
		if _, err := s.AdmitOriginalTarget(authority, q, e, baselineTarget(t, 1, 1)); err != ErrReviewAuthority {
			t.Fatal("baseline lacks authority fence")
		}
		if _, err := s.ResolveClassification(authority, q, Original, nil); err != ErrReviewAuthority {
			t.Fatal("resolution lacks authority fence")
		}
		if _, err := s.ResolveOriginalTarget(authority, q, baselineTarget(t, 1, 1), nil); err != ErrReviewAuthority {
			t.Fatal("baseline resolution lacks authority fence")
		}
	}
	classOnly, err := newReviewAuthority("class-reviewer", reviewClassification)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitOriginalTarget(classOnly, q, e, baselineTarget(t, 1, 1)); err != ErrReviewAuthority {
		t.Fatal("capability crossed permitted command boundary")
	}
	originalOnly, err := newReviewAuthority("baseline-reviewer", reviewOriginalTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitClassification(originalOnly, q, e, Original); err != ErrReviewAuthority {
		t.Fatal("baseline capability classified a Tag")
	}
	s = addClassification(t, s, 1, AcceptedEvidence, Original)
	s = addClassification(t, s, 2, AcceptedEvidence, CustomCard)
	if _, err := s.ResolveClassification(classOnly, reviewRequest(t, 3, 2), Original, []EvidenceID{evidenceID(t, 1)}); err != ErrReviewAuthority {
		t.Fatal("claim capability minted resolution permission")
	}
	s = addBaseline(t, s, 3, AcceptedEvidence, baselineTarget(t, 1, 1))
	s = addBaseline(t, s, 4, AcceptedEvidence, baselineTarget(t, 1, 2))
	if _, err := s.ResolveOriginalTarget(originalOnly, reviewRequest(t, 5, 4), baselineTarget(t, 1, 1), []EvidenceID{evidenceID(t, 3)}); err != ErrReviewAuthority {
		t.Fatal("baseline claim capability resolved conflict")
	}
	p := s.Records()[0].Provenance
	if p.Actor != "core-reviewer" || p.Reason != "explicit review" || p.PolicyRevision != 7 || p.Subject != s.Subject() || p.ExpectedRevision != 0 || p.Revision != 1 || p.Command != q.command {
		t.Fatal("typed review provenance lost")
	}
	if s.Records()[0].Evidence.Kind() != OperatorAssertion {
		t.Fatal("operator assertion promoted to manufacturer evidence")
	}
}

func TestAssignmentReviewAdmissionValidation(t *testing.T) {
	s := emptyEvidence(t)
	q := reviewRequest(t, 1, 0)
	e := claimEvidence(t, s.Subject(), 1, AcceptedEvidence)
	for _, method := range []func(TagEvidence, ReviewRequest, Evidence) (TagEvidence, error){
		func(s TagEvidence, q ReviewRequest, e Evidence) (TagEvidence, error) {
			return s.AdmitClassification(reviewCore(t), q, e, Original)
		},
		func(s TagEvidence, q ReviewRequest, e Evidence) (TagEvidence, error) {
			return s.AdmitOriginalTarget(reviewCore(t), q, e, baselineTarget(t, 1, 1))
		},
	} {
		for _, tc := range []struct {
			state    TagEvidence
			request  ReviewRequest
			evidence Evidence
			want     error
		}{
			{s, ReviewRequest{}, e, ErrInvalidEvidence},
			{s, reviewRequest(t, 1, 1), e, ErrRevisionConflict},
			{s, q, Evidence{}, ErrInvalidEvidence},
			{s, q, claimEvidence(t, reviewTag(t, 2), 2, AcceptedEvidence), ErrEvidenceSubject},
			{TagEvidence{}, q, e, ErrInvalidReference},
			{TagEvidence{tag: s.tag, revision: ^uint64(0)}, reviewRequest(t, 1, ^uint64(0)), e, ErrRevisionConflict},
		} {
			got, err := method(tc.state, tc.request, tc.evidence)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, tc.state) {
				t.Fatalf("admission got %v, want %v", err, tc.want)
			}
		}
	}
	s = addClassification(t, s, 1, AcceptedEvidence, Original)
	if _, err := s.AdmitOriginalTarget(reviewCore(t), reviewRequest(t, 2, 1), e, baselineTarget(t, 1, 1)); err != ErrDuplicateEvidence {
		t.Fatal("duplicate observation crossed dimensions")
	}
	if _, err := s.AdmitClassification(reviewCore(t), reviewRequest(t, 1, 1), claimEvidence(t, s.Subject(), 2, AcceptedEvidence), Original); err != ErrDuplicateReview {
		t.Fatal("duplicate command admitted")
	}
	for _, class := range []PhysicalClassification{Unknown, Conflict, 255} {
		if _, err := emptyEvidence(t).AdmitClassification(reviewCore(t), q, e, class); err != ErrInvalidEvidence {
			t.Fatal("invalid physical claim")
		}
	}
	if _, err := emptyEvidence(t).AdmitOriginalTarget(reviewCore(t), q, e, OriginalTarget{}); err != ErrInvalidReference {
		t.Fatal("zero baseline admitted")
	}
	if _, err := NewTagEvidence(identity.TagID{}); err != ErrInvalidReference {
		t.Fatal("zero Tag admitted")
	}
}

func TestAssignmentEvidenceTextAndIdentityBounds(t *testing.T) {
	s := emptyEvidence(t)
	e := claimEvidence(t, s.Subject(), 1, AcceptedEvidence)
	q := reviewRequest(t, 1, 0)
	for _, reason := range []string{"", "   ", "review\nspoof", "review\x7f", "review\u0085", string([]byte{0xff}), strings.Repeat("a", MaxReviewReasonBytes+1), strings.Repeat("é", MaxReviewReasonBytes/2) + "a"} {
		if _, err := NewReviewRequest(q.command, 0, 7, reason); err != ErrInvalidEvidence {
			t.Fatal("invalid reason admitted")
		}
	}
	for _, reason := range []string{strings.Repeat("a", MaxReviewReasonBytes), strings.Repeat("é", MaxReviewReasonBytes/2)} {
		if _, err := NewReviewRequest(q.command, 0, 7, reason); err != nil {
			t.Fatal("exact UTF-8 reason rejected")
		}
	}
	if _, err := NewReviewRequest(q.command, 0, 0, "review"); err != ErrInvalidEvidence {
		t.Fatal("missing policy revision")
	}
	for _, source := range []string{"", " ", "a\r", string([]byte{0xff}), strings.Repeat("a", MaxReviewReferenceBytes+1)} {
		if _, err := NewEvidence(e.id, s.Subject(), OperatorAssertion, source, AcceptedEvidence); err != ErrInvalidEvidence {
			t.Fatal("invalid source admitted")
		}
		if _, err := newReviewAuthority(source, reviewClassification); err != ErrReviewAuthority {
			t.Fatal("invalid principal admitted")
		}
	}
	for _, reference := range []string{strings.Repeat("a", MaxReviewReferenceBytes), strings.Repeat("é", MaxReviewReferenceBytes/2)} {
		if _, err := NewEvidence(e.id, s.Subject(), ImportedAssertion, reference, AcceptedEvidence); err != nil {
			t.Fatal("exact UTF-8 source rejected")
		}
		if _, err := newReviewAuthority(reference, reviewOriginalTarget); err != nil {
			t.Fatal("exact UTF-8 principal rejected")
		}
	}
	for _, kind := range []EvidenceKind{0, 255} {
		if _, err := NewEvidence(e.id, s.Subject(), kind, "source", AcceptedEvidence); err != ErrInvalidEvidence {
			t.Fatal("invalid evidence kind admitted")
		}
	}
	for _, disposition := range []EvidenceDisposition{0, 255} {
		if _, err := NewEvidence(e.id, s.Subject(), OperatorAssertion, "source", disposition); err != ErrInvalidEvidence {
			t.Fatal("invalid disposition admitted")
		}
	}
	for _, permissions := range []reviewPermission{0, 128} {
		if _, err := newReviewAuthority("reviewer", permissions); err != ErrReviewAuthority {
			t.Fatal("invalid core capability")
		}
	}
	if _, err := NewContentDimensions(true, true, 255); err != ErrInvalidEvidence {
		t.Fatal("invalid protection")
	}
	for _, text := range []string{"", "evd_" + payload + "a", "rev_" + payload, "evd_" + strings.Repeat("I", 26)} {
		if _, err := ParseEvidenceID(text); err == nil {
			t.Fatal("invalid evidence ID")
		}
	}
	for _, text := range []string{"", "rev_" + payload + "a", "evd_" + payload, "rev_" + strings.Repeat("I", 26)} {
		if _, err := ParseReviewID(text); err == nil {
			t.Fatal("invalid review ID")
		}
	}
	if e.ID().String() == "" || q.command.String() == "" || e.Source() != "operator-assertion" || e.Subject() != s.Subject() || e.Disposition() != AcceptedEvidence {
		t.Fatal("evidence accessors lost provenance")
	}
	if _, err := NewOriginalTarget(identity.ContentID{}, baselineTarget(t, 1, 1).version); err != ErrInvalidReference {
		t.Fatal("partial original tuple")
	}
	if _, err := NewOriginalTarget(baselineTarget(t, 1, 1).content, identity.ContentVersionID{}); err != ErrInvalidReference {
		t.Fatal("partial original tuple")
	}
}

func TestAssignmentEvidenceLimitsBeforeMutation(t *testing.T) {
	s := addClassification(t, emptyEvidence(t), 1, AcceptedEvidence, Original)
	q := reviewRequest(t, 2, 1)
	e := claimEvidence(t, s.Subject(), 2, AcceptedEvidence)
	// Simulate already-accounted usage at each ceiling. These are admission
	// unit tests, not fabricated full aggregate/encoding reachability fixtures.
	for _, tc := range []struct {
		usage EvidenceUsage
		want  error
	}{
		{EvidenceUsage{events: MaxEvidenceEvents}, ErrEvidenceEventLimit},
		{EvidenceUsage{observations: MaxEvidenceObservations}, ErrObservationLimit},
		{EvidenceUsage{supportLinks: MaxEvidenceSupportLinks}, ErrSupportLinkLimit},
		{EvidenceUsage{retainedBytes: MaxEvidenceRetainedBytes}, ErrRetainedByteLimit},
	} {
		limited := s
		limited.usage = tc.usage
		before := limited.Records()
		got, err := limited.AdmitClassification(reviewCore(t), q, e, CustomCard)
		if err != tc.want || !reflect.DeepEqual(got, limited) || !reflect.DeepEqual(limited.Records(), before) {
			t.Fatal("failed prospective admission changed state")
		}
		got, err = limited.AdmitOriginalTarget(reviewCore(t), q, e, baselineTarget(t, 1, 1))
		if err != tc.want || !reflect.DeepEqual(got, limited) {
			t.Fatal("baseline bypassed resource budget")
		}
	}
	// Bounded selections use real retained support and include an exact 64 set.
	s = emptyEvidence(t)
	ids := make([]EvidenceID, MaxSelectedSupport)
	for i := range ids {
		ids[i] = evidenceID(t, i+1)
		s = addClassification(t, s, i+1, AcceptedEvidence, Original)
	}
	s = addClassification(t, s, 65, AcceptedEvidence, CustomCard)
	next, err := s.ResolveClassification(reviewCore(t), reviewRequest(t, 66, 65), Original, ids)
	if err != nil || next.Usage().SupportLinks()-s.Usage().SupportLinks() != MaxSelectedSupport || next.Usage().Observations() != s.Usage().Observations() {
		t.Fatal("exact selected support failed")
	}
	oversized := append(append([]EvidenceID(nil), ids...), evidenceID(t, 65))
	if got, err := s.ResolveClassification(reviewCore(t), reviewRequest(t, 66, 65), Original, oversized); err != ErrCommandEvidenceLimit || !reflect.DeepEqual(got, s) {
		t.Fatal("oversized support was copied/admitted")
	}
	limited := s
	limited.usage.retainedBytes = MaxEvidenceRetainedBytes
	if got, err := limited.ResolveClassification(reviewCore(t), reviewRequest(t, 66, 65), Original, ids); err != ErrRetainedByteLimit || !reflect.DeepEqual(got, limited) {
		t.Fatal("resolution bypassed retained-byte budget")
	}
}

func TestAssignmentEvidenceSourceCannotClassify(t *testing.T) {
	// All source text is a bounded descriptive reference, including strings
	// resembling forbidden heuristic inputs. Only the explicit reviewed claim
	// determines state, and imported text never authenticates a manufacturer.
	for _, source := range []string{"filename=original.taf", "path=/custom", "title=original", "model=123", "article=456", "position=0", "offset=500000000", "protocol_valid=true", "owned=true", "cloud_auth=true", "nocloud=false", "claimed=true", "exists=true", "manufacturer=authenticated"} {
		for _, class := range []PhysicalClassification{Original, CustomCard} {
			s := emptyEvidence(t)
			e, err := NewEvidence(evidenceID(t, 1), s.Subject(), ImportedAssertion, source, AcceptedEvidence)
			if err != nil {
				t.Fatal(err)
			}
			s, err = s.AdmitClassification(reviewCore(t), reviewRequest(t, 1, 0), e, class)
			if err != nil || s.Classification() != class || s.Records()[0].Evidence.Kind() != ImportedAssertion {
				t.Fatal("source text influenced classification or authenticity")
			}
		}
	}
}
