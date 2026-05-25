package adminui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/a-h/templ"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/adminui/templates"
	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/appstatus"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/operations"
)

const warningRunwayDays = 7

const (
	operationStateAll       = "all"
	operationStatePlanned   = "planned"
	operationStateQueued    = "queued"
	operationStateRunning   = "running"
	operationStateSucceeded = "succeeded"
	operationStateFailed    = "failed"
	operationStateCanceled  = "canceled"
	operationStateExpired   = "expired"
)

type Options struct {
	Inspect    api.InspectApplication
	Member     api.MemberApplication
	Repair     api.RepairApplication
	DR         api.DisasterRecoveryApplication
	Operations *operations.Store
	Now        func() time.Time
}

type Handler struct {
	inspect    api.InspectApplication
	member     api.MemberApplication
	repair     api.RepairApplication
	dr         api.DisasterRecoveryApplication
	operations *operations.Store
	now        func() time.Time
}

func NewHandler(options Options) http.Handler {
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	handler := &Handler{
		inspect:    options.Inspect,
		member:     options.Member,
		repair:     options.Repair,
		dr:         options.DR,
		operations: options.Operations,
		now:        now,
	}
	mux := http.NewServeMux()
	mux.Handle("/admin/static/", http.StripPrefix("/admin/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("/admin/operations/", handler.operationDetail)
	mux.HandleFunc("/admin/members/", handler.memberDetail)
	mux.HandleFunc("/admin/clients/", handler.clientDetail)
	mux.HandleFunc("/", handler.redirectRoot)
	mux.HandleFunc("/admin/", handler.shell)
	mux.HandleFunc("/admin/views/", handler.partial)
	return mux
}

func (h *Handler) redirectRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

func (h *Handler) shell(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" && r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	h.render(w, r, templates.Shell(h.dashboard(r.Context(), "overview", operationFilter{})))
}

func (h *Handler) partial(w http.ResponseWriter, r *http.Request) {
	view := strings.Trim(path.Clean(strings.TrimPrefix(r.URL.Path, "/admin/views/")), "/")
	if view == "." || view == "" {
		view = "overview"
	}
	component := partialComponent(view)
	if component == nil {
		http.NotFound(w, r)
		return
	}
	filter, err := operationFilterFromRequest(r, view)
	if err != nil {
		http.Error(w, "invalid operation state filter", http.StatusBadRequest)
		return
	}
	h.render(w, r, component(h.dashboard(r.Context(), view, filter)))
}

func (h *Handler) operationDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.operations == nil {
		http.Error(w, "operation store unavailable", http.StatusServiceUnavailable)
		return
	}
	operationID, ok := operationIDFromDetailPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	operation, err := h.operations.Get(operationID)
	if errors.Is(err, operations.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "operation detail unavailable", http.StatusInternalServerError)
		return
	}
	h.render(w, r, templates.OperationDetail(operationData(operation)))
}

func (h *Handler) memberDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	memberID, tab, tabOnly, ok := memberPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if h.inspect == nil {
		http.Error(w, "inspect application unavailable", http.StatusServiceUnavailable)
		return
	}
	if !validMemberTab(tab) {
		http.NotFound(w, r)
		return
	}
	detail, err := h.loadMemberDetail(r.Context(), memberID, tab)
	if isAppNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "member detail unavailable", http.StatusInternalServerError)
		return
	}
	if tabOnly {
		h.render(w, r, templates.MemberTabContent(detail))
		return
	}
	data := h.dashboard(r.Context(), "members", operationFilter{})
	data.MemberDetail = detail
	h.render(w, r, templates.MemberDetail(data))
}

func (h *Handler) clientDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clientID, tab, tabOnly, ok := clientPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !validClientTab(tab) {
		http.NotFound(w, r)
		return
	}
	detail := clientDetailData(clientID, tab)
	if tabOnly {
		h.render(w, r, templates.ClientTabContent(detail))
		return
	}
	data := h.dashboard(r.Context(), "clients", operationFilter{})
	data.ClientDetail = detail
	h.render(w, r, templates.ClientDetail(data))
}

func operationIDFromDetailPath(requestPath string) (string, bool) {
	cleaned := strings.Trim(path.Clean(strings.TrimPrefix(requestPath, "/admin/operations/")), "/")
	operationID, suffix, ok := strings.Cut(cleaned, "/")
	if !ok || suffix != "detail" {
		return "", false
	}
	if operationID == "." || operationID == "" || strings.Contains(operationID, "/") {
		return "", false
	}
	unescaped, err := url.PathUnescape(operationID)
	if err != nil || unescaped == "" || strings.Contains(unescaped, "/") {
		return "", false
	}
	return unescaped, true
}

