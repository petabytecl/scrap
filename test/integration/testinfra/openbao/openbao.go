package openbao

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	baoapi "github.com/openbao/openbao/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/petabytecl/scrap/internal/encryption"
)

const (
	DefaultImage     = "openbao/openbao:2.5.4"
	DefaultMountPath = "transit"
	DefaultKeyName   = "scrap-documents"

	defaultPort    = "8200"
	defaultPortTCP = defaultPort + "/tcp"

	clientMaxRetries = 0
	clientTimeout    = 10 * time.Second
	startupTimeout   = 2 * time.Minute
)

type Container struct {
	testcontainers.Container

	Token     string
	MountPath string
	KeyName   string
}

func Run(ctx context.Context, img string, opts ...testcontainers.ContainerCustomizer) (*Container, error) {
	token, err := newRootToken()
	if err != nil {
		return nil, err
	}
	moduleOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts(defaultPortTCP),
		testcontainers.WithEnv(map[string]string{
			"BAO_DEV_LISTEN_ADDRESS": "0.0.0.0:" + defaultPort,
			"BAO_DEV_ROOT_TOKEN_ID":  token,
			"BAO_LOCAL_CONFIG":       `{"disable_mlock":true}`,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/v1/sys/health").
				WithPort(defaultPortTCP).
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
				WithStartupTimeout(startupTimeout),
		),
	}
	ctr, err := testcontainers.Run(ctx, img, append(moduleOpts, opts...)...)
	var c *Container
	if ctr != nil {
		c = &Container{
			Container: ctr,
			Token:     token,
			MountPath: DefaultMountPath,
			KeyName:   DefaultKeyName,
		}
	}
	if err != nil {
		return c, fmt.Errorf("run openbao: %w", err)
	}
	if err := c.bootstrapTransit(ctx); err != nil {
		return c, err
	}
	return c, nil
}

func (c *Container) HTTPHostAddress(ctx context.Context) (string, error) {
	endpoint, err := c.PortEndpoint(ctx, defaultPort, "http")
	if err != nil {
		return "", fmt.Errorf("openbao endpoint: %w", err)
	}
	return endpoint, nil
}

func (c *Container) Client(ctx context.Context) (*baoapi.Client, error) {
	address, err := c.HTTPHostAddress(ctx)
	if err != nil {
		return nil, err
	}
	cfg := baoapi.DefaultConfig()
	cfg.Address = address
	cfg.Timeout = clientTimeout
	cfg.MaxRetries = clientMaxRetries
	client, err := baoapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("openbao client: %w", err)
	}
	client.SetToken(c.Token)
	return client, nil
}

func (c *Container) TransitConfig(ctx context.Context) (encryption.OpenBaoConfig, error) {
	address, err := c.HTTPHostAddress(ctx)
	if err != nil {
		return encryption.OpenBaoConfig{}, err
	}
	return encryption.OpenBaoConfig{
		Address:   address,
		MountPath: c.MountPath,
		KeyName:   c.KeyName,
		Token:     c.Token,
	}, nil
}

func (c *Container) RotateTransitKey(ctx context.Context) error {
	client, err := c.Client(ctx)
	if err != nil {
		return err
	}
	if _, err := client.Logical().WriteWithContext(ctx, c.path(c.MountPath, "keys", c.KeyName, "rotate"), map[string]any{}); err != nil {
		return fmt.Errorf("openbao rotate transit key: %w", err)
	}
	return nil
}

func (c *Container) bootstrapTransit(ctx context.Context) error {
	client, err := c.Client(ctx)
	if err != nil {
		return err
	}
	if err := client.Sys().MountWithContext(ctx, c.MountPath, &baoapi.MountInput{Type: "transit"}); err != nil {
		return fmt.Errorf("openbao mount transit: %w", err)
	}
	_, err = client.Logical().WriteWithContext(ctx, c.path(c.MountPath, "keys", c.KeyName), map[string]any{
		"type":    "aes256-gcm96",
		"derived": true,
	})
	if err != nil {
		return fmt.Errorf("openbao create transit key: %w", err)
	}
	return nil
}

func (c *Container) path(parts ...string) string {
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, segment := range strings.Split(strings.Trim(part, "/"), "/") {
			if segment != "" {
				segments = append(segments, url.PathEscape(segment))
			}
		}
	}
	return strings.Join(segments, "/")
}

func newRootToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate openbao root token: %w", err)
	}
	return "scrap-test-" + hex.EncodeToString(raw[:]), nil
}
