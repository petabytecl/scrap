package admin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/rewrap"
	"github.com/petabytecl/scrap/internal/security"
)

const (
	readHeaderTimeout         = 10 * time.Second
	maxTestHookBodyBytes      = 1024
	evidenceMarkerHeader      = "X-Scrap-Evidence-Marker"
	maxEvidenceMarkerLen      = 96
	evidenceLogProbeMessage   = "evidence log probe"
	evidenceLogProbeAttribute = "evidence_marker"
)

var evidenceMarkerChars = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

type ProjectionInjector interface {
	InjectProjectionKey(ctx context.Context, txID string, blockID uint64, docCount uint16, completed bool) error
}

type TransitRotator interface {
	RotateTransitKey(context.Context) error
}

type UploadPressureProvider interface {
	UploadPressureSnapshot() (level int, levelName string, pendingBytes int64, pendingBlocks int)
}

type EvictionHealthProvider interface {
	EvictionHealthSnapshot(context.Context) (eviction.HealthSnapshot, error)
}

type Option func(*Server)

type Server struct {
	mu                 sync.Mutex
	httpSrv            *http.Server
	handler            http.Handler
	projectionInjector ProjectionInjector
	transitRotator     TransitRotator
	uploadPressure     UploadPressureProvider
	pprofEnabled       bool
	metricsHandler     http.Handler
	evictionPlanner    EvictionPlanner
	evictionApplier    EvictionApplier
	evictionPlanStatus EvictionPlanStatusProvider
	evictionHealth     EvictionHealthProvider
	rewrapService      RewrapService
	securityMode       security.Mode
	readiness          security.Readiness
	authorizer         *security.Authorizer
	auditSink          audit.Sink
	rateLimiter        *security.RateLimiter
	logger             *slog.Logger
}

func WithProjectionInjector(injector ProjectionInjector) Option {
	return func(s *Server) {
		s.projectionInjector = injector
	}
}

func WithTransitRotator(rotator TransitRotator) Option {
	return func(s *Server) {
		s.transitRotator = rotator
	}
}

func WithUploadPressureProvider(provider UploadPressureProvider) Option {
	return func(s *Server) {
		s.uploadPressure = provider
	}
}

func WithEvictionHealthProvider(provider EvictionHealthProvider) Option {
	return func(s *Server) {
		s.evictionHealth = provider
	}
}

func WithPprof() Option {
	return func(s *Server) {
		s.pprofEnabled = true
	}
}

func WithMetrics(handler http.Handler) Option {
	return func(s *Server) {
		s.metricsHandler = handler
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

func WithSecurityStatus(mode security.Mode, readiness security.Readiness) Option {
	return func(s *Server) {
		s.securityMode = mode
		s.readiness = readiness
	}
}

func WithAuthorizer(authorizer *security.Authorizer) Option {
	return func(s *Server) {
		s.authorizer = authorizer
	}
}

func WithAuditSink(sink audit.Sink) Option {
	return func(s *Server) {
		s.auditSink = sink
	}
}

func WithRateLimiter(limiter *security.RateLimiter) Option {
	return func(s *Server) {
		s.rateLimiter = limiter
	}
}

func New(opts ...Option) *Server {
	s := &Server{}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	if s.projectionInjector != nil {
		mux.HandleFunc("/test-hooks/projection-key", s.handleProjectionKeyHook)
	}
	if s.transitRotator != nil {
		mux.HandleFunc("/test-hooks/transit-rotate", s.handleTransitRotateHook)
	}
	if s.metricsHandler != nil {
		mux.Handle("/metrics", s.authorizedHandler(security.RoleAdminReader, s.metricsHandler))
	}
	if s.evictionPlanner != nil {
		mux.HandleFunc("/admin/eviction/plans", s.handleEvictionPlans)
	}
	if s.evictionApplier != nil || s.evictionPlanStatus != nil {
		mux.HandleFunc("/admin/eviction/plans/", s.handleEvictionPlanByID)
	}
	if s.rewrapService != nil {
		mux.HandleFunc("/admin/rewrap/document", s.handleRewrapDocument)
	}
	if s.pprofEnabled {
		// GET-only: profiling is a read-only diagnostic. getOnly rejects every other
		// method, including HEAD — which net/http's ServeMux otherwise routes to a
		// GET handler, so HEAD /debug/pprof/profile would still start CPU collection.
		mux.HandleFunc("/debug/pprof/", s.authorizedGetPprof(pprof.Index))
		mux.HandleFunc("/debug/pprof/cmdline", s.authorizedGetFunc(security.RoleAdminReader, pprof.Cmdline))
		mux.HandleFunc("/debug/pprof/profile", s.authorizedGetFunc(security.RoleAdminBreakGlass, pprof.Profile))
		mux.HandleFunc("/debug/pprof/trace", s.authorizedGetFunc(security.RoleAdminBreakGlass, pprof.Trace))
		// /symbol is exempt from getOnly: unlike the endpoints above it has no side
		// effects (it's a stateless PC→name lookup), and `go tool pprof` POSTs the
		// address list in the request body whenever it exceeds URL length limits.
		// Gating it to GET would return 405 and silently break symbolization.
		mux.HandleFunc("/debug/pprof/symbol", s.authorizedFunc(security.RoleAdminReader, pprof.Symbol))
	}
	s.handler = mux
	return s
}

func (s *Server) authorizedHandler(role security.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, role) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorizedFunc(role security.Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, role) {
			return
		}
		next(w, r)
	}
}

