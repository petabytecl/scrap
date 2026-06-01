package scrapctl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/eviction"
)

func TestEvictionPlanPostsRequestAndPrintsEvidence(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/admin/eviction/plans" {
			t.Fatalf("path = %s, want /admin/eviction/plans", req.URL.Path)
		}

		var got eviction.PlanRequest
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.MemberHostname != "scrapd-1" || got.MaxBlocks == nil || *got.MaxBlocks != 2 {
			t.Fatalf("request mismatch: %+v", got)
		}

		plan := eviction.Plan{
			PlanID:         "plan-123",
			GeneratedAtUs:  1716700000000000,
			ExpiresAtUs:    1716700600000000,
			MemberHostname: "scrapd-1",
			MemberID:       "member-a",
			RecommendedBounds: eviction.Bounds{
				MaxBlocks: 10,
				MaxBytes:  671088640,
			},
			EffectiveBounds: eviction.Bounds{
				MaxBlocks: 2,
				MaxBytes:  4096,
			},
			Config: eviction.ConfigSnapshot{
				Enabled:                   false,
				HotResidencyWindowSeconds: 86400,
				PlanTTLSeconds:            600,
				RecommendedMaxBlocks:      10,
				RecommendedMaxBytes:       671088640,
				MaxValidateSamples:        1,
			},
			Selected: []eviction.PlanBlock{{
				BlockID:                   1,
				ShardID:                   7,
				SizeBytes:                 1024,
				ConfirmedAtUs:             1716600000000000,
				EligibleAtUs:              1716686400000000,
				HotResidencyWindowSeconds: 86400,
				LocalState:                "hot",
				OpenReaders:               0,
				RepairState:               "idle",
				BackendKey:                "cell/shards/7/1.blk",
			}},
			Skipped: []eviction.PlanBlock{{
				BlockID:   2,
				ShardID:   7,
				SizeBytes: 2048,
				Reason:    eviction.SkipReasonHotResidencyWindow,
			}},
			SkipCountsByReason: map[string]int{
				eviction.SkipReasonHotResidencyWindow: 1,
			},
		}
		data, err := json.Marshal(plan)
		if err != nil {
			t.Fatalf("marshal plan: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{
		"eviction", "plan",
		"--admin-url=http://admin.local",
		"--member-hostname=scrapd-1",
		"--max-blocks=2",
		"--max-bytes=4096",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("eviction plan: %v", err)
	}

	assertTextContains(t, out.String(),
		"plan_id: plan-123",
		"member_hostname: scrapd-1",
		"member_id: member-a",
		"recommended_bounds: max_blocks=10 max_bytes=671088640",
		"effective_bounds: max_blocks=2 max_bytes=4096",
		"config: enabled=false hot_residency_window_seconds=86400 plan_ttl_seconds=600",
		"selected_blocks:",
		"block_id=1",
		"confirmed_at_us=1716600000000000",
		"eligible_at_us=1716686400000000",
		"skipped_candidates:",
		"reason=hot_residency_window",
	)
}

func assertTextContains(t *testing.T, got string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestEvictionPlanRequiresMemberHostname(t *testing.T) {
	err := Run([]string{"eviction", "plan"}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), "member-hostname is required") {
		t.Fatalf("error = %v, want member-hostname is required", err)
	}
}

func TestEvictionApplyPostsStoredPlanWithConfirm(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/admin/eviction/plans/plan-123/apply" {
			t.Fatalf("path = %s, want /admin/eviction/plans/plan-123/apply", req.URL.Path)
		}

		result := eviction.ApplyResult{
			PlanID:         "plan-123",
			Status:         eviction.ApplyStatusCompleted,
			EvictedBlocks:  1,
			SelectedBlocks: 1,
			BytesFreed:     1024,
			Blocks: []eviction.ApplyBlock{{
				BlockID:    1,
				ShardID:    7,
				SizeBytes:  1024,
				Status:     eviction.ApplyBlockStatusEvicted,
				BytesFreed: 1024,
			}},
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{
		"eviction", "apply",
		"--admin-url=http://admin.local",
		"--plan-id=plan-123",
		"--confirm",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("eviction apply: %v", err)
	}

	assertTextContains(t, out.String(),
		"plan_id: plan-123",
		"status: completed",
		"selected_blocks: 1",
		"evicted_blocks: 1",
		"bytes_freed: 1024",
		"block_id=1",
		"status=evicted",
	)
}

