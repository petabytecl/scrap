package releasegate

import (
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/config"
)

func TestDefinitionsHaveAutomationOrManualOwner(t *testing.T) {
	for _, def := range Definitions() {
		if def.ID == "" || def.Name == "" || len(def.Tiers) == 0 {
			t.Fatalf("definition = %#v, want id, name, and tiers", def)
		}
		if len(def.Commands) == 0 && (def.ManualArtifact == "" || def.ArtifactOwner == "") {
			t.Fatalf("definition %s has neither automation nor named manual artifact owner", def.ID)
		}
	}
}

func TestTierRequirementsAreDistinct(t *testing.T) {
	normal := RequiredForTier(TierNormalPR)
	nightly := RequiredForTier(TierNightly)
	dedicated := RequiredForTier(TierDedicatedRunner)
	release := RequiredForTier(TierRelease)

	if len(normal) == 0 || len(nightly) == 0 || len(dedicated) == 0 || len(release) == 0 {
		t.Fatalf("tier lengths normal=%d nightly=%d dedicated=%d release=%d, want all non-empty", len(normal), len(nightly), len(dedicated), len(release))
	}
	if len(normal) >= len(release) {
		t.Fatalf("normal PR gates = %d, release gates = %d, want release gate set to be broader", len(normal), len(release))
	}
	if !hasGate(release, "openbao-smoke") || hasGate(normal, "openbao-smoke") {
		t.Fatal("openbao-smoke should be release-only")
	}
	if !hasGate(dedicated, "crash-fault") || hasGate(normal, "crash-fault") {
		t.Fatal("crash-fault should require dedicated/release evidence, not normal PR evidence")
	}
}

func TestEvaluateReportsMissingReadinessEvidence(t *testing.T) {
	report := Evaluate(TierRelease, Manifest{})

	if report.Ready {
		t.Fatal("empty release evidence manifest was ready")
	}
	want := map[string]string{
		"generated-code":                      config.ReadinessGateMetadataCompatibility,
		"crash-fault":                         config.ReadinessGateRaftMetadataDurability,
		"peer-byte-durability":                config.ReadinessGatePeerByteDurability,
		"backend-restore":                     config.ReadinessGateBackendRestoreWorkflow,
		"openbao-smoke":                       config.ReadinessGateOpenBaoEnvelopeWorkflow,
		"soak-capacity":                       config.ReadinessGateCapacityAdmission,
		"dashboard-alerts":                    config.ReadinessGateOperatorReadiness,
		"production-write-ack-implementation": config.ReadinessGateProductionImplementation,
	}
	for gateID, readinessGate := range want {
		missing, ok := missingGate(report, gateID)
		if !ok {
			t.Fatalf("missing gates = %#v, want %s", report.Missing, gateID)
		}
		if missing.ReadinessGate != readinessGate {
			t.Fatalf("gate %s readiness = %q, want %q", gateID, missing.ReadinessGate, readinessGate)
		}
		if len(missing.Commands) == 0 && missing.ManualArtifact == "" {
			t.Fatalf("gate %s has no command or manual artifact in report: %#v", gateID, missing)
		}
	}
}

func TestCrashFaultGatesPointToDedicatedEvidenceCommand(t *testing.T) {
	for _, gateID := range []string{"crash-fault", "peer-byte-durability", "backend-restore"} {
		def, ok := definition(gateID)
		if !ok {
			t.Fatalf("missing definition %s", gateID)
		}
		if !hasCommand(def, "make crash-fault-evidence") {
			t.Fatalf("definition %s commands = %#v, want make crash-fault-evidence", gateID, def.Commands)
		}
		if gateID != "backend-restore" && len(def.BlockingIssues) != 0 {
			t.Fatalf("definition %s blockers = %#v, want no #85 blocker after command exists", gateID, def.BlockingIssues)
		}
	}
}

func TestClosedImplementationIssuesAreNotReportedAsGateBlockers(t *testing.T) {
	for _, gateID := range []string{"fuzz-property", "hardened-lint", "crash-fault", "peer-byte-durability"} {
		def, ok := definition(gateID)
		if !ok {
			t.Fatalf("missing definition %s", gateID)
		}
		if len(def.BlockingIssues) != 0 {
			t.Fatalf("definition %s blockers = %#v, want no closed implementation blockers", gateID, def.BlockingIssues)
		}
	}
}

func TestEvaluateAcceptsCompleteReleaseManifest(t *testing.T) {
	manifest := Manifest{Evidence: make([]Evidence, 0, len(RequiredForTier(TierRelease)))}
	for _, def := range RequiredForTier(TierRelease) {
		manifest.Evidence = append(manifest.Evidence, Evidence{
			GateID:   def.ID,
			Status:   EvidenceAccepted,
			Artifact: "artifact://" + def.ID,
		})
	}

	report := Evaluate(TierRelease, manifest)
	if !report.Ready || len(report.Missing) != 0 {
		t.Fatalf("report = %#v, want ready", report)
	}
}

func TestExceptionRequiresOwnerAndExpiry(t *testing.T) {
	report := Evaluate(TierRelease, Manifest{Evidence: []Evidence{
		{GateID: "openbao-smoke", Status: EvidenceException, Artifact: "exception://openbao"},
	}})
	if _, ok := missingGate(report, "openbao-smoke"); !ok {
		t.Fatal("exception without owner and expiry satisfied gate")
	}

	report = Evaluate(TierRelease, Manifest{Evidence: []Evidence{
		{
			GateID:    "openbao-smoke",
			Status:    EvidenceException,
			Artifact:  "exception://openbao",
			Owner:     "security owner",
			ExpiresOn: "2026-06-30",
		},
	}})
	if _, ok := missingGate(report, "openbao-smoke"); ok {
		t.Fatal("complete exception evidence did not satisfy gate")
	}
}

func TestReadManifestRejectsUnknownFields(t *testing.T) {
	_, err := ReadManifest(strings.NewReader(`{"evidence":[],"extra":true}`))
	if err == nil {
		t.Fatal("manifest with unknown field was accepted")
	}
}

func TestReadManifestRejectsTrailingJSON(t *testing.T) {
	_, err := ReadManifest(strings.NewReader(`{"evidence":[]}{"evidence":[]}`))
	if err == nil {
		t.Fatal("manifest with trailing JSON was accepted")
	}
}

func hasGate(defs []GateDefinition, gateID string) bool {
	for _, def := range defs {
		if def.ID == gateID {
			return true
		}
	}
	return false
}

func missingGate(report Report, gateID string) (MissingGate, bool) {
	for _, missing := range report.Missing {
		if missing.GateID == gateID {
			return missing, true
		}
	}
	return MissingGate{}, false
}

func definition(gateID string) (GateDefinition, bool) {
	for _, def := range Definitions() {
		if def.ID == gateID {
			return def, true
		}
	}
	return GateDefinition{}, false
}

func hasCommand(def GateDefinition, command string) bool {
	for _, candidate := range def.Commands {
		if candidate == command {
			return true
		}
	}
	return false
}
