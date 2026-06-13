package e2e_test

import "testing"

func TestMetricValueAcceptsPrometheusCounterSuffix(t *testing.T) {
	body := `# HELP scrap_upload_total_total Total backend block upload outcomes.
# TYPE scrap_upload_total_total counter
scrap_upload_total_total{status="success"} 2
`

	value, ok := metricValue(body, "scrap_upload_total", []string{`status="success"`})
	if !ok {
		t.Fatal("metric was not found")
	}
	if value != 2 {
		t.Fatalf("metric value = %v, want 2", value)
	}
}
