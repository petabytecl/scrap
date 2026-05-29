package logbridge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// LevelTrace is a custom slog level below Debug for very high-volume per-frame /
// per-chunk tracing. SCRAP_LOG_LEVEL=trace enables it; debug does not. See the
// logging conventions in docs/go-style-guide.md and ADR 0013.
const LevelTrace = slog.Level(-8)

func parseLevel(s string) slog.Level {
	if s == "" {
		return slog.LevelInfo
	}
	normalized := strings.ToLower(s)
	switch normalized {
	case "trace":
		return LevelTrace
	case "warning":
		normalized = "warn"
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(normalized)); err != nil {
		fmt.Fprintf(os.Stderr, "scrapd: unknown SCRAP_LOG_LEVEL %q, defaulting to INFO\n", s)
		return slog.LevelInfo
	}
	return lvl
}

// levelReplacer renders the custom TRACE level by name so log processors see
// "TRACE" rather than slog's default "DEBUG-4"; standard levels are unchanged.
func levelReplacer(_ []string, a slog.Attr) slog.Attr {
	if a.Key != slog.LevelKey {
		return a
	}
	if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == LevelTrace {
		a.Value = slog.StringValue("TRACE")
	}
	return a
}

// NewLogger creates a *slog.Logger with the given format and level. Records logged
// with a context carrying an active span are enriched with trace_id/span_id, so the
// stdout-JSON logs correlate with traces in Grafana (Tempo <-> Loki).
// format: "json" (default) | "text" (logfmt)
// level: "trace" | "debug" | "info" (default) | "warn" | "error"
func NewLogger(format, level string, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level), ReplaceAttr: levelReplacer}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(&traceContextHandler{Handler: handler})
}

// NewLoggerFromEnv creates a *slog.Logger reading SCRAP_LOG_FORMAT and
// SCRAP_LOG_LEVEL from environment variables.
func NewLoggerFromEnv(w io.Writer) *slog.Logger {
	return NewLogger(os.Getenv("SCRAP_LOG_FORMAT"), os.Getenv("SCRAP_LOG_LEVEL"), w)
}

// traceContextHandler enriches every record with trace_id/span_id from the
// context's active span, when present. Output stays stdout-JSON per CONTEXT.md (no
// OTLP push, so request handling never blocks on log-export backpressure); the
// filelog agent ships it to Loki. See ADR 0013.
type traceContextHandler struct {
	slog.Handler
}

func (h *traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	if err := h.Handler.Handle(ctx, r); err != nil {
		return fmt.Errorf("logbridge: handle record: %w", err)
	}
	return nil
}

func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{Handler: h.Handler.WithGroup(name)}
}
