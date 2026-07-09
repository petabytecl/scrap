package admission_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/admission"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestBudgetAcquireRelease(t *testing.T) {
	b := admission.New(100)
	if err := b.Acquire(context.Background(), 60); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := b.Used(); got != 60 {
		t.Fatalf("Used = %d, want 60", got)
	}
	b.Release(60)
	if got := b.Used(); got != 0 {
		t.Fatalf("Used after Release = %d, want 0", got)
	}
}

func TestBudgetBlocksUntilCapacity(t *testing.T) {
	b := admission.New(50)
	if err := b.Acquire(context.Background(), 50); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := b.Acquire(ctx, 1)
	if !errors.Is(err, storeapi.ErrResourceExhausted) {
		t.Fatalf("Acquire = %v, want ErrResourceExhausted", err)
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		b.Release(50)
		close(released)
	}()
	if err := b.Acquire(context.Background(), 40); err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	<-released
	b.Release(40)
}

func TestBudgetClampsOversizedReservation(t *testing.T) {
	b := admission.New(100)
	if err := b.Acquire(context.Background(), 10_000); err != nil {
		t.Fatalf("Acquire oversized: %v", err)
	}
	if got := b.Used(); got != 100 {
		t.Fatalf("Used = %d, want capacity 100", got)
	}
	b.Release(10_000)
	if got := b.Used(); got != 0 {
		t.Fatalf("Used after oversized Release = %d, want 0", got)
	}
}
