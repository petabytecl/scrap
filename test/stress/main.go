package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

const (
	chunkSize        = 4096
	maxWriteAttempts = 10
	progressInterval = 5 * time.Second
	pollInterval     = 50 * time.Millisecond
	writerFraction   = 2
	readerFraction   = 4
	p50              = 50
	p95              = 95
	p99              = 99
	roundFactor      = 100
	kib              = 1024
	mib              = kib * kib
	gib              = kib * mib
	pctDivisor       = 100
)

func main() {
	cfg := parseFlags()
	logf("scenario=%s addr=%s workers=%d duration=%s doc_size=%s",
		cfg.scenario, cfg.addr, cfg.workers, cfg.duration, formatBytes(cfg.docSize))

	pool := newClientPool(cfg.addr, cfg.workers)
	defer pool.close()

	switch cfg.scenario {
	case "throughput":
		runThroughput(pool, cfg)
	case "mixed":
		runMixed(pool, cfg)
	case "pressure":
		runPressure(pool, cfg)
	default:
		fatalf("unknown scenario: %s", cfg.scenario)
	}
}

// clientPool holds multiple gRPC connections to the same address. K8s NodePort
// does L4 load balancing per TCP connection, while gRPC multiplexes all RPCs
// over a single HTTP/2 connection. Creating one connection per worker ensures
// that workers are distributed across backend pods.
type clientPool struct {
	conns   []*grpc.ClientConn
	clients []scrapv1.DocumentServiceClient
}

func newClientPool(addr string, n int) *clientPool {
	p := &clientPool{
		conns:   make([]*grpc.ClientConn, n),
		clients: make([]scrapv1.DocumentServiceClient, n),
	}
	for i := range n {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			fatalf("dial %s (conn %d): %v", addr, i, err)
		}
		p.conns[i] = conn
		p.clients[i] = scrapv1.NewDocumentServiceClient(conn)
	}
	return p
}

func (p *clientPool) get(i int) scrapv1.DocumentServiceClient {
	return p.clients[i%len(p.clients)]
}

func (p *clientPool) close() {
	for _, conn := range p.conns {
		_ = conn.Close()
	}
}

type config struct {
	addr     string
	scenario string
	workers  int
	duration time.Duration
	docSize  int
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:18090", "scrapd gRPC address")
	flag.StringVar(&cfg.scenario, "scenario", "throughput", "stress scenario: throughput, mixed, pressure")
	flag.IntVar(&cfg.workers, "workers", 8, "concurrent worker goroutines")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second, "test duration")
	flag.IntVar(&cfg.docSize, "doc-size", 16384, "document payload size in bytes")
	flag.Parse()
	return cfg
}

func runThroughput(pool *clientPool, cfg config) {
	logf("starting throughput scenario")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()

	var stats runStats
	var wg sync.WaitGroup

	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats.logProgress()
			}
		}
	}()

	for i := range cfg.workers {
		client := pool.get(i)
		wg.Go(func() {
			payload := randomPayload(cfg.docSize)
			for seq := 0; ctx.Err() == nil; seq++ {
				txID := fmt.Sprintf("stress-tp-%d-%d-%d", i, time.Now().UnixNano(), seq)
				docName := fmt.Sprintf("doc-%d.bin", seq)
				start := time.Now()
				err := writeDoc(ctx, client, cfg.addr, txID, docName, payload)
				elapsed := time.Since(start)
				stats.record(elapsed, err, len(payload))
			}
		})
	}

	wg.Wait()
	result := stats.finalize("throughput")
	emitJSON(result)
}

func runMixed(pool *clientPool, cfg config) {
	logf("starting mixed scenario (write + read + head)")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()

	var writeStats, readStats, headStats runStats
	var written writtenDocs

	var wg sync.WaitGroup

	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ws := writeStats.snapshot()
				rs := readStats.snapshot()
				hs := headStats.snapshot()
				logf("writes=%d reads=%d heads=%d errors=%d",
					ws.total, rs.total, hs.total,
					ws.errors+rs.errors+hs.errors)
			}
		}
	}()

	writerCount := max(1, cfg.workers/writerFraction)
	readerCount := max(1, cfg.workers/readerFraction)
	headerCount := cfg.workers - writerCount - readerCount

	workerIdx := 0
	for i := range writerCount {
		client := pool.get(workerIdx)
		workerIdx++
		wg.Go(func() {
			mixedWriter(ctx, client, cfg.addr, cfg.docSize, i, &writeStats, &written)
		})
	}

	for range readerCount {
		client := pool.get(workerIdx)
		workerIdx++
		wg.Go(func() {
			mixedReader(ctx, client, cfg.addr, &readStats, &written)
		})
	}

	for range headerCount {
		client := pool.get(workerIdx)
		workerIdx++
		wg.Go(func() {
			mixedHeader(ctx, client, cfg.addr, &headStats, &written)
		})
	}

	wg.Wait()

	results := map[string]any{
		"scenario": "mixed",
		"write":    writeStats.finalize("write"),
		"read":     readStats.finalize("read"),
		"head":     headStats.finalize("head"),
	}
	emitJSON(results)
}

