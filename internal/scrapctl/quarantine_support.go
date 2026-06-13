package scrapctl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/petabytecl/scrap/internal/quarantine"
)

var errQuarantineAdminResponseInvalid = errors.New("quarantine admin response invalid")

type quarantineDocumentOutput struct {
	Transaction string     `json:"transaction"`
	Document    string     `json:"document"`
	ShardID     uint64     `json:"shard_id"`
	BlockID     uint64     `json:"block_id"`
	DetectedAt  time.Time  `json:"detected_at"`
	ScanType    string     `json:"scan_type"`
	Reason      string     `json:"reason"`
	Lifecycle   string     `json:"lifecycle"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

type quarantineDocumentsOutput struct {
	Documents []quarantineDocumentOutput `json:"documents"`
}

type quarantineResultOutput struct {
	Status   string                    `json:"status"`
	Reason   string                    `json:"reason"`
	Changed  bool                      `json:"changed"`
	Document *quarantineDocumentOutput `json:"document,omitempty"`
}

type quarantineEvidenceReport struct {
	Status            string                     `json:"status"`
	Command           string                     `json:"command"`
	AdminURL          string                     `json:"admin_url"`
	ArtifactPath      string                     `json:"artifact_path"`
	Result            quarantineEvidenceResult   `json:"result"`
	Documents         []quarantineDocumentOutput `json:"documents,omitempty"`
	Routes            []quarantineRouteProof     `json:"routes"`
	RedactionChecks   []quarantineRedactionCheck `json:"redaction_checks"`
	ChangedBoundaries []string                   `json:"changed_boundaries"`
	SanitizedArgs     []string                   `json:"sanitized_args"`
}

type quarantineEvidenceResult struct {
	Documents int    `json:"documents"`
	Reason    string `json:"reason,omitempty"`
}

type quarantineRouteProof struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type quarantineRedactionCheck struct {
	Surface string `json:"surface"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

func decodeQuarantineResponse(body io.Reader, dst any) error {
	data, err := io.ReadAll(io.LimitReader(body, quarantineResponseMaxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > quarantineResponseMaxBytes {
		return errors.New("response body too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("response contains trailing data")
	}
	return nil
}

func quarantineHTTPError(resp *http.Response, operation string, forbiddenValues ...string) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, quarantineResponseMaxBytes))
	var result quarantine.Result
	if err := json.Unmarshal(data, &result); err == nil && result.Status != "" {
		reason := quarantineSafeResultReason(result.Reason)
		if reason == "" {
			reason = quarantine.ReasonInternalError
		}
		return fmt.Errorf("%s failed: status=%d reason=%s", operation, resp.StatusCode, reason)
	}
	detail := quarantineSafeErrorText(strings.TrimSpace(string(data)), forbiddenValues...)
	return fmt.Errorf("%s failed: status=%d: %s", operation, resp.StatusCode, detail)
}

func quarantineTransportError(operation string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s canceled", operation)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s deadline exceeded", operation)
	default:
		return fmt.Errorf("%s transport failed: sensitive detail redacted", operation)
	}
}

func failedQuarantineResultError(action string, result quarantineResultOutput) error {
	if result.Status == quarantine.StatusFailed {
		return fmt.Errorf("quarantine %s failed: reason=%s", action, quarantineSafeResultReason(result.Reason))
	}
	return nil
}

func quarantineDocumentOutputs(records []quarantine.Record) []quarantineDocumentOutput {
	out := make([]quarantineDocumentOutput, 0, len(records))
	for _, record := range records {
		out = append(out, quarantineDocumentOutputFromRecord(record))
	}
	return out
}

func quarantineDocumentOutputFromRecord(record quarantine.Record) quarantineDocumentOutput {
	return quarantineDocumentOutput{
		Transaction: redactedQuarantineIdentity(record.TransactionID),
		Document:    redactedQuarantineIdentity(record.DocumentName),
		ShardID:     record.ShardID,
		BlockID:     record.BlockID,
		DetectedAt:  record.DetectedAt,
		ScanType:    quarantineSafeScanType(record.ScanType),
		Reason:      quarantineSafeRecordReason(record.Reason),
		Lifecycle:   quarantineSafeLifecycle(record.Lifecycle),
		ConfirmedAt: record.ConfirmedAt,
	}
}

