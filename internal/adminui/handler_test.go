package adminui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/appstatus"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/testutil"
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
	requireContains(t, overviewBody, "data-tweaks-toggle")
	requireContains(t, overviewBody, "data-tweak-refresh")
	requireContains(t, overviewBody, "data-tweak-accent=\"#0093d0\"")
	requireContains(t, overviewBody, "data-compact-value")

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

func TestHandlerRendersOperationsFromStore(t *testing.T) {
	store, err := operations.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open operations store")
	defer func() { testutil.RequireNoErrorf(t, store.Close(), "close operations store") }()
	requestedAt := time.Date(2026, 5, 25, 13, 0, 0, 0, time.UTC)
	testutil.RequireNoErrorf(t, store.Put(&adminv1.Operation{
		OperationId:         "op-1",
		OperationType:       "repair",
		State:               adminv1.OperationState_OPERATION_STATE_RUNNING,
		RequestedByIdentity: "operator-a",
		RequestedAt:         timestamppb.New(requestedAt),
		StartedAt:           timestamppb.New(requestedAt.Add(time.Minute)),
		DryRun:              true,
		Progress: &adminv1.OperationProgress{
			WorkUnitsCompleted: 3,
			WorkUnitsTotal:     6,
			Message:            "repairing peer copy",
		},
	}), "put operation")
	testutil.RequireNoErrorf(t, store.Put(&adminv1.Operation{
		OperationId:   "op-2",
		OperationType: "restore",
		State:         adminv1.OperationState_OPERATION_STATE_SUCCEEDED,
		RequestedAt:   timestamppb.New(requestedAt.Add(2 * time.Minute)),
		FinishedAt:    timestamppb.New(requestedAt.Add(3 * time.Minute)),
		Progress: &adminv1.OperationProgress{
			WorkUnitsCompleted: 6,
			WorkUnitsTotal:     6,
			Message:            "restore complete",
		},
	}), "put succeeded operation")

	response := request(NewHandler(Options{Operations: store}), "/admin/views/operations")
	requireOK(t, response)
	body := response.Body.String()
	requireContains(t, body, "op-1")
	requireContains(t, body, "op-2")
	requireContains(t, body, "running")
	requireContains(t, body, "Succeeded")
	requireContains(t, body, "repairing peer copy")
	requireContains(t, body, "2026-05-25T13:00:00Z")

	filtered := request(NewHandler(Options{Operations: store}), "/admin/views/operations?state=running")
	requireOK(t, filtered)
	filteredBody := filtered.Body.String()
	requireContains(t, filteredBody, "op-1")
	requireContains(t, filteredBody, "filter-pill active")
	requireNotContains(t, filteredBody, "op-2")

	detail := htmxRequest(NewHandler(Options{Operations: store}), "/admin/operations/op-1/detail")
	requireOK(t, detail)
	detailBody := detail.Body.String()
	requireContains(t, detailBody, "operator-a")
	requireContains(t, detailBody, "true")
	requireContains(t, detailBody, "2026-05-25T13:01:00Z")
}

