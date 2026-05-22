package placement

import (
	"errors"
	"fmt"
	"sort"
)

const (
	defaultVoterCount                 = 5
	defaultMinProductionEligibleNodes = 7
)

var ErrInsufficientDistinctNodes = errors.New("placement: insufficient distinct eligible nodes")

type Policy struct {
	VoterCount                 int
	MinProductionEligibleNodes int
}

type Member struct {
	MemberID       string
	KubernetesNode string
	Zone           string
	Eligible       bool
	Online         bool
	Draining       bool
}

type Plan struct {
	Voters            []Member
	ProductionHealthy bool
	Problems          []Problem
}

type Problem struct {
	Reason string
	Detail string
}

const (
	ReasonTooFewEligibleNodes    = "SCRAP_PLACEMENT_TOO_FEW_ELIGIBLE_NODES"
	ReasonTooFewDistinctNodes    = "SCRAP_PLACEMENT_TOO_FEW_DISTINCT_NODES"
	ReasonSelectedDuplicateNode  = "SCRAP_PLACEMENT_SELECTED_DUPLICATE_NODE"
	ReasonSelectedIneligibleNode = "SCRAP_PLACEMENT_SELECTED_INELIGIBLE_NODE"
)

func DefaultPolicy() Policy {
	return Policy{
		VoterCount:                 defaultVoterCount,
		MinProductionEligibleNodes: defaultMinProductionEligibleNodes,
	}
}

func PlanVoters(members []Member, policy Policy) (Plan, error) {
	policy = normalizePolicy(policy)
	candidates := eligibleCandidates(members)
	distinctEligibleNodes := countDistinctNodes(candidates)
	plan := Plan{ProductionHealthy: true}
	if distinctEligibleNodes < policy.MinProductionEligibleNodes {
		plan.ProductionHealthy = false
		plan.Problems = append(plan.Problems, Problem{
			Reason: ReasonTooFewEligibleNodes,
			Detail: fmt.Sprintf("eligible distinct nodes = %d, minimum production eligible nodes = %d", distinctEligibleNodes, policy.MinProductionEligibleNodes),
		})
	}
	if distinctEligibleNodes < policy.VoterCount {
		plan.Problems = append(plan.Problems, Problem{
			Reason: ReasonTooFewDistinctNodes,
			Detail: fmt.Sprintf("eligible distinct nodes = %d, voter count = %d", distinctEligibleNodes, policy.VoterCount),
		})
		return plan, ErrInsufficientDistinctNodes
	}

	plan.Voters = selectVoters(candidates, policy.VoterCount)
	if problems := validateVoters(plan.Voters, policy); len(problems) > 0 {
		plan.ProductionHealthy = false
		plan.Problems = append(plan.Problems, problems...)
	}
	return plan, nil
}

func normalizePolicy(policy Policy) Policy {
	if policy.VoterCount == 0 {
		policy.VoterCount = defaultVoterCount
	}
	if policy.MinProductionEligibleNodes == 0 {
		policy.MinProductionEligibleNodes = defaultMinProductionEligibleNodes
	}
	return policy
}

func eligibleCandidates(members []Member) []Member {
	candidates := make([]Member, 0, len(members))
	for _, member := range members {
		if member.MemberID == "" || member.KubernetesNode == "" {
			continue
		}
		if !member.Eligible || !member.Online || member.Draining {
			continue
		}
		candidates = append(candidates, member)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].MemberID != candidates[j].MemberID {
			return candidates[i].MemberID < candidates[j].MemberID
		}
		return candidates[i].KubernetesNode < candidates[j].KubernetesNode
	})
	return candidates
}

func countDistinctNodes(members []Member) int {
	nodes := make(map[string]struct{}, len(members))
	for _, member := range members {
		nodes[member.KubernetesNode] = struct{}{}
	}
	return len(nodes)
}

func selectVoters(candidates []Member, voterCount int) []Member {
	voters := make([]Member, 0, voterCount)
	usedNodes := make(map[string]struct{}, voterCount)
	zoneCounts := make(map[string]int)
	for len(voters) < voterCount {
		best := -1
		bestZoneCount := 0
		for i, candidate := range candidates {
			if _, ok := usedNodes[candidate.KubernetesNode]; ok {
				continue
			}
			zone := zoneKey(candidate.Zone)
			count := zoneCounts[zone]
			if best == -1 || count < bestZoneCount || (count == bestZoneCount && candidate.MemberID < candidates[best].MemberID) {
				best = i
				bestZoneCount = count
			}
		}
		if best == -1 {
			return voters
		}
		selected := candidates[best]
		voters = append(voters, selected)
		usedNodes[selected.KubernetesNode] = struct{}{}
		zoneCounts[zoneKey(selected.Zone)]++
	}
	return voters
}

func validateVoters(voters []Member, policy Policy) []Problem {
	var problems []Problem
	if len(voters) != policy.VoterCount {
		problems = append(problems, Problem{
			Reason: ReasonTooFewDistinctNodes,
			Detail: fmt.Sprintf("selected voters = %d, voter count = %d", len(voters), policy.VoterCount),
		})
	}
	seenNodes := make(map[string]string, len(voters))
	for _, voter := range voters {
		if voter.MemberID == "" || voter.KubernetesNode == "" || !voter.Eligible || !voter.Online || voter.Draining {
			problems = append(problems, Problem{
				Reason: ReasonSelectedIneligibleNode,
				Detail: fmt.Sprintf("member %q on node %q is not eligible for voting placement", voter.MemberID, voter.KubernetesNode),
			})
			continue
		}
		if existing, ok := seenNodes[voter.KubernetesNode]; ok {
			problems = append(problems, Problem{
				Reason: ReasonSelectedDuplicateNode,
				Detail: fmt.Sprintf("members %q and %q share node %q", existing, voter.MemberID, voter.KubernetesNode),
			})
			continue
		}
		seenNodes[voter.KubernetesNode] = voter.MemberID
	}
	return problems
}

func zoneKey(zone string) string {
	if zone == "" {
		return "\xff"
	}
	return zone
}
