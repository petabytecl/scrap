package e2e_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3 ETag verification uses MD5 for single-PUT object integrity.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/routing"
)

const (
	e2eS3Region           = "us-east-1"
	localStackService     = "localstack"
	uploadE2ETimeout      = 2 * time.Minute
	uploadBlockPayloadLen = 70 * 1024
)

func TestE2EBackendUploadHappyPath(t *testing.T) {
	requireE2E(t)
	restoreLocalStack(t)

	endpoint, stopLocalStack := startLocalStackPortForward(t)
	defer stopLocalStack()
	s3Client := newE2ES3Client(t, "http://"+endpoint)
	ensureE2ES3Bucket(t, s3Client)
	before := listBackendObjects(t, s3Client)

	client := connect(t)
	txID := uniqueName("tx-e2e-upload-happy")
	docName := writeUploadBlockE2E(t, client, txID, "happy", 'h')

	pair := waitNewBackendPairForDocument(t, s3Client, before, txID, docName)
	verifyS3ObjectMD5(t, s3Client, pair.blk)
	verifyS3ObjectMD5(t, s3Client, pair.idx)

	leader := findLeaderPod(t, txID, docName)
	waitShardUploadPendingBlocks(t, leader, e2eShardIDForTransaction(t, txID), 0, uploadE2ETimeout)
	waitCellMetricAbove(t, "scrap_upload_total", []string{`status="success"`}, 0, uploadE2ETimeout)
	waitCellMetricAbove(t, "scrap_upload_verify_total", []string{`status="pass"`}, 0, uploadE2ETimeout)
}

func TestE2EBackendUploadLeaderChange(t *testing.T) {
	requireE2E(t)
	restoreLocalStack(t)

	endpoint, stopLocalStack := startLocalStackPortForward(t)
	s3Client := newE2ES3Client(t, "http://"+endpoint)
	ensureE2ES3Bucket(t, s3Client)
	before := listBackendObjects(t, s3Client)
	stopLocalStack()

	setLocalStackReplicas(t, 0)
	t.Cleanup(func() { restoreLocalStack(t) })

	client := connect(t)
	txID := uniqueName("tx-e2e-upload-failover")
	docName := writeUploadBlockE2E(t, client, txID, "failover", 'f')
	leader := findLeaderPod(t, txID, docName)

	deletePodAndWaitReady(t, leader)

	restoreLocalStack(t)
	endpoint, stopLocalStack = startLocalStackPortForward(t)
	defer stopLocalStack()
	s3Client = newE2ES3Client(t, "http://"+endpoint)
	ensureE2ES3Bucket(t, s3Client)

	pair := waitNewBackendPairForDocument(t, s3Client, before, txID, docName)
	verifyS3ObjectMD5(t, s3Client, pair.blk)
	verifyS3ObjectMD5(t, s3Client, pair.idx)

	newLeader := findLeaderPod(t, txID, docName)
	waitUploadPendingBlocks(t, newLeader, 0, uploadE2ETimeout)
}

func TestE2EBackendUploadAdmissionPressure(t *testing.T) {
	requireE2E(t)
	restoreLocalStack(t)
	setLocalStackReplicas(t, 0)
	t.Cleanup(func() { restoreLocalStack(t) })

	client := connect(t)
	const pressureShardID uint64 = 7
	placement := e2eTwoShardPlacement(t)
	lastTxID := ""
	lastDocName := ""
	rejected := false
	for i := range 6 {
		txID := e2eTransactionForShard(t, placement, fmt.Sprintf("tx-e2e-upload-pressure-%d", i), pressureShardID)
		docName, err := tryWriteUploadBlockE2E(t, client, txID, fmt.Sprintf("pressure-%d", i), byte('a'+i))
		if isUploadPressureError(err) {
			rejected = true
			break
		}
		if err != nil {
			t.Fatalf("write pressure block %d: %v", i, err)
		}
		lastTxID = txID
		lastDocName = docName
	}
	if !rejected {
		probeTxID := e2eTransactionForShard(t, placement, "tx-e2e-upload-pressure-probe", pressureShardID)
		_, err := tryWriteDocE2E(t, client, probeTxID, "probe.bin", "application/octet-stream", []byte("probe"))
		if !isUploadPressureError(err) {
			t.Fatalf("pressure probe error = %v, want upload pressure rejection", err)
		}
		rejected = true
	}
	if !rejected || lastTxID == "" {
		t.Fatal("upload pressure test did not accept writes before rejection")
	}

	leader := findLeaderPod(t, lastTxID, lastDocName)
	waitUploadPressureAtLeast(t, leader, 2, uploadE2ETimeout)

	restoreLocalStack(t)
	endpoint, stopLocalStack := startLocalStackPortForward(t)
	defer stopLocalStack()
	ensureE2ES3Bucket(t, newE2ES3Client(t, "http://"+endpoint))

	waitUploadPendingBlocks(t, leader, 0, uploadE2ETimeout)
	resumedTxID := e2eTransactionForShard(t, placement, "tx-e2e-upload-resumed", pressureShardID)
	if _, err := tryWriteDocE2E(t, client, resumedTxID, "resume.bin", "application/octet-stream", []byte("resume")); err != nil {
		t.Fatalf("write after upload drain: %v", err)
	}
}

