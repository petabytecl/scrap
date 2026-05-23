package capacitysample

import (
	"bytes"
	"context"
	"crypto/hmac"
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
	"path"
	"sort"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/backend"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/safeconv"
)

const (
	ReportKind = "capacity-sample-advisory"

	DefaultEnvironmentID     = "local-kind"
	DefaultSampleCount       = 3
	DefaultDocumentSizeBytes = 4 * 1024
	DefaultDuration          = 5 * time.Second
	DefaultLocalDiskPath     = "."
	DefaultBackendURL        = "http://127.0.0.1:4566/scrap-local"
	DefaultBackendAccessKey  = "test"
	DefaultBackendSecretKey  = "test"
	DefaultBackendRegion     = "us-east-1"
	DefaultBackendService    = "s3"
	DefaultOpenBaoAddress    = "http://127.0.0.1:8200"
	DefaultOpenBaoToken      = "local-root"
	DefaultOpenBaoKeyPath    = "transit/keys/scrap-backend"

	MaxSampleCount       = 32
	MaxDocumentSizeBytes = 8 * 1024 * 1024
	MaxDuration          = 5 * time.Minute
)

type AdminClient interface {
	GetCapacityRunway(context.Context, *adminv1.GetCapacityRunwayRequest, ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error)
}

type Options struct {
	ReleaseSHA             string
	DirtyTree              string
	ProfileID              string
	EnvironmentID          string
	SampleCount            int
	DocumentSizesBytes     []uint64
	Duration               time.Duration
	LocalDiskPath          string
	BackendURL             string
	BackendAccessKeyID     string
	BackendSecretAccessKey string
	BackendRegion          string
	BackendService         string
	OpenBaoAddress         string
	OpenBaoToken           string
	OpenBaoTransitKeyPath  string
	HTTPClient             *http.Client
	Now                    func() time.Time
}

type Report struct {
	ReportKind                             string                  `json:"report_kind"`
	GeneratedAt                            string                  `json:"generated_at"`
	ReleaseSHA                             string                  `json:"release_sha"`
	DirtyTree                              string                  `json:"dirty_tree"`
	ProfileID                              string                  `json:"profile_id"`
	EnvironmentID                          string                  `json:"environment_id"`
	Advisory                               bool                    `json:"advisory"`
	AdvisoryOnly                           bool                    `json:"advisory_only"`
	DoesNotSatisfyProductionReadinessGates bool                    `json:"does_not_satisfy_production_readiness_gates"`
	Workload                               WorkloadShape           `json:"workload"`
	DurationMillis                         int64                   `json:"duration_millis"`
	LocalDisk                              DiskObservation         `json:"local_disk"`
	Admin                                  AdminObservation        `json:"admin"`
	Backend                                []RequestSample         `json:"backend_request_latency_samples"`
	OpenBao                                []RequestSample         `json:"openbao_wrap_unwrap_rewrap_samples"`
	ErrorClasses                           []string                `json:"error_classes"`
	OperationIDs                           []string                `json:"operation_ids"`
	RedactedRequestIDs                     []string                `json:"redacted_request_ids"`
	ProposedProfile                        ProposedCapacityProfile `json:"proposed_capacity_profile"`
	EvidenceLimits                         []string                `json:"evidence_limits"`
}

type WorkloadShape struct {
	SampleCount        int      `json:"sample_count"`
	DurationMillis     int64    `json:"duration_millis"`
	DocumentSizesBytes []uint64 `json:"document_sizes_bytes"`
	BackendOperations  []string `json:"backend_operations"`
	OpenBaoOperations  []string `json:"openbao_operations"`
	Bounded            bool     `json:"bounded"`
	NonProductionOnly  bool     `json:"non_production_only"`
	ActiveProbe        bool     `json:"active_probe"`
}

type DiskObservation struct {
	Path           string `json:"path"`
	TotalBytes     uint64 `json:"total_bytes,omitempty"`
	FreeBytes      uint64 `json:"free_bytes,omitempty"`
	AvailableBytes uint64 `json:"available_bytes,omitempty"`
	ErrorClass     string `json:"error_class,omitempty"`
	Error          string `json:"error,omitempty"`
}

type AdminObservation struct {
	CapacityProfileID    string             `json:"capacity_profile_id,omitempty"`
	UsableBytesRemaining uint64             `json:"usable_bytes_remaining,omitempty"`
	EstimatedBytesPerDay uint64             `json:"estimated_bytes_per_day,omitempty"`
	RunwayDays           uint32             `json:"runway_days,omitempty"`
	Warnings             []OperationWarning `json:"warnings,omitempty"`
	LatencyMillis        int64              `json:"latency_millis"`
	ErrorClass           string             `json:"error_class,omitempty"`
	Error                string             `json:"error,omitempty"`
}

type OperationWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RequestSample struct {
	Target            string `json:"target"`
	Operation         string `json:"operation"`
	Method            string `json:"method,omitempty"`
	StatusCode        int    `json:"status_code,omitempty"`
	Bytes             uint64 `json:"bytes,omitempty"`
	LatencyMillis     int64  `json:"latency_millis"`
	ErrorClass        string `json:"error_class,omitempty"`
	Error             string `json:"error,omitempty"`
	RedactedRequestID string `json:"redacted_request_id,omitempty"`
}

type ProposedCapacityProfile struct {
	ProfileID       string                 `json:"profile_id"`
	Provider        string                 `json:"provider"`
	Mode            string                 `json:"mode"`
	Advisory        bool                   `json:"advisory"`
	ByteBudget      ProposedByteBudget     `json:"byte_budget"`
	RequestBudget   ProposedRequestBudget  `json:"request_budget"`
	LaneBudgets     []ProposedLaneBudget   `json:"lane_budgets"`
	RampPolicy      ProposedRampPolicy     `json:"ramp_policy"`
	CircuitBreakers ProposedCircuitBreaker `json:"circuit_breakers"`
	DiskGuardBands  ProposedDiskGuardBands `json:"disk_guard_bands"`
	Runway          ProposedRunway         `json:"runway_thresholds"`
}

type ProposedByteBudget struct {
	SustainedIngressBytesPerSecond uint64 `json:"sustained_ingress_bytes_per_second"`
	BurstIngressBytesPerSecond     uint64 `json:"burst_ingress_bytes_per_second"`
	BackendWriteBytesPerSecond     uint64 `json:"backend_write_bytes_per_second"`
	BackendReadBytesPerSecond      uint64 `json:"backend_read_bytes_per_second"`
	LocalUsableBytes               uint64 `json:"local_usable_bytes"`
	MaxObjectBytes                 uint64 `json:"max_object_bytes"`
}

type ProposedRequestBudget struct {
	PutRequestsPerSecond    uint64 `json:"put_requests_per_second"`
	GetRequestsPerSecond    uint64 `json:"get_requests_per_second"`
	HeadRequestsPerSecond   uint64 `json:"head_requests_per_second"`
	DeleteRequestsPerSecond uint64 `json:"delete_requests_per_second"`
}

type ProposedLaneBudget struct {
	Lane              string `json:"lane"`
	ReservedWorkers   uint32 `json:"reserved_workers"`
	MaxWorkers        uint32 `json:"max_workers"`
	BytesPerSecond    uint64 `json:"bytes_per_second"`
	RequestsPerSecond uint64 `json:"requests_per_second"`
}

type ProposedRampPolicy struct {
	InitialRequestsPerSecond uint64 `json:"initial_requests_per_second"`
	MaxRequestsPerSecond     uint64 `json:"max_requests_per_second"`
	StepRequestsPerSecond    uint64 `json:"step_requests_per_second"`
	StepIntervalMillis       int64  `json:"step_interval_millis"`
}

type ProposedCircuitBreaker struct {
	ConsecutiveFailures uint32 `json:"consecutive_failures"`
	ErrorRatePermille   uint32 `json:"error_rate_permille"`
	MinimumSamples      uint32 `json:"minimum_samples"`
	OpenIntervalMillis  int64  `json:"open_interval_millis"`
	HalfOpenRequests    uint32 `json:"half_open_requests"`
}

type ProposedDiskGuardBands struct {
	AdmissionMinFreeBytes  uint64 `json:"admission_min_free_bytes"`
	WarningMinFreeBytes    uint64 `json:"warning_min_free_bytes"`
	CriticalMinFreeBytes   uint64 `json:"critical_min_free_bytes"`
	FailClosedMinFreeBytes uint64 `json:"fail_closed_min_free_bytes"`
}

type ProposedRunway struct {
	MinimumSafeRunwayMillis int64  `json:"minimum_safe_runway_millis"`
	EstimatedBytesPerDay    uint64 `json:"estimated_bytes_per_day"`
}

