package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/rewrap"
	"github.com/petabytecl/scrap/internal/security"
)

const securityEvidenceTimeout = 2 * time.Minute

type securityEvidenceReport struct {
	SecurityMode              string `json:"security_mode"`
	ProductionReadinessStatus string `json:"production_readiness_status"`
	ProductionReadinessReason string `json:"production_readiness_reason,omitempty"`
	AuthorizationStatus       string `json:"authorization_status,omitempty"`
	PublicUnauthorizedDenied  bool   `json:"public_unauthorized_denied"`
	PeerUnauthorizedDenied    bool   `json:"peer_unauthorized_denied"`
	AdminUnauthorizedDenied   bool   `json:"admin_unauthorized_denied"`
	AuditSamplesRecorded      bool   `json:"audit_samples_recorded"`
	EncryptedWriteReadOK      bool   `json:"encrypted_write_read_ok"`
	EncryptedBackendUploadOK  bool   `json:"encrypted_backend_upload_ok"`
	EncryptedRestoreOK        bool   `json:"encrypted_restore_ok"`
	RewrapOK                  bool   `json:"rewrap_ok"`
	Phase5EntryBlocked        bool   `json:"phase5_entry_blocked"`
}

type securityHealth struct {
	Status                    string `json:"status"`
	SecurityMode              string `json:"security_mode"`
	ProductionReadinessStatus string `json:"production_readiness_status"`
	ProductionReadinessReason string `json:"production_readiness_reason"`
	AuthorizationStatus       string `json:"authorization_status"`
}

func TestE2EProdlikeSecurityEncryptionEvidence(t *testing.T) {
	requireE2E(t)
	if !e2eTLSConfigured() {
		t.Skip("prod-like security evidence requires SCRAP_E2E_TLS_*")
	}
	restoreLocalStack(t)

	health := fetchRequiredSecurityHealth(t)
	endpoint, stopLocalStack := startLocalStackPortForward(t)
	defer stopLocalStack()
	s3Client := newE2ES3Client(t, "http://"+endpoint)
	ensureE2ES3Bucket(t, s3Client)
	before := listBackendObjects(t, s3Client)

	client := connect(t)
	txID := uniqueName("tx-e2e-security")
	docName := writeUploadBlockE2E(t, client, txID, "encrypted", 'e')
	content := bytes.Repeat([]byte{'e'}, uploadBlockPayloadLen)
	readBack := readDocE2E(t, client, txID, docName)

	pair := waitNewBackendPair(t, s3Client, before, securityEvidenceTimeout)
	verifyS3ObjectMD5(t, s3Client, pair.blk)
	verifyS3ObjectMD5(t, s3Client, pair.idx)

	leader := findLeaderPod(t, txID, docName)
	rewrapResult := postRewrapDocument(t, leader, txID, docName)
	restoreOK := encryptedRestoreOK(t, leader, pair.blk.key, txID, docName, content)

	report := buildSecurityEvidenceReport(t, health, leader, pair, readBack, content, restoreOK, rewrapResult)
	writeSecurityEvidenceReport(t, report)
	assertSecurityEvidenceReportComplete(t, report, rewrapResult)
}

func fetchRequiredSecurityHealth(t *testing.T) securityHealth {
	t.Helper()
	health := fetchSecurityHealth(t)
	if health.SecurityMode != string(security.ModeTest) {
		t.Fatalf("security_mode = %q, want test", health.SecurityMode)
	}
	if health.ProductionReadinessStatus == "" {
		t.Fatal("production_readiness_status is empty")
	}
	return health
}

func buildSecurityEvidenceReport(
	t *testing.T,
	health securityHealth,
	leader string,
	pair backendPair,
	readBack []byte,
	content []byte,
	restoreOK bool,
	rewrapResult rewrap.Result,
) securityEvidenceReport {
	t.Helper()
	return securityEvidenceReport{
		SecurityMode:              health.SecurityMode,
		ProductionReadinessStatus: health.ProductionReadinessStatus,
		ProductionReadinessReason: health.ProductionReadinessReason,
		AuthorizationStatus:       health.AuthorizationStatus,
		PublicUnauthorizedDenied:  publicUnauthorizedDenied(t),
		PeerUnauthorizedDenied:    peerUnauthorizedDenied(t),
		AdminUnauthorizedDenied:   adminUnauthorizedDenied(t, leader),
		AuditSamplesRecorded:      auditSamplesRecorded(t, leader),
		EncryptedWriteReadOK:      bytes.Equal(readBack, content),
		EncryptedBackendUploadOK:  pair.blk.key != "" && pair.idx.key != "",
		EncryptedRestoreOK:        restoreOK,
		RewrapOK:                  rewrapResult.Status == rewrap.StatusOK,
		Phase5EntryBlocked:        health.ProductionReadinessStatus != string(security.ReadinessStatusReady),
	}
}

