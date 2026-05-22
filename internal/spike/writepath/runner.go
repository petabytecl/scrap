package writepath

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type RunnerConfig struct {
	Dir                   string
	Transactions          int
	Concurrency           int
	ChunkSize             int
	Seed                  int64
	ReadBack              bool
	UseRaftBarrier        bool
	UseDurableRaftBarrier bool
	UseRaftClusterBarrier bool
}

type docPlan struct {
	TenantID      string
	TransactionID string
	DocumentName  string
	ContentType   string
	Size          int64
	PatternSeed   int64
}

type metricSet struct {
	mu              sync.Mutex
	writeACK        []time.Duration
	head            []time.Duration
	readFirstByte   []time.Duration
	readFull        []time.Duration
	documents       int
	bytesWritten    int64
	bytesRead       int64
	invariantErrors int
}

type raftBarrierReport struct {
	enabled bool
	mode    string
	applied int
	leader  uint64
}

func Run(ctx context.Context, cfg RunnerConfig, out io.Writer) error {
	cfg = normalizeConfig(cfg)
	raftModes := 0
	if cfg.UseRaftBarrier {
		raftModes++
	}
	if cfg.UseDurableRaftBarrier {
		raftModes++
	}
	if cfg.UseRaftClusterBarrier {
		raftModes++
	}
	if raftModes > 1 {
		return errors.New("choose only one raft barrier mode")
	}

	dir := cfg.Dir
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "scrap-write-path-spike-*")
		if err != nil {
			return err
		}
	}

	storeOptions := StoreOptions{}
	var raftBarrier *RaftCommitBarrier
	var raftClusterBarrier *RaftClusterCommitBarrier
	if cfg.UseRaftBarrier {
		var err error
		raftBarrier, err = NewRaftCommitBarrier()
		if err != nil {
			return err
		}
		defer raftBarrier.Close()
		storeOptions.MetadataCommitBarrier = raftBarrier
	}
	if cfg.UseDurableRaftBarrier {
		var err error
		raftBarrier, err = NewDurableRaftCommitBarrier(filepath.Join(dir, "raft"))
		if err != nil {
			return err
		}
		defer raftBarrier.Close()
		storeOptions.MetadataCommitBarrier = raftBarrier
	}
	if cfg.UseRaftClusterBarrier {
		var err error
		raftClusterBarrier, err = NewRaftClusterCommitBarrier()
		if err != nil {
			return err
		}
		defer raftClusterBarrier.Close()
		storeOptions.MetadataCommitBarrier = raftClusterBarrier
		storeOptions.MetadataReadBarrier = raftClusterBarrier
	}

	store, err := OpenStoreWithOptions(dir, storeOptions)
	if err != nil {
		return err
	}
	defer store.Close()

	server := grpc.NewServer(grpc.ForceServerCodec(gobCodec{}))
	RegisterStorageServer(server, NewServer(store))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	defer func() {
		server.Stop()
		<-serveErr
	}()

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(gobCodec{})),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := NewStorageClient(conn)
	plans := buildWorkload(cfg.Transactions, cfg.Seed)
	metrics, err := runWorkload(ctx, client, plans, cfg)
	if err != nil {
		return err
	}

	raftReport := raftBarrierReport{}
	if raftBarrier != nil {
		raftReport.enabled = true
		if cfg.UseDurableRaftBarrier {
			raftReport.mode = "single-node-durable"
		} else {
			raftReport.mode = "single-node"
		}
		raftReport.applied = raftBarrier.AppliedCount()
		raftReport.leader = 1
	}
	if raftClusterBarrier != nil {
		raftReport.enabled = true
		raftReport.mode = "three-node-cluster"
		raftReport.applied = raftClusterBarrier.AppliedCount()
		raftReport.leader = raftClusterBarrier.LeaderID()
	}

	writeReport(out, cfg, dir, plans, metrics, raftReport)
	return nil
}

func normalizeConfig(cfg RunnerConfig) RunnerConfig {
	if cfg.Transactions <= 0 {
		cfg.Transactions = 25
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 1024 * 1024
	}
	if cfg.Seed == 0 {
		cfg.Seed = 1
	}
	return cfg
}

