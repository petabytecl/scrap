package placement

import (
	"errors"
	"fmt"
	"testing"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPlanVotersSelectsFiveDistinctNodes(t *testing.T) {
	plan, err := PlanVoters(testMembers(7), DefaultPolicy())
	testutil.RequireNoErrorf(t, err, "plan voters")
	if !plan.ProductionHealthy {
		t.Fatalf("production healthy = false, problems = %#v", plan.Problems)
	}
	if len(plan.Voters) != 5 {
		t.Fatalf("voter count = %d, want 5", len(plan.Voters))
	}
	requireDistinctNodes(t, plan.Voters)
}

func TestPlanVotersRejectsFewerThanFiveDistinctNodes(t *testing.T) {
	members := []Member{
		testMember("member-1", "node-a", "zone-a"),
		testMember("member-2", "node-a", "zone-b"),
		testMember("member-3", "node-b", "zone-c"),
		testMember("member-4", "node-c", "zone-a"),
		testMember("member-5", "node-d", "zone-b"),
	}
	plan, err := PlanVoters(members, DefaultPolicy())
	if !errors.Is(err, ErrInsufficientDistinctNodes) {
		t.Fatalf("plan error = %v, want %v", err, ErrInsufficientDistinctNodes)
	}
	if plan.ProductionHealthy {
		t.Fatal("production healthy = true, want unhealthy")
	}
	if !hasProblem(plan.Problems, ReasonTooFewDistinctNodes) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonTooFewDistinctNodes)
	}
	if len(plan.Voters) != 0 {
		t.Fatalf("voters = %#v, want no unsafe placement", plan.Voters)
	}
}

func TestPlanVotersRejectsReducedFaultToleranceByDefault(t *testing.T) {
	plan, err := PlanVoters(testMembers(5), DefaultPolicy())
	if !errors.Is(err, ErrUnsafePlacement) {
		t.Fatalf("plan error = %v, want %v", err, ErrUnsafePlacement)
	}
	if plan.ProductionHealthy {
		t.Fatal("production healthy = true, want unhealthy below seven eligible nodes")
	}
	if !hasProblem(plan.Problems, ReasonTooFewEligibleNodes) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonTooFewEligibleNodes)
	}
	if plan.RiskAccepted {
		t.Fatal("risk accepted without explicit risk mode")
	}
	if len(plan.Voters) != 0 {
		t.Fatalf("voters = %#v, want no unsafe placement", plan.Voters)
	}
}

func TestPlanVotersAllowsReducedFaultToleranceOnlyWithRiskMode(t *testing.T) {
	plan, err := PlanVoters(testMembers(5), Policy{RiskMode: RiskModeReducedFaultTolerance})
	testutil.RequireNoErrorf(t, err, "plan voters")
	if plan.ProductionHealthy {
		t.Fatal("production healthy = true, want reduced-fault-tolerance risk")
	}
	if !plan.RiskAccepted {
		t.Fatal("risk accepted = false, want explicit reduced-fault-tolerance mode to accept risk")
	}
	if !hasProblem(plan.Problems, ReasonTooFewEligibleNodes) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonTooFewEligibleNodes)
	}
	if len(plan.Voters) != 5 {
		t.Fatalf("voter count = %d, want 5", len(plan.Voters))
	}
	requireDistinctNodes(t, plan.Voters)
}

func TestPlanVotersPrefersZoneSpread(t *testing.T) {
	plan, err := PlanVoters([]Member{
		testMember("member-1", "node-1", "zone-a"),
		testMember("member-2", "node-2", "zone-a"),
		testMember("member-3", "node-3", "zone-a"),
		testMember("member-4", "node-4", "zone-b"),
		testMember("member-5", "node-5", "zone-b"),
		testMember("member-6", "node-6", "zone-c"),
		testMember("member-7", "node-7", "zone-c"),
	}, DefaultPolicy())
	testutil.RequireNoErrorf(t, err, "plan voters")
	zoneCounts := map[string]int{}
	for _, voter := range plan.Voters {
		zoneCounts[voter.Zone]++
	}
	if len(zoneCounts) != 3 {
		t.Fatalf("zone counts = %#v, want voters spread across three zones", zoneCounts)
	}
	for zone, count := range zoneCounts {
		if count > 2 {
			t.Fatalf("zone %s has %d voters, want no more than 2 in five-voter plan", zone, count)
		}
	}
}

