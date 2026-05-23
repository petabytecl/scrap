package releasegate

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/petabytecl/scrap/internal/config"
)

type Tier string

const (
	TierNormalPR        Tier = "normal_pr"
	TierNightly         Tier = "nightly"
	TierDedicatedRunner Tier = "dedicated_runner"
	TierRelease         Tier = "release"
)

type EvidenceStatus string

const (
	EvidenceAccepted  EvidenceStatus = "accepted"
	EvidencePassed    EvidenceStatus = "passed"
	EvidenceException EvidenceStatus = "exception"
)

type GateDefinition struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	ReadinessGate  string   `json:"readiness_gate,omitempty"`
	Tiers          []Tier   `json:"tiers"`
	Commands       []string `json:"commands,omitempty"`
	ManualArtifact string   `json:"manual_artifact,omitempty"`
	ArtifactOwner  string   `json:"artifact_owner,omitempty"`
	BlockingIssues []int    `json:"blocking_issues,omitempty"`
	Description    string   `json:"description"`
}

type Evidence struct {
	GateID    string         `json:"gate"`
	Status    EvidenceStatus `json:"status"`
	Artifact  string         `json:"artifact,omitempty"`
	Owner     string         `json:"owner,omitempty"`
	ExpiresOn string         `json:"expires_on,omitempty"`
}

type Manifest struct {
	Evidence []Evidence `json:"evidence"`
}

type MissingGate struct {
	GateID         string   `json:"gate"`
	Name           string   `json:"name"`
	ReadinessGate  string   `json:"readiness_gate,omitempty"`
	RequiredTier   Tier     `json:"required_tier"`
	Commands       []string `json:"commands,omitempty"`
	ManualArtifact string   `json:"manual_artifact,omitempty"`
	ArtifactOwner  string   `json:"artifact_owner,omitempty"`
	BlockingIssues []int    `json:"blocking_issues,omitempty"`
}

type Report struct {
	Tier    Tier          `json:"tier"`
	Ready   bool          `json:"ready"`
	Missing []MissingGate `json:"missing"`
}