func buildWorkload(transactions int, seed int64) []docPlan {
	r := rand.New(rand.NewSource(seed))
	plans := make([]docPlan, 0, transactions*4)
	for tx := 0; tx < transactions; tx++ {
		documents := 2 + r.Intn(6)
		for doc := 0; doc < documents; doc++ {
			plans = append(plans, docPlan{
				TenantID:      "tenant-a",
				TransactionID: fmt.Sprintf("tx-%06d", tx),
				DocumentName:  fmt.Sprintf("documents/doc-%02d.bin", doc),
				ContentType:   "application/octet-stream",
				Size:          sampleDocumentSize(r),
				PatternSeed:   r.Int63(),
			})
		}
	}
	return plans
}

func sampleDocumentSize(r *rand.Rand) int64 {
	p := r.Float64()
	switch {
	case p < 0.70:
		return sampleRange(r, 16*1024, 128*1024)
	case p < 0.95:
		return sampleRange(r, 128*1024, 4*1024*1024)
	case p < 0.999:
		return sampleRange(r, 4*1024*1024, 32*1024*1024)
	default:
		return 128 * 1024 * 1024
	}
}

func sampleRange(r *rand.Rand, minValue, maxValue int64) int64 {
	return minValue + r.Int63n(maxValue-minValue+1)
}

func runWorkload(ctx context.Context, client StorageClient, plans []docPlan, cfg RunnerConfig) (*metricSet, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan docPlan)
	metrics := &metricSet{}
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	setErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}

	for worker := 0; worker < cfg.Concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for plan := range jobs {
				if err := writeAndRead(ctx, client, plan, cfg, metrics); err != nil {
					setErr(err)
					return
				}
			}
		}()
	}

sendPlans:
	for _, plan := range plans {
		select {
		case <-ctx.Done():
			break sendPlans
		case jobs <- plan:
		}
	}
	close(jobs)
	wg.Wait()

	errMu.Lock()
	defer errMu.Unlock()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	return metrics, nil
}

func writeAndRead(ctx context.Context, client StorageClient, plan docPlan, cfg RunnerConfig, metrics *metricSet) error {
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		return err
	}

	start := time.Now()
	if err := stream.Send(&WriteDocumentRequest{Metadata: &WriteDocumentMetadata{
		TenantID:       plan.TenantID,
		TransactionID:  plan.TransactionID,
		DocumentName:   plan.DocumentName,
		ContentType:    plan.ContentType,
		ExpectedLength: plan.Size,
	}}); err != nil {
		return err
	}

	buf := make([]byte, cfg.ChunkSize)
	hasher := sha256.New()
	var sent int64
	for sent < plan.Size {
		size := int64(cfg.ChunkSize)
		if remaining := plan.Size - sent; remaining < size {
			size = remaining
		}
		chunk := buf[:size]
		fillPattern(chunk, plan.PatternSeed, sent)
		if _, err := hasher.Write(chunk); err != nil {
			return err
		}
		if err := stream.Send(&WriteDocumentRequest{Chunk: chunk}); err != nil {
			return err
		}
		sent += size
	}

	result, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}
	writeACK := time.Since(start)

	expectedHash := hex.EncodeToString(hasher.Sum(nil))
	invariantErrors := 0
	if result.ByteLength != plan.Size || result.LogicalSHA256 != expectedHash {
		invariantErrors++
	}

	headStart := time.Now()
	head, err := client.HeadDocument(ctx, &HeadDocumentRequest{
		TenantID:      plan.TenantID,
		TransactionID: plan.TransactionID,
		DocumentName:  plan.DocumentName,
	})
	if err != nil {
		return err
	}
	headLatency := time.Since(headStart)
	if head.Document.LogicalSHA256 != expectedHash || head.Document.ByteLength != plan.Size {
		invariantErrors++
	}

	var readFirstByte time.Duration
	var readFull time.Duration
	var bytesRead int64
	if cfg.ReadBack {
		readStart := time.Now()
		readStream, err := client.ReadDocument(ctx, &ReadDocumentRequest{
			TenantID:      plan.TenantID,
			TransactionID: plan.TransactionID,
			DocumentName:  plan.DocumentName,
		})
		if err != nil {
			return err
		}

		readHasher := sha256.New()
		seenFirstByte := false
		for {
			response, err := readStream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if response.Document != nil {
				continue
			}
			if len(response.Chunk) == 0 {
				continue
			}
			if !seenFirstByte {
				readFirstByte = time.Since(readStart)
				seenFirstByte = true
			}
			if _, err := readHasher.Write(response.Chunk); err != nil {
				return err
			}
			bytesRead += int64(len(response.Chunk))
		}
		readFull = time.Since(readStart)
		if bytesRead != plan.Size || hex.EncodeToString(readHasher.Sum(nil)) != expectedHash {
			invariantErrors++
		}
	}

	metrics.record(writeACK, headLatency, readFirstByte, readFull, plan.Size, bytesRead, invariantErrors)
	return nil
}

