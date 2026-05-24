package writepipelineevidence

import (
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/petabytecl/scrap/internal/observe"
)

const (
	ComponentSignalObserved    = "observed"
	ComponentSignalSampled     = "sampled"
	ComponentSignalNotObserved = "not-observed"

	componentSignalSampleInterval = time.Millisecond
)

type componentSignalSampler struct {
	stop   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	gauges map[string]gaugeObservation
}

type gaugeObservation struct {
	current float64
	max     float64
	samples uint64
}

type histogramObservation struct {
	count uint64
	sum   float64
}

type gaugeSample struct {
	value float64
	ok    bool
}

func startComponentSignalSampler() *componentSignalSampler {
	sampler := &componentSignalSampler{
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		gauges: make(map[string]gaugeObservation),
	}
	go sampler.run()
	return sampler
}

func (s *componentSignalSampler) run() {
	defer close(s.done)
	ticker := time.NewTicker(componentSignalSampleInterval)
	defer ticker.Stop()
	s.sample()
	for {
		select {
		case <-ticker.C:
			s.sample()
		case <-s.stop:
			return
		}
	}
}

func (s *componentSignalSampler) stopAndBuild(require bool) []ComponentSignal {
	close(s.stop)
	<-s.done
	families, err := observe.GatherMetrics()
	if err != nil {
		return unavailableComponentSignals(require)
	}
	s.sampleFamilies(families)
	return s.buildSignals(families, require)
}

func (s *componentSignalSampler) sample() {
	families, err := observe.GatherMetrics()
	if err != nil {
		return
	}
	s.sampleFamilies(families)
}

func (s *componentSignalSampler) sampleFamilies(families []*dto.MetricFamily) {
	values := map[string]gaugeSample{
		"block_append_queue_depth": gaugeValue(families, "scrap_block_append_queue_depth"),
		"raft_queue_depth":         gaugeValue(families, "scrap_raft_queue_depth"),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for name, sample := range values {
		if !sample.ok {
			continue
		}
		observation := s.gauges[name]
		observation.current = sample.value
		if sample.value > observation.max {
			observation.max = sample.value
		}
		observation.samples++
		s.gauges[name] = observation
	}
}

func (s *componentSignalSampler) buildSignals(families []*dto.MetricFamily, require bool) []ComponentSignal {
	blockBatch := histogramValue(families, "scrap_block_sync_batch_size", nil)
	blockLatency := histogramValue(families, "scrap_block_sync_latency_seconds", map[string]string{"outcome": observe.OutcomeSuccess})
	metadataBatch := histogramValue(families, "scrap_metadata_command_batch_size", nil)
	metadataLatency := histogramValue(families, "scrap_metadata_command_sync_latency_seconds", map[string]string{"outcome": observe.OutcomeSuccess})

	s.mu.Lock()
	blockQueue := s.gauges["block_append_queue_depth"]
	raftQueue := s.gauges["raft_queue_depth"]
	s.mu.Unlock()

	return []ComponentSignal{
		histogramSignal(
			"block_append_sync_latency",
			"scrap_block_sync_latency_seconds observed durable block sync latency for successful syncs",
			"seconds",
			blockLatency,
			require,
		),
		gaugeSignal(
			"block_append_queue_depth",
			"scrap_block_append_queue_depth sampled append callers waiting for durable sync during the run",
			"waiters",
			blockQueue,
		),
		histogramSignal(
			"block_sync_batch_size",
			"scrap_block_sync_batch_size observed append waiters completed by one durable sync",
			"waiters_per_sync",
			blockBatch,
			require,
		),
		histogramSignal(
			"metadata_command_sync_latency",
			"scrap_metadata_command_sync_latency_seconds observed durable metadata command-log sync latency for successful syncs",
			"seconds",
			metadataLatency,
			require,
		),
		histogramSignal(
			"metadata_command_batch_size",
			"scrap_metadata_command_batch_size observed commands completed by one durable command-log sync",
			"commands_per_sync",
			metadataBatch,
			require,
		),
		gaugeSignal(
			"raft_queue_depth",
			"scrap_raft_queue_depth sampled metadata proposals pending append or apply during the run",
			"proposals",
			raftQueue,
		),
	}
}

func histogramSignal(name, observation, unit string, value histogramObservation, require bool) ComponentSignal {
	status := ComponentSignalNotObserved
	var average *float64
	if value.count > 0 {
		status = ComponentSignalObserved
		avg := value.sum / float64(value.count)
		average = &avg
	}
	sum := value.sum
	return ComponentSignal{
		Name:        name,
		Status:      status,
		Observation: observation,
		Unit:        unit,
		Count:       value.count,
		Sum:         &sum,
		Average:     average,
		Required:    require,
	}
}

func gaugeSignal(name, observation, unit string, value gaugeObservation) ComponentSignal {
	status := ComponentSignalNotObserved
	if value.samples > 0 {
		status = ComponentSignalSampled
	}
	current := value.current
	maxObserved := value.max
	return ComponentSignal{
		Name:        name,
		Status:      status,
		Observation: observation,
		Unit:        unit,
		Current:     &current,
		MaxObserved: &maxObserved,
		Samples:     value.samples,
	}
}

func unavailableComponentSignals(require bool) []ComponentSignal {
	signals := componentSignals(nil)
	if !require {
		return signals
	}
	required := make([]ComponentSignal, 0, len(signals))
	for _, signal := range signals {
		next := signal
		next.Required = componentSignalRequiresObservation(next.Name)
		next.Status = ComponentSignalNotObserved
		required = append(required, next)
	}
	return required
}

func componentSignalRequiresObservation(name string) bool {
	switch name {
	case "block_append_sync_latency", "block_sync_batch_size", "metadata_command_sync_latency", "metadata_command_batch_size":
		return true
	default:
		return false
	}
}

func gaugeValue(families []*dto.MetricFamily, name string) gaugeSample {
	family := metricFamily(families, name)
	if family == nil {
		return gaugeSample{}
	}
	for _, metric := range family.GetMetric() {
		if metric.GetGauge() != nil {
			return gaugeSample{value: metric.GetGauge().GetValue(), ok: true}
		}
	}
	return gaugeSample{}
}

func histogramValue(families []*dto.MetricFamily, name string, labels map[string]string) histogramObservation {
	family := metricFamily(families, name)
	if family == nil {
		return histogramObservation{}
	}
	for _, metric := range family.GetMetric() {
		if !metricMatchesLabels(metric, labels) || metric.GetHistogram() == nil {
			continue
		}
		histogram := metric.GetHistogram()
		return histogramObservation{
			count: histogram.GetSampleCount(),
			sum:   histogram.GetSampleSum(),
		}
	}
	return histogramObservation{}
}

func metricFamily(families []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}

func metricMatchesLabels(metric *dto.Metric, labels map[string]string) bool {
	for name, want := range labels {
		found := false
		for _, label := range metric.GetLabel() {
			if label.GetName() == name {
				found = true
				if label.GetValue() != want {
					return false
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}
