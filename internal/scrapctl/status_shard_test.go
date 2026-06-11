package scrapctl

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

const shardDiagnosticsHealthBody = `{
	"status":"ok",
	"upload_pressure":"ok",
	"upload_pressure_level":0,
	"upload_pending_bytes":0,
	"upload_pending_blocks":0,
	"shard_diagnostics":{
		"status":"ok",
		"cell_id":"cell-a",
		"member_hostname":"scrapd-0",
		"member_id":"member-a",
		"shards":[{
			"shard_id":7,
			"membership":"local",
			"routes":["0-511"],
			"state":"open",
			"health":"ok",
			"readiness":"ready",
			"is_leader":true,
			"leader_id":1,
			"peer_count":3,
			"peer_health":"configured",
			"upload_pressure":"ok",
			"upload_pressure_level":0,
			"upload_pending_bytes":0,
			"upload_pending_blocks":0,
			"eviction_pressure":"ok",
			"restore_failed_blocks":0
		}]
	}
}`

func TestStatusPrintsShardDiagnosticsJSON(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"status", "--output=json"}, &out, io.Discard, Deps{
		HTTPClient: healthClient(shardDiagnosticsHealthBody),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`"shard_diagnostics"`,
		`"cell_id":"cell-a"`,
		`"member_hostname":"scrapd-0"`,
		`"member_id":"member-a"`,
		`"shard_id":7`,
		`"membership":"local"`,
		`"routes":["0-511"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in output:\n%s", want, got)
		}
	}
}

func TestStatusTextOutputIncludesCellMemberShardTerms(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"status"}, &out, io.Discard, Deps{
		HTTPClient: healthClient(shardDiagnosticsHealthBody),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"status: ok",
		"UploadPressure:ok",
		"Cell: cell-a",
		"Member: scrapd-0 member_id=member-a",
		"Shard 7:",
		"routes=0-511",
		"leader=true",
		"peer_health=configured",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestStatusShardDiagnosticsOutputUsesBoundedEvidence(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"status"}, &out, io.Discard, Deps{
		HTTPClient: healthClient(`{
			"status":"degraded",
			"upload_pressure":"ok",
			"upload_pressure_level":0,
			"upload_pending_bytes":0,
			"upload_pending_blocks":0,
			"shard_diagnostics":{"status":"degraded","reason":"snapshot_unavailable"}
		}`),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	got := out.String()
	for _, want := range []string{"status: degraded", "ShardDiagnostics:degraded", "reason=snapshot_unavailable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"tx-secret-raw", "doc-secret.pdf", "10.1.2.3:9091", "/tmp/secret", "private-key-material"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("status output leaked %q:\n%s", forbidden, got)
		}
	}
}
