package backendupload

import (
	"context"
	"errors"
	"time"
)

type Runner struct {
	Processor   Processor
	RunOnceFunc func(context.Context) (RunResult, error)
	Interval    time.Duration
	Report      func(RunResult, error)
}

func (r Runner) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		return errors.New("backendupload: runner interval must be positive")
	}
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	return r.run(ctx, ticker.C)
}

func (r Runner) run(ctx context.Context, ticks <-chan time.Time) error {
	for {
		result, err := r.runOnce(ctx)
		if err != nil && ctx.Err() != nil {
			//nolint:nilerr // Context cancellation is the normal shutdown path for this runner.
			return nil
		}
		if r.Report != nil {
			r.Report(result, err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
		}
	}
}

func (r Runner) runOnce(ctx context.Context) (RunResult, error) {
	if r.RunOnceFunc != nil {
		return r.RunOnceFunc(ctx)
	}
	return r.Processor.RunOnce(ctx)
}
