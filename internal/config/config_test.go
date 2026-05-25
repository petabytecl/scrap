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
	tests := map[string]struct {
		cfg     Config
		wantErr string
	}{
		"missing public": {
			cfg: Config{
				PublicListenAddress:  "",
				AdminListenAddress:   DefaultAdminListenAddress,
				MetricsListenAddress: DefaultMetricsListenAddress,
			},
			wantErr: "public_listen_address is required",
		},
		"missing admin": {
			cfg: Config{
				PublicListenAddress:  DefaultPublicListenAddress,
				AdminListenAddress:   "",
				MetricsListenAddress: DefaultMetricsListenAddress,
			},
			wantErr: "admin_listen_address is required",
		},
		"missing metrics": {
			cfg: Config{
				PublicListenAddress:  DefaultPublicListenAddress,
				AdminListenAddress:   DefaultAdminListenAddress,
				MetricsListenAddress: "",
			},
			wantErr: "metrics_listen_address is required",
		},
		"duplicate": {
			cfg: Config{
				PublicListenAddress:  "127.0.0.1:1",
				AdminListenAddress:   "127.0.0.1:1",
				MetricsListenAddress: "127.0.0.1:2",
			},
			wantErr: "public_listen_address and admin_listen_address listen addresses must be distinct",
		},
		"duplicate metrics": {
			cfg: Config{
				PublicListenAddress:  "127.0.0.1:1",
				AdminListenAddress:   "127.0.0.1:2",
				MetricsListenAddress: "127.0.0.1:2",
			},
			wantErr: "admin_listen_address and metrics_listen_address listen addresses must be distinct",
		},
		"duplicate admin ui": {
			cfg: Config{
				PublicListenAddress:  "127.0.0.1:1",
				AdminListenAddress:   "127.0.0.1:2",
				MetricsListenAddress: "127.0.0.1:3",
				AdminUIListenAddress: "127.0.0.1:2",
			},
			wantErr: "admin_listen_address and admin_ui_listen_address listen addresses must be distinct",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			testutil.RequireEqualf(t, err.Error(), tt.wantErr, "validation error")
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

func TestAdminUIRequiresLocalNonProductionStorage(t *testing.T) {
	cfg := Default()
	cfg.AdminUIListenAddress = "127.0.0.1:18083"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	testutil.RequireEqualf(
		t,
		err.Error(),
		"admin_ui_listen_address requires local non-production storage until HTTP admin UI authorization is implemented",
		"admin UI validation error",
	)

	cfg.EnableLocalNonProductionStorage = true
	cfg.LocalDataDir = t.TempDir()
	testutil.RequireNoErrorf(t, cfg.Validate(), "local admin UI config did not validate")
}

func TestAdminUIRejectsNonLoopbackAddress(t *testing.T) {
	tests := map[string]string{
		"all interfaces IPv4": "0.0.0.0:18083",
		"all interfaces IPv6": "[::]:18083",
		"external IP":         "10.0.0.1:18083",
	}
	for name, addr := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			cfg.AdminUIListenAddress = addr
			cfg.EnableLocalNonProductionStorage = true
			cfg.LocalDataDir = t.TempDir()
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error for non-loopback admin UI address")
			}
			testutil.RequireEqualf(
				t,
				err.Error(),
				"admin_ui_listen_address must bind to a loopback address until HTTP admin UI authorization is implemented",
				"admin UI loopback validation error",
			)
		})
	}

	loopbacks := map[string]string{
		"IPv4 loopback": "127.0.0.1:18083",
		"IPv6 loopback": "[::1]:18083",
		"localhost":     "localhost:18083",
	}
	for name, addr := range loopbacks {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			cfg.AdminUIListenAddress = addr
			cfg.EnableLocalNonProductionStorage = true
			cfg.LocalDataDir = t.TempDir()
			testutil.RequireNoErrorf(t, cfg.Validate(), "loopback address %s should be valid", addr)
		})
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
	testutil.RequireNoErrorf(t, cfg.Validate(), "local filesystem backend config did not validate")
}

