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
// +build !grpc,!fair,!fifo

package qpool_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/irfansharif/qpool"
)

func TestBlocking(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx, cancel := context.WithCancel(context.Background())
	qp := qpool.New(5)
	qp.Acquire(ctx, 5)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := qp.Acquire(ctx, 4); err != nil {
			wg.Done()
		}
	}()

	time.Sleep(10 * time.Millisecond)

	wg.Add(1)
	go func() {
		if err := qp.Acquire(ctx, 1); err != nil {
			t.Fatal(err)
		}
		qp.Release(1)
		wg.Done()
	}()

	time.Sleep(10 * time.Millisecond)
	qp.Release(1)
	time.Sleep(10 * time.Millisecond)

	cancel()
	wg.Wait()

	quota := qp.Quota()
	if quota != 1 {
		t.Fatalf("expected quota: 5, got: %d", quota)
	}
	qp.Close()
}