func Definitions() []GateDefinition {
	defs := []GateDefinition{
		{
			ID:            "generated-code",
			Name:          "Generated code and protobuf compatibility",
			ReadinessGate: config.ReadinessGateMetadataCompatibility,
			Tiers:         []Tier{TierNormalPR, TierNightly, TierDedicatedRunner, TierRelease},
			Commands:      []string{"make proto-check"},
			Description:   "Buf lint/breaking checks, generated code, and schema cleanliness.",
		},
		{
			ID:            "compatibility",
			Name:          "Stored format and metadata compatibility",
			ReadinessGate: config.ReadinessGateMetadataCompatibility,
			Tiers:         []Tier{TierNormalPR, TierNightly, TierDedicatedRunner, TierRelease},
			Commands:      []string{"make test-compat"},
			Description:   "Mixed-version, stored-fixture, metadata, block, index, envelope, and published metadata compatibility evidence.",
		},
		{
			ID:          "unit-race-build",
			Name:        "Unit, race, lint, and build gates",
			Tiers:       []Tier{TierNormalPR, TierNightly, TierDedicatedRunner, TierRelease},
			Commands:    []string{"make check"},
			Description: "Normal PR correctness, lint, race, and binary build evidence.",
		},
		{
			ID:             "fuzz-property",
			Name:           "Fuzz and property campaigns",
			Tiers:          []Tier{TierNightly, TierDedicatedRunner, TierRelease},
			ManualArtifact: "fuzz-property-campaign-report",
			ArtifactOwner:  "release owner",
			BlockingIssues: []int{45, 85},
			Description:    "Corpus regression, fuzz, model, and property evidence for parser, codec, request, and storage boundaries.",
		},
		{
			ID:             "crash-fault",
			Name:           "Crash recovery and fault campaign",
			ReadinessGate:  config.ReadinessGateRaftMetadataDurability,
			Tiers:          []Tier{TierDedicatedRunner, TierRelease},
			ManualArtifact: "crash-fault-evidence-report",
			ArtifactOwner:  "release owner",
			BlockingIssues: []int{85},
			Description:    "Dedicated crash and fault evidence across local bytes, metadata, projections, backend jobs, restore, repair, and OpenBao fake failures.",
		},
		{
			ID:             "peer-byte-durability",
			Name:           "Peer byte durability and placement evidence",
			ReadinessGate:  config.ReadinessGatePeerByteDurability,
			Tiers:          []Tier{TierDedicatedRunner, TierRelease},
			Commands:       []string{"crash-fault-evidence-report:peer-byte-durability"},
			BlockingIssues: []int{85},
			Description:    "Peer prepare, placement, byte-serving eligibility, repair, and unsafe placement rejection evidence.",
		},
		{
			ID:             "backend-restore",
			Name:           "Backend upload, restore, and repair evidence",
			ReadinessGate:  config.ReadinessGateBackendRestoreWorkflow,
			Tiers:          []Tier{TierDedicatedRunner, TierRelease},
			Commands:       []string{"crash-fault-evidence-report:backend-restore"},
			BlockingIssues: []int{85, 88},
			Description:    "Backend upload, verify, restore, prewarm, repair, and DR rebuild evidence.",
		},
		{
			ID:             "openbao-smoke",
			Name:           "OpenBao Transit smoke evidence",
			ReadinessGate:  config.ReadinessGateOpenBaoEnvelopeWorkflow,
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "openbao-transit-smoke-report",
			ArtifactOwner:  "security owner",
			BlockingIssues: []int{86},
			Description:    "Real OpenBao key, unwrap, rewrap, outage, missing-key, and audit-device evidence.",
		},
		{
			ID:             "soak-capacity",
			Name:           "Soak and capacity evidence",
			ReadinessGate:  config.ReadinessGateCapacityAdmission,
			Tiers:          []Tier{TierDedicatedRunner, TierRelease},
			ManualArtifact: "soak-capacity-evidence-report",
			ArtifactOwner:  "capacity owner",
			BlockingIssues: []int{48, 87},
			Description:    "Representative workload, disk runway, backend lag, repair lag, restore lag, OpenBao latency, and operation backlog evidence.",
		},
		{
			ID:             "dashboard-alerts",
			Name:           "Dashboard and alert contract",
			ReadinessGate:  config.ReadinessGateOperatorReadiness,
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "dashboard-alert-contract-approval",
			ArtifactOwner:  "operations owner",
			Description:    "Write admission, disk runway, backend lag, repair lag, restore lag, corruption, Raft, OpenBao, and operation backlog signals.",
		},
		{
			ID:             "operator-runbooks",
			Name:           "Operator runbooks",
			ReadinessGate:  config.ReadinessGateOperatorReadiness,
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "operator-runbook-approval",
			ArtifactOwner:  "operations owner",
			BlockingIssues: []int{47},
			Description:    "Runbooks for corruption, restore, drain, lost disk, lost member, backend outage, OpenBao outage, and DR rebuild.",
		},
		{
			ID:             "dr-rebuild-drill",
			Name:           "Fresh-cluster DR rebuild drill",
			ReadinessGate:  config.ReadinessGateBackendRestoreWorkflow,
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "fresh-cluster-dr-rebuild-report",
			ArtifactOwner:  "operations owner",
			BlockingIssues: []int{88},
			Description:    "A full DR rebuild drill from documented steps, including measured recovery evidence.",
		},
		{
			ID:             "capacity-compliance-signoff",
			Name:           "Capacity, compliance, product, operations, security, and release signoff",
			ReadinessGate:  config.ReadinessGateCapacityAdmission,
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "production-capacity-compliance-signoff",
			ArtifactOwner:  "release owner",
			BlockingIssues: []int{48},
			Description:    "Owner-provided target profile inputs and release approval.",
		},
		{
			ID:          "codeql-security",
			Name:        "CodeQL and security analysis",
			Tiers:       []Tier{TierNormalPR, TierNightly, TierDedicatedRunner, TierRelease},
			Commands:    []string{"CodeQL Advanced workflow"},
			Description: "Go and GitHub Actions code scanning evidence.",
		},
		{
			ID:             "hardened-lint",
			Name:           "Hardened lint and baseline cleanup",
			Tiers:          []Tier{TierNormalPR, TierNightly, TierDedicatedRunner, TierRelease},
			Commands:       []string{"make lint"},
			BlockingIssues: []int{56, 57},
			Description:    "Hardened lint profile, path and conversion review, and cleanup close handling.",
		},
		{
			ID:          "vulnerability",
			Name:        "Go vulnerability scanning",
			Tiers:       []Tier{TierNormalPR, TierNightly, TierDedicatedRunner, TierRelease},
			Commands:    []string{"make vuln"},
			Description: "Go vulnerability reachability scan evidence.",
		},
		{
			ID:             "image-sbom",
			Name:           "Image and binary SBOM",
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "sbom-artifact",
			ArtifactOwner:  "release owner",
			Description:    "Binary and image software bill of materials.",
		},
		{
			ID:             "image-signature",
			Name:           "Artifact and image signatures",
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "signature-artifact",
			ArtifactOwner:  "release owner",
			Description:    "Signed binaries, images, and release artifacts.",
		},
		{
			ID:             "provenance",
			Name:           "Build provenance",
			Tiers:          []Tier{TierRelease},
			ManualArtifact: "provenance-attestation",
			ArtifactOwner:  "release owner",
			Description:    "Builder, source, command, tool versions, dependency graph, and generated-code status.",
		},
		{
			ID:             "production-write-ack-implementation",
			Name:           "Final production write ACK implementation gate",
			ReadinessGate:  config.ReadinessGateProductionImplementation,
			Tiers:          []Tier{TierRelease},
			Commands:       []string{"scrap-release-gate --tier release --manifest <release-evidence.json>"},
			BlockingIssues: []int{89},
			Description:    "Final fail-closed production ACK enablement after all target-profile evidence is accepted.",
		},
	}
	return append([]GateDefinition(nil), defs...)
}