func TestHandlerRendersMemberDetailAndTabs(t *testing.T) {
	lastSeen := time.Date(2026, 5, 25, 14, 0, 0, 0, time.UTC)
	handler := NewHandler(Options{
		Inspect: fakeInspect{
			member: &adminv1.StorageMember{
				StorageMemberId: "local",
				CellId:          "local",
				State:           adminv1.MemberState_MEMBER_STATE_ONLINE,
				BytesUsed:       2048,
				BytesCapacity:   4096,
				LastSeenAt:      timestamppb.New(lastSeen),
			},
		},
		Member: fakeMember{
			safety: &adminv1.EvictionSafety{
				SafeToEvict: false,
				Warnings: []*adminv1.OperationWarning{
					{Code: "SCRAP_SINGLE_MEMBER_LOCAL_MODE", Message: "no alternate member"},
				},
			},
		},
	})

	detail := request(handler, "/admin/members/local")
	requireOK(t, detail)
	detailBody := detail.Body.String()
	requireContains(t, detailBody, "Members / local")
	requireContains(t, detailBody, "2.0 KiB")
	requireContains(t, detailBody, "50.0% of 4.0 KiB")
	requireContains(t, detailBody, "SCRAP_SINGLE_MEMBER_LOCAL_MODE")
	requireContains(t, detailBody, "/admin/members/local/tab/kubernetes")

	storageTab := htmxRequest(handler, "/admin/members/local/tab/storage")
	requireOK(t, storageTab)
	storageBody := storageTab.Body.String()
	requireContains(t, storageBody, "Bytes used")
	requireContains(t, storageBody, "2.0 KiB")
	requireNotContains(t, storageBody, "Member tabs")

	if response := request(handler, "/admin/members/local/tab/missing"); response.Code != http.StatusNotFound {
		t.Fatalf("unknown member tab status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestHandlerRendersClientDetailAndTabs(t *testing.T) {
	handler := NewHandler(Options{})

	detail := request(handler, "/admin/clients/etl-ingest")
	requireOK(t, detail)
	detailBody := detail.Body.String()
	requireContains(t, detailBody, "Service mesh / etl-ingest")
	requireContains(t, detailBody, "client telemetry unavailable")
	requireContains(t, detailBody, "/admin/clients/etl-ingest/tab/traffic")

	trafficTab := htmxRequest(handler, "/admin/clients/etl-ingest/tab/traffic")
	requireOK(t, trafficTab)
	trafficBody := trafficTab.Body.String()
	requireContains(t, trafficBody, "Active streams")
	requireContains(t, trafficBody, "per-client stream counts")
	requireNotContains(t, trafficBody, "Client tabs")

	if response := request(handler, "/admin/clients/etl-ingest/tab/missing"); response.Code != http.StatusNotFound {
		t.Fatalf("unknown client tab status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestHandlerWrapsPartialsInShellForDirectNavigation(t *testing.T) {
	handler := populatedHandler()

	response := request(handler, "/admin/views/capacity")
	requireOK(t, response)
	body := response.Body.String()
	requireContains(t, body, "<!doctype html>")
	requireContains(t, body, "admin.css")
	requireContains(t, body, "local-profile")

	htmx := htmxRequest(handler, "/admin/views/capacity")
	requireOK(t, htmx)
	htmxBody := htmx.Body.String()
	requireNotContains(t, htmxBody, "<!doctype html>")
	requireContains(t, htmxBody, "local-profile")
}

func TestHandlerWrapsDetailInShellForDirectNavigation(t *testing.T) {
	handler := NewHandler(Options{
		Inspect: fakeInspect{
			member: &adminv1.StorageMember{
				StorageMemberId: "local",
				CellId:          "local",
				State:           adminv1.MemberState_MEMBER_STATE_ONLINE,
				BytesUsed:       2048,
				BytesCapacity:   4096,
			},
		},
		Member: fakeMember{safety: &adminv1.EvictionSafety{SafeToEvict: true}},
	})

	detail := request(handler, "/admin/members/local")
	requireOK(t, detail)
	detailBody := detail.Body.String()
	requireContains(t, detailBody, "<!doctype html>")
	requireContains(t, detailBody, "Members / local")

	tabDirect := request(handler, "/admin/members/local/tab/storage")
	requireOK(t, tabDirect)
	tabBody := tabDirect.Body.String()
	requireContains(t, tabBody, "<!doctype html>")
	requireContains(t, tabBody, "Members / local")
}

func TestHandlerRendersRecoveryReadiness(t *testing.T) {
	checkpoint := time.Date(2026, 5, 25, 15, 0, 0, 0, time.UTC)
	handler := NewHandler(Options{
		DR: fakeDR{
			readiness: &adminv1.RecoveryReadiness{
				Ready:                        true,
				LatestRestorableCheckpointAt: timestamppb.New(checkpoint),
				Warnings: []*adminv1.OperationWarning{
					{Code: "SCRAP_DR_NON_PRODUCTION_MODE", Message: "local recovery artifacts are ready"},
				},
			},
		},
	})

	backend := request(handler, "/admin/views/backend")
	requireOK(t, backend)
	backendBody := backend.Body.String()
	requireContains(t, backendBody, "DisasterRecoveryService readiness")
	requireContains(t, backendBody, "ready")
	requireContains(t, backendBody, "2026-05-25T15:00:00Z")
	requireContains(t, backendBody, "SCRAP_DR_NON_PRODUCTION_MODE")
	requireContains(t, backendBody, "latest restorable checkpoint 2026-05-25T15:00:00Z")
}

func TestHandlerRejectsInvalidOperationFilter(t *testing.T) {
	response := request(NewHandler(Options{}), "/admin/views/operations?state=missing")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body:\n%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestHandlerRejectsUnknownPaths(t *testing.T) {
	handler := NewHandler(Options{})
	if response := request(handler, "/not-admin"); response.Code != http.StatusNotFound {
		t.Fatalf("unknown root status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if response := request(handler, "/admin/views/missing"); response.Code != http.StatusNotFound {
		t.Fatalf("unknown view status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestHandlerServesEmbeddedStaticAssets(t *testing.T) {
	response := request(NewHandler(Options{}), "/admin/static/admin.css")
	requireOK(t, response)
	requireContains(t, response.Body.String(), ".app-shell")

	script := request(NewHandler(Options{}), "/admin/static/admin.js")
	requireOK(t, script)
	requireContains(t, script.Body.String(), "scrap.admin.ui.tweaks")
	requireContains(t, script.Body.String(), "htmx.ajax")
	requireContains(t, script.Body.String(), "applyCompactNumbers")

	logo := request(NewHandler(Options{}), "/admin/static/assets/logo-petabyte-color-white.png")
	requireOK(t, logo)
	if logo.Body.Len() == 0 {
		t.Fatal("logo asset is empty")
	}

	font := request(NewHandler(Options{}), "/admin/static/fonts/Maven_Pro_Bold.otf")
	requireOK(t, font)
	if font.Body.Len() == 0 {
		t.Fatal("font asset is empty")
	}
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

func htmxRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("HX-Request", "true")
	handler.ServeHTTP(recorder, req)
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
		DR: fakeDR{readiness: &adminv1.RecoveryReadiness{
			Ready:                        true,
			LatestRestorableCheckpointAt: timestamppb.New(time.Date(2026, 5, 25, 12, 30, 0, 0, time.UTC)),
		}},
		Now: func() time.Time { return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC) },
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

func requireNotContains(t *testing.T, body, unwanted string) {
	t.Helper()
	if strings.Contains(body, unwanted) {
		t.Fatalf("body unexpectedly contains %q:\n%s", unwanted, body)
	}
}

type fakeInspect struct {
	summary *adminv1.ClusterSummary
	runway  *adminv1.CapacityRunway
	member  *adminv1.StorageMember
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
	if f.member == nil {
		return nil, appstatus.New(appstatus.CodeNotFound, "member not found")
	}
	return f.member, nil
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

type fakeDR struct {
	readiness *adminv1.RecoveryReadiness
}

func (f fakeDR) GetRecoveryReadiness(context.Context) (*adminv1.RecoveryReadiness, error) {
	return f.readiness, nil
}

type fakeMember struct {
	safety *adminv1.EvictionSafety
}

func (f fakeMember) CordonMember(context.Context, api.MemberMutationRequest) (*adminv1.StorageMember, error) {
	return nil, errors.New("not implemented")
}

func (f fakeMember) UncordonMember(context.Context, api.MemberMutationRequest) (*adminv1.StorageMember, error) {
	return nil, errors.New("not implemented")
}

func (f fakeMember) GetEvictionSafety(context.Context, string) (*adminv1.EvictionSafety, error) {
	if f.safety == nil {
		return nil, appstatus.New(appstatus.CodeNotFound, "member not found")
	}
	return f.safety, nil
}
