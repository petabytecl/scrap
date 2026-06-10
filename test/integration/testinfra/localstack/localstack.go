package localstack

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/petabytecl/scrap/internal/backend"
)

const (
	DefaultImage  = "localstack/localstack:4.4.0"
	DefaultRegion = "us-east-1"

	defaultPort        = "4566"
	defaultPortTCP     = defaultPort + "/tcp"
	startupTimeout     = 2 * time.Minute
	localStackServices = "s3"
)

type Container struct {
	testcontainers.Container
}

func Run(ctx context.Context, img string, opts ...testcontainers.ContainerCustomizer) (*Container, error) {
	moduleOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts(defaultPortTCP),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/_localstack/health").
				WithPort(defaultPortTCP).
				WithStartupTimeout(startupTimeout),
		),
		testcontainers.WithEnv(map[string]string{
			"AWS_DEFAULT_REGION": DefaultRegion,
			"SERVICES":           localStackServices,
		}),
	}
	ctr, err := testcontainers.Run(ctx, img, append(moduleOpts, opts...)...)
	var c *Container
	if ctr != nil {
		c = &Container{Container: ctr}
	}
	if err != nil {
		return c, fmt.Errorf("run localstack: %w", err)
	}
	return c, nil
}

func (c *Container) HTTPHostAddress(ctx context.Context) (string, error) {
	endpoint, err := c.PortEndpoint(ctx, defaultPort, "http")
	if err != nil {
		return "", fmt.Errorf("localstack endpoint: %w", err)
	}
	return endpoint, nil
}

func (c *Container) S3Config(ctx context.Context, bucket string) (backend.S3Config, error) {
	endpoint, err := c.HTTPHostAddress(ctx)
	if err != nil {
		return backend.S3Config{}, err
	}
	return backend.S3Config{
		Bucket:    bucket,
		Region:    DefaultRegion,
		Endpoint:  endpoint,
		PathStyle: true,
	}, nil
}
