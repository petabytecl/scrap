package closeutil

import (
	"errors"
	"testing"
)

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}

type typedNilCloser struct{}

func (*typedNilCloser) Close() error {
	panic("typed-nil closer should not be called")
}

func TestIgnoreClosesAndSuppressesError(t *testing.T) {
	closed := false
	Ignore(closerFunc(func() error {
		closed = true
		return errors.New("close failed")
	}))
	if !closed {
		t.Fatal("expected closer to be called")
	}
}

func TestIgnoreSkipsTypedNilCloser(_ *testing.T) {
	var closer *typedNilCloser
	Ignore(closer)
}

func TestLogReportsCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	var gotFormat string
	var gotArgs []any
	Log("resource", func(format string, args ...any) {
		gotFormat = format
		gotArgs = append([]any(nil), args...)
	}, closerFunc(func() error {
		return closeErr
	}))
	if gotFormat != "%s close failed: %v" {
		t.Fatalf("log format = %q, want close failure format", gotFormat)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "resource" {
		t.Fatalf("log args = %#v, want resource and close error", gotArgs)
	}
	gotErr, ok := gotArgs[1].(error)
	if !ok || !errors.Is(gotErr, closeErr) {
		t.Fatalf("log args = %#v, want resource and close error", gotArgs)
	}
}

func TestLogSkipsTypedNilCloser(t *testing.T) {
	var closer *typedNilCloser
	Log("resource", t.Fatalf, closer)
}

func TestLogSuppressesReportWhenLoggerIsNil(t *testing.T) {
	closed := false
	Log("resource", nil, closerFunc(func() error {
		closed = true
		return errors.New("close failed")
	}))
	if !closed {
		t.Fatal("expected closer to be called")
	}
}