func clientPath(requestPath string) (clientID, tab string, tabOnly, ok bool) {
	cleaned := strings.Trim(path.Clean(strings.TrimPrefix(requestPath, "/admin/clients/")), "/")
	rawClientID, suffix, hasSuffix := strings.Cut(cleaned, "/")
	parsedClientID, ok := pathSegment(rawClientID)
	if !ok {
		return "", "", false, false
	}
	if !hasSuffix || suffix == "" {
		return parsedClientID, templates.ClientTabOverview, false, true
	}
	rawTabPrefix, rawTab, hasTab := strings.Cut(suffix, "/")
	if !hasTab || rawTabPrefix != "tab" {
		return "", "", false, false
	}
	parsedTab, ok := pathSegment(rawTab)
	if !ok {
		return "", "", false, false
	}
	return parsedClientID, parsedTab, true, true
}

func memberPath(requestPath string) (memberID, tab string, tabOnly, ok bool) {
	cleaned := strings.Trim(path.Clean(strings.TrimPrefix(requestPath, "/admin/members/")), "/")
	rawMemberID, suffix, hasSuffix := strings.Cut(cleaned, "/")
	parsedMemberID, ok := pathSegment(rawMemberID)
	if !ok {
		return "", "", false, false
	}
	if !hasSuffix || suffix == "" {
		return parsedMemberID, templates.MemberTabOverview, false, true
	}
	rawTabPrefix, rawTab, hasTab := strings.Cut(suffix, "/")
	if !hasTab || rawTabPrefix != "tab" {
		return "", "", false, false
	}
	parsedTab, ok := pathSegment(rawTab)
	if !ok {
		return "", "", false, false
	}
	return parsedMemberID, parsedTab, true, true
}

func pathSegment(raw string) (string, bool) {
	if raw == "." || raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	unescaped, err := url.PathUnescape(raw)
	if err != nil || unescaped == "" || strings.Contains(unescaped, "/") {
		return "", false
	}
	return unescaped, true
}

func validMemberTab(tab string) bool {
	for _, item := range templates.MemberTabs() {
		if item.ID == tab {
			return true
		}
	}
	return false
}

func validClientTab(tab string) bool {
	for _, item := range templates.ClientTabs() {
		if item.ID == tab {
			return true
		}
	}
	return false
}

func partialComponent(view string) func(templates.DashboardData) templ.Component {
	components := map[string]func(templates.DashboardData) templ.Component{
		"overview":   templates.Overview,
		"capacity":   templates.Capacity,
		"members":    templates.Members,
		"raft":       templates.Raft,
		"clients":    templates.Clients,
		"backend":    templates.Backend,
		"openbao":    templates.OpenBao,
		"operations": templates.Operations,
		"repair":     templates.Repair,
	}
	return components[view]
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, "admin UI render failed", http.StatusInternalServerError)
	}
}

func (h *Handler) dashboard(ctx context.Context, activeView string, filter operationFilter) templates.DashboardData {
	data := templates.DashboardData{
		ActiveView:       activeView,
		GeneratedAt:      h.now().Format(time.RFC3339),
		OperationFilter:  filter.ID,
		OperationFilters: operationFilters(),
		MemberTabs:       templates.MemberTabs(),
		ClientTabs:       templates.ClientTabs(),
	}
	data.Nav = []templates.NavItem{
		{ID: "overview", Label: "Overview", Group: "Cluster", Href: "/admin/views/overview"},
		{ID: "capacity", Label: "Capacity", Group: "Cluster", Href: "/admin/views/capacity"},
		{ID: "members", Label: "Members", Group: "Infrastructure", Href: "/admin/views/members"},
		{ID: "raft", Label: "Raft consensus", Group: "Infrastructure", Href: "/admin/views/raft"},
		{ID: "clients", Label: "Service mesh", Group: "Data plane", Href: "/admin/views/clients"},
		{ID: "backend", Label: "Backend", Group: "Data plane", Href: "/admin/views/backend"},
		{ID: "openbao", Label: "OpenBao", Group: "Data plane", Href: "/admin/views/openbao"},
		{ID: "operations", Label: "Operations", Group: "Reliability", Href: "/admin/views/operations"},
		{ID: "repair", Label: "Repair", Group: "Reliability", Href: "/admin/views/repair"},
	}
	h.loadSummary(ctx, &data)
	h.loadCapacity(ctx, &data)
	h.loadOperations(&data, filter)
	h.loadRepair(ctx, &data)
	h.loadRecovery(ctx, &data)
	data.Signals = readinessSignals(data)
	return data
}

