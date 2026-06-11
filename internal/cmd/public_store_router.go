package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/petabytecl/scrap/internal/routing"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type publicStoreRouter struct {
	router routing.Router
	stores map[uint64]storeapi.Store
}

func newPublicStoreRouter(placement routing.Placement, stores map[uint64]storeapi.Store) storeapi.Store {
	copied := make(map[uint64]storeapi.Store, len(stores))
	for shardID, store := range stores {
		if store == nil {
			continue
		}
		copied[shardID] = store
	}
	return &publicStoreRouter{
		router: routing.NewRouter(placement),
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
