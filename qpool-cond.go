// Copyright 2017, Irfan Sharif
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: Irfan Sharif (irfanmahmoudsharif@gmail.com)
//
// +build cond

package qpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// QPool represents a quota pool, a concurrent data structure for efficient
// resource management or flow control.
type QPool struct {
	sync.Mutex
	quota  int64
	closed bool

	cond     *sync.Cond
	closedCh chan struct{}
}

// qpool.New returns a new instance of a quota pool initialized with the
// specified quota.
func New(v int64) *QPool {
	qp := &QPool{
		quota:    v,
		closedCh: make(chan struct{}),
	}
	qp.cond = sync.NewCond(qp)
	return qp
}

// QPool.Release returns the specified amount back to the quota pool. Release
// is a non blocking operation and is safe for concurrent use. Releasing quota
// that was never acquired (see QPool.Acquire) in the first place increases the
// total quota available across the quota pool.
// Releasing quota back to a closed quota pool will go through but as
// specified in the contract for Close, any subsequent or ongoing Acquire
// operations fail with an error indicating so.
func (qp *QPool) Release(v int64) {
	// NB: We don't acquire a lock when returning quota, we only do so when
	// acquiring. This is correct because concurrent Release-Release operations
	// need not conflict with each other and provided there's enough quota to
	// start with, Acquire-Release operations don't conflict with one another as
	// well. Only Acquire-Acquire operations conflict with one another and is
	// guarded by a mutex (see QPool.Acquire and QPool.aquire).
	// By having to make do without locks here and using sync/atomic,
	// we're able to provide a non-blocking Release operation.
	atomic.AddInt64(&qp.quota, v)

	// We broadcast to all listeners, given acquisitions are serialized via
	// qp.Mutex every goroutine that potentially stands to acquire the quota
	// does so without individual acquisition threads needing to coordinate
	// among one another.
	qp.cond.Broadcast()
}

// QPool.Acquire attempts to acquire the specified amount of quota and blocks
// indefinitely until we have done so. Alternatively if the given context gets
// cancelled or quota pool is closed altogether we return with an error
// specifying so. For a non-nil error, indicating a successful quota
// acquisition of size 'v', the caller is responsible for returning the quota
// back to the pool eventually (see QPool.Release).
// Safe for concurrent use.
func (qp *QPool) Acquire(ctx context.Context, v int64) error {
	// TODO(irfansharif): We may want to minimize allocations here across
	// multiple calls to Acquire, we can do this by using sync.Pool for
	// 'res' channels below and the allocated 'done' flag. Alternatively
	// maintaining a freelist will enable us to do the same preserving
	// allocations across GC runs. This approach would necessitate releasing
	// 'res' and 'done' when returning from Acquire.

	res := make(chan error, 1)
	done := new(bool)
	go func() {
		qp.acquire(v, done, res)
	}()

	select {
	case <-ctx.Done():
		// Given we've seen a context cancellation here, we ensure the quota
		// acquisition goroutine runs to completion. We do so by waiting for a
		// result on the 'res' channel. If we end up acquiring quota, we're
		// sure to return it.
		*done = true

		// Wake up the acquisition goroutine to signal it to stop working.
		qp.cond.Broadcast()

		// We've acquired quota, need to release it back because context was
		// cancelled.
		if err := <-res; err == nil {
			qp.Release(v)
		}

		return ctx.Err()
	case <-qp.closedCh:
		// Given the quota pool was just closed, we ensure the quota
		// acquisition goroutine runs to completion. We do so by waiting for a
		// result on the 'res' channel. If we end up acquiring quota, we're
		// sure to return it.
		*done = true

		// Wake up the acquisition goroutine to signal it to stop working.
		qp.cond.Broadcast()

		// We acquired quota, we need not release it back to the pool given
		// future allocations will fail anyway.
		<-res

		return errors.New("quota pool closed")
	case err := <-res:
		return err
	}
}

func (qp *QPool) acquire(v int64, done *bool, res chan<- error) {
	// sync.Cond has the following usage pattern:
	//
	// // Acquire this monitor's lock.
	// c.L.Lock()
	// // While the condition/predicate/assertion that we are waiting for is not true...
	// for !condition() {
	//     // Wait on this monitor's lock and condition variable.
	//     c.Wait()
	// }
	//
	// // Critical section, we have the lock.
	// ...
	//
	// // Wake another waiting thread if appropriate.
	// if signal() {
	//     c.L.Signal()
	// }
	//
	// // Release this monitor's lock.
	// c.L.Unlock()

	// While the is held, no other go routine is acquiring quota.
	// Acquire this monitor's lock.
	qp.cond.L.Lock()
	// While the condition/predicate/assertion that we are waiting for is not true...
	for !(v <= atomic.LoadInt64(&qp.quota)) {
		// Wait on this monitor's lock and condition variable. If we were
		// signalled it could possibly be because we were closed or the we no
		// longer need the result.
		qp.cond.Wait()
		if *done {
			qp.cond.L.Unlock()
			res <- errors.New("acquisition cancelled")
			return
		}
	}

	// Critical section, we have the lock.
	// Lock held for decrementing.
	atomic.AddInt64(&qp.quota, -v)

	// Release this monitor's lock.
	qp.cond.L.Unlock()
	res <- nil
	return
}

// QPool.Quota returns the amount of quota currently available in the system,
// safe for concurrent use.
func (qp *QPool) Quota() int64 {
	return atomic.LoadInt64(&qp.quota)
}

// QPool.Close closes the quota pool and is safe for concurrent use. Any
// ongoing and subsequent acquisitions fail with an error indicating so.
func (qp *QPool) Close() {
	// TODO(irfansharif): Not reusing the same lock as Acquire would reduce
	// lock contention at the cost of increasing the overall size of the quota
	// pool.
	qp.Lock()
	if !qp.closed {
		close(qp.closedCh)
		qp.closed = true
		qp.cond.Broadcast()
	}
	qp.Unlock()
}
