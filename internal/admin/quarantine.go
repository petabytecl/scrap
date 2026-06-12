package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/quarantine"
	"github.com/petabytecl/scrap/internal/security"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const maxQuarantineBodyBytes = 4 * 1024

type QuarantineService interface {
	ListContentQuarantines(context.Context, quarantine.ListFilter) ([]quarantine.Record, error)
	InspectContentQuarantine(context.Context, quarantine.Identity) (quarantine.Record, error)
	ConfirmContentQuarantine(context.Context, quarantine.Identity) (quarantine.Result, error)
	ReleaseContentQuarantine(context.Context, quarantine.Identity) (quarantine.Result, error)
}

func WithQuarantineService(service QuarantineService) Option {
	return func(s *Server) {
		s.quarantineService = service
	}
}

func (s *Server) handleQuarantineDocuments(w http.ResponseWriter, r *http.Request) {
	authorizedRequest, ok := s.authorizeMethod(w, r, security.RoleAdminReader, http.MethodGet)
	if !ok {
		return
	}
	r = authorizedRequest

	filter, err := parseQuarantineListFilter(r)
	if err != nil {
		if !s.recordFailedOperation(w, r, security.RoleAdminReader, audit.OperationQuarantineList, audit.TargetDocument) {
			return
		}
		writeQuarantineError(w, err)
		return
	}
	records, err := s.quarantineService.ListContentQuarantines(r.Context(), filter)
	if err != nil {
		if !s.recordFailedOperation(w, r, security.RoleAdminReader, audit.OperationQuarantineList, audit.TargetDocument) {
			return
		}
		writeQuarantineError(w, err)
		return
	}
	writeQuarantineJSON(w, http.StatusOK, quarantineListResponse{Documents: records})
}

func (s *Server) handleQuarantineDocument(w http.ResponseWriter, r *http.Request) {
	authorizedRequest, ok := s.authorizeMethod(w, r, security.RoleAdminReader, http.MethodGet)
	if !ok {
		return
	}
	r = authorizedRequest

	identity, err := parseQuarantineIdentityQuery(r)
	if err != nil {
		if !s.recordFailedOperation(w, r, security.RoleAdminReader, audit.OperationQuarantineInspect, audit.TargetDocument) {
			return
		}
		writeQuarantineError(w, err)
		return
	}
	record, err := s.quarantineService.InspectContentQuarantine(r.Context(), identity)
	if err != nil {
		if !s.recordFailedOperation(w, r, security.RoleAdminReader, audit.OperationQuarantineInspect, audit.TargetDocument) {
			return
		}
		writeQuarantineError(w, err)
		return
	}
	writeQuarantineJSON(w, http.StatusOK, record)
}

func (s *Server) handleQuarantineConfirm(w http.ResponseWriter, r *http.Request) {
	s.handleQuarantineDecision(w, r, audit.OperationQuarantineConfirm, s.quarantineService.ConfirmContentQuarantine)
}

func (s *Server) handleQuarantineRelease(w http.ResponseWriter, r *http.Request) {
	s.handleQuarantineDecision(w, r, audit.OperationQuarantineRelease, s.quarantineService.ReleaseContentQuarantine)
}

func (s *Server) handleQuarantineDecision(
	w http.ResponseWriter,
	r *http.Request,
	operation string,
	apply func(context.Context, quarantine.Identity) (quarantine.Result, error),
) {
	authorizedRequest, role, ok := s.authorizeAnyMethod(
		w,
		r,
		[]security.Role{security.RoleAdminOperator, security.RoleAdminBreakGlass},
		http.MethodPost,
	)
	if !ok {
		return
	}
	r = authorizedRequest

	var identity quarantine.Identity
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuarantineBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&identity); err != nil {
		if !s.recordFailedOperation(w, r, role, operation, audit.TargetDocument) {
			return
		}
		writeQuarantineError(w, quarantine.ErrInvalidRequest)
		return
	}
	result, err := apply(r.Context(), identity)
	if err != nil {
		if !s.recordFailedOperation(w, r, role, operation, audit.TargetDocument) {
			return
		}
		writeQuarantineResultError(w, result, err)
		return
	}
	writeQuarantineJSON(w, http.StatusOK, result)
}

type quarantineListResponse struct {
	Documents []quarantine.Record `json:"documents"`
}

func parseQuarantineListFilter(r *http.Request) (quarantine.ListFilter, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "transaction_id" && key != "limit" {
			return quarantine.ListFilter{}, quarantine.ErrInvalidRequest
		}
	}
	filter := quarantine.ListFilter{TransactionID: query.Get("transaction_id")}
	if rawLimit := query.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return quarantine.ListFilter{}, quarantine.ErrInvalidRequest
		}
		filter.Limit = limit
	}
	return filter.Validate()
}

func parseQuarantineIdentityQuery(r *http.Request) (quarantine.Identity, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "transaction_id" && key != "document_name" {
			return quarantine.Identity{}, quarantine.ErrInvalidRequest
		}
	}
	identity := quarantine.Identity{
		TransactionID: query.Get("transaction_id"),
		DocumentName:  query.Get("document_name"),
	}
	if err := identity.Validate(); err != nil {
		return quarantine.Identity{}, err
	}
	return identity, nil
}

func writeQuarantineResultError(w http.ResponseWriter, result quarantine.Result, err error) {
	if result.Status == "" {
		result.Status = quarantine.StatusFailed
		result.Reason = quarantineReasonForError(err)
	}
	writeQuarantineJSON(w, quarantineHTTPStatus(err), result)
}

func writeQuarantineError(w http.ResponseWriter, err error) {
	writeQuarantineJSON(w, quarantineHTTPStatus(err), quarantine.Result{
		Status: quarantine.StatusFailed,
		Reason: quarantineReasonForError(err),
	})
}

func writeQuarantineJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, `{"status":"failed","reason":"internal_error"}`, http.StatusInternalServerError)
		return
	}
}

func quarantineHTTPStatus(err error) int {
	switch {
	case errors.Is(err, quarantine.ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, quarantine.ErrNotFound), errors.Is(err, storeapi.ErrTxNotFound), errors.Is(err, storeapi.ErrNotFound):
		return http.StatusNotFound
	case isNotLeader(err), errors.Is(err, storeapi.ErrUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, storeapi.ErrDataLoss), errors.Is(err, storeapi.ErrFailedPrecondition):
		return http.StatusPreconditionFailed
	default:
		return http.StatusInternalServerError
	}
}

func quarantineReasonForError(err error) string {
	switch {
	case err == nil:
		return quarantine.ReasonOK
	case errors.Is(err, quarantine.ErrInvalidRequest):
		return quarantine.ReasonInvalidRequest
	case errors.Is(err, quarantine.ErrNotFound), errors.Is(err, storeapi.ErrTxNotFound), errors.Is(err, storeapi.ErrNotFound):
		return quarantine.ReasonNotFound
	case isNotLeader(err):
		return quarantine.ReasonNotLeader
	case errors.Is(err, storeapi.ErrUnavailable):
		return quarantine.ReasonUnavailable
	case errors.Is(err, storeapi.ErrDataLoss), errors.Is(err, storeapi.ErrFailedPrecondition):
		return quarantine.ReasonDataLoss
	default:
		return quarantine.ReasonInternalError
	}
}