func quarantineResultOutputFromResult(result quarantine.Result) quarantineResultOutput {
	output := quarantineResultOutput{
		Status:  quarantineSafeStatus(result.Status),
		Reason:  quarantineSafeResultReason(result.Reason),
		Changed: result.Changed,
	}
	if result.Document != nil {
		document := quarantineDocumentOutputFromRecord(*result.Document)
		output.Document = &document
	}
	return output
}

func writeQuarantineDocumentsText(w io.Writer, output quarantineDocumentsOutput) error {
	if _, err := fmt.Fprintf(w, "Content Quarantine Documents: %d\n", len(output.Documents)); err != nil {
		return fmt.Errorf("write quarantine summary: %w", err)
	}
	for _, document := range output.Documents {
		if err := writeQuarantineDocumentText(w, document); err != nil {
			return err
		}
	}
	return nil
}

func writeQuarantineDocumentText(w io.Writer, document quarantineDocumentOutput) error {
	if _, err := fmt.Fprintf(
		w,
		"Document: %s Transaction: %s Shard: %d Block: %d lifecycle=%s scan_type=%s reason=%s detected_at=%s",
		document.Document,
		document.Transaction,
		document.ShardID,
		document.BlockID,
		document.Lifecycle,
		document.ScanType,
		document.Reason,
		document.DetectedAt.UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("write quarantine document: %w", err)
	}
	if document.ConfirmedAt != nil {
		if _, err := fmt.Fprintf(w, " confirmed_at=%s", document.ConfirmedAt.UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("write quarantine confirm time: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write quarantine document newline: %w", err)
	}
	return nil
}

func writeQuarantineResultText(w io.Writer, result quarantineResultOutput) error {
	if _, err := fmt.Fprintf(w, "status: %s\nreason: %s\nchanged: %t\n", result.Status, result.Reason, result.Changed); err != nil {
		return fmt.Errorf("write quarantine result: %w", err)
	}
	raftStatus := "committed"
	if result.Status == quarantine.StatusFailed {
		raftStatus = "not_committed"
	}
	if _, err := fmt.Fprintf(w, "Raft: %s\n", raftStatus); err != nil {
		return fmt.Errorf("write quarantine authority: %w", err)
	}
	if result.Document == nil {
		return nil
	}
	return writeQuarantineDocumentText(w, *result.Document)
}

func newQuarantineEvidenceReport(opts quarantineEvidenceOptions, documents []quarantineDocumentOutput) quarantineEvidenceReport {
	return quarantineEvidenceReport{
		Status:       quarantine.StatusOK,
		Command:      "scrapctl quarantine evidence",
		AdminURL:     quarantineSafeAdminURL(opts.common.adminURL),
		ArtifactPath: opts.evidencePath,
		Result: quarantineEvidenceResult{
			Documents: len(documents),
		},
		Documents: documents,
		Routes: []quarantineRouteProof{{
			Method: http.MethodGet,
			Path:   "/admin/quarantine/documents",
		}},
		ChangedBoundaries: []string{
			"internal/scrapctl",
			"cmd/scrapctl entrypoint unchanged",
			"no Shard/Backend/server/admin/public API boundary change",
		},
		SanitizedArgs: sanitizedQuarantineEvidenceArgs(opts),
	}
}

func sanitizedQuarantineEvidenceArgs(opts quarantineEvidenceOptions) []string {
	args := []string{
		"--admin-url=" + quarantineSafeAdminURL(opts.common.adminURL),
		"--evidence-path=" + opts.evidencePath,
		"--limit=" + strconv.Itoa(opts.limit),
	}
	if opts.transactionID != "" {
		args = append(args, "--transaction-id="+redactedQuarantineIdentity(opts.transactionID))
	}
	return args
}

func quarantineRedactionChecks(stdoutText, stderrText string, report quarantineEvidenceReport, forbiddenValues []string) []quarantineRedactionCheck {
	checks := []quarantineRedactionCheck{
		{Surface: "stdout", Status: "pass"},
		{Surface: "stderr", Status: "pass"},
		{Surface: "report", Status: "pass"},
	}
	if err := verifyQuarantineTextRedacted(stdoutText, forbiddenValues); err != nil {
		checks[0] = quarantineRedactionCheck{Surface: "stdout", Status: "fail", Reason: quarantineSafeErrorText(err.Error())}
	}
	if err := verifyQuarantineTextRedacted(stderrText, forbiddenValues); err != nil {
		checks[1] = quarantineRedactionCheck{Surface: "stderr", Status: "fail", Reason: quarantineSafeErrorText(err.Error())}
	}
	if err := verifyQuarantineReportRedacted(report, forbiddenValues); err != nil {
		checks[2] = quarantineRedactionCheck{Surface: "report", Status: "fail", Reason: quarantineSafeErrorText(err.Error())}
	}
	return checks
}

func verifyQuarantineEvidenceRedacted(stdoutText, stderrText string, report quarantineEvidenceReport, forbiddenValues []string) error {
	if err := verifyQuarantineTextRedacted(stdoutText, forbiddenValues); err != nil {
		return err
	}
	if err := verifyQuarantineTextRedacted(stderrText, forbiddenValues); err != nil {
		return err
	}
	return verifyQuarantineReportRedacted(report, forbiddenValues)
}

func verifyQuarantineReportRedacted(report quarantineEvidenceReport, forbiddenValues []string) error {
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal quarantine evidence for redaction check: %w", err)
	}
	return verifyQuarantineTextRedacted(string(data), forbiddenValues)
}

