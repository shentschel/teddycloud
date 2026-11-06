package assignment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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
	HeadRetired      bool
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

type assignmentRead struct {
	aggregate
	found     bool
	tag       TagSnapshot
	targets   map[d.OriginalTarget]TargetSnapshot
	at        time.Time
	knowledge uint64
}

func readAssignment(ctx context.Context, p Port, q EffectiveQuery) (assignmentRead, error) {
	var read assignmentRead
	if _, err := identity.ParseTagID(q.TagID.String()); err != nil {
		return read, ErrInvalidInput
	}
	read.at = q.At.Round(0).UTC()
	if read.at.IsZero() || read.at.Year() < 1 || read.at.Year() > 9999 {
		return read, ErrInvalidInput
	}
	var err error
	read.tag, err = checkedTag(ctx, p, q.TagID)
	if err != nil {
		return read, err
	}
	read.aggregate, read.found, err = load(ctx, p, q.TagID)
	if err != nil {
		return read, err
	}
	read.targets, err = checkRetainedTargets(ctx, p, read.snapshot)
	if err != nil {
		return read, err
	}
	head := uint64(len(read.snapshot.Events))
	read.knowledge = head
	if q.KnowledgeRevision != nil {
		read.knowledge = *q.KnowledgeRevision
	}
	if read.knowledge > head {
		return read, ErrKnowledgeRevision
	}
	return read, nil
}

// GetAssignment includes the retained aggregate identity and head tombstone
// alongside the view at the requested effective time and knowledge revision.
func (s *Service) GetAssignment(ctx context.Context, q EffectiveQuery) (AssignmentView, error) {
	return s.GetEffectiveAssignment(ctx, q)
}

