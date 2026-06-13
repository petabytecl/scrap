package scrapctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	baoapi "github.com/openbao/openbao/api"
)

const (
	openBaoDefaultTokenEnv        = "BAO_TOKEN"
	openBaoDefaultMountPath       = "transit"
	openBaoDefaultKeyName         = "scrap-documents"
	openBaoDefaultKeyType         = "aes256-gcm96"
	openBaoDefaultEnvironment     = "local"
	openBaoDefaultSecretShares    = 1
	openBaoDefaultSecretThreshold = 1
	openBaoClientMaxRetries       = 0
	openBaoSecretFileMode         = 0o600
	openBaoEvidenceFileMode       = 0o600
)

var (
	errOpenBaoKeyMissing = errors.New("openbao transit key missing")
	envNamePattern       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type OpenBaoClientFactory func(openBaoClientConfig) (openBaoBootstrapClient, error)

type openBaoBootstrapClient interface {
	SetToken(token string)
	InitStatus(ctx context.Context) (bool, error)
	Init(ctx context.Context, req openBaoInitRequest) (openBaoInitResult, error)
	SealStatus(ctx context.Context) (openBaoSealStatus, error)
	Unseal(ctx context.Context, key string) (openBaoSealStatus, error)
	ListMounts(ctx context.Context) (map[string]openBaoMount, error)
	MountTransit(ctx context.Context, mountPath string) error
	ReadTransitKey(ctx context.Context, mountPath, keyName string) (openBaoTransitKeyStatus, error)
	CreateTransitKey(ctx context.Context, mountPath, keyName, keyType string, derived bool) error
}

type openBaoClientConfig struct {
	Address    string
	Token      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

type openBaoInitRequest struct {
	SecretShares    int
	SecretThreshold int
}

type openBaoInitResult struct {
	Keys      []string
	KeysB64   []string
	RootToken string
}

type openBaoSealStatus struct {
	Initialized bool
	Sealed      bool
	Progress    int
}

type openBaoMount struct {
	Type string
}

type openBaoTransitKeyStatus struct {
	Type           string
	TypePresent    bool
	Derived        bool
	DerivedPresent bool
	LatestVersion  int
}

type openBaoBootstrapOptions struct {
	common          commonOptions
	address         string
	tokenEnv        string
	mountPath       string
	keyName         string
	keyType         string
	environment     string
	evidencePath    string
	initSecretsPath string
	init            bool
	derived         bool
	secretShares    int
	secretThreshold int
	unsealKeyEnv    stringListFlag
}

type openBaoBootstrapReport struct {
	Status             string                    `json:"status"`
	Command            string                    `json:"command"`
	Environment        string                    `json:"environment"`
	OpenBaoEndpoint    string                    `json:"openbao_endpoint"`
	TransitMount       string                    `json:"transit_mount"`
	TransitKey         string                    `json:"transit_key"`
	KeyType            string                    `json:"key_type"`
	KeyDerived         bool                      `json:"key_derived"`
	EvidencePath       string                    `json:"evidence_path"`
	InitPerformed      bool                      `json:"init_performed"`
	InitSecretsWritten bool                      `json:"init_secrets_written"`
	Phases             []openBaoBootstrapPhase   `json:"phases"`
	RedactionChecks    []openBaoRedactionCheck   `json:"redaction_checks"`
	Dependency         openBaoDependencyEvidence `json:"dependency"`
	ChangedBoundaries  []string                  `json:"changed_boundaries"`
	SanitizedArgs      []string                  `json:"sanitized_args"`
	EnvVarsUsed        []string                  `json:"env_vars_used"`
	NextAction         string                    `json:"next_action"`
}

type openBaoBootstrapPhase struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type openBaoRedactionCheck struct {
	Surface string `json:"surface"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type openBaoDependencyEvidence struct {
	Module     string `json:"module"`
	Version    string `json:"version"`
	Client     string `json:"client"`
	Operations string `json:"operations"`
}

type openBaoBootstrapState struct {
	report           openBaoBootstrapReport
	forbiddenValues  []string
	initUnsealShares []string
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func runOpenBao(args []string, stdout, stderr io.Writer, deps Deps) error {
	if len(args) == 0 {
		return errors.New("usage: scrapctl openbao <bootstrap>")
	}
	switch args[0] {
	case "bootstrap":
		return runOpenBaoBootstrap(args[1:], stdout, stderr, deps)
	default:
		return fmt.Errorf("unsupported openbao command %q", args[0])
	}
}

func runOpenBaoBootstrap(args []string, stdout, stderr io.Writer, deps Deps) error {
	opts, err := parseOpenBaoBootstrapOptions(args)
	if err != nil {
		return err
	}
	deps = deps.withDefaults()
	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()

	state, runErr := executeOpenBaoBootstrap(ctx, opts, deps)
	if runErr != nil {
		state.report.Status = "fail"
		state.report.NextAction = "fix reported OpenBao bootstrap failure and rerun command"
	}
	stderrText := ""
	if runErr != nil {
		stderrText = fmt.Sprintf("openbao bootstrap: %s\n", safeOpenBaoError(runErr))
	}
	if err := finalizeOpenBaoBootstrapReport(opts, stdout, state, stderrText, ""); err != nil {
		return err
	}
	if runErr != nil {
		_, _ = io.WriteString(stderr, stderrText)
		return errors.New(safeOpenBaoError(runErr))
	}
	return nil
}

func parseOpenBaoBootstrapOptions(args []string) (openBaoBootstrapOptions, error) {
	opts := openBaoBootstrapOptions{
		tokenEnv:        openBaoDefaultTokenEnv,
		mountPath:       openBaoDefaultMountPath,
		keyName:         openBaoDefaultKeyName,
		keyType:         openBaoDefaultKeyType,
		environment:     openBaoDefaultEnvironment,
		derived:         true,
		secretShares:    openBaoDefaultSecretShares,
		secretThreshold: openBaoDefaultSecretThreshold,
	}
	fs := newFlagSet("openbao bootstrap", &opts.common, func(fs *flag.FlagSet, _ *commonOptions) {
		fs.StringVar(&opts.address, "address", "", "OpenBao base URL")
		fs.StringVar(&opts.tokenEnv, "token-env", opts.tokenEnv, "Environment variable containing the OpenBao token")
		fs.StringVar(&opts.mountPath, "mount-path", opts.mountPath, "OpenBao Transit mount path")
		fs.StringVar(&opts.keyName, "key-name", opts.keyName, "S.C.R.A.P. Transit key name")
		fs.StringVar(&opts.keyType, "key-type", opts.keyType, "OpenBao Transit key type")
		fs.BoolVar(&opts.derived, "key-derived", opts.derived, "Create or verify key derivation on the Transit key")
		fs.StringVar(&opts.environment, "environment", opts.environment, "Evidence environment label")
		fs.StringVar(&opts.evidencePath, "evidence-path", "", "Path for the redacted bootstrap evidence report")
		fs.BoolVar(&opts.init, "init", false, "Initialize an uninitialized OpenBao target")
		fs.StringVar(&opts.initSecretsPath, "init-secrets-path", "", "Path for init root token and unseal material")
		fs.IntVar(&opts.secretShares, "init-secret-shares", opts.secretShares, "OpenBao init secret shares")
		fs.IntVar(&opts.secretThreshold, "init-secret-threshold", opts.secretThreshold, "OpenBao init secret threshold")
		fs.Var(&opts.unsealKeyEnv, "unseal-key-env", "Environment variable containing one OpenBao unseal key share; may be repeated")
	})
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return openBaoBootstrapOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if err := rejectOpenBaoAdminTLSFlags(fs); err != nil {
		return openBaoBootstrapOptions{}, err
	}
	if !flagWasSet(fs, "address") {
		opts.address = firstEnv("OPENBAO_ADDR", "BAO_ADDR")
	}
	if err := validateOpenBaoBootstrapOptions(opts); err != nil {
		return openBaoBootstrapOptions{}, err
	}
	return opts, nil
}

func validateOpenBaoBootstrapOptions(opts openBaoBootstrapOptions) error {
	if err := validateCommon(opts.common); err != nil {
		return err
	}
	if err := validateOpenBaoBootstrapConnectionOptions(opts); err != nil {
		return err
	}
	if err := validateOpenBaoBootstrapTargetOptions(opts); err != nil {
		return err
	}
	return validateOpenBaoBootstrapInitOptions(opts)
}

func validateOpenBaoBootstrapConnectionOptions(opts openBaoBootstrapOptions) error {
	if _, err := normalizedOpenBaoAddress(opts.address); err != nil {
		return err
	}
	if err := validateEnvName("token env", opts.tokenEnv); err != nil {
		return err
	}
	for _, name := range opts.unsealKeyEnv {
		if err := validateEnvName("unseal key env", name); err != nil {
			return err
		}
	}
	return nil
}

func validateOpenBaoBootstrapTargetOptions(opts openBaoBootstrapOptions) error {
	if _, err := cleanOpenBaoPath("mount path", opts.mountPath); err != nil {
		return err
	}
	if _, err := cleanOpenBaoKeyName(opts.keyName); err != nil {
		return err
	}
	if strings.TrimSpace(opts.keyType) == "" {
		return errors.New("key-type is required")
	}
	if strings.TrimSpace(opts.environment) == "" {
		return errors.New("environment is required")
	}
	if strings.TrimSpace(opts.evidencePath) == "" {
		return errors.New("evidence-path is required")
	}
	if err := validateOperatorPath("evidence-path", opts.evidencePath); err != nil {
		return err
	}
	return nil
}

func validateOpenBaoBootstrapInitOptions(opts openBaoBootstrapOptions) error {
	if !opts.init {
		return nil
	}
	if strings.TrimSpace(opts.initSecretsPath) == "" {
		return errors.New("init-secrets-path is required when init is enabled")
	}
	if err := validateOperatorPath("init-secrets-path", opts.initSecretsPath); err != nil {
		return err
	}
	if sameOperatorPath(opts.evidencePath, opts.initSecretsPath) {
		return errors.New("evidence-path and init-secrets-path must be different")
	}
	if opts.secretShares <= 0 {
		return errors.New("init-secret-shares must be positive")
	}
	if opts.secretThreshold <= 0 || opts.secretThreshold > opts.secretShares {
		return errors.New("init-secret-threshold must be positive and no larger than init-secret-shares")
	}
	return nil
}

func executeOpenBaoBootstrap(ctx context.Context, opts openBaoBootstrapOptions, deps Deps) (openBaoBootstrapState, error) {
	address, err := normalizedOpenBaoAddress(opts.address)
	if err != nil {
		return openBaoBootstrapState{}, err
	}
	token := strings.TrimSpace(os.Getenv(opts.tokenEnv))
	state := openBaoBootstrapState{
		report: newOpenBaoBootstrapReport(opts, address),
	}
	state.captureSecret(token)

	client, err := deps.OpenBaoClientFactory(openBaoClientConfig{
		Address:    address,
		Token:      token,
		Timeout:    commandTimeout(opts.common.timeout),
		HTTPClient: deps.HTTPClient,
	})
	if err != nil {
		return state, fmt.Errorf("configure openbao client: %w", err)
	}

	token, err = initializeOpenBaoIfNeeded(ctx, client, opts, token, &state)
	if err != nil {
		return state, err
	}
	if err := ensureOpenBaoUnsealed(ctx, client, opts, &state); err != nil {
		return state, err
	}
	if err := applyOpenBaoBootstrapToken(client, opts, token, &state); err != nil {
		return state, err
	}

	mountPath, _ := cleanOpenBaoPath("mount path", opts.mountPath)
	keyName, _ := cleanOpenBaoKeyName(opts.keyName)
	if err := ensureOpenBaoTransitMount(ctx, client, mountPath, &state); err != nil {
		return state, err
	}
	if err := ensureOpenBaoTransitKey(ctx, client, mountPath, keyName, opts.keyType, opts.derived, &state); err != nil {
		return state, err
	}
	state.report.Status = "ok"
	state.report.NextAction = "configure S.C.R.A.P. Transit settings from the verified mount and key"
	return state, nil
}

func initializeOpenBaoIfNeeded(
	ctx context.Context,
	client openBaoBootstrapClient,
	opts openBaoBootstrapOptions,
	token string,
	state *openBaoBootstrapState,
) (string, error) {
	initialized, err := client.InitStatus(ctx)
	if err != nil {
		state.addPhase("init_status", "fail", safeOpenBaoError(err))
		return "", err
	}
	state.addPhase("init_status", "ok", initializedReason(initialized))
	if initialized {
		return token, nil
	}
	if !opts.init {
		err := errors.New("openbao target is uninitialized; rerun with init enabled")
		state.addPhase("init", "fail", err.Error())
		return "", err
	}
	sink, err := reserveOpenBaoInitSecretSink(opts.initSecretsPath)
	if err != nil {
		state.addPhase("init_secrets", "fail", err.Error())
		return "", err
	}
	defer sink.cleanupUnlessCommitted()

	initResult, err := client.Init(ctx, openBaoInitRequest{
		SecretShares:    opts.secretShares,
		SecretThreshold: opts.secretThreshold,
	})
	if err != nil {
		state.addPhase("init", "fail", safeOpenBaoError(err))
		return "", err
	}
	state.captureSecret(initResult.RootToken)
	state.captureSecrets(initResult.Keys...)
	state.captureSecrets(initResult.KeysB64...)
	if err := sink.write(initResult); err != nil {
		state.addPhase("init_secrets", "fail", err.Error())
		return "", err
	}
	state.report.InitPerformed = true
	state.report.InitSecretsWritten = true
	client.SetToken(initResult.RootToken)
	state.initUnsealShares = initUnsealShares(initResult)
	state.addPhase("init", "ok", "init material written to explicit secret file")
	return initResult.RootToken, nil
}

func ensureOpenBaoUnsealed(ctx context.Context, client openBaoBootstrapClient, opts openBaoBootstrapOptions, state *openBaoBootstrapState) error {
	sealStatus, err := client.SealStatus(ctx)
	if err != nil {
		state.addPhase("seal_status", "fail", safeOpenBaoError(err))
		return err
	}
	state.addPhase("seal_status", "ok", sealReason(sealStatus.Sealed))
	if sealStatus.Sealed {
		sealStatus, err = unsealOpenBao(ctx, client, opts.unsealKeyEnv, state)
		if err != nil {
			return err
		}
	}
	if sealStatus.Sealed {
		err := errors.New("openbao target remains sealed")
		state.addPhase("unseal", "fail", err.Error())
		return err
	}
	return nil
}

func applyOpenBaoBootstrapToken(client openBaoBootstrapClient, opts openBaoBootstrapOptions, token string, state *openBaoBootstrapState) error {
	if strings.TrimSpace(token) == "" {
		err := fmt.Errorf("token env %s is required", opts.tokenEnv)
		state.addPhase("token", "fail", err.Error())
		return err
	}
	client.SetToken(token)
	state.addPhase("token", "ok", "token loaded from configured boundary")
	return nil
}

func unsealOpenBao(ctx context.Context, client openBaoBootstrapClient, envNames []string, state *openBaoBootstrapState) (openBaoSealStatus, error) {
	if len(state.initUnsealShares) > 0 {
		return unsealOpenBaoWithShares(ctx, client, state.initUnsealShares, state)
	}
	if len(envNames) == 0 {
		err := errors.New("at least one unseal-key-env is required for sealed targets")
		state.addPhase("unseal", "fail", err.Error())
		return openBaoSealStatus{Sealed: true}, err
	}
	shares := make([]string, 0, len(envNames))
	for _, envName := range envNames {
		key := strings.TrimSpace(os.Getenv(envName))
		if key == "" {
			err := fmt.Errorf("unseal key env %s is required", envName)
			state.addPhase("unseal", "fail", err.Error())
			return openBaoSealStatus{Sealed: true}, err
		}
		state.captureSecret(key)
		shares = append(shares, key)
	}
	return unsealOpenBaoWithShares(ctx, client, shares, state)
}

func unsealOpenBaoWithShares(ctx context.Context, client openBaoBootstrapClient, shares []string, state *openBaoBootstrapState) (openBaoSealStatus, error) {
	var status openBaoSealStatus
	for _, key := range shares {
		next, err := client.Unseal(ctx, key)
		if err != nil {
			state.addPhase("unseal", "fail", safeOpenBaoError(err))
			return next, err
		}
		status = next
		if !status.Sealed {
			state.addPhase("unseal", "ok", "target unsealed")
			return status, nil
		}
	}
	state.addPhase("unseal", "fail", "provided shares did not reach unseal threshold")
	return status, errors.New("provided shares did not reach unseal threshold")
}

func ensureOpenBaoTransitMount(ctx context.Context, client openBaoBootstrapClient, mountPath string, state *openBaoBootstrapState) error {
	mounts, err := client.ListMounts(ctx)
	if err != nil {
		state.addPhase("mount_list", "fail", safeOpenBaoError(err))
		return err
	}
	mount, exists := mounts[mountKey(mountPath)]
	if exists {
		if mount.Type != "transit" {
			err := errors.New("existing mount is not transit")
			state.addPhase("mount", "fail", err.Error())
			return err
		}
		state.addPhase("mount", "ok", "transit mount verified")
		return nil
	}
	if err := client.MountTransit(ctx, mountPath); err != nil {
		state.addPhase("mount", "fail", safeOpenBaoError(err))
		return err
	}
	state.addPhase("mount", "ok", "transit mount created")
	return nil
}

func ensureOpenBaoTransitKey(ctx context.Context, client openBaoBootstrapClient, mountPath, keyName, keyType string, derived bool, state *openBaoBootstrapState) error {
	key, err := client.ReadTransitKey(ctx, mountPath, keyName)
	if err == nil {
		if err := validateOpenBaoTransitKey(key, keyType, derived); err != nil {
			state.addPhase("key", "fail", err.Error())
			return err
		}
		state.addPhase("key", "ok", "transit key verified")
		return nil
	}
	if !errors.Is(err, errOpenBaoKeyMissing) {
		state.addPhase("key_read", "fail", safeOpenBaoError(err))
		return err
	}
	if err := client.CreateTransitKey(ctx, mountPath, keyName, keyType, derived); err != nil {
		state.addPhase("key", "fail", safeOpenBaoError(err))
		return err
	}
	key, err = client.ReadTransitKey(ctx, mountPath, keyName)
	if err != nil {
		state.addPhase("key_verify", "fail", safeOpenBaoError(err))
		return err
	}
	if err := validateOpenBaoTransitKey(key, keyType, derived); err != nil {
		state.addPhase("key_verify", "fail", err.Error())
		return err
	}
	state.addPhase("key", "ok", "transit key created and verified")
	return nil
}

func validateOpenBaoTransitKey(key openBaoTransitKeyStatus, keyType string, derived bool) error {
	if !key.TypePresent || key.Type != keyType {
		return errors.New("existing transit key type is incompatible")
	}
	if !key.DerivedPresent || key.Derived != derived {
		return errors.New("existing transit key derivation setting is incompatible")
	}
	if key.LatestVersion < 1 {
		return errors.New("transit key latest version is invalid")
	}
	return nil
}

func validateEnvName(label, name string) error {
	name = strings.TrimSpace(name)
	if !envNamePattern.MatchString(name) {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func normalizedOpenBaoAddress(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("openbao address is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("openbao address scheme is invalid")
	}
	if parsed.User != nil {
		return "", errors.New("openbao address is invalid")
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func cleanOpenBaoPath(label, raw string) (string, error) {
	cleaned := strings.Trim(strings.TrimSpace(raw), "/")
	if cleaned == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", fmt.Errorf("%s is invalid", label)
		}
	}
	return cleaned, nil
}

func cleanOpenBaoKeyName(raw string) (string, error) {
	cleaned, err := cleanOpenBaoPath("key name", raw)
	if err != nil {
		return "", err
	}
	if strings.Contains(cleaned, "/") {
		return "", errors.New("key name is invalid")
	}
	return cleaned, nil
}

func rejectOpenBaoAdminTLSFlags(fs *flag.FlagSet) error {
	for _, name := range []string{"tls-cert", "tls-key", "tls-ca", "tls-server-name"} {
		if flagWasSet(fs, name) {
			return errors.New("admin/public TLS flags do not configure OpenBao; use an OpenBao address with the required TLS endpoint")
		}
	}
	return nil
}

func validateOperatorPath(label, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%s is required", label)
	}
	if strings.ContainsFunc(path, unicode.IsControl) {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func sameOperatorPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr == nil && rightErr == nil {
		return leftAbs == rightAbs
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func initUnsealShares(result openBaoInitResult) []string {
	if len(result.KeysB64) > 0 {
		return append([]string(nil), result.KeysB64...)
	}
	return append([]string(nil), result.Keys...)
}

func mountKey(mountPath string) string {
	return strings.Trim(mountPath, "/") + "/"
}

func initializedReason(initialized bool) string {
	if initialized {
		return "target initialized"
	}
	return "target uninitialized"
}

func sealReason(sealed bool) string {
	if sealed {
		return "target sealed"
	}
	return "target unsealed"
}

func safeOpenBaoError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "openbao request canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "openbao request deadline exceeded"
	}
	var responseErr *baoapi.ResponseError
	if errors.As(err, &responseErr) {
		return fmt.Sprintf("openbao request failed with provider status %d", responseErr.StatusCode)
	}
	return safeBootstrapReason(err.Error())
}

func safeBootstrapEndpoint(value string) string {
	endpoint, err := normalizedOpenBaoAddress(value)
	if err != nil {
		return diagnosticTextRedacted
	}
	return endpoint
}

func safeBootstrapLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		if diagnosticTextRuneAllowed(r) {
			continue
		}
		return diagnosticTextRedacted
	}
	return value
}

func safeBootstrapReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > diagnosticTextMaxBytes {
		return diagnosticTextRedacted
	}
	if bootstrapSensitiveText(value) {
		return diagnosticTextRedacted
	}
	for _, r := range value {
		if diagnosticTextRuneAllowed(r) || r == ' ' || r == ':' {
			continue
		}
		return diagnosticTextRedacted
	}
	return value
}

func bootstrapSensitiveText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"secret",
		"password",
		"private",
		"bearer",
		"authorization",
		"root token",
		"unseal key",
		"transit token",
		"wrapped key",
		"client cert",
		"token=",
		"token:",
		"key=",
		"key:",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
