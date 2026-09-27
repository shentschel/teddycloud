package tag_test

import (
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

func TestNewTagStartsWithImmutableIdentityAndUnknownMetadata(t *testing.T) {
	id := mustTagID(t, "tag_"+strings.Repeat("0", 26))
	uid := tag.UIDFromBytes([8]byte{})

	registered, err := tag.NewTag(id, uid)
	if err != nil {
		t.Fatal(err)
	}
	if registered.ID() != id || registered.UID() != uid || registered.RUID() != uid.RUID() {
		t.Fatal("registration changed identity")
	}
	if registered.Revision() != tag.InitialRevision {
		t.Fatalf("revision = %d, want %d", registered.Revision(), tag.InitialRevision)
	}
	metadata := registered.Metadata()
	states := []tag.MetadataState{
		metadata.ProtocolValid(),
		metadata.Claimed(),
		metadata.CloudAuth(),
		metadata.Owned(),
	}
	for i, state := range states {
		if state != tag.MetadataUnknown {
			t.Fatalf("metadata state %d = %d, want unknown", i, state)
		}
	}

	bytes := registered.UID().Bytes()
	bytes[0] = 0xff
	copyOfTag := registered
	copyBytes := copyOfTag.UID().Bytes()
	copyBytes[1] = 0xff
	if registered.UID() != (tag.UIDFromBytes([8]byte{})) {
		t.Fatal("caller mutated registered UID")
	}
}

func TestNewTagRejectsZeroOpaqueIdentity(t *testing.T) {
	if _, err := tag.NewTag(identity.TagID{}, tag.UIDFromBytes([8]byte{})); err != tag.ErrZeroTagID {
		t.Fatalf("error = %v, want %v", err, tag.ErrZeroTagID)
	}
}

func mustTagID(t *testing.T, text string) identity.TagID {
	t.Helper()
	id, err := identity.ParseTagID(text)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