func (h *Handler) loadMemberDetail(ctx context.Context, memberID, tab string) (templates.MemberDetailData, error) {
	member, err := h.inspect.GetAdminMember(ctx, memberID)
	if err != nil {
		return templates.MemberDetailData{}, err
	}
	var safety *adminv1.EvictionSafety
	safetyError := ""
	if h.member == nil {
		safetyError = "member application is unavailable"
	} else {
		safety, err = h.member.GetEvictionSafety(ctx, memberID)
		if isAppNotFound(err) {
			return templates.MemberDetailData{}, err
		}
		if err != nil {
			safetyError = "eviction safety unavailable"
		}
	}
	return memberDetailData(member, safety, safetyError, tab), nil
}

func (h *Handler) loadSummary(ctx context.Context, data *templates.DashboardData) {
	if h.inspect == nil {
		data.Errors = append(data.Errors, "inspect application is unavailable")
		return
	}
	summary, err := h.inspect.GetAdminClusterSummary(ctx)
	if err != nil {
		data.Errors = append(data.Errors, "cluster summary: "+err.Error())
		return
	}
	data.Summary = templates.SummaryData{
		ShardCount:            summary.GetShardCount(),
		StorageMemberCount:    summary.GetStorageMemberCount(),
		LocalBytesUsed:        summary.GetLocalBytesUsed(),
		LocalBytesCapacity:    summary.GetLocalBytesCapacity(),
		DegradedDocumentCount: summary.GetDegradedDocumentCount(),
		PendingOperationCount: summary.GetPendingOperationCount(),
	}
}

func (h *Handler) loadCapacity(ctx context.Context, data *templates.DashboardData) {
	if h.inspect == nil {
		return
	}
	runway, err := h.inspect.GetAdminCapacityRunway(ctx, "")
	if err != nil {
		data.Errors = append(data.Errors, "capacity runway: "+err.Error())
		return
	}
	data.Capacity = capacityData(runway)
}

func capacityData(runway *adminv1.CapacityRunway) templates.CapacityData {
	if runway == nil {
		return templates.CapacityData{}
	}
	warnings := make([]templates.WarningData, 0, len(runway.GetWarnings()))
	for _, warning := range runway.GetWarnings() {
		warnings = append(warnings, templates.WarningData{
			Code:    warning.GetCode(),
			Message: warning.GetMessage(),
		})
	}
	return templates.CapacityData{
		ProfileID:              runway.GetCapacityProfileId(),
		UsableBytesRemaining:   runway.GetUsableBytesRemaining(),
		EstimatedBytesPerDay:   runway.GetEstimatedBytesPerDay(),
		RunwayDays:             runway.GetRunwayDays(),
		Warnings:               warnings,
		HasMeasuredIngressRate: runway.GetEstimatedBytesPerDay() > 0,
	}
}

func memberDetailData(member *adminv1.StorageMember, safety *adminv1.EvictionSafety, safetyError, tab string) templates.MemberDetailData {
	if member == nil {
		return templates.MemberDetailData{Tab: tab, Tabs: templates.MemberTabs()}
	}
	warnings := []templates.WarningData(nil)
	if safety != nil {
		warnings = warningsData(safety.GetWarnings())
	}
	return templates.MemberDetailData{
		ID:            member.GetStorageMemberId(),
		CellID:        member.GetCellId(),
		State:         member.GetState().String(),
		Cordoned:      member.GetCordoned(),
		BytesUsed:     member.GetBytesUsed(),
		BytesCapacity: member.GetBytesCapacity(),
		LastSeen:      timestampText(member.GetLastSeenAt()),
		Tab:           tab,
		Tabs:          templates.MemberTabs(),
		Eviction: templates.EvictionSafetyData{
			Known:       safety != nil,
			SafeToEvict: safety.GetSafeToEvict(),
			Error:       safetyError,
			Warnings:    warnings,
		},
	}
}

func clientDetailData(clientID, tab string) templates.ClientDetailData {
	return templates.ClientDetailData{
		ID:   clientID,
		Tab:  tab,
		Tabs: templates.ClientTabs(),
	}
}

