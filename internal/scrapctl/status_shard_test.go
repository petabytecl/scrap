package scrapctl

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const shardDiagnosticsHealthBody = `{
	"status":"ok",
	"upload_pressure":"ok",
	"upload_pressure_level":0,
	"upload_pending_bytes":0,
	"upload_pending_blocks":0,
	"eviction_pressure":"degraded",
	"evicted_blocks":2,
	"evicted_bytes":4096,
	"hot_cleanup_needed_blocks":1,
	"metadata_loss_blocks":0,
	"unexpected_loss_blocks":0,
	"quarantined_blocks":1,
	"restore_failed_blocks":1,
	"restore_failures_by_reason":{"restore_failed":1},
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
			"leader_state":"leader",
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
		`"leader_state":"leader"`,
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
		"EvictionPressure:degraded",
		"RestoreFailuresByReason:restore_failed=1",
		"Cell: cell-a",
		"Member: scrapd-0 member_id=member-a",
		"Shard 7:",
		"routes=0-511",
		"leader_state=leader",
		"leader=true",
		"peer_health=configured",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestStatusTextOutputBoundsShardDiagnosticFields(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"status"}, &out, io.Discard, Deps{
		HTTPClient: healthClient(`{
			"status":"ok",
			"upload_pressure":"ok",
			"upload_pressure_level":0,
			"upload_pending_bytes":0,
			"upload_pending_blocks":0,
			"shard_diagnostics":{
				"status":"ok",
				"cell_id":"/tmp/secret",
				"member_hostname":"10.1.2.3:9091",
				"member_id":"private-key-material",
				"shards":[{
					"shard_id":7,
					"membership":"local",
					"routes":["0-511","/tmp/secret"],
					"state":"open",
					"health":"ok",
					"readiness":"ready",
					"peer_health":"10.1.2.3:9091",
					"upload_pressure":"backend-key",
					"eviction_pressure":"ok",
					"failure_reason":"private-key-material"
				}]
			}
		}`),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	got := out.String()
	for _, forbidden := range []string{"/tmp/secret", "10.1.2.3:9091", "private-key-material", "backend-key"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("status output leaked %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "redacted") {
		t.Fatalf("status output missing redacted marker:\n%s", got)
	}
}

func TestStatusShardDiagnosticsProductionRequiresTLSBeforeHTTP(t *testing.T) {
	t.Setenv("SCRAP_SECURITY_MODE", "production")
	called := false
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected HTTP call")
	})}

	err := Run([]string{"status"}, io.Discard, io.Discard, Deps{HTTPClient: client})
	if err == nil {
		t.Fatal("expected TLS configuration error")
	}
	if !strings.Contains(err.Error(), "SCRAP_TLS_SCRAPCTL") {
		t.Fatalf("error = %q, want SCRAP_TLS_SCRAPCTL", err)
	}
	if called {
		t.Fatal("HTTP client was called before production TLS validation")
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
