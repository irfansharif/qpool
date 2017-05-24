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
// +build grpc
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

	// We use a channel to 'park' our quota value for easier composition with
	// context cancellation and leadership changes (see QPool.acquire).
	//
	// NB: A value of '0' is never allowed to be parked in the
	// channel, the lack of quota is represented by an empty channel. Quota
	// additions push a value into the channel whereas acquisitions wait on the
	// channel itself.
	quota  chan int64
	ballot chan struct{}
	done   chan struct{}
	closed bool
}

// qpool.New returns a new quota pool initialized with a given quota,
// qpool.New(0) disallowed.
func New(q int64) *QPool {
	if q == 0 {
		panic("qpool.New(0) disallowed")
	}
	qp := &QPool{
		quota:  make(chan int64, 1),
		ballot: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	qp.quota <- q
	qp.ballot <- struct{}{}
	return qp
}

// Release adds the specified quota back to the pool. Safe for concurrent use.
func (qp *QPool) Release(v int64) {
	if v == 0 {
		return
	}

	qp.Lock()
	select {
	case q := <-qp.quota:
		v += q
	default:
	}
	qp.quota <- v
	qp.Unlock()
}

// Acquire acquires a single unit of quota from the pool. On success, nil is
// returned and the caller must call add(1) or otherwise arrange for the quota
// to be returned to the pool. Safe for concurrent use.
func (qp *QPool) Acquire(ctx context.Context, v int64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-qp.done:
		return errors.New("quota pool no longer in use")
	case <-qp.ballot:
		// break
	}

	var acquired int64
	for acquired < v {
		select {
		case <-ctx.Done():
			if acquired > 0 {
				qp.Release(acquired)
			}
			qp.ballot <- struct{}{}
			return ctx.Err()
		case n := <-qp.quota:
			acquired += n
		case <-qp.done:
			if acquired > 0 {
				qp.Release(acquired)
			}
			qp.ballot <- struct{}{} // Not necessary
			return errors.New("quota pool no longer in use")
		}
	}
	if acquired > v {
		qp.Release(acquired - v)
	}
	qp.ballot <- struct{}{}
	return nil
}

// QPool.Quota returns the amount of quota currently available in the system,
// safe for concurrent use.
func (qp *QPool) Quota() int64 {
	qp.Lock()
	defer qp.Unlock()

	q := <-qp.quota
	qp.quota <- q
	return q
}

// QPool.Close closes the quota pool and is safe for concurrent use. Any
// ongoing and subsequent acquisitions fail with an error indicating so.
func (qp *QPool) Close() {
	qp.Lock()
	if !qp.closed {
		qp.closed = true
		close(qp.done)
	}
	qp.Unlock()
}
