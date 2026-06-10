//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"
)

const integrationOperationTimeout = 2 * time.Minute

func integrationTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), integrationOperationTimeout)
	t.Cleanup(cancel)
	return ctx
}
