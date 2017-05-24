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

package qpool_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/irfansharif/qpool"
)

func TestBasic(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var wg sync.WaitGroup
	ctx := context.Background()
	qp := qpool.New(5)
	qp.Acquire(ctx, 3)

	wg.Add(1)
	go func() {
		qp.Acquire(ctx, 1)
		go func() {
			qp.Release(1)
		}()
		qp.Acquire(ctx, 3)
		qp.Release(3)
		wg.Done()
	}()

	time.Sleep(10 * time.Millisecond)
	qp.Release(3)
	time.Sleep(10 * time.Millisecond)
	wg.Wait()
	quota := qp.Quota()
	if quota != 5 {
		t.Fatalf("expected quota: 5, got: %d", quota)
	}
	qp.Close()
}

func TestBasicContextCancellation(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx, cancel := context.WithCancel(context.Background())
	qp := qpool.New(5)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for {
			if err := qp.Acquire(ctx, 5); err != nil {
				wg.Done()
				break
			}

			qp.Release(5)
		}
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	wg.Wait()

	quota := qp.Quota()
	if quota != 5 {
		t.Fatalf("expected quota: 5, got: %d", quota)
	}
	qp.Close()
}

func TestContextCancellation(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx, cancel := context.WithCancel(context.Background())
	qp := qpool.New(5)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for {
			if err := qp.Acquire(ctx, 3); err != nil {
				wg.Done()
				break
			}

			qp.Release(3)
		}
	}()

	wg.Add(1)
	go func() {
		for {
			if err := qp.Acquire(ctx, 1); err != nil {
				wg.Done()
				break
			}

			qp.Release(1)
		}
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	wg.Wait()
	time.Sleep(10 * time.Millisecond)
	quota := qp.Quota()
	if quota != 5 {
		t.Fatalf("expected quota: 5, got: %d", quota)
	}
	qp.Close()
}

func TestClose(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx := context.Background()
	qp := qpool.New(5)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for {
			if err := qp.Acquire(ctx, 3); err != nil {
				wg.Done()
				break
			}
			qp.Release(3)
		}
	}()

	wg.Add(1)
	go func() {
		for {
			if err := qp.Acquire(ctx, 1); err != nil {
				wg.Done()
				break
			}
			qp.Release(1)
		}
	}()

	time.Sleep(10 * time.Millisecond)
	qp.Close()
	wg.Wait()

	if err := qp.Acquire(ctx, 1); err == nil {
		t.Fatalf("acquired from closed quota pool")
	}
	qp.Close()
}

func TestCancelledAcquisitions(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	qp := qpool.New(5)
	// For unflakiness, any channel can get selected.
	if err := qp.Acquire(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	// qp.Release(5)

	for i := 0; i < 5; i++ {
		if err := qp.Acquire(ctx, 5); err == nil {
			t.Fatal("expected context canceled error")
		}
	}

	qp.Release(5)
	ch := make(chan error)
	go func() {
		if err := qp.Acquire(context.Background(), 5); err != nil {
			ch <- err
		}
		ch <- nil

	}()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("acquisition didn't go through")
	}
}

func TestZero(t *testing.T) {
	defer leaktest.AfterTest(t)()

	qp := qpool.New(5)
	if err := qp.Acquire(context.Background(), 5); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := qp.Acquire(context.Background(), 5); err != nil {
			t.Fatal(err)
		}
		wg.Done()
	}()

	qp.Release(5)
	qp.Release(0)
	wg.Wait()
	qp.Release(5)
	qp.Release(0)
	if err := qp.Acquire(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if err := qp.Acquire(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkAcquisitions(b *testing.B) {
	qp := qpool.New(5)
	ctx := context.Background()
	for n := 0; n < b.N; n++ {
		qp.Acquire(ctx, 5)
		qp.Release(5)
	}
	qp.Close()
}