func TestE2ETransactionForShardUsesRoutingPlacement(t *testing.T) {
	placement := e2eTwoShardPlacement(t)

	tx7 := e2eTransactionForShard(t, placement, "tx-e2e-route-seven", 7)
	tx9 := e2eTransactionForShard(t, placement, "tx-e2e-route-nine", 9)

	assertE2ETransactionRoutesToShard(t, placement, tx7, 7)
	assertE2ETransactionRoutesToShard(t, placement, tx9, 9)
	if tx7 == tx9 {
		t.Fatal("Transaction selector returned duplicate Transaction IDs for distinct Shards")
	}
}

func TestBackendPairsAcceptNonZeroShardPrefixes(t *testing.T) {
	before := map[string]backendObject{
		e2eBackendObjectKey(7, 1, "blk"): {key: e2eBackendObjectKey(7, 1, "blk"), size: 10, etag: `"old"`},
	}
	objects := map[string]backendObject{
		e2eBackendObjectKey(7, 1, "blk"):                          {key: e2eBackendObjectKey(7, 1, "blk"), size: 10, etag: `"old"`},
		e2eBackendObjectKey(7, 2, "blk"):                          {key: e2eBackendObjectKey(7, 2, "blk"), size: 20, etag: `"blk-seven"`},
		e2eBackendObjectKey(7, 2, "idx"):                          {key: e2eBackendObjectKey(7, 2, "idx"), size: 5, etag: `"idx-seven"`},
		e2eBackendObjectKey(9, 3, "blk"):                          {key: e2eBackendObjectKey(9, 3, "blk"), size: 21, etag: `"blk-nine"`},
		e2eBackendObjectKey(9, 3, "idx"):                          {key: e2eBackendObjectKey(9, 3, "idx"), size: 6, etag: `"idx-nine"`},
		"other-cell/shards/0000000000000007/0000000000000004.blk": {key: "other-cell/shards/0000000000000007/0000000000000004.blk", size: 99, etag: `"other"`},
	}

	pairs := collectBackendPairs(objects, before)
	got := backendPairShardIDs(pairs)
	want := []uint64{7, 9}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backend pair Shard IDs = %v, want %v", got, want)
	}
}

func writeUploadBlockE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, label string, seed byte) string {
	t.Helper()
	docName, err := tryWriteUploadBlockE2E(t, client, txID, label, seed)
	if err != nil {
		t.Fatalf("write upload block: %v", err)
	}
	return docName
}

func tryWriteUploadBlockE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, label string, seed byte) (string, error) {
	t.Helper()
	docName := label + "-block.bin"
	payload := bytes.Repeat([]byte{seed}, uploadBlockPayloadLen)
	if _, err := tryWriteDocE2E(t, client, txID, docName, "application/octet-stream", payload); err != nil {
		return docName, err
	}
	if _, err := tryWriteDocE2E(t, client, txID, label+"-seal.bin", "application/octet-stream", []byte("seal")); err != nil {
		return docName, err
	}
	return docName, nil
}

func isUploadPressureError(err error) bool {
	if err == nil || status.Code(err) != codes.ResourceExhausted {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.GetReason() == "upload_pressure" {
			return true
		}
	}
	return false
}

