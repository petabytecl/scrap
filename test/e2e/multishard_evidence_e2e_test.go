package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

type shardDiagnosticsHealth struct {
	ShardDiagnostics e2eShardDiagnostics `json:"shard_diagnostics"`
}

type e2eShardDiagnostics struct {
	Status string               `json:"status"`
	Shards []e2eShardDiagnostic `json:"shards"`
}

type e2eShardDiagnostic struct {
	ShardID             uint64   `json:"shard_id"`
	Membership          string   `json:"membership"`
	Routes              []string `json:"routes"`
	State               string   `json:"state"`
	Health              string   `json:"health"`
	Readiness           string   `json:"readiness"`
	LeaderState         string   `json:"leader_state"`
	UploadPendingBlocks int      `json:"upload_pending_blocks"`
}

func TestE2EMultiShardRestartDeterminism(t *testing.T) {
	requireE2E(t)

	placement := e2eTwoShardPlacement(t)
	client := connect(t)
	tx7 := e2eTransactionForShard(t, placement, "tx-e2e-multishard-seven", 7)
	tx9 := e2eTransactionForShard(t, placement, "tx-e2e-multishard-nine", 9)
	doc7 := "seven.xml"
	doc9 := "nine.xml"
	body7 := []byte("multi-shard-seven")
	body9 := []byte("multi-shard-nine")

	writeDocE2E(t, client, tx7, doc7, "text/xml", body7)
	writeDocE2E(t, client, tx9, doc9, "text/xml", body9)
	assertE2ETransactionRoutesToShard(t, placement, tx7, 7)
	assertE2ETransactionRoutesToShard(t, placement, tx9, 9)
	beforeDiag := fetchAnyShardDiagnostics(t)
	assertE2EDiagnosticsCoverTwoShards(t, beforeDiag)

	rolloutRestartScrapdAndWaitReady(t)
	client = connect(t)

	assertE2ETransactionRoutesToShard(t, placement, tx7, 7)
	assertE2ETransactionRoutesToShard(t, placement, tx9, 9)
	assertReadDocumentE2E(t, client, tx7, doc7, body7)
	assertReadDocumentE2E(t, client, tx9, doc9, body9)
	// After a rollout restart, a follower briefly reports leader_state="unknown"
	// (LeaderID==0) until Raft heartbeats let it learn the new leader. Reads
	// above already prove a leader exists; poll diagnostics until every Shard's
	// leader state has converged before asserting determinism.
	afterDiag := fetchShardDiagnosticsWithResolvedLeaders(t, 60*time.Second)
	assertE2EDiagnosticsCoverTwoShards(t, afterDiag)
	assertShardDiagnosticsStable(t, beforeDiag, afterDiag)
}

func TestE2EMultiShardBackendUploadUsesNonZeroShard(t *testing.T) {
	requireE2E(t)
	restoreLocalStack(t)

	placement := e2eTwoShardPlacement(t)
	txID := e2eTransactionForShard(t, placement, "tx-e2e-multishard-upload", 7)

	endpoint, stopLocalStack := startLocalStackPortForward(t)
	defer stopLocalStack()
	s3Client := newE2ES3Client(t, "http://"+endpoint)
	ensureE2ES3Bucket(t, s3Client)
	before := listBackendObjects(t, s3Client)

	client := connect(t)
	payload := bytes.Repeat([]byte{'s'}, uploadBlockPayloadLen)
	docName := writeUploadBlockE2E(t, client, txID, "upload-seven", 's')
	assertE2ETransactionRoutesToShard(t, placement, txID, 7)
	assertReadDocumentE2E(t, client, txID, docName, payload)

	pair := waitNewBackendPairForDocument(t, s3Client, before, txID, docName)
	assertBackendPairShard(t, pair, 7)
	verifyS3ObjectMD5(t, s3Client, pair.blk)
	verifyS3ObjectMD5(t, s3Client, pair.idx)
	assertE2ETransactionRoutesToShard(t, placement, txID, 7)
	assertReadDocumentE2E(t, client, txID, docName, payload)
	assertShardDiagnosticsIncludeShard(t, fetchAnyShardDiagnostics(t), 7)
}

func assertReadDocumentE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName string, want []byte) {
	t.Helper()
	headResp := headDocE2E(t, client, txID, docName)
	if headResp.GetSize() != int64(len(want)) {
		t.Fatalf("HeadDocument %s/%s size = %d, want %d", txID, docName, headResp.GetSize(), len(want))
	}
	got := readDocE2E(t, client, txID, docName)
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadDocument %s/%s bytes = %d, want %d", txID, docName, len(got), len(want))
	}
}

func fetchAnyShardDiagnostics(t *testing.T) e2eShardDiagnostics {
	t.Helper()
	var lastErr string
	for _, pod := range podNames(t) {
		diag, errText := fetchShardDiagnosticsFromPod(t, pod)
		if errText == "" {
			return diag
		}
		lastErr = errText
	}
	t.Fatalf("no pod returned Shard diagnostics: %s", lastErr)
	return e2eShardDiagnostics{}
}

func tryFetchAnyShardDiagnostics(t *testing.T) (e2eShardDiagnostics, bool) {
	t.Helper()
	for _, pod := range podNames(t) {
		diag, errText := fetchShardDiagnosticsFromPod(t, pod)
		if errText == "" {
			return diag, true
		}
	}
	return e2eShardDiagnostics{}, false
}

