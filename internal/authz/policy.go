package authz

import (
	"context"
	"crypto/x509"
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
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	grpcpeer "google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/identity"
)

const (
	WorkloadIdentityMetadataKey = "x-scrap-workload-identity"

	ReasonPolicyRequired             = "SCRAP_AUTHZ_POLICY_REQUIRED"
	ReasonMissingWorkloadIdentity    = "SCRAP_AUTHZ_WORKLOAD_IDENTITY_REQUIRED"
	ReasonMissingCertificateIdentity = "SCRAP_AUTHZ_CERTIFICATE_IDENTITY_REQUIRED"
	ReasonCapabilityDenied           = "SCRAP_AUTHZ_CAPABILITY_DENIED"
	ReasonTenantDenied               = "SCRAP_AUTHZ_TENANT_DENIED"
	ReasonCapabilityUnmapped         = "SCRAP_AUTHZ_CAPABILITY_UNMAPPED"
	ReasonPolicyReloadRejected       = "SCRAP_AUTHZ_POLICY_RELOAD_REJECTED"
)

const (
	maxIdentifierBytes = 128
	maxReloadAlerts    = 32
)

var ErrInvalidPolicy = errors.New("invalid authorization policy")

type Capability string

type Policy struct {
	Version   string                    `json:"version"`
	Workloads map[string]WorkloadPolicy `json:"workloads"`
}

type WorkloadPolicy struct {
	Capabilities   []Capability `json:"capabilities"`
	AllowedTenants []string     `json:"allowed_tenants,omitempty"`
}

type Decision struct {
	Allowed           bool
	WorkloadIdentity  string
	Capability        Capability
	TenantID          string
	PolicyVersion     string
	PolicyGeneration  uint64
	Reason            string
	ReasonDescription string
}

type DeniedAuditSink interface {
	RecordDeniedRequest(ctx context.Context, method string, decision Decision) error
}

type WorkloadIdentityOptions struct {
	RequireCertificateIdentity bool
}

type InterceptorOptions struct {
	DeniedAuditSink            DeniedAuditSink
	RequireCertificateIdentity bool
	TenantExtractors           map[string]TenantExtractor
}

type TenantExtractor func(req any) (string, bool)

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
	workloads map[string]compiledWorkloadPolicy
}

type compiledWorkloadPolicy struct {
	capabilities   map[Capability]struct{}
	allowedTenants map[string]struct{}
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
	return m.AuthorizeWithOptions(ctx, capability, WorkloadIdentityOptions{})
}

func (m *Manager) AuthorizeWithOptions(ctx context.Context, capability Capability, options WorkloadIdentityOptions) Decision {
	return m.authorize(ctx, capability, "", false, options)
}

func (m *Manager) AuthorizeTenant(ctx context.Context, capability Capability, tenantID string) Decision {
	return m.AuthorizeTenantWithOptions(ctx, capability, tenantID, WorkloadIdentityOptions{})
}

func (m *Manager) AuthorizeTenantWithOptions(ctx context.Context, capability Capability, tenantID string, options WorkloadIdentityOptions) Decision {
	return m.authorize(ctx, capability, tenantID, true, options)
}

