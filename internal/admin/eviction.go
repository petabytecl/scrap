package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/petabytecl/scrap/internal/eviction"
)

const maxEvictionPlanBodyBytes = 8 * 1024

type EvictionPlanner interface {
	CreateEvictionPlan(ctx context.Context, req eviction.PlanRequest) (eviction.Plan, error)
}

func WithEvictionPlanner(planner EvictionPlanner) Option {
	return func(s *Server) {
		s.evictionPlanner = planner
	}
}

func (s *Server) handleEvictionPlans(w http.ResponseWriter, r *http.Request) {
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
