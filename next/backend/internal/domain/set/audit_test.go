package set

import (
	"reflect"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func TestAuditSetRelationsPreserveReferencesAndRejectPositionCollisions(t *testing.T) {
	id, _ := ParseID("set_" + payload)
	a, _ := identity.ParseContentID("cnt_" + payload)
	b, _ := identity.ParseContentID("cnt_1123456789abcdefghjkmnpqrs")
	input := []Member{{99, a}, {0, b}, {7, a}}
	want := []Member{{0, b}, {7, a}, {99, a}}
	group, err := New(id, input)
	if err != nil || !reflect.DeepEqual(group.Members(), want) {
		t.Fatal("position/reference association or repeated content lost")
	}
	if input[0].Position != 99 {
		t.Fatal("constructor sorted caller data")
	}
	input[1].ContentID = a
	returned := group.Members()
	returned[1].ContentID = b
	if !reflect.DeepEqual(group.Members(), want) {
		t.Fatal("input/output aliases membership")
	}
	for _, members := range [][]Member{{{7, a}, {7, b}}, {{7, b}, {7, a}}, {{7, a}, {7, a}}} {
		if _, err := New(id, members); err != ErrDuplicatePosition {
			t.Fatal("duplicate position silently resolved")
		}
	}
	other, _ := ParseID("set_1123456789abcdefghjkmnpqrs")
	if _, err := New(other, want); err != nil {
		t.Fatal("invented cross-set content uniqueness")
	}
}
