package library

import (
	"context"
	"sync/atomic"
	"time"
)

const OperationTimeout = 10 * time.Second
const TransactionTimeout = 5 * time.Second

type Service struct {
	owner  Owner
	active atomic.Bool
}

func NewService(owner Owner) *Service { return &Service{owner: owner} }
func (s *Service) run(ctx context.Context, scope ReadScope, op func(context.Context, Reader) error) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.owner == nil {
		return ErrUnauthorized
	}
	if err := s.owner.AuthorizeLibrary(scope); err != nil {
		return BoundaryError(ctx, err)
	}
	if !s.active.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer s.active.Store(false)
	call := ctx
	called := false
	err := s.owner.WithinLibraryRead(call, scope, func(c context.Context, r Reader) error {
		if called || r == nil {
			return ErrUnavailable
		}
		called = true
		return op(c, r)
	})
	if err == nil && !called {
		err = ErrUnavailable
	}
	if err == nil {
		err = s.owner.AuthorizeLibrary(scope)
	}
	return BoundaryError(call, err)
}
func (s *Service) ListLibrary(ctx context.Context, q Query, p Page, scope ReadScope) (Result, error) {
	// Authorization precedes all shape/token inspection and all SQL.
	if ctx == nil {
		return Result{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if s == nil || s.owner == nil {
		return Result{}, ErrUnauthorized
	}
	if err := s.owner.AuthorizeLibrary(scope); err != nil {
		return Result{}, BoundaryError(ctx, err)
	}
	q, err := NormalizeQuery(q, p)
	if err != nil {
		return Result{}, err
	}
	var result Result
	err = s.run(ctx, scope, func(c context.Context, r Reader) error {
		page, e := r.ListLibrary(c, q, p)
		if e != nil {
			return e
		}
		if e = ValidateQueryResult(q, p, page); e != nil {
			return e
		}
		result = page
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
func (s *Service) Prepare(ctx context.Context, scope ReadScope) (Preparation, error) {
	var p Preparation
	err := s.run(ctx, scope, func(c context.Context, r Reader) error { var e error; p, e = r.Prepare(c); return e })
	if err != nil {
		return Preparation{}, err
	}
	return p, nil
}
func (s *Service) Advance(ctx context.Context, scope ReadScope) (Preparation, error) {
	var p Preparation
	err := s.run(ctx, scope, func(c context.Context, r Reader) error { var e error; p, e = r.Advance(c); return e })
	if err != nil {
		return Preparation{}, err
	}
	return p, nil
}
