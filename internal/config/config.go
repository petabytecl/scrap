package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

const (
	DefaultPublicListenAddress   = "127.0.0.1:18080"
	DefaultAdminListenAddress    = "127.0.0.1:18081"
	DefaultMetricsListenAddress  = "127.0.0.1:18082"
	DefaultBackendUploadInterval = 30 * time.Second
	DefaultOperationRunInterval  = 5 * time.Second
	DefaultLocalSealBlockAtBytes = 256 * 1024 * 1024
	DefaultProductionProfileID   = "scrap-prod-v1"
)

const (
	DefaultGRPCMaxConcurrentStreams           uint64 = 256
	DefaultGRPCMaxRecvMsgSizeBytes                   = 8 * 1024 * 1024
	DefaultGRPCMaxSendMsgSizeBytes                   = 8 * 1024 * 1024
	DefaultGRPCKeepaliveMinTime                      = 30 * time.Second
	DefaultGRPCKeepalivePermitWithoutStream          = true
	DefaultGRPCKeepaliveMaxConnectionIdle            = 5 * time.Minute
	DefaultGRPCKeepaliveMaxConnectionAge             = 30 * time.Minute
	DefaultGRPCKeepaliveMaxConnectionAgeGrace        = 5 * time.Second
	DefaultGRPCKeepaliveTime                         = 2 * time.Minute
	DefaultGRPCKeepaliveTimeout                      = 20 * time.Second
)

const (
	ProductionWriteACKReadinessGate       = "SCRAP_PRODUCTION_WRITE_ACK_READINESS"
	ReadinessGateReleaseProfile           = "SCRAP_RELEASE_PROFILE"
	ReadinessGateMetadataCompatibility    = "SCRAP_METADATA_COMPATIBILITY_BOUNDARY_V1"
	ReadinessGateRaftMetadataDurability   = "SCRAP_RAFT_METADATA_DURABILITY"
	ReadinessGatePeerByteDurability       = "SCRAP_PEER_BYTE_DURABILITY"
	ReadinessGateBackendRestoreWorkflow   = "SCRAP_BACKEND_RESTORE_WORKFLOW"
	ReadinessGateOpenBaoEnvelopeWorkflow  = "SCRAP_OPENBAO_ENVELOPE_WORKFLOW"
	ReadinessGateCapacityAdmission        = "SCRAP_CAPACITY_ADMISSION"
	ReadinessGateOperatorReadiness        = "SCRAP_OPERATOR_READINESS"
	ReadinessGateProductionImplementation = "SCRAP_PRODUCTION_WRITE_ACK_IMPLEMENTATION"
	ReadinessGateReleaseOwnerSignoff      = "SCRAP_RELEASE_OWNER_SIGNOFF"
	ReadinessGateDownstreamDeployment     = "SCRAP_DOWNSTREAM_DEPLOYMENT_APPROVAL"
)

var ErrProductionWriteACKReadinessGate = errors.New("production write ACK readiness gate blocked")

type Config struct {
	PublicListenAddress             string
	AdminListenAddress              string
	MetricsListenAddress            string
	AuthorizationPolicyPath         string
	TLSEnabled                      bool
	TLSCertFile                     string
	TLSKeyFile                      string
	TLSCACertFile                   string
	RequireCertificateIdentity      bool
	LocalDataDir                    string
	EnableLocalNonProductionStorage bool
	EnableLocalFilesystemBackend    bool
	LocalBackendDataDir             string
	BackendUploadInterval           time.Duration
	OperationRunInterval            time.Duration
	LocalSealBlockAtBytes           uint64
	GRPCServerLimits                GRPCServerLimits
	EnableProductionWriteACK        bool
	ProductionReadinessEvidence     ProductionReadinessEvidence
}

type GRPCServerLimits struct {
	MaxConcurrentStreams           uint64
	MaxRecvMsgSizeBytes            int
	MaxSendMsgSizeBytes            int
	KeepaliveMinTime               time.Duration
	KeepalivePermitWithoutStream   bool
	KeepaliveMaxConnectionIdle     time.Duration
	KeepaliveMaxConnectionAge      time.Duration
	KeepaliveMaxConnectionAgeGrace time.Duration
	KeepaliveTime                  time.Duration
	KeepaliveTimeout               time.Duration
}

type ProductionReadinessEvidence struct {
	TargetReleaseProfileID string

	MetadataCompatibilityBoundary  bool
	MetadataCompatibilityArtifact  string
	RaftMetadataDurability         bool
	RaftMetadataDurabilityArtifact string
	PeerByteDurability             bool
	PeerByteDurabilityArtifact     string
	BackendRestoreWorkflow         bool
	BackendRestoreArtifact         string
	OpenBaoEnvelopeWorkflow        bool
	OpenBaoEnvelopeArtifact        string
	CapacityAdmission              bool
	CapacityAdmissionArtifact      string
	OperatorReadiness              bool
	OperatorReadinessArtifact      string

	ProductionWriteACKImplementation bool
	ProductionImplementationArtifact string
	ReleaseOwnerSignoff              bool
	ReleaseOwnerSignoffArtifact      string
	DownstreamDeploymentApproval     bool
	DownstreamDeploymentArtifact     string
	DownstreamDeploymentDeferral     string
}

