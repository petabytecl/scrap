# S.C.R.A.P. Go Style Guide

This guide covers judgment calls that linters cannot enforce: design decisions,
naming intent, architectural patterns, testing philosophy, and performance
discipline. Mechanical formatting (imports, line length, `gofumpt` style) is
enforced by `.golangci.yml` and is not repeated here.

Both human developers and AI coding agents must follow this guide.

**Enforcement:** Code review (human or AI). The guide is referenced from
`CLAUDE.md` so AI agents always have it in context.

**References:** This guide draws from
[Google's Go Style Guide](https://google.github.io/styleguide/go/) and the
[Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md),
adapted to the S.C.R.A.P. codebase.

---

- [Design](#design)
  - [Package Structure](#package-structure)
  - [Interfaces](#interfaces)
  - [Initialization](#initialization)
  - [Receiver Types](#receiver-types)
- [Naming](#naming)
  - [No Stutter](#no-stutter)
  - [Error Naming](#error-naming)
  - [Constants and Config](#constants-and-config)
- [Errors](#errors)
  - [Sentinel Errors](#sentinel-errors)
  - [Custom Error Types](#custom-error-types)
  - [Wrapping Convention](#wrapping-convention)
  - [Handle Errors Once](#handle-errors-once)
- [Concurrency](#concurrency)
  - [Goroutine Ownership](#goroutine-ownership)
  - [Channel Sizing](#channel-sizing)
  - [Synchronous by Default](#synchronous-by-default)
  - [Mutex Placement](#mutex-placement)
- [Resource Lifecycle](#resource-lifecycle)
  - [Close, Stop, and Shutdown](#close-stop-and-shutdown)
  - [Defer for Cleanup](#defer-for-cleanup)
- [Testing](#testing)
  - [Table-Driven Tests](#table-driven-tests)
  - [No Assertion Libraries](#no-assertion-libraries)
  - [Test Helpers](#test-helpers)
  - [Test Doubles](#test-doubles)
- [Performance](#performance)
  - [Pre-Allocate Containers](#pre-allocate-containers)
  - [Prefer strconv over fmt](#prefer-strconv-over-fmt)
  - [Avoid Repeated String-to-Byte Conversions](#avoid-repeated-string-to-byte-conversions)
  - [Use sync.Pool on Hot Paths](#use-syncpool-on-hot-paths)
  - [Benchmark Before Optimizing](#benchmark-before-optimizing)
- [Metrics](#metrics)
  - [Naming Convention](#naming-convention)
  - [Interface Injection](#interface-injection)
  - [Registration](#registration)
- [Documentation](#documentation)
  - [Godoc on All Exports](#godoc-on-all-exports)
  - [Package Comments](#package-comments)

---

## Design

### Package Structure

All domain logic lives under `internal/`. Stable API contracts and reusable
types live under `pkg/`.

| Directory | Purpose | Rule |
|-----------|---------|------|
| `internal/` | Domain logic, subsystem implementations | One package per domain concept. No `util` or `common` packages. |
| `pkg/` | Stable interfaces, value types, error sentinels | Only packages with no concrete business logic. Must be safe for external consumers to import. |
| `cmd/` | Executable entry points | Thin `main()` that delegates to a `run()` function. |
| `proto/` | Protocol Buffer definitions | Source of truth for wire formats. |
| `gen/` | Generated code | Never edit by hand. Excluded from linting. |

### Interfaces

Define interfaces at the consumer, not the producer. Keep them to 1-3 methods.
Larger interfaces are acceptable only as top-level service contracts at package
boundaries.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
// package scrub

// Defined at the producer, bundling
// unrelated capabilities.
type ShardService interface {
  ProposeConsistencyCheck(ctx context.Context, id string) (Result, error)
  CheckConsistency(ctx context.Context, addr, id string) (Result, error)
  IsLeader() bool
  RequestRebuild(ctx context.Context, addr string) error
  WriteDocument(ctx context.Context, ...) error
}
```

</td><td>

```go
// package scrub

// Each interface captures one capability,
// defined where it is consumed.
type Proposer interface {
  ProposeConsistencyCheck(ctx context.Context, id string) (Result, error)
}

type ConsistencyChecker interface {
  CheckConsistency(ctx context.Context, addr, id string) (Result, error)
}

type LeaderChecker interface {
  IsLeader() bool
}
```

</td></tr>
</tbody></table>

The `store.Store` interface (4 methods) is an acceptable exception as a
top-level service contract. If it grows beyond ~6 methods, split it.

Always return concrete types, not interfaces. Let callers define the interface
they need.

### Initialization

Use config structs with a constructor as the default initialization pattern.
Reserve functional options for utility types with many optional knobs.

<table>
<thead><tr><th>Config Struct (default)</th><th>Functional Options (utility types)</th></tr></thead>
<tbody>
<tr><td>

```go
type Config struct {
  DataDir   string
  ShardID   uint64
  SealSize  int64
  Interval  time.Duration
}

func (c *Config) applyDefaults() {
  if c.SealSize <= 0 {
    c.SealSize = DefaultSealSize
  }
}

func Open(cfg Config) (*Shard, error) {
  cfg.applyDefaults()
  // ...
}
```

</td><td>

```go
type Option func(*Generator)

func WithClock(fn func() uint64) Option {
  return func(g *Generator) {
    g.clock = fn
  }
}

func NewGenerator(opts ...Option) *Generator {
  g := &Generator{
    clock: func() uint64 {
      return uint64(time.Now().UnixMilli())
    },
  }
  for _, o := range opts {
    o(g)
  }
  return g
}
```

</td></tr>
</tbody></table>

Config structs must validate and normalize via `applyDefaults()` before use.

### Receiver Types

Use pointer receivers by default. Use value receivers only for small, immutable
types that are never stored in collections and contain no sync primitives.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
// Value receiver on a type with a mutex.
// Copies the mutex, causing races.
func (s Shard) IsReady() bool {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.ready
}
```

</td><td>

```go
func (s *Shard) IsReady() bool {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.ready
}
```

</td></tr>
</tbody></table>

Be consistent within a type: if any method needs a pointer receiver, all
methods on that type should use pointer receivers.

---

## Naming

### No Stutter

Do not repeat the package name in type, function, or method names. The package
qualifier already provides context at every call site.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
package block

type BlockWriter struct { ... }
type BlockReader struct { ... }

// Call site: block.BlockWriter
```

</td><td>

```go
package block

type Writer struct { ... }
type Reader struct { ... }

// Call site: block.Writer
```

</td></tr>
<tr><td>

```go
package scrub

type LightScrubber struct { ... }
type DeepScrubber struct { ... }
type ScrubConfig struct { ... }
```

</td><td>

```go
package scrub

type Light struct { ... }
type Deep struct { ... }
type Config struct { ... }
```

</td></tr>
</tbody></table>

Qualifiers that add real meaning beyond the package name are fine:
`scrub.LightConfig` vs `scrub.DeepConfig` distinguishes two configs within the
same package.

### Error Naming

Sentinel errors use the `Err` prefix. Custom error types use the `Error`
suffix.

```go
// Sentinel errors — exported for matching via errors.Is.
var (
  ErrNotFound      = errors.New("document not found")
  ErrAlreadyExists = errors.New("document already exists")
  ErrDataLoss      = errors.New("data corruption detected")
)

// Custom error type — exported for matching via errors.As.
type NotLeaderError struct {
  LeaderAddr string
}

func (e *NotLeaderError) Error() string {
  if e.LeaderAddr == "" {
    return "not shard leader; leader unknown"
  }
  return "not shard leader; leader at " + e.LeaderAddr
}
```

### Constants and Config

Export constants that callers need. Use typed constants for durations and sizes.
Group related constants together.

```go
const (
  DefaultSealSize       = 64 * 1024 * 1024   // 64 MiB
  DefaultBootstrapGrace = 60 * time.Second
)
```

Config field names must be self-documenting. Include the unit in the name when
the type does not carry it (e.g., JSON serialization).

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
// What unit is Interval? Seconds? Millis?
type Config struct {
  Interval int `json:"interval"`
}
```

</td><td>

```go
type Config struct {
  IntervalMillis int `json:"intervalMillis"`
}

// Or better — use time.Duration in code:
type Config struct {
  Interval time.Duration
}
```

</td></tr>
</tbody></table>

---

## Errors

### Sentinel Errors

Use `errors.New` for package-level sentinel errors. Export them when callers
need to match via `errors.Is`.

```go
var ErrNotFound = errors.New("document not found")
```

Provide convenience matchers only when the check is frequent and improves
readability:

```go
func IsAlreadyExists(err error) bool {
  return errors.Is(err, ErrAlreadyExists)
}
```

### Custom Error Types

Use a custom error type when the error carries dynamic context that callers
need to extract via `errors.As`.

```go
type NotLeaderError struct {
  LeaderAddr string
}

func (e *NotLeaderError) Error() string { ... }
```

### Wrapping Convention

Always wrap errors with `%w`. Format: `"package: operation: %w"`. Avoid
`"failed to"` — it adds noise as errors propagate up the stack.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
return fmt.Errorf("failed to open index: %w", err)
// Chain: "failed to start shard: failed to open index: ..."
```

</td><td>

```go
return fmt.Errorf("shard: open index: %w", err)
// Chain: "shard: open index: pebble: open db: ..."
```

</td></tr>
</tbody></table>

For data loss errors, multi-wrap with the sentinel to allow both `errors.Is`
matching and context:

```go
return fmt.Errorf("%w: %w", store.ErrDataLoss, err)
```

### Handle Errors Once

Handle each error exactly once. Either wrap and return it, or log it and
degrade gracefully. Never log and return the same error.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
u, err := getUser(id)
if err != nil {
  log.Printf("could not get user %q: %v", id, err)
  return err // caller will also log it
}
```

</td><td>

```go
u, err := getUser(id)
if err != nil {
  return fmt.Errorf("get user %q: %w", id, err)
}
```

</td></tr>
</tbody></table>

---

## Concurrency

### Goroutine Ownership

Every goroutine must have an owner and a shutdown path. The owner is
responsible for:

1. Starting the goroutine.
2. Signaling it to stop (via `context.CancelFunc` or a stop channel).
3. Waiting for it to exit (via `sync.WaitGroup` or a done channel).

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
func (s *Server) Start() {
  // Fire-and-forget. No way to stop or wait.
  go s.processQueue()
}
```

</td><td>

```go
func (s *Light) Start(ctx context.Context) {
  ctx, s.cancel = context.WithCancel(ctx)
  s.wg.Add(1)
  go s.loop(ctx)
}

func (s *Light) Stop() {
  if s.cancel != nil {
    s.cancel()
  }
  s.wg.Wait()
}

func (s *Light) loop(ctx context.Context) {
  defer s.wg.Done()
  for {
    select {
    case <-time.After(s.interval()):
      _ = s.RunOnce(ctx)
    case <-ctx.Done():
      return
    }
  }
}
```

</td></tr>
</tbody></table>

### Channel Sizing

Channels should be unbuffered (size 0) or have a size of 1. Any larger buffer
requires a comment explaining the capacity choice and the backpressure
strategy.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
// Magic number. What happens when it fills?
ch := make(chan request, 64)
```

</td><td>

```go
ch := make(chan request) // unbuffered: synchronous handoff

// or

ch := make(chan request, 1) // buffer of 1: decouple send from receive

// Justified buffer: peer sender batches outbound Raft messages.
// senderBufferSize is tuned to absorb election-storm bursts
// without blocking the Raft loop. Backpressure drops messages
// when the buffer is full (leader retransmits).
ch := make(chan outbound, senderBufferSize)
```

</td></tr>
</tbody></table>

### Synchronous by Default

Write functions synchronously. Let the caller decide whether to run them in a
goroutine. A function that spawns internal goroutines hides concurrency from
the caller and makes testing harder.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
// Caller cannot control when this finishes.
func (s *Shard) Rebuild() {
  go func() {
    // ... rebuild logic
  }()
}
```

</td><td>

```go
// Caller controls concurrency.
func (s *Shard) Rebuild(ctx context.Context) error {
  // ... rebuild logic (synchronous)
  return nil
}

// Caller chooses:
go func() { _ = s.Rebuild(ctx) }()
```

</td></tr>
</tbody></table>

### Mutex Placement

Never embed mutexes. Declare them as unexported fields, placed immediately
above the fields they protect.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
type Shard struct {
  sync.Mutex  // Leaks Lock/Unlock into the API
  writer *Writer
}
```

</td><td>

```go
type Shard struct {
  mu     sync.Mutex
  writer *Writer
}
```

</td></tr>
</tbody></table>

Use `sync.RWMutex` when reads significantly outnumber writes. Group fields by
the mutex that guards them:

```go
type Shard struct {
  // ...

  mu          sync.Mutex
  blockWriter *block.Writer
  idxWriter   *block.IndexWriter
  nextBlockID uint64

  scrubMu     sync.RWMutex
  scrubResult *scrub.Result
}
```

---

## Resource Lifecycle

### Close, Stop, and Shutdown

Types that own resources (files, connections, goroutines) must provide a
lifecycle method:

| Method | Use when |
|--------|----------|
| `Close() error` | Type owns I/O resources (files, DB handles, network connections). |
| `Stop()` | Type owns goroutines but no I/O that can fail on close. |
| `Shutdown(ctx context.Context) error` | Type needs a graceful drain period (e.g., HTTP server). |

`Close()` must be idempotent. Closing an already-closed resource must not
panic.

When closing multiple sub-resources, collect errors and return the first:

```go
func (s *Shard) Close() error {
  s.scrubber.Stop()
  s.raft.Stop()

  s.mu.Lock()
  defer s.mu.Unlock()

  var firstErr error
  if s.idxWriter != nil {
    firstErr = s.idxWriter.Close()
  }
  if s.blockWriter != nil {
    if err := s.blockWriter.Close(); err != nil && firstErr == nil {
      firstErr = err
    }
  }
  if err := s.idx.Close(); err != nil && firstErr == nil {
    firstErr = err
  }
  return firstErr
}
```

### Defer for Cleanup

Always use `defer` to release resources. Assign close errors to `_` only for
read-only or non-critical resources.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
f, err := os.Open(path)
if err != nil {
  return err
}
data, err := io.ReadAll(f)
f.Close() // easy to miss on early returns
return data, err
```

</td><td>

```go
f, err := os.Open(path)
if err != nil {
  return err
}
defer func() { _ = f.Close() }() // read-only; close error is non-critical

data, err := io.ReadAll(f)
return data, err
```

</td></tr>
</tbody></table>

For writable resources, handle the close error:

```go
func writeBlock(path string, data []byte) (err error) {
  f, err := os.Create(path)
  if err != nil {
    return fmt.Errorf("block: create %s: %w", path, err)
  }
  defer func() {
    if cerr := f.Close(); cerr != nil && err == nil {
      err = fmt.Errorf("block: close %s: %w", path, cerr)
    }
  }()
  _, err = f.Write(data)
  return err
}
```

---

## Testing

### Table-Driven Tests

Use table-driven tests for functions with multiple input/output cases.
Name each case clearly.

```go
func TestParse(t *testing.T) {
  tests := []struct {
    name    string
    input   string
    want    ULID
    wantErr bool
  }{
    {name: "valid", input: "01ARZ3NDEKTSV4RRFFQ69G5FAV", want: mustParse("01ARZ3NDEKTSV4RRFFQ69G5FAV")},
    {name: "too short", input: "01ARZ", wantErr: true},
    {name: "empty", input: "", wantErr: true},
  }

  for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
      got, err := ulid.Parse(tt.input)
      if tt.wantErr {
        if err == nil {
          t.Fatal("expected error, got nil")
        }
        return
      }
      if err != nil {
        t.Fatalf("unexpected error: %v", err)
      }
      if got != tt.want {
        t.Errorf("got %v, want %v", got, tt.want)
      }
    })
  }
}
```

Use simple one-off tests when there is only a single meaningful case.

### No Assertion Libraries

Use the standard `testing` package. No testify, gomega, or other assertion
libraries. Assertions from these libraries obscure failure messages and add
dependency weight.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
assert.NoError(t, err)
assert.Equal(t, want, got)
```

</td><td>

```go
if err != nil {
  t.Fatalf("unexpected error: %v", err)
}
if got != want {
  t.Errorf("got %v, want %v", got, want)
}
```

</td></tr>
</tbody></table>

Use `t.Errorf` when the test can continue. Use `t.Fatalf` when further
assertions would be meaningless (e.g., nil pointer after a failed setup).

Report **got before want** in assertion messages for consistency.

### Test Helpers

Mark helpers with `t.Helper()` so failure reports point to the caller. Use
`t.TempDir()` for isolated storage. Use `t.Cleanup()` for teardown.

```go
func openTestShard(t *testing.T) *shard.Shard {
  t.Helper()
  dir := t.TempDir()
  s, err := shard.Open(shard.Config{DataDir: dir, ShardID: 1})
  if err != nil {
    t.Fatalf("open shard: %v", err)
  }
  t.Cleanup(func() { _ = s.Close() })
  return s
}
```

### Test Doubles

Use inline test doubles (stubs and fakes) in the test file. Keep them
minimal — implement only the methods the test exercises.

```go
type notLeaderStore struct {
  leaderAddr string
}

func (s *notLeaderStore) WriteDocument(_ context.Context, ...) (store.WriteResult, error) {
  return store.WriteResult{}, &store.NotLeaderError{LeaderAddr: s.leaderAddr}
}
```

---

## Performance

Performance-specific guidelines apply to the hot path: block frame encoding,
index lookups, and the write/read request path. For cold paths, prefer clarity
over optimization.

### Pre-Allocate Containers

Specify capacity when the size is known or can be estimated.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
results := make([]DocumentMeta, 0)
for _, entry := range entries {
  results = append(results, toMeta(entry))
}
```

</td><td>

```go
results := make([]DocumentMeta, 0, len(entries))
for _, entry := range entries {
  results = append(results, toMeta(entry))
}
```

</td></tr>
<tr><td>

```go
m := make(map[string]int)
for _, item := range items {
  m[item.Key] = item.Value
}
```

</td><td>

```go
m := make(map[string]int, len(items))
for _, item := range items {
  m[item.Key] = item.Value
}
```

</td></tr>
</tbody></table>

### Prefer strconv over fmt

When converting primitives to/from strings, `strconv` is faster and
allocates less than `fmt`.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
s := fmt.Sprint(blockID)
```

</td><td>

```go
s := strconv.FormatUint(blockID, 10)
```

</td></tr>
</tbody></table>

### Avoid Repeated String-to-Byte Conversions

Convert once and reuse.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
for i := 0; i < n; i++ {
  w.Write([]byte("SCRAP-BLK"))
}
```

</td><td>

```go
var blockMagic = []byte("SCRAP-BLK")

for i := 0; i < n; i++ {
  w.Write(blockMagic)
}
```

</td></tr>
</tbody></table>

### Use sync.Pool on Hot Paths

For frequently allocated objects in the request path (buffers, frame structs),
use `sync.Pool` to reduce GC pressure.

```go
var framePool = sync.Pool{
  New: func() any {
    return &Frame{buf: make([]byte, 0, 4096)}
  },
}

func processFrame(data []byte) {
  f := framePool.Get().(*Frame)
  defer framePool.Put(f)
  f.Reset()
  // ... use f
}
```

Only pool objects when profiling shows allocation pressure. Do not pool
speculatively.

### Benchmark Before Optimizing

Write benchmarks for hot paths. Optimize only what benchmarks prove is slow.
Include allocation counts in benchmark output.

```go
func BenchmarkWriteFrame(b *testing.B) {
  payload := make([]byte, 4096)
  var buf bytes.Buffer
  b.ResetTimer()
  b.ReportAllocs()
  for i := 0; i < b.N; i++ {
    buf.Reset()
    _ = WriteFrame(&buf, FrameHeader{Size: 4096}, payload)
  }
}
```

---

## Metrics

### Naming Convention

All Prometheus metrics follow the pattern:

```
scrap_<subsystem>_<metric>_<unit>
```

| Component | Rule | Examples |
|-----------|------|---------|
| Prefix | Always `scrap_` | |
| Subsystem | Domain concept, snake_case | `scrub`, `scrub_deep`, `block` |
| Metric | What is measured | `runs`, `duration`, `corruptions_detected` |
| Unit | Prometheus base unit | `_total` (counter), `_seconds` (duration), `_bytes` (size), `_ratio` (0-1 gauge) |

Examples from the codebase:

```
scrap_scrub_light_runs_total
scrap_scrub_light_duration_seconds
scrap_scrub_deep_progress_ratio
scrap_scrub_corruptions_detected_total
scrap_scrub_bad_disk_suspected
```

### Interface Injection

Define a metrics interface at the consumer with only the recording methods that
subsystem needs. Implement it with a concrete Prometheus type.

```go
// Consumer defines what it needs.
type ScrubMetrics interface {
  RecordRun(result string, durationSec float64)
}

// Producer implements it.
type PrometheusMetrics struct {
  runsTotal *prometheus.CounterVec
  duration  prometheus.Observer
}

func (m *PrometheusMetrics) RecordRun(result string, durationSec float64) {
  m.runsTotal.WithLabelValues(result).Inc()
  m.duration.Observe(durationSec)
}
```

This decouples subsystems from Prometheus and makes testing trivial (pass a
no-op or recording stub).

### Registration

Register all metrics at startup in the constructor. Use `MustRegister` since
registration failures at startup should crash the process. Use exponential
buckets for histogram durations.

```go
func NewPrometheusMetrics(reg prometheus.Registerer) *PrometheusMetrics {
  runs := prometheus.NewCounterVec(prometheus.CounterOpts{
    Name: "scrap_scrub_light_runs_total",
    Help: "Total number of light scrub runs by result.",
  }, []string{"result"})

  duration := prometheus.NewHistogram(prometheus.HistogramOpts{
    Name:    "scrap_scrub_light_duration_seconds",
    Help:    "Duration of light scrub runs in seconds.",
    Buckets: prometheus.ExponentialBuckets(1, 2, 8),
  })

  reg.MustRegister(runs, duration)
  return &PrometheusMetrics{runsTotal: runs, duration: duration}
}
```

---

## Documentation

### Godoc on All Exports

Every exported type, function, method, constant, and variable must have a
godoc comment. The comment starts with the symbol name.

<table>
<thead><tr><th>Bad</th><th>Good</th></tr></thead>
<tbody>
<tr><td>

```go
var ErrNotFound = errors.New("document not found")

type Config struct {
  DataDir string
  ShardID uint64
}

func Open(cfg Config) (*Shard, error) {
```

</td><td>

```go
// ErrNotFound is returned when a document does not exist.
var ErrNotFound = errors.New("document not found")

// Config holds the parameters for opening a shard.
type Config struct {
  // DataDir is the root directory for shard storage.
  DataDir string
  // ShardID uniquely identifies this shard in the cluster.
  ShardID uint64
}

// Open creates or opens a shard with the given configuration.
func Open(cfg Config) (*Shard, error) {
```

</td></tr>
</tbody></table>

Do not explain implementation details in godoc. Document **what** the symbol
does and any non-obvious constraints (thread safety, ownership, lifecycle).

### Package Comments

Every package must have a package-level comment in a `doc.go` file or at the
top of the primary source file.

```go
// Package scrub implements data integrity verification for stored blocks.
//
// Light scrubs compare CRC checksums across peers via Raft consensus.
// Deep scrubs verify every frame within a block against its stored checksum.
package scrub
```
