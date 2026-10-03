package tagregistry_test

import (
	"context"
	"errors"
	"github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	"strings"
	"testing"
	"time"
)

func (r *memoryRepository) CompareAndSwap(_ context.Context, expected domaintag.Revision, next domaintag.Tag) error {
	old, found := r.store.byID[next.ID()]
	if !found {
		return tagregistry.ErrTagNotFound
	}
	if old.Revision() != expected {
		return tagregistry.ErrRevisionConflict
	}
	if !old.IsMetadataSuccessor(next) {
		return tagregistry.ErrInvalidInput
	}
	r.store.byID[next.ID()] = next
	r.store.byUID[next.UID()] = next
	return r.store.insertAfterWriteErr
}
func TestTagMetadataApplicationAtomicityAndErrors(t *testing.T) {
	store := newMemoryStore()
	s := tagregistry.New(store)
	initial, err := s.Register(t.Context(), command(syntheticTagID('0'), syntheticUID))
	if err != nil {
		t.Fatal(err)
	}
	oid, _ := evidence.ParseID("obs_" + strings.Repeat("0", 26))
	source, _ := evidence.NewSource("logical", "r1", "record")
	o, _ := evidence.NewObservation(oid, source, evidence.Claim{Subject: initial.ID().String(), Key: "cloud_auth", Value: "true"}, time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC), evidence.Tentative, evidence.Accepted)
	c := tagregistry.MetadataCommand{TagID: initial.ID(), Change: domaintag.MetadataChange{ExpectedRevision: 1, Key: domaintag.CloudAuth, Observations: []evidence.Observation{o}}}
	store.insertAfterWriteErr = errors.New("private storage path SQL failure")
	if _, err := s.UpdateMetadata(t.Context(), c); err != tagregistry.ErrRepositoryUnavailable {
		t.Fatal(err)
	}
	value, err := s.FindByID(t.Context(), initial.ID().String())
	if err != nil || !value.Equal(initial) {
		t.Fatal("partial write escaped transaction", err)
	}
	store.insertAfterWriteErr = nil
	value, err = s.UpdateMetadata(t.Context(), c)
	if err != nil || value.Metadata().CloudAuth() != domaintag.MetadataObservedTrue {
		t.Fatal(err)
	}
	replay, err := s.UpdateMetadata(t.Context(), c)
	if err != nil || !replay.Equal(value) {
		t.Fatal("replay", err)
	}
	c.Change.Key = domaintag.Owned
	c.Change.Observations = make([]evidence.Observation, 65)
	if _, err := s.UpdateMetadata(t.Context(), c); err != tagregistry.ErrLimitExceeded {
		t.Fatal(err)
	}
	c.Change.Observations = []evidence.Observation{o}
	c.Change.Key = domaintag.CloudAuth
	c.Change.ExpectedRevision = 2
	if _, err := s.UpdateMetadata(t.Context(), c); err != tagregistry.ErrEvidenceConflict {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.UpdateMetadata(ctx, c); err != context.Canceled {
		t.Fatal(err)
	}
}
