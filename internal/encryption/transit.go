// Package encryption owns the Transit boundary used by envelope encryption.
package encryption

import (
	"context"
	"errors"
)

// Transit is the provider-neutral boundary used by storage encryption paths.
type Transit interface {
	GenerateDataKey(context.Context, GenerateDataKeyRequest) (DataKey, error)
	UnwrapDataKey(context.Context, UnwrapDataKeyRequest) (UnwrappedDataKey, error)
	RewrapDataKey(context.Context, RewrapDataKeyRequest) (RewrappedKey, error)
	Readiness(context.Context) (Readiness, error)
}

const (
	defaultDataKeyBits = 256
	bitsPerByte        = 8
)

type GenerateDataKeyRequest struct {
	Context []byte
	Bits    int
}

type DataKey struct {
	Plaintext  []byte
	WrappedKey string
	Version    int
}

type UnwrapDataKeyRequest struct {
	WrappedKey string
	Context    []byte
}

type UnwrappedDataKey struct {
	Plaintext []byte
	Version   int
}

type RewrapDataKeyRequest struct {
	WrappedKey string
	Context    []byte
	KeyVersion int
}

type RewrappedKey struct {
	WrappedKey string
	Version    int
	Changed    bool
}

type Readiness struct {
	Ready                    bool
	LatestVersion            int
	MinimumDecryptionVersion int
}

// Class is a provider-neutral Transit failure class.
type Class string

const (
	ClassUnavailable    Class = "unavailable"
	ClassAuthDenied     Class = "auth-denied"
	ClassMissingKey     Class = "missing-key"
	ClassMinimumVersion Class = "minimum-version"
	ClassInvalidConfig  Class = "invalid-config"
	ClassInvalidRequest Class = "invalid-request"
)

var (
	ErrUnavailable    = errors.New("transit unavailable")
	ErrAuthDenied     = errors.New("transit auth denied")
	ErrMissingKey     = errors.New("transit missing key")
	ErrMinimumVersion = errors.New("transit minimum version")
	ErrInvalidConfig  = errors.New("transit invalid config")
	ErrInvalidRequest = errors.New("transit invalid request")
)

// ErrorClass returns the provider-neutral Transit class for err.
func ErrorClass(err error) Class {
	switch {
	case errors.Is(err, ErrUnavailable):
		return ClassUnavailable
	case errors.Is(err, ErrAuthDenied):
		return ClassAuthDenied
	case errors.Is(err, ErrMissingKey):
		return ClassMissingKey
	case errors.Is(err, ErrMinimumVersion):
		return ClassMinimumVersion
	case errors.Is(err, ErrInvalidConfig):
		return ClassInvalidConfig
	case errors.Is(err, ErrInvalidRequest):
		return ClassInvalidRequest
	default:
		return ""
	}
}

// ProductionCapable reports whether a Transit implementation may be wired into
// production mode. Deterministic fakes deliberately return false.
func ProductionCapable(transit Transit) bool {
	capable, ok := transit.(interface{ ProductionCapable() bool })
	return ok && capable.ProductionCapable()
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func dataKeyBits(bits int) int {
	if bits == 0 {
		return defaultDataKeyBits
	}
	return bits
}
