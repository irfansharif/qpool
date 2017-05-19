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

func BenchmarkAcquisitions(b *testing.B) {
	qp := qpool.New(5)
	ctx := context.Background()
	for n := 0; n < b.N; n++ {
		qp.Acquire(ctx, 5)
		qp.Release(5)
	}
	qp.Close()
}
