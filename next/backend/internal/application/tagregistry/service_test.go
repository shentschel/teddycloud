package tagregistry_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

const (
	syntheticUID  = "1A:2B:3C:4D:5E:6F:70:81"
	syntheticRUID = "81706F5E4D3C2B1A"
	zeroUID       = "00:00:00:00:00:00:00:00"
	zeroRUID      = "0000000000000000"
)

func TestTagRegistrationNormalization(t *testing.T) {
	valid := []struct {
		name     string
		physical tagregistry.PhysicalIdentityInput
		wantUID  string
		wantRUID string
	}{
		{
			name: "lowercase UID only",
			physical: tagregistry.PhysicalIdentityInput{
				UID: present(strings.ToLower(syntheticUID)),
			},
			wantUID: syntheticUID, wantRUID: syntheticRUID,
		},
		{
			name: "lowercase rUID only reverses bytes",
			physical: tagregistry.PhysicalIdentityInput{
				RUID: present(strings.ToLower(syntheticRUID)),
			},
			wantUID: syntheticUID, wantRUID: syntheticRUID,
		},
		{
			name: "matching pair",
			physical: tagregistry.PhysicalIdentityInput{
				UID: present(strings.ToLower(syntheticUID)), RUID: present(syntheticRUID),
			},
			wantUID: syntheticUID, wantRUID: syntheticRUID,
		},
		{
			name: "zero UID is present and valid",
			physical: tagregistry.PhysicalIdentityInput{
				UID: present(zeroUID),
			},
			wantUID: zeroUID, wantRUID: zeroRUID,
		},
		{
			name: "zero pair is present and valid",
			physical: tagregistry.PhysicalIdentityInput{
				UID: present(zeroUID), RUID: present(zeroRUID),
			},
			wantUID: zeroUID, wantRUID: zeroRUID,
		},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			service := tagregistry.New(store)
			got, err := service.Register(context.Background(), tagregistry.RegisterCommand{
				OpaqueID: syntheticTagID('0'),
				Physical: test.physical,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.UID().String() != test.wantUID || got.RUID().String() != test.wantRUID {
				t.Fatalf("normalized identity = %s/%s", got.UID(), got.RUID())
			}
			if got.Revision() != domaintag.InitialRevision {
				t.Fatalf("revision = %d", got.Revision())
			}
			assertUnknownMetadata(t, got)
			if store.transactionCalls != 1 || store.insertCalls != 1 {
				t.Fatalf("transactions/inserts = %d/%d, want 1/1", store.transactionCalls, store.insertCalls)
			}
		})
	}

	invalid := []struct {
		name     string
		opaqueID string
		physical tagregistry.PhysicalIdentityInput
	}{
		{name: "neither field", opaqueID: syntheticTagID('0')},
		{name: "absent values do not imply presence", opaqueID: syntheticTagID('0'), physical: tagregistry.PhysicalIdentityInput{UID: tagregistry.OptionalText{Value: syntheticUID}, RUID: tagregistry.OptionalText{Value: syntheticRUID}}},
		{name: "explicit empty UID", opaqueID: syntheticTagID('0'), physical: tagregistry.PhysicalIdentityInput{UID: present("")}},
		{name: "explicit empty rUID", opaqueID: syntheticTagID('0'), physical: tagregistry.PhysicalIdentityInput{RUID: present("")}},
		{name: "malformed UID", opaqueID: syntheticTagID('0'), physical: tagregistry.PhysicalIdentityInput{UID: present("1122334455667788")}},
		{name: "malformed rUID", opaqueID: syntheticTagID('0'), physical: tagregistry.PhysicalIdentityInput{RUID: present("88:77:66:55:44:33:22:11")}},
		{name: "mismatched pair", opaqueID: syntheticTagID('0'), physical: tagregistry.PhysicalIdentityInput{UID: present(syntheticUID), RUID: present("9977665544332211")}},
		{name: "malformed opaque ID", opaqueID: "tag_invalid", physical: tagregistry.PhysicalIdentityInput{UID: present(syntheticUID)}},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			service := tagregistry.New(store)
			if _, err := service.Register(context.Background(), tagregistry.RegisterCommand{
				OpaqueID: test.opaqueID,
				Physical: test.physical,
			}); err != tagregistry.ErrInvalidInput {
				t.Fatalf("error = %v, want %v", err, tagregistry.ErrInvalidInput)
			}
			if store.transactionCalls != 0 || store.insertCalls != 0 {
				t.Fatal("invalid input reached transaction")
			}
		})
	}
}

