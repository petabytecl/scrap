package security

import (
	"errors"
	"fmt"
	"strings"
)

// Mode describes the configured security posture for a running Cell.
type Mode string

const (
	// ModeProduction requires all production security gates to pass before the
	// process may serve traffic.
	ModeProduction Mode = "production"
	// ModeDevelopment is an explicit non-production mode for local/dev Cells.
	ModeDevelopment Mode = "development"
	// ModeTest is an explicit non-production mode for tests and test overlays.
	ModeTest Mode = "test"
)

// ConfigClass is a bounded startup-gate failure class.
type ConfigClass string

const (
	ClassSecurityMode       ConfigClass = "security_mode"
	ClassTLSConfig          ConfigClass = "tls_config"
	ClassRolePolicy         ConfigClass = "role_policy"
	ClassPeerIdentityPolicy ConfigClass = "peer_identity_policy"
	ClassTransitConfig      ConfigClass = "transit_config"
	ClassAuditConfig        ConfigClass = "audit_config"
	ClassRateLimitConfig    ConfigClass = "rate_limit_config"
	ClassDangerousHooks     ConfigClass = "dangerous_hooks"
)

// ReadinessStatus is a bounded production-readiness state.
type ReadinessStatus string

const (
	ReadinessStatusReady    ReadinessStatus = "ready"
	ReadinessStatusNotReady ReadinessStatus = "not_ready"
)

const (
	ReadinessReasonNonProductionSecurityMode = "non_production_security_mode"
	readinessReasonInvalidSecurityMode       = "invalid_security_mode"
)

// Readiness reports whether this process can satisfy production gates.
type Readiness struct {
	Status ReadinessStatus
	Reason string
}

// StartupGateError is a typed startup validation error with a bounded class.
type StartupGateError struct {
	Class  ConfigClass
	Key    string
	Reason string
}

func (e *StartupGateError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s: %s", e.Class, e.Reason)
	}
	return fmt.Sprintf("%s: invalid %s: %s", e.Class, e.Key, e.Reason)
}

func newGateError(class ConfigClass, key, reason string) error {
	return &StartupGateError{Class: class, Key: key, Reason: reason}
}

// ErrorClass extracts a startup gate class from err.
func ErrorClass(err error) ConfigClass {
	var gateErr *StartupGateError
	if errors.As(err, &gateErr) {
		return gateErr.Class
	}
	return ""
}

// ParseMode parses a security mode. Empty or unknown values fail closed.
func ParseMode(raw string) (Mode, error) {
	switch Mode(strings.TrimSpace(raw)) {
	case ModeProduction:
		return ModeProduction, nil
	case ModeDevelopment:
		return ModeDevelopment, nil
	case ModeTest:
		return ModeTest, nil
	default:
		return "", newGateError(ClassSecurityMode, "SCRAP_SECURITY_MODE", "must be production, development, or test")
	}
}

func (m Mode) String() string {
	return string(m)
}

// IsProduction reports whether m is the production security mode.
func (m Mode) IsProduction() bool {
	return m == ModeProduction
}

// IsNonProduction reports whether m is an explicit non-production mode.
func (m Mode) IsNonProduction() bool {
	return m == ModeDevelopment || m == ModeTest
}

// ProductionReadinessForMode reports the production-readiness state implied by m.
func ProductionReadinessForMode(m Mode) Readiness {
	if m.IsProduction() {
		return Readiness{Status: ReadinessStatusReady}
	}
	if m.IsNonProduction() {
		return Readiness{
			Status: ReadinessStatusNotReady,
			Reason: ReadinessReasonNonProductionSecurityMode,
		}
	}
	return Readiness{Status: ReadinessStatusNotReady, Reason: readinessReasonInvalidSecurityMode}
}
