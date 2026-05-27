package scrub_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/petabytecl/scrap/internal/scrub"
)

//nolint:gocognit,cyclop // iterating nested prometheus proto structures in a test
func TestPrometheusMetrics_RegistersRunsCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := scrub.NewPrometheusMetrics(reg)

	m.RecordRun("ok", 1.5)
	m.RecordRun("ok", 2.0)
	m.RecordRun("mismatch", 3.0)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	for _, f := range families {
		if f.GetName() != "scrap_scrub_light_runs_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() != "result" {
					continue
				}
				switch l.GetValue() {
				case "ok":
					if m.GetCounter().GetValue() != 2 {
						t.Fatalf("ok count: got %f, want 2", m.GetCounter().GetValue())
					}
				case "mismatch":
					if m.GetCounter().GetValue() != 1 {
						t.Fatalf("mismatch count: got %f, want 1", m.GetCounter().GetValue())
					}
				}
			}
		}
		return
	}
	t.Fatal("scrap_scrub_light_runs_total not found in gathered metrics")
}

func TestPrometheusMetrics_RegistersDurationHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = scrub.NewPrometheusMetrics(reg)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	for _, f := range families {
		if f.GetName() == "scrap_scrub_light_duration_seconds" {
			return
		}
	}
	t.Fatal("scrap_scrub_light_duration_seconds not found in gathered metrics")
}
