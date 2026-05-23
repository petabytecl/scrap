package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config did not validate: %v", err)
	}
}

func TestConfigRejectsMissingAndDuplicateAddresses(t *testing.T) {
	tests := map[string]Config{
		"missing public": {
			PublicListenAddress: "",
			AdminListenAddress:  DefaultAdminListenAddress,
		},
		"missing admin": {
			PublicListenAddress: DefaultPublicListenAddress,
			AdminListenAddress:  "",
		},
		"duplicate": {
			PublicListenAddress: "127.0.0.1:1",
			AdminListenAddress:  "127.0.0.1:1",
		},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLocalNonProductionStorageRequiresExplicitEnableAndDataDir(t *testing.T) {
	tests := map[string]Config{
		"enabled without data dir": {
			PublicListenAddress:             DefaultPublicListenAddress,
			AdminListenAddress:              DefaultAdminListenAddress,
			EnableLocalNonProductionStorage: true,
		},
		"data dir without enable": {
			PublicListenAddress: DefaultPublicListenAddress,
			AdminListenAddress:  DefaultAdminListenAddress,
			LocalDataDir:        "/tmp/scrap",
		},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	cfg := Default()
	cfg.EnableLocalNonProductionStorage = true
	cfg.LocalDataDir = t.TempDir()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("local non-production config did not validate: %v", err)
	}
}

func TestLocalFilesystemBackendRequiresExplicitEnableStorageAndDataDir(t *testing.T) {
	tests := map[string]Config{
		"enabled without local storage": {
			PublicListenAddress:          DefaultPublicListenAddress,
			AdminListenAddress:           DefaultAdminListenAddress,
			EnableLocalFilesystemBackend: true,
			LocalBackendDataDir:          "/tmp/scrap-backend",
			BackendUploadInterval:        DefaultBackendUploadInterval,
		},
		"enabled without backend data dir": {
			PublicListenAddress:             DefaultPublicListenAddress,
			AdminListenAddress:              DefaultAdminListenAddress,
			EnableLocalNonProductionStorage: true,
			LocalDataDir:                    "/tmp/scrap",
			EnableLocalFilesystemBackend:    true,
			BackendUploadInterval:           DefaultBackendUploadInterval,
		},
		"backend data dir without enable": {
			PublicListenAddress:             DefaultPublicListenAddress,
			AdminListenAddress:              DefaultAdminListenAddress,
			EnableLocalNonProductionStorage: true,
			LocalDataDir:                    "/tmp/scrap",
			LocalBackendDataDir:             "/tmp/scrap-backend",
			BackendUploadInterval:           DefaultBackendUploadInterval,
		},
		"non-positive upload interval": {
			PublicListenAddress:   DefaultPublicListenAddress,
			AdminListenAddress:    DefaultAdminListenAddress,
			BackendUploadInterval: 0,
			OperationRunInterval:  DefaultOperationRunInterval,
		},
		"non-positive operation interval": {
			PublicListenAddress:   DefaultPublicListenAddress,
			AdminListenAddress:    DefaultAdminListenAddress,
			BackendUploadInterval: DefaultBackendUploadInterval,
			OperationRunInterval:  0,
			LocalSealBlockAtBytes: DefaultLocalSealBlockAtBytes,
		},
		"zero local seal block bytes": {
			PublicListenAddress:   DefaultPublicListenAddress,
			AdminListenAddress:    DefaultAdminListenAddress,
			BackendUploadInterval: DefaultBackendUploadInterval,
			OperationRunInterval:  DefaultOperationRunInterval,
			LocalSealBlockAtBytes: 0,
		},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	cfg := Default()
	cfg.EnableLocalNonProductionStorage = true
	cfg.LocalDataDir = t.TempDir()
	cfg.EnableLocalFilesystemBackend = true
	cfg.LocalBackendDataDir = t.TempDir()
	cfg.BackendUploadInterval = 10 * time.Millisecond
	if err := cfg.Validate(); err != nil {
		t.Fatalf("local filesystem backend config did not validate: %v", err)
	}
}

func TestProductionWriteACKGateFailsClosedWithoutReadinessEvidence(t *testing.T) {
	cfg := Default()
	cfg.EnableProductionWriteACK = true

	err := cfg.Validate()
	if !errors.Is(err, ErrProductionWriteACKReadinessGate) {
		t.Fatalf("error = %v, want %v", err, ErrProductionWriteACKReadinessGate)
	}
	gateErr := requireProductionGateError(t, err)
	openbaoMissing, ok := missingReadiness(gateErr.Missing, ReadinessGateOpenBaoEnvelopeWorkflow)
	if !ok {
		t.Fatalf("missing gates = %#v, want %s", gateErr.Missing, ReadinessGateOpenBaoEnvelopeWorkflow)
	}
	if openbaoMissing.ReleaseArtifact != "openbao-envelope-release-evidence" || !intSliceContains(openbaoMissing.BlockingIssues, 86) {
		t.Fatalf("openbao missing gate = %#v, want artifact and #86 blocker", openbaoMissing)
	}
	downstreamMissing, ok := missingReadiness(gateErr.Missing, ReadinessGateDownstreamDeployment)
	if !ok {
		t.Fatalf("missing gates = %#v, want %s", gateErr.Missing, ReadinessGateDownstreamDeployment)
	}
	if downstreamMissing.ReleaseArtifact != "downstream-deployment-approval" {
		t.Fatalf("downstream missing gate = %#v, want release artifact", downstreamMissing)
	}
	for _, gate := range []string{
		ProductionWriteACKReadinessGate,
		ReadinessGateReleaseProfile,
		ReadinessGateMetadataCompatibility,
		ReadinessGateRaftMetadataDurability,
		ReadinessGatePeerByteDurability,
		ReadinessGateBackendRestoreWorkflow,
		ReadinessGateOpenBaoEnvelopeWorkflow,
		ReadinessGateCapacityAdmission,
		ReadinessGateOperatorReadiness,
		ReadinessGateProductionImplementation,
		ReadinessGateReleaseOwnerSignoff,
		ReadinessGateDownstreamDeployment,
	} {
		if !strings.Contains(err.Error(), gate) {
			t.Fatalf("error %q does not name missing gate %s", err, gate)
		}
	}
	for _, detail := range []string{
		"target-release-profile",
		"metadata-compatibility-release-evidence",
		"openbao-envelope-release-evidence",
		"production-write-ack-implementation-evidence",
		"release-owner-signoff",
		"downstream-deployment-approval",
		"#86",
		"#88",
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("error %q does not name missing detail %s", err, detail)
		}
	}
}

func TestProductionWriteACKGateDoesNotBypassLaterReadinessGates(t *testing.T) {
	cfg := Default()
	cfg.EnableProductionWriteACK = true
	cfg.ProductionReadinessEvidence.MetadataCompatibilityBoundary = true
	cfg.ProductionReadinessEvidence.MetadataCompatibilityArtifact = "artifact://metadata"
	cfg.ProductionReadinessEvidence.TargetReleaseProfileID = DefaultProductionProfileID

	err := cfg.Validate()
	if !errors.Is(err, ErrProductionWriteACKReadinessGate) {
		t.Fatalf("error = %v, want %v", err, ErrProductionWriteACKReadinessGate)
	}
	if strings.Contains(err.Error(), ReadinessGateMetadataCompatibility) {
		t.Fatalf("error %q still lists satisfied metadata compatibility gate", err)
	}
	for _, gate := range []string{
		ReadinessGateRaftMetadataDurability,
		ReadinessGatePeerByteDurability,
		ReadinessGateBackendRestoreWorkflow,
		ReadinessGateOpenBaoEnvelopeWorkflow,
		ReadinessGateCapacityAdmission,
		ReadinessGateOperatorReadiness,
		ReadinessGateProductionImplementation,
		ReadinessGateReleaseOwnerSignoff,
		ReadinessGateDownstreamDeployment,
	} {
		if !strings.Contains(err.Error(), gate) {
			t.Fatalf("error %q does not keep later readiness gate %s closed", err, gate)
		}
	}
}

func TestProductionWriteACKGateRejectsLocalNonProductionMode(t *testing.T) {
	cfg := Default()
	cfg.EnableProductionWriteACK = true
	cfg.EnableLocalNonProductionStorage = true
	cfg.LocalDataDir = t.TempDir()

	err := cfg.Validate()
	if !errors.Is(err, ErrProductionWriteACKReadinessGate) {
		t.Fatalf("error = %v, want %v", err, ErrProductionWriteACKReadinessGate)
	}
	if !strings.Contains(err.Error(), "local non-production storage") {
		t.Fatalf("error %q does not identify non-production mode conflict", err)
	}
}

func TestProductionWriteACKGateRequiresArtifactsNotOnlyBooleans(t *testing.T) {
	cfg := Default()
	cfg.EnableProductionWriteACK = true
	cfg.ProductionReadinessEvidence = ProductionReadinessEvidence{
		TargetReleaseProfileID:           DefaultProductionProfileID,
		MetadataCompatibilityBoundary:    true,
		RaftMetadataDurability:           true,
		PeerByteDurability:               true,
		BackendRestoreWorkflow:           true,
		OpenBaoEnvelopeWorkflow:          true,
		CapacityAdmission:                true,
		OperatorReadiness:                true,
		ProductionWriteACKImplementation: true,
		ReleaseOwnerSignoff:              true,
		DownstreamDeploymentApproval:     true,
	}

	err := cfg.Validate()
	if !errors.Is(err, ErrProductionWriteACKReadinessGate) {
		t.Fatalf("error = %v, want %v", err, ErrProductionWriteACKReadinessGate)
	}
	for _, artifact := range []string{
		"metadata-compatibility-release-evidence",
		"production-write-ack-implementation-evidence",
		"release-owner-signoff",
		"downstream-deployment-approval",
	} {
		if !strings.Contains(err.Error(), artifact) {
			t.Fatalf("error %q does not identify missing artifact %s", err, artifact)
		}
	}
}

func TestProductionWriteACKGateRejectsLocalRehearsalWithoutDownstreamApproval(t *testing.T) {
	cfg := productionWriteACKReadyConfig()
	cfg.ProductionReadinessEvidence.DownstreamDeploymentApproval = false
	cfg.ProductionReadinessEvidence.DownstreamDeploymentArtifact = ""
	cfg.ProductionReadinessEvidence.DownstreamDeploymentDeferral = "local release-artifact rehearsal only"

	err := cfg.Validate()
	if !errors.Is(err, ErrProductionWriteACKReadinessGate) {
		t.Fatalf("error = %v, want %v", err, ErrProductionWriteACKReadinessGate)
	}
	for _, want := range []string{
		ReadinessGateDownstreamDeployment,
		"downstream-deployment-approval",
		"local release-artifact rehearsal only",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not report downstream deployment gap %s", err, want)
		}
	}
}

func TestProductionWriteACKGateAcceptsFullySatisfiedProfile(t *testing.T) {
	cfg := productionWriteACKReadyConfig()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("fully satisfied production write ACK config did not validate: %v", err)
	}
}

