package scrub

type Result struct {
	ScrubID      string
	AppliedIndex uint64
	SHA256       [32]byte
}

type ResultCache interface {
	GetScrubResult(scrubID string) (Result, bool)
}
