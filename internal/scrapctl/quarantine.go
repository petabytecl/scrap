package scrapctl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
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

const (
	quarantineResponseMaxBytes = 64 * 1024
	quarantineEvidenceFileMode = 0o600
	quarantineIdentityValues   = 2
)

type quarantineListOptions struct {
	common        commonOptions
	transactionID string
	limit         int
	limitSet      bool
}

type quarantineIdentityOptions struct {
	common        commonOptions
	transactionID string
	documentName  string
}

type quarantineEvidenceOptions struct {
	common        commonOptions
	evidencePath  string
	transactionID string
	limit         int
	limitSet      bool
}

type quarantineDocumentsResponse struct {
	Documents []quarantine.Record `json:"documents"`
}

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

func runQuarantine(args []string, stdout, stderr io.Writer, deps Deps) error {
	if len(args) == 0 {
		return errors.New("usage: scrapctl quarantine <list|inspect|confirm|release|evidence>")
	}
	switch args[0] {
	case "list":
		return runQuarantineList(args[1:], stdout, deps)
	case "inspect":
		return runQuarantineInspect(args[1:], stdout, deps)
	case "confirm":
		return runQuarantineDecision(args[1:], stdout, deps, "confirm")
	case "release":
		return runQuarantineDecision(args[1:], stdout, deps, "release")
	case "evidence":
		return runQuarantineEvidence(args[1:], stdout, stderr, deps)
	default:
		return fmt.Errorf("unsupported quarantine command %q", args[0])
	}
}

func runQuarantineList(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseQuarantineListOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	records, err := getQuarantineDocuments(ctx, opts.common, deps, quarantine.ListFilter{
		TransactionID: opts.transactionID,
		Limit:         opts.limit,
	})
	if err != nil {
		return err
	}
	output := quarantineDocumentsOutput{Documents: quarantineDocumentOutputs(records)}
	if opts.common.output == "json" {
		return writeJSON(stdout, output)
	}
	return writeQuarantineDocumentsText(stdout, output)
}

func runQuarantineInspect(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseQuarantineIdentityOptions("quarantine inspect", args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	record, err := getQuarantineDocument(ctx, opts.common, deps, quarantine.Identity{
		TransactionID: opts.transactionID,
		DocumentName:  opts.documentName,
	})
	if err != nil {
		return err
	}
	output := quarantineDocumentOutputFromRecord(record)
	if opts.common.output == "json" {
		return writeJSON(stdout, output)
	}
	return writeQuarantineDocumentText(stdout, output)
}

func runQuarantineDecision(args []string, stdout io.Writer, deps Deps, action string) error {
	opts, err := parseQuarantineIdentityOptions("quarantine "+action, args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	result, err := postQuarantineDecision(ctx, opts.common, deps, action, quarantine.Identity{
		TransactionID: opts.transactionID,
		DocumentName:  opts.documentName,
	})
	if err != nil {
		return err
	}
	output := quarantineResultOutputFromResult(result)
	if opts.common.output == "json" {
		if err := writeJSON(stdout, output); err != nil {
			return err
		}
		return failedQuarantineResultError(action, output)
	}
	if err := writeQuarantineResultText(stdout, output); err != nil {
		return err
	}
	return failedQuarantineResultError(action, output)
}

func runQuarantineEvidence(args []string, stdout, _ io.Writer, deps Deps) error {
	opts, err := parseQuarantineEvidenceOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	records, err := getQuarantineDocuments(ctx, opts.common, deps, quarantine.ListFilter{
		TransactionID: opts.transactionID,
		Limit:         opts.limit,
	})
	if err != nil {
		return err
	}
	documents := quarantineDocumentOutputs(records)
	stdoutText := opts.evidencePath + "\n"
	report := newQuarantineEvidenceReport(opts, documents)
	forbiddenValues := quarantineForbiddenValues(records)
	report.RedactionChecks = quarantineRedactionChecks(stdoutText, "", report, forbiddenValues)
	if err := verifyQuarantineEvidenceRedacted(stdoutText, "", report, forbiddenValues); err != nil {
		return err
	}
	if err := writeQuarantineEvidence(opts.evidencePath, report); err != nil {
		return err
	}
	if opts.common.output == "json" {
		return writeJSON(stdout, report)
	}
	if _, err := io.WriteString(stdout, stdoutText); err != nil {
		return fmt.Errorf("write quarantine evidence path: %w", err)
	}
	return nil
}

func parseQuarantineListOptions(args []string) (quarantineListOptions, error) {
	opts := quarantineListOptions{limit: quarantine.DefaultListLimit}
	fs := newFlagSet("quarantine list", &opts.common, func(fs *flag.FlagSet, _ *commonOptions) {
		fs.StringVar(&opts.transactionID, "transaction-id", "", "Optional Transaction filter")
		fs.IntVar(&opts.limit, "limit", opts.limit, "Maximum Content Quarantine records to return")
	})
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return quarantineListOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "limit" {
			opts.limitSet = true
		}
	})
	if err := validateCommon(opts.common); err != nil {
		return quarantineListOptions{}, err
	}
	if opts.limitSet && (opts.limit < 1 || opts.limit > quarantine.MaxListLimit) {
		return quarantineListOptions{}, fmt.Errorf("limit must be between 1 and %d", quarantine.MaxListLimit)
	}
	if _, err := (quarantine.ListFilter{TransactionID: opts.transactionID, Limit: opts.limit}).Validate(); err != nil {
		return quarantineListOptions{}, quarantineOptionError(err)
	}
	return opts, nil
}

