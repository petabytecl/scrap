package adminui

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/a-h/templ"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/adminui/templates"
	"github.com/petabytecl/scrap/internal/api"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/operations"
)

type Options struct {
	Inspect    api.InspectApplication
	Repair     api.RepairApplication
	Operations *operations.Store
	Now        func() time.Time
}

type Handler struct {
	inspect    api.InspectApplication
	repair     api.RepairApplication
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
		repair:     options.Repair,
		operations: options.Operations,
		now:        now,
	}
	mux := http.NewServeMux()
	mux.Handle("/admin/static/", http.StripPrefix("/admin/static/", http.FileServerFS(staticFS)))
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
	h.render(w, r, templates.Shell(h.dashboard(r.Context(), "overview")))
}

func (h *Handler) partial(w http.ResponseWriter, r *http.Request) {
	view := strings.Trim(path.Clean(strings.TrimPrefix(r.URL.Path, "/admin/views/")), "/")
	if view == "." || view == "" {
		view = "overview"
	}
	data := h.dashboard(r.Context(), view)
	switch view {
	case "overview":
		h.render(w, r, templates.Overview(data))
	case "capacity":
		h.render(w, r, templates.Capacity(data))
	case "operations":
		h.render(w, r, templates.Operations(data))
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, "admin UI render failed", http.StatusInternalServerError)
	}
}

func (h *Handler) dashboard(ctx context.Context, activeView string) templates.DashboardData {
	data := templates.DashboardData{
		ActiveView:  activeView,
		GeneratedAt: h.now().Format(time.RFC3339),
	}
	data.Nav = []templates.NavItem{
		{ID: "overview", Label: "Overview", Group: "Cluster", Href: "/admin/views/overview"},
		{ID: "capacity", Label: "Capacity", Group: "Cluster", Href: "/admin/views/capacity"},
		{ID: "operations", Label: "Operations", Group: "Reliability", Href: "/admin/views/operations"},
	}
	h.loadSummary(ctx, &data)
	h.loadCapacity(ctx, &data)
	h.loadOperations(&data)
	h.loadRepair(ctx, &data)
	return data
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

func (h *Handler) loadOperations(data *templates.DashboardData) {
	if h.operations == nil {
		return
	}
	items, err := h.operations.List(operations.ListFilter{})
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
		ID:        operation.GetOperationId(),
		Type:      operation.GetOperationType(),
		State:     operation.GetState().String(),
		Requested: timestampText(operation.GetRequestedAt()),
		Completed: completed,
		Total:     total,
		Message:   message,
	}
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
