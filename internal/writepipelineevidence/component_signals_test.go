package writepipelineevidence

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestGaugeValueDistinguishesMissingMetricFromZeroGauge(t *testing.T) {
	zero := 0.0
	families := []*dto.MetricFamily{
		{
			Name: stringPtr("scrap_block_append_queue_depth"),
			Metric: []*dto.Metric{
				{Gauge: &dto.Gauge{Value: &zero}},
			},
		},
	}

	present := gaugeValue(families, "scrap_block_append_queue_depth")
	if !present.ok {
		t.Fatal("zero gauge was reported missing")
	}
	if present.value != 0 {
		t.Fatalf("zero gauge value = %f, want 0", present.value)
	}

	missing := gaugeValue(families, "scrap_missing_queue_depth")
	if missing.ok {
		t.Fatalf("missing gauge = %#v, want ok=false", missing)
	}
}

func TestUnavailableComponentSignalsRequireOnlyHistograms(t *testing.T) {
	signals := unavailableComponentSignals(true)
	for _, signal := range signals {
		switch signal.Name {
		case "block_append_queue_depth", "raft_queue_depth":
			if signal.Required {
				t.Fatalf("%s required = true, want false for sampled gauge signals", signal.Name)
			}
		default:
			if !signal.Required {
				t.Fatalf("%s required = false, want true", signal.Name)
			}
		}
	}
}

func stringPtr(value string) *string {
	return &value
}
