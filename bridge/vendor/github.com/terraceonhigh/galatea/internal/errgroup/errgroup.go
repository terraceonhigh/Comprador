// Package errgroup is a minimal, self-contained equivalent of
// golang.org/x/sync/errgroup, providing just the API the vendored
// rpcserver uses (WithContext, Go, Wait). Reimplemented here rather than
// taking the external module so Galatea stays dependency-free (Milestone
// A, AC7). Semantics match x/sync: the first non-nil error is remembered,
// the derived context is cancelled on first error, and Wait returns it.
package errgroup

import (
	"context"
	"sync"
)

// A Group is a collection of goroutines working on subtasks that are part
// of the same overall task.
type Group struct {
	cancel  func()
	wg      sync.WaitGroup
	errOnce sync.Once
	err     error
}

// WithContext returns a new Group and an associated Context derived from
// ctx. The context is cancelled the first time a function passed to Go
// returns a non-nil error, or the first time Wait returns.
func WithContext(ctx context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{cancel: cancel}, ctx
}

// Go runs f in a new goroutine. The first call to return a non-nil error
// cancels the group's context; that error is returned by Wait.
func (g *Group) Go(f func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := f(); err != nil {
			g.errOnce.Do(func() {
				g.err = err
				if g.cancel != nil {
					g.cancel()
				}
			})
		}
	}()
}

// Wait blocks until all goroutines from Go have returned, then returns the
// first non-nil error (if any).
func (g *Group) Wait() error {
	g.wg.Wait()
	if g.cancel != nil {
		g.cancel()
	}
	return g.err
}