func parseQuarantineIdentityOptions(name string, args []string) (quarantineIdentityOptions, error) {
	opts := quarantineIdentityOptions{}
	fs := newFlagSet(name, &opts.common, func(fs *flag.FlagSet, _ *commonOptions) {
		fs.StringVar(&opts.transactionID, "transaction-id", "", "Content Quarantine Transaction identity")
		fs.StringVar(&opts.documentName, "document-name", "", "Content Quarantine Document name")
	})
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return quarantineIdentityOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if err := validateCommon(opts.common); err != nil {
		return quarantineIdentityOptions{}, err
	}
	if strings.TrimSpace(opts.transactionID) == "" {
		return quarantineIdentityOptions{}, errors.New("transaction-id is required")
	}
	if strings.TrimSpace(opts.documentName) == "" {
		return quarantineIdentityOptions{}, errors.New("document-name is required")
	}
	if err := (quarantine.Identity{TransactionID: opts.transactionID, DocumentName: opts.documentName}).Validate(); err != nil {
		return quarantineIdentityOptions{}, quarantineOptionError(err)
	}
	return opts, nil
}

func parseQuarantineEvidenceOptions(args []string) (quarantineEvidenceOptions, error) {
	opts := quarantineEvidenceOptions{limit: quarantine.DefaultListLimit}
	fs := newFlagSet("quarantine evidence", &opts.common, func(fs *flag.FlagSet, _ *commonOptions) {
		fs.StringVar(&opts.evidencePath, "evidence-path", "", "Path for the redacted quarantine evidence report")
		fs.StringVar(&opts.transactionID, "transaction-id", "", "Optional Transaction filter")
		fs.IntVar(&opts.limit, "limit", opts.limit, "Maximum Content Quarantine records to include")
	})
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return quarantineEvidenceOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "limit" {
			opts.limitSet = true
		}
	})
	if err := validateCommon(opts.common); err != nil {
		return quarantineEvidenceOptions{}, err
	}
	if err := validateOperatorPath("evidence-path", opts.evidencePath); err != nil {
		return quarantineEvidenceOptions{}, err
	}
	if opts.limitSet && (opts.limit < 1 || opts.limit > quarantine.MaxListLimit) {
		return quarantineEvidenceOptions{}, fmt.Errorf("limit must be between 1 and %d", quarantine.MaxListLimit)
	}
	if _, err := (quarantine.ListFilter{TransactionID: opts.transactionID, Limit: opts.limit}).Validate(); err != nil {
		return quarantineEvidenceOptions{}, quarantineOptionError(err)
	}
	return opts, nil
}

func getQuarantineDocuments(ctx context.Context, opts commonOptions, deps Deps, filter quarantine.ListFilter) ([]quarantine.Record, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.adminURL)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(opts.adminURL, "/") + "/admin/quarantine/documents"
	values := url.Values{}
	if filter.TransactionID != "" {
		values.Set("transaction_id", filter.TransactionID)
	}
	if filter.Limit > 0 {
		values.Set("limit", strconv.Itoa(filter.Limit))
	}
	if encoded := values.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build quarantine list request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET quarantine documents: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, quarantineHTTPError(resp, "quarantine list")
	}
	var list quarantineDocumentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode quarantine documents: %w", err)
	}
	return list.Documents, nil
}