func mixedWriter(ctx context.Context, client scrapv1.DocumentServiceClient, addr string, docSize, workerID int, stats *runStats, written *writtenDocs) {
	payload := randomPayload(docSize)
	checksum := sha256Hex(payload)
	for seq := 0; ctx.Err() == nil; seq++ {
		txID := fmt.Sprintf("stress-mx-w-%d-%d-%d", workerID, time.Now().UnixNano(), seq)
		docName := fmt.Sprintf("doc-%d.bin", seq)
		start := time.Now()
		err := writeDoc(ctx, client, addr, txID, docName, payload)
		elapsed := time.Since(start)
		stats.record(elapsed, err, len(payload))
		if err == nil {
			written.add(txID, docName, checksum, int64(len(payload)))
		}
	}
}

func mixedReader(ctx context.Context, client scrapv1.DocumentServiceClient, addr string, stats *runStats, written *writtenDocs) {
	activeClient := client
	for ctx.Err() == nil {
		doc := written.random()
		if doc == nil {
			time.Sleep(pollInterval)
			continue
		}
		start := time.Now()
		err := readAndVerify(ctx, activeClient, doc)
		elapsed := time.Since(start)
		stats.record(elapsed, err, 0)
		if status.Code(err) == codes.Unavailable {
			if fresh := reconnect(addr); fresh != nil {
				activeClient = fresh
			}
		}
	}
}

func mixedHeader(ctx context.Context, client scrapv1.DocumentServiceClient, addr string, stats *runStats, written *writtenDocs) {
	activeClient := client
	for ctx.Err() == nil {
		doc := written.random()
		if doc == nil {
			time.Sleep(pollInterval)
			continue
		}
		start := time.Now()
		err := headAndVerify(ctx, activeClient, doc)
		elapsed := time.Since(start)
		stats.record(elapsed, err, 0)
		if status.Code(err) == codes.Unavailable {
			if fresh := reconnect(addr); fresh != nil {
				activeClient = fresh
			}
		}
	}
}

func runPressure(pool *clientPool, cfg config) {
	logf("starting pressure scenario")
	logf("phase 1: writing until upload pressure triggers")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()

	var stats runStats
	var pressureHits atomic.Int64

	payload := randomPayload(cfg.docSize)
	var wg sync.WaitGroup

	for i := range cfg.workers {
		client := pool.get(i)
		wg.Go(func() {
			for seq := 0; ctx.Err() == nil; seq++ {
				txID := fmt.Sprintf("stress-pr-%d-%d-%d", i, time.Now().UnixNano(), seq)
				start := time.Now()
				err := writeDoc(ctx, client, cfg.addr, txID, fmt.Sprintf("doc-%d.bin", seq), payload)
				elapsed := time.Since(start)
				stats.record(elapsed, err, len(payload))

				if isUploadPressure(err) {
					n := pressureHits.Add(1)
					if n == 1 {
						logf("upload pressure triggered after %d total writes", stats.snapshot().total)
					}
				}
			}
		})
	}

	wg.Wait()
	snap := stats.snapshot()
	pressure := pressureHits.Load()

	result := map[string]any{
		"scenario":            "pressure",
		"total_writes":        snap.total,
		"successful_writes":   snap.total - snap.errors,
		"pressure_rejections": pressure,
		"other_errors":        snap.errors - pressure,
		"total_bytes_written": snap.totalBytes,
		"latency":             snap.latencyStats(),
		"error_breakdown":     snap.errorBreakdown,
	}
	emitJSON(result)
}