func restoreLocalStack(t *testing.T) {
	t.Helper()
	setLocalStackReplicas(t, 1)
	endpoint, stop := startLocalStackPortForward(t)
	defer stop()
	ensureE2ES3Bucket(t, newE2ES3Client(t, "http://"+endpoint))
}

func startLocalStackPortForward(t *testing.T) (string, func()) {
	t.Helper()
	return startResourcePortForward(t, "svc/"+localStackService, 4566)
}

func setLocalStackReplicas(t *testing.T, replicas int) {
	t.Helper()
	switch replicas {
	case 0:
		runScrapctlBackendFault(t, "break")
	case 1:
		runScrapctlBackendFault(t, "restore")
	default:
		t.Fatalf("unsupported localstack replica count %d", replicas)
	}
}

func newE2ES3Client(t *testing.T, endpoint string) *awss3.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(e2eS3Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

func ensureE2ES3Bucket(t *testing.T, client *awss3.Client) {
	t.Helper()
	bucket := e2eS3Bucket()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(bucket)}); err == nil {
		return
	}
	if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil && !isBucketAlreadyOwned(err) {
		t.Fatalf("CreateBucket %s: %v", bucket, err)
	}
}

func isBucketAlreadyOwned(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "BucketAlreadyOwnedByYou"
}

func e2eS3Bucket() string {
	return envOrDefault("SCRAP_E2E_S3_BUCKET", "scrap-e2e")
}

type backendObject struct {
	key  string
	size int64
	etag string
}

type backendPair struct {
	blk backendObject
	idx backendObject
}

func listBackendObjects(t *testing.T, client *awss3.Client) map[string]backendObject {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	paginator := awss3.NewListObjectsV2Paginator(client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(e2eS3Bucket()),
		Prefix: aws.String(e2eS3ObjectPrefix()),
	})
	objects := make(map[string]backendObject)
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListObjectsV2: %v", err)
		}
		for _, item := range out.Contents {
			key := aws.ToString(item.Key)
			if key == "" {
				continue
			}
			objects[key] = backendObject{
				key:  key,
				size: aws.ToInt64(item.Size),
				etag: aws.ToString(item.ETag),
			}
		}
	}
	return objects
}

func e2eS3ObjectPrefix() string {
	return e2eCellID() + "/shards/"
}

func e2eBackendObjectKey(shardID, blockID uint64, ext string) string {
	return fmt.Sprintf("%s%016x/%016x.%s", e2eS3ObjectPrefix(), shardID, blockID, ext)
}

func collectBackendPairs(objects, before map[string]backendObject) map[string]backendPair {
	pairs := make(map[string]backendPair)
	for key, object := range objects {
		if _, existed := before[key]; existed {
			continue
		}
		parsed, ok := parseBackendObjectKey(key)
		if !ok || object.size <= 0 || strings.Trim(object.etag, `"`) == "" {
			continue
		}
		pair := pairs[parsed.base]
		if parsed.ext == "blk" {
			pair.blk = object
		} else {
			pair.idx = object
		}
		pairs[parsed.base] = pair
	}
	return pairs
}

type parsedBackendObjectKey struct {
	base    string
	ext     string
	shardID uint64
}

func parseBackendObjectKey(key string) (parsedBackendObjectKey, bool) {
	prefix := e2eS3ObjectPrefix()
	if !strings.HasPrefix(key, prefix) {
		return parsedBackendObjectKey{}, false
	}
	remainder := strings.TrimPrefix(key, prefix)
	parts := strings.Split(remainder, "/")
	if len(parts) != 2 {
		return parsedBackendObjectKey{}, false
	}
	shardID, ok := parseFixedHexUint64(parts[0])
	if !ok {
		return parsedBackendObjectKey{}, false
	}
	block, ext, ok := splitBackendObjectFilename(parts[1])
	if !ok {
		return parsedBackendObjectKey{}, false
	}
	return parsedBackendObjectKey{
		base:    prefix + parts[0] + "/" + block,
		ext:     ext,
		shardID: shardID,
	}, true
}

func parseFixedHexUint64(value string) (uint64, bool) {
	if len(value) != 16 {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 16, 64)
	if err != nil {
		return 0, false
	}
	if fmt.Sprintf("%016x", parsed) != value {
		return 0, false
	}
	return parsed, true
}