func (s *Server) authorizedGetFunc(role security.Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeMethod(w, r, role, http.MethodGet) {
			return
		}
		next(w, r)
	}
}

func (s *Server) authorizedGetPprof(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role := security.RoleAdminReader
		switch r.URL.Path {
		case "/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/symbol":
		default:
			role = security.RoleAdminBreakGlass
		}
		if !s.authorizeMethod(w, r, role, http.MethodGet) {
			return
		}
		next(w, r)
	}
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, role security.Role) bool {
	return s.authorizeMethod(w, r, role, "")
}

func (s *Server) authorizeMethod(w http.ResponseWriter, r *http.Request, role security.Role, method string) bool {
	operation, target := auditRequest(r, role)
	resolvedRequest, principalErr := s.requestWithResolvedPrincipal(r)
	if decision := s.checkRateLimit(resolvedRequest.Context(), operation); decision.Limited {
		if err := s.recordAudit(resolvedRequest.Context(), role, operation, target, audit.ResultRateLimited, audit.ReasonRateLimited); err != nil {
			http.Error(w, "audit event failed", http.StatusInternalServerError)
			return false
		}
		http.Error(w, security.ErrRateLimited.Error(), http.StatusTooManyRequests)
		return false
	}
	if principalErr != nil {
		if err := s.recordAudit(resolvedRequest.Context(), role, operation, target, audit.ResultDenied, s.auditReasonForError(principalErr)); err != nil {
			http.Error(w, "audit event failed", http.StatusInternalServerError)
			return false
		}
		http.Error(w, principalErr.Error(), security.HTTPStatusForAuthorization(principalErr))
		return false
	}
	if s.authorizer == nil {
		return s.auditAllowedOrMethodDenied(w, resolvedRequest, role, operation, target, method)
	}
	if err := s.authorizer.Authorize(resolvedRequest.Context(), role); err != nil {
		if auditErr := s.recordAudit(resolvedRequest.Context(), role, operation, target, audit.ResultDenied, s.auditReasonForError(err)); auditErr != nil {
			http.Error(w, "audit event failed", http.StatusInternalServerError)
			return false
		}
		http.Error(w, err.Error(), security.HTTPStatusForAuthorization(err))
		return false
	}
	return s.auditAllowedOrMethodDenied(w, resolvedRequest, role, operation, target, method)
}