func warningsData(warnings []*adminv1.OperationWarning) []templates.WarningData {
	result := make([]templates.WarningData, 0, len(warnings))
	for _, warning := range warnings {
		result = append(result, templates.WarningData{
			Code:    warning.GetCode(),
			Message: warning.GetMessage(),
		})
	}
	return result
}

func (h *Handler) loadOperations(data *templates.DashboardData, filter operationFilter) {
	if h.operations == nil {
		return
	}
	items, err := h.operations.List(operations.ListFilter{States: filter.States})
	if err != nil {
		data.Errors = append(data.Errors, "operations: "+err.Error())
		return
	}
	data.Operations = make([]templates.OperationData, 0, len(items))
	for _, item := range items {
		data.Operations = append(data.Operations, operationData(item))
	}
}

func operationData(operation *adminv1.Operation) templates.OperationData {
	if operation == nil {
		return templates.OperationData{}
	}
	progress := operation.GetProgress()
	completed := uint64(0)
	total := uint64(0)
	message := ""
	if progress != nil {
		completed = progress.GetWorkUnitsCompleted()
		total = progress.GetWorkUnitsTotal()
		message = progress.GetMessage()
	}
	return templates.OperationData{
		ID:          operation.GetOperationId(),
		Type:        operation.GetOperationType(),
		State:       operation.GetState().String(),
		RequestedBy: operation.GetRequestedByIdentity(),
		Requested:   timestampText(operation.GetRequestedAt()),
		Started:     timestampText(operation.GetStartedAt()),
		Finished:    timestampText(operation.GetFinishedAt()),
		DryRun:      operation.GetDryRun(),
		Completed:   completed,
		Total:       total,
		Message:     message,
	}
}

type operationFilter struct {
	ID     string
	States []adminv1.OperationState
}

func operationFilterFromRequest(r *http.Request, view string) (operationFilter, error) {
	if view != "operations" {
		return operationFilter{}, nil
	}
	return parseOperationFilter(r.URL.Query().Get("state"))
}

func parseOperationFilter(value string) (operationFilter, error) {
	filterID := strings.ToLower(strings.TrimSpace(value))
	if filterID == "" {
		filterID = operationStateAll
	}
	for _, option := range operationFilterOptions() {
		if option.ID == filterID {
			return operationFilter{ID: filterID, States: option.States}, nil
		}
	}
	return operationFilter{}, errors.New("unknown operation state filter")
}

func operationFilters() []templates.OperationFilter {
	options := operationFilterOptions()
	filters := make([]templates.OperationFilter, 0, len(options))
	for _, option := range options {
		filters = append(filters, templates.OperationFilter{
			ID:    option.ID,
			Label: option.Label,
			Href:  option.Href(),
		})
	}
	return filters
}

func operationFilterOptions() []operationFilterOption {
	return []operationFilterOption{
		{ID: operationStateAll, Label: "All"},
		{ID: operationStatePlanned, Label: "Planned", States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_PLANNED}},
		{ID: operationStateQueued, Label: "Queued", States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_QUEUED}},
		{ID: operationStateRunning, Label: "Running", States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_RUNNING}},
		{ID: operationStateSucceeded, Label: "Succeeded", States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_SUCCEEDED}},
		{ID: operationStateFailed, Label: "Failed", States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_FAILED}},
		{ID: operationStateCanceled, Label: "Canceled", States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_CANCELED}},
		{ID: operationStateExpired, Label: "Expired", States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_EXPIRED}},
	}
}

type operationFilterOption struct {
	ID     string
	Label  string
	States []adminv1.OperationState
}

func (o operationFilterOption) Href() string {
	if o.ID == operationStateAll {
		return "/admin/views/operations"
	}
	return "/admin/views/operations?state=" + o.ID
}

func (h *Handler) loadRepair(ctx context.Context, data *templates.DashboardData) {
	if h.repair == nil {
		return
	}
	items, err := h.repair.GetRepairQueue(ctx, "")
	if err != nil && !errors.Is(err, context.Canceled) {
		data.Errors = append(data.Errors, "repair queue: "+err.Error())
		return
	}
	data.RepairQueueCount = len(items)
}

func (h *Handler) loadRecovery(ctx context.Context, data *templates.DashboardData) {
	if h.dr == nil {
		return
	}
	readiness, err := h.dr.GetRecoveryReadiness(ctx)
	if err != nil {
		data.Recovery = templates.RecoveryData{
			Known: true,
			Error: "recovery readiness: " + err.Error(),
		}
		return
	}
	data.Recovery = recoveryData(readiness)
}

