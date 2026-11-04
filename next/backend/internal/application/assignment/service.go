package assignment

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	c "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const OperationTimeout = 10 * time.Second
const TransactionTimeout = 5 * time.Second

type Service struct {
	transactions Transactor
	active       atomic.Bool
	now          func() time.Time
}

func New(transactions Transactor) *Service {
	return &Service{transactions: transactions, now: time.Now}
}

// run owns the only callback. Validation/authentication and all dependency reads
// occur under its deadline. No repository or authority is cached by the service.
func (s *Service) run(ctx context.Context, operation func(context.Context, Port, AuthenticatedActor, d.ReviewAuthority) error) error {
	if s == nil || s.transactions == nil {
		return ErrUnavailable
	}
	if !s.active.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer s.active.Store(false)
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	txctx, txcancel := context.WithTimeout(ctx, TransactionTimeout)
	defer txcancel()
	calls := 0
	var callbackErr error
	err := s.transactions.WithinAssignmentTransaction(txctx, func(p Port) error {
		calls++
		if calls != 1 || p == nil {
			callbackErr = ErrUnavailable
			return callbackErr
		}
		callbackErr = func() error {
			if err := txctx.Err(); err != nil {
				return err
			}
			actor, err := p.Authenticate(txctx)
			if err != nil {
				return err
			}
			authority, err := d.BindAuthenticatedReviewAuthority(actor.Reference)
			if err != nil {
				return ErrUnauthorized
			}
			if err := operation(txctx, p, actor, authority); err != nil {
				return err
			}
			return txctx.Err()
		}()
		return callbackErr
	})
	// Preserve commit uncertainty even when a caller's deadline has expired.
	if err != nil {
		return sanitize(err)
	}
	if calls != 1 {
		return ErrUnavailable
	}
	if callbackErr != nil {
		return sanitize(callbackErr)
	}
	return sanitize(txctx.Err())
}

