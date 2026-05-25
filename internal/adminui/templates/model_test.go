package templates

import "testing"

func TestCompactMetricValue(t *testing.T) {
	tests := map[string]string{
		"9,999":      "9,999",
		"12,847":     "12.8K",
		"12,847,293": "12.8M",
		"4.0 KiB":    "4.0 KiB",
		"unknown":    "unknown",
	}

	for input, want := range tests {
		if got := compactMetricValue(input); got != want {
			t.Fatalf("compactMetricValue(%q) = %q, want %q", input, got, want)
		}
	}
}
