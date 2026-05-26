package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

const defaultHealthAddress = "127.0.0.1:9090"

func runHealthcheck(args []string) error {
	var address string
	var service string
	var timeout time.Duration
	flags := flag.NewFlagSet("scrapd healthcheck", flag.ContinueOnError)
	flags.StringVar(&address, "address", defaultHealthAddress, "gRPC health endpoint address")
	flags.StringVar(&service, "service", "", "gRPC health service name; empty checks liveness")
	flags.DurationVar(&timeout, "timeout", time.Second, "health check timeout")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if address == "" {
		return errors.New("address is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
