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

func TestPrometheusMetrics_RebuildCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := scrub.NewPrometheusMetrics(reg)

	m.RecordRebuild(2.5)
	m.RecordRebuild(1.0)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	found := false
	for _, f := range families {
		if f.GetName() == "scrap_scrub_index_rebuilds_total" {
			found = true
			metric := f.GetMetric()
			if len(metric) == 0 {
				t.Fatal("no metrics for rebuilds counter")
			}
			if metric[0].GetCounter().GetValue() != 2 {
				t.Fatalf("rebuild count: got %f, want 2", metric[0].GetCounter().GetValue())
			}
		}
	}
	if !found {
		t.Fatal("scrap_scrub_index_rebuilds_total not found")
	}
}

func TestPrometheusMetrics_RebuildDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := scrub.NewPrometheusMetrics(reg)

	m.RecordRebuild(2.5)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	for _, f := range families {
		if f.GetName() == "scrap_scrub_index_rebuild_duration_seconds" {
			return
		}
	}
	t.Fatal("scrap_scrub_index_rebuild_duration_seconds not found")
}

func TestDeepScrubPrometheusMetrics_AllRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := scrub.NewDeepScrubPrometheusMetrics(reg)

	m.RecordDeepRun("ok", 1.5)
	m.RecordFramesVerified(100)
	m.RecordCorruption("frame_crc")
	m.RecordQuarantine()
	m.SetBadDiskSuspected(true)
	m.RecordPause()
	m.SetProgressRatio(0.5)
	m.RecordRepair("ok")
	m.RecordRepair("failed")
	m.DecrementQuarantined()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	expected := map[string]bool{
		"scrap_scrub_deep_runs_total":            false,
		"scrap_scrub_frames_verified_total":      false,
		"scrap_scrub_corruptions_detected_total": false,
		"scrap_scrub_quarantines_total":          false,
		"scrap_scrub_blocks_quarantined":         false,
		"scrap_scrub_deep_progress_ratio":        false,
		"scrap_scrub_bad_disk_suspected":         false,
		"scrap_scrub_paused_total":               false,
		"scrap_scrub_deep_duration_seconds":      false,
		"scrap_scrub_repairs_total":              false,
	}

	for _, f := range families {
		if _, ok := expected[f.GetName()]; ok {
			expected[f.GetName()] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("metric %q not found in registry", name)
		}
	}
}
