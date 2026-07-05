package evidencebundle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type metricSample struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

const metricSampleValueLen = 2

type mimirEnvelope struct {
	Data *struct {
		Result []metricSample `json:"result"`
	} `json:"data"`
}

func queryMetricSamples(ctx context.Context, client *http.Client, baseURL, query string, epoch int64) ([]metricSample, bool) {
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/query"
	values := url.Values{}
	values.Set("query", query)
	values.Set("time", strconv.FormatInt(epoch, 10))
	body, ok := getJSON(ctx, client, endpoint, values)
	if !ok {
		return nil, false
	}
	var envelope mimirEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return nil, false
	}
	return envelope.Data.Result, true
}

func rpcCounterDelta(before, after []metricSample, at int64) []metricSample {
	beforeByKey := make(map[string]float64, len(before))
	for _, sample := range before {
		beforeByKey[seriesKey(sample)] = sampleValue(sample)
	}
	out := make([]metricSample, 0, len(after))
	for _, sample := range after {
		afterValue := sampleValue(sample)
		beforeValue := beforeByKey[seriesKey(sample)]
		delta := afterValue - beforeValue
		if delta < 0 {
			delta = afterValue
		}
		if delta <= 0 {
			continue
		}
		out = append(out, metricSample{
			Metric: cloneMetric(sample.Metric),
			Value:  []any{at, strconv.FormatFloat(delta, 'f', -1, 64)},
		})
	}
	return out
}

func seriesKey(sample metricSample) string {
	return sample.Metric["rpc_method"] + "\x00" + sample.Metric["rpc_grpc_status_code"]
}

func cloneMetric(metric map[string]string) map[string]string {
	out := make(map[string]string, len(metric))
	for key, value := range metric {
		out[key] = value
	}
	return out
}

func sampleValue(sample metricSample) float64 {
	if len(sample.Value) < metricSampleValueLen {
		return 0
	}
	switch value := sample.Value[1].(type) {
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0
		}
		return parsed
	case float64:
		return value
	default:
		return 0
	}
}

func samplesHavePositiveValue(samples []metricSample) bool {
	for _, sample := range samples {
		if sampleValue(sample) > 0 {
			return true
		}
	}
	return false
}

func captureTraceEvidence(ctx context.Context, client *http.Client, cfg Config, dir string, window runWindow) (bool, string, error) {
	values := url.Values{}
	values.Set("tags", "service.name=scrapd")
	values.Set("start", strconv.FormatInt(window.StartUnixSeconds, 10))
	values.Set("end", strconv.FormatInt(window.QueryUnixSeconds, 10))
	values.Set("limit", "20")
	body, ok := getJSON(ctx, client, strings.TrimRight(cfg.TempoProxy, "/")+"/api/search", values)
	if !ok || !jsonHasKey(body, "traces") {
		body = []byte(`{"traces":[]}`)
	}
	count := traceCount(body)
	summary := map[string]any{
		"query":       "service.name=scrapd",
		"trace_count": count,
		"redacted":    true,
	}
	if err := writeJSONFile(filepath.Join(dir, "scrapd.json"), summary); err != nil {
		return false, "", err
	}
	if count > 0 {
		return true, strconv.Itoa(count) + " trace(s) found for service.name=scrapd", nil
	}
	return false, "no Tempo traces found for service.name=scrapd in current run window", nil
}

func captureLogEvidence(ctx context.Context, client *http.Client, cfg Config, dir string, window runWindow, marker string, probeFailed bool) (bool, string, error) {
	values := url.Values{}
	values.Set("query", `{service_name="scrapd"} |= "`+marker+`"`)
	values.Set("start", strconv.FormatInt(window.StartUnixSeconds*1_000_000_000, 10))
	values.Set("end", strconv.FormatInt(window.QueryUnixSeconds*1_000_000_000, 10))
	values.Set("limit", "20")
	values.Set("direction", "forward")
	body, ok := getJSON(ctx, client, strings.TrimRight(cfg.LokiProxy, "/")+"/loki/api/v1/query_range", values)
	if !ok || !jsonHasNestedKeys(body, "data", "result") {
		body = []byte(`{"status":"error","data":{"result":[]}}`)
	}
	if probeFailed {
		if err := writeLogEvidence(dir, marker, body, 0); err != nil {
			return false, "", err
		}
		return false, "admin evidence log probe failed before Loki query", nil
	}
	count := logLineCount(body)
	if err := writeLogEvidence(dir, marker, body, count); err != nil {
		return false, "", err
	}
	if count > 0 {
		return true, strconv.Itoa(count) + " log line(s) found for evidence marker " + marker, nil
	}
	return false, "no Loki log line found for evidence marker " + marker + " in current run window", nil
}

func writeLogEvidence(dir, marker string, body []byte, count int) error {
	evidence := map[string]any{
		"query":     `{service_name="scrapd"} |= "` + marker + `"`,
		"log_count": count,
		"redacted":  true,
		"marker":    marker,
		"response":  redactedLogResponse(body),
	}
	return writeJSONFile(filepath.Join(dir, "scrapd.json"), evidence)
}

