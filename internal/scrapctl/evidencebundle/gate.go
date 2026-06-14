package evidencebundle

import (
	"encoding/json"
	"math"
	"strconv"
)

const gateRateScale = 10000

// EvaluateGate evaluates the Tier 3 evidence gate without shell or jq.
func EvaluateGate(input GateInput) Gate {
	stress, ok := parseStressResults(input.StressResults)
	if !ok {
		return Gate{
			Pass:     false,
			Scenario: "unknown",
			Checks: []GateCheck{{
				Name:   "gates_evaluated",
				Pass:   false,
				Reason: "failed to parse stress output",
			}},
		}
	}

	values := gateValuesFromStress(stress)
	metricsOK := input.MetricFailures == 0 && input.MetricEmptyRequired == 0

	return Gate{
		Pass:     gatePasses(values, metricsOK, input),
		Scenario: values.scenario,
		Checks:   gateChecks(values, metricsOK, input),
	}
}

type gateValues struct {
	scenario  string
	completed bool
	writes    float64
	ops       float64
	errs      float64
	rate      float64
}

func parseStressResults(raw string) (map[string]any, bool) {
	var stress map[string]any
	if err := json.Unmarshal([]byte(raw), &stress); err != nil {
		return nil, false
	}
	return stress, true
}

func gateValuesFromStress(stress map[string]any) gateValues {
	scenario := stringValue(stress["scenario"], "unknown")
	ops := opsForScenario(scenario, stress)
	errs := errorsForScenario(scenario, stress)
	rate := 1.0
	if ops > 0 {
		rate = errs / ops
	}
	return gateValues{
		scenario:  scenario,
		completed: stress["error"] == nil,
		writes:    writesForScenario(scenario, stress),
		ops:       ops,
		errs:      errs,
		rate:      rate,
	}
}

func gateChecks(values gateValues, metricsOK bool, input GateInput) []GateCheck {
	return []GateCheck{
		{
			Name:   "stress_completed",
			Pass:   values.completed,
			Reason: reason(values.completed, "stress run completed", "stress run reported error / no JSON"),
		},
		{
			Name:   "writes_nonzero",
			Pass:   values.writes > 0,
			Reason: "writes=" + formatNumber(values.writes),
		},
		{
			Name:   "error_rate_below_1pct",
			Pass:   values.ops > 0 && values.rate < 0.01,
			Reason: "error_rate=" + formatRate(values.rate) + " errors=" + formatNumber(values.errs) + " ops=" + formatNumber(values.ops),
		},
		{
			Name:   "metrics_captured",
			Pass:   metricsOK,
			Reason: formatNumber(float64(input.MetricFailures)) + " query failure(s), " + formatNumber(float64(input.MetricEmptyRequired)) + " required-empty",
		},
		{Name: "traces_captured", Pass: input.TraceOK, Reason: input.TraceReason},
		{Name: "logs_captured", Pass: input.LogOK, Reason: input.LogReason},
		{Name: "cpu_profile_captured", Pass: input.CPUProfileOK, Reason: input.CPUReason},
		{Name: "heap_profile_captured", Pass: input.HeapProfileOK, Reason: input.HeapReason},
		{Name: "security_mode_recorded", Pass: input.SecurityModeOK, Reason: input.SecurityModeReason},
		{Name: "authorization_denials_recorded", Pass: input.AuthzOK, Reason: input.AuthzReason},
		{Name: "audit_samples_recorded", Pass: input.AuditOK, Reason: input.AuditReason},
		{Name: "encryption_outcomes_recorded", Pass: input.EncryptionOK, Reason: input.EncryptionReason},
		{Name: "rewrap_outcomes_recorded", Pass: input.RewrapOK, Reason: input.RewrapReason},
		{Name: "phase5_gate_recorded", Pass: input.Phase5GateOK, Reason: input.Phase5GateReason},
		{Name: "privacy_scan_passed", Pass: input.PrivacyScanOK, Reason: input.PrivacyScanReason},
	}
}

func gatePasses(values gateValues, metricsOK bool, input GateInput) bool {
	return coreGatePasses(values, metricsOK, input) && securityGatePasses(input)
}

func coreGatePasses(values gateValues, metricsOK bool, input GateInput) bool {
	return values.completed &&
		values.writes > 0 &&
		values.ops > 0 &&
		values.rate < 0.01 &&
		metricsOK &&
		input.TraceOK &&
		input.LogOK &&
		input.CPUProfileOK &&
		input.HeapProfileOK
}

func securityGatePasses(input GateInput) bool {
	return input.SecurityModeOK &&
		input.AuthzOK &&
		input.AuditOK &&
		input.EncryptionOK &&
		input.RewrapOK &&
		input.Phase5GateOK &&
		input.PrivacyScanOK
}

func writesForScenario(scenario string, stress map[string]any) float64 {
	switch scenario {
	case "throughput":
		return numberValue(stress["total_ops"])
	case "mixed":
		return nestedNumber(stress, "write", "total_ops")
	case "pressure":
		return numberValue(stress["total_writes"])
	default:
		return 0
	}
}

func opsForScenario(scenario string, stress map[string]any) float64 {
	switch scenario {
	case "throughput":
		return numberValue(stress["total_ops"])
	case "mixed":
		return nestedNumber(stress, "write", "total_ops") +
			nestedNumber(stress, "read", "total_ops") +
			nestedNumber(stress, "head", "total_ops")
	case "pressure":
		return numberValue(stress["total_writes"])
	default:
		return 0
	}
}

func errorsForScenario(scenario string, stress map[string]any) float64 {
	switch scenario {
	case "throughput":
		return numberValue(stress["failed_ops"])
	case "mixed":
		return nestedNumber(stress, "write", "failed_ops") +
			nestedNumber(stress, "read", "failed_ops") +
			nestedNumber(stress, "head", "failed_ops")
	case "pressure":
		return numberValue(stress["other_errors"])
	default:
		return 0
	}
}

func nestedNumber(values map[string]any, parent, child string) float64 {
	nested, ok := values[parent].(map[string]any)
	if !ok {
		return 0
	}
	return numberValue(nested[child])
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}

func stringValue(value any, fallback string) string {
	if s, ok := value.(string); ok && s != "" {
		return s
	}
	return fallback
}

func reason(ok bool, pass, fail string) string {
	if ok {
		return pass
	}
	return fail
}

func formatRate(rate float64) string {
	return formatNumber(math.Floor(rate*gateRateScale) / gateRateScale)
}

func formatNumber(value float64) string {
	if value == math.Trunc(value) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