func Run(ctx context.Context, opts Options, admin AdminClient) (Report, error) {
	opts, err := ValidateOptions(opts)
	if err != nil {
		return Report{}, err
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	started := time.Now()
	report := Report{
		ReportKind:                             ReportKind,
		GeneratedAt:                            now().UTC().Format(time.RFC3339),
		ReleaseSHA:                             opts.ReleaseSHA,
		DirtyTree:                              opts.DirtyTree,
		ProfileID:                              opts.ProfileID,
		EnvironmentID:                          opts.EnvironmentID,
		Advisory:                               true,
		AdvisoryOnly:                           true,
		DoesNotSatisfyProductionReadinessGates: true,
		Workload:                               workloadShape(opts),
		EvidenceLimits: []string{
			"capacity sample evidence is advisory non-production evidence only",
			"capacity sample evidence does not write production config",
			"capacity sample evidence does not update production signoff documents",
			"capacity sample evidence does not satisfy production write ACK readiness gates",
		},
	}

	report.LocalDisk = observeLocalDisk(opts.LocalDiskPath)
	report.Admin = observeAdmin(ctx, admin, opts.ProfileID)
	report.Backend = probeBackend(ctx, client, opts)
	report.OpenBao = probeOpenBao(ctx, client, opts)
	report.DurationMillis = durationMillis(time.Since(started))
	report.ErrorClasses = collectErrorClasses(report)
	report.RedactedRequestIDs = collectRequestIDs(report)
	report.OperationIDs = []string{}
	report.ProposedProfile = proposeProfile(opts, report)
	if err := report.ProposedProfile.Validate(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func ValidateOptions(opts Options) (Options, error) {
	opts.ReleaseSHA = defaultText(opts.ReleaseSHA, "unknown")
	opts.DirtyTree = defaultText(opts.DirtyTree, "unknown")
	opts.ProfileID = strings.TrimSpace(opts.ProfileID)
	opts.EnvironmentID = defaultText(opts.EnvironmentID, DefaultEnvironmentID)
	opts.LocalDiskPath = defaultText(opts.LocalDiskPath, DefaultLocalDiskPath)
	opts.BackendURL = defaultText(opts.BackendURL, DefaultBackendURL)
	opts.OpenBaoAddress = defaultText(opts.OpenBaoAddress, DefaultOpenBaoAddress)
	opts.OpenBaoToken = defaultText(opts.OpenBaoToken, DefaultOpenBaoToken)
	opts.OpenBaoTransitKeyPath = defaultText(opts.OpenBaoTransitKeyPath, DefaultOpenBaoKeyPath)
	if opts.SampleCount == 0 {
		opts.SampleCount = DefaultSampleCount
	}
	if len(opts.DocumentSizesBytes) == 0 {
		opts.DocumentSizesBytes = []uint64{DefaultDocumentSizeBytes}
	}
	if opts.Duration == 0 {
		opts.Duration = DefaultDuration
	}
	if opts.ProfileID == "" {
		return Options{}, errors.New("capacity sample requires profile_id")
	}
	opts.BackendAccessKeyID = defaultText(opts.BackendAccessKeyID, DefaultBackendAccessKey)
	opts.BackendSecretAccessKey = defaultText(opts.BackendSecretAccessKey, DefaultBackendSecretKey)
	opts.BackendRegion = defaultText(opts.BackendRegion, DefaultBackendRegion)
	opts.BackendService = defaultText(opts.BackendService, DefaultBackendService)
	if opts.SampleCount < 1 || opts.SampleCount > MaxSampleCount {
		return Options{}, fmt.Errorf("capacity sample --samples must be 1..%d", MaxSampleCount)
	}
	if opts.Duration <= 0 || opts.Duration > MaxDuration {
		return Options{}, fmt.Errorf("capacity sample --duration must be positive and no more than %s", MaxDuration)
	}
	for _, size := range opts.DocumentSizesBytes {
		if size == 0 || size > MaxDocumentSizeBytes {
			return Options{}, fmt.Errorf("capacity sample --document-size must be 1..%d bytes", MaxDocumentSizeBytes)
		}
	}
	if err := validateHTTPURL("backend url", opts.BackendURL); err != nil {
		return Options{}, err
	}
	if err := validateHTTPURL("openbao address", opts.OpenBaoAddress); err != nil {
		return Options{}, err
	}
	if strings.TrimSpace(opts.OpenBaoToken) == "" {
		return Options{}, errors.New("capacity sample requires openbao token")
	}
	if _, _, err := splitOpenBaoTransitKeyPath(opts.OpenBaoTransitKeyPath); err != nil {
		return Options{}, err
	}
	return opts, nil
}

func (p ProposedCapacityProfile) Validate() error {
	return p.BackendProfile().Validate()
}

func (p ProposedCapacityProfile) BackendProfile() backend.CapacityProfile {
	lanes := make([]backend.LaneBudget, 0, len(p.LaneBudgets))
	for _, lane := range p.LaneBudgets {
		lanes = append(lanes, backend.LaneBudget{
			Lane:              backend.Lane(lane.Lane),
			ReservedWorkers:   lane.ReservedWorkers,
			MaxWorkers:        lane.MaxWorkers,
			BytesPerSecond:    lane.BytesPerSecond,
			RequestsPerSecond: lane.RequestsPerSecond,
		})
	}
	return backend.CapacityProfile{
		ID:       p.ProfileID,
		Provider: backend.Provider(p.Provider),
		Mode:     backend.ProfileMode(p.Mode),
		ByteBudget: backend.ByteBudget{
			SustainedIngressBytesPerSecond: p.ByteBudget.SustainedIngressBytesPerSecond,
			BurstIngressBytesPerSecond:     p.ByteBudget.BurstIngressBytesPerSecond,
			BackendWriteBytesPerSecond:     p.ByteBudget.BackendWriteBytesPerSecond,
			BackendReadBytesPerSecond:      p.ByteBudget.BackendReadBytesPerSecond,
			LocalUsableBytes:               p.ByteBudget.LocalUsableBytes,
			MaxObjectBytes:                 p.ByteBudget.MaxObjectBytes,
		},
		RequestBudget: backend.RequestBudget{
			PutRequestsPerSecond:    p.RequestBudget.PutRequestsPerSecond,
			GetRequestsPerSecond:    p.RequestBudget.GetRequestsPerSecond,
			HeadRequestsPerSecond:   p.RequestBudget.HeadRequestsPerSecond,
			DeleteRequestsPerSecond: p.RequestBudget.DeleteRequestsPerSecond,
		},
		LaneBudgets: lanes,
		RampPolicy: backend.RampPolicy{
			InitialRequestsPerSecond: p.RampPolicy.InitialRequestsPerSecond,
			MaxRequestsPerSecond:     p.RampPolicy.MaxRequestsPerSecond,
			StepRequestsPerSecond:    p.RampPolicy.StepRequestsPerSecond,
			StepInterval:             time.Duration(p.RampPolicy.StepIntervalMillis) * time.Millisecond,
		},
		CircuitBreakers: backend.CircuitBreakerPolicy{
			ConsecutiveFailures: p.CircuitBreakers.ConsecutiveFailures,
			ErrorRatePermille:   p.CircuitBreakers.ErrorRatePermille,
			MinimumSamples:      p.CircuitBreakers.MinimumSamples,
			OpenInterval:        time.Duration(p.CircuitBreakers.OpenIntervalMillis) * time.Millisecond,
			HalfOpenRequests:    p.CircuitBreakers.HalfOpenRequests,
		},
		DiskGuardBands: backend.DiskGuardBands{
			AdmissionMinFreeBytes:  p.DiskGuardBands.AdmissionMinFreeBytes,
			WarningMinFreeBytes:    p.DiskGuardBands.WarningMinFreeBytes,
			CriticalMinFreeBytes:   p.DiskGuardBands.CriticalMinFreeBytes,
			FailClosedMinFreeBytes: p.DiskGuardBands.FailClosedMinFreeBytes,
		},
		Runway: backend.RunwayThresholds{
			MinimumSafeRunway:    time.Duration(p.Runway.MinimumSafeRunwayMillis) * time.Millisecond,
			EstimatedBytesPerDay: p.Runway.EstimatedBytesPerDay,
		},
	}
}

func workloadShape(opts Options) WorkloadShape {
	return WorkloadShape{
		SampleCount:        opts.SampleCount,
		DurationMillis:     durationMillis(opts.Duration),
		DocumentSizesBytes: append([]uint64(nil), opts.DocumentSizesBytes...),
		BackendOperations:  []string{"PUT", "HEAD", "GET"},
		OpenBaoOperations:  []string{"wrap", "unwrap", "rewrap"},
		Bounded:            true,
		NonProductionOnly:  true,
		ActiveProbe:        true,
	}
}

func observeLocalDisk(path string) DiskObservation {
	observation := DiskObservation{Path: path}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		observation.ErrorClass = string(backend.ErrorClassPermanent)
		observation.Error = err.Error()
		return observation
	}
	blockSize, err := safeconv.Int64ToUint64("statfs block size", stat.Bsize)
	if err != nil {
		observation.ErrorClass = string(backend.ErrorClassPermanent)
		observation.Error = err.Error()
		return observation
	}
	observation.TotalBytes = stat.Blocks * blockSize
	observation.FreeBytes = stat.Bfree * blockSize
	observation.AvailableBytes = stat.Bavail * blockSize
	return observation
}

func observeAdmin(ctx context.Context, client AdminClient, profileID string) AdminObservation {
	started := time.Now()
	observation := AdminObservation{}
	if client == nil {
		observation.ErrorClass = string(backend.ErrorClassPermanent)
		observation.Error = "admin client is required"
		return observation
	}
	resp, err := client.GetCapacityRunway(ctx, &adminv1.GetCapacityRunwayRequest{
		CapacityProfileId: optionalString(profileID),
	})
	observation.LatencyMillis = durationMillis(time.Since(started))
	if err != nil {
		observation.ErrorClass = string(classifyGRPCError(err))
		observation.Error = err.Error()
		return observation
	}
	runway := resp.GetRunway()
	observation.CapacityProfileID = runway.GetCapacityProfileId()
	observation.UsableBytesRemaining = runway.GetUsableBytesRemaining()
	observation.EstimatedBytesPerDay = runway.GetEstimatedBytesPerDay()
	observation.RunwayDays = runway.GetRunwayDays()
	for _, warning := range runway.GetWarnings() {
		observation.Warnings = append(observation.Warnings, OperationWarning{
			Code:    warning.GetCode(),
			Message: warning.GetMessage(),
		})
	}
	return observation
}

func probeBackend(ctx context.Context, client *http.Client, opts Options) []RequestSample {
	samples := make([]RequestSample, 0, opts.SampleCount*3)
	deadline, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()
	for i := range opts.SampleCount {
		size := opts.DocumentSizesBytes[i%len(opts.DocumentSizesBytes)]
		key := fmt.Sprintf("capacity-sample/%s/%d", sanitizePathPart(opts.ProfileID), i)
		body := sampleBytes(size, byte(i+1))
		targetURL, err := backendObjectURL(opts.BackendURL, key)
		if err != nil {
			samples = append(samples, RequestSample{
				Target:     opts.BackendURL,
				Operation:  "put",
				ErrorClass: string(backend.ErrorClassPermanent),
				Error:      err.Error(),
			})
			continue
		}
		samples = append(samples, doBackendRequest(deadline, client, opts, http.MethodPut, targetURL, body, "put", size))
		samples = append(samples, doBackendRequest(deadline, client, opts, http.MethodHead, targetURL, nil, "head", 0))
		samples = append(samples, doBackendRequest(deadline, client, opts, http.MethodGet, targetURL, nil, "get", size))
	}
	return samples
}

func probeOpenBao(ctx context.Context, client *http.Client, opts Options) []RequestSample {
	mount, keyName, err := splitOpenBaoTransitKeyPath(opts.OpenBaoTransitKeyPath)
	if err != nil {
		return []RequestSample{{
			Target:     opts.OpenBaoAddress,
			Operation:  "wrap",
			ErrorClass: string(backend.ErrorClassPermanent),
			Error:      err.Error(),
		}}
	}
	samples := make([]RequestSample, 0, opts.SampleCount*3)
	deadline, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()
	for i := range opts.SampleCount {
		contextValue := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("scrap-capacity-sample:%s:%d", opts.ProfileID, i)))
		wrapTarget := openBaoURL(opts.OpenBaoAddress, mount, "datakey/plaintext", keyName)
		wrapResp := struct {
			Data struct {
				Ciphertext string `json:"ciphertext"`
			} `json:"data"`
		}{}
		wrapSample := doOpenBaoRequest(deadline, client, opts.OpenBaoToken, http.MethodPost, wrapTarget, map[string]any{
			"context": contextValue,
			"bits":    256,
		}, &wrapResp, "wrap")
		samples = append(samples, wrapSample)
		if wrapSample.ErrorClass != "" || wrapResp.Data.Ciphertext == "" {
			continue
		}
		unwrapTarget := openBaoURL(opts.OpenBaoAddress, mount, "decrypt", keyName)
		unwrapSample := doOpenBaoRequest(deadline, client, opts.OpenBaoToken, http.MethodPost, unwrapTarget, map[string]any{
			"ciphertext": wrapResp.Data.Ciphertext,
			"context":    contextValue,
		}, nil, "unwrap")
		samples = append(samples, unwrapSample)
		rewrapTarget := openBaoURL(opts.OpenBaoAddress, mount, "rewrap", keyName)
		rewrapSample := doOpenBaoRequest(deadline, client, opts.OpenBaoToken, http.MethodPost, rewrapTarget, map[string]any{
			"ciphertext": wrapResp.Data.Ciphertext,
			"context":    contextValue,
		}, nil, "rewrap")
		samples = append(samples, rewrapSample)
	}
	return samples
}

