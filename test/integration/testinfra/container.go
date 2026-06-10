package testinfra

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

const containerShutdownTimeout = 30 * time.Second

func CleanupContainer(t testing.TB, container testcontainers.Container) {
	t.Helper()
	t.Cleanup(func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), containerShutdownTimeout)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			t.Errorf("terminate testcontainer: %v", err)
		}
	})
}
