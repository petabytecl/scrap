package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/rewrap"
	"github.com/petabytecl/scrap/internal/security"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const maxRewrapBodyBytes = 4 * 1024

type RewrapService interface {
	RewrapDocument(context.Context, rewrap.Request) (rewrap.Result, error)
	RewrapHealthSnapshot() rewrap.HealthSnapshot
}

func WithRewrapService(service RewrapService) Option {
	return func(s *Server) {
		s.rewrapService = service
	}
}

func (s *Server) handleRewrapDocument(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMethod(w, r, security.RoleAdminOperator, http.MethodPost) {
		return
	}

	var req rewrap.Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRewrapBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}

	result, err := s.rewrapService.RewrapDocument(r.Context(), req)
	if err != nil {
		if !s.recordFailedOperation(w, r, security.RoleAdminOperator, audit.OperationRewrapDocument, audit.TargetDocument) {
			return
		}
		writeRewrapError(w, result, err)
		return
	}

	writeRewrapResult(w, http.StatusOK, result)
}

func writeRewrapError(w http.ResponseWriter, result rewrap.Result, err error) {
	if result.Status == "" {
		result.Status = rewrap.StatusFailed
		result.Reason = rewrap.ReasonInternalError
	}
	writeRewrapResult(w, rewrapHTTPStatus(err), result)
}

func writeRewrapResult(w http.ResponseWriter, status int, result rewrap.Result) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		http.Error(w, "encode rewrap response failed", http.StatusInternalServerError)
		return
	}
}

func rewrapHTTPStatus(err error) int {
	switch {
	case errors.Is(err, rewrap.ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, rewrap.ErrStaleEnvelope):
		return http.StatusConflict
	case isNotLeader(err):
		return http.StatusServiceUnavailable
	case errors.Is(err, rewrap.ErrNotEncrypted), errors.Is(err, storeapi.ErrDataLoss):
		return http.StatusPreconditionFailed
	case errors.Is(err, storeapi.ErrTxNotFound), errors.Is(err, storeapi.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, storeapi.ErrUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func isNotLeader(err error) bool {
	var notLeader *storeapi.NotLeaderError
	return errors.As(err, &notLeader)
}
