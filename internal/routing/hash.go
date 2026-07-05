package routing

import (
	"fmt"
	"hash/fnv"
)

// SlotCount is the fixed Shard routing slot count.
const SlotCount = 1024

// SlotForTransaction returns the fixed hash slot for a Transaction identifier.
// Only the truly empty string is rejected: identifiers must not be trimmed or
// normalized (CONTEXT.md), and the API boundary accepts whitespace-only IDs.
func SlotForTransaction(transactionID string) (uint16, error) {
	if transactionID == "" {
		return 0, fmt.Errorf("%w: transaction_id is required", ErrInvalidTransaction)
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(transactionID))
	return uint16(h.Sum64() % SlotCount), nil
}
