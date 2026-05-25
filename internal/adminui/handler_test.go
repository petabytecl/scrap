package adminui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/api"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
)

func TestHandlerServesOverviewAndCapacityPartials(t *testing.T) {
	handler := populatedHandler()

	overview := request(handler, "/admin/")
	requireOK(t, overview)
	overviewBody := overview.Body.String()
	requireContains(t, overviewBody, "S.C.R.A.P. Admin")
	requireContains(t, overviewBody, "4.0 KiB")
	requireContains(t, overviewBody, "2.0 KiB/day")
	requireContains(t, overviewBody, "2026-05-25T12:00:00Z")

	capacity := request(handler, "/admin/views/capacity")
	requireOK(t, capacity)
	capacityBody := capacity.Body.String()
	requireContains(t, capacityBody, "local-profile")
	requireContains(t, capacityBody, "LOW_RUNWAY")
	requireContains(t, capacityBody, "two days remaining")
}

func TestHandlerServesExpandedDashboardViewRoutes(t *testing.T) {
	handler := populatedHandler()
	tests := map[string]string{
		"/admin/views/members":  "Member inventory surface",
		"/admin/views/raft":     "Consensus evidence surface",
		"/admin/views/clients":  "Client connection coverage",
		"/admin/views/backend":  "Upload and restore evidence",
		"/admin/views/openbao":  "Envelope and key custody surface",
		"/admin/views/repair":   "Repair and corruption evidence",
		"/admin/views/overview": "Production write ACK",
	}
	for target, want := range tests {
		response := request(handler, target)
		requireOK(t, response)
		requireContains(t, response.Body.String(), want)
	}
}

func TestHandlerServesEmbeddedStaticAssets(t *testing.T) {
	response := request(NewHandler(Options{}), "/admin/static/admin.css")
	requireOK(t, response)
	requireContains(t, response.Body.String(), ".app-shell")
}

func TestHandlerReportsUnavailableInspectApplication(t *testing.T) {
	response := request(NewHandler(Options{}), "/admin/views/overview")
	requireOK(t, response)
	requireContains(t, response.Body.String(), "inspect application is unavailable")
}

func request(handler http.Handler, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func populatedHandler() http.Handler {
	return NewHandler(Options{
		Inspect: fakeInspect{
			summary: &adminv1.ClusterSummary{
				ShardCount:            2,
				StorageMemberCount:    3,
				LocalBytesUsed:        4096,
				LocalBytesCapacity:    8192,
				DegradedDocumentCount: 1,
				PendingOperationCount: 4,
			},
			runway: &adminv1.CapacityRunway{
				CapacityProfileId:    "local-profile",
				UsableBytesRemaining: 4096,
				EstimatedBytesPerDay: 2048,
				RunwayDays:           2,
				Warnings: []*adminv1.OperationWarning{
					{Code: "LOW_RUNWAY", Message: "two days remaining"},
				},
			},
		},
		Repair: fakeRepair{items: []*adminv1.RepairQueueItem{{}, {}}},
		Now:    func() time.Time { return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC) },
	})
}

func requireOK(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	const want = http.StatusOK
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body:\n%s", response.Code, want, response.Body.String())
	}
}

func requireContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("body missing %q:\n%s", want, body)
	}
}

type fakeInspect struct {
	summary *adminv1.ClusterSummary
	runway  *adminv1.CapacityRunway
}

func (f fakeInspect) GetAdminClusterSummary(context.Context) (*adminv1.ClusterSummary, error) {
	return f.summary, nil
}

func (f fakeInspect) GetAdminShard(context.Context, string) (*adminv1.Shard, error) {
	return nil, errors.New("not implemented")
}

func (f fakeInspect) GetAdminDocument(context.Context, identity.Document) (*adminv1.AdminDocument, error) {
	return nil, errors.New("not implemented")
}

func (f fakeInspect) GetAdminBlock(context.Context, api.BlockTarget) (*adminv1.Block, error) {
	return nil, errors.New("not implemented")
}

func (f fakeInspect) GetAdminMember(context.Context, string) (*adminv1.StorageMember, error) {
	return nil, errors.New("not implemented")
}

func (f fakeInspect) GetAdminCapacityRunway(context.Context, string) (*adminv1.CapacityRunway, error) {
	return f.runway, nil
}

type fakeRepair struct {
	items []*adminv1.RepairQueueItem
}

func (f fakeRepair) GetRepairQueue(context.Context, string) ([]*adminv1.RepairQueueItem, error) {
	return f.items, nil
}
