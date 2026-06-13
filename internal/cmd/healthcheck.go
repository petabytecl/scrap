package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/petabytecl/scrap/internal/security"
)

const defaultHealthAddress = "127.0.0.1:9090"

func RunHealthcheck(args []string) error {
	var address string
	var service string
	var timeout time.Duration
	tlsFiles := security.ClientTLSFiles{
		CertPath:   os.Getenv("SCRAP_TLS_SCRAPCTL_CERT"),
		KeyPath:    os.Getenv("SCRAP_TLS_SCRAPCTL_KEY"),
		RootCAPath: os.Getenv("SCRAP_TLS_SCRAPCTL_CLIENT_CA"),
		ServerName: os.Getenv("SCRAP_TLS_SCRAPCTL_SERVER_NAME"),
	}
	flags := flag.NewFlagSet("scrapd healthcheck", flag.ContinueOnError)
	flags.StringVar(&address, "address", defaultHealthAddress, "gRPC health endpoint address")
	flags.StringVar(&service, "service", "", "gRPC health service name; empty checks liveness")
	flags.DurationVar(&timeout, "timeout", time.Second, "health check timeout")
	flags.StringVar(&tlsFiles.CertPath, "tls-cert", tlsFiles.CertPath, "mTLS client certificate path")
	flags.StringVar(&tlsFiles.KeyPath, "tls-key", tlsFiles.KeyPath, "mTLS client key path")
	flags.StringVar(&tlsFiles.RootCAPath, "tls-ca", tlsFiles.RootCAPath, "server CA bundle path")
	flags.StringVar(&tlsFiles.ServerName, "tls-server-name", tlsFiles.ServerName, "expected server certificate name")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if address == "" {
		return errors.New("address is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	creds, err := healthcheckCredentials(tlsFiles)
	if err != nil {
		return err
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return fmt.Errorf("dial %s: %w", address, err)
	}
	defer func() { _ = conn.Close() }()

	resp, err := healthv1.NewHealthClient(conn).Check(ctx, &healthv1.HealthCheckRequest{
		Service: service,
	})
	if err != nil {
		return fmt.Errorf("check %q: %w", service, err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_SERVING {
		return fmt.Errorf("service %q status: %s", service, resp.GetStatus())
	}
	return nil
}

func healthcheckCredentials(files security.ClientTLSFiles) (credentials.TransportCredentials, error) {
	if !healthcheckTLSRequired(files) {
		return insecure.NewCredentials(), nil
	}
	tlsConfig, err := security.BuildMTLSClientConfig("SCRAP_TLS_SCRAPCTL", files)
	if err != nil {
		return nil, fmt.Errorf("healthcheck TLS: %w", err)
	}
	return credentials.NewTLS(tlsConfig), nil
}

func healthcheckTLSRequired(files security.ClientTLSFiles) bool {
	return healthcheckTLSFilesPresent(files) || strings.TrimSpace(os.Getenv("SCRAP_SECURITY_MODE")) == string(security.ModeProduction)
}

func healthcheckTLSFilesPresent(files security.ClientTLSFiles) bool {
	return strings.TrimSpace(files.CertPath) != "" ||
		strings.TrimSpace(files.KeyPath) != "" ||
		strings.TrimSpace(files.RootCAPath) != "" ||
		strings.TrimSpace(files.ServerName) != ""
}