//nolint:gocognit,cyclop // streaming gRPC client with retry and error classification
func writeDoc(ctx context.Context, client scrapv1.DocumentServiceClient, addr, txID, docName string, data []byte) error {
	var lastErr error
	activeClient := client

	for attempt := range maxWriteAttempts {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		stream, err := activeClient.WriteDocument(ctx)
		if err != nil {
			lastErr = err
			backoff(ctx, attempt)
			continue
		}

		if err := stream.Send(&scrapv1.WriteDocumentRequest{
			Part: &scrapv1.WriteDocumentRequest_Init{
				Init: &scrapv1.WriteDocumentInit{
					TransactionId: txID,
					DocumentName:  docName,
					ContentType:   "application/octet-stream",
				},
			},
		}); err != nil {
			lastErr = err
			continue
		}

		chunkErr := false
		for i := 0; i < len(data); i += chunkSize {
			end := min(i+chunkSize, len(data))
			if err := stream.Send(&scrapv1.WriteDocumentRequest{
				Part: &scrapv1.WriteDocumentRequest_ChunkData{
					ChunkData: data[i:end],
				},
			}); err != nil {
				lastErr = err
				chunkErr = true
				break
			}
		}
		if chunkErr {
			continue
		}

		_, err = stream.CloseAndRecv()
		if err == nil {
			return nil
		}
		lastErr = err

		if isUploadPressure(err) {
			return err
		}

		if status.Code(err) == codes.Unavailable {
			// Reconnect to get a fresh TCP connection routed to a different pod.
			if freshClient := reconnect(addr); freshClient != nil {
				activeClient = freshClient
			}
			backoff(ctx, attempt)
			continue
		}
		return err
	}
	return lastErr
}

func reconnect(addr string) scrapv1.DocumentServiceClient {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	return scrapv1.NewDocumentServiceClient(conn)
}

func readAndVerify(ctx context.Context, client scrapv1.DocumentServiceClient, doc *docRef) error {
	stream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
		TransactionId: doc.txID,
		DocumentName:  doc.docName,
	})
	if err != nil {
		return fmt.Errorf("read document: %w", err)
	}

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv meta: %w", err)
	}
	if first.GetMeta() == nil {
		return errors.New("expected meta message first")
	}
	if first.GetMeta().GetSize() != doc.size {
		return fmt.Errorf("size mismatch: got %d want %d", first.GetMeta().GetSize(), doc.size)
	}

	var buf bytes.Buffer
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("recv chunk: %w", err)
		}
		buf.Write(msg.GetChunkData())
	}

	got := sha256Hex(buf.Bytes())
	if got != doc.checksum {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, doc.checksum)
	}
	return nil
}

func headAndVerify(ctx context.Context, client scrapv1.DocumentServiceClient, doc *docRef) error {
	resp, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: doc.txID,
		DocumentName:  doc.docName,
	})
	if err != nil {
		return fmt.Errorf("head document: %w", err)
	}
	if resp.GetSize() != doc.size {
		return fmt.Errorf("head size mismatch: got %d want %d", resp.GetSize(), doc.size)
	}
	if resp.GetSha256Checksum() != doc.checksum {
		return fmt.Errorf("head checksum mismatch: got %s want %s", resp.GetSha256Checksum(), doc.checksum)
	}
	return nil
}

func isUploadPressure(err error) bool {
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

func backoff(ctx context.Context, attempt int) {
	delay := min(time.Duration(attempt+1)*100*time.Millisecond, time.Second)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

type docRef struct {
	txID     string
	docName  string
	checksum string
	size     int64
}

type writtenDocs struct {
	mu   sync.Mutex
	docs []*docRef
}

func (w *writtenDocs) add(txID, docName, checksum string, size int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.docs = append(w.docs, &docRef{txID: txID, docName: docName, checksum: checksum, size: size})
}

func (w *writtenDocs) random() *docRef {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.docs) == 0 {
		return nil
	}
	var b [1]byte
	_, _ = rand.Read(b[:])
	return w.docs[int(b[0])%len(w.docs)]
}

type statsSnapshot struct {
	total          int64
	errors         int64
	totalBytes     int64
	latencies      []time.Duration
	errorBreakdown map[string]int64
}

func (s *statsSnapshot) latencyStats() map[string]string {
	if len(s.latencies) == 0 {
		return map[string]string{}
	}
	sorted := make([]time.Duration, len(s.latencies))
	copy(sorted, s.latencies)
	slices.Sort(sorted)

	return map[string]string{
		"p50":  sorted[percentileIndex(len(sorted), p50)].String(),
		"p95":  sorted[percentileIndex(len(sorted), p95)].String(),
		"p99":  sorted[percentileIndex(len(sorted), p99)].String(),
		"min":  sorted[0].String(),
		"max":  sorted[len(sorted)-1].String(),
		"mean": (time.Duration(int64(mean(sorted)))).String(),
	}
}

type runStats struct {
	mu             sync.Mutex
	total          int64
	errors         int64
	totalBytes     int64
	latencies      []time.Duration
	errorBreakdown map[string]int64
	startOnce      sync.Once
	start          time.Time
}

func (s *runStats) record(elapsed time.Duration, err error, payloadBytes int) {
	s.startOnce.Do(func() { s.start = time.Now() })
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	s.latencies = append(s.latencies, elapsed)
	if err != nil {
		s.errors++
		if s.errorBreakdown == nil {
			s.errorBreakdown = make(map[string]int64)
		}
		s.errorBreakdown[classifyError(err)]++
	} else {
		s.totalBytes += int64(payloadBytes)
	}
}

func (s *runStats) snapshot() statsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	lat := make([]time.Duration, len(s.latencies))
	copy(lat, s.latencies)
	eb := make(map[string]int64)
	maps.Copy(eb, s.errorBreakdown)
	return statsSnapshot{
		total:          s.total,
		errors:         s.errors,
		totalBytes:     s.totalBytes,
		latencies:      lat,
		errorBreakdown: eb,
	}
}