func (s *Service) GetEffectiveAssignment(ctx context.Context, q EffectiveQuery) (AssignmentView, error) {
	var view AssignmentView
	err := s.run(ctx, func(ctx context.Context, p Port, actor AuthenticatedActor, _ d.ReviewAuthority) error {
		if !actor.Read {
			return ErrUnauthorized
		}
		a, err := readAssignment(ctx, p, q)
		if err != nil {
			return err
		}
		at, knowledge := a.at, a.knowledge
		view.TagRevision = a.tag.Revision
		view.AggregatePresent = a.found
		view.HeadRetired = a.sealed
		view.Desired = d.DesiredAssignment{TagID: q.TagID, HeadRevision: uint64(len(a.snapshot.Events)), KnowledgeRevision: knowledge}
		if !a.found {
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
			if err := ctx.Err(); err != nil {
				return err
			}
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
			target, ok := a.targets[view.Desired.Target]
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

const (
	MaxHistoryEntries = 128
	MaxHistoryBytes   = 1 << 20
	MaxHistoryCursor  = 256
	historyTokenBytes = 1 + 8 + sha256.Size
)

var ErrStaleCursor = errors.New("stale assignment history cursor")

// HistoryCursorScope is the trusted transactor capability used by history
// reads inside the transaction's lifecycle fence. Instance changes on reopen
// and generation changes on restore, even when the restored data is identical.
type HistoryCursorScope interface {
	AssignmentHistoryScope() (instance, generation uint64)
}

type HistoryQuery struct {
	EffectiveQuery
	Limit  int
	Cursor string
}

// HistoryPage lists every event through KnowledgeRevision in retained revision
// order. Effective time binds continuation and does not filter the ledger.
type HistoryPage struct {
	TagID, AssignmentID                          string
	TagRevision, HeadRevision, KnowledgeRevision uint64
	HeadRetired                                  bool
	Events                                       []HistoryEntry
	Next                                         string
}

type HistoryEntry struct{ Event }

// MarshalJSON uses the lossless internal command encoding because the domain
// values in Event hold private fields. The receipt identities are in Input.
func (entry HistoryEntry) MarshalJSON() ([]byte, error) {
	event := entry.Event
	input, err := canonicalCommand(event.Command, event.Actor)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Input     json.RawMessage
		Revision  uint64
		Previous  string
		Recorded  time.Time
		Primitive []byte
	}{input, event.Result.Revision, event.Result.PreviousCommand.String(), event.RecordedAt, event.Primitive})
}

func (s *Service) ListAssignmentHistory(ctx context.Context, q HistoryQuery) (HistoryPage, error) {
	var page HistoryPage
	var stale bool
	err := s.run(ctx, func(ctx context.Context, p Port, actor AuthenticatedActor, _ d.ReviewAuthority) error {
		if !actor.Read {
			return ErrUnauthorized
		}
		if q.Limit <= 0 || q.Limit > MaxHistoryEntries {
			return ErrInvalidInput
		}
		if len(q.Cursor) > MaxHistoryCursor {
			return ErrLimit
		}
		a, err := readAssignment(ctx, p, q.EffectiveQuery)
		if err != nil {
			return err
		}
		scope, ok := s.transactions.(HistoryCursorScope)
		if !ok {
			return ErrUnavailable
		}
		instance, generation := scope.AssignmentHistoryScope()
		if instance == 0 {
			return ErrUnavailable
		}
		binding, err := historyBinding(a, q, actor.Reference, instance, generation)
		if err != nil {
			return err
		}
		var after uint64
		if q.Cursor != "" {
			after, stale = historyCursorAfter(q.Cursor, binding, a.knowledge)
			if stale {
				// The read transaction succeeded. Report the query-specific
				// outcome after the shared transaction and deadline checks.
				return nil
			}
		}
		page, err = a.historyPage(ctx, q.Limit, after, binding)
		return err
	})
	if err != nil {
		return HistoryPage{}, err
	}
	if stale {
		return HistoryPage{}, ErrStaleCursor
	}
	return page, nil
}

func (a assignmentRead) historyPage(ctx context.Context, limit int, after uint64, binding []byte) (HistoryPage, error) {
	page := HistoryPage{
		TagID: a.tag.ID.String(), AssignmentID: a.snapshot.ID.String(), TagRevision: a.tag.Revision,
		HeadRevision: uint64(len(a.snapshot.Events)), KnowledgeRevision: a.knowledge,
		HeadRetired: a.sealed, Events: make([]HistoryEntry, 0, limit),
	}
	// Start with the exact empty envelope. Each entry adds its compact JSON
	// length and a separator, and an incomplete page also needs a cursor.
	raw, err := json.Marshal(page)
	if err != nil {
		return HistoryPage{}, ErrUnavailable
	}
	size := len(raw)
	cursorSize := base64.RawURLEncoding.EncodedLen(historyTokenBytes)
	for after < a.knowledge && len(page.Events) < limit {
		if err := ctx.Err(); err != nil {
			return HistoryPage{}, err
		}
		entry := HistoryEntry{a.snapshot.Events[after]}
		encoded, err := entry.MarshalJSON()
		if err != nil {
			return HistoryPage{}, ErrUnavailable
		}
		nextSize := size + len(encoded)
		if len(page.Events) != 0 {
			nextSize++ // comma
		}
		responseSize := nextSize
		if after+1 < a.knowledge {
			responseSize += cursorSize
		}
		if responseSize > MaxHistoryBytes {
			if len(page.Events) == 0 {
				return HistoryPage{}, ErrLimit
			}
			break
		}
		page.Events = append(page.Events, entry)
		size = nextSize
		after++
	}
	if after < a.knowledge {
		page.Next = historyCursor(after, binding)
	}
	return page, nil
}

var historyKey = newHistoryKey(rand.Read)

func newHistoryKey(read func([]byte) (int, error)) func() ([32]byte, error) {
	return sync.OnceValues(func() ([32]byte, error) {
		var key [32]byte
		if n, err := read(key[:]); err != nil || n != len(key) {
			return [32]byte{}, ErrUnavailable
		}
		return key, nil
	})
}

func historyBinding(a assignmentRead, q HistoryQuery, actor string, instance, generation uint64) ([]byte, error) {
	key, err := historyKey()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key[:])
	fmt.Fprintf(mac, "PI09/history/1|%d|%d|%q|%s|%s|%d|%s|%t|%d|%d|%d|",
		instance, generation, actor, a.tag.ID.String(), a.snapshot.ID.String(), a.tag.Revision,
		a.at.Format(time.RFC3339Nano), q.KnowledgeRevision != nil, a.knowledge, len(a.snapshot.Events), q.Limit)
	// Follow ledger order to bind the entire reference set deterministically,
	// including targets outside the page or selected knowledge revision.
	for _, event := range a.snapshot.Events {
		if !event.Command.Kind.target() {
			continue
		}
		target := event.Command.Target
		value := a.targets[target]
		fingerprint := value.Version.Fingerprint()
		fmt.Fprintf(mac, "%s|%s|%d|%d|%s|%t|%t|%t|%t|%t|%d|",
			target.ContentID().String(), target.VersionID().String(), value.CatalogRevision,
			fingerprint.AudioID().Uint64(), fingerprint.Hash().String(), value.Bound, value.Superseded,
			value.EditorialConflict, value.Dimensions.PublicSupport(), value.Dimensions.CustomOrigin(), value.Dimensions.Protection())
	}
	return mac.Sum(nil), nil
}

func historyCursor(after uint64, binding []byte) string {
	raw := [historyTokenBytes]byte{1}
	binary.BigEndian.PutUint64(raw[1:9], after)
	mac := hmac.New(sha256.New, binding)
	mac.Write(raw[:9])
	copy(raw[9:], mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func historyCursorAfter(cursor string, binding []byte, knowledge uint64) (after uint64, stale bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != historyTokenBytes {
		return 0, true
	}
	after = binary.BigEndian.Uint64(raw[1:9])
	return after, after == 0 || after >= knowledge || !hmac.Equal([]byte(cursor), []byte(historyCursor(after, binding)))
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
