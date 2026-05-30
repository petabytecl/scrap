package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

const (
	defaultE2ENamespace  = "scrap"
	defaultKindCluster   = "scrap-local"
	scrubE2ETimeout      = 90 * time.Second
	portForwardWait      = 15 * time.Second
	kubectlCommandWait   = 30 * time.Second
	corruptPayloadOffset = 72
)

func TestMain(m *testing.M) {
	code := m.Run()
	if os.Getenv("SCRAP_E2E_CLEANUP") == "1" {
		cluster := envOrDefault("KIND_CLUSTER", defaultKindCluster)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		//nolint:gosec // E2E cleanup intentionally shells out to the local kind tool with a test-controlled cluster name.
		_ = exec.CommandContext(ctx, "go", "run", "sigs.k8s.io/kind@v0.31.0", "delete", "cluster", "--name", cluster).Run()
		cancel()
	}
	os.Exit(code)
}

func TestE2EDeepScrubDetectsAndRepairsBlock(t *testing.T) {
	requireE2E(t)

	client := connect(t)
	txID := uniqueName("tx-e2e-deep")
	content := bytes.Repeat([]byte("scrub-deep-payload-"), 4096)

	writeDocE2E(t, client, txID, "sealed.xml", "application/xml", content)
	writeDocE2E(t, client, txID, "seal-trigger.xml", "application/xml", []byte("seal"))

	leader := findLeaderPod(t, txID, "sealed.xml")
	metricsURL, stopMetrics := startPodPortForward(t, leader, 9100)
	defer stopMetrics()
	metricsEndpoint := "http://" + metricsURL + "/metrics"

	quarantinesBefore := fetchMetricValue(t, metricsEndpoint, "scrap_scrub_deep_quarantines_total")
	repairsBefore := fetchMetricValueWithLabels(t, metricsEndpoint, "scrap_scrub_deep_repairs_total", []string{`result="ok"`})

	corruptFirstBlock(t, leader)

	waitMetricAbove(t, metricsEndpoint, "scrap_scrub_deep_quarantines_total", nil, quarantinesBefore, scrubE2ETimeout)
	waitMetricAbove(t, metricsEndpoint, "scrap_scrub_deep_repairs_total", []string{`result="ok"`}, repairsBefore, scrubE2ETimeout)

	leaderAddr, stopClient := startPodPortForward(t, leader, 9090)
	defer stopClient()
	leaderClient := newDocumentClientAt(t, leaderAddr)
	readBack := readDocE2E(t, leaderClient, txID, "sealed.xml")
	if !bytes.Equal(readBack, content) {
		t.Fatalf("read repaired content: got %d bytes, want %d", len(readBack), len(content))
	}
}

func TestE2ELightScrubDetectsProjectionDivergence(t *testing.T) {
	requireE2E(t)

	client := connect(t)
	txID := uniqueName("tx-e2e-light")
	content := []byte("light scrub content")

	writeDocE2E(t, client, txID, "doc.xml", "text/xml", content)

	leader := findLeaderPod(t, txID, "doc.xml")
	victim := firstNonLeaderPod(t, leader)
	metricsURL, stopMetrics := startPodPortForward(t, leader, 9100)
	defer stopMetrics()
	metricsEndpoint := "http://" + metricsURL + "/metrics"

	mismatchesBefore := fetchMetricValueWithLabels(t, metricsEndpoint, "scrap_scrub_light_runs_total", []string{`result="mismatch"`})
	injectProjectionKey(t, victim, uniqueName("tx-e2e-divergent"))

	waitMetricAbove(t, metricsEndpoint, "scrap_scrub_light_runs_total", []string{`result="mismatch"`}, mismatchesBefore, scrubE2ETimeout)

	readBack := readDocE2E(t, client, txID, "doc.xml")
	if !bytes.Equal(readBack, content) {
		t.Fatalf("read after rebuild: got %d bytes, want %d", len(readBack), len(content))
	}
}

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("SCRAP_E2E") == "" {
		t.Skip("set SCRAP_E2E=1 to run E2E tests")
	}
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func namespace() string {
	return envOrDefault("SCRAP_E2E_NAMESPACE", defaultE2ENamespace)
}