func redactedLogResponse(body []byte) any {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return map[string]any{
			"status":      "error",
			"parse_error": "invalid_json",
		}
	}
	return redactLogValue(value)
}

func redactLogValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if shouldRedactLogField(key, child) {
				continue
			}
			out[key] = redactLogValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactLogValue(child)
		}
		return out
	case string:
		if containsSensitiveLogString(typed) {
			return "redacted"
		}
		return typed
	default:
		return value
	}
}

func shouldRedactLogField(key string, value any) bool {
	lowerKey := strings.ToLower(key)
	if lowerKey == "log_file_path" || lowerKey == "filename" || lowerKey == "file_path" || lowerKey == "__path__" {
		return true
	}
	if lowerKey == "path" {
		if text, ok := value.(string); ok {
			return containsSensitiveLogString(text)
		}
	}
	return false
}

func containsSensitiveLogString(text string) bool {
	for _, root := range sensitiveHostPathRoots {
		if strings.Contains(text, "/"+root+"/") {
			return true
		}
	}
	return false
}

func captureCPUProfile(ctx context.Context, client *http.Client, cfg Config, dir string, window runWindow) (bool, string, error) {
	ok, err := queryProfile(ctx, client, cfg.PyroscopeURL, dir, "cpu", `process_cpu:cpu:nanoseconds:cpu:nanoseconds{service_name="scrapd"}`, window)
	if err != nil {
		return false, "", err
	}
	if ok {
		return true, "CPU profile data found for service_name=scrapd", nil
	}
	return false, "no Pyroscope CPU profile data found for service_name=scrapd in current run window", nil
}

func captureHeapProfile(ctx context.Context, client *http.Client, cfg Config, dir string, window runWindow) (bool, string, error) {
	queries := []struct {
		name  string
		query string
	}{
		{name: "heap_inuse_space", query: `memory:inuse_space:bytes:space:bytes{service_name="scrapd"}`},
		{name: "heap_alloc_space", query: `memory:alloc_space:bytes:space:bytes{service_name="scrapd"}`},
	}
	for _, query := range queries {
		ok, err := queryProfile(ctx, client, cfg.PyroscopeURL, dir, query.name, query.query, window)
		if err != nil {
			return false, "", err
		}
		if ok {
			return true, "heap profile data found via " + query.name + " for service_name=scrapd", nil
		}
	}
	return false, "no Pyroscope heap profile data found for service_name=scrapd in current run window", nil
}

func queryProfile(ctx context.Context, client *http.Client, baseURL, dir, name, query string, window runWindow) (bool, error) {
	values := url.Values{}
	values.Set("query", query)
	values.Set("from", strconv.FormatInt(window.StartUnixSeconds, 10))
	values.Set("until", strconv.FormatInt(window.QueryUnixSeconds, 10))
	values.Set("format", "json")
	values.Set("maxNodes", "64")
	body, ok := getJSON(ctx, client, strings.TrimRight(baseURL, "/")+"/pyroscope/render", values)
	if !ok || !isJSON(body) {
		body = []byte(`{"timeline":{"samples":[]}}`)
	}
	if err := writeRawJSONFile(filepath.Join(dir, name+".json"), body); err != nil {
		return false, err
	}
	return profileHasData(body), nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, values url.Values) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return nil, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	return body, true
}

func isJSON(body []byte) bool {
	var value any
	return json.Unmarshal(body, &value) == nil
}

func jsonHasKey(body []byte, key string) bool {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return false
	}
	_, ok := value[key]
	return ok
}

func jsonHasNestedKeys(body []byte, parent, child string) bool {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return false
	}
	nested, ok := value[parent].(map[string]any)
	if !ok {
		return false
	}
	_, ok = nested[child]
	return ok
}

func traceCount(body []byte) int {
	var value struct {
		Traces []any `json:"traces"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return 0
	}
	return len(value.Traces)
}

func logLineCount(body []byte) int {
	var value struct {
		Data struct {
			Result []struct {
				Values []any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return 0
	}
	count := 0
	for _, result := range value.Data.Result {
		count += len(result.Values)
	}
	return count
}

func profileHasData(body []byte) bool {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return false
	}
	if nestedLen(value, "timeline", "samples") > 0 {
		return true
	}
	if nestedLen(value, "flamebearer", "levels") > 0 {
		return true
	}
	groups, ok := value["groups"].(map[string]any)
	return ok && len(groups) > 0
}

func nestedLen(value map[string]any, parent, child string) int {
	nested, ok := value[parent].(map[string]any)
	if !ok {
		return 0
	}
	items, ok := nested[child].([]any)
	if !ok {
		return 0
	}
	return len(items)
}

func httpError(status int, body string) error {
	return fmt.Errorf("status: %d: %s", status, strings.TrimSpace(body))
}