func ParseTier(value string) (Tier, error) {
	tier := Tier(strings.TrimSpace(value))
	switch tier {
	case TierNormalPR, TierNightly, TierDedicatedRunner, TierRelease:
		return tier, nil
	default:
		return "", fmt.Errorf("unknown release gate tier %q", value)
	}
}

func RequiredForTier(tier Tier) []GateDefinition {
	defs := Definitions()
	out := make([]GateDefinition, 0, len(defs))
	for _, def := range defs {
		if slices.Contains(def.Tiers, tier) {
			out = append(out, def)
		}
	}
	return out
}

func Evaluate(tier Tier, manifest Manifest) Report {
	accepted := make(map[string]bool, len(manifest.Evidence))
	for _, evidence := range manifest.Evidence {
		if evidence.acceptsGate() {
			accepted[evidence.GateID] = true
		}
	}
	missing := make([]MissingGate, 0)
	for _, def := range RequiredForTier(tier) {
		if accepted[def.ID] {
			continue
		}
		missing = append(missing, MissingGate{
			GateID:         def.ID,
			Name:           def.Name,
			ReadinessGate:  def.ReadinessGate,
			RequiredTier:   tier,
			Commands:       append([]string(nil), def.Commands...),
			ManualArtifact: def.ManualArtifact,
			ArtifactOwner:  def.ArtifactOwner,
			BlockingIssues: append([]int(nil), def.BlockingIssues...),
		})
	}
	return Report{Tier: tier, Ready: len(missing) == 0, Missing: missing}
}

func ReadManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("manifest contains trailing JSON")
		}
		return Manifest{}, fmt.Errorf("manifest contains trailing data: %w", err)
	}
	return manifest, nil
}

func (e Evidence) acceptsGate() bool {
	switch e.Status {
	case EvidenceAccepted, EvidencePassed:
		return strings.TrimSpace(e.Artifact) != ""
	case EvidenceException:
		return strings.TrimSpace(e.Artifact) != "" &&
			strings.TrimSpace(e.Owner) != "" &&
			strings.TrimSpace(e.ExpiresOn) != ""
	default:
		return false
	}
}
