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

var (
	ErrUnsafePlacement           = errors.New("placement: unsafe replica placement")
	ErrInsufficientDistinctNodes = errors.New("placement: insufficient distinct eligible nodes")
)

type RiskMode string

const (
	RiskModeProduction            RiskMode = ""
	RiskModeSharedPool            RiskMode = "shared-pool"
	RiskModeReducedFaultTolerance RiskMode = "reduced-fault-tolerance"
)

type Policy struct {
	VoterCount                 int
	MinProductionEligibleNodes int
	RiskMode                   RiskMode
	RequiredNodeLabels         map[string]string
}

type Member struct {
	MemberID       string
	KubernetesNode string
	Zone           string
	StoragePool    string
	NodeLabels     map[string]string
	Eligible       bool
	Online         bool
	Draining       bool
}

type Plan struct {
	Voters []Member

	RiskMode          RiskMode
	RiskAccepted      bool
	ProductionHealthy bool
	Problems          []Problem
}

type Problem struct {
	Reason Reason
	Detail string
}

type Reason string

const (
	ReasonTooFewEligibleNodes    Reason = "SCRAP_PLACEMENT_TOO_FEW_ELIGIBLE_NODES"
	ReasonTooFewDistinctNodes    Reason = "SCRAP_PLACEMENT_TOO_FEW_DISTINCT_NODES"
	ReasonSelectedDuplicateNode  Reason = "SCRAP_PLACEMENT_SELECTED_DUPLICATE_NODE"
	ReasonSelectedSharedPool     Reason = "SCRAP_PLACEMENT_SELECTED_SHARED_POOL"
	ReasonSelectedIneligibleNode Reason = "SCRAP_PLACEMENT_SELECTED_INELIGIBLE_NODE"
	ReasonNodeLabelMismatch      Reason = "SCRAP_PLACEMENT_NODE_LABEL_MISMATCH"
	ReasonUnsupportedRiskMode    Reason = "SCRAP_PLACEMENT_UNSUPPORTED_RISK_MODE"
)

func DefaultPolicy() Policy {
	return Policy{
		VoterCount:                 defaultVoterCount,
		MinProductionEligibleNodes: defaultMinProductionEligibleNodes,
	}
}

