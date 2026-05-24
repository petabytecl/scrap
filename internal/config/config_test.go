package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestDefaultConfigValidates(t *testing.T) {
	testutil.RequireNoErrorf(t, Default().Validate(), "default config did not validate")
}

func TestConfigRejectsMissingAndDuplicateAddresses(t *testing.T) {
	tests := map[string]Config{
		"missing public": {
			PublicListenAddress:  "",
			AdminListenAddress:   DefaultAdminListenAddress,
			MetricsListenAddress: DefaultMetricsListenAddress,
		},
		"missing admin": {
			PublicListenAddress:  DefaultPublicListenAddress,
			AdminListenAddress:   "",
			MetricsListenAddress: DefaultMetricsListenAddress,
		},
		"missing metrics": {
			PublicListenAddress:  DefaultPublicListenAddress,
			AdminListenAddress:   DefaultAdminListenAddress,
			MetricsListenAddress: "",
		},
		"duplicate": {
			PublicListenAddress:  "127.0.0.1:1",
			AdminListenAddress:   "127.0.0.1:1",
			MetricsListenAddress: "127.0.0.1:2",
		},
		"duplicate metrics": {
			PublicListenAddress:  "127.0.0.1:1",
			AdminListenAddress:   "127.0.0.1:2",
			MetricsListenAddress: "127.0.0.1:2",
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
	testutil.RequireNoErrorf(t, cfg.Validate(), "local non-production config did not validate")
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
	testutil.RequireNoErrorf(t, cfg.Validate(), "local filesystem backend config did not validate")
}

func TestProductionWriteACKGateFailsClosedWithoutReadinessEvidence(t *testing.T) {
	cfg := Default()
	cfg.EnableProductionWriteACK = true

	err := cfg.Validate()
	testutil.RequireErrorIsf(t, err, ErrProductionWriteACKReadinessGate, "production write ACK readiness gate")
	testutil.RequireFalsef(t, strings.Contains(err.Error(), "%!"), "error contains fmt artifact: %q", err)
	gateErr := requireProductionGateError(t, err)
	requireOpenBaoReadinessMissing(t, gateErr.Missing)
	requireDownstreamReadinessMissing(t, gateErr.Missing)
	requireProductionGateErrorText(t, err)
}

func requireOpenBaoReadinessMissing(t *testing.T, missing []ProductionReadinessMissing) {
	t.Helper()
	openbaoMissing, ok := missingReadiness(missing, ReadinessGateOpenBaoEnvelopeWorkflow)
	testutil.RequireTruef(t, ok, "missing gates = %#v, want %s", missing, ReadinessGateOpenBaoEnvelopeWorkflow)
	testutil.RequireEqualf(t, openbaoMissing.ReleaseArtifact, "openbao-envelope-release-evidence", "openbao missing release artifact")
	testutil.RequireTruef(t, intSliceContains(openbaoMissing.BlockingIssues, 86), "openbao missing gate = %#v, want #86 blocker", openbaoMissing)
}

func requireDownstreamReadinessMissing(t *testing.T, missing []ProductionReadinessMissing) {
	t.Helper()
	downstreamMissing, ok := missingReadiness(missing, ReadinessGateDownstreamDeployment)
	testutil.RequireTruef(t, ok, "missing gates = %#v, want %s", missing, ReadinessGateDownstreamDeployment)
	testutil.RequireEqualf(t, downstreamMissing.ReleaseArtifact, "downstream-deployment-approval", "downstream release artifact")
}

func requireProductionGateErrorText(t *testing.T, err error) {
	t.Helper()
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
		testutil.RequireTruef(t, strings.Contains(err.Error(), gate), "error %q does not name missing gate %s", err, gate)
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
		testutil.RequireTruef(t, strings.Contains(err.Error(), detail), "error %q does not name missing detail %s", err, detail)
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

	testutil.RequireNoErrorf(t, cfg.Validate(), "fully satisfied production write ACK config did not validate")
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
