package encryption

import (
	"context"
	"fmt"
	"sync"
)

// TransitRoute identifies one allow-listed Transit mount/key pair.
type TransitRoute struct {
	Mount string
	Key   string
}

// RouterConfig configures envelope-identity Transit routing (ADR 0038).
type RouterConfig struct {
	Default Transit
	Routes  map[TransitRoute]Transit
}

// Router selects a Transit implementation from the envelope's stored mount/key.
type Router struct {
	mu           sync.RWMutex
	def          Transit
	routes       map[TransitRoute]Transit
	defaultRoute TransitRoute
}

// NewRouter builds a Transit router. Default is used for GenerateDataKey and
// for unwrap/rewrap when the envelope matches the default mount/key or when no
// more specific route is registered.
func NewRouter(cfg RouterConfig) (*Router, error) {
	if cfg.Default == nil {
		return nil, fmt.Errorf("encryption transit router: default transit is required: %w", ErrInvalidConfig)
	}
	routes := make(map[TransitRoute]Transit, len(cfg.Routes))
	for route, transit := range cfg.Routes {
		mount := normalizeTransitPath(route.Mount, "")
		key := normalizeTransitPath(route.Key, "")
		if mount == "" || key == "" {
			return nil, fmt.Errorf("encryption transit router: route mount and key are required: %w", ErrInvalidConfig)
		}
		if transit == nil {
			return nil, fmt.Errorf("encryption transit router: route %s/%s transit is required: %w", mount, key, ErrInvalidConfig)
		}
		routes[TransitRoute{Mount: mount, Key: key}] = transit
	}
	return &Router{def: cfg.Default, routes: routes}, nil
}

// SetDefaultRoute records the process default mount/key used for GenerateDataKey
// envelopes. Unwrap/Rewrap still prefer the envelope identity.
func (r *Router) SetDefaultRoute(mount, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultRoute = TransitRoute{
		Mount: normalizeTransitPath(mount, DefaultTransitMountPath),
		Key:   normalizeTransitPath(key, DefaultTransitKeyName),
	}
}

func (r *Router) ProductionCapable() bool {
	return ProductionCapable(r.def)
}

func (r *Router) GenerateDataKey(ctx context.Context, req GenerateDataKeyRequest) (DataKey, error) {
	return r.def.GenerateDataKey(ctx, req)
}

func (r *Router) UnwrapDataKey(ctx context.Context, req UnwrapDataKeyRequest) (UnwrappedDataKey, error) {
	transit, err := r.routeForRequest(req.TransitMount, req.TransitKey)
	if err != nil {
		return UnwrappedDataKey{}, err
	}
	return transit.UnwrapDataKey(ctx, req)
}

func (r *Router) RewrapDataKey(ctx context.Context, req RewrapDataKeyRequest) (RewrappedKey, error) {
	transit, err := r.routeForRequest(req.TransitMount, req.TransitKey)
	if err != nil {
		return RewrappedKey{}, err
	}
	return transit.RewrapDataKey(ctx, req)
}

func (r *Router) Readiness(ctx context.Context) (Readiness, error) {
	return r.def.Readiness(ctx)
}

func (r *Router) routeForRequest(mount, key string) (Transit, error) {
	mount = normalizeTransitPath(mount, "")
	key = normalizeTransitPath(key, "")
	if mount == "" && key == "" {
		return r.def, nil
	}
	if mount == "" || key == "" {
		return nil, fmt.Errorf("encryption transit route incomplete: %w", ErrInvalidRequest)
	}
	route := TransitRoute{Mount: mount, Key: key}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if transit, ok := r.routes[route]; ok {
		return transit, nil
	}
	if r.defaultRoute.Mount == mount && r.defaultRoute.Key == key {
		return r.def, nil
	}
	// Allow default Transit when no alternate routes are configured and the
	// envelope identity matches the process default, or when routes are empty
	// (single-key Cells).
	if len(r.routes) == 0 {
		return r.def, nil
	}
	return nil, fmt.Errorf("encryption transit route %s/%s is not allow-listed: %w", mount, key, ErrMissingKey)
}