func kubectlBin() string {
	return envOrDefault("SCRAP_E2E_KUBECTL", "kubectl")
}

func runKubectl(t *testing.T, timeout time.Duration, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	//nolint:gosec // E2E tests intentionally execute the configured kubectl binary against the test cluster.
	cmd := exec.CommandContext(ctx, kubectlBin(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func podNames(t *testing.T) []string {
	t.Helper()
	out := runKubectl(t, kubectlCommandWait, "-n", namespace(), "get", "pods", "-l", "app=scrap", "-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	lines := strings.Fields(out)
	if len(lines) == 0 {
		t.Fatal("no scrap pods found")
	}
	return lines
}

func firstNonLeaderPod(t *testing.T, leader string) string {
	t.Helper()
	for _, pod := range podNames(t) {
		if pod != leader {
			return pod
		}
	}
	t.Fatalf("no non-leader pod found; leader=%s", leader)
	return ""
}

func findLeaderPod(t *testing.T, txID, docName string) string {
	t.Helper()
	for _, pod := range podNames(t) {
		addr, stop := startPodPortForward(t, pod, 9090)
		client := newDocumentClientAt(t, addr)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
			TransactionId: txID,
			DocumentName:  docName,
		})
		cancel()
		stop()
		if err == nil {
			return pod
		}
	}
	t.Fatal("could not identify leader pod")
	return ""
}

func newDocumentClientAt(t *testing.T, addr string) scrapv1.DocumentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return scrapv1.NewDocumentServiceClient(conn)
}

func clientForLeaderHint(t *testing.T, err error) (scrapv1.DocumentServiceClient, bool) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unavailable {
		return nil, false
	}
	for _, detail := range st.Details() {
		hint, ok := detail.(*scrapv1.LeaderHint)
		if !ok || hint.GetLeaderAddr() == "" {
			continue
		}
		podName := strings.Split(hint.GetLeaderAddr(), ".")[0]
		if podName == "" {
			return nil, false
		}
		addr, _ := startPodPortForward(t, podName, 9090)
		return newDocumentClientAt(t, addr), true
	}
	return nil, false
}

func startPodPortForward(t *testing.T, pod string, remotePort int) (string, func()) {
	t.Helper()
	return startResourcePortForward(t, "pod/"+pod, remotePort)
}

func startResourcePortForward(t *testing.T, resource string, remotePort int) (string, func()) {
	t.Helper()
	localPort := freeLocalPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	//nolint:gosec // E2E tests intentionally execute the configured kubectl binary against the test cluster.
	cmd := exec.CommandContext(ctx, kubectlBin(),
		"-n", namespace(),
		"port-forward",
		resource,
		fmt.Sprintf("%d:%d", localPort, remotePort),
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start port-forward %s:%d: %v", resource, remotePort, err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	waitForTCP(t, addr, portForwardWait, output.String)

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			_ = cmd.Wait()
		})
	}
	t.Cleanup(stop)
	return addr, stop
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate local port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForTCP(t *testing.T, addr string, timeout time.Duration, debug func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port-forward did not become ready for %s\n%s", addr, debug())
}

func corruptFirstBlock(t *testing.T, pod string) {
	t.Helper()
	script := fmt.Sprintf(`set -eu
blk=$(ls /proc/1/root/data/blocks/*.blk | sort | head -n 1)
printf Z | dd of="$blk" bs=1 seek=%d conv=notrunc
`, corruptPayloadOffset)
	runKubectl(t, kubectlCommandWait,
		"-n", namespace(),
		"debug", "pod/"+pod,
		"--target=scrapd",
		"--image=busybox:1.36",
		"--share-processes",
		"--",
		"sh", "-c", script,
	)
}

