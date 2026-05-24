package capacitysample

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/petabytecl/scrap/internal/backend"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestValidateOptionsBoundsWorkload(t *testing.T) {
	valid := validOptions(t, nil, nil)
	tests := map[string]struct {
		mutate func(*Options)
		want   string
	}{
		"missing profile": {
			mutate: func(opts *Options) { opts.ProfileID = "" },
			want:   "profile_id",
		},
		"too many samples": {
			mutate: func(opts *Options) { opts.SampleCount = MaxSampleCount + 1 },
			want:   "--samples",
		},
		"oversized document": {
			mutate: func(opts *Options) { opts.DocumentSizesBytes = []uint64{MaxDocumentSizeBytes + 1} },
			want:   "--document-size",
		},
		"unbounded duration": {
			mutate: func(opts *Options) { opts.Duration = MaxDuration + time.Second },
			want:   "--duration",
		},
		"invalid backend url": {
			mutate: func(opts *Options) { opts.BackendURL = "file:///tmp/backend" },
			want:   "backend url",
		},
		"invalid openbao key": {
			mutate: func(opts *Options) { opts.OpenBaoTransitKeyPath = "/" },
			want:   "transit key path",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			opts := valid
			opts.DocumentSizesBytes = append([]uint64(nil), valid.DocumentSizesBytes...)
			test.mutate(&opts)
			if _, err := ValidateOptions(opts); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOptions error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestRunEmitsAdvisoryReportAndSamplesTargets(t *testing.T) {
	backendServer := newBackendProbeServer(t, http.StatusOK)
	openbaoServer := newOpenBaoProbeServer(t)
	opts := validOptions(t, backendServer, openbaoServer)
	opts.SampleCount = 2
	opts.DocumentSizesBytes = []uint64{128, 256}

	admin := &fakeAdmin{runway: &adminv1.CapacityRunway{
		CapacityProfileId:    "scrap-prod-v1",
		UsableBytesRemaining: 8 * 1024 * 1024 * 1024,
		EstimatedBytesPerDay: 128 * 1024 * 1024,
		RunwayDays:           30,
		Warnings: []*adminv1.OperationWarning{
			{Code: "SCRAP_LOCAL_CAPACITY", Message: "local sample is advisory"},
		},
	}}
	report, err := Run(context.Background(), opts, admin)
	testutil.RequireNoErrorf(t, err, "run capacity sample")

	requireCapacitySampleReport(t, report, admin.profileID)
	testutil.RequireNoErrorf(t, report.ProposedProfile.Validate(), "proposed profile rejected")

	encoded, err := json.Marshal(report)
	testutil.RequireNoErrorf(t, err, "marshal report")
	requireCapacitySampleJSON(t, string(encoded))
}

func requireCapacitySampleReport(t *testing.T, report Report, profileID string) {
	t.Helper()
	testutil.RequireEqualf(t, profileID, "scrap-prod-v1", "admin profile id")
	testutil.RequireEqualf(t, report.ReportKind, ReportKind, "report kind")
	testutil.RequireTruef(t, report.AdvisoryOnly, "report advisory_only = false")
	testutil.RequireTruef(t, report.DoesNotSatisfyProductionReadinessGates, "report readiness gate disclaimer = false")
	testutil.RequireTruef(t, report.Workload.Bounded, "workload bounded = false")
	testutil.RequireTruef(t, report.Workload.ActiveProbe, "workload active_probe = false")
	testutil.RequireTruef(t, report.Workload.NonProductionOnly, "workload non_production_only = false")
	testutil.RequireEqualf(t, len(report.Backend), 6, "backend sample count")
	testutil.RequireEqualf(t, len(report.OpenBao), 6, "openbao sample count")
	testutil.RequireTruef(t, len(report.RedactedRequestIDs) > 0, "report did not record redacted request ids")
}

func requireCapacitySampleJSON(t *testing.T, output string) {
	t.Helper()
	for _, forbidden := range []string{"local-root", "ciphertext", "plaintext"} {
		testutil.RequireFalsef(t, strings.Contains(output, forbidden), "report leaked %q: %s", forbidden, output)
	}
	for _, want := range []string{
		`"advisory_only":true`,
		`"does_not_satisfy_production_readiness_gates":true`,
		`"proposed_capacity_profile"`,
		`"backend_request_latency_samples"`,
	} {
		testutil.RequireTruef(t, strings.Contains(output, want), "report JSON missing %s: %s", want, output)
	}
}

func TestRunRecordsBackendErrorClassesWithoutWritingProductionState(t *testing.T) {
	backendServer := newBackendProbeServer(t, http.StatusTooManyRequests)
	openbaoServer := newOpenBaoProbeServer(t)
	opts := validOptions(t, backendServer, openbaoServer)

	report, err := Run(context.Background(), opts, &fakeAdmin{runway: &adminv1.CapacityRunway{CapacityProfileId: "scrap-prod-v1"}})
	testutil.RequireNoErrorf(t, err, "run capacity sample")
	if !contains(report.ErrorClasses, string(backend.ErrorClassThrottled)) {
		t.Fatalf("error classes = %#v, want throttled", report.ErrorClasses)
	}
	for _, limit := range report.EvidenceLimits {
		if strings.Contains(limit, "does not write production config") {
			return
		}
	}
	t.Fatalf("evidence limits = %#v, want advisory-only production write boundary", report.EvidenceLimits)
}

func TestBackendObjectURLKeepsCapacitySamplePrefix(t *testing.T) {
	profile := sanitizePathPart("../prod/../../escape")
	target, err := backendObjectURL("http://backend.local/scrap-local", "capacity-sample/"+profile+"/0")
	testutil.RequireNoErrorf(t, err, "backend object URL")
	if strings.Contains(target, "..") ||
		strings.Contains(profile, ".") ||
		strings.Contains(profile, "/") ||
		!strings.Contains(target, "/scrap-local/capacity-sample/") {
		t.Fatalf("target URL = %s, want sanitized capacity-sample prefix", target)
	}
}

func TestStatfsBytesSaturatesOnOverflow(t *testing.T) {
	if got := statfsBytes(math.MaxUint64, 2); got != math.MaxUint64 {
		t.Fatalf("statfs bytes = %d, want saturated max", got)
	}
	if got := statfsBytes(10, -1); got != 0 {
		t.Fatalf("statfs bytes = %d, want 0 for invalid block size", got)
	}
}

func validOptions(t *testing.T, backendServer, openbaoServer *httptest.Server) Options {
	t.Helper()
	backendURL := "http://127.0.0.1/backend"
	openbaoAddress := "http://127.0.0.1:8200"
	if backendServer != nil {
		backendURL = backendServer.URL + "/scrap-local"
	}
	if openbaoServer != nil {
		openbaoAddress = openbaoServer.URL
	}
	return Options{
		ReleaseSHA:            "abc123",
		DirtyTree:             "clean",
		ProfileID:             "scrap-prod-v1",
		EnvironmentID:         "local-kind",
		SampleCount:           1,
		DocumentSizesBytes:    []uint64{64},
		Duration:              time.Second,
		LocalDiskPath:         t.TempDir(),
		BackendURL:            backendURL,
		OpenBaoAddress:        openbaoAddress,
		OpenBaoToken:          "local-root",
		OpenBaoTransitKeyPath: "transit/keys/scrap-backend",
		Now: func() time.Time {
			return time.Unix(100, 0).UTC()
		},
	}
}

func newBackendProbeServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amz-Request-Id", fmt.Sprintf("backend-%s-%s", r.Method, r.URL.Path))
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Errorf("missing SigV4 authorization header")
		}
		w.WriteHeader(statusCode)
		if statusCode < 200 || statusCode >= 300 {
			return
		}
		switch r.Method {
		case http.MethodPut:
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Errorf("read put body: %v", err)
			}
		case http.MethodGet:
			_, _ = w.Write([]byte("sample"))
		case http.MethodHead:
		default:
			t.Errorf("unexpected backend method %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newOpenBaoProbeServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "local-root" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Vault-Request", "openbao-"+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/datakey/plaintext/"):
			writeJSON(t, w, map[string]any{"data": map[string]string{"ciphertext": "vault:v1:wrapped"}})
		case strings.Contains(r.URL.Path, "/decrypt/"):
			writeJSON(t, w, map[string]any{"data": map[string]string{"plain": base64.StdEncoding.EncodeToString([]byte("dek"))}})
		case strings.Contains(r.URL.Path, "/rewrap/"):
			writeJSON(t, w, map[string]any{"data": map[string]string{"rewrapped": "vault:v2:wrapped"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	testutil.RequireNoErrorf(t, json.NewEncoder(w).Encode(value), "write JSON")
}

type fakeAdmin struct {
	runway    *adminv1.CapacityRunway
	profileID string
	err       error
}

func (f *fakeAdmin) GetCapacityRunway(_ context.Context, req *adminv1.GetCapacityRunwayRequest, _ ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error) {
	f.profileID = req.GetCapacityProfileId()
	if f.err != nil {
		return nil, f.err
	}
	return &adminv1.GetCapacityRunwayResponse{Runway: f.runway}, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
