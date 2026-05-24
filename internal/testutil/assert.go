package testutil

import (
	"errors"
	"io"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func RequireNoErrorf(t testing.TB, err error, format string, args ...any) {
	t.Helper()
	if err == nil {
		return
	}
	failWithErrorf(t, err, format, args...)
}

func RequireErrorIsf(t testing.TB, err, target error, format string, args ...any) {
	t.Helper()
	if errors.Is(err, target) {
		return
	}
	failWithErrorf(t, err, format, args...)
}

func RequireEOFf(t testing.TB, err error, format string, args ...any) {
	t.Helper()
	RequireErrorIsf(t, err, io.EOF, format, args...)
}

func RequireTruef(t testing.TB, value bool, format string, args ...any) {
	t.Helper()
	if !value {
		t.Fatalf(format, args...)
	}
}

func RequireFalsef(t testing.TB, value bool, format string, args ...any) {
	t.Helper()
	if value {
		t.Fatalf(format, args...)
	}
}

func RequireEqualf[T comparable](t testing.TB, got, want T, format string, args ...any) {
	t.Helper()
	if got != want {
		args = append(args, got, want)
		t.Fatalf(format+": got %v, want %v", args...)
	}
}

func RequireDeepEqualf(t testing.TB, got, want any, format string, args ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		args = append(args, got, want)
		t.Fatalf(format+": got %#v, want %#v", args...)
	}
}

func RequireNotNilf(t testing.TB, value any, format string, args ...any) {
	t.Helper()
	if isNil(value) {
		t.Fatalf(format, args...)
	}
}

func RequireNilf(t testing.TB, value any, format string, args ...any) {
	t.Helper()
	if !isNil(value) {
		args = append(args, value)
		t.Fatalf(format+": got %#v", args...)
	}
}

func RequireCode(t testing.TB, err error, code codes.Code) {
	t.Helper()
	if got := status.Code(err); got != code {
		t.Fatalf("code = %s, want %s; err = %v", got, code, err)
	}
}

func failWithErrorf(t testing.TB, err error, format string, args ...any) {
	t.Helper()
	if format == "" {
		t.Fatalf("unexpected error: %v", err)
	}
	args = append(args, err)
	t.Fatalf(format+": %v", args...)
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflectValue := reflect.ValueOf(value)
	switch reflectValue.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflectValue.IsNil()
	default:
		return false
	}
}