func splitBackendObjectFilename(name string) (base, ext string, ok bool) {
	switch {
	case strings.HasSuffix(name, ".blk"):
		base = strings.TrimSuffix(name, ".blk")
		ext = "blk"
	case strings.HasSuffix(name, ".idx"):
		base = strings.TrimSuffix(name, ".idx")
		ext = "idx"
	default:
		return "", "", false
	}
	if _, ok := parseFixedHexUint64(base); !ok {
		return "", "", false
	}
	return base, ext, true
}

func backendPairShardIDs(pairs map[string]backendPair) []uint64 {
	ids := make([]uint64, 0, len(pairs))
	for base, pair := range pairs {
		if pair.blk.key == "" || pair.idx.key == "" {
			continue
		}
		parsed, ok := parseBackendObjectKey(pair.blk.key)
		if !ok || !strings.HasPrefix(pair.idx.key, base+".") {
			continue
		}
		ids = append(ids, parsed.shardID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func assertBackendPairShard(t *testing.T, pair backendPair, want uint64) {
	t.Helper()
	parsedBlock, ok := parseBackendObjectKey(pair.blk.key)
	if !ok {
		t.Fatalf("backend Block key %q is not an ADR 0009 key", pair.blk.key)
	}
	parsedIndex, ok := parseBackendObjectKey(pair.idx.key)
	if !ok {
		t.Fatalf("backend index key %q is not an ADR 0009 key", pair.idx.key)
	}
	if parsedBlock.shardID != want || parsedIndex.shardID != want {
		t.Fatalf("backend pair Shard IDs = %d/%d, want %d", parsedBlock.shardID, parsedIndex.shardID, want)
	}
	if parsedBlock.base != parsedIndex.base {
		t.Fatalf("backend pair base = %q/%q, want matching Block and index", parsedBlock.base, parsedIndex.base)
	}
}

func e2eTwoShardPlacement(t *testing.T) routing.Placement {
	t.Helper()
	placement, err := routing.NewPlacement(routing.PlacementConfig{
		SlotCount: routing.SlotCount,
		Shards:    []uint64{7, 9},
		Ranges: []routing.SlotRange{
			{ShardID: 7, StartSlot: 0, EndSlot: 511},
			{ShardID: 9, StartSlot: 512, EndSlot: 1023},
		},
	})
	if err != nil {
		t.Fatalf("NewPlacement: %v", err)
	}
	return placement
}

func e2eTransactionForShard(t *testing.T, placement routing.Placement, prefix string, shardID uint64) string {
	t.Helper()
	runPrefix := uniqueName(prefix)
	for i := range routing.SlotCount * 2 {
		txID := fmt.Sprintf("%s-%d", runPrefix, i)
		route, err := placement.Lookup(txID)
		if err != nil {
			t.Fatalf("lookup candidate Transaction: %v", err)
		}
		if route.ShardID == shardID {
			return txID
		}
	}
	t.Fatalf("no Transaction candidate routed to Shard %d", shardID)
	return ""
}

func assertE2ETransactionRoutesToShard(t *testing.T, placement routing.Placement, txID string, want uint64) {
	t.Helper()
	route, err := placement.Lookup(txID)
	if err != nil {
		t.Fatalf("Lookup %q: %v", txID, err)
	}
	if route.ShardID != want {
		t.Fatalf("Transaction route ShardID = %d, want %d", route.ShardID, want)
	}
}

func verifyS3ObjectMD5(t *testing.T, client *awss3.Client, object backendObject) {
	t.Helper()
	body := readS3Object(t, client, object)
	sum := md5.Sum(body) //nolint:gosec // S3 single-PUT ETags are MD5 hex values.
	if got, want := hex.EncodeToString(sum[:]), strings.Trim(object.etag, `"`); !strings.EqualFold(got, want) {
		t.Fatalf("object %s MD5 = %s, want ETag %s", object.key, got, want)
	}
}

func readS3Object(t *testing.T, client *awss3.Client, object backendObject) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(e2eS3Bucket()),
		Key:    aws.String(object.key),
	})
	if err != nil {
		t.Fatalf("GetObject %s: %v", object.key, err)
	}
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read %s: %v", object.key, err)
	}
	if int64(len(body)) != object.size {
		t.Fatalf("object %s size = %d, want %d", object.key, len(body), object.size)
	}
	return body
}

