# qpool: Quota Pool implementation in Go

[![GoDoc](https://godoc.org/github.com/irfansharif/qpool?status.svg)](https://godoc.org/github.com/irfansharif/qpool)
[![Build Status](https://travis-ci.org/irfansharif/qpool.svg?branch=master)](https://travis-ci.org/irfansharif/qpool)

Package `qpool` is an efficient implementation of a quota pool designed for
concurrent use. Quota pools, including the one here, can be used for flow
control or admission control purposes. This implementation however differs in
that it allows for arbitrary quota acquisitions thus allowing for finer grained
resource management. Additionally for blocking calls `qpool` allows for
asynchronous context cancellations by internally composing locks with channels.

## API

```go
import "github.com/irfansharif/qpool"
```

```go
// QPool represents a quota pool, a concurrent data structure for efficient
// resource management or flow control.
type QPool struct {
	sync.Mutex
}

// New returns a new instance of a quota pool initialized with the
// specified quota.
func New(v int64) *QPool { ... }

// QPool.Acquire attempts to acquire the specified amount of quota and blocks
// indefinitely until we have done so. Alternatively if the given context gets
// cancelled or quota pool is closed altogether we return with an error specifying
// so. For a non-nil error, indicating a successful quota acquisition of size 'v',
// the caller is responsible for returning the quota back to the pool eventually
// (see QPool.Return). Safe for concurrent use.
func (qp *QPool) Acquire(ctx context.Context, v int64) error { ... }

// QPool.Close closes the quota pool and is safe for concurrent use. Any ongoing
// and subsequent acquisitions fail with an error indicating so.
func (qp *QPool) Close() { ... }

// QPool.Quota returns the amount of quota currently available in the system, safe
// for concurrent use.
func (qp *QPool) Quota() int64 { ... }

// QPool.Release returns the specified amount back to the quota pool. Release is a
// non blocking operation and is safe for concurrent use. Releasing quota that was
// never acquired (see QPool.Acquire) in the first place increases the total quota
// available across the quota pool. Releasing quota back to a closed quota pool
// will go through but as specified in the contract for Close, any subsequent or
// ongoing Acquire operations fail with an error indicating so.
func (qp *QPool) Release(v int64) { ... }
```

## Testing
It's hard to exhaustively test something as subtle as this and while it's not
really a proof for correctness, I've used this (or rather a very slight
variation of this) in
[cockroachdb/cockroach](https://github.com/cockroachdb/cockroach) at our
[storage
layer](https://github.com/cockroachdb/cockroach/tree/master/pkg/storage) with
no issues.

## Author
Irfan Sharif: <irfanmahmoudsharif@gmail.com>, [@irfansharifm](https://twitter.com/irfansharifm)

## License
qpool source code is available under the [Apache License, Version 2.0](/LICENSE).
