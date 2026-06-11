package routing

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidPlacement identifies malformed or incomplete slot placement.
	ErrInvalidPlacement = errors.New("invalid routing placement")
	// ErrInvalidTransaction identifies a missing Transaction identifier.
	ErrInvalidTransaction = errors.New("invalid transaction")
	// ErrRouteNotFound identifies an uncovered slot in a Placement.
	ErrRouteNotFound = errors.New("route not found")
)

func invalidPlacement(format string, args ...any) error {
	msgArgs := append([]any{ErrInvalidPlacement}, args...)
	return fmt.Errorf("%w: "+format, msgArgs...)
}
