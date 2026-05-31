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

	pair := waitNewBackendPair(t, s3Client, before, uploadE2ETimeout)
	verifyS3ObjectMD5(t, s3Client, pair.blk)
	verifyS3ObjectMD5(t, s3Client, pair.idx)

	leader := findLeaderPod(t, txID, docName)
	waitUploadPendingBlocks(t, leader, 0, uploadE2ETimeout)
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

	pair := waitNewBackendPair(t, s3Client, before, uploadE2ETimeout)
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
	lastTxID := ""
	lastDocName := ""
	rejected := false
	for i := range 6 {
		txID := uniqueName(fmt.Sprintf("tx-e2e-upload-pressure-%d", i))
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
		_, err := tryWriteDocE2E(t, client, uniqueName("tx-e2e-upload-pressure-probe"), "probe.bin", "application/octet-stream", []byte("probe"))
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
	if _, err := tryWriteDocE2E(t, client, uniqueName("tx-e2e-upload-resumed"), "resume.bin", "application/octet-stream", []byte("resume")); err != nil {
		t.Fatalf("write after upload drain: %v", err)
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
		return true
	}
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.GetReason() == "upload_pressure" {
			return true
		}
	}
	return true
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
	return e2eCellID() + "/shards/0000000000000000/"
}

func waitNewBackendPair(t *testing.T, client *awss3.Client, before map[string]backendObject, timeout time.Duration) backendPair {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pair, ok := firstCompleteBackendPair(listBackendObjects(t, client), before); ok {
			return pair
		}
		time.Sleep(time.Second)
	}
	t.Fatal("timed out waiting for uploaded .blk/.idx backend pair")
	return backendPair{}
}

func firstCompleteBackendPair(objects, before map[string]backendObject) (backendPair, bool) {
	for _, pair := range collectBackendPairs(objects, before) {
		if pair.blk.key != "" && pair.idx.key != "" {
			return pair, true
		}
	}
	return backendPair{}, false
}

func collectBackendPairs(objects, before map[string]backendObject) map[string]backendPair {
	pairs := make(map[string]backendPair)
	for key, object := range objects {
		if _, existed := before[key]; existed {
			continue
		}
		base, ext, ok := splitBackendObjectKey(key)
		if !ok || object.size <= 0 || strings.Trim(object.etag, `"`) == "" {
			continue
		}
		pair := pairs[base]
		if ext == "blk" {
			pair.blk = object
		} else {
			pair.idx = object
		}
		pairs[base] = pair
	}
	return pairs
}

func splitBackendObjectKey(key string) (base, ext string, ok bool) {
	switch {
	case strings.HasSuffix(key, ".blk"):
		return strings.TrimSuffix(key, ".blk"), "blk", true
	case strings.HasSuffix(key, ".idx"):
		return strings.TrimSuffix(key, ".idx"), "idx", true
	default:
		return "", "", false
	}
}

func verifyS3ObjectMD5(t *testing.T, client *awss3.Client, object backendObject) {
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
	sum := md5.Sum(body) //nolint:gosec // S3 single-PUT ETags are MD5 hex values.
	if got, want := hex.EncodeToString(sum[:]), strings.Trim(object.etag, `"`); !strings.EqualFold(got, want) {
		t.Fatalf("object %s MD5 = %s, want ETag %s", object.key, got, want)
	}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		t.Fatalf("new health request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
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
			current := fetchMetricValueWithLabels(t, "http://"+addr+"/metrics", name, labels)
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
		current := fetchMetricValueWithLabels(t, "http://"+addr+"/metrics", name, labels)
		stop()
		total += current
	}
	return total
}
