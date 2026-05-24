package openbaosmoke

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	ReportKind = "openbao-transit-smoke-evidence"

	DefaultProfileID        = "scrap-prod-v1"
	DefaultEnvironmentID    = "local-kind"
	DefaultNamespace        = "scrap-local"
	DefaultDeployment       = "openbao"
	DefaultAddress          = "http://127.0.0.1:8200"
	DefaultOutageAddress    = "http://127.0.0.1:1"
	DefaultKubernetesRole   = "scrap-transit-smoke"
	DefaultTransitKeyPath   = "transit/keys/scrap-backend"
	DefaultAuditDevice      = "file:/tmp/openbao-audit.log"
	DefaultKubernetesClient = "openbao-transit-smoke"

	httpClientErrorStatus = 400
	transitKeyPathParts   = 2
	transitKeysPathParts  = 3
)

var ErrSmokeFailed = errors.New("openbao transit smoke failed")

type Options struct {
	ReleaseSHA       string
	DirtyTree        string
	ProfileID        string
	EnvironmentID    string
	Namespace        string
	Deployment       string
	Address          string
	OutageAddress    string
	AdminToken       string
	KubernetesJWT    string
	KubernetesRole   string
	KubernetesClient string
	TransitKeyPath   string
	AuditDevice      string
	HTTPClient       *http.Client
	Now              func() time.Time
	OperationID      string
}

type Report struct {
	ReportKind                string                  `json:"report_kind"`
	GeneratedAt               string                  `json:"generated_at"`
	ReleaseSHA                string                  `json:"release_sha"`
	DirtyTree                 string                  `json:"dirty_tree"`
	ProfileID                 string                  `json:"profile_id"`
	EnvironmentID             string                  `json:"environment_id"`
	Namespace                 string                  `json:"namespace"`
	Deployment                string                  `json:"openbao_deployment"`
	KubernetesClient          string                  `json:"kubernetes_client"`
	TransitKeyPath            string                  `json:"transit_key_path"`
	AuditDevice               string                  `json:"audit_device"`
	AuditDeviceStatus         string                  `json:"audit_device_status"`
	OperationIDs              []string                `json:"operation_ids"`
	RedactedRequestIDs        []string                `json:"redacted_request_ids"`
	KubernetesAuth            KubernetesAuthEvidence  `json:"kubernetes_auth"`
	Transit                   TransitEvidence         `json:"transit"`
	CryptoUnavailableOutcomes []CryptoUnavailableCase `json:"crypto_unavailable_outcomes"`
	EvidenceLimits            []string                `json:"evidence_limits"`
	Status                    string                  `json:"status"`
	Errors                    []string                `json:"errors,omitempty"`
}

type KubernetesAuthEvidence struct {
	Role                          string   `json:"role"`
	Policies                      []string `json:"policies"`
	DataKeyCapabilities           []string `json:"data_key_capabilities"`
	KeyAdminCapabilities          []string `json:"key_admin_capabilities"`
	BroadKeyAdminPermissions      bool     `json:"broad_key_admin_permissions"`
	DataKeyRequestWithoutKeyAdmin bool     `json:"data_key_request_without_key_admin"`
	LoginRequestID                string   `json:"login_request_id,omitempty"`
}

type TransitEvidence struct {
	KeyName              string `json:"key_name"`
	KeyVersionBefore     uint32 `json:"key_version_before"`
	DataKeyVersion       uint32 `json:"data_key_version"`
	RewrapVersion        uint32 `json:"rewrap_version"`
	KeyVersionAfter      uint32 `json:"key_version_after"`
	AADContextSHA256     string `json:"aad_context_sha256"`
	UnwrapAADMatched     bool   `json:"unwrap_aad_matched"`
	RewrapAADMatched     bool   `json:"rewrap_aad_matched"`
	PlaintextDEKRedacted bool   `json:"plaintext_dek_redacted"`
	WrappedDEKRedacted   bool   `json:"wrapped_dek_redacted"`
}

type CryptoUnavailableCase struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	ErrorClass  string `json:"error_class"`
	Description string `json:"description"`
	RequestID   string `json:"redacted_request_id,omitempty"`
}

type openBaoClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func Run(ctx context.Context, opts Options) (Report, error) {
	opts, err := validateOptions(opts)
	if err != nil {
		return Report{}, err
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if opts.OperationID == "" {
		opts.OperationID = "openbao-smoke-" + now().UTC().Format("20060102T150405Z")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	mount, keyName, err := splitTransitKeyPath(opts.TransitKeyPath)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		ReportKind:         ReportKind,
		GeneratedAt:        now().UTC().Format(time.RFC3339),
		ReleaseSHA:         opts.ReleaseSHA,
		DirtyTree:          opts.DirtyTree,
		ProfileID:          opts.ProfileID,
		EnvironmentID:      opts.EnvironmentID,
		Namespace:          opts.Namespace,
		Deployment:         opts.Deployment,
		KubernetesClient:   opts.KubernetesClient,
		TransitKeyPath:     opts.TransitKeyPath,
		AuditDevice:        opts.AuditDevice,
		OperationIDs:       []string{opts.OperationID},
		RedactedRequestIDs: []string{},
		Status:             "passed",
		EvidenceLimits: []string{
			"local OpenBao smoke evidence is release-rehearsal evidence only",
			"local OpenBao smoke does not approve live OpenBao HA, unseal, snapshot, key custody, or audit retention",
			"local OpenBao smoke does not apply downstream deployment approval",
			"evidence redacts plaintext DEKs, wrapped DEKs, OpenBao tokens, backend object bytes, and customer payloads",
		},
	}

	admin := openBaoClient{baseURL: opts.Address, token: opts.AdminToken, http: client}
	keyBefore, err := admin.keyVersion(ctx, mount, keyName)
	if err != nil {
		report.fail("read key version before smoke: " + err.Error())
	}
	report.Transit.KeyName = keyName
	report.Transit.KeyVersionBefore = keyBefore
	report.AuditDeviceStatus = auditStatus(ctx, admin, opts.AuditDevice, &report)

	kubeToken, policies, loginRequestID, err := admin.kubernetesLogin(ctx, opts.KubernetesRole, opts.KubernetesJWT)
	if err != nil {
		report.fail("kubernetes auth login: " + err.Error())
	} else {
		report.KubernetesAuth.Role = opts.KubernetesRole
		report.KubernetesAuth.Policies = policies
		report.KubernetesAuth.LoginRequestID = loginRequestID
		report.addRequestID(loginRequestID)
	}
	kube := openBaoClient{baseURL: opts.Address, token: kubeToken, http: client}
	dataCapabilities := capabilities(ctx, kube, fmt.Sprintf("%s/datakey/plaintext/%s", mount, keyName), &report)
	keyCapabilities := capabilities(ctx, kube, fmt.Sprintf("%s/keys/%s", mount, keyName), &report)
	report.KubernetesAuth.DataKeyCapabilities = dataCapabilities
	report.KubernetesAuth.KeyAdminCapabilities = keyCapabilities
	report.KubernetesAuth.BroadKeyAdminPermissions = hasBroadKeyAdmin(keyCapabilities)
	if report.KubernetesAuth.BroadKeyAdminPermissions {
		report.fail("kubernetes client has broad key-admin permissions")
	}

	aad := []byte(fmt.Sprintf("scrap:%s:%s:%s", opts.ProfileID, opts.EnvironmentID, opts.OperationID))
	report.Transit.AADContextSHA256 = sha256String(aad)
	contextValue := base64.StdEncoding.EncodeToString(aad)
	ciphertext, dataKeyVersion, reqID, err := kube.dataKey(ctx, mount, keyName, contextValue)
	if err != nil {
		report.fail("data key request through kubernetes auth: " + err.Error())
	} else {
		report.Transit.DataKeyVersion = dataKeyVersion
		report.KubernetesAuth.DataKeyRequestWithoutKeyAdmin = !report.KubernetesAuth.BroadKeyAdminPermissions
		report.addRequestID(reqID)
	}
	if ciphertext != "" {
		reqID, err = kube.decrypt(ctx, mount, keyName, ciphertext, contextValue)
		if err != nil {
			report.fail("unwrap with expected AAD: " + err.Error())
		} else {
			report.Transit.UnwrapAADMatched = true
			report.addRequestID(reqID)
		}
	}
	if err := admin.rotateKey(ctx, mount, keyName); err != nil {
		report.fail("rotate local test key before rewrap: " + err.Error())
	}
	keyAfter, err := admin.keyVersion(ctx, mount, keyName)
	if err != nil {
		report.fail("read key version after rotate: " + err.Error())
	}
	report.Transit.KeyVersionAfter = keyAfter
	if ciphertext != "" {
		rewrapVersion, reqID, err := kube.rewrap(ctx, mount, keyName, ciphertext, contextValue)
		if err != nil {
			report.fail("rewrap with expected AAD: " + err.Error())
		} else {
			report.Transit.RewrapVersion = rewrapVersion
			report.Transit.RewrapAADMatched = true
			report.addRequestID(reqID)
		}
		report.CryptoUnavailableOutcomes = append(report.CryptoUnavailableOutcomes, missingVersionOutcome(ctx, kube, mount, keyName, ciphertext, contextValue))
	}
	report.CryptoUnavailableOutcomes = append(report.CryptoUnavailableOutcomes, outageOutcome(ctx, opts, client, mount, keyName, contextValue))
	report.Transit.PlaintextDEKRedacted = true
	report.Transit.WrappedDEKRedacted = true
	for _, outcome := range report.CryptoUnavailableOutcomes {
		report.addRequestID(outcome.RequestID)
		if outcome.Status != "crypto-unavailable" {
			report.fail(outcome.Name + " did not produce crypto-unavailable")
		}
	}
	if len(report.Errors) > 0 {
		report.Status = "failed"
		return report, ErrSmokeFailed
	}
	return report, nil
}

func validateOptions(opts Options) (Options, error) {
	opts.ReleaseSHA = defaultText(opts.ReleaseSHA, "unknown")
	opts.DirtyTree = defaultText(opts.DirtyTree, "unknown")
	opts.ProfileID = defaultText(opts.ProfileID, DefaultProfileID)
	opts.EnvironmentID = defaultText(opts.EnvironmentID, DefaultEnvironmentID)
	opts.Namespace = defaultText(opts.Namespace, DefaultNamespace)
	opts.Deployment = defaultText(opts.Deployment, DefaultDeployment)
	opts.Address = defaultText(opts.Address, DefaultAddress)
	opts.OutageAddress = defaultText(opts.OutageAddress, DefaultOutageAddress)
	opts.KubernetesRole = defaultText(opts.KubernetesRole, DefaultKubernetesRole)
	opts.KubernetesClient = defaultText(opts.KubernetesClient, DefaultKubernetesClient)
	opts.TransitKeyPath = defaultText(opts.TransitKeyPath, DefaultTransitKeyPath)
	opts.AuditDevice = defaultText(opts.AuditDevice, DefaultAuditDevice)
	if strings.TrimSpace(opts.AdminToken) == "" {
		return Options{}, errors.New("openbao smoke requires BAO_TOKEN")
	}
	if strings.TrimSpace(opts.KubernetesJWT) == "" {
		return Options{}, errors.New("openbao smoke requires BAO_KUBERNETES_JWT")
	}
	for name, raw := range map[string]string{"openbao address": opts.Address, "openbao outage address": opts.OutageAddress} {
		if err := validateHTTPURL(name, raw); err != nil {
			return Options{}, err
		}
	}
	return opts, nil
}

func auditStatus(ctx context.Context, client openBaoClient, auditDevice string, report *Report) string {
	var out map[string]json.RawMessage
	reqID, err := client.do(ctx, http.MethodGet, "/v1/sys/audit", nil, &out)
	report.addRequestID(reqID)
	if err != nil {
		report.fail("read audit devices: " + err.Error())
		return "unknown"
	}
	expectedType := auditDevice
	if value, _, ok := strings.Cut(auditDevice, ":"); ok {
		expectedType = value
	}
	devices, err := auditDevices(out)
	if err != nil {
		report.fail("read audit devices: " + err.Error())
		return "unknown"
	}
	for path, device := range devices {
		if expectedType == "file" && device.Type == "file" && path == "file/" {
			return "enabled:" + auditDevice
		}
	}
	return "missing:" + auditDevice
}

type auditDeviceInfo struct {
	Type string `json:"type"`
}

func auditDevices(raw map[string]json.RawMessage) (map[string]auditDeviceInfo, error) {
	if len(raw) == 0 {
		return map[string]auditDeviceInfo{}, nil
	}
	if wrapped, ok := raw["data"]; ok && !isJSONNull(wrapped) {
		var devices map[string]auditDeviceInfo
		if err := json.Unmarshal(wrapped, &devices); err != nil {
			return nil, err
		}
		if devices != nil {
			return devices, nil
		}
	}
	devices := make(map[string]auditDeviceInfo)
	for path, payload := range raw {
		if isAuditMetadataField(path) {
			continue
		}
		var device auditDeviceInfo
		if err := json.Unmarshal(payload, &device); err != nil {
			return nil, err
		}
		if device.Type != "" {
			devices[path] = device
		}
	}
	return devices, nil
}

func isAuditMetadataField(path string) bool {
	switch path {
	case "request_id", "lease_id", "renewable", "lease_duration", "wrap_info", "warnings", "auth":
		return true
	default:
		return false
	}
}

func isJSONNull(value json.RawMessage) bool {
	return strings.TrimSpace(string(value)) == "null"
}

func (c openBaoClient) keyVersion(ctx context.Context, mount, keyName string) (uint32, error) {
	var out struct {
		Data struct {
			LatestVersion uint32 `json:"latest_version"`
		} `json:"data"`
	}
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/%s/keys/%s", mount, keyName), nil, &out)
	return out.Data.LatestVersion, err
}

func (c openBaoClient) kubernetesLogin(ctx context.Context, role, jwt string) (string, []string, string, error) {
	var out struct {
		Auth map[string]any `json:"auth"`
	}
	reqID, err := c.do(ctx, http.MethodPost, "/v1/auth/kubernetes/login", map[string]string{
		"role": role,
		"jwt":  jwt,
	}, &out)
	if err != nil {
		return "", nil, reqID, err
	}
	token, _ := out.Auth["client_token"].(string)
	rawPolicies, _ := out.Auth["policies"].([]any)
	policies := make([]string, 0, len(rawPolicies))
	for _, raw := range rawPolicies {
		if policy, ok := raw.(string); ok {
			policies = append(policies, policy)
		}
	}
	if token == "" {
		return "", policies, reqID, errors.New("openbao Kubernetes login did not return a client token")
	}
	return token, policies, reqID, nil
}

func capabilities(ctx context.Context, client openBaoClient, path string, report *Report) []string {
	var out struct {
		Capabilities []string `json:"capabilities"`
	}
	reqID, err := client.do(ctx, http.MethodPost, "/v1/sys/capabilities-self", map[string]string{"path": path}, &out)
	report.addRequestID(reqID)
	if err != nil {
		report.fail("read token capabilities for " + path + ": " + err.Error())
		return nil
	}
	return out.Capabilities
}

func (c openBaoClient) dataKey(ctx context.Context, mount, keyName, aadContext string) (string, uint32, string, error) {
	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	reqID, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/%s/datakey/plaintext/%s", mount, keyName), map[string]any{
		"context": aadContext,
		"bits":    256,
	}, &out)
	version, parseErr := ciphertextVersion(out.Data.Ciphertext)
	if err == nil && parseErr != nil {
		err = parseErr
	}
	return out.Data.Ciphertext, version, reqID, err
}

