package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	readHeaderTimeout    = 10 * time.Second
	maxTestHookBodyBytes = 1024
)

type ProjectionInjector interface {
	InjectProjectionKey(ctx context.Context, txID string, blockID uint64, docCount uint16, completed bool) error
}

type Option func(*Server)

type Server struct {
	mu                 sync.Mutex
	httpSrv            *http.Server
	handler            http.Handler
	registry           *prometheus.Registry
	projectionInjector ProjectionInjector
}

func WithProjectionInjector(injector ProjectionInjector) Option {
	return func(s *Server) {
		s.projectionInjector = injector
	}
}

func New(registry *prometheus.Registry, opts ...Option) *Server {
	s := &Server{registry: registry}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if s.projectionInjector != nil {
		mux.HandleFunc("/test-hooks/projection-key", s.handleProjectionKeyHook)
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
