package closeutil

import "io"

// Logger matches log.Printf-style functions.
type Logger func(string, ...any)

// Ignore closes a non-durable cleanup resource and intentionally discards the
// close error. Durability boundaries must report sync/commit/close errors before
// using this helper for follow-up cleanup.
func Ignore(closer io.Closer) {
	if closer == nil {
		return
	}
	_ = closer.Close()
}

// Log closes a shutdown resource and records close failures without changing the
// caller's exit path.
func Log(name string, logf Logger, closer io.Closer) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil && logf != nil {
		logf("%s close failed: %v", name, err)
	}
}