func (m *Manager) authorize(ctx context.Context, capability Capability, tenantID string, hasTenant bool, options WorkloadIdentityOptions) Decision {
	if m == nil {
		return Decision{
			Capability:        capability,
			TenantID:          tenantID,
			Reason:            ReasonPolicyRequired,
			ReasonDescription: "authorization policy is not loaded",
		}
	}
	workload, ok := WorkloadIdentityFromContextWithOptions(ctx, options)
	if !ok {
		reason := ReasonMissingWorkloadIdentity
		description := "workload identity metadata or mTLS certificate identity is required"
		if options.RequireCertificateIdentity {
			reason = ReasonMissingCertificateIdentity
			description = "mTLS client certificate identity is required"
		}
		return Decision{
			Capability:        capability,
			TenantID:          tenantID,
			Reason:            reason,
			ReasonDescription: description,
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	decision := Decision{
		WorkloadIdentity: workload,
		Capability:       capability,
		TenantID:         tenantID,
		PolicyVersion:    m.policy.version,
		PolicyGeneration: m.generation,
	}
	workloadPolicy, ok := m.policy.workloads[workload]
	if !ok {
		decision.Reason = ReasonCapabilityDenied
		decision.ReasonDescription = "workload identity is not present in the active policy"
		return decision
	}
	if _, ok := workloadPolicy.capabilities[capability]; !ok {
		decision.Reason = ReasonCapabilityDenied
		decision.ReasonDescription = "workload identity does not have the required capability"
		return decision
	}
	if hasTenant && !workloadPolicy.allowsTenant(tenantID) {
		decision.Reason = ReasonTenantDenied
		decision.ReasonDescription = "workload identity is not allowed to access the requested tenant"
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
	return UnaryServerInterceptorWithOptions(manager, capabilities, InterceptorOptions{
		DeniedAuditSink: firstDeniedAuditSink(auditSinks),
	})
}

func UnaryServerInterceptorWithOptions(manager *Manager, capabilities map[string]Capability, options InterceptorOptions) grpc.UnaryServerInterceptor {
	capabilities = cloneCapabilityMap(capabilities)
	tenantExtractors := cloneTenantExtractorMap(options.TenantExtractors)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		tenantID, hasTenant := tenantFromRequest(tenantExtractors, info.FullMethod, req)
		decision, err := requireCapability(ctx, manager, capabilities, info.FullMethod, options, tenantID, hasTenant)
		if err != nil {
			recordDeniedRequest(ctx, options.DeniedAuditSink, info.FullMethod, decision)
			return nil, err
		}
		return handler(ctx, req)
	}
}

func StreamServerInterceptor(manager *Manager, capabilities map[string]Capability, auditSinks ...DeniedAuditSink) grpc.StreamServerInterceptor {
	return StreamServerInterceptorWithOptions(manager, capabilities, InterceptorOptions{
		DeniedAuditSink: firstDeniedAuditSink(auditSinks),
	})
}

func StreamServerInterceptorWithOptions(manager *Manager, capabilities map[string]Capability, options InterceptorOptions) grpc.StreamServerInterceptor {
	capabilities = cloneCapabilityMap(capabilities)
	tenantExtractors := cloneTenantExtractorMap(options.TenantExtractors)
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		decision, err := requireCapability(stream.Context(), manager, capabilities, info.FullMethod, options, "", false)
		if err != nil {
			recordDeniedRequest(stream.Context(), options.DeniedAuditSink, info.FullMethod, decision)
			return err
		}
		if extractor := tenantExtractors[info.FullMethod]; extractor != nil {
			stream = &tenantAuthorizingServerStream{
				ServerStream: stream,
				manager:      manager,
				capability:   decision.Capability,
				method:       info.FullMethod,
				options:      options,
				extractor:    extractor,
			}
		}
		return handler(srv, stream)
	}
}

func ContextWithWorkloadIdentity(ctx context.Context, workloadIdentity string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(WorkloadIdentityMetadataKey, workloadIdentity))
}

func WorkloadIdentityFromContext(ctx context.Context) (string, bool) {
	return WorkloadIdentityFromContextWithOptions(ctx, WorkloadIdentityOptions{})
}

func WorkloadIdentityFromContextWithOptions(ctx context.Context, options WorkloadIdentityOptions) (string, bool) {
	if value, ok := certificateWorkloadIdentityFromContext(ctx); ok {
		return value, true
	}
	if options.RequireCertificateIdentity {
		return "", false
	}
	return metadataWorkloadIdentityFromContext(ctx)
}

func certificateWorkloadIdentityFromContext(ctx context.Context) (string, bool) {
	peer, ok := grpcpeer.FromContext(ctx)
	if !ok || peer.AuthInfo == nil {
		return "", false
	}
	tlsInfo, ok := peer.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", false
	}
	if tlsInfo.SPIFFEID != nil {
		if value := strings.TrimSpace(tlsInfo.SPIFFEID.String()); value != "" {
			return value, true
		}
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return "", false
	}
	return workloadIdentityFromCertificate(tlsInfo.State.PeerCertificates[0])
}

func workloadIdentityFromCertificate(cert *x509.Certificate) (string, bool) {
	if cert == nil {
		return "", false
	}
	for _, uri := range cert.URIs {
		if uri == nil {
			continue
		}
		if value := strings.TrimSpace(uri.String()); value != "" {
			return value, true
		}
	}
	if value := strings.TrimSpace(cert.Subject.CommonName); value != "" {
		return value, true
	}
	for _, dnsName := range cert.DNSNames {
		if value := strings.TrimSpace(dnsName); value != "" {
			return value, true
		}
	}
	for _, emailAddress := range cert.EmailAddresses {
		if value := strings.TrimSpace(emailAddress); value != "" {
			return value, true
		}
	}
	return "", false
}