func TestPlanVotersExcludesUnavailableMembers(t *testing.T) {
	members := append(testMembers(5),
		Member{MemberID: "member-offline", KubernetesNode: "node-offline", Zone: "zone-c", Eligible: true, Online: false},
		Member{MemberID: "member-draining", KubernetesNode: "node-draining", Zone: "zone-c", Eligible: true, Online: true, Draining: true},
		Member{MemberID: "member-ineligible", KubernetesNode: "node-ineligible", Zone: "zone-c", Eligible: false, Online: true},
	)
	plan, err := PlanVoters(members, Policy{VoterCount: 5, MinProductionEligibleNodes: 5})
	testutil.RequireNoErrorf(t, err, "plan voters")
	for _, voter := range plan.Voters {
		if voter.MemberID == "member-offline" || voter.MemberID == "member-draining" || voter.MemberID == "member-ineligible" {
			t.Fatalf("selected unavailable voter %#v", voter)
		}
	}
	requireDistinctNodes(t, plan.Voters)
}

func TestPlanVotersReportsNodeLabelMismatch(t *testing.T) {
	members := testMembers(5)
	for i := range members {
		members[i].NodeLabels = map[string]string{"scrap.petabyte.io/storage-node": "true"}
	}
	members[3].NodeLabels = map[string]string{"scrap.petabyte.io/storage-node": "false"}

	plan, err := PlanVoters(members, Policy{
		VoterCount:                 5,
		MinProductionEligibleNodes: 5,
		RequiredNodeLabels:         map[string]string{"scrap.petabyte.io/storage-node": "true"},
	})
	if !errors.Is(err, ErrInsufficientDistinctNodes) {
		t.Fatalf("plan error = %v, want %v", err, ErrInsufficientDistinctNodes)
	}
	if !hasProblem(plan.Problems, ReasonNodeLabelMismatch) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonNodeLabelMismatch)
	}
	if !hasProblem(plan.Problems, ReasonTooFewDistinctNodes) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonTooFewDistinctNodes)
	}
	if len(plan.Voters) != 0 {
		t.Fatalf("voters = %#v, want no unsafe placement", plan.Voters)
	}
}

func TestPlanVotersReportsNodeLabelMismatchWhenProductionMarginFails(t *testing.T) {
	members := testMembers(7)
	for i := range members {
		members[i].NodeLabels = map[string]string{"scrap.petabyte.io/storage-node": "true"}
	}
	members[6].NodeLabels = map[string]string{"scrap.petabyte.io/storage-node": "false"}

	plan, err := PlanVoters(members, Policy{
		RequiredNodeLabels: map[string]string{"scrap.petabyte.io/storage-node": "true"},
	})
	if !errors.Is(err, ErrUnsafePlacement) {
		t.Fatalf("plan error = %v, want %v", err, ErrUnsafePlacement)
	}
	if !hasProblem(plan.Problems, ReasonTooFewEligibleNodes) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonTooFewEligibleNodes)
	}
	if !hasProblem(plan.Problems, ReasonNodeLabelMismatch) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonNodeLabelMismatch)
	}
	if hasProblem(plan.Problems, ReasonTooFewDistinctNodes) {
		t.Fatalf("problems = %#v, did not expect %s with six eligible voters", plan.Problems, ReasonTooFewDistinctNodes)
	}
	if len(plan.Voters) != 0 {
		t.Fatalf("voters = %#v, want no unsafe placement", plan.Voters)
	}
}