type ProductionReadinessMissing struct {
	ReadinessGate                string
	ReleaseArtifact              string
	BlockingIssues               []int
	Reason                       string
	DownstreamDeploymentDeferral string
}

type ProductionWriteACKGateError struct {
	Missing []ProductionReadinessMissing
}

func Default() Config {
	return Config{
		PublicListenAddress:   DefaultPublicListenAddress,
		AdminListenAddress:    DefaultAdminListenAddress,
		MetricsListenAddress:  DefaultMetricsListenAddress,
		BackendUploadInterval: DefaultBackendUploadInterval,
		OperationRunInterval:  DefaultOperationRunInterval,
		LocalSealBlockAtBytes: DefaultLocalSealBlockAtBytes,
		GRPCServerLimits: GRPCServerLimits{
			MaxConcurrentStreams:           DefaultGRPCMaxConcurrentStreams,
			MaxRecvMsgSizeBytes:            DefaultGRPCMaxRecvMsgSizeBytes,
			MaxSendMsgSizeBytes:            DefaultGRPCMaxSendMsgSizeBytes,
			KeepaliveMinTime:               DefaultGRPCKeepaliveMinTime,
			KeepalivePermitWithoutStream:   DefaultGRPCKeepalivePermitWithoutStream,
			KeepaliveMaxConnectionIdle:     DefaultGRPCKeepaliveMaxConnectionIdle,
			KeepaliveMaxConnectionAge:      DefaultGRPCKeepaliveMaxConnectionAge,
			KeepaliveMaxConnectionAgeGrace: DefaultGRPCKeepaliveMaxConnectionAgeGrace,
			KeepaliveTime:                  DefaultGRPCKeepaliveTime,
			KeepaliveTimeout:               DefaultGRPCKeepaliveTimeout,
		},
	}
}

func (c Config) Validate() error {
	if err := c.validateListenAddresses(); err != nil {
		return err
	}
	if err := c.validateGRPCTLS(); err != nil {
		return err
	}
	if err := c.validateProductionWriteACKGate(); err != nil {
		return err
	}
	if err := c.validateLocalStorage(); err != nil {
		return err
	}
	if c.BackendUploadInterval <= 0 {
		return errors.New("backend_upload_interval must be positive")
	}
	if c.OperationRunInterval <= 0 {
		return errors.New("operation_run_interval must be positive")
	}
	if c.LocalSealBlockAtBytes == 0 {
		return errors.New("local_seal_block_at_bytes must be positive")
	}
	if err := c.GRPCServerLimits.validate(); err != nil {
		return err
	}
	return c.validateLocalFilesystemBackend()
}

func (l GRPCServerLimits) validate() error {
	if err := validateGRPCMaxConcurrentStreams(l.MaxConcurrentStreams); err != nil {
		return err
	}
	return firstValidationError(
		validatePositiveInt("grpc_max_recv_msg_size_bytes", l.MaxRecvMsgSizeBytes),
		validatePositiveInt("grpc_max_send_msg_size_bytes", l.MaxSendMsgSizeBytes),
		validatePositiveDuration("grpc_keepalive_min_time", l.KeepaliveMinTime),
		validatePositiveDuration("grpc_keepalive_max_connection_idle", l.KeepaliveMaxConnectionIdle),
		validatePositiveDuration("grpc_keepalive_max_connection_age", l.KeepaliveMaxConnectionAge),
		validatePositiveDuration("grpc_keepalive_max_connection_age_grace", l.KeepaliveMaxConnectionAgeGrace),
		validatePositiveDuration("grpc_keepalive_time", l.KeepaliveTime),
		validatePositiveDuration("grpc_keepalive_timeout", l.KeepaliveTimeout),
	)
}

func validateGRPCMaxConcurrentStreams(value uint64) error {
	if value == 0 {
		return errors.New("grpc_max_concurrent_streams must be positive")
	}
	if value > math.MaxUint32 {
		return fmt.Errorf("grpc_max_concurrent_streams must be no more than %d", uint64(math.MaxUint32))
	}
	return nil
}

func validatePositiveInt(field string, value int) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

