package evidencebundle

import "testing"

func TestEvaluateGateIsScenarioAware(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stress string
	}{
		{
			name:   "throughput",
			stress: `{"scenario":"throughput","total_ops":10,"failed_ops":0}`,
		},
		{
			name:   "mixed",
			stress: `{"scenario":"mixed","write":{"total_ops":10,"failed_ops":0},"read":{"total_ops":5,"failed_ops":0},"head":{"total_ops":5,"failed_ops":0}}`,
		},
		{
			name:   "pressure",
			stress: `{"scenario":"pressure","total_writes":10,"other_errors":0,"pressure_rejections":4}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := EvaluateGate(GateInput{
				StressResults: tc.stress,
				TraceOK:       true,
				TraceReason:   "trace",
				LogOK:         true,
				LogReason:     "log",
				CPUProfileOK:  true,
				CPUReason:     "cpu",
				HeapProfileOK: true,
				HeapReason:    "heap",
			})
			if !gate.Pass {
				t.Fatalf("gate pass = false, checks = %+v", gate.Checks)
			}
			assertBundleCheck(t, gate, "writes_nonzero", true)
			assertBundleCheck(t, gate, "error_rate_below_1pct", true)
		})
	}
}

func TestEvaluateGateFailsInvalidStressOutput(t *testing.T) {
	gate := EvaluateGate(GateInput{StressResults: `not-json`})

	if gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	assertBundleCheck(t, gate, "gates_evaluated", false)
}