func doBackendRequest(
	ctx context.Context,
	client *http.Client,
	opts Options,
	method string,
	targetURL string,
	body []byte,
	operation string,
	size uint64,
) RequestSample {
	started := time.Now()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reader)
	if err != nil {
		return failedRequestSample("backend", operation, method, size, started, backend.ErrorClassPermanent, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if err := signBackendRequest(req, body, opts, time.Now().UTC()); err != nil {
		return failedRequestSample("backend", operation, method, size, started, backend.ErrorClassPermanent, err)
	}
	// #nosec G704 -- capacity sampling probes operator-configured local rehearsal endpoints.
	resp, err := client.Do(req)
	if err != nil {
		return failedRequestSample("backend", operation, method, size, started, classifyProbeError(err), err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if method != http.MethodHead {
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return failedRequestSample("backend", operation, method, size, started, classifyProbeError(err), err)
		}
	}
	return RequestSample{
		Target:            "backend",
		Operation:         operation,
		Method:            method,
		StatusCode:        resp.StatusCode,
		Bytes:             size,
		LatencyMillis:     durationMillis(time.Since(started)),
		ErrorClass:        string(classifyHTTPStatus(resp.StatusCode)),
		RedactedRequestID: redactedRequestID(resp.Header),
	}
}

func doOpenBaoRequest(
	ctx context.Context,
	client *http.Client,
	token string,
	method string,
	targetURL string,
	payload map[string]any,
	out any,
	operation string,
) RequestSample {
	body, err := json.Marshal(payload)
	if err != nil {
		return RequestSample{
			Target:     "openbao",
			Operation:  operation,
			Method:     method,
			ErrorClass: string(backend.ErrorClassPermanent),
			Error:      err.Error(),
		}
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return failedRequestSample("openbao", operation, method, 0, started, backend.ErrorClassPermanent, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("X-Bao-Token", token)
	// #nosec G704 -- capacity sampling probes operator-configured local OpenBao endpoints.
	resp, err := client.Do(req)
	if err != nil {
		return failedRequestSample("openbao", operation, method, 0, started, classifyProbeError(err), err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return failedRequestSample("openbao", operation, method, 0, started, classifyProbeError(readErr), readErr)
	}
	sample := RequestSample{
		Target:            "openbao",
		Operation:         operation,
		Method:            method,
		StatusCode:        resp.StatusCode,
		LatencyMillis:     durationMillis(time.Since(started)),
		ErrorClass:        string(classifyHTTPStatus(resp.StatusCode)),
		RedactedRequestID: redactedRequestID(resp.Header),
	}
	if sample.ErrorClass != "" || out == nil {
		return sample
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		sample.ErrorClass = string(backend.ErrorClassPermanent)
		sample.Error = err.Error()
	}
	return sample
}

func signBackendRequest(req *http.Request, body []byte, opts Options, now time.Time) error {
	if opts.BackendAccessKeyID == "" && opts.BackendSecretAccessKey == "" {
		return nil
	}
	if opts.BackendAccessKeyID == "" || opts.BackendSecretAccessKey == "" {
		return errors.New("backend SigV4 signing requires both access key id and secret access key")
	}
	payloadHash := sha256Hex(body)
	amzDate := now.UTC().Format("20060102T150405Z")
	shortDate := now.UTC().Format("20060102")
	req.Host = req.URL.Host
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := strings.Join([]string{
		"host:" + strings.ToLower(req.URL.Host),
		"x-amz-content-sha256:" + payloadHash,
		"x-amz-date:" + amzDate,
		"",
	}, "\n")
	canonicalRequest := req.Method + "\n" +
		canonicalURI(req.URL) + "\n" +
		canonicalQuery(req.URL) + "\n" +
		canonicalHeaders +
		signedHeaders + "\n" +
		payloadHash
	scope := strings.Join([]string{shortDate, opts.BackendRegion, opts.BackendService, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(opts.BackendSecretAccessKey, shortDate, opts.BackendRegion, opts.BackendService), []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		opts.BackendAccessKeyID,
		scope,
		signedHeaders,
		signature,
	))
	return nil
}

func signingKey(secretAccessKey, shortDate, region, service string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secretAccessKey), []byte(shortDate))
	regionKey := hmacSHA256(dateKey, []byte(region))
	serviceKey := hmacSHA256(regionKey, []byte(service))
	return hmacSHA256(serviceKey, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalURI(parsed *url.URL) string {
	uri := parsed.EscapedPath()
	if uri == "" {
		return "/"
	}
	return uri
}

func canonicalQuery(parsed *url.URL) string {
	if parsed.RawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return parsed.RawQuery
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		sort.Strings(values[key])
		for _, value := range values[key] {
			parts = append(parts, awsEscape(key)+"="+awsEscape(value))
		}
	}
	return strings.Join(parts, "&")
}

func awsEscape(value string) string {
	escaped := url.QueryEscape(value)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

func failedRequestSample(
	target string,
	operation string,
	method string,
	size uint64,
	started time.Time,
	class backend.ErrorClass,
	err error,
) RequestSample {
	if class == "" {
		class = backend.ErrorClassPermanent
	}
	return RequestSample{
		Target:        target,
		Operation:     operation,
		Method:        method,
		Bytes:         size,
		LatencyMillis: durationMillis(time.Since(started)),
		ErrorClass:    string(class),
		Error:         err.Error(),
	}
}

func proposeProfile(opts Options, report Report) ProposedCapacityProfile {
	localUsable := report.LocalDisk.AvailableBytes
	if localUsable == 0 {
		localUsable = report.Admin.UsableBytesRemaining
	}
	if localUsable < 1<<30 {
		localUsable = 1 << 30
	}
	guard := proposeGuardBands(localUsable)
	postAdmission := localUsable - guard.AdmissionMinFreeBytes
	maxDailyBPS := maxUint64(1, postAdmission/uint64((24*time.Hour)/time.Second))
	observedPutBPS := observedBytesPerSecond(report.Backend, "put")
	if observedPutBPS == 0 {
		observedPutBPS = maxUint64(1, maxDocumentSize(opts.DocumentSizesBytes))
	}
	minimumSamples := safeMinimumSamples(len(report.Backend) + len(report.OpenBao))
	sustained := minUint64(observedPutBPS, maxDailyBPS)
	burst := maxUint64(sustained*2, sustained)
	backendWrite := maxUint64(observedPutBPS, sustained)
	backendRead := maxUint64(observedBytesPerSecond(report.Backend, "get"), sustained)
	requestBudget := proposeRequestBudget(report.Backend, opts.Duration)
	maxObjectBytes := minUint64(maxUint64(maxDocumentSize(opts.DocumentSizesBytes)*4, 64*1024*1024), postAdmission/2)
	if maxObjectBytes == 0 {
		maxObjectBytes = 1
	}
	return ProposedCapacityProfile{
		ProfileID: opts.ProfileID + "-advisory",
		Provider:  string(backend.ProviderS3Like),
		Mode:      string(backend.ProfileModeProduction),
		Advisory:  true,
		ByteBudget: ProposedByteBudget{
			SustainedIngressBytesPerSecond: sustained,
			BurstIngressBytesPerSecond:     burst,
			BackendWriteBytesPerSecond:     backendWrite,
			BackendReadBytesPerSecond:      backendRead,
			LocalUsableBytes:               localUsable,
			MaxObjectBytes:                 maxObjectBytes,
		},
		RequestBudget: requestBudget,
		LaneBudgets:   proposeLaneBudgets(backendWrite, requestBudget.PutRequestsPerSecond),
		RampPolicy: ProposedRampPolicy{
			InitialRequestsPerSecond: maxUint64(1, requestBudget.PutRequestsPerSecond/4),
			MaxRequestsPerSecond:     maxUint64(requestBudget.PutRequestsPerSecond, 1),
			StepRequestsPerSecond:    maxUint64(1, requestBudget.PutRequestsPerSecond/4),
			StepIntervalMillis:       durationMillis(time.Second),
		},
		CircuitBreakers: ProposedCircuitBreaker{
			ConsecutiveFailures: 3,
			ErrorRatePermille:   100,
			MinimumSamples:      minimumSamples,
			OpenIntervalMillis:  durationMillis(30 * time.Second),
			HalfOpenRequests:    2,
		},
		DiskGuardBands: guard,
		Runway: ProposedRunway{
			MinimumSafeRunwayMillis: durationMillis(24 * time.Hour),
			EstimatedBytesPerDay:    sustained * uint64((24*time.Hour)/time.Second),
		},
	}
}

func proposeGuardBands(localUsable uint64) ProposedDiskGuardBands {
	admission := maxUint64(1, localUsable/4)
	warning := maxUint64(1, localUsable/8)
	critical := maxUint64(1, localUsable/16)
	failClosed := maxUint64(1, localUsable/32)
	return ProposedDiskGuardBands{
		AdmissionMinFreeBytes:  admission,
		WarningMinFreeBytes:    warning,
		CriticalMinFreeBytes:   critical,
		FailClosedMinFreeBytes: failClosed,
	}
}

func proposeRequestBudget(samples []RequestSample, duration time.Duration) ProposedRequestBudget {
	put := successfulRequestsPerSecond(samples, "put", duration)
	get := successfulRequestsPerSecond(samples, "get", duration)
	head := successfulRequestsPerSecond(samples, "head", duration)
	return ProposedRequestBudget{
		PutRequestsPerSecond:    put,
		GetRequestsPerSecond:    get,
		HeadRequestsPerSecond:   head,
		DeleteRequestsPerSecond: maxUint64(1, put/4),
	}
}

func proposeLaneBudgets(bytesPerSecond, requestsPerSecond uint64) []ProposedLaneBudget {
	return []ProposedLaneBudget{
		proposedLane(backend.LaneIngestUpload, bytesPerSecond, requestsPerSecond, 60, 1, 2),
		proposedLane(backend.LaneRepair, bytesPerSecond, requestsPerSecond, 20, 1, 2),
		proposedLane(backend.LaneRestore, bytesPerSecond, requestsPerSecond, 20, 1, 2),
		proposedLane(backend.LaneDRCopy, bytesPerSecond, requestsPerSecond, 10, 1, 1),
		proposedLane(backend.LaneRewrap, bytesPerSecond, requestsPerSecond, 10, 1, 1),
		proposedLane(backend.LaneScrub, bytesPerSecond, requestsPerSecond, 5, 1, 1),
	}
}

func proposedLane(
	lane backend.Lane,
	bytesPerSecond uint64,
	requestsPerSecond uint64,
	percent uint64,
	reservedWorkers uint32,
	maxWorkers uint32,
) ProposedLaneBudget {
	return ProposedLaneBudget{
		Lane:              string(lane),
		ReservedWorkers:   reservedWorkers,
		MaxWorkers:        maxWorkers,
		BytesPerSecond:    maxUint64(1, bytesPerSecond*percent/100),
		RequestsPerSecond: maxUint64(1, requestsPerSecond*percent/100),
	}
}

func safeMinimumSamples(value int) uint32 {
	converted, err := safeconv.IntToUint32("capacity sample minimum samples", value)
	if err != nil || converted < 3 {
		return 3
	}
	return converted
}

func observedBytesPerSecond(samples []RequestSample, operation string) uint64 {
	var bytesTotal uint64
	var millisTotal int64
	for _, sample := range samples {
		if sample.Operation != operation || sample.ErrorClass != "" {
			continue
		}
		bytesTotal += sample.Bytes
		millisTotal += maxInt64(sample.LatencyMillis, 1)
	}
	if bytesTotal == 0 || millisTotal == 0 {
		return 0
	}
	return maxUint64(1, bytesTotal*1000/uint64(millisTotal))
}

func successfulRequestsPerSecond(samples []RequestSample, operation string, duration time.Duration) uint64 {
	var count uint64
	for _, sample := range samples {
		if sample.Operation == operation && sample.ErrorClass == "" {
			count++
		}
	}
	if count == 0 {
		return 1
	}
	seconds, err := safeconv.Int64ToUint64("capacity sample duration seconds", int64(duration/time.Second))
	if err != nil {
		return 1
	}
	if seconds == 0 {
		seconds = 1
	}
	rps := count / seconds
	if count%seconds != 0 {
		rps++
	}
	return maxUint64(1, rps)
}

func collectErrorClasses(report Report) []string {
	classes := make(map[string]bool)
	if report.LocalDisk.ErrorClass != "" {
		classes[report.LocalDisk.ErrorClass] = true
	}
	if report.Admin.ErrorClass != "" {
		classes[report.Admin.ErrorClass] = true
	}
	for _, sample := range append(report.Backend, report.OpenBao...) {
		if sample.ErrorClass != "" {
			classes[sample.ErrorClass] = true
		}
	}
	return sortedKeys(classes)
}

func collectRequestIDs(report Report) []string {
	ids := make(map[string]bool)
	for _, sample := range append(report.Backend, report.OpenBao...) {
		if sample.RedactedRequestID != "" {
			ids[sample.RedactedRequestID] = true
		}
	}
	return sortedKeys(ids)
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func classifyGRPCError(err error) backend.ErrorClass {
	switch status.Code(err) {
	case codes.OK:
		return ""
	case codes.ResourceExhausted:
		return backend.ErrorClassThrottled
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return backend.ErrorClassTransient
	case codes.Unauthenticated, codes.PermissionDenied:
		return backend.ErrorClassAuth
	case codes.NotFound:
		return backend.ErrorClassNotFound
	case codes.AlreadyExists, codes.Aborted:
		return backend.ErrorClassConflict
	case codes.DataLoss:
		return backend.ErrorClassCorrupt
	default:
		return backend.ErrorClassPermanent
	}
}

func classifyHTTPStatus(statusCode int) backend.ErrorClass {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return ""
	case statusCode == http.StatusTooManyRequests:
		return backend.ErrorClassThrottled
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return backend.ErrorClassAuth
	case statusCode == http.StatusNotFound:
		return backend.ErrorClassNotFound
	case statusCode == http.StatusConflict:
		return backend.ErrorClassConflict
	case statusCode >= 500:
		return backend.ErrorClassTransient
	default:
		return backend.ErrorClassPermanent
	}
}

func classifyProbeError(err error) backend.ErrorClass {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return backend.ErrorClassTransient
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return backend.ErrorClassTransient
	}
	return backend.ClassifyError(err)
}

func validateHTTPURL(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("capacity sample %s is invalid: %w", name, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("capacity sample %s must use http or https", name)
	}
	if parsed.Host == "" {
		return fmt.Errorf("capacity sample %s requires a host", name)
	}
	return nil
}

func backendObjectURL(baseURL, key string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = path.Join(parsed.Path, key)
	return parsed.String(), nil
}

func openBaoURL(baseURL, mount, operation, keyName string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	parsed.Path = path.Join(parsed.Path, "v1", mount, operation, keyName)
	return parsed.String()
}

func splitOpenBaoTransitKeyPath(value string) (string, string, error) {
	cleaned := strings.Trim(strings.TrimSpace(value), "/")
	if cleaned == "" {
		return "", "", errors.New("capacity sample requires openbao transit key path")
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) >= 3 && parts[1] == "keys" {
		keyName := strings.Join(parts[2:], "/")
		if keyName == "" {
			return "", "", errors.New("capacity sample openbao transit key path requires a key name")
		}
		return parts[0], keyName, nil
	}
	if len(parts) >= 2 {
		return parts[0], strings.Join(parts[1:], "/"), nil
	}
	return "transit", cleaned, nil
}

func sampleBytes(size uint64, seed byte) []byte {
	body := make([]byte, size)
	for i := range body {
		body[i] = seed + byte(i%251)
	}
	return body
}

func redactedRequestID(header http.Header) string {
	for _, key := range []string{"X-Amz-Request-Id", "X-Amz-Id-2", "X-Request-Id", "X-Vault-Request", "X-Bao-Request"} {
		value := strings.TrimSpace(header.Get(key))
		if value == "" {
			continue
		}
		sum := sha256.Sum256([]byte(value))
		return "sha256:" + hex.EncodeToString(sum[:])[:16]
	}
	return ""
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "-")
	if value == "" {
		return "profile"
	}
	return value
}

func defaultText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func durationMillis(duration time.Duration) int64 {
	millis := duration.Milliseconds()
	if millis == 0 && duration > 0 {
		return 1
	}
	return millis
}

func maxDocumentSize(sizes []uint64) uint64 {
	var largest uint64
	for _, size := range sizes {
		if size > largest {
			largest = size
		}
	}
	return largest
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
