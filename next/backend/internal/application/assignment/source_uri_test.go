package assignment

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
)

func TestAssignmentURIHasNoAuthority(t *testing.T) {
	const locator = "https://127.0.0.1.invalid/Original/Available"
	in := command(t, 2, Assign, 1)
	query := EffectiveQuery{TagID: in.TagID, At: time.Unix(100, 0)}
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprintf("bound=%t", bound), func(t *testing.T) {
			var views [2]AssignmentView
			var snapshots [2]Snapshot
			for i := range views {
				s, m := fixture(t)
				target := m.targets[in.Target]
				target.Bound = bound
				m.targets[in.Target] = target
				review := command(t, 1, RecordClassification, 0)
				review.Classification = d.CustomCard
				execute(t, s, review)
				request := in
				if i == 1 {
					request.URI = OptionalURI{Present: true, Value: locator}
				}
				execute(t, s, request)
				var err error
				views[i], err = s.GetEffectiveAssignment(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				snapshots[i] = m.snapshots[in.TagID]
			}
			if !reflect.DeepEqual(views[0], views[1]) || views[1].Desired.Target != in.Target ||
				views[1].Classification != d.CustomCard || views[1].CatalogOnly == bound || views[1].BoundUnchecked != bound {
				t.Fatal("locator changed assignment target, classification, or availability")
			}
			a, b := snapshots[0].Events[1], snapshots[1].Events[1]
			if a.Result != b.Result || !bytes.Equal(a.Primitive, b.Primitive) {
				t.Fatal("locator changed decision semantics")
			}
			slot := b.Command.URI.Encoding()
			if slot != (d.SourceURIEncoding{Present: true, Profile: "pi09-uri/1", Locator: locator}) {
				t.Fatal("missing retained provenance")
			}
			// The locator and profile occur in command, result and event input,
			// and again in the result nested inside the event.
			wantExtra := 4 * uint64(len(locator)+len("pi09-uri/1")-1)
			without, with := snapshots[0].Usage, snapshots[1].Usage
			if with.RetainedBytes() != without.RetainedBytes()+wantExtra ||
				with.Events() != without.Events() || with.Observations() != without.Observations() ||
				with.SupportLinks() != without.SupportLinks() {
				t.Fatal("incorrect retained provenance charge")
			}
		})
	}

	s, m := fixture(t)
	in = command(t, 1, Assign, 0)
	in.URI = OptionalURI{Present: true, Value: locator}
	m.actor.Decide = false
	_, err := s.Execute(t.Context(), in)
	wantError(t, err, ErrUnauthorized)
	m.actor.Decide = true
	in.ExpectedCatalogRevision++
	_, err = s.Execute(t.Context(), in)
	wantError(t, err, ErrDependencyConflict)
	delete(m.targets, in.Target)
	_, err = s.Execute(t.Context(), in)
	wantError(t, err, ErrTargetNotFound)
	in.Target = d.OriginalTarget{}
	_, err = s.Execute(t.Context(), in)
	wantError(t, err, ErrInvalidInput)
	if m.commits != 0 {
		t.Fatal("locator bypassed dependency or authority checks")
	}
}

func TestAssignmentURICanonicalReplay(t *testing.T) {
	s, m := fixture(t)
	legacy := command(t, 1, Assign, 0)
	legacyResult := execute(t, s, legacy)
	in := command(t, 2, Assign, 1)
	in.URI = OptionalURI{Present: true, Value: "HTTPS://Catalog.Example:443/a%62"}
	uriResult := execute(t, s, in)
	execute(t, s, command(t, 3, RetireAssignment, 2))
	delete(m.targets, in.Target)
	delete(m.tags, in.TagID)
	in.URI.Value = "https://catalog.example/%61b"
	for _, tc := range []struct {
		command Command
		result  Result
	}{{legacy, legacyResult}, {in, uriResult}} {
		replayed, err := s.Execute(t.Context(), tc.command)
		if err != nil || replayed != tc.result {
			t.Fatal("canonical replay after retirement", err)
		}
		readback, found, err := s.LookupAssignmentCommand(t.Context(), tc.command)
		if err != nil || !found || readback != tc.result {
			t.Fatal("canonical command readback after retirement", err)
		}
	}
	for _, replacement := range []OptionalURI{{}, {Present: true, Value: "https://catalog.example/Ab"}} {
		changed := in
		changed.URI = replacement
		_, err := s.Execute(t.Context(), changed)
		wantError(t, err, ErrCommandConflict)
		_, _, err = s.LookupAssignmentCommand(t.Context(), changed)
		wantError(t, err, ErrCommandConflict)
	}
	legacy.URI = in.URI
	_, err := s.Execute(t.Context(), legacy)
	wantError(t, err, ErrCommandConflict)
	snapshot := m.snapshots[in.TagID]
	if m.commits != 3 || len(snapshot.Events) != 3 || snapshot.Events[0].Command.URI != (OptionalURI{}) ||
		snapshot.Events[1].Command.URI.Value != "https://catalog.example/ab" {
		t.Fatal("replay changed retained history")
	}
}

func TestAssignmentURIErrorRedaction(t *testing.T) {
	s, m := fixture(t)
	in := command(t, 1, Assign, 0)
	for _, uri := range []OptionalURI{
		{Present: true},
		{Value: "https://catalog.example/absent-secret"},
		{Present: true, Value: "https://operator:password@catalog.example/a"},
		{Present: true, Value: "https://catalog.example/e0040000aabbccdd?token=credential"},
		{Present: true, Value: `C:\private\tag\e0040000aabbccdd`},
		{Present: true, Value: "https://catalog.example/secret%gg"},
		{Present: true, Value: "https://catalog.example/" + strings.Repeat("s", d.MaxSourceURIBytes)},
	} {
		in.URI = uri
		if result, err := s.Execute(t.Context(), in); err != ErrInvalidInput || result != (Result{}) {
			t.Fatal("Execute did not return only its fixed validation error")
		}
		if result, found, err := s.LookupAssignmentCommand(t.Context(), in); err != ErrInvalidInput || found || result != (Result{}) {
			t.Fatal("readback did not return only its fixed validation error")
		}
	}
	for kind := Unassign; kind <= RetireAssignment; kind++ {
		request := command(t, 1, kind, 0)
		request.URI = OptionalURI{Present: true, Value: "https://catalog.example/a"}
		_, err := s.Execute(t.Context(), request)
		wantError(t, err, ErrInvalidInput)
	}
	in.Actor = "untrusted assertion"
	_, err := s.Execute(t.Context(), in)
	wantError(t, err, ErrUnauthorized)
	in.Actor = ""
	m.actor.Decide = false
	_, _, err = s.LookupAssignmentCommand(t.Context(), in)
	wantError(t, err, ErrUnauthorized)
	m.fault = fmt.Errorf("private database path /private/assignment.sqlite: %w", ErrUnavailable)
	if _, err := s.Execute(t.Context(), in); err != ErrUnavailable {
		t.Fatal("database error exposed internal details")
	}
	if m.commits != 0 || len(m.commands) != 0 || len(m.snapshots) != 0 {
		t.Fatal("rejected request retained evidence")
	}
}