func validatePositiveDuration(field string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

func firstValidationError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (c Config) validateGRPCTLS() error {
	if c.RequireCertificateIdentity && !c.TLSEnabled {
		return errors.New("require_certificate_identity requires grpc TLS to be enabled")
	}
	if !c.TLSEnabled {
		return nil
	}
	return firstValidationError(
		validateRequiredGRPCTLSFile("tls_cert_file", c.TLSCertFile),
		validateRequiredGRPCTLSFile("tls_key_file", c.TLSKeyFile),
		validateRequiredGRPCTLSFile("tls_ca_cert_file", c.TLSCACertFile),
	)
}

func validateRequiredGRPCTLSFile(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required when grpc TLS is enabled", field)
	}
	return nil
}

func (c Config) validateListenAddresses() error {
	addresses := []listenAddress{
		{field: "public_listen_address", value: c.PublicListenAddress},
		{field: "admin_listen_address", value: c.AdminListenAddress},
		{field: "metrics_listen_address", value: c.MetricsListenAddress},
	}
	seen := make(map[string]string, len(addresses))
	for _, address := range addresses {
		if err := validateListenAddress(address.field, address.value); err != nil {
			return err
		}
		if otherField, exists := seen[address.value]; exists {
			return fmt.Errorf("%s and %s listen addresses must be distinct", otherField, address.field)
		}
		seen[address.value] = address.field
	}
	return nil
}

type listenAddress struct {
	field string
	value string
}

func (c Config) validateLocalStorage() error {
	if c.EnableLocalNonProductionStorage {
		if strings.TrimSpace(c.LocalDataDir) == "" {
			return errors.New("local_data_dir is required when local non-production storage is enabled")
		}
		return nil
	}
	if strings.TrimSpace(c.LocalDataDir) != "" {
		return errors.New("local_data_dir requires local non-production storage to be explicitly enabled")
	}
	return nil
}

func (c Config) validateLocalFilesystemBackend() error {
	if c.EnableLocalFilesystemBackend {
		if !c.EnableLocalNonProductionStorage {
			return errors.New("local filesystem backend requires local non-production storage to be enabled")
		}
		if strings.TrimSpace(c.LocalBackendDataDir) == "" {
			return errors.New("local_backend_data_dir is required when local filesystem backend is enabled")
		}
	} else if strings.TrimSpace(c.LocalBackendDataDir) != "" {
		return errors.New("local_backend_data_dir requires local filesystem backend to be explicitly enabled")
	}
	return nil
}

func (c Config) validateProductionWriteACKGate() error {
	if !c.EnableProductionWriteACK {
		return nil
	}
	if c.EnableLocalNonProductionStorage {
		return &ProductionWriteACKGateError{Missing: []ProductionReadinessMissing{{
			ReadinessGate: ProductionWriteACKReadinessGate,
			Reason:        "production write ACK mode cannot run with local non-production storage enabled",
		}}}
	}
	missing := c.ProductionReadinessEvidence.missingGates()
	if len(missing) > 0 {
		return &ProductionWriteACKGateError{Missing: missing}
	}
	return nil
}

