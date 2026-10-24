package catalog

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// This staged owner double tests only the application/transaction boundary.
// Real graph computation, SQL revocation and durability are C/D obligations.
type supersessionOwner struct {
	store                                       map[domain.CatalogCommandID]supersessionReceipt
	lookups, commits, callbacks                 int
	repeat, skip, persistedRevoked, noExtension bool
	lookupError, commitError, finishError       error
	durable                                     bool
	afterLookup, afterWrite, beforeCommit       func()
	afterCommit                                 func()
	mutate                                      func(*domain.CatalogSupersessionResult)
	page                                        HistoryPage
	leaked                                      *supersessionPort
}
type supersessionReceipt struct {
	raw    string
	result domain.CatalogSupersessionResult
}
type supersessionPort struct {
	CatalogRepository
	owner  *supersessionOwner
	store  map[domain.CatalogCommandID]supersessionReceipt
	active bool
	used   []SupersessionOperation
}

func (p *supersessionPort) auth(ctx context.Context, op SupersessionOperation) error {
	if !p.active {
		return ErrRepositoryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.owner.persistedRevoked {
		return ErrUnauthorized
	}
	return op.Capability.Authorize(op.Command.Source)
}
func (p *supersessionPort) LookupSupersession(ctx context.Context, op SupersessionOperation) (domain.CatalogSupersessionResult, bool, error) {
	if err := p.auth(ctx, op); err != nil {
		return domain.CatalogSupersessionResult{}, false, err
	}
	p.owner.lookups++
	p.used = append(p.used, op)
	if p.owner.lookupError != nil {
		return domain.CatalogSupersessionResult{}, false, p.owner.lookupError
	}
	raw, err := op.Command.CanonicalEncoding(op.Capability)
	if err != nil {
		return domain.CatalogSupersessionResult{}, false, err
	}
	receipt, found := p.store[op.Command.ID]
	if found && receipt.raw != string(raw) {
		return domain.CatalogSupersessionResult{}, false, domain.ErrCatalogCommandConflict
	}
	if p.owner.afterLookup != nil {
		p.owner.afterLookup()
	}
	result := receipt.result
	result.Support = append([]evidence.ID(nil), result.Support...)
	if found && p.owner.mutate != nil {
		p.owner.mutate(&result)
	}
	return result, found, nil
}
func (p *supersessionPort) CommitSupersession(ctx context.Context, op SupersessionOperation) (domain.CatalogSupersessionResult, error) {
	if err := p.auth(ctx, op); err != nil {
		return domain.CatalogSupersessionResult{}, err
	}
	p.owner.commits++
	if p.owner.commitError != nil {
		return domain.CatalogSupersessionResult{}, p.owner.commitError
	}
	c := op.Command
	aggregate, err := nextAggregate(c.ExpectedAggregate)
	if err != nil {
		return domain.CatalogSupersessionResult{}, err
	}
	graph, err := nextAggregate(c.ExpectedSupersession)
	if err != nil {
		return domain.CatalogSupersessionResult{}, err
	}
	result := domain.CatalogSupersessionResult{Command: c.ID, Content: c.Content, Old: c.Old, New: c.New, AggregateRevision: aggregate, SupersessionRevision: graph, OldReviewRevision: c.ExpectedOldReview, NewReviewRevision: c.ExpectedNewReview, OrderRevision: c.ExpectedAggregate, Support: append([]evidence.ID(nil), c.Support...)}
	raw, err := c.CanonicalEncoding(op.Capability)
	if err != nil {
		return domain.CatalogSupersessionResult{}, err
	}
	p.store[c.ID] = supersessionReceipt{string(raw), result}
	if p.owner.afterWrite != nil {
		p.owner.afterWrite()
	}
	result.Support = append([]evidence.ID(nil), result.Support...)
	if p.owner.mutate != nil {
		p.owner.mutate(&result)
	}
	return result, nil
}
func (p *supersessionPort) QuerySupersession(ctx context.Context, query SupersessionQuery) (HistoryPage, error) {
	if !p.active {
		return HistoryPage{}, ErrRepositoryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return HistoryPage{}, err
	}
	return p.owner.page, p.owner.lookupError
}
func (o *supersessionOwner) WithinCatalogTransaction(ctx context.Context, callback func(CatalogRepository) error) error {
	o.callbacks++
	if o.skip {
		return nil
	}
	if o.noExtension {
		return callback(&contractRepository{})
	}
	staged := map[domain.CatalogCommandID]supersessionReceipt{}
	for k, v := range o.store {
		staged[k] = v
	}
	p := &supersessionPort{owner: o, store: staged, active: true}
	o.leaked = p
	defer func() { p.active = false }()
	if err := callback(p); err != nil {
		return err
	}
	if o.repeat {
		return callback(p)
	}
	if o.beforeCommit != nil {
		o.beforeCommit()
	}
	for _, op := range p.used {
		if err := p.auth(ctx, op); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.finishError == nil || o.durable {
		o.store = staged
	}
	if o.afterCommit != nil {
		o.afterCommit()
	}
	return o.finishError
}

func newSupersessionAppFixture(t *testing.T) (SupersessionService, *supersessionOwner, SupersedeVersionCommand) {
	t.Helper()
	r := newRegistrationFixture(t)
	v := newFixture(t).versions
	o := &supersessionOwner{store: map[domain.CatalogCommandID]supersessionReceipt{}}
	base := r.service.service
	base.transactions = o
	cap, err := domain.NewCatalogSupersessionCapability(r.reviews[0])
	require(t, err)
	s, err := NewSupersessionService(base, cap)
	require(t, err)
	one, _ := domain.NewCatalogRevision(1)
	three, _ := domain.NewCatalogRevision(3)
	id, _ := domain.NewCatalogCommandID()
	c := SupersedeVersionCommand{ID: id, Content: v[0].ContentID(), Old: v[0], New: v[1], ExpectedAggregate: three, ExpectedSupersession: one, ExpectedOldReview: one, ExpectedNewReview: one, Reason: "correct"}
	claim, _ := domain.OwnershipClaim(c.Content)
	subject, _ := domain.VersionSubject(c.Content, c.Old.ID())
	obs := r.observation(t, r.operator, r.opIngest, subject, claim, payload)
	e, err := domain.NewCatalogSupersessionEvidence(r.opIngest, r.operator, domain.CatalogSupersessionEvidenceInput{ID: obs.ID(), Content: c.Content, OldVersion: c.Old.ID(), NewVersion: c.New.ID(), Coordinate: obs.Input().Coordinate, ObservedAt: time.Unix(1, 0)})
	require(t, err)
	c.Evidence = []domain.CatalogSupersessionEvidence{e}
	c.Support = []evidence.ID{e.ID()}
	return s, o, c
}

func TestSupersessionApplicationReplayReadback(t *testing.T) {
	s, o, c := newSupersessionAppFixture(t)
	if r, found, err := s.ReadSupersession(context.Background(), c); err != nil || found || !reflect.DeepEqual(r, domain.CatalogSupersessionResult{}) {
		t.Fatalf("absent: %v %v %+v", err, found, r)
	}
	r, err := s.SupersedeVersion(context.Background(), c)
	require(t, err)
	r.Support[0] = evidence.ID{}
	replayed, err := s.SupersedeVersion(context.Background(), c)
	require(t, err)
	if replayed.Support[0] != c.Support[0] || o.commits != 1 {
		t.Fatal("result aliases store or replay wrote")
	}
	read, found, err := s.ReadSupersession(context.Background(), c)
	require(t, err)
	if !found || !reflect.DeepEqual(read, replayed) {
		t.Fatal("readback differs")
	}
	for name, change := range map[string]func(*SupersedeVersionCommand){
		"reason":   func(c *SupersedeVersionCommand) { c.Reason = "other" },
		"revision": func(c *SupersedeVersionCommand) { c.ExpectedAggregate, _ = domain.NewCatalogRevision(9) },
		"tuple": func(c *SupersedeVersionCommand) {
			order, err := domain.NewVersionOrderEvidence("legacy", 7)
			require(t, err)
			c.Old, err = domain.NewContentVersion(c.Old.ID(), c.Content, c.Old.Fingerprint(), order)
			require(t, err)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := c
			change(&changed)
			_, err := s.SupersedeVersion(context.Background(), changed)
			if err != ErrCommandConflict || o.commits != 1 {
				t.Fatalf("changed command accepted: %v", err)
			}
		})
	}
	op := SupersessionOperation{Capability: s.capability}
	if _, _, err := o.leaked.LookupSupersession(context.Background(), op); err != ErrRepositoryUnavailable {
		t.Fatal("leaked port usable")
	}
}

func TestSupersessionApplicationAuthority(t *testing.T) {
	for _, phase := range []string{"before", "lookup", "write", "commit", "persisted", "persisted-commit"} {
		t.Run(phase, func(t *testing.T) {
			s, o, c := newSupersessionAppFixture(t)
			revoke := func() { s.service.source.Revoke() }
			switch phase {
			case "before":
				revoke()
			case "lookup":
				o.afterLookup = revoke
			case "write":
				o.afterWrite = revoke
			case "commit":
				o.beforeCommit = revoke
			case "persisted":
				o.persistedRevoked = true
			case "persisted-commit":
				o.beforeCommit = func() { o.persistedRevoked = true }
			}
			_, err := s.SupersedeVersion(context.Background(), c)
			if err != ErrUnauthorized || len(o.store) != 0 {
				t.Fatalf("authority: %v committed=%d", err, len(o.store))
			}
			if phase == "before" && o.lookups != 0 {
				t.Fatal("unauthorized existence lookup")
			}
		})
	}
	s, _, _ := newSupersessionAppFixture(t)
	other, _, _ := newSupersessionAppFixture(t)
	if _, err := NewSupersessionService(s.service, other.capability); err != ErrUnauthorized {
		t.Fatalf("reviewer: %v", err)
	}
	review, err := domain.NewReviewCapability(s.service.review.Reviewer(), other.service.source)
	require(t, err)
	cap, err := domain.NewCatalogSupersessionCapability(review)
	require(t, err)
	if _, err := NewSupersessionService(s.service, cap); err != ErrSourceMismatch {
		t.Fatalf("source: %v", err)
	}
	if _, err := NewSupersessionService(s.service, domain.CatalogSupersessionCapability{}); err != ErrUnauthorized {
		t.Fatalf("empty grant: %v", err)
	}
}

func TestSupersessionApplicationMalformedResponseRollback(t *testing.T) {
	for name, mutate := range map[string]func(*domain.CatalogSupersessionResult){
		"command":    func(r *domain.CatalogSupersessionResult) { r.Command = domain.CatalogCommandID{} },
		"content":    func(r *domain.CatalogSupersessionResult) { r.Content = r.New.ContentID(); r.Old = r.New },
		"aggregate":  func(r *domain.CatalogSupersessionResult) { r.AggregateRevision, _ = domain.NewCatalogRevision(8) },
		"graph":      func(r *domain.CatalogSupersessionResult) { r.SupersessionRevision, _ = domain.NewCatalogRevision(8) },
		"old-review": func(r *domain.CatalogSupersessionResult) { r.OldReviewRevision, _ = domain.NewCatalogRevision(8) },
		"new-review": func(r *domain.CatalogSupersessionResult) { r.NewReviewRevision, _ = domain.NewCatalogRevision(8) },
		"order":      func(r *domain.CatalogSupersessionResult) { r.OrderRevision = domain.CatalogRevision{} },
		"support":    func(r *domain.CatalogSupersessionResult) { r.Support = nil },
	} {
		t.Run(name, func(t *testing.T) {
			s, o, c := newSupersessionAppFixture(t)
			o.mutate = mutate
			r, err := s.SupersedeVersion(context.Background(), c)
			if err != ErrRepositoryUnavailable || len(o.store) != 0 || !reflect.DeepEqual(r, domain.CatalogSupersessionResult{}) {
				t.Fatalf("malformed response: %v %+v", err, r)
			}
		})
	}
}

func TestSupersessionApplicationLifecycle(t *testing.T) {
	for _, phase := range []string{"before", "lookup", "write", "commit"} {
		t.Run("cancel-"+phase, func(t *testing.T) {
			s, o, c := newSupersessionAppFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch phase {
			case "before":
				cancel()
			case "lookup":
				o.afterLookup = cancel
			case "write":
				o.afterWrite = cancel
			case "commit":
				o.beforeCommit = cancel
			}
			_, err := s.SupersedeVersion(ctx, c)
			if err != context.Canceled || len(o.store) != 0 {
				t.Fatalf("cancellation: %v", err)
			}
		})
	}
	for _, mode := range []string{"repeat", "skip", "extension"} {
		t.Run(mode, func(t *testing.T) {
			s, o, c := newSupersessionAppFixture(t)
			o.repeat = mode == "repeat"
			o.skip = mode == "skip"
			o.noExtension = mode == "extension"
			_, err := s.SupersedeVersion(context.Background(), c)
			want := ErrRepositoryUnavailable
			if mode == "extension" {
				want = ErrUnsupported
			}
			if err != want || len(o.store) != 0 {
				t.Fatalf("callback: %v", err)
			}
		})
	}
	for _, tc := range []struct{ input, want error }{
		{fmt.Errorf("private path: %w", ErrRepositoryContention), ErrRepositoryContention},
		{errors.New("private SQL path"), ErrRepositoryUnavailable},
		{domain.ErrVersionIdentityConflict, ErrCommandConflict},
		{domain.ErrCatalogObservationConflict, ErrEvidenceConflict},
		{domain.ErrCatalogRevisionConflict, ErrRevisionConflict},
		{ErrUnsupported, ErrUnsupported},
	} {
		s, o, c := newSupersessionAppFixture(t)
		o.lookupError = tc.input
		_, err := s.SupersedeVersion(context.Background(), c)
		if err != tc.want || errors.Unwrap(err) != nil {
			t.Fatalf("sanitizer: %v want %v", err, tc.want)
		}
	}
	s, o, c := newSupersessionAppFixture(t)
	o.finishError = ErrIndeterminate
	o.durable = true
	if _, err := s.SupersedeVersion(context.Background(), c); err != ErrIndeterminate || len(o.store) != 1 {
		t.Fatalf("uncertain: %v", err)
	}
	o.finishError = nil
	o.durable = false
	if _, found, err := s.ReadSupersession(context.Background(), c); err != nil || !found || o.commits != 1 {
		t.Fatalf("uncertain readback: %v", err)
	}
}

func historyFixture(t *testing.T) (SupersessionService, *supersessionOwner, SupersessionQuery) {
	t.Helper()
	s, o, c := newSupersessionAppFixture(t)
	r, err := s.SupersedeVersion(context.Background(), c)
	require(t, err)
	op := domain.CatalogSupersessionCommand{ID: c.ID, Content: c.Content, Old: c.Old, New: c.New, ExpectedAggregate: c.ExpectedAggregate, ExpectedSupersession: c.ExpectedSupersession, ExpectedOldReview: c.ExpectedOldReview, ExpectedNewReview: c.ExpectedNewReview, Source: s.service.source, Evidence: c.Evidence, Support: c.Support, Reason: c.Reason}
	command, err := op.CanonicalEncoding(s.capability)
	require(t, err)
	result, err := r.CanonicalEncoding()
	require(t, err)
	o.page = HistoryPage{Entries: []SupersessionHistoryEntry{{Command: command, Result: result, SourceRevoked: true}}, AggregateRevision: r.AggregateRevision, SupersessionRevision: r.SupersessionRevision, Complete: true, Inspected: 1}
	setHistorySize(&o.page)
	return s, o, SupersessionQuery{Content: c.Content, Page: PageRequest{Limit: 128}}
}
func setHistorySize(p *HistoryPage) {
	// Independently materialize the contract wire layout to check accounting.
	var wire bytes.Buffer
	number := func(n uint64) { _ = binary.Write(&wire, binary.BigEndian, n) }
	value := func(b []byte) { number(uint64(len(b))); wire.Write(b) }
	value([]byte("pi08-version-supersession-page/1"))
	number(p.AggregateRevision.Uint64())
	number(p.SupersessionRevision.Uint64())
	if p.Complete {
		number(1)
	} else {
		number(0)
	}
	value([]byte(p.Next))
	number(p.Inspected)
	number(uint64(len(p.Entries)))
	for _, e := range p.Entries {
		value(e.Command)
		value(e.Result)
		if e.SourceRevoked {
			number(1)
		} else {
			number(0)
		}
	}
	p.EncodedBytes = uint64(wire.Len())
}

func TestSupersessionHistoryContract(t *testing.T) {
	s, o, q := historyFixture(t)
	page, err := s.QuerySupersession(context.Background(), q)
	require(t, err)
	if !page.Entries[0].SourceRevoked {
		t.Fatal("revocation provenance lost")
	}
	page.Entries[0].Command[0] ^= 1
	page.Entries[0].Result[0] ^= 1
	if _, err := s.QuerySupersession(context.Background(), q); err != nil {
		t.Fatalf("response aliases storage: %v", err)
	}
	// A separate live service may read revoked historical provenance.
	other, _, _ := newSupersessionAppFixture(t)
	other.service.transactions = o
	s.service.source.Revoke()
	if _, err := other.QuerySupersession(context.Background(), q); err != nil {
		t.Fatalf("historical source revocation blocks authorized query: %v", err)
	}
	for _, name := range []string{"size", "bytes", "revision", "cursor", "inspected", "trailing", "foreign", "duplicate", "unfiltered-empty"} {
		t.Run(name, func(t *testing.T) {
			s, o, q := historyFixture(t)
			switch name {
			case "size":
				o.page.EncodedBytes++
			case "bytes":
				o.page.EncodedBytes = domain.QueryBytes.Maximum() + 1
			case "revision":
				o.page.AggregateRevision = domain.CatalogRevision{}
			case "cursor":
				o.page.Next = "unexpected"
			case "inspected":
				o.page.Inspected = 0
			case "trailing":
				o.page.Entries[0].Command = append(o.page.Entries[0].Command, 0)
				setHistorySize(&o.page)
			case "foreign":
				q.Content, _ = identity.ParseContentID("cnt_1123456789abcdefghjkmnpqrs")
			case "duplicate":
				o.page.Entries = append(o.page.Entries, o.page.Entries[0])
				setHistorySize(&o.page)
			case "unfiltered-empty":
				o.page.Entries = nil
				setHistorySize(&o.page)
			}
			if _, err := s.QuerySupersession(context.Background(), q); err == nil {
				t.Fatal("malformed page accepted")
			}
		})
	}
	s, o, q = historyFixture(t)
	one, _ := domain.NewCatalogRevision(1)
	o.page = HistoryPage{AggregateRevision: one, SupersessionRevision: one, Complete: true}
	setHistorySize(&o.page)
	if _, err := s.QuerySupersession(context.Background(), q); err != nil {
		t.Fatalf("absent history: %v", err)
	}
	q.Content = domain.CatalogSupersessionResult{}.Content
	q.Version = newFixture(t).versions[0].ID()
	if _, err := s.QuerySupersession(context.Background(), q); err != ErrInvalidInput {
		t.Fatalf("version without content: %v", err)
	}
}

func TestSupersessionHistoryPaginationBounds(t *testing.T) {
	for _, name := range []string{"limit-zero", "limit-one-over", "cursor-without-revision", "cursor-one-over", "stale", "result-mismatch", "oversize-entry", "filtered-unrelated", "filtered-correct", "incomplete"} {
		t.Run(name, func(t *testing.T) {
			s, o, q := historyFixture(t)
			want := ErrInvalidInput
			switch name {
			case "limit-zero":
				q.Page.Limit = 0
			case "limit-one-over":
				q.Page.Limit = 129
				want = ErrLimitExceeded
			case "cursor-without-revision":
				q.Page.Cursor = "cursor"
			case "cursor-one-over":
				q.Page.Cursor = string(make([]byte, 257))
				want = ErrLimitExceeded
			case "stale":
				q.Page.ExpectedRevision, _ = domain.NewCatalogRevision(1)
				want = ErrStaleCursor
			case "result-mismatch":
				r, err := domain.RehydrateCatalogSupersessionResult(o.page.Entries[0].Result)
				require(t, err)
				r.AggregateRevision, _ = domain.NewCatalogRevision(6)
				o.page.Entries[0].Result, err = r.CanonicalEncoding()
				require(t, err)
				setHistorySize(&o.page)
				want = ErrRepositoryUnavailable
			case "oversize-entry":
				o.page.Entries[0].Command = make([]byte, 1<<20)
				setHistorySize(&o.page)
				want = ErrLimitExceeded
			case "filtered-unrelated":
				q.Version, _ = identity.ParseContentVersionID("ver_2123456789abcdefghjkmnpqrs")
				want = ErrRepositoryUnavailable
			case "filtered-correct":
				r, err := domain.RehydrateCatalogSupersessionResult(o.page.Entries[0].Result)
				require(t, err)
				q.Version = r.New.ID()
				want = nil
			case "incomplete":
				o.page.Complete = false
				o.page.Next = "cursor"
				setHistorySize(&o.page)
				want = nil
			}
			if _, err := s.QuerySupersession(context.Background(), q); err != want {
				t.Fatalf("got %v want %v", err, want)
			}
		})
	}
}

func TestSupersessionApplicationIndeterminateDominatesCancellation(t *testing.T) {
	s, o, c := newSupersessionAppFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o.durable = true
	// Driver reports an uncertain commit while cancellation arrives after its
	// final precommit context check; Service must not turn that into rollback.
	o.afterCommit = cancel
	o.finishError = fmt.Errorf("private driver: %w", ErrIndeterminate)
	r, err := s.SupersedeVersion(ctx, c)
	if ctx.Err() != context.Canceled || err != ErrIndeterminate || !reflect.DeepEqual(r, domain.CatalogSupersessionResult{}) {
		t.Fatalf("uncertain: %v", err)
	}
}