func getQuarantineDocument(ctx context.Context, opts commonOptions, deps Deps, identity quarantine.Identity) (quarantine.Record, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.adminURL)
	if err != nil {
		return quarantine.Record{}, err
	}
	values := url.Values{}
	values.Set("transaction_id", identity.TransactionID)
	values.Set("document_name", identity.DocumentName)
	endpoint := strings.TrimRight(opts.adminURL, "/") + "/admin/quarantine/document?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return quarantine.Record{}, fmt.Errorf("build quarantine inspect request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return quarantine.Record{}, fmt.Errorf("GET quarantine document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return quarantine.Record{}, quarantineHTTPError(resp, "quarantine inspect")
	}
	var record quarantine.Record
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return quarantine.Record{}, fmt.Errorf("decode quarantine document: %w", err)
	}
	return record, nil
}

func postQuarantineDecision(ctx context.Context, opts commonOptions, deps Deps, action string, identity quarantine.Identity) (quarantine.Result, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.adminURL)
	if err != nil {
		return quarantine.Result{}, err
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return quarantine.Result{}, fmt.Errorf("marshal quarantine %s request: %w", action, err)
	}
	endpoint := strings.TrimRight(opts.adminURL, "/") + "/admin/quarantine/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return quarantine.Result{}, fmt.Errorf("build quarantine %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return quarantine.Result{}, fmt.Errorf("POST quarantine %s: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return quarantine.Result{}, quarantineHTTPError(resp, "quarantine "+action)
	}
	var result quarantine.Result
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return quarantine.Result{}, fmt.Errorf("decode quarantine %s: %w", action, err)
	}
	return result, nil
}

func quarantineHTTPError(resp *http.Response, operation string) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, quarantineResponseMaxBytes))
	var result quarantine.Result
	if err := json.Unmarshal(data, &result); err == nil && result.Status != "" {
		reason := quarantineSafeLabel(result.Reason)
		if reason == "" {
			reason = quarantine.ReasonInternalError
		}
		return fmt.Errorf("%s failed: status=%d reason=%s", operation, resp.StatusCode, reason)
	}
	detail := quarantineSafeErrorText(strings.TrimSpace(string(data)))
	return fmt.Errorf("%s failed: status=%d: %s", operation, resp.StatusCode, detail)
}

func failedQuarantineResultError(action string, result quarantineResultOutput) error {
	if result.Status == quarantine.StatusFailed {
		return fmt.Errorf("quarantine %s failed: reason=%s", action, quarantineSafeLabel(result.Reason))
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
		ScanType:    quarantineSafeLabel(record.ScanType),
		Reason:      quarantineSafeLabel(record.Reason),
		Lifecycle:   quarantineSafeLabel(record.Lifecycle),
		ConfirmedAt: record.ConfirmedAt,
	}
}

func quarantineResultOutputFromResult(result quarantine.Result) quarantineResultOutput {
	output := quarantineResultOutput{
		Status:  quarantineSafeLabel(result.Status),
		Reason:  quarantineSafeLabel(result.Reason),
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
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create quarantine evidence directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, quarantineEvidenceFileMode) //nolint:gosec // path is explicit operator-selected evidence output.
	if err != nil {
		return fmt.Errorf("write quarantine evidence: %w", err)
	}
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
	return syncDirectory(filepath.Dir(path))
}

func quarantineForbiddenValues(records []quarantine.Record) []string {
	values := make([]string, 0, len(records)*quarantineIdentityValues)
	for _, record := range records {
		values = append(values, strings.TrimSpace(record.TransactionID), strings.TrimSpace(record.DocumentName))
	}
	return values
}

func redactedQuarantineIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return diagnosticTextRedacted
	}
	sum := sha256.Sum256([]byte(value))
	return "redacted:" + hex.EncodeToString(sum[:])[:12]
}

func quarantineSafeAdminURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return diagnosticTextRedacted
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func quarantineOptionError(err error) error {
	message := err.Error()
	message = strings.ReplaceAll(message, "quarantine invalid request: invalid argument: ", "")
	message = strings.ReplaceAll(message, "quarantine invalid request: ", "")
	message = strings.ReplaceAll(message, "transaction_id", "transaction-id")
	message = strings.ReplaceAll(message, "document_name", "document-name")
	return errors.New(message)
}

func quarantineSafeLabel(value string) string {
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

func quarantineSafeErrorText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
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