func verifyQuarantineTextRedacted(text string, forbiddenValues []string) error {
	for _, value := range forbiddenValues {
		if value != "" && strings.Contains(text, value) {
			return errors.New("quarantine evidence redaction failed")
		}
	}
	return nil
}

func writeQuarantineEvidence(path string, report quarantineEvidenceReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal quarantine evidence: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create quarantine evidence directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write quarantine evidence: %w", err)
	}
	tmpPath := file.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := file.Chmod(quarantineEvidenceFileMode); err != nil {
		_ = file.Close()
		return fmt.Errorf("write quarantine evidence: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write quarantine evidence: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("write quarantine evidence: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write quarantine evidence: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("write quarantine evidence: %w", err)
	}
	committed = true
	return syncDirectory(dir)
}

func quarantineForbiddenValues(records []quarantine.Record, extra ...string) []string {
	values := make([]string, 0, len(records)*quarantineIdentityValues+len(extra))
	for _, record := range records {
		values = appendNonEmptyQuarantineForbiddenValue(values, record.TransactionID)
		values = appendNonEmptyQuarantineForbiddenValue(values, record.DocumentName)
	}
	for _, value := range extra {
		values = appendNonEmptyQuarantineForbiddenValue(values, value)
	}
	return values
}

func appendNonEmptyQuarantineForbiddenValue(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	return append(values, value)
}

func redactedQuarantineIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return diagnosticTextRedacted
	}
	sum := sha256.Sum256([]byte(value))
	return "redacted:" + hex.EncodeToString(sum[:])[:12]
}

func quarantineAdminBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("admin-url must be an absolute http or https URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("admin-url must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("admin-url must not include credentials, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func quarantineSafeAdminURL(value string) string {
	base, err := quarantineAdminBaseURL(value)
	if err != nil {
		return diagnosticTextRedacted
	}
	return base
}

func quarantineOptionError(err error) error {
	message := err.Error()
	message = strings.ReplaceAll(message, "quarantine invalid request: invalid argument: ", "")
	message = strings.ReplaceAll(message, "quarantine invalid request: ", "")
	message = strings.ReplaceAll(message, "transaction_id", "transaction-id")
	message = strings.ReplaceAll(message, "document_name", "document-name")
	return errors.New(message)
}

func quarantineSafeStatus(value string) string {
	clean := strings.TrimSpace(value)
	if quarantineKnownStatus(clean) {
		return clean
	}
	return quarantine.StatusFailed
}

func quarantineKnownStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case quarantine.StatusOK, quarantine.StatusFailed:
		return true
	default:
		return false
	}
}

func quarantineSafeResultReason(value string) string {
	clean := strings.TrimSpace(value)
	if quarantineKnownResultReason(clean) {
		return clean
	}
	return quarantine.ReasonInternalError
}

