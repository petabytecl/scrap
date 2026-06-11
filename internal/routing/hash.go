package routing

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// SlotCount is the fixed Shard routing slot count.
const SlotCount = 1024

// SlotForTransaction returns the fixed hash slot for a Transaction identifier.
func SlotForTransaction(transactionID string) (uint16, error) {
	if strings.TrimSpace(transactionID) == "" {
		return 0, fmt.Errorf("%w: transaction_id is required", ErrInvalidTransaction)
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(transactionID))
	return uint16(h.Sum64() % SlotCount), nil
}
