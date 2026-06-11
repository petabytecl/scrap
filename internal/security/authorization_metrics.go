package security

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/petabytecl/scrap/internal/audit"
)

const authorizationUnknownOperation = "unknown"

// AuthorizationDecision is the bounded metric view of an authorization denial.
type AuthorizationDecision struct {
	Surface   RateLimitSurface
	Operation string
	Reason    string
	Status    string
}

// AuthorizationObserver records bounded authorization-denial evidence.
type AuthorizationObserver interface {
	AuthorizationDenied(context.Context, AuthorizationDecision)
}

// AuthorizationOTelMetrics records bounded authorization-denial metrics.
type AuthorizationOTelMetrics struct {
	denials metric.Int64Counter
}

// NewAuthorizationOTelMetrics creates OTel authorization-denial metrics.
func NewAuthorizationOTelMetrics(meter metric.Meter) (*AuthorizationOTelMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}
	denials, err := meter.Int64Counter(
		"scrap.security.authorization.denials",
		metric.WithDescription("Total number of requests denied by SCRAP authorization checks."),
	)
	if err != nil {
		return nil, fmt.Errorf("create authorization denial counter: %w", err)
	}
	return &AuthorizationOTelMetrics{denials: denials}, nil
}

func (m *AuthorizationOTelMetrics) AuthorizationDenied(ctx context.Context, decision AuthorizationDecision) {
	if m == nil {
		return
	}
	m.denials.Add(ctx, 1, metric.WithAttributes(
		attribute.String("scrap.surface", authorizationMetricSurface(decision.Surface)),
		attribute.String("scrap.operation", authorizationMetricOperation(decision.Operation)),
		attribute.String("scrap.reason", authorizationMetricReason(decision.Reason)),
		attribute.String("scrap.authorization_status", authorizationMetricStatus(decision.Status)),
	))
}

func authorizationMetricSurface(surface RateLimitSurface) string {
	if validRateLimitSurface(surface) {
		return string(surface)
	}
	return string(RateLimitSurfacePeer)
}

func authorizationMetricOperation(operation string) string {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return authorizationUnknownOperation
	}
	return operation
}

func authorizationMetricReason(reason string) string {
	reason = strings.TrimSpace(reason)
	switch reason {
	case audit.ReasonUnauthenticated,
		audit.ReasonPermissionDenied,
		audit.ReasonMissingRole,
		audit.ReasonMismatch,
		audit.ReasonInternalError:
		return reason
	default:
		return audit.ReasonInternalError
	}
}

func authorizationMetricStatus(status string) string {
	if validAuthorizationStatus(status) {
		return status
	}
	return AuthorizationStatusDenied
}