func productionWriteACKReadyConfig() Config {
	cfg := Default()
	cfg.EnableProductionWriteACK = true
	cfg.ProductionReadinessEvidence = ProductionReadinessEvidence{
		TargetReleaseProfileID:           DefaultProductionProfileID,
		MetadataCompatibilityBoundary:    true,
		MetadataCompatibilityArtifact:    "artifact://metadata-compatibility",
		RaftMetadataDurability:           true,
		RaftMetadataDurabilityArtifact:   "artifact://raft-metadata",
		PeerByteDurability:               true,
		PeerByteDurabilityArtifact:       "artifact://peer-byte-durability",
		BackendRestoreWorkflow:           true,
		BackendRestoreArtifact:           "artifact://backend-restore",
		OpenBaoEnvelopeWorkflow:          true,
		OpenBaoEnvelopeArtifact:          "artifact://openbao-envelope",
		CapacityAdmission:                true,
		CapacityAdmissionArtifact:        "artifact://capacity-admission",
		OperatorReadiness:                true,
		OperatorReadinessArtifact:        "artifact://operator-readiness",
		ProductionWriteACKImplementation: true,
		ProductionImplementationArtifact: "artifact://production-write-ack-implementation",
		ReleaseOwnerSignoff:              true,
		ReleaseOwnerSignoffArtifact:      "artifact://release-owner-signoff",
		DownstreamDeploymentApproval:     true,
		DownstreamDeploymentArtifact:     "artifact://downstream-deployment-approval",
	}
	return cfg
}

func requireProductionGateError(t *testing.T, err error) *ProductionWriteACKGateError {
	t.Helper()
	var gateErr *ProductionWriteACKGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("error = %#v, want ProductionWriteACKGateError", err)
	}
	return gateErr
}

func missingReadiness(missing []ProductionReadinessMissing, gate string) (ProductionReadinessMissing, bool) {
	for _, candidate := range missing {
		if candidate.ReadinessGate == gate {
			return candidate, true
		}
	}
	return ProductionReadinessMissing{}, false
}

func intSliceContains(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
