package avscan

import "context"

// UnavailableEngine is the production-safe Content Scanner engine until a
// ClamAV/YARA provider is configured. It never reports CLEAN: every scan
// surfaces engine_unavailable so operators can see the gap (ADR 0008 / M-05).
type UnavailableEngine struct{}

func (UnavailableEngine) Scan(context.Context, Block) (Result, error) {
	return Result{}, ErrEngineUnavailable
}

// CleanEngine is retained for tests that need a deterministic CLEAN result.
// Production composition must not use CleanEngine.
type CleanEngine struct{}

func (CleanEngine) Scan(context.Context, Block) (Result, error) {
	return Result{Status: ResultClean}, nil
}