func (s *Server) auditAllowedOrMethodDenied(w http.ResponseWriter, r *http.Request, role security.Role, operation, target, method string) bool {
	if method != "" && r.Method != method {
		if err := s.recordAudit(r.Context(), role, operation, target, audit.ResultDenied, audit.ReasonMethodNotAllowed); err != nil {
			http.Error(w, "audit event failed", http.StatusInternalServerError)
			return false
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if err := s.recordAudit(r.Context(), role, operation, target, audit.ResultAllowed, audit.ReasonAllowed); err != nil {
		http.Error(w, "audit event failed", http.StatusInternalServerError)
		return false
	}
	return true
}

func (s *Server) requestWithResolvedPrincipal(r *http.Request) (*http.Request, error) {
	if s.authorizer == nil {
		return r, nil
	}
	if _, ok := security.PrincipalFromContext(r.Context()); ok {
		return r, nil
	}
	if r.TLS == nil {
		return r, nil
	}
	ctx, err := s.authorizer.ContextWithTLSPrincipal(r.Context(), *r.TLS)
	if err != nil {
		return r, err
	}
	return r.WithContext(ctx), nil
}

func (s *Server) checkRateLimit(ctx context.Context, operation string) security.RateLimitDecision {
	if s.rateLimiter == nil {
		return security.RateLimitDecision{}
	}
	return s.rateLimiter.Allow(ctx, security.RateLimitSurfaceAdmin, adminPrincipalID(ctx), operation)
}

func (s *Server) recordAudit(ctx context.Context, role security.Role, operation, target, result, reason string) error {
	if s.auditSink == nil {
		return nil
	}
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: adminPrincipalID(ctx),
		Role:        string(role),
		Surface:     audit.SurfaceAdmin,
		Operation:   operation,
		Target:      target,
		Result:      result,
		Reason:      reason,
	})
	if err != nil {
		return err
	}
	return s.auditSink.Record(ctx, event)
}

func (s *Server) auditReasonForError(err error) string {
	switch {
	case errors.Is(err, security.ErrUnauthenticated):
		return audit.ReasonUnauthenticated
	case errors.Is(err, security.ErrPermissionDenied):
		if security.AuthorizationStatusForError(err) == security.AuthorizationStatusMissingRole {
			return audit.ReasonMissingRole
		}
		return audit.ReasonPermissionDenied
	default:
		return audit.ReasonInternalError
	}
}

func auditRequest(r *http.Request, role security.Role) (operation, target string) {
	path := r.URL.Path
	if route, ok := adminAuditRoutes[path]; ok {
		return route.operation, route.target
	}
	if strings.HasPrefix(path, "/admin/eviction/plans") {
		return auditEvictionRequest(r)
	}
	if strings.HasPrefix(path, "/debug/pprof/") {
		return audit.OperationPprofProfile, audit.TargetProfile
	}
	if role == security.RoleAdminBreakGlass {
		return audit.OperationProjectionKeyHook, audit.TargetAdmin
	}
	return audit.OperationHealth, audit.TargetAdmin
}

type auditRoute struct {
	operation string
	target    string
}

var adminAuditRoutes = map[string]auditRoute{
	"/healthz":                   {operation: audit.OperationHealth, target: audit.TargetAdmin},
	"/metrics":                   {operation: audit.OperationMetrics, target: audit.TargetMetrics},
	"/admin/rewrap/document":     {operation: audit.OperationRewrapDocument, target: audit.TargetDocument},
	"/test-hooks/projection-key": {operation: audit.OperationProjectionKeyHook, target: audit.TargetEvidence},
	"/test-hooks/transit-rotate": {operation: audit.OperationTransitRotateHook, target: audit.TargetEvidence},
	"/debug/pprof/":              {operation: audit.OperationPprofIndex, target: audit.TargetProfile},
	"/debug/pprof/cmdline":       {operation: audit.OperationPprofCmdline, target: audit.TargetProfile},
	"/debug/pprof/profile":       {operation: audit.OperationPprofProfile, target: audit.TargetProfile},
	"/debug/pprof/trace":         {operation: audit.OperationPprofTrace, target: audit.TargetProfile},
	"/debug/pprof/symbol":        {operation: audit.OperationPprofSymbol, target: audit.TargetProfile},
}

func auditEvictionRequest(r *http.Request) (string, string) {
	if r.URL.Path == "/admin/eviction/plans" && r.Method == http.MethodPost {
		return audit.OperationEvictionPlanCreate, audit.TargetBlock
	}
	if strings.HasSuffix(r.URL.Path, "/apply") {
		return audit.OperationEvictionApply, audit.TargetBlock
	}
	return audit.OperationEvictionPlanStatus, audit.TargetBlock
}

func adminPrincipalID(ctx context.Context) string {
	principal, ok := security.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return principal.ID
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) ListenAndServe(addr string) error {
	return s.listenAndServe(addr, nil)
}

func (s *Server) ListenAndServeTLS(addr string, cfg *tls.Config) error {
	if cfg == nil {
		return errors.New("admin TLS config is required")
	}
	return s.listenAndServe(addr, cfg)
}