func fillPattern(dst []byte, seed int64, offset int64) {
	state := uint64(seed) + uint64(offset)*0x9e3779b97f4a7c15
	for i := range dst {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		dst[i] = byte(state)
	}
}

func (m *metricSet) record(writeACK, head, readFirstByte, readFull time.Duration, bytesWritten, bytesRead int64, invariantErrors int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.writeACK = append(m.writeACK, writeACK)
	m.head = append(m.head, head)
	if readFirstByte > 0 {
		m.readFirstByte = append(m.readFirstByte, readFirstByte)
	}
	if readFull > 0 {
		m.readFull = append(m.readFull, readFull)
	}
	m.documents++
	m.bytesWritten += bytesWritten
	m.bytesRead += bytesRead
	m.invariantErrors += invariantErrors
}

func writeReport(out io.Writer, cfg RunnerConfig, dir string, plans []docPlan, metrics *metricSet, raftReport raftBarrierReport) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Fprintln(out, "S.C.R.A.P. write path spike")
	fmt.Fprintln(out, "status: disposable prototype")
	fmt.Fprintf(out, "scratch_dir: %s\n", dir)
	fmt.Fprintf(out, "transactions: %d\n", cfg.Transactions)
	fmt.Fprintf(out, "documents_planned: %d\n", len(plans))
	fmt.Fprintf(out, "documents_completed: %d\n", metrics.documents)
	fmt.Fprintf(out, "concurrency: %d\n", cfg.Concurrency)
	fmt.Fprintf(out, "chunk_size: %d\n", cfg.ChunkSize)
	fmt.Fprintf(out, "raft_barrier: %t\n", raftReport.enabled)
	if raftReport.enabled {
		fmt.Fprintf(out, "raft_barrier_mode: %s\n", raftReport.mode)
		fmt.Fprintf(out, "raft_leader: %d\n", raftReport.leader)
		fmt.Fprintf(out, "raft_applied_commands: %d\n", raftReport.applied)
	}
	fmt.Fprintf(out, "bytes_written: %d\n", metrics.bytesWritten)
	fmt.Fprintf(out, "bytes_read: %d\n", metrics.bytesRead)
	fmt.Fprintf(out, "invariant_errors: %d\n", metrics.invariantErrors)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "write_ack: %s\n", summarizeDurations(metrics.writeACK))
	fmt.Fprintf(out, "head_document: %s\n", summarizeDurations(metrics.head))
	fmt.Fprintf(out, "read_first_byte: %s\n", summarizeDurations(metrics.readFirstByte))
	fmt.Fprintf(out, "read_full: %s\n", summarizeDurations(metrics.readFull))
	fmt.Fprintln(out)
	fmt.Fprintf(out, "heap_alloc: %d\n", mem.HeapAlloc)
	fmt.Fprintf(out, "total_alloc: %d\n", mem.TotalAlloc)
	fmt.Fprintf(out, "gc_cycles: %d\n", mem.NumGC)
	fmt.Fprintf(out, "gc_pause_total_ns: %d\n", mem.PauseTotalNs)
	fmt.Fprintf(out, "goroutines: %d\n", runtime.NumGoroutine())
}

func summarizeDurations(values []time.Duration) string {
	if len(values) == 0 {
		return "n=0"
	}
	copied := append([]time.Duration(nil), values...)
	sort.Slice(copied, func(i, j int) bool {
		return copied[i] < copied[j]
	})
	return fmt.Sprintf(
		"n=%d p50=%s p95=%s p99=%s",
		len(copied),
		percentile(copied, 0.50),
		percentile(copied, 0.95),
		percentile(copied, 0.99),
	)
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*p)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}
