package shard

import (
	"math"

	"github.com/petabytecl/scrap/internal/index"
)

type uploadObligations struct {
	local []uploadObligation
}

type uploadObligation struct {
	upload   index.PendingUpload
	inFlight bool
}

type uploadPressureStats struct {
	pendingBytes  int64
	pendingBlocks int
}

func (o *uploadObligations) recordLocal(upload index.PendingUpload) {
	next := make([]uploadObligation, 0, len(o.local)+1)
	replaced := false
	for _, existing := range o.local {
		if existing.upload.BlockID == upload.BlockID {
			next = append(next, uploadObligation{
				upload:   upload,
				inFlight: existing.inFlight,
			})
			replaced = true
			continue
		}
		next = append(next, existing)
	}
	if !replaced {
		next = append(next, uploadObligation{upload: upload})
	}
	o.local = next
}

func (o *uploadObligations) forget(blockID uint64) {
	next := make([]uploadObligation, 0, len(o.local))
	for _, obligation := range o.local {
		if obligation.upload.BlockID == blockID {
			continue
		}
		next = append(next, obligation)
	}
	o.local = next
}

func (o *uploadObligations) beginRetry() []index.PendingUpload {
	pending := make([]index.PendingUpload, 0, len(o.local))
	next := make([]uploadObligation, 0, len(o.local))
	for _, obligation := range o.local {
		if obligation.inFlight {
			next = append(next, obligation)
			continue
		}
		pending = append(pending, obligation.upload)
		next = append(next, uploadObligation{
			upload:   obligation.upload,
			inFlight: true,
		})
	}
	o.local = next
	return pending
}

func (o *uploadObligations) markRetryFailed(blockID uint64) {
	next := make([]uploadObligation, 0, len(o.local))
	for _, obligation := range o.local {
		if obligation.upload.BlockID == blockID && obligation.inFlight {
			next = append(next, uploadObligation{upload: obligation.upload})
			continue
		}
		next = append(next, obligation)
	}
	o.local = next
}

func (o *uploadObligations) len() int {
	return len(o.local)
}

func (o *uploadObligations) pressureStats(committed []PendingUpload) uploadPressureStats {
	seen := make(map[uint64]struct{}, len(committed)+len(o.local))
	stats := uploadPressureStats{}
	for _, upload := range committed {
		addPendingUpload(&stats, upload)
		seen[upload.BlockID] = struct{}{}
	}
	for _, obligation := range o.local {
		if _, ok := seen[obligation.upload.BlockID]; ok {
			continue
		}
		addPendingUpload(&stats, obligation.upload)
	}
	return stats
}

func addPendingUpload(stats *uploadPressureStats, upload PendingUpload) {
	if upload.SealedSizeBytes > math.MaxInt64-stats.pendingBytes {
		stats.pendingBytes = math.MaxInt64
	} else {
		stats.pendingBytes += upload.SealedSizeBytes
	}
	stats.pendingBlocks++
}
