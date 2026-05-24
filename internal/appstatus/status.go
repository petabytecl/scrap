package appstatus

import (
	"fmt"

	"google.golang.org/protobuf/proto"
)

type Code uint8

const (
	CodeInvalidArgument Code = iota + 1
	CodeNotFound
	CodeAlreadyExists
	CodeFailedPrecondition
	CodeResourceExhausted
	CodeUnavailable
	CodeDataLoss
	CodeInternal
)

type Error struct {
	Code    Code
	Message string
	Cause   error
	details []proto.Message
}

type Option func(*Error)

func New(code Code, message string, options ...Option) error {
	err := &Error{Code: code, Message: message}
	for _, option := range options {
		option(err)
	}
	return err
}

func Errorf(code Code, format string, args ...any) error {
	return New(code, fmt.Sprintf(format, args...))
}

func Wrap(code Code, message string, cause error, options ...Option) error {
	err := &Error{Code: code, Message: message, Cause: cause}
	for _, option := range options {
		option(err)
	}
	return err
}

func WithDetails(details ...proto.Message) Option {
	return func(err *Error) {
		err.details = append(err.details, details...)
	}
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return e.Message + ": " + e.Cause.Error()
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) Details() []proto.Message {
	return append([]proto.Message(nil), e.details...)
}
