package encryption

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	baoapi "github.com/openbao/openbao/api"
)

const (
	defaultClientTimeout = 10 * time.Second
	defaultClientRetries = 0
	minimumKeyVersion    = 1
)

type OpenBaoConfig struct {
	Address    string
	MountPath  string
	KeyName    string
	Token      string
	HTTPClient *http.Client
}

type OpenBaoTransit struct {
	client    *baoapi.Client
	mountPath string
	keyName   string
}

type transitKeyMetadata struct {
	latestVersion            int
	minimumDecryptionVersion int
	softDeleted              bool
	supportsEncryption       bool
	supportsDecryption       bool
}

func NewOpenBaoTransit(cfg OpenBaoConfig) (*OpenBaoTransit, error) {
	baseURL, err := validateOpenBaoAddress(cfg.Address)
	if err != nil {
		return nil, err
	}
	mountPath, err := cleanTransitPath("mount path", cfg.MountPath)
	if err != nil {
		return nil, err
	}
	keyName, err := cleanTransitPath("key name", cfg.KeyName)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, fmt.Errorf("openbao transit config: token is required: %w", ErrInvalidConfig)
	}
	client, err := newOpenBaoClient(baseURL.String(), token, cfg.HTTPClient)
	if err != nil {
		return nil, err
	}
	return &OpenBaoTransit{
		client:    client,
		mountPath: mountPath,
		keyName:   keyName,
	}, nil
}

func (*OpenBaoTransit) ProductionCapable() bool {
	return true
}

func (t *OpenBaoTransit) GenerateDataKey(ctx context.Context, req GenerateDataKeyRequest) (DataKey, error) {
	keyBytes, err := requestedKeyBytes(req.Bits)
	if err != nil {
		return DataKey{}, err
	}
	body := map[string]any{"bits": keyBytes * bitsPerByte}
	if len(req.Context) > 0 {
		body["context"] = base64.StdEncoding.EncodeToString(req.Context)
	}
	data, err := t.write(ctx, t.path("datakey", "plaintext", t.keyName), body)
	if err != nil {
		return DataKey{}, err
	}
	plaintextValue, err := transitString(data, "datakey plaintext", "plaintext")
	if err != nil {
		return DataKey{}, err
	}
	plaintext, err := decodeTransitBase64("datakey plaintext", plaintextValue)
	if err != nil {
		return DataKey{}, err
	}
	ciphertext, err := transitString(data, "datakey ciphertext", "ciphertext")
	if err != nil {
		return DataKey{}, err
	}
	if ciphertext == "" {
		return DataKey{}, fmt.Errorf("openbao transit datakey response missing ciphertext: %w", ErrUnavailable)
	}
	return DataKey{
		Plaintext:  plaintext,
		WrappedKey: ciphertext,
		Version:    versionFromWrappedKey(ciphertext),
	}, nil
}

func (t *OpenBaoTransit) UnwrapDataKey(ctx context.Context, req UnwrapDataKeyRequest) (UnwrappedDataKey, error) {
	body := map[string]any{"ciphertext": strings.TrimSpace(req.WrappedKey)}
	if len(req.Context) > 0 {
		body["context"] = base64.StdEncoding.EncodeToString(req.Context)
	}
	data, err := t.write(ctx, t.path("decrypt", t.keyName), body)
	if err != nil {
		return UnwrappedDataKey{}, err
	}
	plaintextValue, err := transitString(data, "decrypt plaintext", "plaintext")
	if err != nil {
		return UnwrappedDataKey{}, err
	}
	plaintext, err := decodeTransitBase64("decrypt plaintext", plaintextValue)
	if err != nil {
		return UnwrappedDataKey{}, err
	}
	return UnwrappedDataKey{
		Plaintext: plaintext,
		Version:   versionFromWrappedKey(req.WrappedKey),
	}, nil
}

