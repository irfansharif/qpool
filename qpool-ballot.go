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
// +build ballot
//
// Building with '-tags ballot' will build the version of QPool using only
// channels. The general idea is that there is a notification channel all
// ongoing acquisitions are listening in on. Any quota Release notifies an
// arbitrary listener on the channel, the listener deducts quota if possible
// and notifies the next goroutine waiting for acquisition (if any) if there's
// still quota remaining. Additionally if there isn't enough quota available,
// we wait until we've notified another goroutine (this avoids busy waiting
// lest this goroutine notifies itself). If in the interim we receive another
// notification, it must've been so that there was new quota added to the
// system so we try again.
// NOTE: This is done so using goto.

package qpool

import (
	"context"
	"errors"
	"sync"
	"time"
)

// QPool represents a quota pool, a concurrent data structure for efficient
// resource management or flow control.
type QPool struct {
	sync.Mutex

	quota    int64
	closed   bool
	notifyCh chan struct{}
	closedCh chan struct{}
}

// qpool.New returns a new instance of a quota pool initialized with the
// specified quota.
func New(q int64) *QPool {
	qp := &QPool{
		notifyCh: make(chan struct{}),
		closedCh: make(chan struct{}),
		quota:    q,
	}
	return qp
}

// QPool.Release returns the specified amount back to the quota pool and is
// safe for concurrent use. Releasing quota that was never acquired (see
// QPool.Acquire) in the first place increases the total quota available across
// the quota pool.
// Releasing quota back to a closed quota pool will go through but as
// specified in the contract for Close, any subsequent or ongoing Acquire
// operations fail with an error indicating so.
func (qp *QPool) Release(v int64) {
	qp.Lock()
	qp.quota += v

	select {
	case qp.notifyCh <- struct{}{}:
	default:
	}
	qp.Unlock()
}

// QPool.Acquire attempts to acquire the specified amount of quota and blocks
// indefinitely until we have done so. Alternatively if the given context gets
// cancelled or quota pool is closed altogether we return with an error
// specifying so. For a non-nil error, indicating a successful quota
// acquisition of size 'v', the caller is responsible for returning the quota
// back to the pool eventually (see QPool.Release).
// Safe for concurrent use.
func (qp *QPool) Acquire(ctx context.Context, v int64) error {
	now := time.NewTimer(0).C
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-qp.closedCh:
			return errors.New("quota pool closed")
		case <-now:
			qp.Lock()
			if qp.closed {
				qp.Unlock()
				return errors.New("quota pool closed")
			}
			if v <= qp.quota {
				qp.quota -= v
				qp.Unlock()
				return nil
			}
			qp.Unlock()
		case <-qp.notifyCh:
		L:
			qp.Lock()
			if qp.quota < v {
				qp.Unlock()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-qp.closedCh:
					return errors.New("quota pool closed")
				case qp.notifyCh <- struct{}{}:
					continue
				case <-qp.notifyCh:
					goto L
				}
			}
			qp.quota -= v
			if qp.quota != 0 {
				select {
				case qp.notifyCh <- struct{}{}:
				default:
				}
			}
			qp.Unlock()
			return nil
		}
	}
}

// QPool.Quota returns the amount of quota currently available in the system,
// safe for concurrent use.
func (qp *QPool) Quota() int64 {
	qp.Lock()
	defer qp.Unlock()
	return qp.quota
}

// QPool.Close closes the quota pool and is safe for concurrent use. Any
// ongoing and subsequent acquisitions fail with an error indicating so.
func (qp *QPool) Close() {
	qp.Lock()
	if !qp.closed {
		qp.closed = true
		close(qp.closedCh)
	}
	qp.Unlock()
}
