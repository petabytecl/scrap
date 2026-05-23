package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/petabytecl/scrap/internal/closeutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	WorkloadIdentityMetadataKey = "x-scrap-workload-identity"

	ReasonPolicyRequired          = "SCRAP_AUTHZ_POLICY_REQUIRED"
	ReasonMissingWorkloadIdentity = "SCRAP_AUTHZ_WORKLOAD_IDENTITY_REQUIRED"
	ReasonCapabilityDenied        = "SCRAP_AUTHZ_CAPABILITY_DENIED"
	ReasonCapabilityUnmapped      = "SCRAP_AUTHZ_CAPABILITY_UNMAPPED"
	ReasonPolicyReloadRejected    = "SCRAP_AUTHZ_POLICY_RELOAD_REJECTED"
)

const maxReloadAlerts = 32

var ErrInvalidPolicy = errors.New("invalid authorization policy")

type Capability string

type Policy struct {
	Version   string                    `json:"version"`
	Workloads map[string]WorkloadPolicy `json:"workloads"`
}

type WorkloadPolicy struct {
	Capabilities []Capability `json:"capabilities"`
}

type Decision struct {
	Allowed           bool
	WorkloadIdentity  string
	Capability        Capability
	PolicyVersion     string
	PolicyGeneration  uint64
	Reason            string
	ReasonDescription string
}

type DeniedAuditSink interface {
	RecordDeniedRequest(ctx context.Context, method string, decision Decision) error
}

type ReloadAlert struct {
	Code             string
	Message          string
	RejectedAt       time.Time
	ActiveVersion    string
	ActiveGeneration uint64
}

type Manager struct {
	mu                sync.RWMutex
	policy            compiledPolicy
	knownCapabilities map[Capability]struct{}
	generation        uint64
	alerts            []ReloadAlert
}

type compiledPolicy struct {
	version   string
	workloads map[string]map[Capability]struct{}
}

func LoadManagerFromFile(path string, knownCapabilities []Capability) (*Manager, error) {
	policy, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	return NewManager(policy, knownCapabilities)
}

func LoadFile(path string) (Policy, error) {
	if strings.TrimSpace(path) == "" {
		return Policy{}, fmt.Errorf("%w: authorization policy path is required", ErrInvalidPolicy)
	}
	cleanPath := filepath.Clean(path)
	// #nosec G304 -- this is an operator-configured policy file path, not request input.
	file, err := os.Open(cleanPath)
	if err != nil {
		return Policy{}, fmt.Errorf("open authorization policy: %w", err)
	}
	defer closeutil.Ignore(file)

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("%w: decode authorization policy: %w", ErrInvalidPolicy, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Policy{}, fmt.Errorf("%w: authorization policy contains trailing JSON", ErrInvalidPolicy)
		}
		return Policy{}, fmt.Errorf("%w: authorization policy contains trailing data: %w", ErrInvalidPolicy, err)
	}
	return policy, nil
}

func NewManager(policy Policy, knownCapabilities []Capability) (*Manager, error) {
	known := makeKnownCapabilities(knownCapabilities)
	compiled, err := compilePolicy(policy, known)
	if err != nil {
		return nil, err
	}
	return &Manager{
		policy:            compiled,
		knownCapabilities: known,
		generation:        1,
	}, nil
}

func (m *Manager) ReloadFile(path string) error {
	policy, err := LoadFile(path)
	if err != nil {
		m.recordReloadAlert(err)
		return err
	}
	compiled, err := compilePolicy(policy, m.knownCapabilities)
	if err != nil {
		m.recordReloadAlert(err)
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = compiled
	m.generation++
	return nil
}

func (m *Manager) Authorize(ctx context.Context, capability Capability) Decision {
	if m == nil {
		return Decision{
			Capability:        capability,
			Reason:            ReasonPolicyRequired,
			ReasonDescription: "authorization policy is not loaded",
		}
	}
	workload, ok := WorkloadIdentityFromContext(ctx)
	if !ok {
		return Decision{
			Capability:        capability,
			Reason:            ReasonMissingWorkloadIdentity,
			ReasonDescription: "workload identity metadata is required",
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	decision := Decision{
		WorkloadIdentity: workload,
		Capability:       capability,
		PolicyVersion:    m.policy.version,
		PolicyGeneration: m.generation,
	}
	capabilities, ok := m.policy.workloads[workload]
	if !ok {
		decision.Reason = ReasonCapabilityDenied
		decision.ReasonDescription = "workload identity is not present in the active policy"
		return decision
	}
	if _, ok := capabilities[capability]; !ok {
		decision.Reason = ReasonCapabilityDenied
		decision.ReasonDescription = "workload identity does not have the required capability"
		return decision
	}
	decision.Allowed = true
	return decision
}

func (m *Manager) Generation() uint64 {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation
}

func (m *Manager) PolicyVersion() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy.version
}

func (m *Manager) ReloadAlerts() []ReloadAlert {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]ReloadAlert(nil), m.alerts...)
}

func UnaryServerInterceptor(manager *Manager, capabilities map[string]Capability, auditSinks ...DeniedAuditSink) grpc.UnaryServerInterceptor {
	capabilities = cloneCapabilityMap(capabilities)
	auditSink := firstDeniedAuditSink(auditSinks)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		decision, err := requireCapability(ctx, manager, capabilities, info.FullMethod)
		if err != nil {
			recordDeniedRequest(ctx, auditSink, info.FullMethod, decision)
			return nil, err
		}
		return handler(ctx, req)
	}
}