func (e ProductionReadinessEvidence) missingGates() []ProductionReadinessMissing {
	gates := []productionReadinessRequirement{
		{
			ReadinessGate:    ReadinessGateMetadataCompatibility,
			ReleaseArtifact:  "metadata-compatibility-release-evidence",
			BlockingIssues:   []int{85},
			Ready:            e.MetadataCompatibilityBoundary,
			ProvidedArtifact: e.MetadataCompatibilityArtifact,
		},
		{
			ReadinessGate:    ReadinessGateRaftMetadataDurability,
			ReleaseArtifact:  "raft-metadata-durability-release-evidence",
			BlockingIssues:   []int{85},
			Ready:            e.RaftMetadataDurability,
			ProvidedArtifact: e.RaftMetadataDurabilityArtifact,
		},
		{
			ReadinessGate:    ReadinessGatePeerByteDurability,
			ReleaseArtifact:  "peer-byte-durability-release-evidence",
			BlockingIssues:   []int{85},
			Ready:            e.PeerByteDurability,
			ProvidedArtifact: e.PeerByteDurabilityArtifact,
		},
		{
			ReadinessGate:    ReadinessGateBackendRestoreWorkflow,
			ReleaseArtifact:  "backend-restore-workflow-release-evidence",
			BlockingIssues:   []int{88},
			Ready:            e.BackendRestoreWorkflow,
			ProvidedArtifact: e.BackendRestoreArtifact,
		},
		{
			ReadinessGate:    ReadinessGateOpenBaoEnvelopeWorkflow,
			ReleaseArtifact:  "openbao-envelope-release-evidence",
			BlockingIssues:   []int{86},
			Ready:            e.OpenBaoEnvelopeWorkflow,
			ProvidedArtifact: e.OpenBaoEnvelopeArtifact,
		},
		{
			ReadinessGate:    ReadinessGateCapacityAdmission,
			ReleaseArtifact:  "capacity-admission-release-evidence",
			BlockingIssues:   []int{48, 87},
			Ready:            e.CapacityAdmission,
			ProvidedArtifact: e.CapacityAdmissionArtifact,
		},
		{
			ReadinessGate:    ReadinessGateOperatorReadiness,
			ReleaseArtifact:  "operator-readiness-release-evidence",
			BlockingIssues:   []int{88},
			Ready:            e.OperatorReadiness,
			ProvidedArtifact: e.OperatorReadinessArtifact,
		},
		{
			ReadinessGate:    ReadinessGateProductionImplementation,
			ReleaseArtifact:  "production-write-ack-implementation-evidence",
			Ready:            e.ProductionWriteACKImplementation,
			ProvidedArtifact: e.ProductionImplementationArtifact,
		},
		{
			ReadinessGate:    ReadinessGateReleaseOwnerSignoff,
			ReleaseArtifact:  "release-owner-signoff",
			BlockingIssues:   []int{48, 86, 88},
			Ready:            e.ReleaseOwnerSignoff,
			ProvidedArtifact: e.ReleaseOwnerSignoffArtifact,
		},
		{
			ReadinessGate:                ReadinessGateDownstreamDeployment,
			ReleaseArtifact:              "downstream-deployment-approval",
			Ready:                        e.DownstreamDeploymentApproval,
			ProvidedArtifact:             e.DownstreamDeploymentArtifact,
			DownstreamDeploymentDeferral: e.DownstreamDeploymentDeferral,
		},
	}
	missing := make([]ProductionReadinessMissing, 0, len(gates)+1)
	profileID := strings.TrimSpace(e.TargetReleaseProfileID)
	if profileID == "" {
		missing = append(missing, ProductionReadinessMissing{
			ReadinessGate:   ReadinessGateReleaseProfile,
			ReleaseArtifact: "target-release-profile",
			BlockingIssues:  []int{48},
			Reason:          "target release profile is required",
		})
	} else if profileID != DefaultProductionProfileID {
		missing = append(missing, ProductionReadinessMissing{
			ReadinessGate:   ReadinessGateReleaseProfile,
			ReleaseArtifact: "target-release-profile",
			BlockingIssues:  []int{48},
			Reason:          fmt.Sprintf("unsupported target release profile %q; expected %q", profileID, DefaultProductionProfileID),
		})
	}
	for _, gate := range gates {
		if gate.Ready && strings.TrimSpace(gate.ProvidedArtifact) != "" {
			continue
		}
		reason := "readiness evidence is not satisfied"
		if gate.Ready {
			reason = "release artifact is required"
		}
		missing = append(missing, ProductionReadinessMissing{
			ReadinessGate:                gate.ReadinessGate,
			ReleaseArtifact:              gate.ReleaseArtifact,
			BlockingIssues:               append([]int(nil), gate.BlockingIssues...),
			Reason:                       reason,
			DownstreamDeploymentDeferral: gate.DownstreamDeploymentDeferral,
		})
	}
	return missing
}

func (e *ProductionWriteACKGateError) Error() string {
	if e == nil {
		return ErrProductionWriteACKReadinessGate.Error()
	}
	parts := make([]string, 0, len(e.Missing))
	for _, missing := range e.Missing {
		detail := missing.ReadinessGate
		if missing.ReleaseArtifact != "" {
			detail += " artifact=" + missing.ReleaseArtifact
		}
		if len(missing.BlockingIssues) > 0 {
			blockers := make([]string, 0, len(missing.BlockingIssues))
			for _, issue := range missing.BlockingIssues {
				blockers = append(blockers, fmt.Sprintf("#%d", issue))
			}
			detail += " blocking_issues=" + strings.Join(blockers, ",")
		}
		if missing.DownstreamDeploymentDeferral != "" {
			detail += " downstream_deployment_deferral=" + missing.DownstreamDeploymentDeferral
		}
		if missing.Reason != "" {
			detail += " reason=" + missing.Reason
		}
		parts = append(parts, detail)
	}
	return fmt.Sprintf(
		"%v: %s missing readiness evidence: %s",
		ErrProductionWriteACKReadinessGate,
		ProductionWriteACKReadinessGate,
		strings.Join(parts, "; "),
	)
}

func (e *ProductionWriteACKGateError) Is(target error) bool {
	return target == ErrProductionWriteACKReadinessGate
}

type productionReadinessRequirement struct {
	ReadinessGate                string
	ReleaseArtifact              string
	BlockingIssues               []int
	Ready                        bool
	ProvidedArtifact             string
	DownstreamDeploymentDeferral string
}

func validateListenAddress(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("%s must be host:port: %w", field, err)
	}
	if port == "" {
		return fmt.Errorf("%s must include a port", field)
	}
	if host == "" {
		return fmt.Errorf("%s must include a host", field)
	}
	return nil
}
