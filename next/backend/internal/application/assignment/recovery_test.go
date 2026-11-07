package assignment

import (
	"context"
	"errors"
	"testing"
)

type assignmentOutcomeTransactor func(context.Context, func(Port) error) error

func (f assignmentOutcomeTransactor) WithinAssignmentTransaction(ctx context.Context, run func(Port) error) error {
	return f(ctx, run)
}

func TestAssignmentRecoveryOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		committed bool
		boundary  error
		want      error
	}{
		{"confirmed precommit cancellation", false, context.Canceled, context.Canceled},
		{"confirmed precommit storage failure", false, ErrUnavailable, ErrUnavailable},
		{"uncertainty survives cancellation", true, ErrIndeterminate, ErrIndeterminate},
		{"late cancellation preserves confirmed commit", true, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reliable, storage := fixture(t)
			in := command(t, 1, Assign, 0)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			faulty := New(assignmentOutcomeTransactor(func(ctx context.Context, run func(Port) error) error {
				if err := storage.WithinAssignmentTransaction(ctx, func(port Port) error {
					if err := run(port); err != nil {
						return err
					}
					if !tc.committed {
						return tc.boundary
					}
					return nil
				}); err != nil && !errors.Is(err, tc.boundary) {
					return err
				}
				cancel()
				return tc.boundary
			}))
			want := Result{}
			if tc.committed {
				want = Result{CommandID: in.CommandID, AssignmentID: in.AssignmentID, TagID: in.TagID, Revision: 1}
			}
			wantExecute := Result{}
			if tc.want == nil {
				wantExecute = want
			}
			if got, err := faulty.Execute(ctx, in); err != tc.want || got != wantExecute {
				t.Fatalf("execute: %+v, %v", got, err)
			}
			got, found, err := reliable.LookupAssignmentCommand(t.Context(), in)
			if got != want || found != tc.committed || err != nil || storage.calls != 2 {
				t.Fatalf("readback: %+v, %t, %v, calls %d", got, found, err, storage.calls)
			}
		})
	}
}

func TestAssignmentRecoveryRequiresCurrentAuthority(t *testing.T) {
	for _, kind := range []Kind{Assign, RecordClassification} {
		s, storage := fixture(t)
		in := command(t, 1, kind, 0)
		want := execute(t, s, in)
		original := storage.actor
		absent := command(t, 2, kind, 0)
		storage.actor.Read = false
		for _, probe := range []Command{in, absent} {
			got, found, err := s.LookupAssignmentCommand(t.Context(), probe)
			if got != (Result{}) || found || err != ErrUnauthorized {
				t.Fatalf("read denial leaked receipt presence: %+v, %t, %v", got, found, err)
			}
		}

		storage.actor = original
		if kind.review() {
			storage.actor.Review = false
		} else {
			storage.actor.Decide = false
		}
		got, found, err := s.LookupAssignmentCommand(t.Context(), in)
		if got != (Result{}) || found || err != ErrUnauthorized {
			t.Fatalf("lookup ignored current action authority: %+v, %t, %v", got, found, err)
		}
		if got, err := s.Execute(t.Context(), in); got != (Result{}) || err != ErrUnauthorized {
			t.Fatalf("replay ignored current action authority: %+v, %v", got, err)
		}
		storage.actor = original
		storage.actor.Reference = "another authorized operator"
		if got, found, err := s.LookupAssignmentCommand(t.Context(), in); got != (Result{}) || found || err != ErrCommandConflict {
			t.Fatalf("changed identity: %+v, %t, %v", got, found, err)
		}
		storage.actor = original
		if got, found, err := s.LookupAssignmentCommand(t.Context(), in); got != want || !found || err != nil {
			t.Fatalf("authorized original receipt: %+v, %t, %v", got, found, err)
		}
		if got, err := s.Execute(t.Context(), in); got != want || err != nil || storage.commits != 1 {
			t.Fatalf("authorized replay: %+v, %v, commits %d", got, err, storage.commits)
		}
	}
}
