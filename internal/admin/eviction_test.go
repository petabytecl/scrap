package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/eviction"
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

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d, want 201", resp.StatusCode)
	}
	if planner.req.MemberHostname != "scrapd-1" || planner.req.MaxBlocks == nil || *planner.req.MaxBlocks != 2 {
		t.Fatalf("planner request mismatch: %+v", planner.req)
	}

	var got eviction.Plan
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.PlanID != "plan-123" || got.MemberID != "member-a" {
		t.Fatalf("plan response mismatch: %+v", got)
	}
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

type evictionPlannerFunc func(context.Context, eviction.PlanRequest) (eviction.Plan, error)

func (f evictionPlannerFunc) CreateEvictionPlan(ctx context.Context, req eviction.PlanRequest) (eviction.Plan, error) {
	return f(ctx, req)
}
