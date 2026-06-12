package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	authorizedRequest, role, ok := s.authorizeQuarantineMethod(w, r, security.RoleAdminReader, http.MethodGet)
	if !ok {
		return
	}
	r = authorizedRequest

	filter, err := parseQuarantineListFilter(r)
	if err != nil {
		if !s.recordInvalidQuarantineRequest(w, r, role, audit.OperationQuarantineList) {
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
	authorizedRequest, role, ok := s.authorizeQuarantineMethod(w, r, security.RoleAdminReader, http.MethodGet)
	if !ok {
		return
	}
	r = authorizedRequest

	identity, err := parseQuarantineIdentityQuery(r)
	if err != nil {
		if !s.recordInvalidQuarantineRequest(w, r, role, audit.OperationQuarantineInspect) {
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
	authorizedRequest, role, ok := s.authorizeQuarantineAnyMethod(
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
		if !s.recordInvalidQuarantineRequest(w, r, role, operation) {
			return
		}
		writeQuarantineError(w, quarantine.ErrInvalidRequest)
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if !s.recordInvalidQuarantineRequest(w, r, role, operation) {
			return
		}
		writeQuarantineError(w, quarantine.ErrInvalidRequest)
		return
	}
	if err := identity.Validate(); err != nil {
		if !s.recordInvalidQuarantineRequest(w, r, role, operation) {
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

func (s *Server) authorizeQuarantineMethod(
	w http.ResponseWriter,
	r *http.Request,
	role security.Role,
	method string,
) (*http.Request, security.Role, bool) {
	return s.authorizeQuarantineAnyMethod(w, r, []security.Role{role}, method)
}

func (s *Server) authorizeQuarantineAnyMethod(
	w http.ResponseWriter,
	r *http.Request,
	roles []security.Role,
	method string,
) (*http.Request, security.Role, bool) {
	role := security.RoleUnknown
	if len(roles) > 0 {
		role = roles[0]
	}
	operation, target := auditRequest(r, role)
	resolvedRequest, principalErr := s.requestWithResolvedPrincipal(r)
	if !s.allowQuarantineRateLimit(w, resolvedRequest, role, operation, target) {
		return nil, role, false
	}
	if principalErr != nil {
		s.writeQuarantineAuthDenied(w, resolvedRequest, role, operation, target, principalErr)
		return nil, role, false
	}
	if s.authorizer != nil {
		authorizedRole, err := s.authorizeAnyRole(resolvedRequest.Context(), roles)
		role = authorizedRole
		if err != nil {
			s.writeQuarantineAuthDenied(w, resolvedRequest, role, operation, target, err)
			return nil, role, false
		}
	}
	if method != "" && r.Method != method {
		s.writeQuarantineMethodDenied(w, resolvedRequest, role, operation, target)
		return nil, role, false
	}
	if !s.recordQuarantineAudit(w, resolvedRequest, role, operation, target, audit.ResultAllowed, audit.ReasonAllowed) {
		return nil, role, false
	}
	return resolvedRequest, role, true
}

func (s *Server) allowQuarantineRateLimit(
	w http.ResponseWriter,
	r *http.Request,
	role security.Role,
	operation string,
	target string,
) bool {
	if decision := s.checkRateLimit(r.Context(), operation); !decision.Limited {
		return true
	}
	if !s.recordQuarantineAudit(w, r, role, operation, target, audit.ResultRateLimited, audit.ReasonRateLimited) {
		return false
	}
	writeQuarantineJSON(w, http.StatusTooManyRequests, quarantine.Result{
		Status: quarantine.StatusFailed,
		Reason: quarantine.ReasonRateLimited,
	})
	return false
}

func (s *Server) writeQuarantineAuthDenied(
	w http.ResponseWriter,
	r *http.Request,
	role security.Role,
	operation string,
	target string,
	err error,
) {
	if !s.recordQuarantineAudit(w, r, role, operation, target, audit.ResultDenied, s.auditReasonForError(err)) {
		return
	}
	writeQuarantineJSON(w, security.HTTPStatusForAuthorization(err), quarantine.Result{
		Status: quarantine.StatusFailed,
		Reason: quarantineAuthReasonForError(err),
	})
}

func (s *Server) writeQuarantineMethodDenied(
	w http.ResponseWriter,
	r *http.Request,
	role security.Role,
	operation string,
	target string,
) {
	if !s.recordQuarantineAudit(w, r, role, operation, target, audit.ResultDenied, audit.ReasonMethodNotAllowed) {
		return
	}
	writeQuarantineJSON(w, http.StatusMethodNotAllowed, quarantine.Result{
		Status: quarantine.StatusFailed,
		Reason: quarantine.ReasonMethodNotAllowed,
	})
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
	txID, err := singleQuarantineQueryValue(query, "transaction_id")
	if err != nil {
		return quarantine.ListFilter{}, err
	}
	rawLimit, err := singleQuarantineQueryValue(query, "limit")
	if err != nil {
		return quarantine.ListFilter{}, err
	}
	filter := quarantine.ListFilter{TransactionID: txID}
	if rawLimit != "" {
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
	txID, err := singleQuarantineQueryValue(query, "transaction_id")
	if err != nil {
		return quarantine.Identity{}, err
	}
	docName, err := singleQuarantineQueryValue(query, "document_name")
	if err != nil {
		return quarantine.Identity{}, err
	}
	identity := quarantine.Identity{
		TransactionID: txID,
		DocumentName:  docName,
	}
	if err := identity.Validate(); err != nil {
		return quarantine.Identity{}, err
	}
	return identity, nil
}

func singleQuarantineQueryValue(query map[string][]string, key string) (string, error) {
	values := query[key]
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 {
		return "", quarantine.ErrInvalidRequest
	}
	return values[0], nil
}

func (s *Server) recordInvalidQuarantineRequest(
	w http.ResponseWriter,
	r *http.Request,
	role security.Role,
	operation string,
) bool {
	return s.recordQuarantineAudit(w, r, role, operation, audit.TargetDocument, audit.ResultFailed, audit.ReasonInvalidRequest)
}

func (s *Server) recordQuarantineAudit(
	w http.ResponseWriter,
	r *http.Request,
	role security.Role,
	operation string,
	target string,
	result string,
	reason string,
) bool {
	if err := s.recordAudit(r.Context(), role, operation, target, result, reason); err != nil {
		writeQuarantineJSON(w, http.StatusInternalServerError, quarantine.Result{
			Status: quarantine.StatusFailed,
			Reason: quarantine.ReasonAuditFailed,
		})
		return false
	}
	return true
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
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"status":"failed","reason":"internal_error","changed":false}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
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
	case errors.Is(err, storeapi.ErrDataLoss):
		return quarantine.ReasonDataLoss
	case errors.Is(err, storeapi.ErrFailedPrecondition):
		return quarantine.ReasonFailedPrecondition
	default:
		return quarantine.ReasonInternalError
	}
}

func quarantineAuthReasonForError(err error) string {
	switch {
	case errors.Is(err, security.ErrUnauthenticated):
		return quarantine.ReasonUnauthenticated
	case errors.Is(err, security.ErrPermissionDenied):
		return quarantine.ReasonPermissionDenied
	default:
		return quarantine.ReasonInternalError
	}
}
