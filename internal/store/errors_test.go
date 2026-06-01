package store_test

import (
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/store"
)

func TestNotLeaderErrorMessage(t *testing.T) {
	err := &store.NotLeaderError{LeaderAddr: "scrapd-2.scrap-headless.ns.svc:9090"}
	want := "not shard leader; leader at scrapd-2.scrap-headless.ns.svc:9090"
	if err.Error() != want {
		t.Fatalf("Error(): got %q, want %q", err.Error(), want)
	}
}

func TestNotLeaderErrorWithoutAddr(t *testing.T) {
	err := &store.NotLeaderError{}
	want := "not shard leader; leader unknown"
	if err.Error() != want {
		t.Fatalf("Error(): got %q, want %q", err.Error(), want)
	}
}

func TestNotLeaderErrorAsInterface(t *testing.T) {
	var err error = &store.NotLeaderError{LeaderAddr: "localhost:9090"}

	var nle *store.NotLeaderError
	if !errors.As(err, &nle) {
		t.Fatal("errors.As should match NotLeaderError")
	}
	if nle.LeaderAddr != "localhost:9090" {
		t.Fatalf("LeaderAddr: got %q", nle.LeaderAddr)
	}
}

func TestNotLeaderErrorIsNotSentinel(t *testing.T) {
	err := &store.NotLeaderError{LeaderAddr: "addr"}
	if errors.Is(err, store.ErrAlreadyExists) {
		t.Fatal("NotLeaderError should not match ErrAlreadyExists")
	}
	if errors.Is(err, store.ErrNotFound) {
		t.Fatal("NotLeaderError should not match ErrNotFound")
	}
}

func TestUnavailableErrorReason(t *testing.T) {
	err := store.NewUnavailable(store.UnavailableReasonBackendRestoreUnavailable, "restore paused")

	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatal("UnavailableError should match ErrUnavailable")
	}
	reason, ok := store.UnavailableReason(err)
	if !ok || reason != store.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("UnavailableReason = %q/%v, want backend_restore_unavailable", reason, ok)
	}
	if got, want := err.Error(), "temporarily unavailable: restore paused"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestUnavailableErrorWithoutMessage(t *testing.T) {
	err := store.NewUnavailable(store.UnavailableReasonBackendRestoreUnavailable, "")

	if got, want := err.Error(), store.ErrUnavailable.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestUnavailableReasonMissing(t *testing.T) {
	if reason, ok := store.UnavailableReason(store.ErrUnavailable); ok || reason != "" {
		t.Fatalf("UnavailableReason = %q/%v, want empty/false", reason, ok)
	}
}
