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

type Config struct {
	PublicListenAddress             string
	AdminListenAddress              string
	LocalDataDir                    string
	EnableLocalNonProductionStorage bool
	EnableLocalFilesystemBackend    bool
	LocalBackendDataDir             string
	BackendUploadInterval           time.Duration
	OperationRunInterval            time.Duration
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

func validateListenAddress(field string, value string) error {
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
