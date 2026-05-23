package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	DefaultPublicListenAddress   = "127.0.0.1:18080"
	DefaultAdminListenAddress    = "127.0.0.1:18081"
	DefaultBackendUploadInterval = 30 * time.Second
	DefaultOperationRunInterval  = 5 * time.Second
)

const (
	ProductionWriteACKReadinessGate       = "SCRAP_PRODUCTION_WRITE_ACK_READINESS"
	ReadinessGateMetadataCompatibility    = "SCRAP_METADATA_COMPATIBILITY_BOUNDARY_V1"
	ReadinessGateRaftMetadataDurability   = "SCRAP_RAFT_METADATA_DURABILITY"
	ReadinessGatePeerByteDurability       = "SCRAP_PEER_BYTE_DURABILITY"
	ReadinessGateBackendRestoreWorkflow   = "SCRAP_BACKEND_RESTORE_WORKFLOW"
	ReadinessGateOpenBaoEnvelopeWorkflow  = "SCRAP_OPENBAO_ENVELOPE_WORKFLOW"
	ReadinessGateCapacityAdmission        = "SCRAP_CAPACITY_ADMISSION"
	ReadinessGateOperatorReadiness        = "SCRAP_OPERATOR_READINESS"
	ReadinessGateProductionImplementation = "SCRAP_PRODUCTION_WRITE_ACK_IMPLEMENTATION"
)

var ErrProductionWriteACKReadinessGate = errors.New("production write ACK readiness gate blocked")

type Config struct {
	PublicListenAddress             string
	AdminListenAddress              string
	AuthorizationPolicyPath         string
	LocalDataDir                    string
	EnableLocalNonProductionStorage bool
	EnableLocalFilesystemBackend    bool
	LocalBackendDataDir             string
	BackendUploadInterval           time.Duration
	OperationRunInterval            time.Duration
	EnableProductionWriteACK        bool
	ProductionReadinessEvidence     ProductionReadinessEvidence
}

type ProductionReadinessEvidence struct {
	MetadataCompatibilityBoundary bool
	RaftMetadataDurability        bool
	PeerByteDurability            bool
	BackendRestoreWorkflow        bool
	OpenBaoEnvelopeWorkflow       bool
	CapacityAdmission             bool
	OperatorReadiness             bool
}

func Default() Config {
	return Config{
		PublicListenAddress:   DefaultPublicListenAddress,
		AdminListenAddress:    DefaultAdminListenAddress,
		BackendUploadInterval: DefaultBackendUploadInterval,
		OperationRunInterval:  DefaultOperationRunInterval,
	}
}

func (c Config) Validate() error {
	if err := validateListenAddress("public_listen_address", c.PublicListenAddress); err != nil {
		return err
	}
	if err := validateListenAddress("admin_listen_address", c.AdminListenAddress); err != nil {
		return err
	}
	if c.PublicListenAddress == c.AdminListenAddress {
		return errors.New("public and admin listen addresses must be distinct")
	}
	if err := c.validateProductionWriteACKGate(); err != nil {
		return err
	}
	if c.EnableLocalNonProductionStorage {
		if strings.TrimSpace(c.LocalDataDir) == "" {
			return errors.New("local_data_dir is required when local non-production storage is enabled")
		}
	} else if strings.TrimSpace(c.LocalDataDir) != "" {
		return errors.New("local_data_dir requires local non-production storage to be explicitly enabled")
	}
	if c.BackendUploadInterval <= 0 {
		return errors.New("backend_upload_interval must be positive")
	}
	if c.OperationRunInterval <= 0 {
		return errors.New("operation_run_interval must be positive")
	}
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
		return fmt.Errorf(
			"%w: %s cannot run with local non-production storage enabled",
			ErrProductionWriteACKReadinessGate,
			ProductionWriteACKReadinessGate,
		)
	}
	missing := c.ProductionReadinessEvidence.missingGates()
	if len(missing) > 0 {
		return fmt.Errorf(
			"%w: %s missing readiness evidence for %s",
			ErrProductionWriteACKReadinessGate,
			ProductionWriteACKReadinessGate,
			strings.Join(missing, ", "),
		)
	}
	return fmt.Errorf(
		"%w: %s missing readiness evidence for %s; production write ACK mode is not implemented yet",
		ErrProductionWriteACKReadinessGate,
		ProductionWriteACKReadinessGate,
		ReadinessGateProductionImplementation,
	)
}

func (e ProductionReadinessEvidence) missingGates() []string {
	gates := []struct {
		name  string
		ready bool
	}{
		{name: ReadinessGateMetadataCompatibility, ready: e.MetadataCompatibilityBoundary},
		{name: ReadinessGateRaftMetadataDurability, ready: e.RaftMetadataDurability},
		{name: ReadinessGatePeerByteDurability, ready: e.PeerByteDurability},
		{name: ReadinessGateBackendRestoreWorkflow, ready: e.BackendRestoreWorkflow},
		{name: ReadinessGateOpenBaoEnvelopeWorkflow, ready: e.OpenBaoEnvelopeWorkflow},
		{name: ReadinessGateCapacityAdmission, ready: e.CapacityAdmission},
		{name: ReadinessGateOperatorReadiness, ready: e.OperatorReadiness},
	}
	missing := make([]string, 0, len(gates))
	for _, gate := range gates {
		if !gate.ready {
			missing = append(missing, gate.name)
		}
	}
	return missing
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
