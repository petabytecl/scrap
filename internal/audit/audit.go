// Package audit defines bounded security audit records and sink boundaries.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	SurfacePublic = "public"
	SurfacePeer   = "peer"
	SurfaceAdmin  = "admin"

	TargetDocument = "document"
	TargetPeer     = "peer"
	TargetAdmin    = "admin"
	TargetBlock    = "block"
	TargetMetrics  = "metrics"
	TargetProfile  = "profile"
	TargetEvidence = "evidence"

	ResultAllowed     = "allowed"
	ResultDenied      = "denied"
	ResultRateLimited = "rate_limited"
	ResultFailed      = "failed"

	ReasonAllowed          = "ok"
	ReasonUnauthenticated  = "unauthenticated"
	ReasonPermissionDenied = "permission_denied"
	ReasonMissingRole      = "missing_role"
	ReasonMismatch         = "mismatch"
	ReasonRateLimited      = "rate_limited"
	ReasonInvalidRequest   = "invalid_request"
	ReasonInternalError    = "internal_error"
	ReasonMethodNotAllowed = "method_not_allowed"
	ReasonNotFound         = "not_found"

	OperationWriteDocument       = "write_document"
	OperationReadDocument        = "read_document"
	OperationHeadDocument        = "head_document"
	OperationFindDocuments       = "find_documents"
	OperationReplicateDocument   = "replicate_document"
	OperationForwardRaft         = "forward_raft"
	OperationForwardRaftStream   = "forward_raft_stream"
	OperationRequestIndexRebuild = "request_index_rebuild"
	OperationConsistencyCheck    = "consistency_check"
	OperationTransferBlock       = "transfer_block"
	OperationHealth              = "health"
	OperationMetrics             = "metrics"
	OperationEvictionPlanCreate  = "eviction_plan_create"
	OperationEvictionPlanStatus  = "eviction_plan_status"
	OperationEvictionApply       = "eviction_apply"
	OperationRewrapDocument      = "rewrap_document"
	OperationQuarantineList      = "quarantine_list"
	OperationQuarantineInspect   = "quarantine_inspect"
	OperationQuarantineConfirm   = "quarantine_confirm"
	OperationQuarantineRelease   = "quarantine_release"
	OperationPprofIndex          = "pprof_index"
	OperationPprofCmdline        = "pprof_cmdline"
	OperationPprofProfile        = "pprof_profile"
	OperationPprofTrace          = "pprof_trace"
	OperationPprofSymbol         = "pprof_symbol"
	OperationProjectionKeyHook   = "projection_key_hook"
	OperationTransitRotateHook   = "transit_rotate_hook"
	OperationLightScrubHook      = "light_scrub_hook"

	PrincipalAnonymous = "anonymous"
)

const (
	principalHashPrefix = "sha256:"
	principalHashBytes  = 8
	defaultMaxEventSize = 1024

	// FailureModeFailClosed blocks the audited operation when the sink write fails.
	FailureModeFailClosed = "fail_closed"
	// FailureModeFailOpen drops the event, logs the loss, and lets the operation proceed.
	FailureModeFailOpen = "fail_open"
)

// Event is the bounded audit record emitted for security decisions.
type Event struct {
	Time      time.Time `json:"time"`
	Principal string    `json:"principal"`
	Role      string    `json:"role"`
	Surface   string    `json:"surface"`
	Operation string    `json:"operation"`
	Target    string    `json:"target"`
	Result    string    `json:"result"`
	Reason    string    `json:"reason"`
}

// EventInput contains the potentially raw boundary inputs used to build Event.
type EventInput struct {
	PrincipalID string
	Role        string
	Surface     string
	Operation   string
	Target      string
	Result      string
	Reason      string
	Now         time.Time
}