func TestEvictionApplyPrintsSkipAndFailureDetails(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		result := eviction.ApplyResult{
			PlanID:         "plan-123",
			Status:         eviction.ApplyStatusCompletedWithSkips,
			SelectedBlocks: 2,
			SkippedBlocks:  1,
			FailedBlocks:   1,
			Blocks: []eviction.ApplyBlock{
				{
					BlockID:   2,
					ShardID:   7,
					SizeBytes: 2048,
					Status:    eviction.ApplyBlockStatusSkipped,
					Reason:    eviction.SkipReasonLocalStateNotHot,
				},
				{
					BlockID:   3,
					ShardID:   7,
					SizeBytes: 4096,
					Status:    eviction.ApplyBlockStatusFailed,
					Error:     "remove Block: permission denied",
				},
			},
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{
		"eviction", "apply",
		"--admin-url=http://admin.local",
		"--plan-id=plan-123",
		"--confirm",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("eviction apply: %v", err)
	}

	assertTextContains(t, out.String(),
		"status: completed_with_skips",
		"skipped_blocks: 1",
		"failed_blocks: 1",
		"reason=local_state_not_hot",
		"error=\"remove Block: permission denied\"",
	)
}

func TestEvictionApplyReturnsErrorForFailedResult(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		result := eviction.ApplyResult{
			PlanID:         "plan-123",
			Status:         eviction.ApplyStatusFailed,
			SelectedBlocks: 1,
			FailedBlocks:   1,
			Blocks: []eviction.ApplyBlock{{
				BlockID:   3,
				ShardID:   7,
				SizeBytes: 4096,
				Status:    eviction.ApplyBlockStatusFailed,
				Error:     "confirmed Block size mismatch",
			}},
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{
		"eviction", "apply",
		"--admin-url=http://admin.local",
		"--plan-id=plan-123",
		"--confirm",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "eviction apply failed") {
		t.Fatalf("error = %v, want eviction apply failed", err)
	}
	assertTextContains(t, out.String(), "status: failed", "failed_blocks: 1")
}

func TestEvictionApplySupportsJSONOutput(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		data, err := json.Marshal(eviction.ApplyResult{
			PlanID: "plan-123",
			Status: eviction.ApplyStatusNoEffect,
		})
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{
		"eviction", "apply",
		"--admin-url=http://admin.local",
		"--plan-id=plan-123",
		"--confirm",
		"--output=json",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("eviction apply json: %v", err)
	}

	assertTextContains(t, out.String(), `"plan_id":"plan-123"`, `"status":"no_effect"`)
}

func TestEvictionApplyReportsHTTPError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusPreconditionFailed,
			Body:       io.NopCloser(strings.NewReader("eviction: plan not found\n")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	err := Run([]string{
		"eviction", "apply",
		"--admin-url=http://admin.local",
		"--plan-id=plan-123",
		"--confirm",
	}, io.Discard, io.Discard, Deps{HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "POST eviction apply status: 412") {
		t.Fatalf("error = %v, want HTTP status error", err)
	}
}

func TestEvictionApplyRequiresConfirm(t *testing.T) {
	err := Run([]string{"eviction", "apply", "--plan-id=plan-123"}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), "confirm is required") {
		t.Fatalf("error = %v, want confirm is required", err)
	}
}

func TestEvictionApplyRequiresPlanID(t *testing.T) {
	err := Run([]string{"eviction", "apply", "--confirm"}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), "plan-id is required") {
		t.Fatalf("error = %v, want plan-id is required", err)
	}
}
