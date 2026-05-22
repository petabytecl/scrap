package backendupload

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/metastore"
)

func TestRunnerRunsImmediatelyAndOnTicks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	observed := make(chan RunResult, 2)
	reportErrors := make(chan error, 1)
	intent := testUploadIntent("block-1")
	store := openTestBackendStore(t)

	done := make(chan error, 1)
	go func() {
		done <- Runner{
			Processor: Processor{
				Uploader: Uploader{Backend: store, Source: staticBlockSource{body: []byte("block")}, Index: staticBlockIndexSource{body: []byte("index")}},
				Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
				Updater:  &recordingIntentStateUpdater{},
			},
			Report: func(result RunResult, err error) {
				if err != nil {
					reportErrors <- err
					return
				}
				observed <- result
			},
		}.run(ctx, ticks)
	}()

	requireNoRunError(t, reportErrors)
	first := receiveRunResult(t, observed)
	if first.Scanned != 1 || first.Uploaded != 1 {
		t.Fatalf("first result = %#v, want one upload", first)
	}
	ticks <- time.Unix(1, 0)
	requireNoRunError(t, reportErrors)
	second := receiveRunResult(t, observed)
	if second.Scanned != 1 || second.Uploaded != 1 {
		t.Fatalf("second result = %#v, want one upload", second)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runner returned error: %v", err)
	}
}

func TestRunnerReportsErrorsAndContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	observed := make(chan error, 2)
	listerErr := errors.New("metastore unavailable")
	store := openTestBackendStore(t)

	done := make(chan error, 1)
	go func() {
		done <- Runner{
			Processor: Processor{
				Uploader: Uploader{Backend: store, Source: staticBlockSource{body: []byte("block")}, Index: staticBlockIndexSource{body: []byte("index")}},
				Intents:  staticIntentLister{err: listerErr},
				Updater:  &recordingIntentStateUpdater{},
			},
			Report: func(_ RunResult, err error) {
				observed <- err
			},
		}.run(ctx, ticks)
	}()

	if err := receiveRunError(t, observed); !errors.Is(err, listerErr) {
		t.Fatalf("first error = %v, want %v", err, listerErr)
	}
	ticks <- time.Unix(1, 0)
	if err := receiveRunError(t, observed); !errors.Is(err, listerErr) {
		t.Fatalf("second error = %v, want %v", err, listerErr)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runner returned error: %v", err)
	}
}

func TestRunnerUsesRunOnceFunc(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	observed := make(chan RunResult, 1)
	calls := 0

	done := make(chan error, 1)
	go func() {
		done <- Runner{
			RunOnceFunc: func(context.Context) (RunResult, error) {
				calls++
				return RunResult{Scanned: calls}, nil
			},
			Report: func(result RunResult, _ error) {
				observed <- result
			},
		}.run(ctx, ticks)
	}()

	first := receiveRunResult(t, observed)
	if first.Scanned != 1 {
		t.Fatalf("first result = %#v, want callback result", first)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runner returned error: %v", err)
	}
}

func TestRunnerRejectsNonPositiveInterval(t *testing.T) {
	err := Runner{}.Run(context.Background())
	if err == nil {
		t.Fatal("expected interval validation error")
	}
}

func requireNoRunError(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("runner reported error: %v", err)
	default:
	}
}

func receiveRunResult(t *testing.T, ch <-chan RunResult) RunResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run result")
		return RunResult{}
	}
}

func receiveRunError(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run error")
		return nil
	}
}