// NewEvent creates a bounded audit event without retaining raw principal IDs.
func NewEvent(input EventInput) (Event, error) {
	event := Event{
		Time:      input.Now,
		Principal: PrincipalHandle(input.PrincipalID),
		Role:      strings.TrimSpace(input.Role),
		Surface:   strings.TrimSpace(input.Surface),
		Operation: strings.TrimSpace(input.Operation),
		Target:    strings.TrimSpace(input.Target),
		Result:    strings.TrimSpace(input.Result),
		Reason:    strings.TrimSpace(input.Reason),
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if err := validateEvent(event); err != nil {
		return Event{}, err
	}
	return event, nil
}

// PrincipalHandle returns a stable, bounded principal handle for audit output.
func PrincipalHandle(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return PrincipalAnonymous
	}
	sum := sha256.Sum256([]byte(id))
	return principalHashPrefix + hex.EncodeToString(sum[:principalHashBytes])
}

// Sink records audit events.
type Sink interface {
	Record(context.Context, Event) error
}

// MemorySink is a test sink that stores copied events.
type MemorySink struct {
	mu     sync.Mutex
	events []Event
}

func NewMemorySink() *MemorySink {
	return &MemorySink{}
}

func (s *MemorySink) Record(_ context.Context, event Event) error {
	if err := validateEvent(event); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *MemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]Event, len(s.events))
	copy(events, s.events)
	return events
}

// LoggerSink writes bounded audit events to slog.
type LoggerSink struct {
	logger *slog.Logger
}

func NewLoggerSink(logger *slog.Logger) *LoggerSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggerSink{logger: logger.With("component", "audit")}
}

