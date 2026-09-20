package set

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const payload = "0123456789abcdefghjkmnpqrs"

func TestMembersOrderCopyAndLimits(t *testing.T) {
	id, err := ParseID("set_" + payload)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := identity.ParseContentID("cnt_" + payload)
	members := []Member{{3, content}, {0, content}}
	s, err := New(id, members)
	if err != nil || s.Members()[0].Position != 0 {
		t.Fatal("ordering failed")
	}
	members[0] = Member{}
	copy := s.Members()
	copy[0] = Member{}
	if s.Members()[0].ContentID != content {
		t.Fatal("membership alias")
	}
	for _, tc := range []struct {
		members []Member
		err     error
	}{
		{[]Member{{0, content}, {0, content}}, ErrDuplicatePosition},
		{[]Member{{0, identity.ContentID{}}}, ErrInvalidMember},
		{make([]Member, MaxMembers+1), ErrLimit},
	} {
		if _, err := New(id, tc.members); !errors.Is(err, tc.err) {
			t.Fatal("wrong validation")
		}
	}
	if _, err := New(ID{}, nil); err != ErrInvalidID {
		t.Fatal("zero identity accepted")
	}
	if _, err := New(id, nil); err != nil {
		t.Fatal("invented cardinality constraint")
	}
	for _, text := range []string{"", "cnt_" + payload, strings.Repeat("x", 4096), "set_" + strings.Repeat("i", 26)} {
		if _, err := ParseID(text); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
}
func FuzzMemberOrder(f *testing.F) {
	f.Add([]byte{3, 1, 2, 0})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 256 {
			return
		}
		id, _ := ParseID("set_" + payload)
		content, _ := identity.ParseContentID("cnt_" + payload)
		seen := map[byte]bool{}
		var members []Member
		for _, b := range input {
			if !seen[b] {
				members = append(members, Member{uint32(b), content})
				seen[b] = true
			}
		}
		a, err := New(id, members)
		if err != nil {
			t.Fatal(err)
		}
		for i, j := 0, len(members)-1; i < j; i, j = i+1, j-1 {
			members[i], members[j] = members[j], members[i]
		}
		b, err := New(id, members)
		if err != nil || !reflect.DeepEqual(a, b) {
			t.Fatal("input order changed membership")
		}
	})
}
