// Copyright 2017, Irfan Sharif.
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
// Author: Irfan Sharif (irfansharif@cockroachlabs.com)
//
// +build cockroach

package qpool

import (
	"errors"
	"sync"

	"golang.org/x/net/context"
)

type QPool struct {
	sync.Mutex

	q      int64
	max    int64
	closed bool
	cond   *sync.Cond
}

// newQuotaPool returns a new instance of a quota pool initialized with the
// specified quota. The quota pool is capped at this amount.
func New(v int64) *QPool {
	qp := &QPool{
		q:   v,
		max: v,
	}
	qp.cond = sync.NewCond(qp)
	return qp
}

// add returns the specified amount back to the quota pool and is a blocking
// call. We let adds go through on a closed quota pool given subsequent
// acquisitions will not succeed. Safe for concurrent use.
func (qp *QPool) Release(q int64) {
	qp.Lock()
	qp.q += q
	if qp.q > qp.max {
		qp.q = qp.max
	}
	qp.cond.Broadcast()
	qp.Unlock()
}

// acquire attempts to acquire the specified amount of quota and blocks
// indefinitely until we have done so. Alternatively if the given context gets
// cancelled or quota pool is closed altogether we return with an error
// specifying so. The lack of an error indicates a successful quota
// acquisition, the caller is responsible for returning the quota back to the
// pool eventually (see QPool.add). Safe for concurrent use.
func (qp *QPool) Acquire(ctx context.Context, v int64) error {
	res := make(chan error)
	go func() {
		qp.acquireInternal(v, ctx.Done(), res)
	}()

	for {
		select {
		case <-ctx.Done():
			// Given we've seen a context cancellation here, we ensure the quota
			// acquisition goroutine runs to completion. We do so by waiting for a
			// result on the 'res' channel. If we end up acquiring quota, we're
			// sure to return it.

			// Wake up the acquisition goroutine to signal it to stop working.
			qp.cond.Broadcast()

			// We've acquired quota, need to release it back because context was
			// cancelled.
			if err := <-res; err == nil {
				qp.Release(v)
			}

			return ctx.Err()
		case err := <-res:
			return err
		}
	}
}

func (qp *QPool) acquireInternal(v int64, done <-chan struct{}, res chan<- error) {
	qp.Lock()

	for !(v <= qp.q) {
		qp.cond.Wait()
		// If we were signalled it could possibly be because quota was just
		// added to the pool which we check in the next loop iteration.
		// Alternatively we no longer need the result.
		select {
		case <-done:
			qp.Unlock()
			res <- errors.New("acquisition cancelled")
			return
		default:
		}

		if qp.closed {
			qp.Unlock()
			res <- errors.New("quota pool closed")
			return
		}
	}

	// Critical section, we have the lock. If the pool was closed, we fail with
	// an error indicating so.
	if qp.closed {
		qp.Unlock()
		res <- errors.New("quota pool closed")
		return
	}

	// While the lock is held, no other go routine is acquiring/decrementing quota.
	qp.q -= v
	qp.Unlock()
	res <- nil
}

func (qp *QPool) Quota() int64 {
	qp.Lock()
	defer qp.Unlock()
	return qp.q
}

// close closes the quota pool and is safe for concurrent use. Any
// ongoing and subsequent acquisitions fail with an error indicating so.
func (qp *QPool) Close() {
	qp.Lock()
	if !qp.closed {
		qp.closed = true
		qp.cond.Broadcast()
	}
	qp.Unlock()
}