func (c openBaoClient) decrypt(ctx context.Context, mount, keyName, ciphertext, aadContext string) (string, error) {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/%s/decrypt/%s", mount, keyName), map[string]string{
		"ciphertext": ciphertext,
		"context":    aadContext,
	}, nil)
}

func (c openBaoClient) rotateKey(ctx context.Context, mount, keyName string) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/%s/keys/%s/rotate", mount, keyName), map[string]string{}, nil)
	return err
}

func (c openBaoClient) rewrap(ctx context.Context, mount, keyName, ciphertext, aadContext string) (uint32, string, error) {
	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	reqID, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/%s/rewrap/%s", mount, keyName), map[string]string{
		"ciphertext": ciphertext,
		"context":    aadContext,
	}, &out)
	version, parseErr := ciphertextVersion(out.Data.Ciphertext)
	if err == nil && parseErr != nil {
		err = parseErr
	}
	return version, reqID, err
}

func missingVersionOutcome(ctx context.Context, client openBaoClient, mount, keyName, ciphertext, aadContext string) CryptoUnavailableCase {
	badCiphertext := replaceCiphertextVersion(ciphertext, "4294967295")
	reqID, err := client.decrypt(ctx, mount, keyName, badCiphertext, aadContext)
	outcome := CryptoUnavailableCase{
		Name:        "missing-key-version",
		Status:      "crypto-unavailable",
		ErrorClass:  "key-material-unavailable",
		Description: "missing Transit key version is reported without logging wrapped key material",
		RequestID:   reqID,
	}
	if err == nil {
		outcome.Status = "unexpected-success"
		outcome.ErrorClass = ""
	}
	return outcome
}

