// Package admission provides a process-wide byte-weighted concurrency budget
// shared by Document write, peer transfer, and restore paths (ADR 0036 / H-15).
package admission

import (
	"context"
	"fmt"
	"sync"

	storeapi "github.com/petabytecl/scrap/internal/store"
)

// DefaultCapacityBytes sizes the shared buffer budget for the shipped 512 MiB
// pod: enough for a few concurrent max Documents without stacking whole-Block
// copies on top of the Go heap and encryption working set.
const DefaultCapacityBytes int64 = 256 << 20 // 256 MiB

// Budget is a process-wide byte-weighted semaphore.
type Budget struct {
	mu       sync.Mutex
	cond     *sync.Cond
	capacity int64
	used     int64
}

// Process is the shared budget used by scrapd production paths.
var Process = New(DefaultCapacityBytes)

// New returns a Budget with the given capacity in bytes.
func New(capacity int64) *Budget {
	if capacity <= 0 {
		capacity = DefaultCapacityBytes
	}
	b := &Budget{capacity: capacity}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Acquire reserves n bytes. n is clamped to capacity so a single max Document
// can always proceed when the budget is otherwise idle.
func (b *Budget) Acquire(ctx context.Context, n int64) error {
	if n <= 0 {
		return nil
	}
	if n > b.capacity {
		n = b.capacity
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			b.cond.Broadcast()
			b.mu.Unlock()
		case <-done:
		}
	}()

	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used+n > b.capacity {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: byte admission: %w", storeapi.ErrResourceExhausted, err)
		}
		b.cond.Wait()
	}
	b.used += n
	return nil
}

// Release returns n bytes to the budget.
func (b *Budget) Release(n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.capacity {
		n = b.capacity
	}
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
	b.cond.Broadcast()
}

// Used reports currently reserved bytes (tests).
func (b *Budget) Used() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Capacity reports the configured capacity (tests).
func (b *Budget) Capacity() int64 {
	return b.capacity
}