func TestTagRegistrationReplayAndCollisions(t *testing.T) {
	store := newMemoryStore()
	service := tagregistry.New(store)
	firstCommand := command(syntheticTagID('0'), syntheticUID)
	first, err := service.Register(context.Background(), firstCommand)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Register(context.Background(), firstCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Equal(first) || replayed.Revision() != domaintag.InitialRevision {
		t.Fatal("exact replay changed the record")
	}
	if store.insertCalls != 1 || store.findByIDCalls != 2 || store.findByUIDCalls != 2 {
		t.Fatalf("insert/id/uid calls = %d/%d/%d", store.insertCalls, store.findByIDCalls, store.findByUIDCalls)
	}

	if _, err := service.Register(context.Background(), command(syntheticTagID('1'), syntheticUID)); err != tagregistry.ErrIdentityConflict {
		t.Fatalf("same UID error = %v", err)
	}
	if _, err := service.Register(context.Background(), command(syntheticTagID('0'), "21:22:33:44:55:66:77:88")); err != tagregistry.ErrIdentityConflict {
		t.Fatalf("same ID error = %v", err)
	}
	if store.insertCalls != 1 || store.recordCount() != 1 {
		t.Fatal("collision overwrote identity")
	}
	unchanged, err := service.FindByID(context.Background(), syntheticTagID('0'))
	if err != nil || !unchanged.Equal(first) {
		t.Fatal("stored identity changed after collision")
	}
}

func TestTagQueriesUseCallbackScopedTransactions(t *testing.T) {
	store := newMemoryStore()
	service := tagregistry.New(store)
	want, err := service.Register(context.Background(), command(syntheticTagID('0'), syntheticUID))
	if err != nil {
		t.Fatal(err)
	}
	baselineTransactions := store.transactionCalls

	queries := []struct {
		name string
		find func() (domaintag.Tag, error)
	}{
		{name: "opaque ID", find: func() (domaintag.Tag, error) { return service.FindByID(context.Background(), syntheticTagID('0')) }},
		{name: "physical UID", find: func() (domaintag.Tag, error) {
			return service.FindByUID(context.Background(), strings.ToLower(syntheticUID))
		}},
		{name: "physical rUID", find: func() (domaintag.Tag, error) {
			return service.FindByRUID(context.Background(), strings.ToLower(syntheticRUID))
		}},
		{name: "matching physical pair", find: func() (domaintag.Tag, error) {
			return service.FindByPhysicalIdentity(context.Background(), tagregistry.PhysicalIdentityInput{UID: present(syntheticUID), RUID: present(syntheticRUID)})
		}},
	}
	for _, query := range queries {
		t.Run(query.name, func(t *testing.T) {
			got, findErr := query.find()
			if findErr != nil || !got.Equal(want) {
				t.Fatalf("got %#v, error %v", got, findErr)
			}
		})
	}
	if store.transactionCalls != baselineTransactions+len(queries) {
		t.Fatalf("query transaction count = %d", store.transactionCalls-baselineTransactions)
	}

	bytes := want.UID().Bytes()
	bytes[0] ^= 0xff
	again, err := service.FindByUID(context.Background(), syntheticUID)
	if err != nil || !again.Equal(want) {
		t.Fatal("returned value was not defensively immutable")
	}
}

func TestTagQueryMissingInvalidAndContextErrors(t *testing.T) {
	store := newMemoryStore()
	service := tagregistry.New(store)
	if _, err := service.FindByID(context.Background(), syntheticTagID('0')); err != tagregistry.ErrTagNotFound {
		t.Fatalf("missing error = %v", err)
	}
	before := store.transactionCalls
	if _, err := service.FindByUID(context.Background(), ""); err != tagregistry.ErrInvalidInput {
		t.Fatalf("empty UID error = %v", err)
	}
	if store.transactionCalls != before {
		t.Fatal("invalid query reached transaction")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.FindByID(canceled, syntheticTagID('0')); err != context.Canceled {
		t.Fatalf("canceled error = %v", err)
	}
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if _, err := service.FindByID(deadline, syntheticTagID('0')); err != context.DeadlineExceeded {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestTagServiceSanitizesStorageErrorsAndRollsBack(t *testing.T) {
	leaked := errors.New("SQL tc_tags /private/data.db UID 11:22:33:44:55:66:77:88")

	t.Run("transaction", func(t *testing.T) {
		store := newMemoryStore()
		store.transactionErr = leaked
		_, err := tagregistry.New(store).FindByID(context.Background(), syntheticTagID('0'))
		assertSanitizedUnavailable(t, err, leaked)
	})

	t.Run("repository read", func(t *testing.T) {
		store := newMemoryStore()
		store.findByIDErr = leaked
		_, err := tagregistry.New(store).FindByID(context.Background(), syntheticTagID('0'))
		assertSanitizedUnavailable(t, err, leaked)
	})

	t.Run("insert rollback", func(t *testing.T) {
		store := newMemoryStore()
		store.insertAfterWriteErr = leaked
		service := tagregistry.New(store)
		_, err := service.Register(context.Background(), command(syntheticTagID('0'), syntheticUID))
		assertSanitizedUnavailable(t, err, leaked)
		if store.recordCount() != 0 {
			t.Fatal("failed transaction retained partial identity")
		}
		store.insertAfterWriteErr = nil
		if _, err := service.Register(context.Background(), command(syntheticTagID('0'), syntheticUID)); err != nil {
			t.Fatalf("registration after rollback: %v", err)
		}
	})

	t.Run("contention", func(t *testing.T) {
		store := newMemoryStore()
		store.transactionErr = fmt.Errorf("driver detail: %w", tagregistry.ErrRepositoryContention)
		_, err := tagregistry.New(store).FindByID(context.Background(), syntheticTagID('0'))
		if err != tagregistry.ErrRepositoryContention {
			t.Fatalf("contention error = %v", err)
		}
		if strings.Contains(err.Error(), "driver") {
			t.Fatal("contention leaked storage detail")
		}
	})
}

func assertSanitizedUnavailable(t *testing.T, got, leaked error) {
	t.Helper()
	if got != tagregistry.ErrRepositoryUnavailable {
		t.Fatalf("error = %v, want unavailable", got)
	}
	if errors.Is(got, leaked) || strings.Contains(got.Error(), "SQL") || strings.Contains(got.Error(), "UID") || strings.Contains(got.Error(), "/private") {
		t.Fatalf("error disclosed storage detail: %v", got)
	}
}

func assertUnknownMetadata(t *testing.T, registered domaintag.Tag) {
	t.Helper()
	metadata := registered.Metadata()
	if metadata.ProtocolValid() != domaintag.MetadataUnknown ||
		metadata.Claimed() != domaintag.MetadataUnknown ||
		metadata.CloudAuth() != domaintag.MetadataUnknown ||
		metadata.Owned() != domaintag.MetadataUnknown {
		t.Fatal("registration inferred metadata")
	}
}

func present(value string) tagregistry.OptionalText {
	return tagregistry.OptionalText{Present: true, Value: value}
}

func syntheticTagID(fill byte) string {
	return "tag_" + strings.Repeat(string(fill), 26)
}

func command(id, uid string) tagregistry.RegisterCommand {
	return tagregistry.RegisterCommand{
		OpaqueID: id,
		Physical: tagregistry.PhysicalIdentityInput{UID: present(uid)},
	}
}

type memoryStore struct {
	mu                  sync.Mutex
	byID                map[identity.TagID]domaintag.Tag
	byUID               map[domaintag.UID]domaintag.Tag
	transactionCalls    int
	findByIDCalls       int
	findByUIDCalls      int
	insertCalls         int
	transactionErr      error
	findByIDErr         error
	findByUIDErr        error
	insertErr           error
	insertAfterWriteErr error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		byID:  make(map[identity.TagID]domaintag.Tag),
		byUID: make(map[domaintag.UID]domaintag.Tag),
	}
}

func (store *memoryStore) WithinTagTransaction(ctx context.Context, callback func(tagregistry.TagRepository) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.transactionCalls++
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.transactionErr != nil {
		return store.transactionErr
	}
	beforeID := cloneMap(store.byID)
	beforeUID := cloneMap(store.byUID)
	repository := &memoryRepository{store: store, active: true}
	err := callback(repository)
	repository.active = false
	if err != nil {
		store.byID = beforeID
		store.byUID = beforeUID
	}
	return err
}

func (store *memoryStore) recordCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.byID)
}

type memoryRepository struct {
	store  *memoryStore
	active bool
}

func (repository *memoryRepository) FindByID(_ context.Context, id identity.TagID) (domaintag.Tag, bool, error) {
	if !repository.active {
		return domaintag.Tag{}, false, errors.New("repository used outside transaction")
	}
	repository.store.findByIDCalls++
	if repository.store.findByIDErr != nil {
		return domaintag.Tag{}, false, repository.store.findByIDErr
	}
	registered, found := repository.store.byID[id]
	return registered, found, nil
}

func (repository *memoryRepository) FindByUID(_ context.Context, uid domaintag.UID) (domaintag.Tag, bool, error) {
	if !repository.active {
		return domaintag.Tag{}, false, errors.New("repository used outside transaction")
	}
	repository.store.findByUIDCalls++
	if repository.store.findByUIDErr != nil {
		return domaintag.Tag{}, false, repository.store.findByUIDErr
	}
	registered, found := repository.store.byUID[uid]
	return registered, found, nil
}

func (repository *memoryRepository) Insert(_ context.Context, registered domaintag.Tag) error {
	if !repository.active {
		return errors.New("repository used outside transaction")
	}
	repository.store.insertCalls++
	if repository.store.insertErr != nil {
		return repository.store.insertErr
	}
	if _, found := repository.store.byID[registered.ID()]; found {
		return tagregistry.ErrIdentityConflict
	}
	if _, found := repository.store.byUID[registered.UID()]; found {
		return tagregistry.ErrIdentityConflict
	}
	repository.store.byID[registered.ID()] = registered
	repository.store.byUID[registered.UID()] = registered
	return repository.store.insertAfterWriteErr
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	cloned := make(map[K]V, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
