package placement

import (
	"errors"
	"fmt"
	"testing"
)

func TestPlanVotersSelectsFiveDistinctNodes(t *testing.T) {
	plan, err := PlanVoters(testMembers(7), DefaultPolicy())
	if err != nil {
		t.Fatalf("plan voters: %v", err)
	}
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

func TestPlanVotersMarksProductionUnhealthyBelowSevenEligibleNodes(t *testing.T) {
	plan, err := PlanVoters(testMembers(5), DefaultPolicy())
	if err != nil {
		t.Fatalf("plan voters: %v", err)
	}
	if plan.ProductionHealthy {
		t.Fatal("production healthy = true, want unhealthy below seven eligible nodes")
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
	if err != nil {
		t.Fatalf("plan voters: %v", err)
	}
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
	if err != nil {
		t.Fatalf("plan voters: %v", err)
	}
	for _, voter := range plan.Voters {
		if voter.MemberID == "member-offline" || voter.MemberID == "member-draining" || voter.MemberID == "member-ineligible" {
			t.Fatalf("selected unavailable voter %#v", voter)
		}
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

func testMember(memberID string, node string, zone string) Member {
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

func hasProblem(problems []Problem, reason string) bool {
	for _, problem := range problems {
		if problem.Reason == reason {
			return true
		}
	}
	return false
}