func PlanVoters(members []Member, policy Policy) (Plan, error) {
	policy = normalizePolicy(policy)
	plan := Plan{RiskMode: policy.RiskMode}
	if !supportedRiskMode(policy.RiskMode) {
		plan.Problems = append(plan.Problems, Problem{
			Reason: ReasonUnsupportedRiskMode,
			Detail: fmt.Sprintf("risk mode %q is not supported", policy.RiskMode),
		})
		return finishPlan(plan, policy)
	}

	candidates, skippedProblems := eligibleCandidates(members, policy)
	distinctEligibleNodes := countDistinctNodes(candidates)
	skippedProblemsReported := false
	if distinctEligibleNodes < policy.MinProductionEligibleNodes {
		plan.Problems = append(plan.Problems, Problem{
			Reason: ReasonTooFewEligibleNodes,
			Detail: fmt.Sprintf("eligible distinct nodes = %d, minimum production eligible nodes = %d", distinctEligibleNodes, policy.MinProductionEligibleNodes),
		})
		plan.Problems = append(plan.Problems, skippedProblems...)
		skippedProblemsReported = true
	}
	if distinctEligibleNodes < policy.VoterCount {
		if !skippedProblemsReported {
			plan.Problems = append(plan.Problems, skippedProblems...)
		}
		plan.Problems = append(plan.Problems, Problem{
			Reason: ReasonTooFewDistinctNodes,
			Detail: fmt.Sprintf("eligible distinct nodes = %d, voter count = %d", distinctEligibleNodes, policy.VoterCount),
		})
		return finishPlan(plan, policy)
	}

	enforceDistinctStoragePools := policy.RiskMode != RiskModeSharedPool
	plan.Voters = selectVoters(candidates, policy.VoterCount, enforceDistinctStoragePools)
	if len(plan.Voters) < policy.VoterCount && enforceDistinctStoragePools {
		plan.Problems = append(plan.Problems, Problem{
			Reason: ReasonSelectedSharedPool,
			Detail: fmt.Sprintf("eligible storage pools = %d, voter count = %d", countDistinctStoragePools(candidates), policy.VoterCount),
		})
		return finishPlan(plan, policy)
	}
	if problems := validateVoters(plan.Voters, policy); len(problems) > 0 {
		plan.Problems = append(plan.Problems, problems...)
	}
	return finishPlan(plan, policy)
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

func supportedRiskMode(mode RiskMode) bool {
	switch mode {
	case RiskModeProduction, RiskModeSharedPool, RiskModeReducedFaultTolerance:
		return true
	default:
		return false
	}
}

func eligibleCandidates(members []Member, policy Policy) ([]Member, []Problem) {
	candidates := make([]Member, 0, len(members))
	var problems []Problem
	for _, member := range members {
		if member.MemberID == "" || member.KubernetesNode == "" {
			continue
		}
		if !member.Eligible || !member.Online || member.Draining {
			continue
		}
		if problem, ok := nodeLabelProblem(member, policy.RequiredNodeLabels); ok {
			problems = append(problems, problem)
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
	return candidates, problems
}

func countDistinctNodes(members []Member) int {
	nodes := make(map[string]struct{}, len(members))
	for _, member := range members {
		nodes[member.KubernetesNode] = struct{}{}
	}
	return len(nodes)
}

func countDistinctStoragePools(members []Member) int {
	pools := make(map[string]struct{}, len(members))
	for _, member := range members {
		pools[storagePoolKey(member)] = struct{}{}
	}
	return len(pools)
}

func selectVoters(candidates []Member, voterCount int, enforceDistinctStoragePools bool) []Member {
	voters := make([]Member, 0, voterCount)
	usedNodes := make(map[string]struct{}, voterCount)
	usedStoragePools := make(map[string]struct{}, voterCount)
	zoneCounts := make(map[string]int)
	for len(voters) < voterCount {
		best := -1
		bestZoneCount := 0
		for i, candidate := range candidates {
			if _, ok := usedNodes[candidate.KubernetesNode]; ok {
				continue
			}
			if enforceDistinctStoragePools {
				if _, ok := usedStoragePools[storagePoolKey(candidate)]; ok {
					continue
				}
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
		usedStoragePools[storagePoolKey(selected)] = struct{}{}
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
	seenStoragePools := make(map[string]string, len(voters))
	for _, voter := range voters {
		if voter.MemberID == "" || voter.KubernetesNode == "" || !voter.Eligible || !voter.Online || voter.Draining {
			problems = append(problems, Problem{
				Reason: ReasonSelectedIneligibleNode,
				Detail: fmt.Sprintf("member %q on node %q is not eligible for voting placement", voter.MemberID, voter.KubernetesNode),
			})
			continue
		}
		if problem, ok := nodeLabelProblem(voter, policy.RequiredNodeLabels); ok {
			problems = append(problems, problem)
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
		pool := storagePoolKey(voter)
		if existing, ok := seenStoragePools[pool]; ok {
			problems = append(problems, Problem{
				Reason: ReasonSelectedSharedPool,
				Detail: fmt.Sprintf("members %q and %q share storage pool %q", existing, voter.MemberID, pool),
			})
			continue
		}
		seenStoragePools[pool] = voter.MemberID
	}
	return problems
}

func finishPlan(plan Plan, policy Policy) (Plan, error) {
	if len(plan.Problems) == 0 {
		plan.ProductionHealthy = true
		return plan, nil
	}
	err := placementError(plan.Problems, policy)
	plan.ProductionHealthy = false
	plan.RiskAccepted = err == nil
	if err != nil {
		plan.Voters = nil
	}
	return plan, err
}

func placementError(problems []Problem, policy Policy) error {
	for _, problem := range problems {
		if problem.Reason == ReasonTooFewDistinctNodes {
			return ErrInsufficientDistinctNodes
		}
	}
	for _, problem := range problems {
		if problemAllowedByRiskMode(problem.Reason, policy.RiskMode) {
			continue
		}
		return ErrUnsafePlacement
	}
	return nil
}

func problemAllowedByRiskMode(reason Reason, mode RiskMode) bool {
	switch mode {
	case RiskModeReducedFaultTolerance:
		return reason == ReasonTooFewEligibleNodes
	case RiskModeSharedPool:
		return reason == ReasonSelectedSharedPool
	default:
		return false
	}
}

func nodeLabelProblem(member Member, required map[string]string) (Problem, bool) {
	keys := make([]string, 0, len(required))
	for key := range required {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		want := required[key]
		got, ok := member.NodeLabels[key]
		if !ok {
			return Problem{
				Reason: ReasonNodeLabelMismatch,
				Detail: fmt.Sprintf("member %q on node %q is missing required label %q=%q", member.MemberID, member.KubernetesNode, key, want),
			}, true
		}
		if got != want {
			return Problem{
				Reason: ReasonNodeLabelMismatch,
				Detail: fmt.Sprintf("member %q on node %q label %q=%q, want %q", member.MemberID, member.KubernetesNode, key, got, want),
			}, true
		}
	}
	return Problem{}, false
}

func storagePoolKey(member Member) string {
	if member.StoragePool != "" {
		return member.StoragePool
	}
	return member.KubernetesNode
}

func zoneKey(zone string) string {
	if zone == "" {
		return "\xff"
	}
	return zone
}