type uploadHealth struct {
	Status              string `json:"status"`
	UploadPressure      string `json:"upload_pressure"`
	UploadPressureLevel int    `json:"upload_pressure_level"`
	UploadPendingBytes  int64  `json:"upload_pending_bytes"`
	UploadPendingBlocks int    `json:"upload_pending_blocks"`
}

func waitUploadPendingBlocks(t *testing.T, pod string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		health := fetchUploadHealth(t, pod)
		if health.UploadPendingBlocks == want {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("pod %s upload_pending_blocks did not become %d", pod, want)
}

// e2eShardIDForTransaction resolves the Shard that owns a Transaction under the
// prod-like two-Shard placement.
func e2eShardIDForTransaction(t *testing.T, txID string) uint64 {
	t.Helper()
	route, err := e2eTwoShardPlacement(t).Lookup(txID)
	if err != nil {
		t.Fatalf("lookup Shard for %q: %v", txID, err)
	}
	return route.ShardID
}

// findShardDiagnostic returns the diagnostic entry for one Shard, if the pod
// reports it.
func findShardDiagnostic(diag e2eShardDiagnostics, shardID uint64) (e2eShardDiagnostic, bool) {
	for _, shard := range diag.Shards {
		if shard.ShardID == shardID {
			return shard, true
		}
	}
	return e2eShardDiagnostic{}, false
}

// waitShardUploadPendingBlocks polls one pod's per-Shard diagnostics until the
// named Shard drains to want pending Blocks. Unlike the pod-aggregated
// /healthz counter, this ignores unrelated pending uploads on the Cell's other
// Shard so the wait reflects only the Transaction under test.
func waitShardUploadPendingBlocks(t *testing.T, pod string, shardID uint64, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last e2eShardDiagnostics
	for time.Now().Before(deadline) {
		diag, errText := fetchShardDiagnosticsFromPod(t, pod)
		if errText == "" {
			last = diag
			if pending, ok := shardPendingBlocks(diag, shardID); ok && pending == want {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("pod %s Shard %d upload_pending_blocks did not become %d: %+v", pod, shardID, want, last.Shards)
}

// shardPendingBlocks resolves the pending upload Block count for the target
// Shard from a pod's live diagnostics. On the prod-like multi-Shard Cell it
// reads the matching Shard's per-Shard counter so unrelated pending uploads on
// the Cell's other Shard are ignored. On a single-Shard local Cell — where the
// Transaction's prod-like Shard ID is absent because the Cell falls back to
// Shard 0 — the only Shard's counter is already the Member total, so use it.
func shardPendingBlocks(diag e2eShardDiagnostics, shardID uint64) (int, bool) {
	if shard, ok := findShardDiagnostic(diag, shardID); ok {
		return shard.UploadPendingBlocks, true
	}
	if len(diag.Shards) == 1 {
		return diag.Shards[0].UploadPendingBlocks, true
	}
	return 0, false
}

func waitUploadPressureAtLeast(t *testing.T, pod string, minLevel int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		health := fetchUploadHealth(t, pod)
		if health.UploadPressureLevel >= minLevel {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("pod %s upload pressure did not reach level %d", pod, minLevel)
}

func fetchUploadHealth(t *testing.T, pod string) uploadHealth {
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
		t.Fatalf("GET healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("healthz status: got %d, want 200: %s", resp.StatusCode, string(body))
	}
	var health uploadHealth
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	return health
}

func waitCellMetricAbove(t *testing.T, name string, labels []string, previous float64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, pod := range podNames(t) {
			addr, stop := startPodPortForward(t, pod, 9100)
			current := fetchMetricValueWithLabels(t, e2eAdminURL(addr, "/metrics"), name, labels)
			stop()
			if current > previous {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("metric %s did not increase above %.0f on any pod", name, previous)
}

func waitCellMetricSumAbove(t *testing.T, name string, labels []string, previous float64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cellMetricSum(t, name, labels) > previous {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("metric %s cell-wide sum did not increase above %.0f", name, previous)
}

func cellMetricSum(t *testing.T, name string, labels []string) float64 {
	t.Helper()
	var total float64
	for _, pod := range podNames(t) {
		addr, stop := startPodPortForward(t, pod, 9100)
		current := fetchMetricValueWithLabels(t, e2eAdminURL(addr, "/metrics"), name, labels)
		stop()
		total += current
	}
	return total
}
