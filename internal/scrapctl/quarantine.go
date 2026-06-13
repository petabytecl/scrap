package scrapctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	forbiddenValues := quarantineForbiddenValues(records, opts.transactionID)
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
	baseURL, err := quarantineAdminBaseURL(opts.adminURL)
	if err != nil {
		return nil, err
	}
	deps, err = withHTTPClientTLS(deps, opts, baseURL)
	if err != nil {
		return nil, err
	}
	endpoint := quarantineDocumentsEndpoint(baseURL, filter)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build quarantine list request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return nil, quarantineTransportError("GET quarantine documents", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, quarantineHTTPError(resp, "quarantine list", filter.TransactionID)
	}
	var list quarantineDocumentsResponse
	if err := decodeQuarantineResponse(resp.Body, &list); err != nil {
		return nil, fmt.Errorf("decode quarantine documents: %w", err)
	}
	if err := validateQuarantineRecords(list.Documents); err != nil {
		return nil, fmt.Errorf("decode quarantine documents: %w", err)
	}
	return list.Documents, nil
}

func getQuarantineDocument(ctx context.Context, opts commonOptions, deps Deps, identity quarantine.Identity) (quarantine.Record, error) {
	baseURL, err := quarantineAdminBaseURL(opts.adminURL)
	if err != nil {
		return quarantine.Record{}, err
	}
	deps, err = withHTTPClientTLS(deps, opts, baseURL)
	if err != nil {
		return quarantine.Record{}, err
	}
	values := url.Values{}
	values.Set("transaction_id", identity.TransactionID)
	values.Set("document_name", identity.DocumentName)
	endpoint := baseURL + "/admin/quarantine/document?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return quarantine.Record{}, fmt.Errorf("build quarantine inspect request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return quarantine.Record{}, quarantineTransportError("GET quarantine document", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return quarantine.Record{}, quarantineHTTPError(resp, "quarantine inspect", identity.TransactionID, identity.DocumentName)
	}
	var record quarantine.Record
	if err := decodeQuarantineResponse(resp.Body, &record); err != nil {
		return quarantine.Record{}, fmt.Errorf("decode quarantine document: %w", err)
	}
	if err := validateQuarantineRecord(record); err != nil {
		return quarantine.Record{}, fmt.Errorf("decode quarantine document: %w", err)
	}
	return record, nil
}

func postQuarantineDecision(ctx context.Context, opts commonOptions, deps Deps, action string, identity quarantine.Identity) (quarantine.Result, error) {
	baseURL, err := quarantineAdminBaseURL(opts.adminURL)
	if err != nil {
		return quarantine.Result{}, err
	}
	deps, err = withHTTPClientTLS(deps, opts, baseURL)
	if err != nil {
		return quarantine.Result{}, err
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return quarantine.Result{}, fmt.Errorf("marshal quarantine %s request: %w", action, err)
	}
	endpoint := baseURL + "/admin/quarantine/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return quarantine.Result{}, fmt.Errorf("build quarantine %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return quarantine.Result{}, quarantineTransportError("POST quarantine "+action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return quarantine.Result{}, quarantineHTTPError(resp, "quarantine "+action, identity.TransactionID, identity.DocumentName)
	}
	var result quarantine.Result
	if err := decodeQuarantineResponse(resp.Body, &result); err != nil {
		return quarantine.Result{}, fmt.Errorf("decode quarantine %s: %w", action, err)
	}
	if err := validateQuarantineResult(result); err != nil {
		return quarantine.Result{}, fmt.Errorf("decode quarantine %s: %w", action, err)
	}
	return result, nil
}

func quarantineDocumentsEndpoint(baseURL string, filter quarantine.ListFilter) string {
	endpoint := baseURL + "/admin/quarantine/documents"
	values := url.Values{}
	if filter.TransactionID != "" {
		values.Set("transaction_id", filter.TransactionID)
	}
	if filter.Limit > 0 {
		values.Set("limit", strconv.Itoa(filter.Limit))
	}
	if encoded := values.Encode(); encoded != "" {
		return endpoint + "?" + encoded
	}
	return endpoint
}
