package scrub

import "errors"

var ErrConsistencyResultNotReady = errors.New("scrub: consistency result not ready")

type Result struct {
	ScrubID      string
	AppliedIndex uint64
	SHA256       [32]byte
}

type ResultCache interface {
	GetScrubResult(scrubID string) (Result, bool)
}