func recoveryData(readiness *adminv1.RecoveryReadiness) templates.RecoveryData {
	if readiness == nil {
		return templates.RecoveryData{Known: true, Error: "recovery readiness unavailable"}
	}
	return templates.RecoveryData{
		Known:                        true,
		Ready:                        readiness.GetReady(),
		LatestRestorableCheckpointAt: timestampText(readiness.GetLatestRestorableCheckpointAt()),
		Warnings:                     warningsData(readiness.GetWarnings()),
	}
}

func timestampText(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	t := ts.AsTime()
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func isAppNotFound(err error) bool {
	var appErr *appstatus.Error
	return errors.As(err, &appErr) && appErr.Code == appstatus.CodeNotFound
}

func readinessSignals(data templates.DashboardData) []templates.ReadinessSignal {
	signals := []templates.ReadinessSignal{
		{
			Name:   "Write admission",
			State:  "advisory",
			Detail: "production write ACK evidence remains in the release gate",
		},
		{
			Name:   "Disk runway",
			State:  runwayState(data.Capacity),
			Detail: runwayDetail(data.Capacity),
		},
		{
			Name:   "Backend lag",
			State:  "unknown",
			Detail: "backend upload lag requires live deployment evidence",
		},
		{
			Name:   "Repair lag",
			State:  repairState(data),
			Detail: repairDetail(data),
		},
		{
			Name:   "Restore lag",
			State:  recoveryState(data.Recovery),
			Detail: recoveryDetail(data.Recovery),
		},
		{
			Name:   "Corruption incidents",
			State:  degradedState(data.Summary.DegradedDocumentCount),
			Detail: degradedDetail(data.Summary.DegradedDocumentCount),
		},
		{
			Name:   "Raft health",
			State:  memberState(data.Summary.StorageMemberCount),
			Detail: memberDetail(data.Summary.StorageMemberCount),
		},
		{
			Name:   "OpenBao health",
			State:  "external",
			Detail: "OpenBao release evidence is collected outside this local UI",
		},
	}
	return signals
}

func recoveryState(recovery templates.RecoveryData) string {
	switch {
	case !recovery.Known:
		return "unknown"
	case recovery.Error != "":
		return "warning"
	case recovery.Ready:
		return "healthy"
	default:
		return "warning"
	}
}

func recoveryDetail(recovery templates.RecoveryData) string {
	switch {
	case !recovery.Known:
		return "disaster recovery readiness application is unavailable"
	case recovery.Error != "":
		return recovery.Error
	case recovery.Ready && recovery.LatestRestorableCheckpointAt != "":
		return "latest restorable checkpoint " + recovery.LatestRestorableCheckpointAt
	case recovery.Ready:
		return "recovery artifacts are ready"
	case len(recovery.Warnings) > 0:
		return recovery.Warnings[0].Message
	default:
		return "recovery readiness has not been established"
	}
}

func runwayState(capacity templates.CapacityData) string {
	if !capacity.HasMeasuredIngressRate {
		return "unknown"
	}
	if capacity.RunwayDays < warningRunwayDays {
		return "warning"
	}
	return "healthy"
}

func runwayDetail(capacity templates.CapacityData) string {
	if !capacity.HasMeasuredIngressRate {
		return "ingress rate not measured locally"
	}
	return "estimated runway " + templates.NumberText(uint64(capacity.RunwayDays)) + " days"
}

func repairState(data templates.DashboardData) string {
	if data.RepairQueueCount > 0 || data.Summary.DegradedDocumentCount > 0 {
		return "warning"
	}
	return "healthy"
}

func repairDetail(data templates.DashboardData) string {
	if data.RepairQueueCount == 0 {
		return "repair queue empty"
	}
	return templates.CountText(data.RepairQueueCount) + " queued repair item(s)"
}

func degradedState(count uint32) string {
	if count > 0 {
		return "warning"
	}
	return "healthy"
}

func degradedDetail(count uint32) string {
	if count == 0 {
		return "no degraded documents reported"
	}
	return templates.NumberText(uint64(count)) + " degraded document(s) reported"
}

func memberState(count uint32) string {
	if count == 0 {
		return "unknown"
	}
	return "healthy"
}

func memberDetail(count uint32) string {
	if count == 0 {
		return "no storage members reported by local summary"
	}
	return templates.NumberText(uint64(count)) + " storage member(s) visible"
}
