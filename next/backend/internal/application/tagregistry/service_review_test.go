package tagregistry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

const (
	caseUID  = "1A:2B:3C:4D:5E:6F:70:81"
	caseRUID = "81706F5E4D3C2B1A"
)

func TestTagRegistrationNormalizationHexCase(t *testing.T) {
	tests := []struct {
		name     string
		physical tagregistry.PhysicalIdentityInput
	}{
		{
			name: "lowercase UID",
			physical: tagregistry.PhysicalIdentityInput{
				UID: present(strings.ToLower(caseUID)),
			},
		},
		{
			name: "lowercase rUID",
			physical: tagregistry.PhysicalIdentityInput{
				RUID: present(strings.ToLower(caseRUID)),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registered, err := tagregistry.New(newMemoryStore()).Register(
				context.Background(),
				tagregistry.RegisterCommand{OpaqueID: syntheticTagID('0'), Physical: test.physical},
			)
			if err != nil {
				t.Fatal(err)
			}
			if registered.UID().String() != caseUID || registered.RUID().String() != caseRUID {
				t.Fatalf("normalized identity = %s/%s", registered.UID(), registered.RUID())
			}
		})
	}
}

func TestTagQueriesRejectCorruptFoundRecords(t *testing.T) {
	requestedID := mustReviewID(t, syntheticTagID('0'))
	requestedUID := mustReviewUID(t, caseUID)
	wrongIDTag := mustReviewTag(t, syntheticTagID('1'), caseUID)
	wrongUIDTag := mustReviewTag(t, syntheticTagID('0'), "2A:2B:3C:4D:5E:6F:70:81")

	tests := []struct {
		name  string
		seed  func(*memoryStore)
		query func(tagregistry.Service) (domaintag.Tag, error)
	}{
		{
			name: "zero Tag from ID lookup",
			seed: func(store *memoryStore) { store.byID[requestedID] = domaintag.Tag{} },
			query: func(service tagregistry.Service) (domaintag.Tag, error) {
				return service.FindByID(context.Background(), syntheticTagID('0'))
			},
		},
		{
			name: "different ID from ID lookup",
			seed: func(store *memoryStore) { store.byID[requestedID] = wrongIDTag },
			query: func(service tagregistry.Service) (domaintag.Tag, error) {
				return service.FindByID(context.Background(), syntheticTagID('0'))
			},
		},
		{
			name: "zero Tag from UID lookup",
			seed: func(store *memoryStore) { store.byUID[requestedUID] = domaintag.Tag{} },
			query: func(service tagregistry.Service) (domaintag.Tag, error) {
				return service.FindByUID(context.Background(), caseUID)
			},
		},
		{
			name: "different UID from UID lookup",
			seed: func(store *memoryStore) { store.byUID[requestedUID] = wrongUIDTag },
			query: func(service tagregistry.Service) (domaintag.Tag, error) {
				return service.FindByUID(context.Background(), caseUID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			test.seed(store)
			beforeID := cloneMap(store.byID)
			beforeUID := cloneMap(store.byUID)
			if _, err := test.query(tagregistry.New(store)); err != tagregistry.ErrRepositoryUnavailable {
				t.Fatalf("error = %v, want unavailable", err)
			}
			if store.insertCalls != 0 || !sameReviewTagMap(store.byID, beforeID) || !sameReviewTagMap(store.byUID, beforeUID) {
				t.Fatal("corrupt query changed storage")
			}
		})
	}
}

func TestTagRegistrationRejectsCorruptLookupWithoutWrite(t *testing.T) {
	requestedID := mustReviewID(t, syntheticTagID('0'))
	requestedUID := mustReviewUID(t, caseUID)
	wrongIDTag := mustReviewTag(t, syntheticTagID('1'), caseUID)
	wrongUIDTag := mustReviewTag(t, syntheticTagID('0'), "2A:2B:3C:4D:5E:6F:70:81")

	tests := []struct {
		name string
		seed func(*memoryStore)
	}{
		{name: "zero Tag by ID", seed: func(store *memoryStore) { store.byID[requestedID] = domaintag.Tag{} }},
		{name: "wrong ID by ID", seed: func(store *memoryStore) { store.byID[requestedID] = wrongIDTag }},
		{name: "zero Tag by UID", seed: func(store *memoryStore) { store.byUID[requestedUID] = domaintag.Tag{} }},
		{name: "wrong UID by UID", seed: func(store *memoryStore) { store.byUID[requestedUID] = wrongUIDTag }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			test.seed(store)
			beforeID := cloneMap(store.byID)
			beforeUID := cloneMap(store.byUID)
			_, err := tagregistry.New(store).Register(
				context.Background(),
				tagregistry.RegisterCommand{OpaqueID: syntheticTagID('0'), Physical: tagregistry.PhysicalIdentityInput{UID: present(caseUID)}},
			)
			if err != tagregistry.ErrRepositoryUnavailable {
				t.Fatalf("error = %v, want unavailable", err)
			}
			if store.insertCalls != 0 || !sameReviewTagMap(store.byID, beforeID) || !sameReviewTagMap(store.byUID, beforeUID) {
				t.Fatal("corrupt registration lookup changed storage")
			}
		})
	}
}

func mustReviewID(t *testing.T, text string) identity.TagID {
	t.Helper()
	id, err := identity.ParseTagID(text)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustReviewUID(t *testing.T, text string) domaintag.UID {
	t.Helper()
	uid, err := domaintag.ParseUID(text)
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

func mustReviewTag(t *testing.T, idText, uidText string) domaintag.Tag {
	t.Helper()
	registered, err := domaintag.NewTag(mustReviewID(t, idText), mustReviewUID(t, uidText))
	if err != nil {
		t.Fatal(err)
	}
	return registered
}

func sameReviewTagMap[K comparable](left, right map[K]domaintag.Tag) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || !value.Equal(other) {
			return false
		}
	}
	return true
}
