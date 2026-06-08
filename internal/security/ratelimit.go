package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	RateLimitSurfacePublic RateLimitSurface = "public"
	RateLimitSurfacePeer   RateLimitSurface = "peer"
	RateLimitSurfaceAdmin  RateLimitSurface = "admin"

	rateLimitReasonExceeded = "rate_limited"
	rateLimitUnknownOp      = "unknown"
	rateLimitAnonymousKey   = "anonymous"
	maxRateLimitKeyLen      = 64
)

// ErrRateLimited is returned when a request budget is exhausted.
var ErrRateLimited = errors.New("rate limit exceeded")

type RateLimitSurface string

type RateLimitPolicy struct {
	Surfaces []RateLimitSurfacePolicy
}

type RateLimitSurfacePolicy struct {
	Surface RateLimitSurface
	Limit   int
	Window  time.Duration
}

type RateLimitDecision struct {
	Surface    RateLimitSurface
	Operation  string
	Reason     string
	RetryAfter time.Duration
	Limited    bool
}

type RateLimitObserver interface {
	RateLimitDenied(context.Context, RateLimitDecision)
}

type RateLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	observer RateLimitObserver
	surfaces map[RateLimitSurface]*surfaceBudget
}

type surfaceBudget struct {
	limit  int
	window time.Duration
	keys   map[string]windowCounter
}

type windowCounter struct {
	count int
	reset time.Time
}

type RateLimiterOption func(*RateLimiter)

func WithRateLimitNow(now func() time.Time) RateLimiterOption {
	return func(l *RateLimiter) {
		if now != nil {
			l.now = now
		}
	}
}

func WithRateLimitObserver(observer RateLimitObserver) RateLimiterOption {
	return func(l *RateLimiter) {
		l.observer = observer
	}
}

func NewRateLimiter(policy RateLimitPolicy, opts ...RateLimiterOption) *RateLimiter {
	limiter := &RateLimiter{
		now:      time.Now,
		surfaces: make(map[RateLimitSurface]*surfaceBudget, len(policy.Surfaces)),
	}
	for _, surface := range policy.Surfaces {
		if surface.Limit <= 0 || surface.Window <= 0 {
			continue
		}
		limiter.surfaces[surface.Surface] = &surfaceBudget{
			limit:  surface.Limit,
			window: surface.Window,
			keys:   make(map[string]windowCounter),
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(limiter)
		}
	}
	return limiter
}

func (l *RateLimiter) Allow(ctx context.Context, surface RateLimitSurface, key string, operation ...string) RateLimitDecision {
	if l == nil {
		return RateLimitDecision{Surface: surface}
	}
	op := rateLimitUnknownOp
	if len(operation) > 0 && strings.TrimSpace(operation[0]) != "" {
		op = strings.TrimSpace(operation[0])
	}
	l.mu.Lock()
	budget := l.surfaces[surface]
	if budget == nil {
		l.mu.Unlock()
		return RateLimitDecision{Surface: surface, Operation: op}
	}
	now := l.now()
	cleanKey := rateLimitKey(key)
	counter := budget.keys[cleanKey]
	if counter.reset.IsZero() || !now.Before(counter.reset) {
		counter = windowCounter{reset: now.Add(budget.window)}
	}
	decision := RateLimitDecision{Surface: surface, Operation: op}
	if counter.count >= budget.limit {
		decision.Limited = true
		decision.Reason = rateLimitReasonExceeded
		decision.RetryAfter = counter.reset.Sub(now)
		budget.keys[cleanKey] = counter
		l.mu.Unlock()
		if l.observer != nil {
			l.observer.RateLimitDenied(ctx, decision)
		}
		return decision
	}
	counter.count++
	budget.keys[cleanKey] = counter
	l.mu.Unlock()
	return decision
}

func LoadRateLimitPolicy(path string) (RateLimitPolicy, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Operator-configured rate-limit policy path.
	if err != nil {
		return RateLimitPolicy{}, errors.New("rate-limit policy file is unreadable")
	}
	var raw struct {
		Surfaces []struct {
			Surface string `json:"surface"`
			Limit   int    `json:"limit"`
			Window  string `json:"window"`
		} `json:"surfaces"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return RateLimitPolicy{}, errors.New("rate-limit policy file must be valid JSON")
	}
	policy := RateLimitPolicy{Surfaces: make([]RateLimitSurfacePolicy, 0, len(raw.Surfaces))}
	seen := make(map[RateLimitSurface]struct{}, len(raw.Surfaces))
	for _, item := range raw.Surfaces {
		surface, surfacePolicy, err := parseRateLimitSurfacePolicy(item.Surface, item.Limit, item.Window)
		if err != nil {
			return RateLimitPolicy{}, err
		}
		if _, ok := seen[surface]; ok {
			return RateLimitPolicy{}, errors.New("rate-limit policy surface is duplicated")
		}
		seen[surface] = struct{}{}
		policy.Surfaces = append(policy.Surfaces, surfacePolicy)
	}
	for _, required := range []RateLimitSurface{RateLimitSurfacePublic, RateLimitSurfacePeer, RateLimitSurfaceAdmin} {
		if _, ok := seen[required]; !ok {
			return RateLimitPolicy{}, errors.New("rate-limit policy missing required surface")
		}
	}
	return policy, nil
}

func parseRateLimitSurfacePolicy(surfaceValue string, limit int, windowValue string) (RateLimitSurface, RateLimitSurfacePolicy, error) {
	surface := RateLimitSurface(strings.TrimSpace(surfaceValue))
	if !validRateLimitSurface(surface) {
		return "", RateLimitSurfacePolicy{}, errors.New("rate-limit policy surface is invalid")
	}
	if limit <= 0 {
		return "", RateLimitSurfacePolicy{}, errors.New("rate-limit policy limit is invalid")
	}
	window, err := time.ParseDuration(strings.TrimSpace(windowValue))
	if err != nil || window <= 0 {
		return "", RateLimitSurfacePolicy{}, errors.New("rate-limit policy window is invalid")
	}
	return surface, RateLimitSurfacePolicy{Surface: surface, Limit: limit, Window: window}, nil
}

type RateLimitOTelMetrics struct {
	denials metric.Int64Counter
}

func NewRateLimitOTelMetrics(meter metric.Meter) (*RateLimitOTelMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}
	denials, err := meter.Int64Counter(
		"scrap.security.rate_limit.denials",
		metric.WithDescription("Total number of requests denied by SCRAP security rate limits."),
	)
	if err != nil {
		return nil, fmt.Errorf("create rate-limit denial counter: %w", err)
	}
	return &RateLimitOTelMetrics{denials: denials}, nil
}

func (m *RateLimitOTelMetrics) RateLimitDenied(ctx context.Context, decision RateLimitDecision) {
	if m == nil {
		return
	}
	m.denials.Add(ctx, 1, metric.WithAttributes(
		attribute.String("scrap.surface", string(decision.Surface)),
		attribute.String("scrap.operation", decision.Operation),
		attribute.String("scrap.reason", decision.Reason),
	))
}

func RateLimitedError() error {
	return newAuthorizationError(ErrRateLimited, ErrRateLimited.Error())
}

func validRateLimitSurface(surface RateLimitSurface) bool {
	switch surface {
	case RateLimitSurfacePublic, RateLimitSurfacePeer, RateLimitSurfaceAdmin:
		return true
	default:
		return false
	}
}

func rateLimitKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return rateLimitAnonymousKey
	}
	if len(key) <= maxRateLimitKeyLen {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(sum[:8])
}
