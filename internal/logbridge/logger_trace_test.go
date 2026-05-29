package logbridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/petabytecl/scrap/internal/logbridge"
)

func TestNewLoggerInjectsTraceContext(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)

	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	logger.InfoContext(ctx, "write committed", "block_id", 42)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse log line: %v (%q)", err, buf.String())
	}
	if rec["trace_id"] != traceID.String() {
		t.Fatalf("trace_id = %v, want %s", rec["trace_id"], traceID)
	}
	if rec["span_id"] != spanID.String() {
		t.Fatalf("span_id = %v, want %s", rec["span_id"], spanID)
	}
}

func TestNewLoggerNoTraceWithoutSpan(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "info", &buf)
	logger.Info("scrapd starting")
	if strings.Contains(buf.String(), "trace_id") {
		t.Fatalf("a pre-span log must not carry trace_id: %s", buf.String())
	}
}

func TestTraceLevelSuppressedAtDebugAndNamedAtTrace(t *testing.T) {
	var buf bytes.Buffer
	dbg := logbridge.NewLogger("json", "debug", &buf)
	dbg.Log(context.Background(), logbridge.LevelTrace, "frame written")
	if buf.Len() != 0 {
		t.Fatalf("TRACE must be suppressed at debug level: %s", buf.String())
	}

	buf.Reset()
	tr := logbridge.NewLogger("json", "trace", &buf)
	tr.Log(context.Background(), logbridge.LevelTrace, "frame written")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("parse: %v (%q)", err, buf.String())
	}
	if rec["level"] != "TRACE" {
		t.Fatalf("level = %v, want TRACE", rec["level"])
	}
}