func metadataWorkloadIdentityFromContext(ctx context.Context) (string, bool) {
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

func requireCapability(
	ctx context.Context,
	manager *Manager,
	capabilities map[string]Capability,
	method string,
	options InterceptorOptions,
	tenantID string,
	hasTenant bool,
) (Decision, error) {
	capability, ok := capabilities[method]
	if !ok {
		decision := Decision{
			Reason:            ReasonCapabilityUnmapped,
			ReasonDescription: "RPC method is not mapped to an authorization capability",
		}
		return decision, status.Error(codes.PermissionDenied, ReasonCapabilityUnmapped+": RPC method is not mapped to an authorization capability")
	}
	if hasTenant {
		return requireTenant(ctx, manager, capability, method, options, tenantID)
	}
	decision := manager.AuthorizeWithOptions(ctx, capability, workloadIdentityOptions(options))
	return authorizationError(method, decision)
}

func requireTenant(
	ctx context.Context,
	manager *Manager,
	capability Capability,
	method string,
	options InterceptorOptions,
	tenantID string,
) (Decision, error) {
	decision := manager.AuthorizeTenantWithOptions(ctx, capability, tenantID, workloadIdentityOptions(options))
	return authorizationError(method, decision)
}

func authorizationError(method string, decision Decision) (Decision, error) {
	if decision.Allowed {
		return decision, nil
	}
	code := codes.PermissionDenied
	if decision.Reason == ReasonMissingWorkloadIdentity ||
		decision.Reason == ReasonMissingCertificateIdentity ||
		decision.Reason == ReasonPolicyRequired {
		code = codes.Unauthenticated
	}
	return decision, status.Error(code, fmt.Sprintf("%s: %s requires %s", decision.Reason, method, decision.Capability))
}

func workloadIdentityOptions(options InterceptorOptions) WorkloadIdentityOptions {
	return WorkloadIdentityOptions{
		RequireCertificateIdentity: options.RequireCertificateIdentity,
	}
}

type tenantAuthorizingServerStream struct {
	grpc.ServerStream
	manager    *Manager
	capability Capability
	method     string
	options    InterceptorOptions
	extractor  TenantExtractor
}

func (s tenantAuthorizingServerStream) RecvMsg(req any) error {
	if err := s.ServerStream.RecvMsg(req); err != nil {
		return err
	}
	tenantID, ok := s.extractor(req)
	if !ok {
		return nil
	}
	decision, err := requireTenant(s.Context(), s.manager, s.capability, s.method, s.options, tenantID)
	if err != nil {
		recordDeniedRequest(s.Context(), s.options.DeniedAuditSink, s.method, decision)
		return err
	}
	return nil
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
		workloads: make(map[string]compiledWorkloadPolicy, len(policy.Workloads)),
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
		allowedTenants, err := compileAllowedTenants(workload, workloadPolicy.AllowedTenants)
		if err != nil {
			return compiledPolicy{}, err
		}
		out.workloads[workload] = compiledWorkloadPolicy{
			capabilities:   capabilities,
			allowedTenants: allowedTenants,
		}
	}
	return out, nil
}

func (p compiledWorkloadPolicy) allowsTenant(tenantID string) bool {
	if len(p.allowedTenants) == 0 {
		return true
	}
	_, ok := p.allowedTenants[tenantID]
	return ok
}

func compileAllowedTenants(workload string, tenants []string) (map[string]struct{}, error) {
	allowed := make(map[string]struct{}, len(tenants))
	if len(tenants) == 0 {
		return allowed, nil
	}
	for _, tenant := range tenants {
		tenant = strings.TrimSpace(tenant)
		if err := validateAllowedTenant(tenant); err != nil {
			return nil, fmt.Errorf("%w: workload %q has invalid allowed_tenants entry: %w", ErrInvalidPolicy, workload, err)
		}
		allowed[tenant] = struct{}{}
	}
	return allowed, nil
}

func validateAllowedTenant(value string) error {
	if value == "" {
		return fmt.Errorf("%w: allowed tenant is required", ErrInvalidPolicy)
	}
	if len(value) > identity.MaxTenantIDBytes {
		return fmt.Errorf("%w: allowed tenant exceeds %d bytes", ErrInvalidPolicy, identity.MaxTenantIDBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: allowed tenant must be valid UTF-8", ErrInvalidPolicy)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: allowed tenant must not contain control characters", ErrInvalidPolicy)
		}
	}
	return nil
}

func validateIdentifier(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidPolicy, label)
	}
	if len(value) > maxIdentifierBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidPolicy, label, maxIdentifierBytes)
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

func cloneTenantExtractorMap(in map[string]TenantExtractor) map[string]TenantExtractor {
	out := make(map[string]TenantExtractor, len(in))
	for method, extractor := range in {
		out[method] = extractor
	}
	return out
}

func tenantFromRequest(extractors map[string]TenantExtractor, method string, req any) (string, bool) {
	extractor := extractors[method]
	if extractor == nil {
		return "", false
	}
	tenantID, ok := extractor(req)
	if !ok {
		return "", false
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", false
	}
	return tenantID, true
}

func SortedCapabilities(capabilities []Capability) []Capability {
	out := append([]Capability(nil), capabilities...)
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out
}