func StreamServerInterceptor(manager *Manager, capabilities map[string]Capability, auditSinks ...DeniedAuditSink) grpc.StreamServerInterceptor {
	capabilities = cloneCapabilityMap(capabilities)
	auditSink := firstDeniedAuditSink(auditSinks)
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		decision, err := requireCapability(stream.Context(), manager, capabilities, info.FullMethod)
		if err != nil {
			recordDeniedRequest(stream.Context(), auditSink, info.FullMethod, decision)
			return err
		}
		return handler(srv, stream)
	}
}

func ContextWithWorkloadIdentity(ctx context.Context, workloadIdentity string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(WorkloadIdentityMetadataKey, workloadIdentity))
}

func WorkloadIdentityFromContext(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	values := md.Get(WorkloadIdentityMetadataKey)
	if len(values) != 1 {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return "", false
	}
	return value, true
}

func requireCapability(ctx context.Context, manager *Manager, capabilities map[string]Capability, method string) (Decision, error) {
	capability, ok := capabilities[method]
	if !ok {
		decision := Decision{
			Reason:            ReasonCapabilityUnmapped,
			ReasonDescription: "RPC method is not mapped to an authorization capability",
		}
		return decision, status.Error(codes.PermissionDenied, ReasonCapabilityUnmapped+": RPC method is not mapped to an authorization capability")
	}
	decision := manager.Authorize(ctx, capability)
	if decision.Allowed {
		return decision, nil
	}
	code := codes.PermissionDenied
	if decision.Reason == ReasonMissingWorkloadIdentity || decision.Reason == ReasonPolicyRequired {
		code = codes.Unauthenticated
	}
	return decision, status.Error(code, fmt.Sprintf("%s: %s requires %s", decision.Reason, method, capability))
}

func firstDeniedAuditSink(auditSinks []DeniedAuditSink) DeniedAuditSink {
	if len(auditSinks) == 0 {
		return nil
	}
	return auditSinks[0]
}

func recordDeniedRequest(ctx context.Context, auditSink DeniedAuditSink, method string, decision Decision) {
	if auditSink == nil {
		return
	}
	_ = auditSink.RecordDeniedRequest(ctx, method, decision)
}

func (m *Manager) recordReloadAlert(err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, ReloadAlert{
		Code:             ReasonPolicyReloadRejected,
		Message:          err.Error(),
		RejectedAt:       time.Now().UTC(),
		ActiveVersion:    m.policy.version,
		ActiveGeneration: m.generation,
	})
	if len(m.alerts) > maxReloadAlerts {
		m.alerts = append([]ReloadAlert(nil), m.alerts[len(m.alerts)-maxReloadAlerts:]...)
	}
}

func compilePolicy(policy Policy, knownCapabilities map[Capability]struct{}) (compiledPolicy, error) {
	if strings.TrimSpace(policy.Version) == "" {
		return compiledPolicy{}, fmt.Errorf("%w: version is required", ErrInvalidPolicy)
	}
	if len(policy.Workloads) == 0 {
		return compiledPolicy{}, fmt.Errorf("%w: at least one workload is required", ErrInvalidPolicy)
	}
	out := compiledPolicy{
		version:   policy.Version,
		workloads: make(map[string]map[Capability]struct{}, len(policy.Workloads)),
	}
	for workload, workloadPolicy := range policy.Workloads {
		if err := validateIdentifier("workload identity", workload); err != nil {
			return compiledPolicy{}, err
		}
		if len(workloadPolicy.Capabilities) == 0 {
			return compiledPolicy{}, fmt.Errorf("%w: workload %q must declare at least one capability", ErrInvalidPolicy, workload)
		}
		capabilities := make(map[Capability]struct{}, len(workloadPolicy.Capabilities))
		for _, capability := range workloadPolicy.Capabilities {
			if err := validateCapability(capability, knownCapabilities); err != nil {
				return compiledPolicy{}, err
			}
			capabilities[capability] = struct{}{}
		}
		out.workloads[workload] = capabilities
	}
	return out, nil
}

func validateIdentifier(label string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidPolicy, label)
	}
	if len(value) > 128 {
		return fmt.Errorf("%w: %s exceeds 128 bytes", ErrInvalidPolicy, label)
	}
	for _, r := range value {
		if r > unicode.MaxASCII || unicode.IsControl(r) {
			return fmt.Errorf("%w: %s must be printable ASCII", ErrInvalidPolicy, label)
		}
	}
	return nil
}

func validateCapability(capability Capability, knownCapabilities map[Capability]struct{}) error {
	value := string(capability)
	if err := validateIdentifier("capability", value); err != nil {
		return err
	}
	if value == "*" || value == "admin" || value == "admin.*" || value == "public" || value == "public.*" {
		return fmt.Errorf("%w: broad capability %q is not operation-specific", ErrInvalidPolicy, value)
	}
	if len(knownCapabilities) > 0 {
		if _, ok := knownCapabilities[capability]; !ok {
			return fmt.Errorf("%w: unknown capability %q", ErrInvalidPolicy, value)
		}
	}
	return nil
}

func makeKnownCapabilities(capabilities []Capability) map[Capability]struct{} {
	if len(capabilities) == 0 {
		return nil
	}
	known := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		known[capability] = struct{}{}
	}
	return known
}

func cloneCapabilityMap(in map[string]Capability) map[string]Capability {
	out := make(map[string]Capability, len(in))
	for method, capability := range in {
		out[method] = capability
	}
	return out
}

func SortedCapabilities(capabilities []Capability) []Capability {
	out := append([]Capability(nil), capabilities...)
	sort.Slice(out, func(i int, j int) bool {
		return out[i] < out[j]
	})
	return out
}