func injectProjectionKey(t *testing.T, pod, txID string) {
	t.Helper()
	addr, stop := startPodPortForward(t, pod, 9100)
	defer stop()

	payload := map[string]any{
		"transaction_id": txID,
		"block_id":       999,
		"doc_count":      1,
		"completed":      false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal projection hook request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/test-hooks/projection-key", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new projection hook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST projection hook: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("projection hook status: got %d, want 204: %s", resp.StatusCode, string(respBody))
	}
}

func fetchMetricValue(t *testing.T, url, name string) float64 {
	t.Helper()
	return fetchMetricValueWithLabels(t, url, name, nil)
}

func fetchMetricValueWithLabels(t *testing.T, url, name string, labels []string) float64 {
	t.Helper()
	body := fetchMetrics(t, url)
	value, ok := metricValue(body, name, labels)
	if !ok {
		return 0
	}
	return value
}

func waitMetricAbove(t *testing.T, url, name string, labels []string, previous float64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if current := fetchMetricValueWithLabels(t, url, name, labels); current > previous {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("metric %s did not increase above %.0f", name, previous)
}

func fetchMetrics(t *testing.T, url string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new metrics request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET metrics status: got %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	return string(body)
}

func metricValue(body, name string, labels []string) (float64, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !metricNameMatches(fields[0], name) {
			continue
		}
		if !lineContainsLabels(fields[0], labels) {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		return value, true
	}
	return 0, false
}

func metricNameMatches(token, name string) bool {
	return token == name ||
		strings.HasPrefix(token, name+"{") ||
		token == name+"_total" ||
		strings.HasPrefix(token, name+"_total{")
}

func lineContainsLabels(metricToken string, labels []string) bool {
	for _, label := range labels {
		if !strings.Contains(metricToken, label) {
			return false
		}
	}
	return true
}

//nolint:gocognit,cyclop // test helper handles streaming reads, retry deadlines, and leader redirects.
func readDocE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	activeClient := client
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			break
		}
		stream, err := activeClient.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
			TransactionId: txID,
			DocumentName:  docName,
		})
		if err != nil {
			if leaderClient, redirected := clientForLeaderHint(t, err); redirected {
				activeClient = leaderClient
				waitBeforeRetry(ctx, attempt)
				continue
			}
			if status.Code(err) == codes.Unavailable {
				waitBeforeRetry(ctx, attempt)
				continue
			}
			t.Fatalf("ReadDocument: %v", err)
		}

		first, err := stream.Recv()
		if err != nil {
			if leaderClient, redirected := clientForLeaderHint(t, err); redirected {
				activeClient = leaderClient
				waitBeforeRetry(ctx, attempt)
				continue
			}
			if status.Code(err) == codes.Unavailable {
				waitBeforeRetry(ctx, attempt)
				continue
			}
			t.Fatalf("Recv meta: %v", err)
		}
		if first.GetMeta() == nil {
			t.Fatal("first ReadDocument message should be metadata")
		}

		var out []byte
		for {
			msg, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return out
			}
			if err != nil {
				t.Fatalf("Recv chunk: %v", err)
			}
			out = append(out, msg.GetChunkData()...)
		}
	}
	t.Fatal("ReadDocument failed after leader redirects")
	return nil
}

func headDocE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName string) *scrapv1.HeadDocumentResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	activeClient := client
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			break
		}
		resp, err := activeClient.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
			TransactionId: txID,
			DocumentName:  docName,
		})
		if err == nil {
			return resp
		}
		if leaderClient, redirected := clientForLeaderHint(t, err); redirected {
			activeClient = leaderClient
			waitBeforeRetry(ctx, attempt)
			continue
		}
		if status.Code(err) == codes.Unavailable {
			waitBeforeRetry(ctx, attempt)
			continue
		}
		t.Fatalf("HeadDocument: %v", err)
	}
	t.Fatal("HeadDocument failed after leader redirects")
	return nil
}
