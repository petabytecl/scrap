package scrapctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	baoapi "github.com/openbao/openbao/api"
)

type officialOpenBaoBootstrapClient struct {
	client *baoapi.Client
}

func newOfficialOpenBaoBootstrapClient(cfg openBaoClientConfig) (openBaoBootstrapClient, error) {
	clientConfig := baoapi.DefaultConfig()
	clientConfig.Address = cfg.Address
	clientConfig.Timeout = cfg.Timeout
	clientConfig.MaxRetries = openBaoClientMaxRetries
	if cfg.HTTPClient != nil {
		clientConfig.HttpClient = cfg.HTTPClient
	}
	client, err := baoapi.NewClient(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("openbao client: %w", err)
	}
	client.SetToken(strings.TrimSpace(cfg.Token))
	return &officialOpenBaoBootstrapClient{client: client}, nil
}

func (c *officialOpenBaoBootstrapClient) SetToken(token string) {
	c.client.SetToken(strings.TrimSpace(token))
}

func (c *officialOpenBaoBootstrapClient) InitStatus(ctx context.Context) (bool, error) {
	initialized, err := c.client.Sys().InitStatusWithContext(ctx)
	if err != nil {
		return false, fmt.Errorf("openbao init status: %w", err)
	}
	return initialized, nil
}

func (c *officialOpenBaoBootstrapClient) Init(ctx context.Context, req openBaoInitRequest) (openBaoInitResult, error) {
	resp, err := c.client.Sys().InitWithContext(ctx, &baoapi.InitRequest{
		SecretShares:    req.SecretShares,
		SecretThreshold: req.SecretThreshold,
	})
	if err != nil {
		return openBaoInitResult{}, fmt.Errorf("openbao init: %w", err)
	}
	return openBaoInitResult{
		Keys:      append([]string(nil), resp.Keys...),
		KeysB64:   append([]string(nil), resp.KeysB64...),
		RootToken: resp.RootToken,
	}, nil
}

func (c *officialOpenBaoBootstrapClient) SealStatus(ctx context.Context) (openBaoSealStatus, error) {
	status, err := c.client.Sys().SealStatusWithContext(ctx)
	if err != nil {
		return openBaoSealStatus{}, fmt.Errorf("openbao seal status: %w", err)
	}
	return openBaoSealStatus{
		Initialized: status.Initialized,
		Sealed:      status.Sealed,
		Progress:    status.Progress,
	}, nil
}

func (c *officialOpenBaoBootstrapClient) Unseal(ctx context.Context, key string) (openBaoSealStatus, error) {
	status, err := c.client.Sys().UnsealWithOptionsWithContext(ctx, &baoapi.UnsealOpts{Key: key})
	if err != nil {
		return openBaoSealStatus{}, fmt.Errorf("openbao unseal: %w", err)
	}
	return openBaoSealStatus{
		Initialized: status.Initialized,
		Sealed:      status.Sealed,
		Progress:    status.Progress,
	}, nil
}

func (c *officialOpenBaoBootstrapClient) ListMounts(ctx context.Context) (map[string]openBaoMount, error) {
	mounts, err := c.client.Sys().ListMountsWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("openbao list mounts: %w", err)
	}
	out := make(map[string]openBaoMount, len(mounts))
	for path, mount := range mounts {
		out[path] = openBaoMount{Type: mount.Type}
	}
	return out, nil
}

func (c *officialOpenBaoBootstrapClient) MountTransit(ctx context.Context, mountPath string) error {
	if err := c.client.Sys().MountWithContext(ctx, mountPath, &baoapi.MountInput{Type: "transit"}); err != nil {
		return fmt.Errorf("openbao mount transit: %w", err)
	}
	return nil
}

func (c *officialOpenBaoBootstrapClient) ReadTransitKey(ctx context.Context, mountPath, keyName string) (openBaoTransitKeyStatus, error) {
	secret, err := c.client.Logical().ReadWithContext(ctx, openBaoPath(mountPath, "keys", keyName))
	if err != nil {
		return openBaoTransitKeyStatus{}, fmt.Errorf("openbao read transit key: %w", err)
	}
	if secret == nil || secret.Data == nil {
		return openBaoTransitKeyStatus{}, errOpenBaoKeyMissing
	}
	keyType, typePresent := openBaoString(secret.Data, "type")
	derived, derivedPresent := openBaoBool(secret.Data, "derived")
	return openBaoTransitKeyStatus{
		Type:           keyType,
		TypePresent:    typePresent,
		Derived:        derived,
		DerivedPresent: derivedPresent,
		LatestVersion:  openBaoIntValue(secret.Data, "latest_version"),
	}, nil
}

func (c *officialOpenBaoBootstrapClient) CreateTransitKey(ctx context.Context, mountPath, keyName, keyType string, derived bool) error {
	_, err := c.client.Logical().WriteWithContext(ctx, openBaoPath(mountPath, "keys", keyName), map[string]any{
		"type":    keyType,
		"derived": derived,
	})
	if err != nil {
		return fmt.Errorf("openbao create transit key: %w", err)
	}
	return nil
}

func openBaoPath(parts ...string) string {
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

func openBaoString(data map[string]any, key string) (string, bool) {
	value, ok := data[key].(string)
	return value, ok
}

func openBaoBool(data map[string]any, key string) (bool, bool) {
	value, ok := data[key].(bool)
	return value, ok
}

func openBaoIntValue(data map[string]any, key string) int {
	switch value := data[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		out, _ := value.Int64()
		return int(out)
	case float64:
		return int(value)
	default:
		return 0
	}
}
