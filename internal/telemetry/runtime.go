package telemetry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"

	"go.opentelemetry.io/otel/metric"
)

const nsPerSecond = 1e9

type RuntimeMetrics struct {
	registration metric.Registration
}

func NewRuntimeMetrics(meter metric.Meter) (*RuntimeMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}

	goroutines, err := meter.Int64ObservableGauge("process.runtime.go.goroutines",
		metric.WithDescription("Number of goroutines."),
	)
	if err != nil {
		return nil, fmt.Errorf("create goroutines gauge: %w", err)
	}

	heapAlloc, err := meter.Int64ObservableGauge("process.runtime.go.mem.heap_alloc",
		metric.WithDescription("Bytes of allocated heap objects."),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("create heap alloc gauge: %w", err)
	}

	heapSys, err := meter.Int64ObservableGauge("process.runtime.go.mem.heap_sys",
		metric.WithDescription("Bytes of heap memory obtained from the OS."),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("create heap sys gauge: %w", err)
	}

	gcCount, err := meter.Int64ObservableCounter("process.runtime.go.gc.count",
		metric.WithDescription("Number of completed GC cycles."),
	)
	if err != nil {
		return nil, fmt.Errorf("create gc count counter: %w", err)
	}

	gcPauseTotal, err := meter.Float64ObservableCounter("process.runtime.go.gc.pause_total",
		metric.WithDescription("Cumulative GC pause time."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create gc pause counter: %w", err)
	}

	reg, err := meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)

			o.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
			o.ObserveInt64(heapAlloc, safeUint64ToInt64(ms.HeapAlloc))
			o.ObserveInt64(heapSys, safeUint64ToInt64(ms.HeapSys))
			o.ObserveInt64(gcCount, int64(ms.NumGC))
			o.ObserveFloat64(gcPauseTotal, float64(ms.PauseTotalNs)/nsPerSecond)
			return nil
		},
		goroutines, heapAlloc, heapSys, gcCount, gcPauseTotal,
	)
	if err != nil {
		return nil, fmt.Errorf("register runtime callback: %w", err)
	}

	return &RuntimeMetrics{registration: reg}, nil
}

func safeUint64ToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func (r *RuntimeMetrics) Unregister() error {
	if r == nil || r.registration == nil {
		return nil
	}
	if err := r.registration.Unregister(); err != nil {
		return fmt.Errorf("unregister runtime metrics: %w", err)
	}
	return nil
}