func assertSecurityEvidenceReportComplete(t *testing.T, report securityEvidenceReport, rewrapResult rewrap.Result) {
	t.Helper()
	if !securityDenialsRecorded(report) {
		t.Fatalf("unauthorized denial report = %+v", report)
	}
	if !report.AuditSamplesRecorded {
		t.Fatalf("audit sample report = %+v", report)
	}
	if !securityEncryptionRecorded(report) {
		t.Fatalf("encryption evidence report = %+v, rewrap=%+v", report, rewrapResult)
	}
}

func securityDenialsRecorded(report securityEvidenceReport) bool {
	return report.PublicUnauthorizedDenied && report.PeerUnauthorizedDenied && report.AdminUnauthorizedDenied
}

func securityEncryptionRecorded(report securityEvidenceReport) bool {
	return report.EncryptedWriteReadOK && report.EncryptedBackendUploadOK && report.EncryptedRestoreOK && report.RewrapOK
}

func fetchSecurityHealth(t *testing.T) securityHealth {
	t.Helper()
	addr, stop := startPodPortForward(t, podNames(t)[0], 9100)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e2eAdminURL(addr, "/healthz"), nil)
	if err != nil {
		t.Fatalf("new health request: %v", err)
	}
	resp, err := e2eHTTPClient(t).Do(req)
	if err != nil {
		t.Fatalf("GET healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("healthz status: got %d, want 200: %s", resp.StatusCode, string(body))
	}
	var health securityHealth
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	return health
}

func publicUnauthorizedDenied(t *testing.T) bool {
	t.Helper()
	conn, err := grpc.NewClient(scrapAddr(), grpc.WithTransportCredentials(e2eGRPCCredentialsWithoutCertificate(t)))
	if err != nil {
		t.Fatalf("Dial unauthorized public client: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = scrapv1.NewDocumentServiceClient(conn).HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "unauthorized",
		DocumentName:  "probe.xml",
	})
	return deniedBeforeHandler(err)
}

