package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

func TestSingleConnectionPoolWaitHonorsDeadlineAndRecovers(t *testing.T) {
	for _, test := range []struct {
		name   string
		commit bool
	}{
		{name: "holder rollback", commit: false},
		{name: "holder commit", commit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := openApplicationDatabase(t)
			content := testContent(t, test.name, true)
			holderRollback := errors.New("holder rollback")
			holderEntered := make(chan struct{})
			releaseHolder := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseHolder) }) }
			t.Cleanup(release)
			holderDone := make(chan error, 1)

			go func() {
				holderDone <- database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
					if err := repository.Save(t.Context(), content); err != nil {
						return err
					}
					close(holderEntered)
					<-releaseHolder
					if !test.commit {
						return holderRollback
					}
					return nil
				})
			}()
			waitForWriteLock(t, holderEntered, holderDone)

			deadlineContext := newControlledDeadlineContext(t.Context())
			t.Cleanup(deadlineContext.expire)
			waitCountBefore := database.db.Stats().WaitCount
			var callbackCalls atomic.Int32
			waiterDone := make(chan error, 1)
			go func() {
				waiterDone <- database.WithinTransaction(deadlineContext, func(applicationcatalog.ContentRepository) error {
					callbackCalls.Add(1)
					return nil
				})
			}()
			waitForConnectionWaiter(t, database, waitCountBefore+1)
			if got := callbackCalls.Load(); got != 0 {
				t.Fatalf("waiter callback calls before deadline = %d, want 0", got)
			}

			deadlineContext.expire()
			select {
			case err := <-waiterDone:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("pool wait error = %v, want %v", err, context.DeadlineExceeded)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("pool waiter did not return after deadline")
			}
			if got := callbackCalls.Load(); got != 0 {
				t.Fatalf("waiter callback calls after deadline = %d, want 0", got)
			}

			release()
			select {
			case err := <-holderDone:
				if test.commit && err != nil {
					t.Fatalf("commit holder: %v", err)
				}
				if !test.commit && !errors.Is(err, holderRollback) {
					t.Fatalf("rollback holder error = %v, want %v", err, holderRollback)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("holder did not finish after release")
			}

			if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
				got, found, err := repository.FindByID(t.Context(), content.ID())
				if err != nil {
					return err
				}
				if found != test.commit {
					return fmt.Errorf("content found after holder completion = %t, want %t", found, test.commit)
				}
				if found && !reflect.DeepEqual(got, content) {
					return fmt.Errorf("committed holder content = %#v, want %#v", got, content)
				}
				return nil
			}); err != nil {
				t.Fatalf("use pool after holder completion: %v", err)
			}
		})
	}
}

type controlledDeadlineContext struct {
	parent   context.Context
	deadline time.Time
	done     chan struct{}
	once     sync.Once
}

func newControlledDeadlineContext(parent context.Context) *controlledDeadlineContext {
	return &controlledDeadlineContext{
		parent:   parent,
		deadline: time.Now().Add(time.Hour),
		done:     make(chan struct{}),
	}
}

func (ctx *controlledDeadlineContext) Deadline() (time.Time, bool) {
	return ctx.deadline, true
}

func (ctx *controlledDeadlineContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *controlledDeadlineContext) Err() error {
	select {
	case <-ctx.done:
		return context.DeadlineExceeded
	default:
		return ctx.parent.Err()
	}
}

func (ctx *controlledDeadlineContext) Value(key any) any {
	return ctx.parent.Value(key)
}

func (ctx *controlledDeadlineContext) expire() {
	ctx.once.Do(func() { close(ctx.done) })
}
