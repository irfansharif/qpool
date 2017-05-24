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
// Author: Irfan Sharif (irfanmahmoudsharif@gmail.com)
//
// +build fair
//
// The code below is a simplified version of a similar structure found in
// grpc-go (github.com/grpc/grpc-go/blob/b2fae0c/transport/control.go).
// This version merges ideas from qpool-ballot.go and the implementation in
// grpc-go.

/*
*
* Copyright 2014, Google Inc.
* All rights reserved.
*
* Redistribution and use in source and binary forms, with or without
* modification, are permitted provided that the following conditions are
* met:
*
*     * Redistributions of source code must retain the above copyright
* notice, this list of conditions and the following disclaimer.
*     * Redistributions in binary form must reproduce the above
* copyright notice, this list of conditions and the following disclaimer
* in the documentation and/or other materials provided with the
* distribution.
*     * Neither the name of Google Inc. nor the names of its
* contributors may be used to endorse or promote products derived from
* this software without specific prior written permission.
*
* THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
* "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
* LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
* A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
* OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
* SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
* LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
* DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
* THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
* (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
* OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
*
 */

package qpool

import (
	"errors"

	"github.com/cockroachdb/cockroach/pkg/util/syncutil"
	"golang.org/x/net/context"
)

type QPool struct {
	syncutil.Mutex

	// We service quota acquisitions in a first come, first serve basis in
	// order to prevent starvations of large acquisitions from a continuous
	// stream of smaller ones. Acquisitions 'register' themselves for a
	// notification that would indicate they were next in line. This is done by
	// appending to the queue a pointer to a channel they will then wait on.
	// If a thread no longer needs to be notified, i.e. their acquisition
	// context has been cancelled, it is sufficient for them to set their
	// notification channel to nil.
	queue []*chan struct{}

	// We use a channel to 'park' our quota value for easier composition with
	// context cancellation and QPool closing (see QPool.acquire).
	//
	// Quota additions push a value into the channel whereas the acquisition
	// next in line (we use a FIFO queue to prevent starvation) waits on the
	// channel itself.
	quota chan int64
	max   int64

	// Ongoing acquisitions listen on QPool.done which is closed when the quota
	// pool is closed (see QPool.Close)
	done   chan struct{}
	closed bool
}

// qpool.New returns a new quota pool initialized with a given quota, the quota
// is capped at this amount.
func New(q int64) *QPool {
	qp := &QPool{
		quota: make(chan int64, 1),
		done:  make(chan struct{}),
		max:   q,
	}
	qp.quota <- q
	return qp
}

// Release adds the specified quota back to the pool. Safe for concurrent use.
func (qp *QPool) Release(v int64) {
	qp.Lock()
	select {
	case q := <-qp.quota:
		v += q
	default:
	}
	if v > qp.max {
		v = qp.max
	}
	qp.quota <- v
	qp.Unlock()
}

// Acquire acquires a single unit of quota from the pool. On success, nil is
// returned and the caller must call add(1) or otherwise arrange for the quota
// to be returned to the pool. Safe for concurrent use.
func (qp *QPool) Acquire(ctx context.Context, v int64) error {
	notifyCh := make(chan struct{})
	qp.Lock()
	qp.queue = append(qp.queue, &notifyCh)

	// We're first in line, notify ourself immediately.
	if len(qp.queue) == 1 {
		close(notifyCh)
	}
	qp.Unlock()

	select {
	case <-ctx.Done():
		qp.Lock()
		// We no longer need to notified but we need to be careful and check
		// whether or not we're first in queue. If so, we need to notify the
		// next acquisition thread and clean up the waiting queue while doing
		// so.
		// Otherwise we simply 'unregister' ourselves from the queue by setting
		// the channel to nil. This is what is checked when a thread wishes to
		// notify the next in line.  This has to be done under lock so to not
		// change what the pointer in the queue is pointing to whilst another
		// thread is iterating over those pointers.

		if qp.queue[0] == &notifyCh {
			// We're at the head of the queue. We traverse until we find a
			// thread waiting to be notified, truncate our queue so to ensure
			// the said thread is at the beginning, then proceed to notify the thread.
			// If we determine there are non threads waiting, we simply
			// truncate our queue to reflect this.
			for i, ch := range qp.queue[1:] {
				if *ch != nil {
					qp.queue = qp.queue[i+1:]
					close(*ch)
					qp.Unlock()
					return ctx.Err()
				}
			}
			qp.queue = qp.queue[:0]
		}

		notifyCh = nil
		qp.Unlock()
		return ctx.Err()
	case <-qp.done:
		// We don't need to 'unregister' ourselves by setting notifyCh to
		// nil as in the case when the context is cancelled. By not setting
		// notifyCh = nil we in fact 'suck' up the notification (if any)
		// and all acquisitions threads further along the queue definitely
		// receive on qp.done.
		return errors.New("quota pool no longer in use")
	case <-notifyCh:
	}

	var acquired int64
	for acquired < v {
		select {
		case <-ctx.Done():
			if acquired > 0 {
				qp.Release(acquired)
			}
			qp.Lock()
			for i, ch := range qp.queue[1:] {
				if *ch != nil {
					qp.queue = qp.queue[i+1:]
					close(*ch)
					qp.Unlock()
					return ctx.Err()
				}
			}
			qp.queue = qp.queue[:0]
			qp.Unlock()
			return ctx.Err()
		case <-qp.done:
			// We don't need to release quota back as all ongoing and
			// subsequent acquisitions will fail with an error indicating that
			// the pool is now closed.
			return errors.New("quota pool no longer in use")
		case q := <-qp.quota:
			acquired += q
		}
	}
	if acquired > v {
		qp.Release(acquired - v)
	}

	qp.Lock()
	for i, ch := range qp.queue[1:] {
		if *ch != nil {
			qp.queue = qp.queue[i+1:]
			close(*ch)
			qp.Unlock()
			return nil
		}
	}
	qp.queue = qp.queue[:0]
	qp.Unlock()
	return nil
}

// QPool.Quota will correctly report the amount of quota available in the
// system only if there are no ongoing acquisition threads. If there are, other
// threads this call may block and it's return a value that is up to 'v' less
// than currently available quota where 'v' is the value the acquisition thread
// next in line is attempting to acquire.
func (qp *QPool) Quota() int64 {
	qp.Lock()
	defer qp.Unlock()

	q := <-qp.quota
	qp.quota <- q
	return q
}

// QPool.Close closes the quota pool and is safe for concurrent use.
//
// NB: This is best effort, we try to fail all ongoing and subsequent
// acquisitions with an error indicating so but acquisitions may still seep
// through. This is due to go's behaviour where if we're waiting on multiple
// channels in a select statement, if there are values ready for more than one
// channel the runtime will pseudo-randomly choose one to proceed.
func (qp *QPool) Close() {
	qp.Lock()
	if !qp.closed {
		qp.closed = true
		close(qp.done)
	}
	qp.Unlock()
}