func outageOutcome(ctx context.Context, opts Options, httpClient *http.Client, mount, keyName, aadContext string) CryptoUnavailableCase {
	outageClient := openBaoClient{baseURL: opts.OutageAddress, token: "redacted-outage-token", http: httpClient}
	reqID, err := outageClient.decrypt(ctx, mount, keyName, "vault:v1:redacted", aadContext)
	outcome := CryptoUnavailableCase{
		Name:        "transit-outage",
		Status:      "crypto-unavailable",
		ErrorClass:  "transit-unavailable",
		Description: "unreachable Transit endpoint is reported as crypto-unavailable",
		RequestID:   reqID,
	}
	if err == nil {
		outcome.Status = "unexpected-success"
		outcome.ErrorClass = ""
	}
	return outcome
}

func (c openBaoClient) do(ctx context.Context, method, apiPath string, payload, out any) (string, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		body = bytes.NewReader(encoded)
	}
	target, err := joinURL(c.baseURL, apiPath)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return "", fmt.Errorf("build openbao request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Vault-Token", c.token)
	req.Header.Set("X-Bao-Token", c.token)
	// #nosec G704 -- smoke probes operator-configured local OpenBao rehearsal endpoints.
	resp, err := c.http.Do(req)
	if err != nil {
		return "", classifyProbeError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	requestID := ""
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return requestID, readErr
	}
	requestID = redactedRequestID(resp.Header, responseBody)
	if resp.StatusCode >= httpClientErrorStatus {
		return requestID, fmt.Errorf("openbao %s %s failed with HTTP %d", method, apiPath, resp.StatusCode)
	}
	if out == nil {
		return requestID, nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return requestID, err
	}
	return requestID, nil
}

