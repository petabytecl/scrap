package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"
)

const (
	readHeaderTimeout    = 10 * time.Second
	maxTestHookBodyBytes = 1024
)

type ProjectionInjector interface {
	InjectProjectionKey(ctx context.Context, txID string, blockID uint64, docCount uint16, completed bool) error
}

type UploadPressureProvider interface {
	UploadPressureSnapshot() (level int, levelName string, pendingBytes int64, pendingBlocks int)
}

type Option func(*Server)

type Server struct {
	mu                 sync.Mutex
	httpSrv            *http.Server
	handler            http.Handler
	projectionInjector ProjectionInjector
	uploadPressure     UploadPressureProvider
	pprofEnabled       bool
}

func WithProjectionInjector(injector ProjectionInjector) Option {
	return func(s *Server) {
		s.projectionInjector = injector
	}
}

func WithUploadPressureProvider(provider UploadPressureProvider) Option {
	return func(s *Server) {
		s.uploadPressure = provider
	}
}

func WithPprof() Option {
	return func(s *Server) {
		s.pprofEnabled = true
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
	if s.pprofEnabled {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	s.handler = mux
	return s
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()

	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("admin listen %s: %w", addr, err)
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

type healthResponse struct {
	Status              string `json:"status"`
	UploadPressure      string `json:"upload_pressure"`
	UploadPressureLevel int    `json:"upload_pressure_level"`
	UploadPendingBytes  int64  `json:"upload_pending_bytes"`
	UploadPendingBlocks int    `json:"upload_pending_blocks"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{
		Status:              "ok",
		UploadPressure:      "ok",
		UploadPressureLevel: 0,
	}
	if s.uploadPressure != nil {
		level, levelName, pendingBytes, pendingBlocks := s.uploadPressure.UploadPressureSnapshot()
		resp.UploadPressureLevel = level
		resp.UploadPressure = levelName
		resp.UploadPendingBytes = pendingBytes
		resp.UploadPendingBlocks = pendingBlocks
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "encode health response failed", http.StatusInternalServerError)
		return
	}
}
