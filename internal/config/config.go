package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	DefaultPublicListenAddress = "127.0.0.1:18080"
	DefaultAdminListenAddress  = "127.0.0.1:18081"
)

type Config struct {
	PublicListenAddress string
	AdminListenAddress  string
}

func Default() Config {
	return Config{
		PublicListenAddress: DefaultPublicListenAddress,
		AdminListenAddress:  DefaultAdminListenAddress,
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
