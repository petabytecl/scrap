package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	"github.com/petabytecl/scrap/internal/routing"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type publicStoreRouter struct {
	router routing.Router
	stores map[uint64]storeapi.Store
}

func newPublicStoreRouter(placement routing.Placement, stores map[uint64]storeapi.Store, recorder routing.LookupRecorder) storeapi.Store {
	copied := make(map[uint64]storeapi.Store, len(stores))
	for shardID, store := range stores {
		if store == nil {
			continue
		}
		copied[shardID] = store
	}
	return &publicStoreRouter{
		router: routing.NewRouter(placement, routing.WithLookupRecorder(recorder)),
		stores: copied,
	}
}

func (r *publicStoreRouter) WriteDocument(ctx context.Context, txID, docName, contentType, idempotencyKey string, body io.Reader) (storeapi.WriteResult, error) {
	target, err := r.targetStore(ctx, txID)
	if err != nil {
		return storeapi.WriteResult{}, err
	}
	return target.WriteDocument(ctx, txID, docName, contentType, idempotencyKey, body)
}

func (r *publicStoreRouter) HeadDocument(ctx context.Context, txID, docName string) (storeapi.DocumentMeta, error) {
	target, err := r.targetStore(ctx, txID)
	if err != nil {
		return storeapi.DocumentMeta{}, err
	}
	return target.HeadDocument(ctx, txID, docName)
}

func (r *publicStoreRouter) ReadDocument(ctx context.Context, txID, docName string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	target, err := r.targetStore(ctx, txID)
	if err != nil {
		return nil, storeapi.DocumentMeta{}, err
	}
	return target.ReadDocument(ctx, txID, docName)
}

func (r *publicStoreRouter) FindDocuments(ctx context.Context, txID string) ([]storeapi.DocumentMeta, error) {
	target, err := r.targetStore(ctx, txID)
	if err != nil {
		return nil, err
	}
	return target.FindDocuments(ctx, txID)
}

func (r *publicStoreRouter) targetStore(ctx context.Context, txID string) (storeapi.Store, error) {
	if r == nil || len(r.stores) == 0 {
		return nil, multiShardPublicRoutingPending()
	}
	route, err := r.router.Lookup(ctx, txID)
	if err != nil {
		return nil, publicRouteLookupError(err)
	}
	target, ok := r.stores[route.ShardID]
	if !ok || target == nil {
		return nil, publicRouteUnavailable()
	}
	return target, nil
}

func publicRouteLookupError(err error) error {
	if errors.Is(err, routing.ErrInvalidTransaction) {
		return fmt.Errorf("%w: transaction_id is required", storeapi.ErrInvalidArgument)
	}
	return publicRouteUnavailable()
}

func publicRouteUnavailable() error {
	return storeapi.NewUnavailable(
		storeapi.UnavailableReasonShardRouteUnavailable,
		"Shard route unavailable",
	)
}

type publicRouteLookupLogger struct {
	logger *slog.Logger
}

func (r publicRouteLookupLogger) RecordRoutingLookup(ctx context.Context, record routing.LookupRecord) {
	if r.logger == nil {
		return
	}
	attrs := []any{
		"scrap.route.outcome", string(record.Outcome),
		"scrap.route.reason", string(record.Reason),
	}
	if record.ShardIDValid {
		attrs = append(attrs, "scrap.route.shard_id", strconv.FormatUint(record.ShardID, 10))
	}
	r.logger.DebugContext(ctx, "public route lookup", attrs...)
}
