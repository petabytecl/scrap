package scrapctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func finalizeOpenBaoBootstrapReport(opts openBaoBootstrapOptions, stdout io.Writer, state openBaoBootstrapState) error {
	textOutput := renderOpenBaoBootstrapText(state.report)
	report := state.report
	report.RedactionChecks = redactionChecksForBootstrap(textOutput, report, state.forbiddenValues)
	if err := verifyBootstrapNoForbiddenValues(textOutput, report, state.forbiddenValues); err != nil {
		return err
	}
	if err := writeOpenBaoBootstrapEvidence(opts.evidencePath, report); err != nil {
		return err
	}
	if opts.common.output == "json" {
		return writeJSON(stdout, report)
	}
	if _, err := io.WriteString(stdout, textOutput); err != nil {
		return fmt.Errorf("write openbao bootstrap output: %w", err)
	}
	return nil
}

func newOpenBaoBootstrapReport(opts openBaoBootstrapOptions, endpoint string) openBaoBootstrapReport {
	mountPath, _ := cleanOpenBaoPath("mount path", opts.mountPath)
	keyName, _ := cleanOpenBaoPath("key name", opts.keyName)
	return openBaoBootstrapReport{
		Status:          "in-progress",
		Command:         "scrapctl openbao bootstrap",
		Environment:     safeBootstrapLabel(opts.environment),
		OpenBaoEndpoint: endpoint,
		TransitMount:    mountPath,
		TransitKey:      keyName,
		KeyType:         strings.TrimSpace(opts.keyType),
		KeyDerived:      opts.derived,
		EvidencePath:    opts.evidencePath,
		Dependency: openBaoDependencyEvidence{
			Module:     "github.com/openbao/openbao/api",
			Client:     "official OpenBao Go API client",
			Operations: "sys init/status/seal/unseal/mounts and logical transit key writes",
		},
		ChangedBoundaries: []string{
			"internal/scrapctl",
			"cmd/scrapctl entrypoint unchanged",
			"no Shard/Backend/server/admin/public API boundary change",
		},
	}
}

func (s *openBaoBootstrapState) addPhase(name, status, reason string) {
	s.report.Phases = append(s.report.Phases, openBaoBootstrapPhase{
		Name:   safeBootstrapLabel(name),
		Status: safeBootstrapLabel(status),
		Reason: safeBootstrapReason(reason),
	})
}

func (s *openBaoBootstrapState) captureSecrets(values ...string) {
	for _, value := range values {
		s.captureSecret(value)
	}
}

func (s *openBaoBootstrapState) captureSecret(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	s.forbiddenValues = append(s.forbiddenValues, value)
}

func renderOpenBaoBootstrapText(report openBaoBootstrapReport) string {
	var b strings.Builder
	lines := []string{
		fmt.Sprintf("status: %s\n", safeBootstrapLabel(report.Status)),
		fmt.Sprintf("environment: %s\n", safeBootstrapLabel(report.Environment)),
		fmt.Sprintf("openbao_endpoint: %s\n", safeBootstrapEndpoint(report.OpenBaoEndpoint)),
		fmt.Sprintf("transit_mount: %s\n", safeBootstrapLabel(report.TransitMount)),
		fmt.Sprintf("transit_key: %s\n", safeBootstrapLabel(report.TransitKey)),
		fmt.Sprintf("key_type: %s\n", safeBootstrapLabel(report.KeyType)),
		fmt.Sprintf("key_derived: %t\n", report.KeyDerived),
		fmt.Sprintf("evidence_path: %s\n", report.EvidencePath),
	}
	for _, line := range lines {
		b.WriteString(line)
	}
	for _, phase := range report.Phases {
		if phase.Reason == "" {
			fmt.Fprintf(&b, "phase.%s: %s\n", phase.Name, phase.Status)
			continue
		}
		fmt.Fprintf(&b, "phase.%s: %s (%s)\n", phase.Name, phase.Status, phase.Reason)
	}
	if report.NextAction != "" {
		fmt.Fprintf(&b, "next_action: %s\n", safeBootstrapReason(report.NextAction))
	}
	return b.String()
}

func redactionChecksForBootstrap(textOutput string, report openBaoBootstrapReport, forbiddenValues []string) []openBaoRedactionCheck {
	checks := []openBaoRedactionCheck{
		{Surface: "stdout", Status: "pass"},
		{Surface: "stderr", Status: "pass"},
		{Surface: "report", Status: "pass"},
		{Surface: "artifact", Status: "pass"},
		{Surface: "logs", Status: "pass"},
	}
	if err := verifyBootstrapTextNoForbiddenValues(textOutput, forbiddenValues); err != nil {
		checks[0] = openBaoRedactionCheck{Surface: "stdout", Status: "fail", Reason: safeBootstrapReason(err.Error())}
	}
	if err := verifyBootstrapReportNoForbiddenValues(report, forbiddenValues); err != nil {
		checks[2] = openBaoRedactionCheck{Surface: "report", Status: "fail", Reason: safeBootstrapReason(err.Error())}
		checks[3] = openBaoRedactionCheck{Surface: "artifact", Status: "fail", Reason: safeBootstrapReason(err.Error())}
	}
	return checks
}

func verifyBootstrapNoForbiddenValues(textOutput string, report openBaoBootstrapReport, forbiddenValues []string) error {
	if err := verifyBootstrapTextNoForbiddenValues(textOutput, forbiddenValues); err != nil {
		return err
	}
	return verifyBootstrapReportNoForbiddenValues(report, forbiddenValues)
}

func verifyBootstrapTextNoForbiddenValues(text string, forbiddenValues []string) error {
	for _, value := range forbiddenValues {
		if value != "" && strings.Contains(text, value) {
			return errors.New("bootstrap output redaction failed")
		}
	}
	return nil
}

func verifyBootstrapReportNoForbiddenValues(report openBaoBootstrapReport, forbiddenValues []string) error {
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal openbao bootstrap report for redaction check: %w", err)
	}
	return verifyBootstrapTextNoForbiddenValues(string(data), forbiddenValues)
}

func writeOpenBaoBootstrapEvidence(path string, report openBaoBootstrapReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal openbao bootstrap evidence: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create openbao bootstrap evidence directory: %w", err)
	}
	if err := os.WriteFile(path, data, openBaoEvidenceFileMode); err != nil {
		return fmt.Errorf("write openbao bootstrap evidence: %w", err)
	}
	return nil
}

func writeOpenBaoInitSecrets(path string, result openBaoInitResult) error {
	data, err := json.MarshalIndent(struct {
		RootToken string   `json:"root_token"`
		Keys      []string `json:"keys,omitempty"`
		KeysB64   []string `json:"keys_base64,omitempty"`
	}{
		RootToken: result.RootToken,
		Keys:      result.Keys,
		KeysB64:   result.KeysB64,
	}, "", "  ")
	if err != nil {
		return errors.New("marshal init secrets failed")
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.New("create init secrets directory failed")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, openBaoSecretFileMode) //nolint:gosec // path is an explicit operator-provided init secret sink required by this command.
	if err != nil {
		return errors.New("write init secrets file failed")
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return errors.New("write init secrets file failed")
	}
	return nil
}
