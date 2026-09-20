// Package set models ordered Content relations, independently of track layouts.
// Cardinality policy and public catalog vocabulary remain PI-13 decisions.
package set

import (
	"errors"
	"sort"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// MaxMembers is a prototype resource bound, not a product cardinality rule.
const MaxMembers = 1024

var (
	ErrInvalidID         = errors.New("invalid set identity")
	ErrInvalidMember     = errors.New("invalid set member")
	ErrDuplicatePosition = errors.New("duplicate set member position")
	ErrLimit             = errors.New("set member limit exceeded")
)

// ID is allocated independently of metadata; its payload follows PI-04/A.
type ID struct{ value string }

func ParseID(text string) (ID, error) {
	if len(text) != 30 || text[:4] != "set_" {
		return ID{}, ErrInvalidID
	}
	if _, err := identity.ParseContentID("cnt_" + text[4:]); err != nil {
		return ID{}, ErrInvalidID
	}
	return ID{value: text}, nil
}
func (id ID) String() string { return id.value }
func (id ID) IsZero() bool   { return id.value == "" }

// Member contains only an explicit, zero-based position and a Content reference.
// Sparse positions and repeated Content references are preserved; uniqueness of
// Content within/across Sets is deliberately not a PI-04 policy.
type Member struct {
	Position  uint32
	ContentID identity.ContentID
}

// Set owns membership order, not Content, tracks, articles or member models.
type Set struct {
	id      ID
	members []Member
}

func New(id ID, members []Member) (Set, error) {
	if id.IsZero() {
		return Set{}, ErrInvalidID
	}
	if len(members) > MaxMembers {
		return Set{}, ErrLimit
	}
	result := append([]Member(nil), members...)
	sort.Slice(result, func(i, j int) bool { return result[i].Position < result[j].Position })
	for i, member := range result {
		if member.ContentID.IsZero() {
			return Set{}, ErrInvalidMember
		}
		if i > 0 && result[i-1].Position == member.Position {
			return Set{}, ErrDuplicatePosition
		}
	}
	return Set{id: id, members: result}, nil
}
func (s Set) ID() ID            { return s.id }
func (s Set) Members() []Member { return append([]Member(nil), s.members...) }
