package scrapctl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/quarantine"
)

const (
	quarantineRawTransaction = "tx-visible-123"
	quarantineRawDocument    = "invoice-2026.xml"
)

func TestQuarantineListCallsAdminHTTPAndRedactsOutput(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if req.URL.Path != "/admin/quarantine/documents" {
			t.Fatalf("path = %s, want /admin/quarantine/documents", req.URL.Path)
		}
		if got := req.URL.Query().Get("transaction_id"); got != quarantineRawTransaction {
			t.Fatalf("transaction_id = %q, want %q", got, quarantineRawTransaction)
		}
		if got := req.URL.Query().Get("limit"); got != "1" {
			t.Fatalf("limit = %q, want 1", got)
		}
		return jsonResponse(t, http.StatusOK, quarantineListFixture())
	})}

	var out bytes.Buffer
	err := Run([]string{
		"quarantine", "list",
		"--admin-url=http://admin.local",
		"--transaction-id=" + quarantineRawTransaction,
		"--limit=1",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("quarantine list: %v\n%s", err, out.String())
	}

	assertTextContains(t, out.String(),
		"Content Quarantine Documents: 1",
		"Transaction: redacted:",
		"Document: redacted:",
		"Shard: 7",
		"Block: 42",
		"lifecycle=active",
	)
	assertTextNotContains(t, out.String(), quarantineRawTransaction, quarantineRawDocument, "transaction_id", "document_name")
}

func TestQuarantineInspectJSONRedactsRawIdentity(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if req.URL.Path != "/admin/quarantine/document" {
			t.Fatalf("path = %s, want /admin/quarantine/document", req.URL.Path)
		}
		if got := req.URL.Query().Get("transaction_id"); got != quarantineRawTransaction {
			t.Fatalf("transaction_id = %q, want %q", got, quarantineRawTransaction)
		}
		if got := req.URL.Query().Get("document_name"); got != quarantineRawDocument {
			t.Fatalf("document_name = %q, want %q", got, quarantineRawDocument)
		}
		return jsonResponse(t, http.StatusOK, quarantineRecordFixture(quarantine.LifecycleActive))
	})}

	var out bytes.Buffer
	err := Run([]string{
		"quarantine", "inspect",
		"--admin-url=http://admin.local",
		"--transaction-id=" + quarantineRawTransaction,
		"--document-name=" + quarantineRawDocument,
		"--output=json",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("quarantine inspect: %v\n%s", err, out.String())
	}

	assertTextContains(t, out.String(), `"transaction":"redacted:`, `"document":"redacted:`, `"lifecycle":"active"`)
	assertTextNotContains(t, out.String(), quarantineRawTransaction, quarantineRawDocument, `"transaction_id"`, `"document_name"`)
}

func TestQuarantineConfirmPostsIdentityAndReportsCommittedOutcome(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/admin/quarantine/confirm" {
			t.Fatalf("path = %s, want /admin/quarantine/confirm", req.URL.Path)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var got quarantine.Identity
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.TransactionID != quarantineRawTransaction || got.DocumentName != quarantineRawDocument {
			t.Fatalf("identity = %+v", got)
		}
		result := quarantine.Result{
			Status:   quarantine.StatusOK,
			Reason:   quarantine.ReasonOK,
			Changed:  true,
			Document: ptrRecord(quarantineRecordFixture(quarantine.LifecycleConfirmed)),
		}
		return jsonResponse(t, http.StatusOK, result)
	})}

	var out bytes.Buffer
	err := Run([]string{
		"quarantine", "confirm",
		"--admin-url=http://admin.local",
		"--transaction-id=" + quarantineRawTransaction,
		"--document-name=" + quarantineRawDocument,
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("quarantine confirm: %v\n%s", err, out.String())
	}

	assertTextContains(t, out.String(), "status: ok", "reason: ok", "changed: true", "lifecycle=confirmed", "Raft: committed")
	assertTextNotContains(t, out.String(), quarantineRawTransaction, quarantineRawDocument, "transaction_id", "document_name")
}

func TestQuarantineReleaseReportsTypedHTTPFailureWithoutLeak(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/admin/quarantine/release" {
			t.Fatalf("path = %s, want /admin/quarantine/release", req.URL.Path)
		}
		return jsonResponse(t, http.StatusPreconditionFailed, quarantine.Result{
			Status:  quarantine.StatusFailed,
			Reason:  quarantine.ReasonDataLoss,
			Changed: false,
		})
	})}

	err := Run([]string{
		"quarantine", "release",
		"--admin-url=http://admin.local",
		"--transaction-id=" + quarantineRawTransaction,
		"--document-name=" + quarantineRawDocument,
	}, io.Discard, io.Discard, Deps{HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "quarantine release failed: status=412 reason=data_loss") {
		t.Fatalf("error = %v, want typed data_loss failure", err)
	}
	assertTextNotContains(t, err.Error(), quarantineRawTransaction, quarantineRawDocument, "transaction_id", "document_name")
}

