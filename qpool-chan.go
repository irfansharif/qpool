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
// +build chan

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

	quota       int64
	closed      bool
	closedCh    chan struct{}
	listenerChs map[int]chan int64
}

// qpool.New returns a new instance of a quota pool initialized with the
// specified quota.
func New(v int64) *QPool {
	return &QPool{
		quota:       v,
		closedCh:    make(chan struct{}),
		listenerChs: make(map[int]chan int64),
	}
}

// QPool.Release is a blocking call that releases the specified quota back to
// the pool, it is safe for concurrent use. Releasing quota that was never
// acquired (see QPool.Acquire) in the first place increases the total quota
// available across the quota pool.
// Releasing quota back to a closed quota pool will go through but as
// specified in the contract for Close, any subsequent or ongoing Acquire
// operations fail with an error indicating so.
func (qp *QPool) Release(v int64) {
	qp.Lock()
	qp.quota += v
	chs := qp.listenerChs

	for i, listenerCh := range chs {
		select {
		case listenerCh <- qp.quota:
			<-listenerCh
		case <-listenerCh:
			close(listenerCh)
			delete(qp.listenerChs, i)
		}
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
func (qp *QPool) Acquire(ctx context.Context, v int64) (err error) {
	listenerCh := make(chan int64)
	qp.Lock()
	i := len(qp.listenerChs)
	qp.Unlock()

	now := time.NewTimer(0).C
	for {
		select {
		case <-ctx.Done():
			select {
			case listenerCh <- 0:
			default:
			}
			return ctx.Err()
		case <-qp.closedCh:
			select {
			case listenerCh <- 0:
			default:
			}
			return errors.New("quota pool closed")
		case quota := <-listenerCh:
			if v <= quota {
				qp.quota -= v
				delete(qp.listenerChs, i)
				listenerCh <- 0
				return nil
			} else {
				listenerCh <- quota
				continue
			}
		case <-now:
			qp.Lock()
			if v <= qp.quota {
				qp.quota -= v
				close(listenerCh)
				qp.Unlock()
				return nil
			}
			qp.listenerChs[i] = listenerCh
			qp.Unlock()
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
	// TODO(irfansharif): Not reusing the same lock as Acquire would reduce
	// lock contention at the cost of increasing the overall size of the quota
	// pool.
	qp.Lock()
	if !qp.closed {
		close(qp.closedCh)
		qp.closed = true
	}
	qp.Unlock()
}
