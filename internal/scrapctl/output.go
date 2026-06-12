package scrapctl

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	diagnosticTextMaxBytes = 128
	diagnosticTextRedacted = "redacted"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type DoctorReport struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
	Health *Health `json:"health,omitempty"`
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(value)
}

func writeDoctorText(w io.Writer, report DoctorReport) error {
	if _, err := fmt.Fprintf(w, "status: %s\n", report.Status); err != nil {
		return fmt.Errorf("write doctor status: %w", err)
	}
	for _, check := range report.Checks {
		if check.Reason == "" {
			_, err := fmt.Fprintf(w, "%s: %s\n", check.Name, check.Status)
			if err != nil {
				return fmt.Errorf("write doctor check: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%s: %s (%s)\n", check.Name, check.Status, check.Reason); err != nil {
			return fmt.Errorf("write doctor check: %w", err)
		}
	}
	return nil
}

func writeHealthText(w io.Writer, health Health) error {
	if err := writeHealthSummaryText(w, health); err != nil {
		return err
	}
	if err := writeHealthEvictionText(w, health); err != nil {
		return err
	}
	if err := writeHealthSecurityText(w, health); err != nil {
		return err
	}
	return writeShardDiagnosticsText(w, health.ShardDiagnostics)
}

func writeHealthSummaryText(w io.Writer, health Health) error {
	if _, err := fmt.Fprintf(w, "status: %s\n", diagnosticTextValue(health.Status)); err != nil {
		return fmt.Errorf("write health status: %w", err)
	}
	if _, err := fmt.Fprintf(
		w,
		"UploadPressure:%s Level:%d PendingBytes:%d PendingBlocks:%d\n",
		diagnosticTextValue(health.UploadPressure),
		health.UploadPressureLevel,
		health.UploadPendingBytes,
		health.UploadPendingBlocks,
	); err != nil {
		return fmt.Errorf("write upload pressure: %w", err)
	}
	return nil
}

func writeHealthEvictionText(w io.Writer, health Health) error {
	if _, err := fmt.Fprintf(
		w,
		"EvictionPressure:%s EvictedBlocks:%d EvictedBytes:%d HotCleanupNeededBlocks:%d MetadataLossBlocks:%d UnexpectedLossBlocks:%d QuarantinedBlocks:%d RestoreFailedBlocks:%d\n",
		diagnosticTextValue(health.EvictionPressure),
		health.EvictedBlocks,
		health.EvictedBytes,
		health.HotCleanupNeededBlocks,
		health.MetadataLossBlocks,
		health.UnexpectedLossBlocks,
		health.QuarantinedBlocks,
		health.RestoreFailedBlocks,
	); err != nil {
		return fmt.Errorf("write eviction health: %w", err)
	}
	if len(health.RestoreFailuresByReason) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "RestoreFailuresByReason:%s\n", diagnosticReasonCounts(health.RestoreFailuresByReason)); err != nil {
		return fmt.Errorf("write restore failure reasons: %w", err)
	}
	return nil
}

func writeHealthSecurityText(w io.Writer, health Health) error {
	if health.SecurityMode == "" {
		return nil
	}
	if _, err := fmt.Fprintf(
		w,
		"SecurityMode:%s ProductionReadiness:%s reason=%s\n",
		diagnosticTextValue(health.SecurityMode),
		diagnosticTextValue(health.ProductionReadyStatus),
		diagnosticTextValue(health.ProductionReadyReason),
	); err != nil {
		return fmt.Errorf("write security status: %w", err)
	}
	return nil
}

func writeShardDiagnosticsText(w io.Writer, diag *ShardDiagnostics) error {
	if diag == nil {
		return nil
	}
	if err := writeShardDiagnosticsHeaderText(w, diag); err != nil {
		return err
	}
	if err := writeShardDiagnosticsIdentityText(w, diag); err != nil {
		return err
	}
	return writeShardDiagnosticsEntriesText(w, diag.Shards)
}

func writeShardDiagnosticsHeaderText(w io.Writer, diag *ShardDiagnostics) error {
	if _, err := fmt.Fprintf(w, "ShardDiagnostics:%s", diagnosticTextValue(diag.Status)); err != nil {
		return fmt.Errorf("write Shard diagnostics status: %w", err)
	}
	if diag.Reason != "" {
		if _, err := fmt.Fprintf(w, " reason=%s", diagnosticTextValue(diag.Reason)); err != nil {
			return fmt.Errorf("write Shard diagnostics reason: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write Shard diagnostics newline: %w", err)
	}
	return nil
}

func writeShardDiagnosticsIdentityText(w io.Writer, diag *ShardDiagnostics) error {
	if diag.CellID != "" {
		if _, err := fmt.Fprintf(w, "Cell: %s\n", diagnosticTextValue(diag.CellID)); err != nil {
			return fmt.Errorf("write Cell status: %w", err)
		}
	}
	if diag.MemberHostname != "" || diag.MemberID != "" {
		if _, err := fmt.Fprintf(
			w,
			"Member: %s member_id=%s\n",
			diagnosticTextValue(diag.MemberHostname),
			diagnosticTextValue(diag.MemberID),
		); err != nil {
			return fmt.Errorf("write Member status: %w", err)
		}
	}
	return nil
}

func writeShardDiagnosticsEntriesText(w io.Writer, shards []ShardDiagnostic) error {
	for _, shard := range shards {
		if err := writeShardDiagnosticText(w, shard); err != nil {
			return err
		}
	}
	return nil
}

func writeShardDiagnosticText(w io.Writer, shard ShardDiagnostic) error {
	if _, err := fmt.Fprintf(
		w,
		"Shard %d: membership=%s state=%s health=%s readiness=%s routes=%s leader_state=%s leader=%t leader_id=%d peer_count=%d peer_health=%s upload_pressure=%s eviction_pressure=%s",
		shard.ShardID,
		diagnosticTextValue(shard.Membership),
		diagnosticTextValue(shard.State),
		diagnosticTextValue(shard.Health),
		diagnosticTextValue(shard.Readiness),
		strings.Join(diagnosticTextValues(shard.Routes), ","),
		diagnosticTextValue(shard.LeaderState),
		shard.IsLeader,
		shard.LeaderID,
		shard.PeerCount,
		diagnosticTextValue(shard.PeerHealth),
		diagnosticTextValue(shard.UploadPressure),
		diagnosticTextValue(shard.EvictionPressure),
	); err != nil {
		return fmt.Errorf("write Shard status: %w", err)
	}
	if shard.ScannerStatus != "" {
		if _, err := fmt.Fprintf(
			w,
			" scanner_status=%s scanner_lag_blocks=%d scanner_in_flight_blocks=%d scanner_reason=%s scanner_scanned_blocks=%d scanner_failed_blocks=%d scanner_updated_unix=%d",
			diagnosticTextValue(shard.ScannerStatus),
			shard.ScannerLagBlocks,
			shard.ScannerInFlightBlocks,
			diagnosticTextValue(shard.ScannerLastReason),
			shard.ScannerScannedBlocks,
			shard.ScannerFailedBlocks,
			shard.ScannerLastUpdatedUnix,
		); err != nil {
			return fmt.Errorf("write Shard scanner status: %w", err)
		}
	}
	if shard.FailureReason != "" {
		if _, err := fmt.Fprintf(w, " reason=%s", diagnosticTextValue(shard.FailureReason)); err != nil {
			return fmt.Errorf("write Shard failure reason: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write Shard newline: %w", err)
	}
	return nil
}

func diagnosticReasonCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", diagnosticTextValue(key), counts[key]))
	}
	return strings.Join(parts, ",")
}

func diagnosticTextValues(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = diagnosticTextValue(value)
	}
	return out
}

func diagnosticTextValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if diagnosticTextLooksSensitive(value) {
		return diagnosticTextRedacted
	}
	if len(value) > diagnosticTextMaxBytes {
		return diagnosticTextRedacted
	}
	for _, r := range value {
		if diagnosticTextRuneAllowed(r) {
			continue
		}
		return diagnosticTextRedacted
	}
	return value
}

func diagnosticTextLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"secret", "private", "token", "password", "key"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func diagnosticTextRuneAllowed(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		strings.ContainsRune("_.-", r)
}

func reportFailed(checks []Check) bool {
	for _, check := range checks {
		if check.Status == "fail" {
			return true
		}
	}
	return false
}
