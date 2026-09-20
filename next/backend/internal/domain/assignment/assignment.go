// Package assignment prototypes immutable, effective-dated assignment history.
// It does not certify blob availability, ownership or applied gateway state.
package assignment

import (
	"errors"
	"sort"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const MaxRevisions = 4096

var (
	ErrInvalidID        = errors.New("invalid assignment identity")
	ErrInvalidReference = errors.New("assignment references are required")
	ErrInvalidInterval  = errors.New("invalid assignment effective interval")
	ErrRevisionConflict = errors.New("conflicting assignment revision")
	ErrOverlap          = errors.New("overlapping assignment intervals")
	ErrLimit            = errors.New("assignment revision limit exceeded")
)

type ID struct{ value string }

func ParseID(text string) (ID, error) {
	if len(text) != 30 || text[:4] != "asn_" {
		return ID{}, ErrInvalidID
	}
	if _, err := identity.ParseContentID("cnt_" + text[4:]); err != nil {
		return ID{}, ErrInvalidID
	}
	return ID{value: text}, nil
}
func (id ID) String() string { return id.value }
func (id ID) IsZero() bool   { return id.value == "" }

// Interval is [from, until). A zero until explicitly means no known end.
// Times are normalized to UTC without monotonic process-local clock state.
type Interval struct{ from, until time.Time }

func NewInterval(from, until time.Time) (Interval, error) {
	if from.IsZero() || (!until.IsZero() && !until.After(from)) {
		return Interval{}, ErrInvalidInterval
	}
	from = from.Round(0).UTC()
	if !until.IsZero() {
		until = until.Round(0).UTC()
	} else {
		until = time.Time{}
	}
	return Interval{from: from, until: until}, nil
}
func (i Interval) From() time.Time  { return i.from }
func (i Interval) Until() time.Time { return i.until }
func (i Interval) Contains(at time.Time) bool {
	return !i.from.IsZero() && !at.Before(i.from) && (i.until.IsZero() || at.Before(i.until))
}

type Assignment struct {
	id       ID
	revision uint64
	tag      identity.TagID
	version  identity.ContentVersionID
	interval Interval
}

func New(id ID, revision uint64, tag identity.TagID, version identity.ContentVersionID, interval Interval) (Assignment, error) {
	if id.IsZero() {
		return Assignment{}, ErrInvalidID
	}
	if tag.IsZero() || version.IsZero() {
		return Assignment{}, ErrInvalidReference
	}
	if revision == 0 {
		return Assignment{}, ErrRevisionConflict
	}
	if interval.from.IsZero() {
		return Assignment{}, ErrInvalidInterval
	}
	return Assignment{id: id, revision: revision, tag: tag, version: version, interval: interval}, nil
}
func (a Assignment) ID() ID                                      { return a.id }
func (a Assignment) Revision() uint64                            { return a.revision }
func (a Assignment) TagID() identity.TagID                       { return a.tag }
func (a Assignment) ContentVersionID() identity.ContentVersionID { return a.version }
func (a Assignment) Effective() Interval                         { return a.interval }

// History is a validated snapshot for one Assignment aggregate and one Tag.
// Revisions must increase with effective time; gaps are allowed and never
// filled by inference. Exact replay is idempotent; conflicting replay fails.
// Intervals are supplied explicitly, never closed by mutating an older record.
// Command fencing and desired/applied projection belong to later services.
type History struct{ entries []Assignment }

func NewHistory(entries []Assignment) (History, error) {
	if len(entries) > MaxRevisions {
		return History{}, ErrLimit
	}
	sorted := append([]Assignment(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].revision < sorted[j].revision })
	result := make([]Assignment, 0, len(sorted))
	for _, a := range sorted {
		if _, err := New(a.id, a.revision, a.tag, a.version, a.interval); err != nil {
			return History{}, err
		}
		if len(result) > 0 {
			previous := result[len(result)-1]
			if a.id != previous.id || a.tag != previous.tag {
				return History{}, ErrRevisionConflict
			}
			if a.revision == previous.revision {
				if a != previous {
					return History{}, ErrRevisionConflict
				}
				continue
			}
			if !a.interval.from.After(previous.interval.from) {
				return History{}, ErrRevisionConflict
			}
			if previous.interval.until.IsZero() || a.interval.from.Before(previous.interval.until) {
				return History{}, ErrOverlap
			}
		}
		result = append(result, a)
	}
	return History{entries: result}, nil
}
func (h History) Entries() []Assignment { return append([]Assignment(nil), h.entries...) }

// CurrentAt is time-based: the greatest revision may still be in the future.
// Validation guarantees at most one match. No wall clock or first-match rule.
func (h History) CurrentAt(at time.Time) (Assignment, bool) {
	i := sort.Search(len(h.entries), func(i int) bool { return h.entries[i].interval.from.After(at) })
	if i == 0 || !h.entries[i-1].interval.Contains(at) {
		return Assignment{}, false
	}
	return h.entries[i-1], true
}
