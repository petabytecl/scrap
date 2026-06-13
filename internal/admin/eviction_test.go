package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/eviction"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type evictionPlannerStub struct {
	req eviction.PlanRequest
}

func (s *evictionPlannerStub) CreateEvictionPlan(_ context.Context, req eviction.PlanRequest) (eviction.Plan, error) {
	s.req = req
	return eviction.Plan{
		PlanID:         "plan-123",
		MemberHostname: req.MemberHostname,
		MemberID:       "member-a",
		RecommendedBounds: eviction.Bounds{
			MaxBlocks: 10,
			MaxBytes:  640 << 20,
		},
		EffectiveBounds: eviction.Bounds{
			MaxBlocks: 2,
			MaxBytes:  4096,
		},
		Selected: []eviction.PlanBlock{{
			BlockID:    1,
			ShardID:    7,
			BackendKey: "cell/shards/7/1.blk",
		}},
	}, nil
}

func TestServer_CreateEvictionPlan(t *testing.T) {
	planner := &evictionPlannerStub{}
	srv := admin.New(admin.WithEvictionPlanner(planner))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := []byte(`{"member_hostname":"scrapd-1","max_blocks":2,"max_bytes":4096,"reason":"evidence_run","note":"dry run"}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/admin/eviction/plans", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/eviction/plans: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	assertCreateEvictionPlanRequest(t, planner.req)
	assertCreateEvictionPlanResponse(t, resp)
}

func assertCreateEvictionPlanRequest(t *testing.T, req eviction.PlanRequest) {
	t.Helper()

	if req.MemberHostname != "scrapd-1" || req.MaxBlocks == nil || *req.MaxBlocks != 2 {
		t.Fatalf("planner request mismatch: %+v", req)
	}
}

func assertCreateEvictionPlanResponse(t *testing.T, resp *http.Response) {
	t.Helper()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d, want 201", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	assertAdminBodyNotContains(t, string(data), "backend_key", "cell/shards/7/1.blk")
	var got eviction.Plan
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.PlanID != "plan-123" || got.MemberID != "member-a" {
		t.Fatalf("plan response mismatch: %+v", got)
	}
}

func TestServer_ApplyEvictionPlanRedactsSensitiveErrors(t *testing.T) {
	applier := evictionApplierFunc(func(context.Context, eviction.ApplyRequest) (eviction.ApplyResult, error) {
		return eviction.ApplyResult{
			PlanID: "plan-123",
			Status: eviction.ApplyStatusEvictedWithValidationFailure,
			Blocks: []eviction.ApplyBlock{{
				BlockID: 1,
				ShardID: 7,
				Status:  eviction.ApplyBlockStatusFailed,
				Error:   "backend_key=cell/shards/7/1.blk validation_token=secret",
			}},
			Validations: []eviction.ValidationBlock{{
				BlockID: 1,
				ShardID: 7,
				Status:  eviction.ValidationStatusFailed,
				Error:   "transaction_id=tx-1 document_name=doc.xml request_id=req-1 /tmp/block.idx",
			}},
		}, nil
	})
	srv := admin.New(admin.WithEvictionApplier(applier))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := postEvictionApplyForAdminTest(ts.URL, "plan-123", []byte(`{}`))
	if err != nil {
		t.Fatalf("POST apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body := string(data)
	assertAdminBodyContains(t, body, "sensitive detail redacted")
	assertAdminBodyNotContains(t, body, "backend_key", "cell/shards/7/1.blk", "validation_token", "transaction_id", "document_name", "request_id", "/tmp/block.idx")
}

func TestServer_CreateEvictionPlanTargetMismatchReturnsPreconditionFailed(t *testing.T) {
	planner := evictionPlannerFunc(func(context.Context, eviction.PlanRequest) (eviction.Plan, error) {
		return eviction.Plan{}, eviction.ErrTargetMemberMismatch
	})
	srv := admin.New(admin.WithEvictionPlanner(planner))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/admin/eviction/plans",
		bytes.NewReader([]byte(`{"member_hostname":"scrapd-9"}`)),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/eviction/plans: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status: got %d, want 412", resp.StatusCode)
	}
}

func TestServer_CreateEvictionPlanRejectsInvalidJSON(t *testing.T) {
	planner := evictionPlannerFunc(func(context.Context, eviction.PlanRequest) (eviction.Plan, error) {
		return eviction.Plan{}, errors.New("should not be called")
	})
	srv := admin.New(admin.WithEvictionPlanner(planner))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/admin/eviction/plans",
		bytes.NewReader([]byte(`{"member_hostname":`)),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/eviction/plans: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestServer_ApplyEvictionPlan(t *testing.T) {
	applier := &evictionApplierStub{}
	statusProvider := evictionPlanStatusFunc(func(context.Context, string) (eviction.PlanStatus, error) {
		return eviction.PlanStatus{}, errors.New("should not be called")
	})
	srv := admin.New(admin.WithEvictionApplier(applier), admin.WithEvictionPlanStatusProvider(statusProvider))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/admin/eviction/plans/plan-123/apply",
		bytes.NewReader([]byte(`{}`)),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/eviction/plans/plan-123/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if applier.req.PlanID != "plan-123" {
		t.Fatalf("apply request = %+v, want plan-123", applier.req)
	}

	var got eviction.ApplyResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.PlanID != "plan-123" || got.Status != eviction.ApplyStatusCompleted {
		t.Fatalf("apply response mismatch: %+v", got)
	}
}

func TestServer_GetEvictionPlanStatus(t *testing.T) {
	statusProvider := evictionPlanStatusFunc(func(_ context.Context, planID string) (eviction.PlanStatus, error) {
		return eviction.PlanStatus{
			PlanID: planID,
			Status: eviction.PlanStatusRunning,
		}, nil
	})
	srv := admin.New(admin.WithEvictionPlanStatusProvider(statusProvider))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/admin/eviction/plans/plan-123", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/eviction/plans/plan-123: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var got eviction.PlanStatus
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.PlanID != "plan-123" || got.Status != eviction.PlanStatusRunning {
		t.Fatalf("status response = %+v, want running plan-123", got)
	}
}

func TestServer_GetEvictionPlanStatusRedactsSensitiveErrors(t *testing.T) {
	statusProvider := evictionPlanStatusFunc(func(_ context.Context, planID string) (eviction.PlanStatus, error) {
		return eviction.PlanStatus{
			PlanID: planID,
			Status: eviction.ApplyStatusEvictedWithValidationFailure,
			ApplyResult: &eviction.ApplyResult{
				PlanID: planID,
				Status: eviction.ApplyStatusEvictedWithValidationFailure,
				Validations: []eviction.ValidationBlock{{
					BlockID: 1,
					ShardID: 7,
					Status:  eviction.ValidationStatusFailed,
					Error:   "peer address 10.0.0.1 certificate /home/scrap/cert.pem",
				}},
			},
		}, nil
	})
	srv := admin.New(admin.WithEvictionPlanStatusProvider(statusProvider))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/admin/eviction/plans/plan-123", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/eviction/plans/plan-123: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body := string(data)
	assertAdminBodyContains(t, body, "sensitive detail redacted")
	assertAdminBodyNotContains(t, body, "peer address", "10.0.0.1", "certificate", "/home/scrap/cert.pem")
}

func TestServer_GetEvictionPlanStatusNotFoundReturnsPreconditionFailed(t *testing.T) {
	statusProvider := evictionPlanStatusFunc(func(context.Context, string) (eviction.PlanStatus, error) {
		return eviction.PlanStatus{}, eviction.ErrPlanNotFound
	})
	srv := admin.New(admin.WithEvictionPlanStatusProvider(statusProvider))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/admin/eviction/plans/missing", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/eviction/plans/missing: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status: got %d, want 412", resp.StatusCode)
	}
}

func TestServer_ApplyEvictionPlanNotFoundReturnsPreconditionFailed(t *testing.T) {
	applier := evictionApplierFunc(func(context.Context, eviction.ApplyRequest) (eviction.ApplyResult, error) {
		return eviction.ApplyResult{}, eviction.ErrPlanNotFound
	})
	srv := admin.New(admin.WithEvictionApplier(applier))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/admin/eviction/plans/missing/apply",
		bytes.NewReader([]byte(`{}`)),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/eviction/plans/missing/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status: got %d, want 412", resp.StatusCode)
	}
}

func TestServer_ApplyEvictionPlanMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "in progress", err: eviction.ErrApplyInProgress, want: http.StatusConflict},
		{name: "rebuilding", err: storeapi.ErrRebuilding, want: http.StatusServiceUnavailable},
		{name: "backend unavailable", err: storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, "Backend restore is not configured"), want: http.StatusServiceUnavailable},
		{name: "invalid request", err: eviction.ErrInvalidPlanRequest, want: http.StatusBadRequest},
		{name: "unexpected", err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applier := evictionApplierFunc(func(context.Context, eviction.ApplyRequest) (eviction.ApplyResult, error) {
				return eviction.ApplyResult{}, tt.err
			})
			srv := admin.New(admin.WithEvictionApplier(applier))
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()

			resp, err := postEvictionApplyForAdminTest(ts.URL, "plan-123", []byte(`{}`))
			if err != nil {
				t.Fatalf("POST apply: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestServer_ApplyEvictionPlanRejectsInvalidRequest(t *testing.T) {
	applier := &evictionApplierStub{}
	srv := admin.New(admin.WithEvictionApplier(applier))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/admin/eviction/plans/plan-123/apply", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET apply: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", resp.StatusCode)
	}

	resp, err = postEvictionApplyForAdminTest(ts.URL, "plan-123", []byte(`{"plan_id":"x"}`))
	if err != nil {
		t.Fatalf("POST invalid apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", resp.StatusCode)
	}
}

type evictionPlannerFunc func(context.Context, eviction.PlanRequest) (eviction.Plan, error)

func (f evictionPlannerFunc) CreateEvictionPlan(ctx context.Context, req eviction.PlanRequest) (eviction.Plan, error) {
	return f(ctx, req)
}

type evictionApplierStub struct {
	req eviction.ApplyRequest
}

func (s *evictionApplierStub) ApplyEvictionPlan(_ context.Context, req eviction.ApplyRequest) (eviction.ApplyResult, error) {
	s.req = req
	return eviction.ApplyResult{
		PlanID: req.PlanID,
		Status: eviction.ApplyStatusCompleted,
	}, nil
}

type evictionApplierFunc func(context.Context, eviction.ApplyRequest) (eviction.ApplyResult, error)

func (f evictionApplierFunc) ApplyEvictionPlan(ctx context.Context, req eviction.ApplyRequest) (eviction.ApplyResult, error) {
	return f(ctx, req)
}

type evictionPlanStatusFunc func(context.Context, string) (eviction.PlanStatus, error)

func (f evictionPlanStatusFunc) EvictionPlanStatus(ctx context.Context, planID string) (eviction.PlanStatus, error) {
	return f(ctx, planID)
}

func postEvictionApplyForAdminTest(baseURL, planID string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		baseURL+"/admin/eviction/plans/"+planID+"/apply",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("new apply request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do apply request: %w", err)
	}
	return resp, nil
}

func assertAdminBodyContains(t *testing.T, body string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %q:\n%s", want, body)
		}
	}
}

func assertAdminBodyNotContains(t *testing.T, body string, rejects ...string) {
	t.Helper()

	for _, reject := range rejects {
		if strings.Contains(body, reject) {
			t.Fatalf("response leaked %q:\n%s", reject, body)
		}
	}
}
