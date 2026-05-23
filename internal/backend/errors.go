package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
)

type ErrorClass string

const (
	ErrorClassThrottled ErrorClass = "throttled"
	ErrorClassTransient ErrorClass = "transient"
	ErrorClassAuth      ErrorClass = "auth"
	ErrorClassNotFound  ErrorClass = "not_found"
	ErrorClassConflict  ErrorClass = "conflict"
	ErrorClassCorrupt   ErrorClass = "corrupt"
	ErrorClassPermanent ErrorClass = "permanent"
)

type Error struct {
	Class     ErrorClass
	Operation string
	Key       string
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "backend"
	if e.Operation != "" {
		message += " " + e.Operation
	}
	if e.Key != "" {
		message += " " + e.Key
	}
	if e.Class != "" {
		message += " " + string(e.Class)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	return classSentinel(e.Class) == target
}

func NewError(class ErrorClass, operation string, key string, err error) error {
	if err == nil {
		err = classSentinel(class)
	}
	return &Error{
		Class:     class,
		Operation: operation,
		Key:       key,
		Err:       err,
	}
}

func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ""
	}
	var classified *Error
	if errors.As(err, &classified) && classified.Class.Valid() {
		return classified.Class
	}
	switch {
	case errors.Is(err, ErrThrottled):
		return ErrorClassThrottled
	case errors.Is(err, ErrTransient), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ErrorClassTransient
	case errors.Is(err, ErrAuth), errors.Is(err, os.ErrPermission):
		return ErrorClassAuth
	case errors.Is(err, ErrNotFound), errors.Is(err, os.ErrNotExist):
		return ErrorClassNotFound
	case errors.Is(err, ErrConflict):
		return ErrorClassConflict
	case errors.Is(err, ErrChecksumMismatch):
		return ErrorClassCorrupt
	default:
		return ErrorClassPermanent
	}
}

func IsErrorClass(err error, class ErrorClass) bool {
	return ClassifyError(err) == class
}

func (c ErrorClass) Valid() bool {
	switch c {
	case ErrorClassThrottled,
		ErrorClassTransient,
		ErrorClassAuth,
		ErrorClassNotFound,
		ErrorClassConflict,
		ErrorClassCorrupt,
		ErrorClassPermanent:
		return true
	default:
		return false
	}
}

func classSentinel(class ErrorClass) error {
	switch class {
	case ErrorClassThrottled:
		return ErrThrottled
	case ErrorClassTransient:
		return ErrTransient
	case ErrorClassAuth:
		return ErrAuth
	case ErrorClassNotFound:
		return ErrNotFound
	case ErrorClassConflict:
		return ErrConflict
	case ErrorClassCorrupt:
		return ErrChecksumMismatch
	case ErrorClassPermanent:
		return ErrPermanent
	default:
		return fmt.Errorf("backend: unknown error class %q", class)
	}
}
