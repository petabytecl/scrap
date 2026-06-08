package shard

import (
	"errors"
	"math"
	"testing"

	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestRewrapKeyVersion32BoundsCheck(t *testing.T) {
	got, err := rewrapKeyVersion32(math.MaxInt32)
	if err != nil {
		t.Fatalf("rewrapKeyVersion32 valid: %v", err)
	}
	if got != math.MaxInt32 {
		t.Fatalf("rewrapKeyVersion32 valid = %d, want %d", got, math.MaxInt32)
	}

	for _, version := range []int{-1, math.MaxInt32 + 1} {
		_, err := rewrapKeyVersion32(version)
		if !errors.Is(err, storeapi.ErrDataLoss) {
			t.Fatalf("rewrapKeyVersion32(%d) error = %v, want ErrDataLoss", version, err)
		}
	}
}