func (s *LoggerSink) Record(ctx context.Context, event Event) error {
	if err := validateEvent(event); err != nil {
		return err
	}
	record := slog.NewRecord(event.Time, slog.LevelInfo, "security audit event", 0)
	record.AddAttrs(
		slog.String("audit.principal", event.Principal),
		slog.String("audit.role", event.Role),
		slog.String("audit.surface", event.Surface),
		slog.String("audit.operation", event.Operation),
		slog.String("audit.target", event.Target),
		slog.String("audit.result", event.Result),
		slog.String("audit.reason", event.Reason),
	)
	if err := s.logger.Handler().Handle(ctx, record); err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

type NopSink struct{}

func NewNopSink() NopSink {
	return NopSink{}
}

func (NopSink) Record(context.Context, Event) error {
	return nil
}

// Policy describes the production audit sink policy.
type Policy struct {
	Sink          string
	FailureMode   string
	MaxEventBytes int
}

func LoadPolicy(path string) (Policy, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Operator-configured audit policy path.
	if err != nil {
		return Policy{}, errors.New("audit policy file is unreadable")
	}
	var raw struct {
		Sink          string `json:"sink"`
		FailureMode   string `json:"failure_mode"`
		MaxEventBytes int    `json:"max_event_bytes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Policy{}, errors.New("audit policy file must be valid JSON")
	}
	policy := Policy{
		Sink:          strings.TrimSpace(raw.Sink),
		FailureMode:   strings.TrimSpace(raw.FailureMode),
		MaxEventBytes: raw.MaxEventBytes,
	}
	if policy.FailureMode == "" {
		policy.FailureMode = "fail_closed"
	}
	if policy.MaxEventBytes == 0 {
		policy.MaxEventBytes = defaultMaxEventSize
	}
	if err := policy.validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (p Policy) validate() error {
	switch p.Sink {
	case "log", "stderr":
	default:
		return errors.New("audit policy sink is invalid")
	}
	if p.FailureMode != FailureModeFailClosed && p.FailureMode != FailureModeFailOpen {
		return errors.New("audit policy failure_mode is invalid")
	}
	if p.MaxEventBytes < 256 || p.MaxEventBytes > 4096 {
		return errors.New("audit policy max_event_bytes is invalid")
	}
	return nil
}

// PolicySink enforces the production audit policy on every recorded event:
// events over MaxEventBytes are rejected (fields are enum-bounded, so an
// oversized event indicates corruption, and truncation would break that
// bounding), and sink write failures follow FailureMode.
type PolicySink struct {
	inner  Sink
	policy Policy
	logger *slog.Logger
}

func NewPolicySink(inner Sink, policy Policy, logger *slog.Logger) (*PolicySink, error) {
	if inner == nil {
		return nil, errors.New("audit policy sink requires an inner sink")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PolicySink{inner: inner, policy: policy, logger: logger.With("component", "audit")}, nil
}

func (s *PolicySink) Record(ctx context.Context, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode audit event: %w", err)
	}
	if len(data) > s.policy.MaxEventBytes {
		return fmt.Errorf("audit event size %d exceeds policy max %d bytes", len(data), s.policy.MaxEventBytes)
	}
	if err := s.inner.Record(ctx, event); err != nil {
		if s.policy.FailureMode == FailureModeFailOpen {
			s.logger.WarnContext(ctx, "audit event dropped by fail-open policy", "error", err)
			return nil
		}
		return fmt.Errorf("audit sink write failed: %w", err)
	}
	return nil
}

func validateEvent(event Event) error {
	checks := []struct {
		name    string
		value   string
		allowed map[string]struct{}
	}{
		{name: "role", value: event.Role, allowed: allowedRoles},
		{name: "surface", value: event.Surface, allowed: allowedSurfaces},
		{name: "operation", value: event.Operation, allowed: allowedOperations},
		{name: "target", value: event.Target, allowed: allowedTargets},
		{name: "result", value: event.Result, allowed: allowedResults},
		{name: "reason", value: event.Reason, allowed: allowedReasons},
	}
	if event.Principal != PrincipalAnonymous && !strings.HasPrefix(event.Principal, principalHashPrefix) {
		return errors.New("audit principal must be bounded")
	}
	for _, check := range checks {
		if _, ok := check.allowed[check.value]; !ok {
			return fmt.Errorf("audit %s is invalid", check.name)
		}
	}
	return nil
}

var allowedRoles = set(
	"document_writer",
	"document_reader",
	"peer_member",
	"admin_reader",
	"admin_operator",
	"admin_break_glass",
	"unknown",
)

var allowedSurfaces = set(SurfacePublic, SurfacePeer, SurfaceAdmin)

var allowedTargets = set(TargetDocument, TargetPeer, TargetAdmin, TargetBlock, TargetMetrics, TargetProfile, TargetEvidence)

var allowedResults = set(ResultAllowed, ResultDenied, ResultRateLimited, ResultFailed)

var allowedReasons = set(
	ReasonAllowed,
	ReasonUnauthenticated,
	ReasonPermissionDenied,
	ReasonMissingRole,
	ReasonMismatch,
	ReasonRateLimited,
	ReasonInvalidRequest,
	ReasonInternalError,
	ReasonMethodNotAllowed,
	ReasonNotFound,
)

var allowedOperations = set(
	OperationWriteDocument,
	OperationReadDocument,
	OperationHeadDocument,
	OperationFindDocuments,
	OperationReplicateDocument,
	OperationForwardRaft,
	OperationForwardRaftStream,
	OperationRequestIndexRebuild,
	OperationConsistencyCheck,
	OperationTransferBlock,
	OperationHealth,
	OperationMetrics,
	OperationEvictionPlanCreate,
	OperationEvictionPlanStatus,
	OperationEvictionApply,
	OperationRewrapDocument,
	OperationQuarantineList,
	OperationQuarantineInspect,
	OperationQuarantineConfirm,
	OperationQuarantineRelease,
	OperationPprofIndex,
	OperationPprofCmdline,
	OperationPprofProfile,
	OperationPprofTrace,
	OperationPprofSymbol,
	OperationProjectionKeyHook,
	OperationTransitRotateHook,
	OperationLightScrubHook,
)

func set(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