func (s *runStats) logProgress() {
	snap := s.snapshot()
	s.mu.Lock()
	started := s.start
	s.mu.Unlock()
	if started.IsZero() {
		logf("waiting for first operation...")
		return
	}
	elapsed := time.Since(started).Truncate(time.Second)
	rate := float64(0)
	if elapsed > 0 {
		rate = float64(snap.total) / elapsed.Seconds()
	}
	logf("elapsed=%s ops=%d errors=%d rate=%.1f/s bytes=%s",
		elapsed, snap.total, snap.errors, rate, formatBytes(int(snap.totalBytes)))
}

func (s *runStats) finalize(label string) map[string]any {
	snap := s.snapshot()
	elapsed := time.Since(s.start)
	rate := float64(0)
	if elapsed > 0 {
		rate = float64(snap.total-snap.errors) / elapsed.Seconds()
	}

	result := map[string]any{
		"scenario":        label,
		"duration":        elapsed.Truncate(time.Millisecond).String(),
		"total_ops":       snap.total,
		"successful_ops":  snap.total - snap.errors,
		"failed_ops":      snap.errors,
		"ops_per_second":  math.Round(rate*roundFactor) / roundFactor,
		"total_bytes":     snap.totalBytes,
		"throughput_mbps": math.Round(float64(snap.totalBytes)/elapsed.Seconds()/mib*roundFactor) / roundFactor,
		"latency":         snap.latencyStats(),
		"error_breakdown": snap.errorBreakdown,
	}

	logf("--- %s results ---", label)
	logf("duration:    %s", elapsed.Truncate(time.Millisecond))
	logf("total ops:   %d (success: %d, failed: %d)", snap.total, snap.total-snap.errors, snap.errors)
	logf("ops/sec:     %.1f", rate)
	logf("throughput:  %.2f MiB/s", float64(snap.totalBytes)/elapsed.Seconds()/mib)
	if ls := snap.latencyStats(); len(ls) > 0 {
		logf("latency:     p50=%s p95=%s p99=%s", ls["p50"], ls["p95"], ls["p99"])
	}
	if len(snap.errorBreakdown) > 0 {
		logf("errors:      %v", snap.errorBreakdown)
	}

	return result
}

func classifyError(err error) string {
	if isUploadPressure(err) {
		return "upload_pressure"
	}
	code := status.Code(err)
	switch code {
	case codes.Unavailable:
		return "unavailable"
	case codes.AlreadyExists:
		return "already_exists"
	case codes.ResourceExhausted:
		return "resource_exhausted"
	case codes.DeadlineExceeded:
		return "deadline_exceeded"
	case codes.Canceled:
		return "canceled"
	default:
		return code.String()
	}
}

func randomPayload(size int) []byte {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		fatalf("random payload: %v", err)
	}
	return buf
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func percentileIndex(n, p int) int {
	idx := int(math.Ceil(float64(n)*float64(p)/pctDivisor)) - 1
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

func mean(durations []time.Duration) float64 {
	if len(durations) == 0 {
		return 0
	}
	var sum int64
	for _, d := range durations {
		sum += int64(d)
	}
	return float64(sum) / float64(len(durations))
}

func formatBytes(b int) string {
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/mib)
	case b >= kib:
		return fmt.Sprintf("%.1f KiB", float64(b)/kib)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatalf("encode result: %v", err)
	}
}

func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "[stress] %s %s\n", time.Now().Format("15:04:05"), msg)
}

func fatalf(format string, args ...any) {
	logf(format, args...)
	os.Exit(1)
}
