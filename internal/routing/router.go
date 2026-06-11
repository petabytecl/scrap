package routing

import (
	"context"
	"errors"
)

type (
	LookupOutcome string
	LookupReason  string
)

const (
	LookupOutcomeRouted   LookupOutcome = "routed"
	LookupOutcomeRejected LookupOutcome = "rejected"

	LookupReasonMatched            LookupReason = "matched"
	LookupReasonInvalidTransaction LookupReason = "invalid_transaction"
	LookupReasonNoRoute            LookupReason = "no_route"
)

// LookupRecord is a bounded telemetry payload for route lookups.
type LookupRecord struct {
	Outcome      LookupOutcome
	Reason       LookupReason
	ShardID      uint64
	ShardIDValid bool
}

// LookupRecorder records route lookup outcomes without raw Transaction IDs.
type LookupRecorder interface {
	RecordRoutingLookup(context.Context, LookupRecord)
}

type RouterOption func(*Router)

// Router wraps a Placement with optional route lookup telemetry.
type Router struct {
	placement Placement
	recorder  LookupRecorder
}

// NewRouter returns a route lookup boundary over a validated Placement.
func NewRouter(placement Placement, opts ...RouterOption) Router {
	router := Router{placement: placement}
	for _, opt := range opts {
		opt(&router)
	}
	return router
}

// WithLookupRecorder configures bounded route lookup telemetry.
func WithLookupRecorder(recorder LookupRecorder) RouterOption {
	return func(router *Router) {
		router.recorder = recorder
	}
}

// Lookup returns the owning Shard route and records a bounded lookup outcome.
func (r Router) Lookup(ctx context.Context, transactionID string) (Route, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	route, err := r.placement.Lookup(transactionID)
	if err != nil {
		r.record(ctx, LookupRecord{
			Outcome: LookupOutcomeRejected,
			Reason:  reasonForLookupError(err),
		})
		return Route{}, err
	}
	r.record(ctx, LookupRecord{
		Outcome:      LookupOutcomeRouted,
		Reason:       LookupReasonMatched,
		ShardID:      route.ShardID,
		ShardIDValid: true,
	})
	return route, nil
}

func (r Router) record(ctx context.Context, record LookupRecord) {
	if r.recorder == nil {
		return
	}
	r.recorder.RecordRoutingLookup(ctx, record)
}

func reasonForLookupError(err error) LookupReason {
	switch {
	case errors.Is(err, ErrInvalidTransaction):
		return LookupReasonInvalidTransaction
	case errors.Is(err, ErrRouteNotFound):
		return LookupReasonNoRoute
	default:
		return LookupReasonNoRoute
	}
}
