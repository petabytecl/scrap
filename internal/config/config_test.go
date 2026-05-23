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
	for _, gate := range []string{
		ProductionWriteACKReadinessGate,
		ReadinessGateMetadataCompatibility,
		ReadinessGateRaftMetadataDurability,
		ReadinessGatePeerByteDurability,
		ReadinessGateBackendRestoreWorkflow,
		ReadinessGateOpenBaoEnvelopeWorkflow,
		ReadinessGateCapacityAdmission,
		ReadinessGateOperatorReadiness,
	} {
		if !strings.Contains(err.Error(), gate) {
			t.Fatalf("error %q does not name missing gate %s", err, gate)
		}
	}
}

func TestProductionWriteACKGateDoesNotBypassLaterReadinessGates(t *testing.T) {
	cfg := Default()
	cfg.EnableProductionWriteACK = true
	cfg.ProductionReadinessEvidence.MetadataCompatibilityBoundary = true

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

func TestProductionWriteACKGateRemainsClosedUntilProductionImplementationExists(t *testing.T) {
	cfg := Default()
	cfg.EnableProductionWriteACK = true
	cfg.ProductionReadinessEvidence = ProductionReadinessEvidence{
		MetadataCompatibilityBoundary: true,
		RaftMetadataDurability:        true,
		PeerByteDurability:            true,
		BackendRestoreWorkflow:        true,
		OpenBaoEnvelopeWorkflow:       true,
		CapacityAdmission:             true,
		OperatorReadiness:             true,
	}

	err := cfg.Validate()
	if !errors.Is(err, ErrProductionWriteACKReadinessGate) {
		t.Fatalf("error = %v, want %v", err, ErrProductionWriteACKReadinessGate)
	}
	if !strings.Contains(err.Error(), ReadinessGateProductionImplementation) {
		t.Fatalf("error %q does not identify missing production implementation gate", err)
	}
}
