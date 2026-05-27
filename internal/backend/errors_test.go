package backend_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/petabytecl/scrap/internal/backend"
)

func TestErrorClassSentinels(t *testing.T) {
	tests := []struct {
		name     string
		sentinel error
		want     backend.Class
	}{
		{name: "throttled", sentinel: backend.ErrThrottled, want: backend.ClassThrottled},
		{name: "transient", sentinel: backend.ErrTransient, want: backend.ClassTransient},
		{name: "auth", sentinel: backend.ErrAuth, want: backend.ClassAuth},
		{name: "not found", sentinel: backend.ErrNotFound, want: backend.ClassNotFound},
		{name: "conflict", sentinel: backend.ErrConflict, want: backend.ClassConflict},
		{name: "corrupt", sentinel: backend.ErrCorrupt, want: backend.ClassCorrupt},
		{name: "permanent", sentinel: backend.ErrPermanent, want: backend.ClassPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("provider detail: %w", tt.sentinel)
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("wrapped error should match sentinel %v", tt.sentinel)
			}
			if got := backend.ErrorClass(err); got != tt.want {
				t.Fatalf("ErrorClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorClassUnclassified(t *testing.T) {
	if got := backend.ErrorClass(nil); got != "" {
		t.Fatalf("nil ErrorClass() = %q, want empty class", got)
	}

	if got := backend.ErrorClass(errors.New("plain provider error")); got != "" {
		t.Fatalf("unclassified ErrorClass() = %q, want empty class", got)
	}
}