func (t *OpenBaoTransit) RewrapDataKey(ctx context.Context, req RewrapDataKeyRequest) (RewrappedKey, error) {
	priorKey := strings.TrimSpace(req.WrappedKey)
	body := map[string]any{"ciphertext": priorKey}
	if len(req.Context) > 0 {
		body["context"] = base64.StdEncoding.EncodeToString(req.Context)
	}
	if req.KeyVersion > 0 {
		body["key_version"] = req.KeyVersion
	}
	data, err := t.write(ctx, t.path("rewrap", t.keyName), body)
	if err != nil {
		return RewrappedKey{}, err
	}
	ciphertext, err := transitString(data, "rewrap ciphertext", "ciphertext")
	if err != nil {
		return RewrappedKey{}, err
	}
	if ciphertext == "" {
		return RewrappedKey{}, fmt.Errorf("openbao transit rewrap response missing ciphertext: %w", ErrUnavailable)
	}
	// Transit rewrap re-encrypts with a fresh nonce, so the ciphertext string
	// differs on every call even when the wrapping key version is unchanged.
	// Deriving Changed from string inequality would report every no-op rewrap as
	// changed, defeating the shard's idempotency short-circuit and forcing a full
	// Block re-upload each time. The wrapping key version is the meaningful
	// signal, matching the fake's semantics.
	newVersion := versionFromWrappedKey(ciphertext)
	if newVersion < minimumKeyVersion {
		// A non-empty ciphertext whose version cannot be parsed must fail closed:
		// otherwise it derives Changed=false and the shard reports a successful
		// no-op rewrap for a malformed provider response instead of erroring.
		return RewrappedKey{}, fmt.Errorf("openbao transit rewrap returned unparsable key version: %w", ErrUnavailable)
	}
	return RewrappedKey{
		WrappedKey: ciphertext,
		Version:    newVersion,
		Changed:    newVersion > versionFromWrappedKey(priorKey),
	}, nil
}

func (t *OpenBaoTransit) Readiness(ctx context.Context) (Readiness, error) {
	data, err := t.read(ctx, t.path("keys", t.keyName))
	if err != nil {
		return Readiness{}, err
	}
	metadata, err := transitKeyMetadataFromData(data)
	if err != nil {
		return Readiness{}, err
	}
	if metadata.latestVersion < minimumKeyVersion {
		return Readiness{}, fmt.Errorf("openbao transit key metadata missing latest version: %w", ErrMissingKey)
	}
	if metadata.softDeleted || !metadata.supportsEncryption || !metadata.supportsDecryption {
		return Readiness{}, fmt.Errorf("openbao transit key is not usable for envelope encryption: %w", ErrUnavailable)
	}
	return Readiness{
		Ready:                    true,
		LatestVersion:            metadata.latestVersion,
		MinimumDecryptionVersion: metadata.minimumDecryptionVersion,
	}, nil
}

func transitKeyMetadataFromData(data map[string]any) (transitKeyMetadata, error) {
	latestVersion, err := transitInt(data, "latest_version")
	if err != nil {
		return transitKeyMetadata{}, err
	}
	minimumDecryptionVersion, err := optionalTransitInt(data, "min_decryption_version")
	if err != nil {
		return transitKeyMetadata{}, err
	}
	softDeleted, err := optionalTransitBool(data, "soft_deleted")
	if err != nil {
		return transitKeyMetadata{}, err
	}
	supportsEncryption, err := optionalTransitBool(data, "supports_encryption")
	if err != nil {
		return transitKeyMetadata{}, err
	}
	supportsDecryption, err := optionalTransitBool(data, "supports_decryption")
	if err != nil {
		return transitKeyMetadata{}, err
	}
	return transitKeyMetadata{
		latestVersion:            latestVersion,
		minimumDecryptionVersion: minimumDecryptionVersion,
		softDeleted:              softDeleted,
		supportsEncryption:       supportsEncryption,
		supportsDecryption:       supportsDecryption,
	}, nil
}

// transitInsecureEnvVars are TLS-verification kill switches the OpenBao/Vault
// SDK honors through DefaultConfig's environment reading. SCRAP exchanges
// plaintext data keys over this connection, so it refuses to start when any of
// them disables certificate verification rather than silently accepting a
// MITM-able transport.
var transitInsecureEnvVars = []string{"VAULT_SKIP_VERIFY", "BAO_SKIP_VERIFY"}

func rejectInsecureTransitEnv() error {
	for _, key := range transitInsecureEnvVars {
		raw, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		skip, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("openbao transit config: %s is not a valid boolean: %w", key, ErrInvalidConfig)
		}
		if skip {
			return fmt.Errorf("openbao transit config: %s disables TLS verification and is not permitted: %w", key, ErrInvalidConfig)
		}
	}
	return nil
}

func newOpenBaoClient(address, token string, httpClient *http.Client) (*baoapi.Client, error) {
	if err := rejectInsecureTransitEnv(); err != nil {
		return nil, err
	}
	cfg := baoapi.DefaultConfig()
	if cfg.Error != nil {
		return nil, fmt.Errorf("openbao transit client config: %w", ErrInvalidConfig)
	}
	cfg.Address = address
	cfg.Timeout = defaultClientTimeout
	cfg.MaxRetries = defaultClientRetries
	if httpClient != nil {
		cfg.HttpClient = httpClient
	}
	client, err := baoapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("openbao transit client config: %w", ErrInvalidConfig)
	}
	client.SetToken(token)
	return client, nil
}

func (t *OpenBaoTransit) read(ctx context.Context, apiPath string) (map[string]any, error) {
	secret, err := t.client.Logical().ReadWithContext(ctx, apiPath)
	if err != nil {
		return nil, classifyOpenBaoError(err)
	}
	return transitData(secret, ErrMissingKey)
}

