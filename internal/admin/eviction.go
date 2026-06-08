package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/security"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const maxEvictionPlanBodyBytes = 8 * 1024

type EvictionPlanner interface {
	CreateEvictionPlan(ctx context.Context, req eviction.PlanRequest) (eviction.Plan, error)
}

type EvictionApplier interface {
	ApplyEvictionPlan(ctx context.Context, req eviction.ApplyRequest) (eviction.ApplyResult, error)
}

type EvictionPlanStatusProvider interface {
	EvictionPlanStatus(ctx context.Context, planID string) (eviction.PlanStatus, error)
}

func WithEvictionPlanner(planner EvictionPlanner) Option {
	return func(s *Server) {
		s.evictionPlanner = planner
	}
}

func WithEvictionApplier(applier EvictionApplier) Option {
	return func(s *Server) {
		s.evictionApplier = applier
	}
}

func WithEvictionPlanStatusProvider(provider EvictionPlanStatusProvider) Option {
	return func(s *Server) {
		s.evictionPlanStatus = provider
	}
}

func (s *Server) handleEvictionPlans(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, security.RoleAdminOperator) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req eviction.PlanRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEvictionPlanBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}

	plan, err := s.evictionPlanner.CreateEvictionPlan(r.Context(), req)
	if err != nil {
		writeEvictionPlanError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(plan); err != nil {
		http.Error(w, "encode eviction plan response failed", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleEvictionPlanByID(w http.ResponseWriter, r *http.Request) {
	planID, action, ok := evictionPlanPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		s.handleEvictionPlanStatus(w, r, planID)
	case action == "apply" && r.Method == http.MethodPost:
		s.handleEvictionPlanApply(w, r, planID)
	case action == "", action == "apply":
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleEvictionPlanApply(w http.ResponseWriter, r *http.Request, planID string) {
	if !s.authorize(w, r, security.RoleAdminOperator) {
		return
	}
	if s.evictionApplier == nil {
		http.NotFound(w, r)
		return
	}
	if err := decodeEvictionApplyBody(w, r); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}

	result, err := s.evictionApplier.ApplyEvictionPlan(r.Context(), eviction.ApplyRequest{PlanID: planID})
	if err != nil {
		writeEvictionApplyError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		http.Error(w, "encode eviction apply response failed", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleEvictionPlanStatus(w http.ResponseWriter, r *http.Request, planID string) {
	if !s.authorize(w, r, security.RoleAdminReader) {
		return
	}
	if s.evictionPlanStatus == nil {
		http.NotFound(w, r)
		return
	}
	status, err := s.evictionPlanStatus.EvictionPlanStatus(r.Context(), planID)
	if err != nil {
		writeEvictionApplyError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		http.Error(w, "encode eviction plan status response failed", http.StatusInternalServerError)
		return
	}
}

func evictionPlanPath(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/admin/eviction/plans/")
	if rest == path || rest == "" {
		return "", "", false
	}
	planID, suffix, hasSuffix := strings.Cut(rest, "/")
	if planID == "" {
		return "", "", false
	}
	if !hasSuffix {
		return planID, "", true
	}
	return planID, suffix, suffix == "apply"
}

func decodeEvictionApplyBody(w http.ResponseWriter, r *http.Request) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEvictionPlanBodyBytes))
	dec.DisallowUnknownFields()
	var body struct{}
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeEvictionPlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, eviction.ErrTargetMemberMismatch):
		http.Error(w, err.Error(), http.StatusPreconditionFailed)
	case errors.Is(err, eviction.ErrInvalidPlanRequest), errors.Is(err, eviction.ErrPlanCapExceedsCeiling):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "create eviction plan failed", http.StatusInternalServerError)
	}
}

func writeEvictionApplyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, eviction.ErrPlanNotFound), errors.Is(err, eviction.ErrPlanExpired),
		errors.Is(err, eviction.ErrApplyDisabled), errors.Is(err, eviction.ErrPlanStale):
		http.Error(w, err.Error(), http.StatusPreconditionFailed)
	case errors.Is(err, eviction.ErrApplyInProgress):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, storeapi.ErrRebuilding):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, storeapi.ErrUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, eviction.ErrInvalidPlanRequest):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "apply eviction plan failed", http.StatusInternalServerError)
	}
}