func quarantineKnownResultReason(value string) bool {
	switch strings.TrimSpace(value) {
	case quarantine.ReasonOK,
		quarantine.ReasonInvalidRequest,
		quarantine.ReasonNotFound,
		quarantine.ReasonUnauthenticated,
		quarantine.ReasonPermissionDenied,
		quarantine.ReasonRateLimited,
		quarantine.ReasonMethodNotAllowed,
		quarantine.ReasonNotLeader,
		quarantine.ReasonUnavailable,
		quarantine.ReasonFailedPrecondition,
		quarantine.ReasonDataLoss,
		quarantine.ReasonAuditFailed,
		quarantine.ReasonInternalError:
		return true
	default:
		return false
	}
}

func quarantineSafeRecordReason(value string) string {
	if strings.TrimSpace(value) == quarantine.ReasonScannerDetection {
		return quarantine.ReasonScannerDetection
	}
	return diagnosticTextRedacted
}

func quarantineSafeScanType(value string) string {
	clean := strings.TrimSpace(value)
	switch clean {
	case quarantine.ScanTypeInitial, quarantine.ScanTypeRescan:
		return clean
	default:
		return diagnosticTextRedacted
	}
}

func quarantineSafeLifecycle(value string) string {
	clean := strings.TrimSpace(value)
	switch clean {
	case quarantine.LifecycleActive, quarantine.LifecycleConfirmed, quarantine.LifecycleReleased:
		return clean
	default:
		return diagnosticTextRedacted
	}
}

func validateQuarantineRecords(records []quarantine.Record) error {
	for _, record := range records {
		if err := validateQuarantineRecord(record); err != nil {
			return err
		}
	}
	return nil
}

func validateQuarantineRecord(record quarantine.Record) error {
	if err := (quarantine.Identity{
		TransactionID: record.TransactionID,
		DocumentName:  record.DocumentName,
	}).Validate(); err != nil {
		return errQuarantineAdminResponseInvalid
	}
	if !quarantineKnownRecordReason(record.Reason) {
		return errQuarantineAdminResponseInvalid
	}
	if quarantineSafeScanType(record.ScanType) == diagnosticTextRedacted {
		return errQuarantineAdminResponseInvalid
	}
	if quarantineSafeLifecycle(record.Lifecycle) == diagnosticTextRedacted {
		return errQuarantineAdminResponseInvalid
	}
	return nil
}

func validateQuarantineResult(result quarantine.Result) error {
	if !quarantineKnownStatus(result.Status) {
		return errQuarantineAdminResponseInvalid
	}
	if !quarantineKnownResultReason(result.Reason) {
		return errQuarantineAdminResponseInvalid
	}
	if result.Document != nil {
		return validateQuarantineRecord(*result.Document)
	}
	return nil
}

func quarantineKnownRecordReason(value string) bool {
	return strings.TrimSpace(value) == quarantine.ReasonScannerDetection
}

func quarantineSafeErrorText(value string, forbiddenValues ...string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "sensitive detail redacted"
	}
	if quarantineTextContainsForbidden(value, forbiddenValues) {
		return "sensitive detail redacted"
	}
	if len(value) > diagnosticTextMaxBytes || quarantineSensitiveText(value) {
		return "sensitive detail redacted"
	}
	for _, r := range value {
		if diagnosticTextRuneAllowed(r) || r == ' ' || r == ':' {
			continue
		}
		return "sensitive detail redacted"
	}
	return value
}

func quarantineTextContainsForbidden(value string, forbiddenValues []string) bool {
	for _, forbidden := range forbiddenValues {
		forbidden = strings.TrimSpace(forbidden)
		if forbidden != "" && strings.Contains(value, forbidden) {
			return true
		}
	}
	return false
}

func quarantineSensitiveText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"sec" + "ret",
		"pass" + "word",
		"priv" + "ate",
		"bear" + "er",
		"auth" + "orization",
		"tok" + "en",
		"key=",
		"key:",
		"transaction_id",
		"document_name",
		"idempotency",
		"backend key",
		"backend_key",
		"trace_id",
		"request_id",
		"signature",
		"yara",
		"clamd",
		"rule",
		"file_path",
		"operator_note",
		"auth_claim",
		"/",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
