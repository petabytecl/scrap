package testutil

import (
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequireHelpersAcceptPassingConditions(t *testing.T) {
	RequireNoErrorf(t, nil, "nil error")
	RequireErrorIsf(t, io.EOF, io.EOF, "eof")
	RequireEOFf(t, io.EOF, "eof")
	RequireTruef(t, true, "true")
	RequireFalsef(t, false, "false")
	RequireEqualf(t, 7, 7, "equal")
	RequireDeepEqualf(t, []string{"a"}, []string{"a"}, "deep equal")
	value := "value"
	RequireNotNilf(t, &value, "not nil")
	RequireNilf(t, (*string)(nil), "nil pointer")
	RequireCode(t, status.Error(codes.InvalidArgument, "bad request"), codes.InvalidArgument)
}

func TestIsNilRecognizesNilableValues(t *testing.T) {
	if !isNil(nil) {
		t.Fatal("nil interface was not nil")
	}
	values := []any{
		(*string)(nil),
		[]string(nil),
		map[string]string(nil),
		(chan int)(nil),
		(func())(nil),
	}
	for _, value := range values {
		if !isNil(value) {
			t.Fatalf("%T was not nil", value)
		}
	}
	if isNil("not nil") {
		t.Fatal("string value reported nil")
	}
	if isNil(errors.New("not nil")) {
		t.Fatal("error value reported nil")
	}
}
