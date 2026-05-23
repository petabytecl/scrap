package closeutil

import (
	"io"
	"reflect"
)

// Logger matches log.Printf-style functions.
type Logger func(string, ...any)

// Ignore closes a non-durable cleanup resource and intentionally discards the
// close error. Durability boundaries must report sync/commit/close errors before
// using this helper for follow-up cleanup.
func Ignore(closer io.Closer) {
	if isNilCloser(closer) {
		return
	}
	_ = closer.Close()
}

// Log closes a shutdown resource and records close failures without changing the
// caller's exit path. Passing a nil logger intentionally suppresses reporting.
func Log(name string, logf Logger, closer io.Closer) {
	if isNilCloser(closer) {
		return
	}
	if err := closer.Close(); err != nil && logf != nil {
		logf("%s close failed: %v", name, err)
	}
}

func isNilCloser(closer io.Closer) bool {
	if closer == nil {
		return true
	}
	value := reflect.ValueOf(closer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