func allowed(actor AuthenticatedActor, k Kind) bool {
	if k.review() {
		return actor.Review
	}
	return k >= Assign && k <= RetireAssignment && actor.Decide
}
func (s *Service) Execute(ctx context.Context, input Command) (Result, error) {
	var result Result
	err := s.run(ctx, func(ctx context.Context, p Port, actor AuthenticatedActor, authority d.ReviewAuthority) error {
		if !allowed(actor, input.Kind) {
			return ErrUnauthorized
		}
		command, err := normalizedCommand(input, actor.Reference)
		if err != nil {
			return err
		}
		prior, found, err := p.LookupCommand(ctx, command.CommandID)
		if err != nil {
			return err
		}
		if found {
			result, err = readReplay(ctx, p, command, actor.Reference, prior)
			return err
		}
		tag, err := checkedTag(ctx, p, command.TagID)
		if err != nil {
			return err
		}
		if tag.Revision != command.ExpectedTagRevision {
			return ErrDependencyConflict
		}
		aggregate, found, err := load(ctx, p, command.TagID)
		if err != nil {
			return err
		}
		if !found {
			aggregate, err = empty(command.AssignmentID, command.TagID)
			if err != nil {
				return err
			}
		}
		if aggregate.snapshot.ID != command.AssignmentID {
			return ErrCommandConflict
		}
		if uint64(len(aggregate.snapshot.Events)) != command.ExpectedRevision {
			return ErrRevisionConflict
		}
		if _, err := checkRetainedTargets(ctx, p, aggregate.snapshot); err != nil {
			return err
		}
		if command.Kind.target() {
			target, err := checkedTarget(ctx, p, command.Target)
			if err != nil {
				return err
			}
			if target.CatalogRevision != command.ExpectedCatalogRevision {
				return ErrDependencyConflict
			}
		}
		next, event, err := aggregate.append(command, actor.Reference, authority, s.now())
		if err != nil {
			return err
		}
		if err := p.Commit(ctx, command.ExpectedRevision, cloneSnapshot(next.snapshot)); err != nil {
			return err
		}
		result = event.Result
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func checkedTag(ctx context.Context, p Port, id identity.TagID) (TagSnapshot, error) {
	tag, found, err := p.Tag(ctx, id)
	if err != nil {
		return TagSnapshot{}, err
	}
	if !found {
		return TagSnapshot{}, ErrTagNotFound
	}
	if tag.ID != id || tag.Revision == 0 || tag.Revision > d.MaxDecisionRevision {
		return TagSnapshot{}, ErrUnavailable
	}
	return tag, nil
}
func checkedTarget(ctx context.Context, p Port, target d.OriginalTarget) (TargetSnapshot, error) {
	value, found, err := p.Target(ctx, target)
	if err != nil {
		return TargetSnapshot{}, err
	}
	if !found {
		return TargetSnapshot{}, ErrTargetNotFound
	}
	if value.Content.ID() != target.ContentID() || value.Version.ID() != target.VersionID() ||
		value.Version.ContentID() != target.ContentID() || value.Version.Fingerprint().IsZero() ||
		value.CatalogRevision == 0 || value.CatalogRevision > d.MaxDecisionRevision {
		return TargetSnapshot{}, ErrUnavailable
	}
	owner, ownerOK := value.Ownership.Value()
	fingerprint, fingerprintOK := value.Fingerprint.Value()
	if !value.Managed || !ownerOK || !fingerprintOK || owner.Key() != c.OwnershipClaimKey ||
		fingerprint.Key() != c.FingerprintClaimKey || owner.Owner() != target.ContentID() ||
		fingerprint.Fingerprint() != value.Version.Fingerprint() || len(value.Ownership.Alternatives[0].Support) == 0 ||
		len(value.Fingerprint.Alternatives[0].Support) == 0 {
		return TargetSnapshot{}, ErrUnsupportedState
	}
	return value, nil
}

type aggregate struct {
	snapshot  Snapshot
	decisions d.DecisionLedger
	reviews   d.TagEvidence
	sealed    bool
}

func empty(id d.ID, tag identity.TagID) (aggregate, error) {
	decisions, err := d.NewDecisionLedger(id, tag)
	if err != nil {
		return aggregate{}, err
	}
	reviews, err := d.NewTagEvidence(tag)
	if err != nil {
		return aggregate{}, err
	}
	return aggregate{snapshot: Snapshot{ID: id, TagID: tag}, decisions: decisions, reviews: reviews}, nil
}
func load(ctx context.Context, p Port, tag identity.TagID) (aggregate, bool, error) {
	snapshot, found, err := p.Load(ctx, tag)
	if err != nil || !found {
		return aggregate{}, false, err
	}
	if snapshot.TagID != tag || len(snapshot.Events) == 0 || uint64(len(snapshot.Events)) > d.MaxEvidenceEvents {
		return aggregate{}, false, ErrUnavailable
	}
	a, err := empty(snapshot.ID, tag)
	if err != nil {
		return aggregate{}, false, ErrUnavailable
	}
	seen := make(map[d.CommandID]bool, len(snapshot.Events))
	for _, retained := range snapshot.Events {
		if uint64(len(retained.Primitive)) > d.MaxEvidenceRetainedBytes {
			return aggregate{}, false, ErrUnavailable
		}
		if err := ctx.Err(); err != nil {
			return aggregate{}, false, err
		}
		if seen[retained.Command.CommandID] {
			return aggregate{}, false, ErrUnavailable
		}
		seen[retained.Command.CommandID] = true
		auth, err := d.BindAuthenticatedReviewAuthority(retained.Actor)
		if err != nil {
			return aggregate{}, false, ErrUnavailable
		}
		next, event, err := a.append(retained.Command, retained.Actor, auth, retained.RecordedAt)
		if err != nil || event.Result != retained.Result || !bytes.Equal(event.Primitive, retained.Primitive) {
			return aggregate{}, false, ErrUnavailable
		}
		// Canonical storage must not hide inactive fields, a noncanonical timestamp,
		// or request-supplied authority in a retained command.
		if retained.Command.Actor != "" || !retained.RecordedAt.Equal(event.RecordedAt) {
			return aggregate{}, false, ErrUnavailable
		}
		a = next
	}
	if a.snapshot.Usage != snapshot.Usage {
		return aggregate{}, false, ErrUnavailable
	}
	return a, true, nil
}
func (a aggregate) append(command Command, actor string, auth d.ReviewAuthority, at time.Time) (aggregate, Event, error) {
	command, err := normalizedCommand(command, actor)
	if err != nil {
		return a, Event{}, err
	}
	revision := uint64(len(a.snapshot.Events))
	if command.AssignmentID != a.snapshot.ID || command.TagID != a.snapshot.TagID || command.ExpectedRevision != revision {
		return a, Event{}, ErrRevisionConflict
	}
	if revision >= d.MaxDecisionRevision {
		return a, Event{}, ErrLimit
	}
	if a.sealed {
		return a, Event{}, ErrRetired
	}
	at = at.Round(0).UTC()
	if at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
		return a, Event{}, ErrUnavailable
	}
	next := a
	// Construct only the bounded NEW primitive first. Aggregate-wide admission
	// must precede the domain ledgers' history copies and the application append.
	var primitive []byte
	delta := d.EvidenceDelta{Events: 1}
	var dc d.DecisionCommand
	var request d.ReviewRequest
	if command.Kind.review() {
		request, err = d.NewReviewRequest(command.ReviewID, a.reviews.Revision(), command.PolicyRevision, command.Reason)
		if err != nil {
			return a, Event{}, err
		}
		record := d.ReviewRecord{Provenance: d.ReviewProvenance{Command: command.ReviewID, Subject: command.TagID, Actor: actor, Reason: command.Reason,
			PolicyRevision: command.PolicyRevision, ExpectedRevision: a.reviews.Revision(), Revision: a.reviews.Revision() + 1},
			Classification: command.Classification, Target: command.Target, Support: command.Support}
		switch command.Kind {
		case RecordClassification:
			record.Kind = d.ClassificationClaim
		case ResolveClassification:
			record.Kind = d.ClassificationResolution
		case RecordOriginalTarget:
			record.Kind = d.OriginalTargetClaim
		case ResolveOriginalTarget:
			record.Kind = d.OriginalTargetResolution
		}
		if command.Kind == RecordClassification || command.Kind == RecordOriginalTarget {
			record.Evidence = command.Evidence
			record.Support = []d.EvidenceID{command.Evidence.ID()}
			delta.Observations = 1
		}
		delta.SupportLinks = uint64(len(record.Support))
		// Preserve A1a's conservative primitive envelope, including escaped text.
		delta.RetainedBytes = 1024 + 6*uint64(len(actor)+len(command.Reason)+len(command.Evidence.Source())) + 256*delta.SupportLinks
		primitive, err = record.CanonicalBytes()
		if err != nil {
			return a, Event{}, err
		}
		if uint64(len(primitive)) > delta.RetainedBytes {
			delta.RetainedBytes = uint64(len(primitive))
		}
	} else {
		dc, err = d.NewDecisionCommand(command.decisionInput(actor, a.decisions.Revision()))
		if err != nil {
			return a, Event{}, err
		}
		result := d.DecisionResult{Command: dc, Revision: a.decisions.Revision() + 1}
		for i := len(a.snapshot.Events) - 1; i >= 0; i-- {
			if !a.snapshot.Events[i].Command.Kind.review() {
				result.PreviousCommand = a.snapshot.Events[i].Command.CommandID
				break
			}
		}
		primitive, err = (d.DecisionEvent{Result: result, RecordedAt: at}).CanonicalBytes()
		if err != nil {
			return a, Event{}, err
		}
		delta.RetainedBytes = uint64(len(primitive))
	}
	result := Result{CommandID: command.CommandID, AssignmentID: command.AssignmentID, TagID: command.TagID, Revision: revision + 1}
	if revision > 0 {
		result.PreviousCommand = a.snapshot.Events[revision-1].Command.CommandID
	}
	event := Event{Command: command, Actor: actor, Result: result, RecordedAt: at, Primitive: primitive}
	extra, err := retainedEnvelopeBytes(event)
	if err != nil {
		return a, Event{}, err
	}
	delta.RetainedBytes += extra
	usage, err := a.snapshot.Usage.Prospective(delta)
	if err != nil {
		return a, Event{}, err
	}
	// Semantic domain admission follows prospective COMPLETE retention checks.
	if command.Kind.review() {
		switch command.Kind {
		case RecordClassification:
			next.reviews, err = a.reviews.AdmitClassification(auth, request, command.Evidence, command.Classification)
		case ResolveClassification:
			next.reviews, err = a.reviews.ResolveClassification(auth, request, command.Classification, command.Support)
		case RecordOriginalTarget:
			next.reviews, err = a.reviews.AdmitOriginalTarget(auth, request, command.Evidence, command.Target)
		case ResolveOriginalTarget:
			next.reviews, err = a.reviews.ResolveOriginalTarget(auth, request, command.Target, command.Support)
		}
	} else {
		refs := d.DecisionReferences{TagID: command.TagID, TagRevision: command.ExpectedTagRevision, CatalogRevision: command.ExpectedCatalogRevision, Target: command.Target}
		next.decisions, _, err = a.decisions.Admit(dc, refs, at)
	}
	if err != nil {
		return a, Event{}, err
	}
	// Only new material is charged: never add either domain ledger's cumulative
	// usage again. Counts charge one global event, including every review kind.
	next.snapshot = a.snapshot
	next.snapshot.Usage = usage
	next.snapshot.Events = append(append([]Event(nil), a.snapshot.Events...), event)
	next.sealed = a.sealed || command.Kind == RetireAssignment
	return next, event, nil
}

// Complete conservative retained charge: canonical command, result with its
// input, event envelope, dependency/primitive snapshot, aggregate head and both
// Tag/Command indices. Domain primitive/envelope is charged separately above;
// an event stores it as base64 here, so escaping/framing are covered as well.
// Fixed reserve covers bounded keys/scalars/index framing; no caller discount.
func retainedEnvelopeBytes(event Event) (uint64, error) {
	input, err := canonicalCommand(event.Command, event.Actor)
	if err != nil {
		return 0, err
	}
	result, err := json.Marshal(struct {
		Command, Assignment, Tag string
		Revision                 uint64
		Previous                 string
		Input                    json.RawMessage
	}{
		event.Result.CommandID.String(), event.Result.AssignmentID.String(), event.Result.TagID.String(), event.Result.Revision, event.Result.PreviousCommand.String(), input})
	if err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(struct {
		Input     json.RawMessage
		Result    json.RawMessage
		At        string
		Primitive []byte
	}{input, result, event.RecordedAt.Format(time.RFC3339Nano), event.Primitive})
	if err != nil {
		return 0, err
	}
	return uint64(len(input)+len(result)+len(encoded)) + 1024, nil
}
func readReplay(ctx context.Context, p Port, command Command, actor string, prior Event) (Result, error) {
	if prior.Command.CommandID != command.CommandID || prior.Command.Actor != "" {
		return Result{}, ErrUnavailable
	}
	if _, err := d.BindAuthenticatedReviewAuthority(prior.Actor); err != nil {
		return Result{}, ErrUnavailable
	}
	want, err := canonicalCommand(command, actor)
	if err != nil {
		return Result{}, err
	}
	got, err := canonicalCommand(prior.Command, prior.Actor)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	if !bytes.Equal(want, got) {
		return Result{}, ErrCommandConflict
	}
	a, found, err := load(ctx, p, prior.Command.TagID)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, ErrUnavailable
	}
	for _, event := range a.snapshot.Events {
		if event.Command.CommandID == command.CommandID {
			b, err := canonicalCommand(event.Command, event.Actor)
			if err != nil || !bytes.Equal(want, b) || event.Result != prior.Result || !event.RecordedAt.Equal(prior.RecordedAt) || !bytes.Equal(event.Primitive, prior.Primitive) {
				return Result{}, ErrUnavailable
			}
			return event.Result, nil
		}
	}
	return Result{}, ErrUnavailable
}
func cloneSnapshot(s Snapshot) Snapshot {
	s.Events = append([]Event(nil), s.Events...)
	for i := range s.Events {
		s.Events[i].Command.Support = append([]d.EvidenceID(nil), s.Events[i].Command.Support...)
		s.Events[i].Primitive = append([]byte(nil), s.Events[i].Primitive...)
	}
	return s
}

// Validate the whole retained reference set before new admission or projection.
// Exact authorized replay deliberately bypasses current dependency admission.
// The map lives only inside this callback; it cannot become a reference cache.
func checkRetainedTargets(ctx context.Context, p Port, snapshot Snapshot) (map[d.OriginalTarget]TargetSnapshot, error) {
	targets := make(map[d.OriginalTarget]TargetSnapshot)
	for _, event := range snapshot.Events {
		if !event.Command.Kind.target() {
			continue
		}
		target := event.Command.Target
		if _, ok := targets[target]; ok {
			continue
		}
		value, err := checkedTarget(ctx, p, target)
		if err == ErrTargetNotFound || err == ErrUnsupportedState {
			return nil, ErrUnavailable
		}
		if err != nil {
			return nil, err
		}
		targets[target] = value
	}
	return targets, nil
}
