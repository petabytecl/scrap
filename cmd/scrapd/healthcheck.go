package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

const defaultHealthcheckAddress = "127.0.0.1:18081"

func runHealthcheck(args []string, stderr io.Writer) error {
	var address string
	var service string
	var timeout time.Duration
	flags := flag.NewFlagSet("scrapd healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&address, "address", defaultHealthcheckAddress, "local gRPC health endpoint address")
	flags.StringVar(&service, "service", "", "gRPC health service name; empty checks liveness")
	flags.DurationVar(&timeout, "timeout", time.Second, "health check timeout")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse healthcheck flags: %w", err)
	}
	if address == "" {
		return errors.New("healthcheck address is required")
	}
	if timeout <= 0 {
		return errors.New("healthcheck timeout must be positive")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("create health client: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	resp, err := healthv1.NewHealthClient(conn).Check(ctx, &healthv1.HealthCheckRequest{
		Service: service,
	})
	if err != nil {
		return fmt.Errorf("check health service %q: %w", service, err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_SERVING {
		return fmt.Errorf("health service %q status %s", service, resp.GetStatus())
	}
	return nil
}