func (t *OpenBaoTransit) write(ctx context.Context, apiPath string, body map[string]any) (map[string]any, error) {
	secret, err := t.client.Logical().WriteWithContext(ctx, apiPath, body)
	if err != nil {
		return nil, classifyOpenBaoError(err)
	}
	return transitData(secret, ErrUnavailable)
}

func transitData(secret *baoapi.Secret, missing error) (map[string]any, error) {
	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("openbao transit response missing data: %w", missing)
	}
	return secret.Data, nil
}

func transitString(data map[string]any, label, key string) (string, error) {
	value, ok := data[key]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("openbao transit %s decode failed: %w", label, ErrUnavailable)
	}
	return text, nil
}

func transitInt(data map[string]any, key string) (int, error) {
	value, ok := data[key]
	if !ok {
		return 0, fmt.Errorf("openbao transit key metadata missing latest version: %w", ErrMissingKey)
	}
	out, err := openBaoInt(value)
	if err != nil {
		return 0, fmt.Errorf("openbao transit %s decode failed: %w", key, ErrUnavailable)
	}
	return out, nil
}

func optionalTransitInt(data map[string]any, key string) (int, error) {
	value, ok := data[key]
	if !ok {
		return 0, nil
	}
	out, err := openBaoInt(value)
	if err != nil {
		return 0, fmt.Errorf("openbao transit %s decode failed: %w", key, ErrUnavailable)
	}
	return out, nil
}

func optionalTransitBool(data map[string]any, key string) (bool, error) {
	value, ok := data[key]
	if !ok {
		return false, nil
	}
	out, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("openbao transit %s decode failed: %w", key, ErrUnavailable)
	}
	return out, nil
}

func openBaoInt(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		return strconv.Atoi(strconv.FormatInt(typed, 10))
	case json.Number:
		return strconv.Atoi(typed.String())
	default:
		return 0, fmt.Errorf("unexpected %T", value)
	}
}

func (t *OpenBaoTransit) path(parts ...string) string {
	segments := splitTransitPath(t.mountPath)
	for _, part := range parts {
		segments = append(segments, splitTransitPath(part)...)
	}
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func validateOpenBaoAddress(raw string) (url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return url.URL{}, fmt.Errorf("openbao transit config: address is invalid: %w", ErrInvalidConfig)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return url.URL{}, fmt.Errorf("openbao transit config: address scheme is invalid: %w", ErrInvalidConfig)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return *parsed, nil
}

func cleanTransitPath(label, raw string) (string, error) {
	cleaned := strings.Trim(strings.TrimSpace(raw), "/")
	if cleaned == "" {
		return "", fmt.Errorf("openbao transit config: %s is required: %w", label, ErrInvalidConfig)
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", fmt.Errorf("openbao transit config: %s is invalid: %w", label, ErrInvalidConfig)
		}
	}
	return cleaned, nil
}

func splitTransitPath(raw string) []string {
	var segments []string
	for _, segment := range strings.Split(raw, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func decodeTransitBase64(label, value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("openbao transit %s missing: %w", label, ErrUnavailable)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("openbao transit %s decode failed: %w", label, ErrUnavailable)
	}
	return decoded, nil
}

func classifyOpenBaoError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("openbao transit request canceled: %w", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("openbao transit request deadline exceeded: %w", context.DeadlineExceeded)
	}
	var responseErr *baoapi.ResponseError
	if !errors.As(err, &responseErr) {
		return fmt.Errorf("openbao transit request failed: %w", ErrUnavailable)
	}
	return classifyOpenBaoFailure(responseErr.StatusCode, strings.ToLower(strings.Join(responseErr.Errors, "\n")))
}

func classifyOpenBaoFailure(statusCode int, bodyText string) error {
	var cause error
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		cause = ErrAuthDenied
	case statusCode == http.StatusNotFound:
		cause = ErrMissingKey
	case statusCode == http.StatusBadRequest:
		// The minimum-version signal is a body-text heuristic, so only trust it
		// on a 400. A 5xx whose body happens to contain "minimum"+"version" is a
		// transient provider failure, not the terminal min-version condition.
		if isMinimumVersionFailure(bodyText) {
			cause = ErrMinimumVersion
		} else {
			cause = ErrInvalidRequest
		}
	case statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError:
		cause = ErrUnavailable
	default:
		cause = ErrUnavailable
	}
	return fmt.Errorf("openbao transit request failed with provider status %d: %w", statusCode, cause)
}

func isMinimumVersionFailure(bodyText string) bool {
	return strings.Contains(bodyText, "minimum") && strings.Contains(bodyText, "version") ||
		strings.Contains(bodyText, "disallowed by policy") && strings.Contains(bodyText, "too old")
}