func TestQuarantineHTTPFailureSanitizesRawBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := "dependency failed " + "transaction_" + "id=tx-visible-123 " +
			"document_" + "name=invoice-2026.xml trace_" + "id=abc"
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	err := Run([]string{
		"quarantine", "release",
		"--admin-url=http://admin.local",
		"--transaction-id=" + quarantineRawTransaction,
		"--document-name=" + quarantineRawDocument,
	}, io.Discard, io.Discard, Deps{HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "sensitive detail redacted") {
		t.Fatalf("error = %v, want sanitized failure detail", err)
	}
	assertTextNotContains(t, err.Error(), quarantineRawTransaction, quarantineRawDocument, "transaction_id", "document_name", "trace_id")
}

func TestQuarantineEvidenceWritesReportAndRedactionChecks(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if req.URL.Path != "/admin/quarantine/documents" {
			t.Fatalf("path = %s, want /admin/quarantine/documents", req.URL.Path)
		}
		return jsonResponse(t, http.StatusOK, quarantineListFixture())
	})}
	evidencePath := filepath.Join(t.TempDir(), "quarantine", "evidence.json")

	var out bytes.Buffer
	err := Run([]string{
		"quarantine", "evidence",
		"--admin-url=http://admin.local",
		"--evidence-path=" + evidencePath,
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("quarantine evidence: %v\n%s", err, out.String())
	}

	if strings.TrimSpace(out.String()) != evidencePath {
		t.Fatalf("stdout = %q, want evidence path", out.String())
	}
	assertQuarantineEvidenceFileMode(t, evidencePath)
	data := string(mustReadFile(t, evidencePath))
	assertTextContains(t, data, `"status": "ok"`, `"command": "scrapctl quarantine evidence"`, `"artifact_path": "`+evidencePath+`"`, `"redaction_checks"`)
	assertTextNotContains(t, out.String(), quarantineRawTransaction, quarantineRawDocument)
	assertTextNotContains(t, data, quarantineRawTransaction, quarantineRawDocument, `"transaction_id"`, `"document_name"`)
	assertQuarantineEvidenceReport(t, data)
}

func assertQuarantineEvidenceFileMode(t *testing.T, evidencePath string) {
	t.Helper()

	info, err := os.Stat(evidencePath)
	if err != nil {
		t.Fatalf("stat evidence: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("evidence mode = %o, want 0600", got)
	}
}

func assertQuarantineEvidenceReport(t *testing.T, data string) {
	t.Helper()

	var report struct {
		RedactionChecks []struct {
			Surface string `json:"surface"`
			Status  string `json:"status"`
		} `json:"redaction_checks"`
		Routes []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	assertQuarantineReportHasRedactionPasses(t, report.RedactionChecks, "stdout", "stderr", "report")
	if len(report.Routes) != 1 || report.Routes[0].Method != http.MethodGet || report.Routes[0].Path != "/admin/quarantine/documents" {
		t.Fatalf("routes = %+v, want GET /admin/quarantine/documents", report.Routes)
	}
}

func TestQuarantineCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unsupported subcommand", args: []string{"quarantine", "bad"}, want: `unsupported quarantine command "bad"`},
		{name: "missing transaction", args: []string{"quarantine", "inspect", "--document-name=doc.xml"}, want: "transaction-id is required"},
		{name: "missing document", args: []string{"quarantine", "confirm", "--transaction-id=tx"}, want: "document-name is required"},
		{name: "bad limit", args: []string{"quarantine", "list", "--limit=0"}, want: "limit must be between 1 and 100"},
		{name: "missing evidence path", args: []string{"quarantine", "evidence"}, want: "evidence-path is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Run(tt.args, io.Discard, io.Discard, Deps{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func quarantineListFixture() struct {
	Documents []quarantine.Record `json:"documents"`
} {
	return struct {
		Documents []quarantine.Record `json:"documents"`
	}{Documents: []quarantine.Record{quarantineRecordFixture(quarantine.LifecycleActive)}}
}

func quarantineRecordFixture(lifecycle string) quarantine.Record {
	return quarantine.Record{
		ShardID:       7,
		TransactionID: quarantineRawTransaction,
		DocumentName:  quarantineRawDocument,
		BlockID:       42,
		DetectedAt:    time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC),
		ScanType:      quarantine.ScanTypeInitial,
		Reason:        quarantine.ReasonScannerDetection,
		Lifecycle:     lifecycle,
	}
}

func ptrRecord(record quarantine.Record) *quarantine.Record {
	return &record
}

func jsonResponse(t *testing.T, status int, value any) (*http.Response, error) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     make(http.Header),
	}, nil
}

func assertQuarantineReportHasRedactionPasses(t *testing.T, checks []struct {
	Surface string `json:"surface"`
	Status  string `json:"status"`
}, surfaces ...string,
) {
	t.Helper()

	seen := make(map[string]string, len(checks))
	for _, check := range checks {
		seen[check.Surface] = check.Status
	}
	for _, surface := range surfaces {
		if seen[surface] != "pass" {
			t.Fatalf("redaction check %s = %q, want pass in %+v", surface, seen[surface], checks)
		}
	}
}