func peerUnauthorizedDenied(t *testing.T) bool {
	t.Helper()
	pod := podNames(t)[0]
	addr, stop := startPodPortForward(t, pod, 9091)
	defer stop()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(e2eGRPCCredentialsWithoutCertificate(t)))
	if err != nil {
		t.Fatalf("Dial unauthorized peer client: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = scrapv1.NewPeerServiceClient(conn).ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{})
	return deniedBeforeHandler(err)
}

func adminUnauthorizedDenied(t *testing.T, pod string) bool {
	t.Helper()
	addr, stop := startPodPortForward(t, pod, 9100)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2eAdminURL(addr, "/admin/rewrap/document"), bytes.NewReader([]byte(`{"transaction_id":"unauthorized","document_name":"probe.xml"}`)))
	if err != nil {
		t.Fatalf("new unauthorized admin request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e2eHTTPClientWithoutCertificate(t).Do(req)
	if err != nil {
		return true
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
}

func deniedBeforeHandler(err error) bool {
	if err == nil {
		return false
	}
	code := status.Code(err)
	return code == codes.Unauthenticated || code == codes.PermissionDenied || code == codes.Unavailable
}

func postRewrapDocument(t *testing.T, pod, txID, docName string) rewrap.Result {
	t.Helper()
	addr, stop := startPodPortForward(t, pod, 9100)
	defer stop()
	body := bytes.NewReader([]byte(`{"transaction_id":` + quotedJSON(txID) + `,"document_name":` + quotedJSON(docName) + `,"key_version":1,"reason":"e2e_security_evidence"}`))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2eAdminURL(addr, "/admin/rewrap/document"), body)
	if err != nil {
		t.Fatalf("new rewrap request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e2eHTTPClient(t).Do(req)
	if err != nil {
		t.Fatalf("POST rewrap: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read rewrap response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rewrap status: got %d, want 200: %s", resp.StatusCode, string(data))
	}
	var result rewrap.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode rewrap response: %v\n%s", err, data)
	}
	return result
}

func encryptedRestoreOK(t *testing.T, leaderPod, freshBackendKey, txID, docName string, want []byte) bool {
	t.Helper()
	for _, pod := range podNames(t) {
		if pod == leaderPod {
			continue
		}
		plan, selectedFreshBlock := postSecurityEvictionPlan(t, pod, freshBackendKey)
		if !selectedFreshBlock {
			continue
		}
		result := postSecurityEvictionApply(t, pod, plan.PlanID)
		if result.EvictedBlocks == 0 || result.FailedBlocks != 0 || result.ValidatedBlocks == 0 || result.ValidationFailedBlocks != 0 {
			return false
		}
		addr, stop := startPodPortForward(t, pod, 9090)
		readBack := readDocE2E(t, newDocumentClientAt(t, addr), txID, docName)
		stop()
		return bytes.Equal(readBack, want)
	}
	t.Fatalf("no non-leader Member selected fresh backend Block %s for restore evidence", freshBackendKey)
	return false
}

func postSecurityEvictionPlan(t *testing.T, pod, freshBackendKey string) (eviction.Plan, bool) {
	t.Helper()
	addr, stop := startPodPortForward(t, pod, 9100)
	defer stop()
	maxBlocks := 10
	body, err := json.Marshal(eviction.PlanRequest{
		MemberHostname: pod,
		MaxBlocks:      &maxBlocks,
		Reason:         eviction.ReasonEvidenceRun,
	})
	if err != nil {
		t.Fatalf("marshal eviction plan request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2eAdminURL(addr, "/admin/eviction/plans"), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new eviction plan request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e2eHTTPClient(t).Do(req)
	if err != nil {
		t.Fatalf("POST eviction plan: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read eviction plan response: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("eviction plan status: got %d, want 201: %s", resp.StatusCode, string(data))
	}
	var plan eviction.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatalf("decode eviction plan response: %v\n%s", err, data)
	}
	return plan, planSelectsBackendKey(plan, freshBackendKey)
}

func planSelectsBackendKey(plan eviction.Plan, backendKey string) bool {
	for _, block := range plan.Selected {
		if block.BackendKey == backendKey {
			return true
		}
	}
	return false
}

func postSecurityEvictionApply(t *testing.T, pod, planID string) eviction.ApplyResult {
	t.Helper()
	addr, stop := startPodPortForward(t, pod, 9100)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e2eAdminURL(addr, "/admin/eviction/plans/"+planID+"/apply"), bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("new eviction apply request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e2eHTTPClient(t).Do(req)
	if err != nil {
		t.Fatalf("POST eviction apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read eviction apply response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("eviction apply status: got %d, want 200: %s", resp.StatusCode, string(data))
	}
	var result eviction.ApplyResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode eviction apply response: %v\n%s", err, data)
	}
	return result
}

func auditSamplesRecorded(t *testing.T, pod string) bool {
	t.Helper()
	out := runKubectl(t, kubectlCommandWait, "-n", namespace(), "logs", pod, "-c", "scrapd", "--tail=500")
	return strings.Contains(out, "security audit event") &&
		strings.Contains(out, `"audit.surface":"admin"`) &&
		strings.Contains(out, `"audit.result":"allowed"`)
}

func quotedJSON(value string) string {
	out, _ := json.Marshal(value)
	return string(out)
}

func writeSecurityEvidenceReport(t *testing.T, report securityEvidenceReport) {
	t.Helper()
	path := os.Getenv("SCRAP_E2E_SECURITY_REPORT")
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { //nolint:gosec // E2E harness selects the test-owned report path.
		t.Fatalf("create security report dir: %v", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal security evidence report: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil { //nolint:gosec // E2E harness selects the test-owned report path.
		t.Fatalf("write security evidence report: %v", err)
	}
}