func TestPlanVotersRejectsUnsupportedRiskMode(t *testing.T) {
	plan, err := PlanVoters(testMembers(7), Policy{RiskMode: RiskMode("unknown-risk")})
	if !errors.Is(err, ErrUnsafePlacement) {
		t.Fatalf("plan error = %v, want %v", err, ErrUnsafePlacement)
	}
	if plan.ProductionHealthy {
		t.Fatal("production healthy = true, want unsupported risk mode rejected")
	}
	if plan.RiskAccepted {
		t.Fatal("risk accepted = true, want unsupported risk mode rejected")
	}
	if !hasProblem(plan.Problems, ReasonUnsupportedRiskMode) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonUnsupportedRiskMode)
	}
	if len(plan.Voters) != 0 {
		t.Fatalf("voters = %#v, want no unsafe placement", plan.Voters)
	}
}

func TestPlanVotersRejectsSharedStoragePoolByDefault(t *testing.T) {
	members := membersInStoragePool(testMembers(5), "pool-a")
	plan, err := PlanVoters(members, Policy{VoterCount: 5, MinProductionEligibleNodes: 5})
	if !errors.Is(err, ErrUnsafePlacement) {
		t.Fatalf("plan error = %v, want %v", err, ErrUnsafePlacement)
	}
	if !hasProblem(plan.Problems, ReasonSelectedSharedPool) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonSelectedSharedPool)
	}
	if len(plan.Voters) != 0 {
		t.Fatalf("voters = %#v, want no unsafe placement", plan.Voters)
	}
}

func TestPlanVotersAllowsSharedStoragePoolOnlyWithRiskMode(t *testing.T) {
	members := membersInStoragePool(testMembers(5), "pool-a")
	plan, err := PlanVoters(members, Policy{
		VoterCount:                 5,
		MinProductionEligibleNodes: 5,
		RiskMode:                   RiskModeSharedPool,
	})
	testutil.RequireNoErrorf(t, err, "plan voters")
	if plan.ProductionHealthy {
		t.Fatal("production healthy = true, want shared-pool risk")
	}
	if !plan.RiskAccepted {
		t.Fatal("risk accepted = false, want explicit shared-pool mode to accept risk")
	}
	if !hasProblem(plan.Problems, ReasonSelectedSharedPool) {
		t.Fatalf("problems = %#v, want %s", plan.Problems, ReasonSelectedSharedPool)
	}
	if len(plan.Voters) != 5 {
		t.Fatalf("voter count = %d, want 5", len(plan.Voters))
	}
	requireDistinctNodes(t, plan.Voters)
}

func testMembers(count int) []Member {
	members := make([]Member, 0, count)
	zones := []string{"zone-a", "zone-b", "zone-c"}
	for i := 1; i <= count; i++ {
		members = append(members, testMember(
			fmt.Sprintf("member-%d", i),
			fmt.Sprintf("node-%d", i),
			zones[(i-1)%len(zones)],
		))
	}
	return members
}

func testMember(memberID, node, zone string) Member {
	return Member{
		MemberID:       memberID,
		KubernetesNode: node,
		Zone:           zone,
		Eligible:       true,
		Online:         true,
	}
}

func requireDistinctNodes(t *testing.T, members []Member) {
	t.Helper()
	nodes := map[string]string{}
	for _, member := range members {
		if existing, ok := nodes[member.KubernetesNode]; ok {
			t.Fatalf("members %s and %s share node %s", existing, member.MemberID, member.KubernetesNode)
		}
		nodes[member.KubernetesNode] = member.MemberID
	}
}

func membersInStoragePool(members []Member, pool string) []Member {
	for i := range members {
		members[i].StoragePool = pool
	}
	return members
}

func hasProblem(problems []Problem, reason Reason) bool {
	for _, problem := range problems {
		if problem.Reason == reason {
			return true
		}
	}
	return false
}
