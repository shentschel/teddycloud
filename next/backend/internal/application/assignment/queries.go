package assignment

import (
	"context"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type EffectiveQuery struct {
	TagID             identity.TagID
	At                time.Time
	KnowledgeRevision *uint64
}
type AssignmentView struct {
	AggregatePresent bool
	Desired          d.DesiredAssignment
	Classification   d.PhysicalClassification
	OriginalState    d.OriginalTargetState
	OriginalTarget   d.OriginalTarget
	TagRevision      uint64
	// Live annotations are explicitly not historical catalog claims.
	CatalogRevision                                            uint64
	Dimensions                                                 d.ContentDimensions
	CatalogOnly, BoundUnchecked, Superseded, EditorialConflict bool
}

func (s *Service) GetEffectiveAssignment(ctx context.Context, q EffectiveQuery) (AssignmentView, error) {
	var view AssignmentView
	err := s.run(ctx, func(ctx context.Context, p Port, actor AuthenticatedActor, _ d.ReviewAuthority) error {
		if !actor.Read {
			return ErrUnauthorized
		}
		if _, err := identity.ParseTagID(q.TagID.String()); err != nil {
			return ErrInvalidInput
		}
		at := q.At.Round(0).UTC()
		if at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
			return ErrInvalidInput
		}
		tag, err := checkedTag(ctx, p, q.TagID)
		if err != nil {
			return err
		}
		view.TagRevision = tag.Revision
		a, found, err := load(ctx, p, q.TagID)
		if err != nil {
			return err
		}
		retainedTargets, err := checkRetainedTargets(ctx, p, a.snapshot)
		if err != nil {
			return err
		}
		head := uint64(len(a.snapshot.Events))
		knowledge := head
		if q.KnowledgeRevision != nil {
			knowledge = *q.KnowledgeRevision
		}
		if knowledge > head {
			return ErrKnowledgeRevision
		}
		view.AggregatePresent = found
		view.Desired = d.DesiredAssignment{TagID: q.TagID, HeadRevision: head, KnowledgeRevision: knowledge}
		if !found {
			return nil
		}
		view.Desired.AssignmentID = a.snapshot.ID
		reviews, err := d.NewTagEvidence(q.TagID)
		if err != nil {
			return err
		}
		// Replay only the selected knowledge for the two review dimensions. The
		// full aggregate was already validated; live metadata cannot reclassify it.
		for _, event := range a.snapshot.Events {
			if event.Result.Revision > knowledge {
				break
			}
			in := event.Command
			if in.Kind.review() {
				auth, err := d.BindAuthenticatedReviewAuthority(event.Actor)
				if err != nil {
					return ErrUnavailable
				}
				request, err := d.NewReviewRequest(in.ReviewID, reviews.Revision(), in.PolicyRevision, in.Reason)
				if err != nil {
					return ErrUnavailable
				}
				switch in.Kind {
				case RecordClassification:
					reviews, err = reviews.AdmitClassification(auth, request, in.Evidence, in.Classification)
				case ResolveClassification:
					reviews, err = reviews.ResolveClassification(auth, request, in.Classification, in.Support)
				case RecordOriginalTarget:
					reviews, err = reviews.AdmitOriginalTarget(auth, request, in.Evidence, in.Target)
				case ResolveOriginalTarget:
					reviews, err = reviews.ResolveOriginalTarget(auth, request, in.Target, in.Support)
				}
				if err != nil {
					return ErrUnavailable
				}
				continue
			}
			if in.EffectiveAt.After(at) {
				if view.Desired.TransitionRevision != 0 && view.Desired.Until.IsZero() {
					view.Desired.Until = in.EffectiveAt
				}
				continue
			}
			view.Desired.TransitionRevision = event.Result.Revision
			view.Desired.From = in.EffectiveAt
			view.Desired.Target = d.OriginalTarget{}
			switch in.Kind {
			case Assign:
				view.Desired.State = d.Assigned
				view.Desired.Target = in.Target
			case Unassign:
				view.Desired.State = d.Unassigned
			case RetireAssignment:
				view.Desired.State = d.Retired
			}
		}
		view.Classification = reviews.Classification()
		view.OriginalState = reviews.OriginalState()
		view.OriginalTarget, _ = reviews.OriginalBaseline()
		if view.Desired.State == d.Assigned {
			target, ok := retainedTargets[view.Desired.Target]
			if !ok {
				return ErrUnavailable
			}
			view.CatalogRevision = target.CatalogRevision
			view.Dimensions = target.Dimensions
			view.CatalogOnly = !target.Bound
			view.BoundUnchecked = target.Bound
			view.Superseded = target.Superseded
			view.EditorialConflict = target.EditorialConflict
		}
		return nil
	})
	if err != nil {
		return AssignmentView{}, err
	}
	return view, nil
}

// LookupAssignmentCommand is authorized readback using the complete original
// input, never a raw-ID result oracle. Exact replay needs no current dependency
// admission and remains available after retirement/catalog changes.
func (s *Service) LookupAssignmentCommand(ctx context.Context, input Command) (Result, bool, error) {
	var result Result
	var found bool
	err := s.run(ctx, func(ctx context.Context, p Port, actor AuthenticatedActor, _ d.ReviewAuthority) error {
		if !actor.Read || !allowed(actor, input.Kind) {
			return ErrUnauthorized
		}
		command, err := normalizedCommand(input, actor.Reference)
		if err != nil {
			return err
		}
		prior, ok, err := p.LookupCommand(ctx, command.CommandID)
		if err != nil || !ok {
			return err
		}
		result, err = readReplay(ctx, p, command, actor.Reference, prior)
		found = err == nil
		return err
	})
	if err != nil {
		return Result{}, false, err
	}
	return result, found, nil
}
