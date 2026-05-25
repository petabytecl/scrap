package appstatus

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewBuildsStatusErrorWithDetails(t *testing.T) {
	detail := timestamppb.Now()
	status := requireStatusError(t, New(CodeDataLoss, "stored checksum mismatch", WithDetails(detail)), CodeDataLoss, "stored checksum mismatch")

	details := status.Details()
	if len(details) != 1 || details[0] != detail {
		t.Fatalf("details = %#v, want original detail", details)
	}
	details[0] = nil
	if got := status.Details()[0]; got != detail {
		t.Fatalf("Details() returned mutable backing storage; got %#v", got)
	}
}

func TestErrorfBuildsFormattedStatusError(t *testing.T) {
	err := Errorf(CodeInvalidArgument, "invalid %s", "request")
	_ = requireStatusError(t, err, CodeInvalidArgument, "invalid request")
}

func TestWrapPreservesCauseAndDetails(t *testing.T) {
	cause := errors.New("metadata write failed")
	detail := timestamppb.Now()
	err := Wrap(CodeUnavailable, "commit unavailable", cause, WithDetails(detail))

	if got := err.Error(); got != "commit unavailable: metadata write failed" {
		t.Fatalf("Error() = %q, want wrapped message", got)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("wrapped error does not match cause %v", cause)
	}
	var status *Error
	if !errors.As(err, &status) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if !errors.Is(status.Unwrap(), cause) {
		t.Fatalf("Unwrap() = %v, want %v", status.Unwrap(), cause)
	}
	if status.Code != CodeUnavailable {
		t.Fatalf("code = %d, want %d", status.Code, CodeUnavailable)
	}
	if details := status.Details(); len(details) != 1 || details[0] != detail {
		t.Fatalf("details = %#v, want wrapped detail", details)
	}
}

func requireStatusError(t *testing.T, err error, code Code, message string) *Error {
	t.Helper()
	var status *Error
	if !errors.As(err, &status) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if status.Code != code {
		t.Fatalf("code = %d, want %d", status.Code, code)
	}
	if got := status.Error(); got != message {
		t.Fatalf("Error() = %q, want %q", got, message)
	}
	if got := status.Message; got != message {
		t.Fatalf("message = %q, want %q", got, message)
	}
	return status
}