func splitTransitKeyPath(value string) (string, string, error) {
	cleaned := strings.Trim(strings.TrimSpace(value), "/")
	parts := strings.Split(cleaned, "/")
	if len(parts) >= transitKeysPathParts && parts[1] == "keys" {
		return parts[0], strings.Join(parts[2:], "/"), nil
	}
	if len(parts) >= transitKeyPathParts {
		return parts[0], strings.Join(parts[1:], "/"), nil
	}
	return "", "", fmt.Errorf("transit key path %q must include mount and key name", value)
}

func ciphertextVersion(ciphertext string) (uint32, error) {
	parts := strings.Split(ciphertext, ":")
	if len(parts) < transitKeysPathParts || !strings.HasPrefix(parts[1], "v") {
		return 0, errors.New("unexpected Transit ciphertext shape")
	}
	var version uint64
	if _, err := fmt.Sscanf(parts[1], "v%d", &version); err != nil {
		return 0, fmt.Errorf("parse transit ciphertext version: %w", err)
	}
	if version > uint64(^uint32(0)) {
		return 0, fmt.Errorf("transit key version %d exceeds uint32", version)
	}
	return uint32(version), nil
}

func replaceCiphertextVersion(ciphertext, version string) string {
	parts := strings.SplitN(ciphertext, ":", transitKeysPathParts)
	if len(parts) != transitKeysPathParts {
		return "vault:v" + version + ":redacted"
	}
	return parts[0] + ":v" + version + ":" + parts[2]
}

func hasBroadKeyAdmin(capabilities []string) bool {
	for _, capability := range capabilities {
		switch capability {
		case "create", "update", "delete", "sudo":
			return true
		}
	}
	return false
}

func (r *Report) fail(message string) {
	r.Errors = append(r.Errors, message)
}

func (r *Report) addRequestID(id string) {
	if id == "" {
		return
	}
	for _, existing := range r.RedactedRequestIDs {
		if existing == id {
			return
		}
	}
	r.RedactedRequestIDs = append(r.RedactedRequestIDs, id)
}

func redactedRequestID(header http.Header, body []byte) string {
	for _, key := range []string{"X-Vault-Request", "X-Bao-Request", "X-Request-Id"} {
		value := strings.TrimSpace(header.Get(key))
		if value == "" {
			continue
		}
		return "sha256:" + sha256String([]byte(value))[:16]
	}
	var metadata struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &metadata); err == nil && strings.TrimSpace(metadata.RequestID) != "" {
		return "sha256:" + sha256String([]byte(metadata.RequestID))[:16]
	}
	return ""
}

func sha256String(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func classifyProbeError(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("transit unavailable: %w", err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("transit unavailable: %w", err)
	}
	return err
}

func validateHTTPURL(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", name)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s requires host", name)
	}
	return nil
}

func joinURL(base, apiPath string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse openbao base URL: %w", err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(apiPath, "/")
	return parsed.String(), nil
}

func defaultText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