func shardLeaderStatesResolved(diag e2eShardDiagnostics) bool {
	if len(diag.Shards) == 0 {
		return false
	}
	for _, shard := range diag.Shards {
		if shard.LeaderState != "leader" && shard.LeaderState != "follower" {
			return false
		}
	}
	return true
}

// fetchShardDiagnosticsWithResolvedLeaders polls Shard diagnostics until every
// local Shard reports a resolved leader state (leader or follower), tolerating
// the transient post-restart window where a follower has not yet learned the
// current leader. It fails closed if leadership never converges in time.
func fetchShardDiagnosticsWithResolvedLeaders(t *testing.T, timeout time.Duration) e2eShardDiagnostics {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last e2eShardDiagnostics
	for time.Now().Before(deadline) {
		diag, ok := tryFetchAnyShardDiagnostics(t)
		if ok {
			last = diag
			if shardLeaderStatesResolved(diag) {
				return diag
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("Shard leader states did not resolve within %s: %+v", timeout, last.Shards)
	return last
}

func fetchShardDiagnosticsFromPod(t *testing.T, pod string) (e2eShardDiagnostics, string) {
	t.Helper()
	addr, stop := startPodPortForward(t, pod, 9100)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e2eAdminURL(addr, "/healthz"), nil)
	if err != nil {
		t.Fatalf("new health request: %v", err)
	}
	resp, err := e2eHTTPClient(t).Do(req)
	if err != nil {
		return e2eShardDiagnostics{}, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return e2eShardDiagnostics{}, string(body)
	}
	var health shardDiagnosticsHealth
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return e2eShardDiagnostics{}, err.Error()
	}
	if len(health.ShardDiagnostics.Shards) == 0 {
		return e2eShardDiagnostics{}, "missing shard_diagnostics"
	}
	return health.ShardDiagnostics, ""
}

func assertE2EDiagnosticsCoverTwoShards(t *testing.T, diag e2eShardDiagnostics) {
	t.Helper()
	assertBoundedShardDiagnosticsStatus(t, diag.Status)
	got := diagnosticShardIDs(diag)
	want := []uint64{7, 9}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic Shard IDs = %v, want %v", got, want)
	}
	for _, shard := range diag.Shards {
		if shard.Membership != "local" {
			t.Fatalf("Shard %d membership = %q, want local", shard.ShardID, shard.Membership)
		}
		if len(shard.Routes) == 0 {
			t.Fatalf("Shard %d routes are empty", shard.ShardID)
		}
		assertShardDiagnosticBounded(t, shard)
	}
}

func assertShardDiagnosticsIncludeShard(t *testing.T, diag e2eShardDiagnostics, shardID uint64) {
	t.Helper()
	for _, shard := range diag.Shards {
		if shard.ShardID == shardID {
			if shard.Membership != "local" {
				t.Fatalf("Shard %d membership = %q, want local", shardID, shard.Membership)
			}
			if len(shard.Routes) == 0 {
				t.Fatalf("Shard %d routes are empty", shardID)
			}
			assertShardDiagnosticBounded(t, shard)
			return
		}
	}
	t.Fatalf("missing Shard %d in diagnostics: %v", shardID, diagnosticShardIDs(diag))
}

func assertBoundedShardDiagnosticsStatus(t *testing.T, status string) {
	t.Helper()
	switch status {
	case "ok", "degraded":
	default:
		t.Fatalf("shard diagnostics status = %q, want ok or degraded", status)
	}
}

func assertShardDiagnosticBounded(t *testing.T, shard e2eShardDiagnostic) {
	t.Helper()
	if shard.State != "open" {
		t.Fatalf("Shard %d state = %q, want open", shard.ShardID, shard.State)
	}
	switch shard.Health {
	case "ok", "degraded":
	default:
		t.Fatalf("Shard %d health = %q, want ok or degraded", shard.ShardID, shard.Health)
	}
	if shard.Readiness != "ready" {
		t.Fatalf("Shard %d readiness = %q, want ready", shard.ShardID, shard.Readiness)
	}
	switch shard.LeaderState {
	case "leader", "follower":
	default:
		t.Fatalf("Shard %d leader_state = %q, want leader or follower", shard.ShardID, shard.LeaderState)
	}
}

func assertShardDiagnosticsStable(t *testing.T, before, after e2eShardDiagnostics) {
	t.Helper()
	if got, want := diagnosticShardIDs(after), diagnosticShardIDs(before); !reflect.DeepEqual(got, want) {
		t.Fatalf("post-restart diagnostic Shard IDs = %v, want %v", got, want)
	}
	if got, want := diagnosticRoutesByShard(after), diagnosticRoutesByShard(before); !reflect.DeepEqual(got, want) {
		t.Fatalf("post-restart diagnostic routes = %v, want %v", got, want)
	}
}

func diagnosticShardIDs(diag e2eShardDiagnostics) []uint64 {
	ids := make([]uint64, 0, len(diag.Shards))
	for _, shard := range diag.Shards {
		ids = append(ids, shard.ShardID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func diagnosticRoutesByShard(diag e2eShardDiagnostics) map[uint64][]string {
	routes := make(map[uint64][]string, len(diag.Shards))
	for _, shard := range diag.Shards {
		routes[shard.ShardID] = append([]string(nil), shard.Routes...)
	}
	return routes
}
