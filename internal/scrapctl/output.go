package scrapctl

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	if err := writeHealthSecurityText(w, health); err != nil {
		return err
	}
	return writeShardDiagnosticsText(w, health.ShardDiagnostics)
}

func writeHealthSummaryText(w io.Writer, health Health) error {
	if _, err := fmt.Fprintf(w, "status: %s\n", health.Status); err != nil {
		return fmt.Errorf("write health status: %w", err)
	}
	if _, err := fmt.Fprintf(
		w,
		"UploadPressure:%s Level:%d PendingBytes:%d PendingBlocks:%d\n",
		health.UploadPressure,
		health.UploadPressureLevel,
		health.UploadPendingBytes,
		health.UploadPendingBlocks,
	); err != nil {
		return fmt.Errorf("write upload pressure: %w", err)
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
		health.SecurityMode,
		health.ProductionReadyStatus,
		health.ProductionReadyReason,
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
	if _, err := fmt.Fprintf(w, "ShardDiagnostics:%s", diag.Status); err != nil {
		return fmt.Errorf("write Shard diagnostics status: %w", err)
	}
	if diag.Reason != "" {
		if _, err := fmt.Fprintf(w, " reason=%s", diag.Reason); err != nil {
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
		if _, err := fmt.Fprintf(w, "Cell: %s\n", diag.CellID); err != nil {
			return fmt.Errorf("write Cell status: %w", err)
		}
	}
	if diag.MemberHostname != "" || diag.MemberID != "" {
		if _, err := fmt.Fprintf(w, "Member: %s member_id=%s\n", diag.MemberHostname, diag.MemberID); err != nil {
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
		"Shard %d: membership=%s state=%s health=%s readiness=%s routes=%s leader=%t leader_id=%d peer_count=%d peer_health=%s upload_pressure=%s eviction_pressure=%s",
		shard.ShardID,
		shard.Membership,
		shard.State,
		shard.Health,
		shard.Readiness,
		strings.Join(shard.Routes, ","),
		shard.IsLeader,
		shard.LeaderID,
		shard.PeerCount,
		shard.PeerHealth,
		shard.UploadPressure,
		shard.EvictionPressure,
	); err != nil {
		return fmt.Errorf("write Shard status: %w", err)
	}
	if shard.FailureReason != "" {
		if _, err := fmt.Fprintf(w, " reason=%s", shard.FailureReason); err != nil {
			return fmt.Errorf("write Shard failure reason: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write Shard newline: %w", err)
	}
	return nil
}

func reportFailed(checks []Check) bool {
	for _, check := range checks {
		if check.Status == "fail" {
			return true
		}
	}
	return false
}