func TestGRPCServerLimitsDefaultAndValidation(t *testing.T) {
	cfg := Default()
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.MaxConcurrentStreams, DefaultGRPCMaxConcurrentStreams, "max concurrent streams")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.MaxRecvMsgSizeBytes, DefaultGRPCMaxRecvMsgSizeBytes, "max recv message bytes")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.MaxSendMsgSizeBytes, DefaultGRPCMaxSendMsgSizeBytes, "max send message bytes")
	testutil.RequireNoErrorf(t, cfg.Validate(), "default grpc server limits")
}

func TestGRPCServerLimitsRejectUnsafeValues(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		wantErr string
	}{
		"zero streams": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.MaxConcurrentStreams = 0 },
			wantErr: "grpc_max_concurrent_streams must be positive",
		},
		"stream overflow": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.MaxConcurrentStreams = 1 << 32 },
			wantErr: "grpc_max_concurrent_streams must be no more than 4294967295",
		},
		"zero recv": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.MaxRecvMsgSizeBytes = 0 },
			wantErr: "grpc_max_recv_msg_size_bytes must be positive",
		},
		"negative recv": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.MaxRecvMsgSizeBytes = -1 },
			wantErr: "grpc_max_recv_msg_size_bytes must be positive",
		},
		"zero send": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.MaxSendMsgSizeBytes = 0 },
			wantErr: "grpc_max_send_msg_size_bytes must be positive",
		},
		"zero keepalive min": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.KeepaliveMinTime = 0 },
			wantErr: "grpc_keepalive_min_time must be positive",
		},
		"zero keepalive idle": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.KeepaliveMaxConnectionIdle = 0 },
			wantErr: "grpc_keepalive_max_connection_idle must be positive",
		},
		"zero keepalive age": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.KeepaliveMaxConnectionAge = 0 },
			wantErr: "grpc_keepalive_max_connection_age must be positive",
		},
		"zero keepalive age grace": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.KeepaliveMaxConnectionAgeGrace = 0 },
			wantErr: "grpc_keepalive_max_connection_age_grace must be positive",
		},
		"zero keepalive time": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.KeepaliveTime = 0 },
			wantErr: "grpc_keepalive_time must be positive",
		},
		"zero keepalive timeout": {
			mutate:  func(cfg *Config) { cfg.GRPCServerLimits.KeepaliveTimeout = 0 },
			wantErr: "grpc_keepalive_timeout must be positive",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			testutil.RequireEqualf(t, err.Error(), tt.wantErr, "validation error")
		})
	}
}

func TestGRPCTLSConfigValidatesWhenEnabled(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		wantErr string
	}{
		"missing cert": {
			mutate: func(cfg *Config) {
				cfg.TLSEnabled = true
				cfg.TLSKeyFile = "server-key.pem"
				cfg.TLSCACertFile = "ca.pem"
			},
			wantErr: "tls_cert_file is required when grpc TLS is enabled",
		},
		"missing key": {
			mutate: func(cfg *Config) {
				cfg.TLSEnabled = true
				cfg.TLSCertFile = "server.pem"
				cfg.TLSCACertFile = "ca.pem"
			},
			wantErr: "tls_key_file is required when grpc TLS is enabled",
		},
		"missing ca": {
			mutate: func(cfg *Config) {
				cfg.TLSEnabled = true
				cfg.TLSCertFile = "server.pem"
				cfg.TLSKeyFile = "server-key.pem"
			},
			wantErr: "tls_ca_cert_file is required when grpc TLS is enabled",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			testutil.RequireEqualf(t, err.Error(), tt.wantErr, "validation error")
		})
	}

	cfg := Default()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = "server.pem"
	cfg.TLSKeyFile = "server-key.pem"
	cfg.TLSCACertFile = "ca.pem"
	testutil.RequireNoErrorf(t, cfg.Validate(), "enabled grpc TLS config")
}

func TestRequireCertificateIdentityRequiresGRPCTLS(t *testing.T) {
	cfg := Default()
	cfg.RequireCertificateIdentity = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	testutil.RequireEqualf(t, err.Error(), "require_certificate_identity requires grpc TLS to be enabled", "validation error")
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
