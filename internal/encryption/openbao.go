package encryption

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	openBaoAPIPrefix     = "v1"
	openBaoTokenHeader   = "X-Vault-Token" //nolint:gosec // Header name only; token values come from runtime config.
	defaultClientTimeout = 10 * time.Second
	maxErrorBodyBytes    = 4096
)

type OpenBaoConfig struct {
	Address    string
	MountPath  string
	KeyName    string
	Token      string
	HTTPClient *http.Client
}

type OpenBaoTransit struct {
	baseURL    url.URL
	mountPath  string
	keyName    string
	token      string
	httpClient *http.Client
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
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultClientTimeout}
	}
	return &OpenBaoTransit{
		baseURL:    baseURL,
		mountPath:  mountPath,
		keyName:    keyName,
		token:      token,
		httpClient: httpClient,
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
	var out struct {
		Plaintext  string `json:"plaintext"`
		Ciphertext string `json:"ciphertext"`
	}
	if err := t.write(ctx, http.MethodPost, t.path("datakey", "plaintext", t.keyName), body, &out); err != nil {
		return DataKey{}, err
	}
	plaintext, err := decodeTransitBase64("datakey plaintext", out.Plaintext)
	if err != nil {
		return DataKey{}, err
	}
	if out.Ciphertext == "" {
		return DataKey{}, fmt.Errorf("openbao transit datakey response missing ciphertext: %w", ErrUnavailable)
	}
	return DataKey{
		Plaintext:  plaintext,
		WrappedKey: out.Ciphertext,
		Version:    versionFromWrappedKey(out.Ciphertext),
	}, nil
}

func (t *OpenBaoTransit) UnwrapDataKey(ctx context.Context, req UnwrapDataKeyRequest) (UnwrappedDataKey, error) {
	body := map[string]any{"ciphertext": strings.TrimSpace(req.WrappedKey)}
	if len(req.Context) > 0 {
		body["context"] = base64.StdEncoding.EncodeToString(req.Context)
	}
	var out struct {
		Plaintext string `json:"plaintext"`
	}
	if err := t.write(ctx, http.MethodPost, t.path("decrypt", t.keyName), body, &out); err != nil {
		return UnwrappedDataKey{}, err
	}
	plaintext, err := decodeTransitBase64("decrypt plaintext", out.Plaintext)
	if err != nil {
		return UnwrappedDataKey{}, err
	}
	return UnwrappedDataKey{
		Plaintext: plaintext,
		Version:   versionFromWrappedKey(req.WrappedKey),
	}, nil
}

func (t *OpenBaoTransit) RewrapDataKey(ctx context.Context, req RewrapDataKeyRequest) (RewrappedKey, error) {
	body := map[string]any{"ciphertext": strings.TrimSpace(req.WrappedKey)}
	if len(req.Context) > 0 {
		body["context"] = base64.StdEncoding.EncodeToString(req.Context)
	}
	if req.KeyVersion > 0 {
		body["key_version"] = req.KeyVersion
	}
	var out struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := t.write(ctx, http.MethodPost, t.path("rewrap", t.keyName), body, &out); err != nil {
		return RewrappedKey{}, err
	}
	if out.Ciphertext == "" {
		return RewrappedKey{}, fmt.Errorf("openbao transit rewrap response missing ciphertext: %w", ErrUnavailable)
	}
	return RewrappedKey{
		WrappedKey: out.Ciphertext,
		Version:    versionFromWrappedKey(out.Ciphertext),
		Changed:    out.Ciphertext != req.WrappedKey,
	}, nil
}

func (t *OpenBaoTransit) Readiness(ctx context.Context) (Readiness, error) {
	var out struct {
		LatestVersion            int `json:"latest_version"`
		MinimumDecryptionVersion int `json:"min_decryption_version"`
	}
	if err := t.write(ctx, http.MethodGet, t.path("keys", t.keyName), nil, &out); err != nil {
		return Readiness{}, err
	}
	if out.LatestVersion < 1 {
		return Readiness{}, fmt.Errorf("openbao transit key metadata missing latest version: %w", ErrMissingKey)
	}
	return Readiness{
		Ready:                    true,
		LatestVersion:            out.LatestVersion,
		MinimumDecryptionVersion: out.MinimumDecryptionVersion,
	}, nil
}

func (t *OpenBaoTransit) write(ctx context.Context, method, apiPath string, body, out any) error {
	reader, err := encodeOpenBaoRequestBody(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, t.endpoint(apiPath), reader)
	if err != nil {
		return fmt.Errorf("openbao transit request build failed: %w", ErrInvalidConfig)
	}
	req.Header.Set(openBaoTokenHeader, t.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openbao transit request failed: %w", ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeOpenBaoResponse(resp, out)
}

func encodeOpenBaoRequestBody(body any) (io.Reader, error) {
	if body == nil {
		return http.NoBody, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openbao transit request encode failed: %w", ErrInvalidRequest)
	}
	return bytes.NewReader(data), nil
}

func decodeOpenBaoResponse(resp *http.Response, out any) error {
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return fmt.Errorf("openbao transit response read failed: %w", ErrUnavailable)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return classifyOpenBaoFailure(resp.StatusCode, respBody)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []string        `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("openbao transit response decode failed: %w", ErrUnavailable)
	}
	if len(envelope.Errors) > 0 {
		return classifyOpenBaoFailure(resp.StatusCode, respBody)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("openbao transit response missing data: %w", ErrUnavailable)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("openbao transit data decode failed: %w", ErrUnavailable)
	}
	return nil
}

func (t *OpenBaoTransit) endpoint(apiPath string) string {
	u := t.baseURL
	u.Path = joinURLPath(u.Path, apiPath)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func (t *OpenBaoTransit) path(parts ...string) string {
	segments := []string{openBaoAPIPrefix}
	segments = append(segments, splitTransitPath(t.mountPath)...)
	for _, part := range parts {
		segments = append(segments, splitTransitPath(part)...)
	}
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func joinURLPath(basePath, apiPath string) string {
	basePath = strings.Trim(basePath, "/")
	apiPath = strings.Trim(apiPath, "/")
	if basePath == "" {
		return "/" + apiPath
	}
	return "/" + path.Join(basePath, apiPath)
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

func classifyOpenBaoFailure(statusCode int, body []byte) error {
	bodyText := strings.ToLower(string(body))
	var cause error
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		cause = ErrAuthDenied
	case statusCode == http.StatusNotFound:
		cause = ErrMissingKey
	case strings.Contains(bodyText, "minimum") && strings.Contains(bodyText, "version"):
		cause = ErrMinimumVersion
	case statusCode == http.StatusBadRequest:
		cause = ErrInvalidRequest
	case statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError:
		cause = ErrUnavailable
	default:
		cause = ErrUnavailable
	}
	return fmt.Errorf("openbao transit request failed with provider status %d: %w", statusCode, cause)
}
