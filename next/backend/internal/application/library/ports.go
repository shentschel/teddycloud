package library

import (
	"context"
	"crypto/rand"
	"math"
	"sync/atomic"
)

// ReadAuthority is installed by the trusted local core, never parsed from a
// request. Issuing another authority cannot create a scope for an existing owner.
type ReadAuthority struct{ generation atomic.Uint64 }
type ReadScope struct {
	authority  *ReadAuthority
	generation uint64
	id         [32]byte
}

func NewReadAuthority() *ReadAuthority { a := &ReadAuthority{}; a.generation.Store(1); return a }
func (a *ReadAuthority) Issue() ReadScope {
	if a == nil {
		return ReadScope{}
	}
	var id [32]byte
	_, _ = rand.Read(id[:])
	return ReadScope{authority: a, generation: a.generation.Load(), id: id}
}
func (a *ReadAuthority) Revoke() {
	if a != nil {
		for {
			current := a.generation.Load()
			if current == math.MaxUint64 {
				return
			}
			if a.generation.CompareAndSwap(current, current+1) {
				return
			}
		}
	}
}
func (s ReadScope) Check() error {
	if s.authority == nil || s.generation == 0 || s.generation == math.MaxUint64 || s.authority.generation.Load() != s.generation {
		return ErrUnauthorized
	}
	return nil
}
func (a *ReadAuthority) Authorize(s ReadScope) error {
	if a == nil || a != s.authority {
		return ErrUnauthorized
	}
	return s.Check()
}
func (s ReadScope) Binding() ([32]byte, uint64) { return s.id, s.generation }

type Preparation struct {
	Ready    bool
	Contents uint64
	Items    uint64
	Bytes    uint64
	Pages    uint64
}
type Reader interface {
	ListLibrary(context.Context, Query, Page) (Result, error)
	Prepare(context.Context) (Preparation, error)
	Advance(context.Context) (Preparation, error)
}
type Owner interface {
	AuthorizeLibrary(ReadScope) error
	WithinLibraryRead(context.Context, ReadScope, func(context.Context, Reader) error) error
}