func (s *Server) listenAndServe(addr string, cfg *tls.Config) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("admin listen %s: %w", addr, err)
	}
	return s.serveListener(ln, cfg)
}

func (s *Server) serveListener(ln net.Listener, cfg *tls.Config) error {
	srv := &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	if cfg != nil {
		srv.TLSConfig = cfg.Clone()
	}

	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()

	serve := srv.Serve
	if cfg != nil {
		serve = func(listener net.Listener) error {
			return srv.ServeTLS(listener, "", "")
		}
	}
	if err := serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("admin serve: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()

	if srv == nil {
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("admin shutdown: %w", err)
	}
	return nil
}

type projectionKeyRequest struct {
	TransactionID string `json:"transaction_id"`
	BlockID       uint64 `json:"block_id"`
	DocCount      uint16 `json:"doc_count"`
	Completed     bool   `json:"completed"`
}

func (s *Server) handleProjectionKeyHook(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, security.RoleAdminBreakGlass) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req projectionKeyRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTestHookBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}
	if req.TransactionID == "" || req.BlockID == 0 || req.DocCount == 0 {
		http.Error(w, "transaction_id, block_id, and doc_count are required", http.StatusBadRequest)
		return
	}

	if err := s.projectionInjector.InjectProjectionKey(r.Context(), req.TransactionID, req.BlockID, req.DocCount, req.Completed); err != nil {
		http.Error(w, "projection injection failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTransitRotateHook(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, security.RoleAdminBreakGlass) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.transitRotator.RotateTransitKey(r.Context()); err != nil {
		http.Error(w, "transit rotate failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type healthResponse struct {
	Status                  string         `json:"status"`
	SecurityMode            string         `json:"security_mode,omitempty"`
	ProductionReadyStatus   string         `json:"production_readiness_status,omitempty"`
	ProductionReadyReason   string         `json:"production_readiness_reason,omitempty"`
	AuthorizationStatus     string         `json:"authorization_status,omitempty"`
	UploadPressure          string         `json:"upload_pressure"`
	UploadPressureLevel     int            `json:"upload_pressure_level"`
	UploadPendingBytes      int64          `json:"upload_pending_bytes"`
	UploadPendingBlocks     int            `json:"upload_pending_blocks"`
	EvictionPressure        string         `json:"eviction_pressure"`
	EvictedBlocks           int            `json:"evicted_blocks"`
	EvictedBytes            int64          `json:"evicted_bytes"`
	HotCleanupNeededBlocks  int            `json:"hot_cleanup_needed_blocks"`
	MetadataLossBlocks      int            `json:"metadata_loss_blocks"`
	UnexpectedLossBlocks    int            `json:"unexpected_loss_blocks"`
	QuarantinedBlocks       int            `json:"quarantined_blocks"`
	RestoreFailedBlocks     int            `json:"restore_failed_blocks"`
	RestoreFailuresByReason map[string]int `json:"restore_failures_by_reason,omitempty"`
	RewrapStatus            string         `json:"rewrap_status,omitempty"`
	RewrapLastResult        string         `json:"rewrap_last_result,omitempty"`
	RewrapLastReason        string         `json:"rewrap_last_reason,omitempty"`
	RewrapLastTransitMount  string         `json:"rewrap_last_transit_mount,omitempty"`
	RewrapLastTransitKey    string         `json:"rewrap_last_transit_key,omitempty"`
	RewrapLastOldVersion    int            `json:"rewrap_last_old_version,omitempty"`
	RewrapLastNewVersion    int            `json:"rewrap_last_new_version,omitempty"`
	RewrapLastChanged       bool           `json:"rewrap_last_changed,omitempty"`
	RewrapLastAt            time.Time      `json:"rewrap_last_at,omitempty"`
	RewrapFailuresByReason  map[string]int `json:"rewrap_failures_by_reason,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, security.RoleAdminReader) {
		return
	}

	if !s.handleEvidenceMarker(w, r) {
		return
	}

	resp := healthResponse{
		Status:              "ok",
		UploadPressure:      "ok",
		UploadPressureLevel: 0,
		EvictionPressure:    eviction.HealthPressureOK,
	}
	s.applySecurityHealth(&resp)
	s.applyUploadPressure(&resp)
	s.applyEvictionHealth(r.Context(), &resp)
	s.applyRewrapHealth(&resp)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "encode health response failed", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleEvidenceMarker(w http.ResponseWriter, r *http.Request) bool {
	marker := r.Header.Get(evidenceMarkerHeader)
	if marker == "" {
		return true
	}
	if !validEvidenceMarker(marker) {
		http.Error(w, "invalid evidence marker", http.StatusBadRequest)
		return false
	}
	if s.logger != nil {
		s.logger.InfoContext(r.Context(), evidenceLogProbeMessage, evidenceLogProbeAttribute, marker)
	}
	return true
}

func (s *Server) applySecurityHealth(resp *healthResponse) {
	if s.securityMode != "" {
		readiness := s.readiness
		if readiness.Status == "" {
			readiness = security.ProductionReadinessForMode(s.securityMode)
		}
		resp.SecurityMode = s.securityMode.String()
		resp.ProductionReadyStatus = string(readiness.Status)
		resp.ProductionReadyReason = readiness.Reason
	}
	resp.AuthorizationStatus = s.authorizationStatus()
}

func (s *Server) applyUploadPressure(resp *healthResponse) {
	if s.uploadPressure != nil {
		level, levelName, pendingBytes, pendingBlocks := s.uploadPressure.UploadPressureSnapshot()
		resp.UploadPressureLevel = level
		resp.UploadPressure = levelName
		resp.UploadPendingBytes = pendingBytes
		resp.UploadPendingBlocks = pendingBlocks
	}
}

func (s *Server) applyEvictionHealth(ctx context.Context, resp *healthResponse) {
	if s.evictionHealth != nil {
		snapshot, err := s.evictionHealth.EvictionHealthSnapshot(ctx)
		if err != nil {
			resp.Status = "degraded"
			resp.EvictionPressure = eviction.HealthPressureDegraded
			resp.RestoreFailuresByReason = map[string]int{eviction.RestoreFailureUnknown: 1}
		} else {
			applyEvictionHealthSnapshot(resp, snapshot)
		}
	}
}

func (s *Server) applyRewrapHealth(resp *healthResponse) {
	if s.rewrapService == nil {
		return
	}
	snapshot := s.rewrapService.RewrapHealthSnapshot()
	resp.RewrapStatus = snapshot.Status
	if resp.RewrapStatus == "" {
		resp.RewrapStatus = rewrap.StatusOK
	}
	resp.RewrapLastResult = snapshot.LastResult
	resp.RewrapLastReason = snapshot.LastReason
	resp.RewrapLastTransitMount = snapshot.LastTransitMount
	resp.RewrapLastTransitKey = snapshot.LastTransitKey
	resp.RewrapLastOldVersion = snapshot.LastOldVersion
	resp.RewrapLastNewVersion = snapshot.LastNewVersion
	resp.RewrapLastChanged = snapshot.LastChanged
	resp.RewrapLastAt = snapshot.LastAt
	resp.RewrapFailuresByReason = snapshot.FailuresByReason
	if resp.RewrapStatus != rewrap.StatusOK {
		resp.Status = "degraded"
	}
}

func (s *Server) authorizationStatus() string {
	if s.authorizer == nil {
		return ""
	}
	return s.authorizer.AuthorizationStatus()
}

func applyEvictionHealthSnapshot(resp *healthResponse, snapshot eviction.HealthSnapshot) {
	resp.EvictionPressure = snapshot.Pressure
	if resp.EvictionPressure == "" {
		resp.EvictionPressure = eviction.HealthPressureOK
	}
	resp.EvictedBlocks = snapshot.EvictedBlocks
	resp.EvictedBytes = snapshot.EvictedBytes
	resp.HotCleanupNeededBlocks = snapshot.HotCleanupNeededBlocks
	resp.MetadataLossBlocks = snapshot.MetadataLossBlocks
	resp.UnexpectedLossBlocks = snapshot.UnexpectedLossBlocks
	resp.QuarantinedBlocks = snapshot.QuarantinedBlocks
	resp.RestoreFailedBlocks = snapshot.RestoreFailedBlocks
	resp.RestoreFailuresByReason = snapshot.RestoreFailuresByReason
	if resp.EvictionPressure != eviction.HealthPressureOK {
		resp.Status = "degraded"
	}
}

func validEvidenceMarker(marker string) bool {
	if len(marker) == 0 || len(marker) > maxEvidenceMarkerLen {
		return false
	}
	return evidenceMarkerChars.MatchString(marker)
}
