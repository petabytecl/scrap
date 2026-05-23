package authz

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

const (
	capHead  Capability = "public.document.head"
	capWrite Capability = "public.document.write"
	capDrain Capability = "admin.member.drain.start"
)

func TestManagerAuthorizesOperationSpecificCapabilities(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {Capabilities: []Capability{capHead}},
		},
	})

	allowed := manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "billing-etl"), capHead)
	if !allowed.Allowed || allowed.PolicyVersion != "policy-v1" || allowed.PolicyGeneration != 1 {
		t.Fatalf("allowed decision = %#v, want policy-v1 generation 1 allow", allowed)
	}

	denied := manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "billing-etl"), capWrite)
	if denied.Allowed || denied.Reason != ReasonCapabilityDenied {
		t.Fatalf("denied decision = %#v, want capability denied", denied)
	}
}

func TestManagerRejectsMissingWorkloadIdentity(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {Capabilities: []Capability{capHead}},
		},
	})

	decision := manager.Authorize(context.Background(), capHead)
	if decision.Allowed || decision.Reason != ReasonMissingWorkloadIdentity {
		t.Fatalf("decision = %#v, want missing workload identity denial", decision)
	}
}

func TestInvalidPolicyFailsClosed(t *testing.T) {
	_, err := NewManager(Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"operator": {Capabilities: []Capability{"admin"}},
		},
	}, []Capability{capHead})
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("error = %v, want invalid policy", err)
	}
}

func TestLoadFileRejectsTrailingJSON(t *testing.T) {
	path := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}
{
  "version": "policy-v2",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.write"] }
  }
}`)
	_, err := LoadFile(path)
	if !errors.Is(err, ErrInvalidPolicy) || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("error = %v, want trailing JSON invalid policy", err)
	}
}

func TestBadReloadKeepsLastValidPolicyAndAlerts(t *testing.T) {
	validPath := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "operator": { "capabilities": ["admin.member.drain.start"] }
  }
}`)
	manager, err := LoadManagerFromFile(validPath, []Capability{capDrain})
	if err != nil {
		t.Fatalf("load manager: %v", err)
	}
	if !manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain).Allowed {
		t.Fatal("initial policy did not allow operator drain")
	}

	badPath := writePolicy(t, `{
  "version": "policy-v2",
  "workloads": {
    "operator": { "capabilities": ["admin"] }
  }
}`)
	if err := manager.ReloadFile(badPath); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("reload error = %v, want invalid policy", err)
	}
	if manager.PolicyVersion() != "policy-v1" || manager.Generation() != 1 {
		t.Fatalf("active policy = version %s generation %d, want original policy", manager.PolicyVersion(), manager.Generation())
	}
	if !manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain).Allowed {
		t.Fatal("last valid policy was not preserved after bad reload")
	}
	alerts := manager.ReloadAlerts()
	if len(alerts) != 1 || alerts[0].Code != ReasonPolicyReloadRejected || !strings.Contains(alerts[0].Message, "admin") {
		t.Fatalf("alerts = %#v, want rejected policy alert", alerts)
	}
}

func TestReloadAlertsAreBounded(t *testing.T) {
	validPath := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "operator": { "capabilities": ["admin.member.drain.start"] }
  }
}`)
	manager, err := LoadManagerFromFile(validPath, []Capability{capDrain})
	if err != nil {
		t.Fatalf("load manager: %v", err)
	}
	badPath := writePolicy(t, `{
  "version": "policy-v2",
  "workloads": {
    "operator": { "capabilities": ["admin"] }
  }
}`)

	for range maxReloadAlerts + 5 {
		if err := manager.ReloadFile(badPath); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("reload error = %v, want invalid policy", err)
		}
	}
	alerts := manager.ReloadAlerts()
	if len(alerts) != maxReloadAlerts {
		t.Fatalf("alert count = %d, want %d", len(alerts), maxReloadAlerts)
	}
}

func TestGoodReloadReplacesPolicy(t *testing.T) {
	firstPath := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "operator": { "capabilities": ["admin.member.drain.start"] }
  }
}`)
	manager, err := LoadManagerFromFile(firstPath, []Capability{capDrain, capHead})
	if err != nil {
		t.Fatalf("load manager: %v", err)
	}
	secondPath := writePolicy(t, `{
  "version": "policy-v2",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}`)
	if err := manager.ReloadFile(secondPath); err != nil {
		t.Fatalf("reload valid policy: %v", err)
	}
	if manager.PolicyVersion() != "policy-v2" || manager.Generation() != 2 {
		t.Fatalf("active policy = version %s generation %d, want policy-v2 generation 2", manager.PolicyVersion(), manager.Generation())
	}
	if manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain).Allowed {
		t.Fatal("old operator capability remained after reload")
	}
	if !manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "billing-etl"), capHead).Allowed {
		t.Fatal("new policy did not allow billing-etl head")
	}
}

func newTestManager(t *testing.T, policy Policy) *Manager {
	t.Helper()
	manager, err := NewManager(policy, []Capability{capHead, capWrite, capDrain})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return manager
}

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "policy-*.json")
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if _, err := file.WriteString(body); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close policy: %v", err)
	}
	return file.Name()
}
