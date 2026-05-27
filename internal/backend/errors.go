package backend

import "errors"

// Class is a provider-neutral backend error class.
type Class string

const (
	ClassThrottled Class = "throttled"
	ClassTransient Class = "transient"
	ClassAuth      Class = "auth"
	ClassNotFound  Class = "not-found"
	ClassConflict  Class = "conflict"
	ClassCorrupt   Class = "corrupt"
	ClassPermanent Class = "permanent"
)

var (
	ErrThrottled = errors.New("backend throttled")
	ErrTransient = errors.New("backend transient")
	ErrAuth      = errors.New("backend auth")
	ErrNotFound  = errors.New("backend not found")
	ErrConflict  = errors.New("backend conflict")
	ErrCorrupt   = errors.New("backend corrupt")
	ErrPermanent = errors.New("backend permanent")
)

// ErrorClass returns the provider-neutral class for err, or the empty class when
// err is nil or was not wrapped with one of the backend sentinels.
func ErrorClass(err error) Class {
	switch {
	case errors.Is(err, ErrThrottled):
		return ClassThrottled
	case errors.Is(err, ErrTransient):
		return ClassTransient
	case errors.Is(err, ErrAuth):
		return ClassAuth
	case errors.Is(err, ErrNotFound):
		return ClassNotFound
	case errors.Is(err, ErrConflict):
		return ClassConflict
	case errors.Is(err, ErrCorrupt):
		return ClassCorrupt
	case errors.Is(err, ErrPermanent):
		return ClassPermanent
	default:
		return ""
	}
}
